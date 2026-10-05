import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type Inventory, type NodeRow, type NodeSpec } from '../../api'
import { Tabs } from '../../components/Tabs'
import { Breadcrumbs, CopyButton, ErrorBox, Pill, SeenAgo } from '../../components/ui'
import { KindPill, TypePill } from '../../components/Machine'
import { nodeEntry } from '../../machine'
import { running } from '../../ops'
import { clusters, live, machineList, machines, statuses } from '../../store'
import { useLive } from '../../useLive'
import { HardwareTab } from './Hardware'
import { KubernetesTab } from './Kubernetes'
import { LogsTab } from './Logs'
import { OverviewTab } from './Overview'
import { ServicesTab } from './Services'
import { talosLive } from './talos'

type TabId = 'overview' | 'hardware' | 'kubernetes' | 'services' | 'logs'

export function NodePage({ ip: ipParam, mac }: { ip?: string; mac?: string }) {
  const [tab, setTab] = useState<TabId>('overview')
  const node = mac ? machines.value.get(mac.toLowerCase()) ?? null : machineList.value.find((n) => n.ip === ipParam) ?? null
  const ip = node?.ip ?? ipParam ?? ''
  useEffect(() => {
    if (node && !mac) history.replaceState(null, '', `/machines/${node.mac}`)
  }, [node?.mac, mac])
  const talos = talosLive(node)
  const { data: inv, error: invErr } = useLive(() => api.inventory(ip), [ip], talos.scopes, { enabled: !!node?.talos, refresh: talos.refresh })
  const { data: k8s, error: k8sErr } = useLive(() => api.nodeKubernetes(ip), [ip], [...talos.scopes, [node?.cluster ?? '', 'workloads']], { enabled: node?.kind === 'member' })

  const cluster = node?.cluster ? clusters.value.find((c) => c.name === node.cluster) : undefined
  const spec: NodeSpec | undefined = cluster?.spec.spec.nodes.find((n) => (node?.mac && n.mac === node.mac) || n.hostname === node?.hostname)
  const title = node?.hostname || ip || mac || ''
  const ops = running.value.filter((o) => { const r = o.request as { hostname?: string; mac?: string } | undefined; return (o.cluster === node?.cluster && r?.hostname === node?.hostname) || r?.mac === node?.mac })
  const tabs: { id: TabId; label: string; badge?: number }[] = [
    { id: 'overview', label: 'Overview' }, { id: 'hardware', label: 'Hardware' },
    ...(node?.kind === 'member' ? [{ id: 'kubernetes' as TabId, label: 'Kubernetes', badge: k8s?.pods?.length }] : []),
    ...(node?.talos ? [{ id: 'services' as TabId, label: 'Services' }, { id: 'logs' as TabId, label: 'Logs' }] : []),
  ]
  const shown = node && !tabs.some((t) => t.id === tab) ? 'overview' : tab

  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 border-b border-border bg-panel">
        <Breadcrumbs items={node?.cluster ? [{ label: node.cluster, href: `/clusters/${node.cluster}/overview` }, { label: 'Nodes', href: `/clusters/${node.cluster}/nodes` }, { label: title }] : [{ label: 'Discovery', href: '/discovery' }, { label: title }]} />
        <div class="flex flex-wrap items-center gap-3 mt-2 mb-3">
          <h1 class="text-xl font-semibold">{title}</h1>
          {node && !node.cluster && <KindPill m={node} />}
          {node && <TypePill m={node} />}
          {node?.talos && <ReachPill node={node} inv={inv} invErr={invErr} />}
          {k8s ? <Pill tone={k8s.ready ? 'good' : 'warn'}>{k8s.ready ? 'Ready' : 'NotReady'}</Pill> : null}
          {k8s?.unschedulable && <Pill tone="warn">cordoned</Pill>}
          {ops.map((o) => <Pill key={o.id} tone="warn">{fmt.kind(o.kind)} running</Pill>)}
          {node?.kind === 'maintenance' && <span class="ml-auto"><CopyButton className="btn btn-primary btn-sm" label="Copy node entry" text={() => nodeEntry(node)} /></span>}
        </div>
        <Tabs active={shown} onSelect={(t) => setTab(t as TabId)} tabs={tabs} />
      </header>
      <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
        <ErrorBox error={!node && live.value ? `No machine ${mac ?? ipParam} is known.` : null} />
        {shown === 'overview' && <OverviewTab inv={inv} invErr={invErr} k8s={k8s} k8sErr={k8sErr} node={node} spec={spec} storage={cluster?.spec.spec.platform.longhorn?.enabled ? cluster.spec.spec.storage : undefined} />}
        {shown === 'hardware' && <HardwareTab inv={inv} invErr={invErr} node={node} />}
        {shown === 'kubernetes' && <KubernetesTab k8s={k8s} err={k8sErr} />}
        {shown === 'services' && <ServicesTab ip={ip} node={node} />}
        {shown === 'logs' && <LogsTab ip={ip} node={node} />}
      </div>
    </div>
  )
}

function ReachPill({ node, inv, invErr }: { node: NodeRow; inv: Inventory | null; invErr: string | null }) {
  const st = node.cluster ? statuses.value.get(node.cluster) : undefined
  const ns = st?.nodes.find((n) => n.hostname === node.hostname)
  const blind = st?.observer === 'offline' || ns?.talosReach === 'no-network'
  if (blind) return <Pill tone="muted" title={st?.observerError || ns?.talosError}>Kubit cannot reach the network</Pill>
  if (ns) return <span class="flex items-center gap-2"><Pill tone={ns.talosReachable ? 'good' : 'bad'} title={ns.talosError}>{ns.talosReachable ? `Talos ${ns.talosVersion || ''}`.trim() : 'Talos unreachable'}</Pill><SeenAgo contact={st?.lastContactAt} observed={st?.observedAt} blind={!ns.talosReachable} /></span>
  if (inv) return <Pill tone="good">Talos {inv.talosVersion}</Pill>
  if (invErr) return <Pill tone="bad" title={invErr}>Talos not answering</Pill>
  return null
}
