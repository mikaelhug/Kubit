import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type ClusterRow, type NodeNetwork, type NodeSpec, type NodeStatus } from '../../api'
import { machineList, toast, watch } from '../../store'
import { DataTable, type Column } from '../../components/DataTable'
import { ConfirmDialog, Dialog, ErrorBox, Field, MaintenanceNotice, Pill, Section } from '../../components/ui'
import { KVEditor } from '../../components/PoolsEditor'
import { guessGateway } from '../create/net'
import { dataCandidates, installCandidates, isVirtual, modelOf } from '../create/steps'
import type { ClusterCtx } from './ClusterPage'

export function Nodes({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const adoptIP = typeof location !== 'undefined' ? new URLSearchParams(location.search).get('adopt') : null
  const [add, setAdd] = useState(!!adoptIP)
  const [remove, setRemove] = useState<NodeStatus | null>(null)
  const [readdress, setReaddress] = useState<NodeStatus | null>(null)
  const [pool, setPool] = useState('')
  const specs = cluster.spec.spec.nodes
  const all: NodeStatus[] = status?.nodes ?? specs.map((n) => ({ ...n, role: n.role ?? 'worker', pool: n.pool ?? '', kvm: !!n.kvm, ready: false, unschedulable: false, registered: false, stage: '', talosVersion: '', kubeletVersion: '', cpuMilli: 0, cpuCapMilli: 0, memBytes: 0, memCapBytes: 0, pods: 0, podCap: 0, gvisor: false, talosReachable: false, talosError: 'querying…' }))
  const rows = pool ? all.filter((n) => n.pool === pool) : all
  const pools = cluster.spec.spec.pools ?? []
  const apiUp = !!status?.apiReachable
  const specOf = (n: NodeStatus) => specs.find((s) => s.hostname === n.hostname)

  const columns: Column<NodeStatus>[] = [
    { id: 'hostname', header: 'Hostname', sort: (n) => n.hostname, cell: (n) => n.talosReachable ? <a href={`/nodes/${n.ip}`} class="font-medium hover:underline">{n.hostname}</a> : <span class="font-medium">{n.hostname}</span> },
    { id: 'ip', header: 'Address', sort: (n) => n.ip, mono: true, text: (n) => `${n.ip} ${n.seenAt ?? ''}`, cell: (n) => {
      const sp = specOf(n)
      const moved = n.seenAt && n.seenAt !== n.ip
      return (
        <div class="flex flex-col">
          <span>{n.ip} <span class="text-[10px] text-muted">{sp?.network ? (sp.network.vlan ? `static · vlan ${sp.network.vlan}` : 'static') : 'dhcp'}</span></span>
          {moved && <button class="text-[11px] text-warn hover:underline text-left" title={`Discovery last saw this machine (by MAC) at ${n.seenAt}; the declaration still says ${n.ip}.`} onClick={() => setReaddress(n)}>seen at {n.seenAt} — update</button>}
        </div>
      )
    } },
    { id: 'pool', header: 'Pool', sort: (n) => n.pool, cell: (n) => <span class="flex items-center gap-1.5"><span class="mono">{n.pool || '—'}</span><span class="text-[10px] text-muted">{n.role === 'controlplane' ? 'control plane' : 'worker'}</span></span> },
    { id: 'status', header: 'Status', sort: (n) => (n.talosReachable ? 1 : 0) + (n.ready ? 2 : 0), text: (n) => `${n.talosReachable ? '' : 'unreachable'} ${n.ready ? 'ready' : 'notready'}`, cell: (n) => <NodeHealth n={n} apiReachable={apiUp} /> },
    { id: 'talos', header: 'Talos', sort: (n) => n.talosVersion, mono: true, cell: (n) => n.talosVersion || '—' },
    { id: 'kubelet', header: 'Kubelet', sort: (n) => n.kubeletVersion, mono: true, cell: (n) => n.kubeletVersion || '—' },
    { id: 'cpu', header: 'CPU', align: 'right', sort: (n) => n.cpuMilli, cell: (n) => <>{fmt.cores(n.cpuMilli)}<span class="text-muted">/{fmt.cores(n.cpuCapMilli)}</span></> },
    { id: 'ram', header: 'RAM', align: 'right', sort: (n) => n.memBytes, cell: (n) => <>{fmt.bytes(n.memBytes)}<span class="text-muted">/{fmt.bytes(n.memCapBytes)}</span></> },
    { id: 'pods', header: 'Pods', align: 'right', sort: (n) => n.pods, cell: (n) => n.pods },
    { id: 'gvisor', header: 'gVisor', sort: (n) => n.gvisor ? 1 : 0, cell: (n) => n.gvisor ? <Pill tone="good">{n.kvm ? 'kvm' : 'runsc'}</Pill> : <span class="text-muted">—</span> },
    { id: 'actions', header: '', align: 'right', cell: (n) => (
      <span class="whitespace-nowrap flex gap-1 justify-end">
        <a href={`/nodes/${n.ip}`} class={`btn !py-1 ${n.talosReachable ? '' : 'pointer-events-none opacity-50'}`} title={n.talosReachable ? 'Services, logs, hardware' : `Talos API not answering: ${n.talosError}`}>Open</a>
        <button class="btn btn-danger !py-1" disabled={!apiUp} title={!apiUp ? 'Needs the Kubernetes API to drain' : 'Drain, delete and reset'} onClick={() => setRemove(n)}>Remove</button>
      </span>
    ) },
  ]

  return (
    <>
      <Section title="Nodes" help="Talos reachability and Kubernetes readiness are shown separately: a machine can answer Talos while its kubelet is not registered, and vice versa. Machines are tracked by MAC; a node whose DHCP lease moved shows where it was last seen."
        actions={<>
          {pools.length > 2 || pool ? (
            <select class="input !py-1 w-auto" value={pool} onChange={(e) => setPool((e.target as HTMLSelectElement).value)} aria-label="Filter by pool">
              <option value="">All pools</option>
              {pools.map((p) => <option key={p.name} value={p.name}>{p.name} ({specs.filter((s) => s.pool === p.name).length})</option>)}
            </select>
          ) : null}
          <button class="btn btn-primary" onClick={() => setAdd(true)}>+ Add node</button>
        </>}>
        <DataTable id="nodes" columns={columns} rows={rows} rowKey={(n) => n.hostname} defaultSort={{ id: 'role', dir: 'asc' }} />
      </Section>
      {add && <AddNodeDialog cluster={cluster} preselect={adoptIP ?? undefined} onClose={() => { setAdd(false); if (adoptIP) history.replaceState(null, '', location.pathname) }} />}
      {readdress && <ReaddressDialog cluster={cluster} n={readdress} spec={specOf(readdress)} onClose={() => setReaddress(null)} />}
      {remove && (
        <ConfirmDialog title={`Remove ${remove.hostname}`} action="Drain and remove" tone="danger" cluster={name} onClose={() => setRemove(null)}
          onConfirm={() => api.removeNode(name, remove.hostname).then((r) => { setRemove(null); watch(r) }).catch((e) => toast(e.message, 'error'))}
          impact={<RemoveImpact n={remove} cluster={cluster} />} />
      )}
    </>
  )
}

/** One pill per fact; the reason travels in the tooltip and inline when unreachable. */
export function NodeHealth({ n, apiReachable }: { n: NodeStatus; apiReachable: boolean }) {
  const pills = []
  if (!n.talosReachable) pills.push(<Pill tone="bad" title={n.talosError}>Talos unreachable</Pill>)
  else if (n.stage && n.stage !== 'running') pills.push(<Pill tone="warn">{n.stage}</Pill>)
  if (!apiReachable) pills.push(<Pill tone="muted">k8s unknown</Pill>)
  else if (!n.registered) pills.push(<Pill tone="warn" title="No Node object: the kubelet has not registered with the API server">not registered</Pill>)
  else if (!n.ready) pills.push(<Pill tone="warn">NotReady</Pill>)
  else pills.push(<Pill tone="good">Ready</Pill>)
  if (n.unschedulable) pills.push(<Pill tone="warn">cordoned</Pill>)
  return (
    <div class="flex flex-col gap-0.5">
      <span class="inline-flex flex-wrap gap-1">{pills}</span>
      {!n.talosReachable && n.talosError && <span class="text-[11px] text-muted max-w-[260px] truncate" title={n.talosError}>{n.talosError}</span>}
    </div>
  )
}

function RemoveImpact({ n, cluster }: { n: NodeStatus; cluster: ClusterRow }) {
  const cps = cluster.spec.spec.nodes.filter((x) => x.role === 'controlplane').length
  const remaining = n.role === 'controlplane' ? cps - 1 : cps
  return (
    <ul class="list-disc pl-5 flex flex-col gap-1">
      <li>Cordon and drain <b>{n.pods}</b> running pod{n.pods === 1 ? '' : 's'} (DaemonSet pods stay).</li>
      <li>Delete the Node object from Kubernetes.</li>
      <li>Talos <b>reset</b>: {n.role === 'controlplane' ? 'leave etcd, ' : ''}wipe state and reboot into maintenance mode. The machine becomes a candidate again.</li>
      {n.role === 'controlplane' && remaining === 0 && <li class="text-bad">This is the last control plane: Kubit will refuse.</li>}
      {n.role === 'controlplane' && remaining === 2 && <li class="text-warn">Leaves 2 control planes: etcd survives no further failure. Kubit refuses unless forced from the CLI.</li>}
      {n.role === 'controlplane' && remaining >= 3 && <li>etcd keeps quorum with {remaining} members.</li>}
      <li>cluster.yaml is updated; the platform layer is left as is.</li>
    </ul>
  )
}

function AddNodeDialog({ cluster, onClose, preselect }: { cluster: ClusterRow; onClose: () => void; preselect?: string }) {
  const pools = cluster.spec.spec.pools ?? []
  const [ip, setIp] = useState(preselect ?? '')
  const [hostname, setHostname] = useState('')
  const [pool, setPool] = useState(pools.find((p) => p.role === 'worker')?.name ?? pools[0]?.name ?? 'worker')
  const [disk, setDisk] = useState('')
  const [labels, setLabels] = useState<Record<string, string> | undefined>()
  const [taints, setTaints] = useState<Record<string, string> | undefined>()
  const [network, setNetwork] = useState<NodeNetwork | undefined>()
  const [more, setMore] = useState(false)
  const [dataDisks, setDataDisks] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const candidates = machineList.value.filter((n) => n.state === 'maintenance' && !n.cluster)
  const selected = candidates.find((c) => c.ip === ip)
  const p = pools.find((x) => x.name === pool)
  const role = p?.role ?? 'worker'
  useEffect(() => {
    if (!selected) return
    const cand = installCandidates(selected)
    if (cand[0]) setDisk(cand[0].devPath)
    setDataDisks([])
    const n = cluster.spec.spec.nodes.filter((x) => x.pool === pool).length + 1
    setHostname(`${cluster.name}-${pool === 'controlplane' ? 'cp' : pool}-${String(n).padStart(2, '0')}`)
  }, [selected, pool, cluster])
  const submit = () => {
    if (!selected) return
    const node: NodeSpec = { hostname, ip, mac: selected.mac, uuid: selected.uuid, pool, arch: selected.arch, kvm: !!selected.inventory?.kvm, installDisk: disk ? { path: disk } : undefined, dataDisks: dataDisks.length ? dataDisks : undefined, labels, taints, network }
    api.addNode(cluster.name, node).then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  }
  const cps = cluster.spec.spec.nodes.filter((x) => x.role === 'controlplane').length
  const staticOn = !!network
  return (
    <Dialog title={`Add node to ${cluster.name}`} width="max-w-2xl" onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class="btn btn-primary" disabled={!selected || !hostname || (!disk && !p?.installDisk)} onClick={submit}>Install and join</button>
      </>
    }>
      <ErrorBox error={error} />
      <Field label="Discovered machine (maintenance mode)" hint={candidates.length === 0 ? 'No unassigned machines. Run a discovery under Fleet → Inventory first.' : undefined}>
        <select class="input" value={ip} onChange={(e) => setIp((e.target as HTMLSelectElement).value)}>
          <option value="">Select…</option>
          {candidates.map((c) => <option key={c.mac} value={c.ip}>{modelOf(c)} ({isVirtual(c) ? 'VM' : 'metal'}) · {c.ip} · {c.arch} · {c.inventory?.cpus ?? '?'} CPU · {fmt.bytes(c.inventory?.memoryBytes ?? 0)}{c.inventory?.kvm ? ' · kvm' : ''} · {c.mac}</option>)}
        </select>
      </Field>
      <div class="grid grid-cols-2 gap-3">
        <Field label="Pool" hint={role === 'controlplane' ? `etcd goes from ${cps} to ${cps + 1} members${(cps + 1) % 2 === 0 ? ' — an even count adds no fault tolerance' : ''}` : p ? [Object.keys(p.labels ?? {}).length ? `${Object.keys(p.labels ?? {}).length} pool label(s)` : '', Object.keys(p.taints ?? {}).length ? `${Object.keys(p.taints ?? {}).length} taint(s)` : '', p.extensions?.length ? 'own installer image' : ''].filter(Boolean).join(', ') || 'plain worker' : undefined}>
          <select class="input" value={pool} onChange={(e) => setPool((e.target as HTMLSelectElement).value)}>
            {pools.map((x) => <option key={x.name} value={x.name}>{x.name}{x.role === 'controlplane' ? ' (control plane)' : ''}</option>)}
          </select>
        </Field>
        <Field label="Hostname"><input class="input mono" value={hostname} onInput={(e) => setHostname((e.target as HTMLInputElement).value)} /></Field>
      </div>
      <Field label="Install disk" hint={p?.installDisk ? 'Empty follows the pool\'s disk policy.' : 'Wiped and installed with Talos.'}>
        <select class="input mono" value={disk} onChange={(e) => { const v = (e.target as HTMLSelectElement).value; setDisk(v); setDataDisks(dataDisks.filter((d) => d !== v)) }}>
          {p?.installDisk && <option value="">pool policy ({Object.values(p.installDisk.selector ?? {}).join(' ')})</option>}
          {selected?.inventory?.disks.filter((d) => !d.readonly && !d.cdrom).map((d) => <option key={d.devPath} value={d.devPath}>{d.devPath} · {fmt.bytes(d.sizeBytes)} {d.model ? `· ${d.model}` : ''} {d.transport ? `· ${d.transport}` : ''}</option>)}
          {!selected && <option value="">—</option>}
        </select>
      </Field>
      {selected && dataCandidates(selected, disk).length > 0 && (
        <Field label="Data disks" hint="Wiped, formatted (xfs) and mounted at /var/mnt/data-N for node-local storage.">
          <div class="flex flex-col gap-1 text-[13px] mono">
            {dataCandidates(selected, disk).map((d) => <label key={d.devPath} class="flex items-center gap-2"><input type="checkbox" checked={dataDisks.includes(d.devPath)} onChange={(e) => setDataDisks((e.target as HTMLInputElement).checked ? dataCandidates(selected, disk).map((x) => x.devPath).filter((x) => x === d.devPath || dataDisks.includes(x)) : dataDisks.filter((x) => x !== d.devPath))} />{d.devPath} <span class="text-muted">{fmt.bytes(d.sizeBytes)}{d.model ? ` · ${d.model}` : ''}</span></label>)}
          </div>
        </Field>
      )}
      <button class="text-[12px] text-accent text-left hover:underline" onClick={() => setMore(!more)}>{more ? '▾' : '▸'} Labels, taints and addressing</button>
      {more && (
        <div class="grid grid-cols-2 gap-3">
          <Field label="Extra labels" hint="key=value per line; merged over the pool's."><KVEditor value={labels} onChange={setLabels} /></Field>
          <Field label="Extra taints" hint="key=value:Effect per line."><KVEditor value={taints} onChange={setTaints} /></Field>
          <Field label="Addressing" hint={staticOn ? 'Pinned in the machine config; the node comes up on this address.' : 'Follows the LAN DHCP server; Kubit tracks the machine by MAC.'}>
            <select class="input" value={staticOn ? 'static' : 'dhcp'} onChange={(e) => setNetwork((e.target as HTMLSelectElement).value === 'static' && selected ? { addresses: [`${selected.ip}/24`], gateway: guessGateway(selected.ip, 24) } : undefined)}>
              <option value="dhcp">DHCP</option>
              <option value="static">Static</option>
            </select>
          </Field>
          {staticOn && (
            <div class="flex flex-col gap-2">
              <input class="input mono" placeholder="address/prefix" value={network!.addresses[0] ?? ''} onInput={(e) => setNetwork({ ...network!, addresses: [(e.target as HTMLInputElement).value.trim()] })} />
              <input class="input mono" placeholder="gateway" value={network!.gateway ?? ''} onInput={(e) => setNetwork({ ...network!, gateway: (e.target as HTMLInputElement).value.trim() || undefined })} />
              <input class="input mono" placeholder="VLAN id (optional)" type="number" min={0} max={4094} value={network!.vlan ?? ''} onInput={(e) => { const v = Number((e.target as HTMLInputElement).value); setNetwork({ ...network!, vlan: v > 0 ? v : undefined }) }} />
            </div>
          )}
        </div>
      )}
      <p class="text-[12px] text-muted">Runs: preflight → generate config from the cluster's secrets → apply and install → wait for Ready. Progress opens in the Activity drawer.</p>
    </Dialog>
  )
}

