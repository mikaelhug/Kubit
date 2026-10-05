import type { ComponentChildren } from 'preact'
import { computed } from '@preact/signals'
import { LocationProvider, Route, Router, useLocation } from 'preact-iso'
import { useEffect } from 'preact/hooks'
import { api, fmt } from './api'
import { ActivityDrawer } from './components/ActivityDrawer'
import { Palette, Shortcuts, ThemeToggle } from './components/Palette'
import { Elapsed } from './components/Time'
import { Toasts } from './components/Toasts'
import { ClusterPill, Pill } from './components/ui'
import { connectLive, reconnectLive } from './live'
import { hostName, labState, vmsOf } from './machine'
import { ClusterPage } from './pages/cluster/ClusterPage'
import { NewCluster } from './pages/create/NewCluster'
import { Inventory } from './pages/fleet/Inventory'
import { NetworkBoot } from './pages/fleet/NetworkBoot'
import { Home } from './pages/Home'
import { KubitSettings } from './pages/KubitSettings'
import { LabHostPage } from './pages/LabHost'
import { NodePage } from './pages/node/NodePage'
import { Operations } from './pages/Operations'
import { SignIn } from './pages/SignIn'
import { runningCount } from './ops'
import { clusters, connected, daemon, drawerHeight, drawerOpen, labHosts, loadMe, me, reconnectAttempt, resyncing, statuses, stopped, toast } from './store'
import { stateTone } from './tone'

export function App() {
  useEffect(() => { loadMe().then(() => connectLive()).catch((e) => toast(e.message, 'error')) }, [])
  if (me.value === undefined) return <div class="min-h-screen bg-bg" />
  if (me.value === null) return <><SignIn /><Toasts /></>
  return (
    <LocationProvider>
      <Shell />
      <Palette />
      <Shortcuts />
      <Toasts />
    </LocationProvider>
  )
}

const redirects: Record<string, string> = { '/start': '/', '/fleet/pxe': '/fleet/network-boot', '/settings': '/settings/general' }
const mainPad = computed(() => `padding-bottom:${drawerOpen.value ? drawerHeight.value : 0}px`)

function Shell() {
  const { path, route } = useLocation()
  useEffect(() => { if (redirects[path]) route(redirects[path], true) }, [path, route])
  const activeCluster = /^\/clusters\/([^/]+)/.exec(path)?.[1]

  return (
    <div class="flex h-full min-h-screen">
      <nav class="w-52 shrink-0 border-r border-border bg-panel flex flex-col overflow-y-auto">
        <a href="/" class="px-4 py-3.5 border-b border-border flex items-center gap-2">
          <span class="inline-block h-2.5 w-2.5 rounded-sm bg-accent" />
          <span class="font-semibold tracking-tight">Kubit</span>
        </a>
        <div class="mt-2"><NavLink href="/" path={path} exact>Home</NavLink></div>
        <div class="px-4 pt-4 pb-1 label">Clusters</div>
        <ClusterNav active={activeCluster} />
        <a href="/clusters/new" class={`${navCls(path === '/clusters/new')} mt-0.5 text-accent`}>+ New cluster</a>
        <LabHostNav path={path} />
        <div class="px-4 pt-5 pb-1 label">Fleet</div>
        <NavLink href="/fleet/inventory" path={path}>Inventory</NavLink>
        <NavLink href="/fleet/network-boot" path={path}>Network boot</NavLink>
        <div class="px-4 pt-5 pb-1 label">Kubit</div>
        <NavLink href="/operations" path={path}>Activity <RunningBadge /></NavLink>
        <NavLink href="/settings" path={path}>Settings</NavLink>
        <Account />
        <div class="px-4 py-2.5 text-[11px] text-muted border-t border-border flex items-center gap-2">
          <ConnectionDot />
          <DaemonUptime />
          <span class="ml-auto flex items-center gap-2"><ThemeToggle /><button class="hover:text-text" title="Jump to (⌘K)" onClick={() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))}>⌘K</button><button class="hover:text-text" title="Keyboard shortcuts" onClick={() => window.dispatchEvent(new KeyboardEvent('keydown', { key: '?' }))}>?</button></span>
        </div>
      </nav>
      <main class="flex-1 min-w-0 overflow-auto" style={mainPad}>
        <ReconnectBanner />
        <Router>
          <Route path="/clusters/new" component={NewCluster} />
          <Route path="/clusters/:name" component={ClusterPage} />
          <Route path="/clusters/:name/:section" component={ClusterPage} />
          <Route path="/clusters/:name/:section/:sub" component={ClusterPage} />
          <Route path="/nodes/:ip" component={NodePage} />
          <Route path="/machines/:mac" component={NodePage} />
          <Route path="/labhosts/:mac" component={LabHostPage} />
          <Route path="/labhosts/:mac/:tab" component={LabHostPage} />
          <Route path="/fleet/inventory" component={Inventory} />
          <Route path="/fleet/network-boot" component={NetworkBoot} />
          <Route path="/operations" component={Operations} />
          <Route path="/operations/:id" component={Operations} />
          <Route path="/settings/:page" component={KubitSettings} />
          <Route path="/" component={Home} />
          <Route default component={Home} />
        </Router>
      </main>
      <ActivityDrawer />
    </div>
  )
}

