import { LocationProvider, Router, Route, useLocation } from 'preact-iso'
import { useCallback, useEffect, useState } from 'preact/hooks'
import { api, type ClusterRow, type Operation } from './api'
import { useOperationUpdates } from './events'
import { Pill, stateTone } from './components/ui'
import { Dashboard } from './pages/Dashboard'
import { NewCluster } from './pages/NewCluster'
import { NodePage } from './pages/Node'
import { Operations } from './pages/Operations'
import { Inventory } from './pages/Inventory'

export function App() {
  return (
    <LocationProvider>
      <Shell />
    </LocationProvider>
  )
}

function Shell() {
  const { path, route } = useLocation()
  const [clusters, setClusters] = useState<ClusterRow[]>([])
  const [running, setRunning] = useState<Operation[]>([])
  const reload = useCallback(() => { api.clusters().then(setClusters).catch(() => {}) }, [])
  useEffect(() => { reload(); api.operations().then((ops) => setRunning(ops.filter((o) => o.status === 'running'))).catch(() => {}) }, [reload])
  useOperationUpdates(useCallback((op: Operation) => {
    setRunning((prev) => op.status === 'running' ? [...prev.filter((p) => p.id !== op.id), op] : prev.filter((p) => p.id !== op.id))
    if (op.status !== 'running') reload()
  }, [reload]))
  useEffect(() => {
    if (path === '/' && clusters.length > 0) route(`/clusters/${clusters[0].name}`, true)
  }, [path, clusters, route])

  return (
    <div class="flex h-full min-h-screen">
      <nav class="w-56 shrink-0 border-r border-border bg-panel flex flex-col">
        <a href="/" class="px-4 py-4 border-b border-border flex items-center gap-2">
          <span class="inline-block h-2.5 w-2.5 rounded-sm bg-accent" />
          <span class="font-semibold tracking-tight">Kubit</span>
        </a>
        <div class="px-4 pt-4 pb-1 label">Clusters</div>
        {clusters.map((c) => (
          <a key={c.name} href={`/clusters/${c.name}`} class={`mx-2 rounded-md px-2 py-1.5 flex items-center justify-between hover:bg-panel-2 ${path.startsWith(`/clusters/${c.name}`) ? 'bg-panel-2' : ''}`}>
            <span class="truncate">{c.name}</span>
            <Pill tone={stateTone(c.state)}>{c.state}</Pill>
          </a>
        ))}
        <a href="/clusters/new" class={`mx-2 mt-1 rounded-md px-2 py-1.5 text-accent hover:bg-panel-2 ${path === '/clusters/new' ? 'bg-panel-2' : ''}`}>+ New cluster</a>
        <div class="px-4 pt-5 pb-1 label">Fleet</div>
        <a href="/inventory" class={`mx-2 rounded-md px-2 py-1.5 hover:bg-panel-2 ${path === '/inventory' ? 'bg-panel-2' : ''}`}>Discovered nodes</a>
        <a href="/operations" class={`mx-2 rounded-md px-2 py-1.5 hover:bg-panel-2 flex items-center justify-between ${path.startsWith('/operations') ? 'bg-panel-2' : ''}`}>
          <span>Operations</span>
          {running.length > 0 && <Pill tone="warn">{running.length} running</Pill>}
        </a>
        <div class="mt-auto px-4 py-3 text-[11px] text-muted border-t border-border">Talos · Kubernetes · OpenTofu</div>
      </nav>
      <main class="flex-1 min-w-0 overflow-auto">
        <Router>
          <Route path="/clusters/new" component={NewCluster} />
          <Route path="/clusters/:name" component={Dashboard} />
          <Route path="/nodes/:ip" component={NodePage} />
          <Route path="/inventory" component={Inventory} />
          <Route path="/operations" component={Operations} />
          <Route path="/operations/:id" component={Operations} />
          <Route default component={Home} clusters={clusters} />
        </Router>
      </main>
    </div>
  )
}

function Home({ clusters }: { clusters: ClusterRow[] }) {
  if (clusters.length > 0) return null
  return (
    <div class="p-10 max-w-xl flex flex-col gap-3">
      <h1 class="text-xl font-semibold">No clusters yet</h1>
      <p class="text-muted">Boot machines from a Talos ISO or over PXE, then discover them and create a cluster.</p>
      <a href="/clusters/new" class="btn btn-primary self-start">Create a cluster</a>
    </div>
  )
}
