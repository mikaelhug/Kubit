import { useCallback, useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type ClusterRow, type Status } from '../../api'
import { clusters, connected, loadHealth, operations, statuses } from '../../store'
import { Tabs } from '../../components/Tabs'
import { ErrorBox, Pill, stateTone } from '../../components/ui'
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
  // The watcher pushes status every 15 s over SSE; poll only while disconnected.
  useEffect(() => {
    if (connected.value) return
    const t = setInterval(refresh, 10000)
    return () => clearInterval(t)
  }, [refresh, connected.value])
  const finished = [...operations.value.values()].filter((o) => o.cluster === name && o.status !== 'running').length
  useEffect(() => { refresh() }, [finished, refresh])

  if (!cluster) return <div class="p-8 text-muted">{error ?? `Cluster ${name} is not known.`}</div>
  const ctx: ClusterCtx = { name, cluster, status, refresh, error }
  const spec = cluster.spec.spec
  const runningHere = [...operations.value.values()].filter((o) => o.cluster === name && o.status === 'running')

  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 pb-0 border-b border-border bg-panel/60">
        <div class="flex flex-wrap items-center gap-3 mb-3">
          <h1 class="text-xl font-semibold">{name}</h1>
          <Pill tone={stateTone(cluster.state)}>{cluster.state}</Pill>
          {status && <Pill tone={status.apiReachable ? 'good' : 'bad'} title={status.apiError}>{status.apiReachable ? 'API reachable' : 'API unreachable'}</Pill>}
          {runningHere.length > 0 && <Pill tone="warn">{runningHere.length} operation{runningHere.length === 1 ? '' : 's'} running</Pill>}
          <span class="mono text-muted text-[12px]">Talos {spec.talosVersion} · Kubernetes {spec.kubernetesVersion} · {spec.controlPlane.endpoint}</span>
        </div>
        <Tabs active={section} tabs={sectionList.map(([id, label]) => ({ id, label, href: `/clusters/${name}/${id}`, badge: id === 'overview' && runningHere.length ? runningHere.length : undefined }))} />
      </header>
      <div class="p-6 flex flex-col gap-5 max-w-[1300px]">
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
    default: return <Placeholder title="Not found" milestone="">Unknown section.</Placeholder>
  }
}

/** Old per-cluster Operations URLs land on Activity filtered to the cluster. */
function Redirect({ to }: { to: string }) {
  const { route } = useLocation()
  useEffect(() => { route(to, true) }, [to])
  return null
}
