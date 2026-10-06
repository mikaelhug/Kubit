import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type ClusterRow, type Status } from '../../api'
import { Tabs } from '../../components/Tabs'
import { ClusterPill, PlanPill, SeenAgo } from '../../components/ui'
import { pendingSections, sectionList, type Section } from '../../routes'
import { clusters, live, plans, statuses, toast } from '../../store'
import { Addons } from './Addons'
import { Backups } from './Backups'
import { Changes } from './Changes'
import { Network } from './Network'
import { Nodes } from './Nodes'
import { Overview } from './Overview'
import { Repository } from './Repository'
import { Secrets } from './Secrets'
import { Storage } from './Storage'
import { Workloads } from './Workloads'

export interface ClusterCtx { name: string; cluster: ClusterRow; status: Status | null }

export function ClusterPage({ name, section = 'overview' }: { name: string; section?: string }) {
  const cluster = clusters.value.find((c) => c.name === name)
  const status: Status | null = statuses.value.get(name) ?? null
  if (!cluster) return live.value ? <div class="p-8 text-muted">Cluster {name} is not known.</div> : null
  const ctx: ClusterCtx = { name, cluster, status }

  const declared = cluster.state === 'declared'
  const pending = declared || cluster.state === 'connecting'
  const shown = (pending && !pendingSections.includes(section) ? 'changes' : section) as Section
  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 pb-0 border-b border-border bg-panel">
        <div class="flex flex-wrap items-center gap-3 mb-3">
          <h1 class="text-xl font-semibold">{name}</h1>
          <ClusterPill state={cluster.state} status={status} />
          <PlanPill plan={plans.value.get(name)} href={`/clusters/${name}/changes`} />
          {pending ? <span class="text-[12px] text-muted">{declared ? 'Not created yet.' : 'No node answers yet.'}</span> : (
            <>
              <SeenAgo contact={status?.lastContactAt} observed={status?.observedAt} blind={!!status && (status.observer === 'offline' || (!status.apiReachable && !status.nodes.some((n) => n.talosReachable)))} />
              <CheckNow name={name} />
            </>
          )}
        </div>
        <Tabs active={shown} tabs={sectionList.filter(([id]) => !pending || pendingSections.includes(id)).map(([id, label]) => ({ id, label, href: `/clusters/${name}/${id}`}))} />
      </header>
      <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
        {renderSection(shown, ctx)}
      </div>
    </div>
  )
}

function renderSection(section: Section, ctx: ClusterCtx) {
  switch (section) {
    case 'overview': return <Overview ctx={ctx} />
    case 'nodes': return <Nodes ctx={ctx} />
    case 'addons': return <Addons ctx={ctx} />
    case 'changes': return <Changes ctx={ctx} />
    case 'repository': return <Repository ctx={ctx} />
    case 'workloads': return <Workloads ctx={ctx} />
    case 'network': return <Network ctx={ctx} />
    case 'storage': return <Storage ctx={ctx} />
    case 'backups': return <Backups ctx={ctx} />
    case 'secrets': return <Secrets ctx={ctx} />
    default: return <Redirect to={legacy(ctx.name, section)} />
  }
}

function CheckNow({ name }: { name: string }) {
  const observed = statuses.value.get(name)?.observedAt ?? ''
  const [since, setSince] = useState<string | null>(null)
  const busy = since !== null && since === observed
  const check = () => {
    setSince(observed)
    api.checkNow(name).catch((e) => { setSince(null); toast(e.message, 'error') })
  }
  return <button class="btn btn-sm" disabled={busy} onClick={check}>{busy ? 'Checking' : 'Check now'}</button>
}

function legacy(name: string, section: string) {
  if (section !== 'config') return `/clusters/${name}/overview`
  const view = new URLSearchParams(location.search).get('view')
  if (view === 'yaml') return `/clusters/${name}/repository`
  if (view === 'repo') return `/clusters/${name}/repository?view=git`
  if (view === 'certs') return `/clusters/${name}/repository?view=certs`
  return `/clusters/${name}/changes`
}

function Redirect({ to }: { to: string }) {
  const { route } = useLocation()
  useEffect(() => { route(to, true) }, [to])
  return null
}