/** Re-point the declaration (and the machine config) at the address the machine now uses, or pin a static one. */
export function ReaddressDialog({ cluster, n, spec, onClose }: { cluster: ClusterRow; n: NodeStatus; spec?: NodeSpec; onClose: () => void }) {
  const isEndpoint = cluster.spec.spec.controlPlane.endpoint === `https://${n.ip}:6443`
  const [mode, setMode] = useState<'dhcp' | 'static'>(spec?.network ? 'static' : 'dhcp')
  const seen = n.seenAt && n.seenAt !== n.ip ? n.seenAt : ''
  const [ip, setIp] = useState(seen || n.ip)
  const [network, setNetwork] = useState<NodeNetwork>(spec?.network ?? { addresses: [`${seen || n.ip}/24`], gateway: guessGateway(seen || n.ip, 24) })
  const [error, setError] = useState<string | null>(null)
  const submit = () => api.readdressNode(cluster.name, n.hostname, mode === 'static' ? network : null, mode === 'static' ? network.addresses[0].split('/')[0] : ip)
    .then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Update address of ${n.hostname}`} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={isEndpoint && ip !== n.ip} onClick={submit}>Apply</button></>}>
      <ErrorBox error={error} />
      <MaintenanceNotice cluster={cluster.name} />
      {seen && <p class="text-[13px]">The declaration says <span class="mono">{n.ip}</span>; discovery last saw this MAC at <span class="mono">{seen}</span>.</p>}
      {isEndpoint && <p class="text-[13px] text-warn">This node is the API endpoint (no VIP). Changing its address would break every kubeconfig; set a VIP first.</p>}
      <Field label="Mode">
        <select class="input" value={mode} onChange={(e) => setMode((e.target as HTMLSelectElement).value as any)}>
          <option value="dhcp">DHCP — record the new lease address</option>
          <option value="static">Static — pin an address in the machine config</option>
        </select>
      </Field>
      {mode === 'dhcp' ? (
        <Field label="Address Kubit should use" hint="Where the Talos API answers now."><input class="input mono" value={ip} onInput={(e) => setIp((e.target as HTMLInputElement).value.trim())} /></Field>
      ) : (
        <div class="grid grid-cols-2 gap-3">
          <Field label="Address (CIDR)"><input class="input mono" value={network.addresses[0] ?? ''} onInput={(e) => setNetwork({ ...network, addresses: [(e.target as HTMLInputElement).value.trim()] })} /></Field>
          <Field label="Gateway"><input class="input mono" value={network.gateway ?? ''} onInput={(e) => setNetwork({ ...network, gateway: (e.target as HTMLInputElement).value.trim() || undefined })} /></Field>
          <Field label="Nameservers" hint="Optional; comma-separated."><input class="input mono" value={(network.nameservers ?? []).join(', ')} onInput={(e) => { const v = (e.target as HTMLInputElement).value.split(/[,\s]+/).filter(Boolean); setNetwork({ ...network, nameservers: v.length ? v : undefined }) }} /></Field>
          <Field label="VLAN"><input class="input mono" type="number" min={0} max={4094} value={network.vlan ?? ''} onInput={(e) => { const v = Number((e.target as HTMLInputElement).value); setNetwork({ ...network, vlan: v > 0 ? v : undefined }) }} /></Field>
        </div>
      )}
      <p class="text-[12px] text-muted">Applies the node's config without a reboot, waits for the Talos API on the new address and for Kubernetes Ready, then saves cluster.yaml.</p>
    </Dialog>
  )
}
