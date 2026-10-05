import type { ComponentChildren } from 'preact'
import { computed } from '@preact/signals'
import { LocationProvider, Route, Router, useLocation } from 'preact-iso'
import { useEffect, useState } from 'preact/hooks'
import { api, fmt } from './api'
import { ActivityDrawer } from './components/ActivityDrawer'
import { Palette, Shortcuts, ThemeToggle } from './components/Palette'
import { Elapsed } from './components/Time'
import { Toasts } from './components/Toasts'
import { ClusterPill, ConfirmDialog, Pill } from './components/ui'
import { connectLive } from './live'
import { ClusterPage } from './pages/cluster/ClusterPage'
import { Discovery } from './pages/Discovery'
import { Home } from './pages/Home'
import { NodePage } from './pages/node/NodePage'
import { Operations } from './pages/Operations'
import { Secrets } from './pages/Secrets'
import { runningCount } from './ops'
import { clusters, connected, daemon, drawerHeight, drawerOpen, machineList, reconnectAttempt, resyncing, statuses, stopped, toast } from './store'

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

const redirects: Record<string, string> = { '/start': '/', '/fleet/inventory': '/discovery', '/fleet/network-boot': '/discovery', '/fleet/pxe': '/discovery' }
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
        <div class="px-4 pt-5 pb-1 label">Kubit</div>
        <NavLink href="/discovery" path={path}>Discovery <DiscoveryBadge /></NavLink>
        <NavLink href="/secrets" path={path}>Secrets</NavLink>
        <NavLink href="/operations" path={path}>Activity <RunningBadge /></NavLink>
        <StopKubit />
        <div class="px-4 py-2.5 text-[11px] text-muted border-t border-border flex items-center gap-2">
          <ConnectionDot />
          <DaemonUptime />
          <span class="ml-auto flex items-center gap-2"><ThemeToggle /><button class="hover:text-text" title="Jump to (⌘K)" onClick={() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))}>⌘K</button><button class="hover:text-text" title="Keyboard shortcuts" onClick={() => window.dispatchEvent(new KeyboardEvent('keydown', { key: '?' }))}>?</button></span>
        </div>
      </nav>
      <main class="flex-1 min-w-0 overflow-auto" style={mainPad}>
        <ReconnectBanner />
        <Router>
          <Route path="/clusters/:name" component={ClusterPage} />
          <Route path="/clusters/:name/:section" component={ClusterPage} />
          <Route path="/clusters/:name/:section/:sub" component={ClusterPage} />
          <Route path="/nodes/:ip" component={NodePage} />
          <Route path="/machines/:mac" component={NodePage} />
          <Route path="/discovery" component={Discovery} />
          <Route path="/secrets" component={Secrets} />
          <Route path="/operations" component={Operations} />
          <Route path="/operations/:id" component={Operations} />
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


function DiscoveryBadge() {
  const n = machineList.value.filter((m) => m.kind === 'maintenance').length
  return n > 0 ? <Pill tone="good">{n}</Pill> : null
}

function RunningBadge() {
  return runningCount.value > 0 ? <Pill tone="warn">{runningCount.value}</Pill> : null
}

function StopKubit() {
  const [confirm, setConfirm] = useState(false)
  const busy = runningCount.value > 0
  return (
    <div class="mt-auto px-4 py-2 border-t border-border">
      <button class="text-[12px] text-muted hover:text-bad disabled:opacity-50" disabled={!connected.value || busy} title={busy ? 'Operations are running' : ''} onClick={() => setConfirm(true)}>Stop Kubit</button>
      {confirm && <ConfirmDialog title="Stop Kubit" action="Stop Kubit" tone="danger" onClose={() => setConfirm(false)}
        onConfirm={() => api.stopDaemon().then(() => { stopped.value = true; setConfirm(false) }).catch((e) => toast(e.message, 'error'))}
        impact={<p>Alerts and discovery pause until you run <span class="mono">kubit</span> again.</p>} />}
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
