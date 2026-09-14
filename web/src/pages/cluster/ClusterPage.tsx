import { useCallback, useEffect, useState } from 'preact/hooks'
import { api, type ClusterRow, type Status } from '../../api'
import { clusters, operations } from '../../store'
import { Tabs } from '../../components/Tabs'
import { ErrorBox, Pill, stateTone } from '../../components/ui'
import { sectionList, type Section } from '../../app'
import { Overview } from './Overview'
import { Nodes } from './Nodes'
import { Addons } from './Addons'
import { PlanReview } from './PlanReview'
import { ClusterOperations } from './ClusterOperations'
import { Settings } from './Settings'
import { Placeholder } from './Placeholder'

export interface ClusterCtx { name: string; cluster: ClusterRow; status: Status | null; refresh: () => void; error: string | null }

/** Cluster scope: header + section tabs; sections render below. */
export function ClusterPage({ name, section = 'overview', sub }: { name: string; section?: string; sub?: string }) {
  const [status, setStatus] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)
  const cluster = clusters.value.find((c) => c.name === name)
  const refresh = useCallback(() => { api.status(name).then((s) => { setStatus(s); setError(null) }).catch((e) => setError(e.message)) }, [name])
  useEffect(() => { setStatus(null); refresh(); const t = setInterval(refresh, 10000); return () => clearInterval(t) }, [refresh])
  // Refresh when an operation on this cluster finishes.
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
        <Tabs active={section} tabs={sectionList.map(([id, label]) => ({ id, label, href: `/clusters/${name}/${id}`, badge: id === 'operations' ? runningHere.length : undefined }))} />
      </header>
      <div class="p-6 flex flex-col gap-5 max-w-[1300px]">
        <ErrorBox error={error} />
        {renderSection(section as Section, sub, ctx)}
      </div>
    </div>
  )
}

function renderSection(section: Section, sub: string | undefined, ctx: ClusterCtx) {
  switch (section) {
    case 'overview': return <Overview ctx={ctx} />
    case 'nodes': return <Nodes ctx={ctx} />
    case 'addons': return sub ? <PlanReview ctx={ctx} planId={Number(sub)} /> : <Addons ctx={ctx} />
    case 'operations': return <ClusterOperations ctx={ctx} />
    case 'settings': return <Settings ctx={ctx} />
    case 'workloads': return <Placeholder title="Workloads" milestone="M4">Namespaces, deployments, daemonsets, statefulsets and pods with logs and events.</Placeholder>
    case 'network': return <Placeholder title="Network" milestone="M4">Services with their LoadBalancer IPs, MetalLB pool usage, ingress hosts, VIP and CIDRs.</Placeholder>
    case 'storage': return <Placeholder title="Storage" milestone="M4">Storage classes, persistent volumes and claims.</Placeholder>
    default: return <Placeholder title="Not found" milestone="">Unknown section.</Placeholder>
  }
}
