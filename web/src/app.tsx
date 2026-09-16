import { LocationProvider, Router, Route, useLocation } from 'preact-iso'
import { useEffect } from 'preact/hooks'
import { clusters, connected, daemon, drawerHeight, drawerOpen, reconnectAttempt, resyncing, running } from './store'
import { connectLive } from './live'
import { Pill, stateTone } from './components/ui'
import { ActivityDrawer } from './components/ActivityDrawer'
import { Toasts } from './components/Toasts'
import { Palette, Shortcuts, ThemeToggle } from './components/Palette'
import { ClusterPage } from './pages/cluster/ClusterPage'
import { NewCluster } from './pages/create/NewCluster'
import { NodePage } from './pages/Node'
import { Operations } from './pages/Operations'
import { Inventory } from './pages/fleet/Inventory'
import { GettingStarted } from './pages/GettingStarted'
import { KubitSettings } from './pages/KubitSettings'

export function App() {
  useEffect(() => { connectLive() }, [])
  return (
    <LocationProvider>
      <Shell />
      <Palette />
      <Shortcuts />
      <Toasts />
    </LocationProvider>
  )
}

const sections = [
  ['overview', 'Overview'], ['nodes', 'Nodes'], ['workloads', 'Workloads'], ['network', 'Network'],
  ['storage', 'Storage'], ['addons', 'Add-ons'], ['backups', 'Backups'], ['settings', 'Settings'],
] as const
export type Section = typeof sections[number][0]
export const sectionList = sections

function Shell() {
  const { path, route } = useLocation()
  const list = clusters.value
  useEffect(() => { if (path === '/' && list.length > 0) route(`/clusters/${list[0].name}/overview`, true) }, [path, list, route])
  const activeCluster = /^\/clusters\/([^/]+)/.exec(path)?.[1]
  const pad = drawerOpen.value ? drawerHeight.value : 0

  return (
    <div class="flex h-full min-h-screen">
      <nav class="w-56 shrink-0 border-r border-border bg-panel flex flex-col overflow-y-auto">
        <a href="/" class="px-4 py-3.5 border-b border-border flex items-center gap-2">
          <span class="inline-block h-2.5 w-2.5 rounded-sm bg-accent" />
          <span class="font-semibold tracking-tight">Kubit</span>
        </a>
        <div class="px-4 pt-4 pb-1 label">Clusters</div>
        {list.map((c) => (
          <a key={c.name} href={`/clusters/${c.name}/overview`} class={`mx-2 rounded-md px-2 py-1.5 flex items-center justify-between hover:bg-panel-2 ${activeCluster === c.name ? 'bg-panel-2' : ''}`}>
            <span class="truncate font-medium">{c.name}</span>
            <Pill tone={stateTone(c.state)}>{c.state}</Pill>
          </a>
        ))}
        <a href="/clusters/new" class={`mx-2 mt-1 rounded-md px-2 py-1.5 text-accent hover:bg-panel-2 ${path === '/clusters/new' ? 'bg-panel-2' : ''}`}>+ New cluster</a>
        <div class="px-4 pt-5 pb-1 label">Fleet</div>
        <NavLink href="/fleet/inventory" path={path}>Inventory</NavLink>
        <div class="px-4 pt-5 pb-1 label">Kubit</div>
        <NavLink href="/operations" path={path}>Activity {running.value.length > 0 && <Pill tone="warn">{running.value.length}</Pill>}</NavLink>
        <NavLink href="/settings" path={path}>Settings</NavLink>
        <div class="mt-auto px-4 py-2.5 text-[11px] text-muted border-t border-border flex items-center gap-2">
          <span class={`inline-block h-1.5 w-1.5 rounded-full ${connected.value ? (resyncing.value ? 'bg-warn animate-pulse' : 'bg-good') : 'bg-bad animate-pulse'}`} />
          {connected.value ? (resyncing.value ? 'resyncing…' : 'live') : `reconnecting${reconnectAttempt.value > 1 ? ` (${reconnectAttempt.value})` : ''}…`}
          <DaemonMode />
          <span class="ml-auto flex items-center gap-2"><ThemeToggle /><button class="hover:text-text" title="Jump to… (⌘K)" onClick={() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))}>⌘K</button><button class="hover:text-text" title="Keyboard shortcuts" onClick={() => window.dispatchEvent(new KeyboardEvent('keydown', { key: '?' }))}>?</button></span>
        </div>
      </nav>
      <main class="flex-1 min-w-0 overflow-auto" style={{ paddingBottom: pad }}>
        {!connected.value && <div class="sticky top-0 z-30 bg-warn/15 border-b border-warn/40 text-warn text-[12.5px] px-4 py-1.5">Live updates paused — reconnecting to the Kubit daemon{reconnectAttempt.value > 1 ? ` (attempt ${reconnectAttempt.value})` : ''}… What you see may be stale.</div>}
        <Router>
          <Route path="/clusters/new" component={NewCluster} />
          <Route path="/clusters/:name" component={ClusterPage} />
          <Route path="/clusters/:name/:section" component={ClusterPage} />
          <Route path="/clusters/:name/:section/:sub" component={ClusterPage} />
          <Route path="/nodes/:ip" component={NodePage} />
          <Route path="/machines/:mac" component={NodePage} />
          <Route path="/fleet/inventory" component={Inventory} />
          <Route path="/start" component={GettingStarted} />
          <Route path="/operations" component={Operations} />
          <Route path="/operations/:id" component={Operations} />
          <Route path="/settings" component={KubitSettings} />
          <Route default component={Home} />
        </Router>
      </main>
      <ActivityDrawer />
    </div>
  )
}

function NavLink({ href, path, children }: { href: string; path: string; children: preact.ComponentChildren }) {
  const active = path === href || path.startsWith(href + '/')
  return <a href={href} class={`mx-2 rounded-md px-2 py-1.5 flex items-center justify-between hover:bg-panel-2 ${active ? 'bg-panel-2' : ''}`}>{children}</a>
}

function Home() {
  if (clusters.value.length > 0) return null
  return <GettingStarted />
}

/** Whether the daemon is a supervised service (alerts and snapshots keep running unattended) or a foreground process. */
function DaemonMode() {
  const v = daemon.value
  if (!v) return null
  return <span title={`kubit ${v.version}, up since ${new Date(v.startedAt).toLocaleString()}`}>· {v.service ? 'service' : 'foreground'}</span>
}
