import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type ClusterRow, type Status } from '../../api'
import { Tabs } from '../../components/Tabs'
import { ClusterPill, ErrorBox, SeenAgo } from '../../components/ui'
import { setIn } from '../../maps'
import { sectionList, type Section } from '../../routes'
import { clusters, loadHealth, statuses } from '../../store'
import { useLive } from '../../useLive'
import { Addons } from './Addons'
import { Backups } from './Backups'
import { Config } from './Config'
import { Network } from './Network'
import { Nodes } from './Nodes'
import { Overview } from './Overview'
import { Storage } from './Storage'
import { Workloads } from './Workloads'

export interface ClusterCtx { name: string; cluster: ClusterRow; status: Status | null }

export function ClusterPage({ name, section = 'overview' }: { name: string; section?: string }) {
  const { data: fetched, error } = useLive(() => api.status(name), [name])
  useEffect(() => { loadHealth(name) }, [name])
  const cluster = clusters.value.find((c) => c.name === name)
  const pushed = statuses.value.get(name)
  const status: Status | null = pushed ?? fetched
  const statusError = pushed ? null : error
  if (!cluster) return <div class="p-8 text-muted">{statusError ?? `Cluster ${name} is not known.`}</div>
  const ctx: ClusterCtx = { name, cluster, status }

  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 pb-0 border-b border-border bg-panel">
        <div class="flex flex-wrap items-center gap-3 mb-3">
          <h1 class="text-xl font-semibold">{name}</h1>
          <ClusterPill state={cluster.state} status={status} />
          <SeenAgo contact={status?.lastContactAt} observed={status?.observedAt} blind={!!status && (status.observer === 'offline' || (!status.apiReachable && !status.nodes.some((n) => n.talosReachable)))} />
          <CheckNow name={name} />
        </div>
        <Tabs active={section} tabs={sectionList.map(([id, label]) => ({ id, label, href: `/clusters/${name}/${id}`}))} />
      </header>
      <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
        <ErrorBox error={statusError} />
        {renderSection(section as Section, ctx)}
      </div>
    </div>
  )
}

function renderSection(section: Section, ctx: ClusterCtx) {
  switch (section) {
    case 'overview': return <Overview ctx={ctx} />
    case 'nodes': return <Nodes ctx={ctx} />
    case 'addons': return <Addons ctx={ctx} />
    case 'config': return <Config ctx={ctx} />
    case 'workloads': return <Workloads ctx={ctx} />
    case 'network': return <Network ctx={ctx} />
    case 'storage': return <Storage ctx={ctx} />
    case 'backups': return <Backups ctx={ctx} />
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
  return <button class="btn btn-sm" disabled={busy} title="Probe the Talos and Kubernetes APIs now" onClick={check}>{busy ? 'Checking' : 'Check now'}</button>
}

function Redirect({ to }: { to: string }) {
  const { route } = useLocation()
  useEffect(() => { route(to, true) }, [to])
  return null
}
