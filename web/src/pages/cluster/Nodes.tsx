import { useMemo, useState } from 'preact/hooks'
import { fmt, type NodeStatus } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { useConfigStatus } from '../../configStatus'
import { Pill, Section } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

const smallAlloc = 768 * 1048576

export function Nodes({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const [pool, setPool] = useState('')
  const specs = cluster.spec.spec.nodes
  const all: NodeStatus[] = status?.nodes ?? specs.map((n) => ({ ...n, role: n.role ?? 'worker', pool: n.pool ?? '', kvm: !!n.kvm, ready: false, unschedulable: false, registered: false, stage: '', talosVersion: '', kubeletVersion: '', cpuMilli: 0, cpuCapMilli: 0, memBytes: 0, memCapBytes: 0, memAllocBytes: 0, pods: 0, podCap: 0, gvisor: false, talosReachable: false, talosError: 'querying' }))
  const rows = pool ? all.filter((n) => n.pool === pool) : all
  const pools = cluster.spec.spec.pools ?? []
  const apiUp = !!status?.apiReachable
  const behind = useConfigStatus(name)?.behind

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
            {moved && <span class="text-[11px] text-warn" title={`Declared ${n.ip}`}>seen at {n.seenAt}</span>}
          </div>
        )
      } },
      { id: 'pool', header: 'Pool', sort: (n) => `${n.role === 'controlplane' ? 0 : 1} ${n.pool}`, cell: (n) => <span class="flex items-center gap-1.5"><span class="mono">{n.pool || '—'}</span><span class="text-[10px] text-muted">{n.role === 'controlplane' ? 'control plane' : 'worker'}</span></span> },
      { id: 'status', header: 'Status', sort: (n) => (n.talosReachable ? 1 : 0) + (n.ready ? 2 : 0), text: (n) => `${n.talosReachable ? '' : 'unreachable'} ${n.ready ? 'ready' : 'notready'}${behind?.includes(n.hostname) ? ' config behind' : ''}`, cell: (n) => <NodeHealth n={n} apiReachable={apiUp} behind={!!behind?.includes(n.hostname)} /> },
      { id: 'talos', header: 'Talos', sort: (n) => n.talosVersion, mono: true, cell: (n) => n.talosVersion || '—' },
      { id: 'kubelet', header: 'Kubelet', sort: (n) => n.kubeletVersion, mono: true, cell: (n) => n.kubeletVersion || '—' },
      { id: 'cpu', header: 'CPU', align: 'right', sort: (n) => n.cpuMilli, cell: (n) => <>{fmt.cores(n.cpuMilli)}<span class="text-muted">/{fmt.cores(n.cpuCapMilli)}</span></> },
      { id: 'ram', header: 'RAM used / total', align: 'right', sort: (n) => n.memBytes, cell: (n) => { const small = !!n.memAllocBytes && n.memAllocBytes < smallAlloc; return <span title={n.memAllocBytes ? `${fmt.bytes(n.memAllocBytes)} allocatable for pods${small ? '; too small for the add-ons' : ''}` : undefined}><span class={n.memCapBytes && n.memBytes >= n.memCapBytes * 0.95 ? 'text-bad' : ''}>{fmt.bytes(n.memBytes)}</span><span class={small ? 'text-warn' : 'text-muted'}>/{fmt.bytes(n.memCapBytes)}</span></span> } },
      { id: 'pods', header: 'Pods', align: 'right', sort: (n) => n.pods, cell: (n) => <a href={`/clusters/${name}/workloads?view=pods&node=${encodeURIComponent(n.hostname)}`} class="hover:underline">{n.pods}</a> },
      { id: 'gvisor', header: 'gVisor', sort: (n) => n.gvisor ? 1 : 0, cell: (n) => n.gvisor ? <Pill tone="good">{n.kvm ? 'kvm' : 'runsc'}</Pill> : <span class="text-muted">—</span> },
    ]
  }, [specs, apiUp, name, behind])

  return (
    <Section title="Nodes" help="Declared under spec.nodes in cluster.yaml."
      actions={pools.length > 2 || pool ? (
        <select class="input !py-1 w-auto" value={pool} onChange={(e) => setPool((e.target as HTMLSelectElement).value)} aria-label="Filter by pool">
          <option value="">All pools</option>
          {pools.map((p) => <option key={p.name} value={p.name}>{p.name} ({specs.filter((s) => s.pool === p.name).length})</option>)}
        </select>
      ) : undefined}>
      <DataTable id="nodes" columns={columns} rows={rows} rowKey={(n) => n.hostname} defaultSort={{ id: 'pool', dir: 'asc' }} />
    </Section>
  )
}

function NodeHealth({ n, apiReachable, behind }: { n: NodeStatus; apiReachable: boolean; behind: boolean }) {
  const pills = []
  if (!n.talosReachable) pills.push(<Pill key="talos" tone="bad" title={n.talosError}>Talos unreachable</Pill>)
  else if (n.stage && n.stage !== 'running') pills.push(<Pill key="talos" tone="warn">{n.stage}</Pill>)
  if (!apiReachable) pills.push(<Pill key="k8s" tone="muted">k8s unknown</Pill>)
  else if (!n.registered) pills.push(<Pill key="k8s" tone="warn" title="The kubelet has not registered a Node object">not registered</Pill>)
  else if (!n.ready) pills.push(<Pill key="k8s" tone="warn">NotReady</Pill>)
  else pills.push(<Pill key="k8s" tone="good">Ready</Pill>)
  if (n.unschedulable) pills.push(<Pill key="cordon" tone="warn">cordoned</Pill>)
  if (behind) pills.push(<Pill key="config" tone="warn" title="Run kubit apply">Config behind</Pill>)
  return (
    <div class="flex flex-col gap-0.5">
      <span class="inline-flex flex-wrap gap-1">{pills}</span>
      {!n.talosReachable && n.talosError && <span class="text-[11px] text-muted max-w-[260px] truncate" title={n.talosError}>{n.talosError}</span>}
    </div>
  )
}
