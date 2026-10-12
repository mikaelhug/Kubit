import { useEffect, useMemo, useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'
import { fmt, type NodeStatus } from '../../api'
import { DataTable, selectColumn, useSelection, type Column } from '../../components/DataTable'
import { EditAddress } from '../../components/EditAddress'
import { NodeActions } from '../../components/NodeActions'
import { RunLog } from '../../components/RunLog'
import { Ago } from '../../components/Time'
import { addressOf } from '../../net'
import { useConfigStatus } from '../../configStatus'
import { Pill, Section } from '../../components/ui'
import { applyRuns, loadApplyRun, machines } from '../../store'
import { memoryOf, modelName, nodeState, roleLabel, specsOf } from '../../machine'
import { degrees, sensorOf, temperatureTitle, temperatureTone } from '../../temperature'
import { toneText } from '../../tone'
import type { ClusterCtx } from './ClusterPage'

const smallAlloc = 768 * 1048576
const nodeRuns = ['restart', 'shutdown', 'power-on']

function OnApply({ href, children }: { href: string; children: ComponentChildren }) {
  return <a class="text-[11px] text-info hover:underline" href={href}>→ {children} on apply</a>
}

export function Nodes({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const specs = cluster.spec.spec.nodes
  const apiUp = !!status?.apiReachable
  const behind = useConfigStatus(name)?.behind
  const sensors = !!status?.nodes.some((n) => n.temperatures?.length)
  const changes = `/clusters/${name}/changes`
  const sel = useSelection()
  const rows = status?.nodes ?? []
  const picked = rows.filter((n) => sel.selected.has(n.hostname))
  const run = applyRuns.value.get(name)
  const [hidden, setHidden] = useState<string | undefined>()
  useEffect(() => { loadApplyRun(name) }, [name])

  const columns = useMemo<Column<NodeStatus>[]>(() => {
    const specOf = (n: NodeStatus) => specs.find((s) => s.hostname === n.hostname)
    const machineHref = (n: NodeStatus) => { const mac = specOf(n)?.mac; return mac ? `/machines/${mac}` : `/nodes/${n.ip}` }
    const inventoryOf = (n: NodeStatus) => { const mac = specOf(n)?.mac?.toLowerCase(); return mac ? machines.value.get(mac)?.inventory : undefined }
    return [
      { id: 'hostname', header: 'Hostname', sort: (n) => n.hostname, cell: (n) => <a href={machineHref(n)} class="font-medium hover:underline">{n.hostname}</a> },
      { id: 'ip', header: 'Address', sort: (n) => n.ip, mono: true, text: (n) => `${n.ip} ${n.seenAt ?? ''}`, cell: (n) => {
        const sp = specOf(n)
        const moved = n.seenAt && n.seenAt !== n.ip
        const target = addressOf(sp?.network?.addresses[0])
        return (
          <div class="flex flex-col">
            <span>{sp ? <EditAddress cluster={name} hostname={n.hostname}>{n.ip}</EditAddress> : n.ip} {n.network && <span class="text-[10px] text-muted">{n.network.dhcp ? 'dhcp' : n.network.vlan ? `static · vlan ${n.network.vlan}` : 'static'}</span>}</span>
            {target && target !== n.ip && <OnApply href={changes}>{target}</OnApply>}
            {moved && <span class="text-[11px] text-warn" title={`Declared ${n.ip}`}>seen at {n.seenAt}</span>}
          </div>
        )
      } },
      { id: 'role', header: 'Role', sort: (n) => `${n.role === 'controlplane' ? 0 : 1} ${n.hostname}`, cell: (n) => roleLabel(n.role) },
      { id: 'hardware', header: 'Hardware', sort: (n) => memoryOf(inventoryOf(n)), text: (n) => `${modelName(inventoryOf(n))} ${specsOf(inventoryOf(n))}`, cell: (n) => {
        const inv = inventoryOf(n)
        return inv ? (
          <span class="flex flex-col whitespace-nowrap max-w-[200px]" title={`${modelName(inv)}\n${specsOf(inv)}`}>
            <span class="truncate">{modelName(inv)}</span>
            <span class="text-[10px] text-muted truncate">{specsOf(inv)}</span>
          </span>
        ) : <span class="text-muted">—</span>
      } },
      { id: 'status', header: 'Status', sort: (n) => (n.talosReachable ? 1 : 0) + (n.ready ? 2 : 0), text: (n) => `${nodeState(n, apiUp)[1]}${behind?.includes(n.hostname) ? ' config behind' : ''}`, cell: (n) => <NodeHealth n={n} apiReachable={apiUp} behind={!!behind?.includes(n.hostname)} declared={n.declared} changes={changes} /> },
      { id: 'versions', header: 'Versions', sort: (n) => `${n.talosVersion} ${n.kubeletVersion}`, cell: (n) => (
        <span class="flex flex-col whitespace-nowrap mono text-[12px]">
          <span>{n.talosVersion || '—'}<span class="text-muted"> Talos</span></span>
          <span>{n.kubeletVersion || '—'}<span class="text-muted"> Kubernetes</span></span>
          {n.talosTarget && n.talosVersion && n.talosTarget !== n.talosVersion && <OnApply href={changes}>Talos {n.talosTarget}</OnApply>}
        </span>
      ) },
      { id: 'cpu', header: 'CPU', align: 'right', sort: (n) => n.cpuMilli, cell: (n) => <>{fmt.cores(n.cpuMilli)}<span class="text-muted">/{fmt.cores(n.cpuCapMilli)}</span></> },
      { id: 'ram', header: 'Memory', align: 'right', sort: (n) => n.memBytes, cell: (n) => { const small = !!n.memAllocBytes && n.memAllocBytes < smallAlloc; return <span title={n.memAllocBytes ? `${fmt.bytes(n.memAllocBytes)} allocatable for pods${small ? '; too small for the add-ons' : ''}` : undefined}><span class={n.memCapBytes && n.memBytes >= n.memCapBytes * 0.95 ? 'text-bad' : ''}>{fmt.bytes(n.memBytes)}</span><span class={small ? 'text-warn' : 'text-muted'}>/{fmt.bytes(memoryOf(inventoryOf(n)) || n.memCapBytes)}</span></span> } },
      { id: 'pods', header: 'Pods', align: 'right', sort: (n) => n.pods, cell: (n) => <a href={`/clusters/${name}/workloads?view=pods&node=${encodeURIComponent(n.hostname)}`} class="hover:underline">{n.pods}</a> },
      ...(sensors ? [{ id: 'temp', header: 'Temp', align: 'right', sort: (n: NodeStatus) => sensorOf(n.temperatures, 'cpu')?.celsius ?? -1, cell: (n: NodeStatus) => <NodeTemperature n={n} /> } satisfies Column<NodeStatus>] : []),
    ]
  }, [specs, apiUp, name, changes, behind, machines.value, sensors])

  const toolbar = (
    <span class="flex items-center gap-2">
      {picked.length > 0 && <span class="text-[12px] text-muted">{picked.length} selected</span>}
      <NodeActions ctx={ctx} picked={picked} onDone={sel.clear} />
    </span>
  )
  const showRun = !!run?.kind && nodeRuns.includes(run.kind) && (run.running || run.lines.length > 0) && run.started !== hidden
  return (
    <Section title="Nodes" actions={<a class="btn btn-sm" href="/discovery">Add machines</a>}>
      <DataTable id="nodes" columns={[selectColumn(rows, (n) => n.hostname, sel, (n) => n.hostname), ...columns]} rows={rows} loading={!status} rowKey={(n) => n.hostname} defaultSort={{ id: 'role', dir: 'asc' }} toolbar={toolbar} />
      {showRun && run && <RunLog run={run} capped onClose={() => setHidden(run.started)} />}
    </Section>
  )
}

function NodeHealth({ n, apiReachable, behind, declared, changes }: { n: NodeStatus; apiReachable: boolean; behind: boolean; declared: boolean; changes: string }) {
  const [tone, state] = nodeState(n, apiReachable)
  return (
    <div class="flex flex-col items-start gap-0.5">
      <Pill tone={tone} title={!n.off && !n.talosReachable ? n.talosError : undefined}>{state}</Pill>
      {n.bootedAt && n.talosReachable && <span class="text-[11px] text-muted whitespace-nowrap">up <Ago iso={n.bootedAt} bare /></span>}
      {behind && <a class="text-[11px] text-warn hover:underline" href={changes}>Config behind</a>}
      {!declared && <span class="text-[11px] text-warn">undeclared</span>}
    </div>
  )
}

function NodeTemperature({ n }: { n: NodeStatus }) {
  const cpu = sensorOf(n.temperatures, 'cpu')
  const disk = sensorOf(n.temperatures, 'disk')
  if (!cpu && !disk) return <span class="text-muted">—</span>
  return (
    <span class="inline-flex flex-col items-end whitespace-nowrap" title={temperatureTitle(n.temperatures!)}>
      {cpu ? <span class={toneText[temperatureTone(cpu)]}>{degrees(cpu.celsius)}</span> : <span class="text-muted">—</span>}
      {disk && <span class={`text-[10px] ${temperatureTone(disk) === 'good' ? 'text-muted' : toneText[temperatureTone(disk)]}`}>disk {degrees(disk.celsius)}</span>}
    </span>
  )
}
