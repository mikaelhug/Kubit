import { LocationProvider, Router, Route, useLocation } from 'preact-iso'
import { useEffect } from 'preact/hooks'
import { clusters, connected, daemon, drawerHeight, drawerOpen, loadMe, machineList, me, reconnectAttempt, resyncing, running, statuses, toast } from './store'
import { connectLive, reconnectLive } from './live'
import { api } from './api'
import { SignIn } from './pages/SignIn'
import { now } from './clock'
import { ClusterPill, Pill, stateTone } from './components/ui'
import { hostName } from './machine'
import { ActivityDrawer } from './components/ActivityDrawer'
import { Toasts } from './components/Toasts'
import { Palette, Shortcuts, ThemeToggle } from './components/Palette'
import { ClusterPage } from './pages/cluster/ClusterPage'
import { NewCluster } from './pages/create/NewCluster'
import { NodePage } from './pages/Node'
import { LabHostPage } from './pages/LabHost'
import { Operations } from './pages/Operations'
import { Inventory } from './pages/fleet/Inventory'
import { NetworkBoot } from './pages/fleet/NetworkBoot'
import { Home } from './pages/Home'
import { KubitSettings } from './pages/KubitSettings'

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

const sections = [
  ['overview', 'Overview'], ['nodes', 'Nodes'], ['workloads', 'Workloads'], ['network', 'Network'],
  ['storage', 'Storage'], ['addons', 'Add-ons'], ['backups', 'Backups'], ['lifecycle', 'Lifecycle'], ['settings', 'Settings'],
] as const
export type Section = typeof sections[number][0]
export const sectionList = sections
export const settingsPages = [
  ['general', 'General'], ['discovery', 'Discovery'], ['alerts', 'Alerts'], ['offsite', 'Off-site'],
  ['accounts', 'Accounts'], ['sso', 'Single sign-on'], ['backup', 'Backup'],
] as const
export type SettingsPage = typeof settingsPages[number][0]

function Shell() {
  const { path, route } = useLocation()
  const list = clusters.value
  const labHosts = machineList.value.filter((m) => m.labhost)
  const redirects: Record<string, string> = { '/start': '/', '/fleet/pxe': '/fleet/network-boot', '/settings': '/settings/general' }
  useEffect(() => { if (redirects[path]) route(redirects[path], true) }, [path, route])
  const activeCluster = /^\/clusters\/([^/]+)/.exec(path)?.[1]
  const pad = drawerOpen.value ? drawerHeight.value : 0

  return (
    <div class="flex h-full min-h-screen">
      <nav class="w-52 shrink-0 border-r border-border bg-panel flex flex-col overflow-y-auto">
        <a href="/" class="px-4 py-3.5 border-b border-border flex items-center gap-2">
          <span class="inline-block h-2.5 w-2.5 rounded-sm bg-accent" />
          <span class="font-semibold tracking-tight">Kubit</span>
        </a>
        <div class="mt-2"><NavLink href="/" path={path} exact>Home</NavLink></div>
        <div class="px-4 pt-4 pb-1 label">Clusters</div>
        {list.map((c) => (
          <a key={c.name} href={`/clusters/${c.name}/overview`} class={`${navCls(activeCluster === c.name)} flex items-center justify-between`}>
            <span class="truncate font-medium">{c.name}</span>
            <ClusterPill state={c.state} status={statuses.value.get(c.name)} />
          </a>
        ))}
        <a href="/clusters/new" class={`${navCls(path === '/clusters/new')} mt-0.5 text-accent`}>+ New cluster</a>
        {labHosts.length > 0 && <div class="px-4 pt-5 pb-1 label">Lab hosts</div>}
        {labHosts.map((h) => (
          <a key={h.mac} href={`/labhosts/${h.mac}/overview`} class={`${navCls(path.startsWith(`/labhosts/${h.mac}`))} flex items-center justify-between`}>
            <span class="truncate font-medium">{hostName(h)}</span>
            <Pill tone={stateTone(h.labhost!.state)} title={`${(h.labhost!.vms ?? []).length} VMs`}>{h.labhost!.state}</Pill>
          </a>
        ))}
        <div class="px-4 pt-5 pb-1 label">Fleet</div>
        <NavLink href="/fleet/inventory" path={path}>Inventory</NavLink>
        <NavLink href="/fleet/network-boot" path={path}>Network boot</NavLink>
        <div class="px-4 pt-5 pb-1 label">Kubit</div>
        <NavLink href="/operations" path={path}>Activity {running.value.length > 0 && <Pill tone="warn">{running.value.length}</Pill>}</NavLink>
        <NavLink href="/settings" path={path}>Settings</NavLink>
        <div class="mt-auto px-4 py-2 text-[11px] text-muted border-t border-border flex items-center gap-2">
          <span class="truncate" title={`${me.value?.role} via ${me.value?.via}`}>{me.value?.via === 'loopback' ? 'no accounts yet' : me.value?.user}</span>
          <span class="text-[10px] uppercase tracking-wide">{me.value?.role}</span>
          {me.value?.via === 'session' && <button class="ml-auto hover:text-text" onClick={() => api.logout().then(() => loadMe()).then(() => reconnectLive())}>Sign out</button>}
        </div>
        <div class="px-4 py-2.5 text-[11px] text-muted border-t border-border flex items-center gap-2">
          <span class={`inline-block h-1.5 w-1.5 rounded-full shrink-0 ${connected.value ? (resyncing.value ? 'bg-warn animate-pulse' : 'bg-good') : 'bg-bad animate-pulse'}`} title={connected.value ? (resyncing.value ? 'resyncing' : 'live') : `reconnecting${reconnectAttempt.value > 1 ? ` (${reconnectAttempt.value})` : ''}`} />
          <DaemonUptime />
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

function NavLink({ href, path, children, exact }: { href: string; path: string; children: preact.ComponentChildren; exact?: boolean }) {
  const active = path === href || (!exact && path.startsWith(href + '/'))
  return <a href={href} class={`${navCls(active)} flex items-center justify-between`}>{children}</a>
}

function DaemonUptime() {
  const v = daemon.value
  if (!v) return null
  const s = Math.max(0, Math.floor((now.value - new Date(v.startedAt).getTime()) / 1000))
  const pad = (n: number) => String(n).padStart(2, '0')
  const clock = `${pad(Math.floor(s / 3600) % 24)}:${pad(Math.floor(s / 60) % 60)}:${pad(s % 60)}`
  const days = Math.floor(s / 86400)
  return <span class="num whitespace-nowrap" title={`kubit ${v.version} (${v.service ? 'service' : 'foreground'}), up since ${new Date(v.startedAt).toLocaleString()}`}>uptime {days > 0 ? `${days}d ` : ''}{clock}</span>
}
