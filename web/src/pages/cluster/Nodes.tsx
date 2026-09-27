import { useMemo, useState } from 'preact/hooks'
import { api, fmt, type ClusterRow, type NodeStatus } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { ReaddressDialog } from '../../components/ReaddressDialog'
import { ConfirmDialog, Pill, Section } from '../../components/ui'
import { runOp } from '../../ops'
import { useQueryParam } from '../../query'
import { AddNodeDialog } from './AddNodeDialog'
import type { ClusterCtx } from './ClusterPage'

const smallAlloc = 768 * 1048576

export function Nodes({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const [adoptIP, setAdoptIP] = useQueryParam('adopt')
  const [add, setAdd] = useState(false)
  const [remove, setRemove] = useState<NodeStatus | null>(null)
  const [readdress, setReaddress] = useState<NodeStatus | null>(null)
  const [pool, setPool] = useState('')
  const specs = cluster.spec.spec.nodes
  const all: NodeStatus[] = status?.nodes ?? specs.map((n) => ({ ...n, role: n.role ?? 'worker', pool: n.pool ?? '', kvm: !!n.kvm, ready: false, unschedulable: false, registered: false, stage: '', talosVersion: '', kubeletVersion: '', cpuMilli: 0, cpuCapMilli: 0, memBytes: 0, memCapBytes: 0, memAllocBytes: 0, pods: 0, podCap: 0, gvisor: false, talosReachable: false, talosError: 'querying' }))
  const rows = pool ? all.filter((n) => n.pool === pool) : all
  const pools = cluster.spec.spec.pools ?? []
  const apiUp = !!status?.apiReachable

  const columns = useMemo<Column<NodeStatus>[]>(() => {
    const specOf = (n: NodeStatus) => specs.find((s) => s.hostname === n.hostname)
    const machineHref = (n: NodeStatus) => { const mac = specOf(n)?.mac; return mac ? `/machines/${mac}` : `/nodes/${n.ip}` }
    return [
      { id: 'hostname', header: 'Hostname', sort: (n) => n.hostname, cell: (n) => <a href={machineHref(n)} class="font-medium hover:underline">{n.hostname}</a> },
      { id: 'ip', header: 'Address', sort: (n) => n.ip, mono: true, text: (n) => `${n.ip} ${n.seenAt ?? ''}`, cell: (n) => {
        const sp = specOf(n)
        const moved = n.seenAt && n.seenAt !== n.ip
        return (
          <div class="flex flex-col">
            <span>{n.ip} <span class="text-[10px] text-muted">{sp?.network ? (sp.network.vlan ? `static · vlan ${sp.network.vlan}` : 'static') : 'dhcp'}</span></span>
            {moved && <button class="text-[11px] text-warn hover:underline text-left" title={`Last seen at ${n.seenAt}; declared ${n.ip}`} onClick={() => setReaddress(n)}>seen at {n.seenAt} — update</button>}
          </div>
        )
      } },
      { id: 'pool', header: 'Pool', sort: (n) => `${n.role === 'controlplane' ? 0 : 1} ${n.pool}`, cell: (n) => <span class="flex items-center gap-1.5"><span class="mono">{n.pool || '—'}</span><span class="text-[10px] text-muted">{n.role === 'controlplane' ? 'control plane' : 'worker'}</span></span> },
      { id: 'status', header: 'Status', sort: (n) => (n.talosReachable ? 1 : 0) + (n.ready ? 2 : 0), text: (n) => `${n.talosReachable ? '' : 'unreachable'} ${n.ready ? 'ready' : 'notready'}`, cell: (n) => <NodeHealth n={n} apiReachable={apiUp} /> },
      { id: 'talos', header: 'Talos', sort: (n) => n.talosVersion, mono: true, cell: (n) => n.talosVersion || '—' },
      { id: 'kubelet', header: 'Kubelet', sort: (n) => n.kubeletVersion, mono: true, cell: (n) => n.kubeletVersion || '—' },
      { id: 'cpu', header: 'CPU', align: 'right', sort: (n) => n.cpuMilli, cell: (n) => <>{fmt.cores(n.cpuMilli)}<span class="text-muted">/{fmt.cores(n.cpuCapMilli)}</span></> },
      { id: 'ram', header: 'RAM used / total', align: 'right', sort: (n) => n.memBytes, cell: (n) => { const small = !!n.memAllocBytes && n.memAllocBytes < smallAlloc; return <span title={n.memAllocBytes ? `${fmt.bytes(n.memAllocBytes)} allocatable for pods${small ? '; too small for the add-ons' : ''}` : undefined}><span class={n.memCapBytes && n.memBytes >= n.memCapBytes * 0.95 ? 'text-bad' : ''}>{fmt.bytes(n.memBytes)}</span><span class={small ? 'text-warn' : 'text-muted'}>/{fmt.bytes(n.memCapBytes)}</span></span> } },
      { id: 'pods', header: 'Pods', align: 'right', sort: (n) => n.pods, cell: (n) => <a href={`/clusters/${name}/workloads?view=pods&node=${encodeURIComponent(n.hostname)}`} class="hover:underline">{n.pods}</a> },
      { id: 'gvisor', header: 'gVisor', sort: (n) => n.gvisor ? 1 : 0, cell: (n) => n.gvisor ? <Pill tone="good">{n.kvm ? 'kvm' : 'runsc'}</Pill> : <span class="text-muted">—</span> },
      { id: 'actions', header: '', align: 'right', cell: (n) => (
        <span class="whitespace-nowrap flex gap-1 justify-end">
          <a href={machineHref(n)} class="btn btn-sm">Open</a>
          <button class="btn btn-danger btn-sm" disabled={!apiUp} title={!apiUp ? 'Needs the Kubernetes API to drain' : 'Drain, delete and reset'} onClick={() => setRemove(n)}>Remove</button>
        </span>
      ) },
    ]
  }, [specs, apiUp, name])

  return (
    <>
      <Section title="Nodes"
        actions={<>
          {pools.length > 2 || pool ? (
            <select class="input !py-1 w-auto" value={pool} onChange={(e) => setPool((e.target as HTMLSelectElement).value)} aria-label="Filter by pool">
              <option value="">All pools</option>
              {pools.map((p) => <option key={p.name} value={p.name}>{p.name} ({specs.filter((s) => s.pool === p.name).length})</option>)}
            </select>
          ) : null}
          <button class="btn btn-primary" onClick={() => setAdd(true)}>+ Add node</button>
        </>}>
        <DataTable id="nodes" columns={columns} rows={rows} rowKey={(n) => n.hostname} defaultSort={{ id: 'pool', dir: 'asc' }} />
      </Section>
      {(add || !!adoptIP) && <AddNodeDialog cluster={cluster} preselect={adoptIP || undefined} onClose={() => { setAdd(false); setAdoptIP('') }} />}
      {readdress && <ReaddressDialog cluster={cluster} n={readdress} spec={specs.find((s) => s.hostname === readdress.hostname)} onClose={() => setReaddress(null)} />}
      {remove && (
        <ConfirmDialog title={`Remove ${remove.hostname}`} action="Drain and remove" tone="danger" cluster={name} onClose={() => setRemove(null)}
          onConfirm={() => runOp(api.removeNode(name, remove.hostname)).then((ok) => { if (ok) setRemove(null) })}
          impact={<RemoveImpact n={remove} cluster={cluster} />} />
      )}
    </>
  )
}

function NodeHealth({ n, apiReachable }: { n: NodeStatus; apiReachable: boolean }) {
  const pills = []
  if (!n.talosReachable) pills.push(<Pill key="talos" tone="bad" title={n.talosError}>Talos unreachable</Pill>)
  else if (n.stage && n.stage !== 'running') pills.push(<Pill key="talos" tone="warn">{n.stage}</Pill>)
  if (!apiReachable) pills.push(<Pill key="k8s" tone="muted">k8s unknown</Pill>)
  else if (!n.registered) pills.push(<Pill key="k8s" tone="warn" title="The kubelet has not registered a Node object">not registered</Pill>)
  else if (!n.ready) pills.push(<Pill key="k8s" tone="warn">NotReady</Pill>)
  else pills.push(<Pill key="k8s" tone="good">Ready</Pill>)
  if (n.unschedulable) pills.push(<Pill key="cordon" tone="warn">cordoned</Pill>)
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
      <li>Cordons and drains <b>{n.pods}</b> running pod{n.pods === 1 ? '' : 's'}, deletes the Node object and resets Talos to maintenance mode.</li>
      {n.role === 'controlplane' && remaining === 0 && <li class="text-bad">This is the last control plane: Kubit will refuse.</li>}
      {n.role === 'controlplane' && remaining === 2 && <li class="text-warn">Leaves 2 control planes; Kubit refuses unless forced from the CLI.</li>}
      {n.role === 'controlplane' && remaining >= 3 && <li>etcd keeps quorum with {remaining} members.</li>}
    </ul>
  )
}
