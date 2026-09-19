import { useCallback, useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type ClusterRow, type Status } from '../../api'
import { clusters, loadHealth, operations, statuses } from '../../store'
import { Tabs } from '../../components/Tabs'
import { ClusterPill, ErrorBox, Pill, SeenAgo } from '../../components/ui'
import { sectionList, type Section } from '../../app'
import { Overview } from './Overview'
import { Nodes } from './Nodes'
import { Addons } from './Addons'
import { PlanReview } from './PlanReview'
import { Settings } from './Settings'
import { Placeholder } from './Placeholder'
import { Workloads } from './Workloads'
import { Network } from './Network'
import { Storage } from './Storage'
import { Backups } from './Backups'
import { Lifecycle } from './Lifecycle'

export interface ClusterCtx { name: string; cluster: ClusterRow; status: Status | null; refresh: () => void; error: string | null }

/** Cluster scope: header + section tabs; sections render below. */
export function ClusterPage({ name, section = 'overview', sub }: { name: string; section?: string; sub?: string }) {
  const [fetched, setFetched] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)
  const cluster = clusters.value.find((c) => c.name === name)
  const pushed = statuses.value.get(name)
  const status: Status | null = pushed ?? fetched
  const refresh = useCallback(() => { api.status(name).then((s) => { setFetched(s); setError(null) }).catch((e) => setError(e.message)) }, [name])
  useEffect(() => { setFetched(null); refresh(); loadHealth(name) }, [refresh, name])
  // Status arrives on every watcher tick over the live connection; the fetch above
  // only covers the moment before the first tick.
  if (!cluster) return <div class="p-8 text-muted">{error ?? `Cluster ${name} is not known.`}</div>
  const ctx: ClusterCtx = { name, cluster, status, refresh, error }
  const runningHere = [...operations.value.values()].filter((o) => o.cluster === name && o.status === 'running')

  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 pb-0 border-b border-border bg-panel">
        <div class="flex flex-wrap items-center gap-3 mb-3">
          <h1 class="text-xl font-semibold">{name}</h1>
          <ClusterPill state={cluster.state} status={status} />
          {runningHere.length > 0 && <Pill tone="warn">{runningHere.length} operation{runningHere.length === 1 ? '' : 's'} running</Pill>}
          <SeenAgo contact={status?.lastContactAt} observed={status?.observedAt} blind={!!status && (status.observer === 'offline' || (!status.apiReachable && !status.nodes.some((n) => n.talosReachable)))} />
          <CheckNow name={name} />
        </div>
        <Tabs active={section} tabs={sectionList.map(([id, label]) => ({ id, label, href: `/clusters/${name}/${id}`, badge: id === 'overview' && runningHere.length ? runningHere.length : undefined }))} />
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
    default: return <Placeholder title="Not found" milestone="">Unknown section.</Placeholder>
  }
}

/** A live probe of the cluster right now, outside the watcher's tick. */
function CheckNow({ name }: { name: string }) {
  const [busy, setBusy] = useState(false)
  const check = () => { setBusy(true); api.status(name, true).then((st) => { const sm = new Map(statuses.value); sm.set(name, { ...(statuses.value.get(name) ?? st), ...st, health: statuses.value.get(name)?.health, openAlerts: statuses.value.get(name)?.openAlerts, lastContactAt: st.apiReachable || st.nodes.some((n) => n.talosReachable) ? st.observedAt : statuses.value.get(name)?.lastContactAt }); statuses.value = sm }).catch(() => {}).finally(() => setBusy(false)) }
  return <button class="btn !py-0.5 !px-2 text-[11px]" disabled={busy} title="Probe the Talos and Kubernetes APIs now" onClick={check}>{busy ? 'Checking' : 'Check now'}</button>
}

/** Old per-cluster Operations URLs land on Activity filtered to the cluster. */
function Redirect({ to }: { to: string }) {
  const { route } = useLocation()
  useEffect(() => { route(to, true) }, [to])
  return null
}
