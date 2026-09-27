import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type ClusterRow, type NodeNetwork, type NodeSpec } from '../../api'
import { KVEditor } from '../../components/KVEditor'
import { StaticNetworkFields } from '../../components/StaticNetworkFields'
import { Dialog, ErrorBox, Field } from '../../components/ui'
import { dataCandidates, diskLabel, installCandidates, modelOf, typeOf } from '../../machine'
import { nextHostname, staticNetwork } from '../../net'
import { watch } from '../../ops'
import { machineList } from '../../store'

export function AddNodeDialog({ cluster, onClose, preselect }: { cluster: ClusterRow; onClose: () => void; preselect?: string }) {
  const pools = cluster.spec.spec.pools ?? []
  const [mac, setMac] = useState('')
  const [hostname, setHostname] = useState<string | null>(null)
  const [pool, setPool] = useState(pools.find((p) => p.role === 'worker')?.name ?? pools[0]?.name ?? 'worker')
  const [disk, setDisk] = useState('')
  const [labels, setLabels] = useState<Record<string, string> | undefined>()
  const [taints, setTaints] = useState<Record<string, string> | undefined>()
  const [network, setNetwork] = useState<NodeNetwork | undefined>()
  const [more, setMore] = useState(false)
  const [dataDisks, setDataDisks] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const candidates = machineList.value.filter((n) => n.state === 'maintenance' && !n.cluster)
  const selected = candidates.find((c) => (mac ? c.mac === mac : !!preselect && c.ip === preselect))
  const p = pools.find((x) => x.name === pool)
  const role = p?.role ?? 'worker'
  const nodes = cluster.spec.spec.nodes
  const name = hostname ?? (selected ? nextHostname(cluster.name, pool, nodes.filter((x) => x.pool === pool).length, nodes.map((x) => x.hostname)) : '')
  useEffect(() => {
    if (!selected) return
    setDisk(installCandidates(selected)[0]?.devPath ?? '')
    setDataDisks([])
  }, [selected?.mac])
  const submit = () => {
    if (!selected) return
    const node: NodeSpec = { hostname: name, ip: selected.ip, mac: selected.mac, uuid: selected.uuid, pool, arch: selected.arch, kvm: !!selected.inventory?.kvm, installDisk: disk ? { path: disk } : undefined, dataDisks: dataDisks.length ? dataDisks : undefined, labels, taints, network }
    api.addNode(cluster.name, node).then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  }
  const cps = cluster.spec.spec.nodes.filter((x) => x.role === 'controlplane').length
  const staticOn = !!network
  const count = (m?: Record<string, string>) => Object.keys(m ?? {}).length
  const poolHint = role === 'controlplane' ? `etcd goes from ${cps} to ${cps + 1} members${(cps + 1) % 2 === 0 ? '; an even count adds no fault tolerance' : ''}` : p ? [count(p.labels) ? `${count(p.labels)} pool label(s)` : '', count(p.taints) ? `${count(p.taints)} taint(s)` : '', p.extensions?.length ? 'own installer image' : ''].filter(Boolean).join(', ') || 'plain worker' : undefined
  return (
    <Dialog title={`Add node to ${cluster.name}`} width="max-w-2xl" onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class="btn btn-primary" disabled={!selected || !name || (!disk && !p?.installDisk)} onClick={submit}>Install and join</button>
      </>
    }>
      <ErrorBox error={error} />
      <Field label="Discovered machine (maintenance mode)" hint={candidates.length === 0 ? 'None unassigned; scan in Inventory first' : undefined}>
        <select class="input" value={selected?.mac ?? ''} onChange={(e) => setMac((e.target as HTMLSelectElement).value)}>
          <option value="">Select</option>
          {candidates.map((c) => <option key={c.mac} value={c.mac}>{modelOf(c)} ({typeOf(c)}) · {c.ip} · {c.arch} · {c.inventory?.cpus ?? '?'} CPU · {fmt.bytes(c.inventory?.memoryBytes ?? 0)}{c.inventory?.kvm ? ' · kvm' : ''} · {c.mac}</option>)}
        </select>
      </Field>
      <div class="grid grid-cols-2 gap-3">
        <Field label="Pool" hint={poolHint}>
          <select class="input" value={pool} onChange={(e) => setPool((e.target as HTMLSelectElement).value)}>
            {pools.map((x) => <option key={x.name} value={x.name}>{x.name}{x.role === 'controlplane' ? ' (control plane)' : ''}</option>)}
          </select>
        </Field>
        <Field label="Hostname"><input class="input mono" value={name} onInput={(e) => setHostname((e.target as HTMLInputElement).value)} /></Field>
      </div>
      <Field label="Install disk" hint={p?.installDisk ? 'Empty follows the pool policy' : 'Wiped and installed with Talos'}>
        <select class="input mono" value={disk} onChange={(e) => { const v = (e.target as HTMLSelectElement).value; setDisk(v); setDataDisks(dataDisks.filter((d) => d !== v)) }}>
          {p?.installDisk && <option value="">pool policy ({Object.values(p.installDisk.selector ?? {}).join(' ')})</option>}
          {installCandidates(selected).map((d) => <option key={d.devPath} value={d.devPath}>{diskLabel(d)}</option>)}
          {!selected && <option value="">—</option>}
        </select>
      </Field>
      {selected && dataCandidates(selected, disk).length > 0 && (
        <Field label="Data disks" hint="Wiped and mounted at /var/mnt/data-N">
          <div class="flex flex-col gap-1 text-[13px] mono">
            {dataCandidates(selected, disk).map((d) => <label key={d.devPath} class="flex items-center gap-2"><input type="checkbox" checked={dataDisks.includes(d.devPath)} onChange={(e) => setDataDisks((e.target as HTMLInputElement).checked ? dataCandidates(selected, disk).map((x) => x.devPath).filter((x) => x === d.devPath || dataDisks.includes(x)) : dataDisks.filter((x) => x !== d.devPath))} />{d.devPath} <span class="text-muted">{fmt.bytes(d.sizeBytes)}{d.model ? ` · ${d.model}` : ''}</span></label>)}
          </div>
        </Field>
      )}
      <button class="text-[12px] text-accent text-left hover:underline" onClick={() => setMore(!more)}>{more ? '▾' : '▸'} Labels, taints and addressing</button>
      {more && (
        <div class="grid grid-cols-2 gap-3">
          <Field label="Extra labels" hint="key=value per line, over the pool's"><KVEditor value={labels} onChange={setLabels} /></Field>
          <Field label="Extra taints" hint="key=value:Effect per line"><KVEditor value={taints} onChange={setTaints} /></Field>
          <Field label="Addressing" hint={staticOn ? 'Pinned in the machine config' : 'DHCP; tracked by MAC'}>
            <select class="input" value={staticOn ? 'static' : 'dhcp'} onChange={(e) => setNetwork((e.target as HTMLSelectElement).value === 'static' && selected ? staticNetwork(selected.ip) : undefined)}>
              <option value="dhcp">DHCP</option>
              <option value="static">Static</option>
            </select>
          </Field>
        </div>
      )}
      {more && network && <StaticNetworkFields value={network} lease={selected?.ip} onChange={setNetwork} />}
      <p class="text-[12px] text-muted">Installs Talos and joins the node; progress in Activity.</p>
    </Dialog>
  )
}
