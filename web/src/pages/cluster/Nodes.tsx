import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type ClusterRow, type NodeRow, type NodeSpec, type NodeStatus } from '../../api'
import { toast, watch } from '../../store'
import { DataTable, type Column } from '../../components/DataTable'
import { ConfirmDialog, Dialog, ErrorBox, Field, Pill, Section } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

export function Nodes({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const adoptIP = typeof location !== 'undefined' ? new URLSearchParams(location.search).get('adopt') : null
  const [add, setAdd] = useState(!!adoptIP)
  const [remove, setRemove] = useState<NodeStatus | null>(null)
  const rows: NodeStatus[] = status?.nodes ?? cluster.spec.spec.nodes.map((n) => ({ ...n, kvm: !!n.kvm, ready: false, unschedulable: false, registered: false, stage: '', talosVersion: '', kubeletVersion: '', cpuMilli: 0, cpuCapMilli: 0, memBytes: 0, memCapBytes: 0, pods: 0, podCap: 0, gvisor: false, talosReachable: false, talosError: 'querying…' }))
  const apiUp = !!status?.apiReachable

  const columns: Column<NodeStatus>[] = [
    { id: 'hostname', header: 'Hostname', sort: (n) => n.hostname, cell: (n) => n.talosReachable ? <a href={`/nodes/${n.ip}`} class="font-medium hover:underline">{n.hostname}</a> : <span class="font-medium">{n.hostname}</span> },
    { id: 'ip', header: 'IP', sort: (n) => n.ip, mono: true, cell: (n) => n.ip },
    { id: 'role', header: 'Role', sort: (n) => n.role, cell: (n) => n.role === 'controlplane' ? 'Control plane' : 'Worker' },
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
      <Section title="Nodes" help="Talos reachability and Kubernetes readiness are shown separately: a machine can answer Talos while its kubelet is not registered, and vice versa."
        actions={<button class="btn btn-primary" onClick={() => setAdd(true)}>+ Add node</button>}>
        <DataTable id="nodes" columns={columns} rows={rows} rowKey={(n) => n.hostname} defaultSort={{ id: 'role', dir: 'asc' }} />
      </Section>
      {add && <AddNodeDialog cluster={cluster} preselect={adoptIP ?? undefined} onClose={() => { setAdd(false); if (adoptIP) history.replaceState(null, '', location.pathname) }} />}
      {remove && (
        <ConfirmDialog title={`Remove ${remove.hostname}`} action="Drain and remove" tone="danger" onClose={() => setRemove(null)}
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
  const [candidates, setCandidates] = useState<NodeRow[]>([])
  const [ip, setIp] = useState(preselect ?? '')
  const [hostname, setHostname] = useState('')
  const [role, setRole] = useState<'controlplane' | 'worker'>('worker')
  const [disk, setDisk] = useState('')
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { api.nodes().then((ns) => setCandidates(ns.filter((n) => n.state === 'maintenance' && !n.cluster))).catch((e) => setError(e.message)) }, [])
  const selected = candidates.find((c) => c.ip === ip)
  useEffect(() => {
    if (!selected) return
    const cand = selected.inventory?.disks.filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb').sort((a, b) => b.sizeBytes - a.sizeBytes)
    if (cand && cand[0]) setDisk(cand[0].devPath)
    const n = cluster.spec.spec.nodes.filter((x) => x.role === role).length + 1
    setHostname(`${cluster.name}-${role === 'controlplane' ? 'cp' : 'worker'}-${String(n).padStart(2, '0')}`)
  }, [selected, role, cluster])
  const submit = () => {
    if (!selected) return
    const node: NodeSpec = { hostname, ip, mac: selected.mac, role, arch: selected.arch, kvm: !!selected.inventory?.kvm, installDisk: { path: disk } }
    api.addNode(cluster.name, node).then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  }
  const cps = cluster.spec.spec.nodes.filter((x) => x.role === 'controlplane').length
  return (
    <Dialog title={`Add node to ${cluster.name}`} onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class="btn btn-primary" disabled={!selected || !hostname || !disk} onClick={submit}>Install and join</button>
      </>
    }>
      <ErrorBox error={error} />
      <Field label="Discovered machine (maintenance mode)" hint={candidates.length === 0 ? 'No unassigned machines. Run a discovery under Fleet → Inventory first.' : undefined}>
        <select class="input" value={ip} onChange={(e) => setIp((e.target as HTMLSelectElement).value)}>
          <option value="">Select…</option>
          {candidates.map((c) => <option key={c.ip} value={c.ip}>{c.ip} · {c.arch} · {c.inventory?.cpus ?? '?'} CPU · {fmt.bytes(c.inventory?.memoryBytes ?? 0)}{c.inventory?.kvm ? ' · kvm' : ''}</option>)}
        </select>
      </Field>
      <div class="grid grid-cols-2 gap-3">
        <Field label="Role" hint={role === 'controlplane' ? `etcd goes from ${cps} to ${cps + 1} members${(cps + 1) % 2 === 0 ? ' — an even count adds no fault tolerance' : ''}` : undefined}>
          <select class="input" value={role} onChange={(e) => setRole((e.target as HTMLSelectElement).value as any)}>
            <option value="worker">Worker</option>
            <option value="controlplane">Control plane</option>
          </select>
        </Field>
        <Field label="Hostname"><input class="input" value={hostname} onInput={(e) => setHostname((e.target as HTMLInputElement).value)} /></Field>
      </div>
      <Field label="Install disk" hint="Wiped and installed with Talos.">
        <select class="input" value={disk} onChange={(e) => setDisk((e.target as HTMLSelectElement).value)}>
          {selected?.inventory?.disks.filter((d) => !d.readonly && !d.cdrom).map((d) => <option key={d.devPath} value={d.devPath}>{d.devPath} · {fmt.bytes(d.sizeBytes)} {d.model ? `· ${d.model}` : ''} {d.transport ? `· ${d.transport}` : ''}</option>)}
          {!selected && <option value="">—</option>}
        </select>
      </Field>
      <p class="text-[12px] text-muted">Runs: preflight → generate config from the cluster's secrets → apply and install → wait for Ready. Progress opens in the Activity drawer.</p>
    </Dialog>
  )
}
