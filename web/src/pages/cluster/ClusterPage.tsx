import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type ClusterRow, type Status } from '../../api'
import { Tabs } from '../../components/Tabs'
import { ClusterPill, ErrorBox, Pill, SeenAgo } from '../../components/ui'
import { setIn } from '../../maps'
import { sectionList, type Section } from '../../routes'
import { clusters, loadHealth, runningFor, statuses } from '../../store'
import { useLive } from '../../useLive'
import { Addons } from './Addons'
import { Backups } from './Backups'
import { Lifecycle } from './Lifecycle'
import { Network } from './Network'
import { Nodes } from './Nodes'
import { Overview } from './Overview'
import { PlanReview } from './PlanReview'
import { Settings } from './Settings'
import { Storage } from './Storage'
import { Workloads } from './Workloads'

export interface ClusterCtx { name: string; cluster: ClusterRow; status: Status | null }

export function ClusterPage({ name, section = 'overview', sub }: { name: string; section?: string; sub?: string }) {
  const { data: fetched, error } = useLive(() => api.status(name), [name])
  useEffect(() => { loadHealth(name) }, [name])
  const cluster = clusters.value.find((c) => c.name === name)
  const status: Status | null = statuses.value.get(name) ?? fetched
  if (!cluster) return <div class="p-8 text-muted">{error ?? `Cluster ${name} is not known.`}</div>
  const ctx: ClusterCtx = { name, cluster, status }
  const runningHere = runningFor(name).length

  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 pb-0 border-b border-border bg-panel">
        <div class="flex flex-wrap items-center gap-3 mb-3">
          <h1 class="text-xl font-semibold">{name}</h1>
          <ClusterPill state={cluster.state} status={status} />
          {runningHere > 0 && <Pill tone="warn">{runningHere} operation{runningHere === 1 ? '' : 's'} running</Pill>}
          <SeenAgo contact={status?.lastContactAt} observed={status?.observedAt} blind={!!status && (status.observer === 'offline' || (!status.apiReachable && !status.nodes.some((n) => n.talosReachable)))} />
          <CheckNow name={name} />
        </div>
        <Tabs active={section} tabs={sectionList.map(([id, label]) => ({ id, label, href: `/clusters/${name}/${id}`, badge: id === 'overview' && runningHere ? runningHere : undefined }))} />
      </header>
      <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
        <ErrorBox error={error} />
        {renderSection(section as Section | 'operations', sub, ctx)}
      </div>
    </div>
  )
}

function renderSection(section: Section | 'operations', sub: string | undefined, ctx: ClusterCtx) {
  switch (section) {
    case 'overview': return <Overview ctx={ctx} />
    case 'nodes': return <Nodes ctx={ctx} />
    case 'addons': return sub ? <PlanReview ctx={ctx} planId={Number(sub)} /> : <Addons ctx={ctx} />
    case 'operations': return <Redirect to={`/operations?cluster=${ctx.name}`} />
    case 'settings': return <Settings ctx={ctx} />
    case 'workloads': return <Workloads ctx={ctx} />
    case 'network': return <Network ctx={ctx} />
    case 'storage': return <Storage ctx={ctx} />
    case 'backups': return <Backups ctx={ctx} />
    case 'lifecycle': return <Lifecycle ctx={ctx} />
    default: return <Redirect to={`/clusters/${ctx.name}/overview`} />
  }
}

function CheckNow({ name }: { name: string }) {
  const [busy, setBusy] = useState(false)
  const check = () => {
    setBusy(true)
    api.status(name, true).then((st) => {
      const prev = statuses.value.get(name)
      const contact = st.apiReachable || st.nodes.some((n) => n.talosReachable) ? st.observedAt : prev?.lastContactAt
      setIn(statuses, name, { ...(prev ?? st), ...st, health: prev?.health, openAlerts: prev?.openAlerts, lastContactAt: contact })
    }).catch(() => {}).finally(() => setBusy(false))
  }
  return <button class="btn btn-xs" disabled={busy} title="Probe the Talos and Kubernetes APIs now" onClick={check}>{busy ? 'Checking' : 'Check now'}</button>
}

function Redirect({ to }: { to: string }) {
  const { route } = useLocation()
  useEffect(() => { route(to, true) }, [to])
  return null
}