const navCls = (active: boolean) => `pl-[14px] pr-3 py-1.5 border-l-2 hover:bg-panel-2 ${active ? 'bg-panel-2 border-accent' : 'border-transparent'}`

function NavLink({ href, path, children, exact }: { href: string; path: string; children: ComponentChildren; exact?: boolean }) {
  const active = path === href || (!exact && path.startsWith(href + '/'))
  return <a href={href} class={`${navCls(active)} flex items-center justify-between`}>{children}</a>
}

function ClusterNav({ active }: { active?: string }) {
  return (
    <>
      {clusters.value.map((c) => (
        <a key={c.name} href={`/clusters/${c.name}/overview`} class={`${navCls(active === c.name)} flex items-center justify-between`}>
          <span class="truncate font-medium">{c.name}</span>
          <ClusterPill state={c.state} status={statuses.value.get(c.name)} />
        </a>
      ))}
    </>
  )
}

function LabHostNav({ path }: { path: string }) {
  const hosts = labHosts.value
  if (hosts.length === 0) return null
  return (
    <>
      <div class="px-4 pt-5 pb-1 label">Lab hosts</div>
      {hosts.map((h) => (
        <a key={h.mac} href={`/labhosts/${h.mac}/overview`} class={`${navCls(path.startsWith(`/labhosts/${h.mac}`))} flex items-center justify-between`}>
          <span class="truncate font-medium">{hostName(h)}</span>
          <Pill tone={stateTone(labState(h.labhost))} title={`${vmsOf(h.labhost).length} VMs`}>{labState(h.labhost)}</Pill>
        </a>
      ))}
    </>
  )
}

function RunningBadge() {
  return runningCount.value > 0 ? <Pill tone="warn">{runningCount.value}</Pill> : null
}

function Account() {
  const m = me.value
  return (
    <div class="mt-auto px-4 py-2 text-[11px] text-muted border-t border-border flex items-center gap-2">
      <span class="truncate" title={`${m?.role} via ${m?.via}`}>{m?.via === 'loopback' ? 'no accounts yet' : m?.user}</span>
      <span class="text-[10px] uppercase tracking-wide">{m?.role}</span>
      {m?.via === 'session' && <button class="ml-auto hover:text-text" onClick={() => api.logout().then(() => loadMe()).then(() => reconnectLive())}>Sign out</button>}
    </div>
  )
}

function ConnectionDot() {
  const live = connected.value
  const attempt = reconnectAttempt.value
  return <span class={`inline-block h-1.5 w-1.5 rounded-full shrink-0 ${live ? (resyncing.value ? 'bg-warn animate-pulse' : 'bg-good') : 'bg-bad animate-pulse'}`} title={live ? (resyncing.value ? 'resyncing' : 'live') : stopped.value ? 'stopped' : `reconnecting${attempt > 1 ? ` (${attempt})` : ''}`} />
}

function ReconnectBanner() {
  if (connected.value) return null
  const attempt = reconnectAttempt.value
  return <div class="sticky top-0 z-30 bg-warn/15 border-b border-warn/40 text-warn text-[12.5px] px-4 py-1.5">{stopped.value ? <>Kubit stopped; start it with <span class="mono">kubit</span>.</> : `Live updates paused; reconnecting${attempt > 1 ? ` (attempt ${attempt})` : ''}.`}</div>
}

function DaemonUptime() {
  const v = daemon.value
  if (!v) return null
  return <span class="whitespace-nowrap" title={`kubit ${v.version}, up since ${fmt.datetime(v.startedAt)}`}>uptime <Elapsed from={v.startedAt} /></span>
}
