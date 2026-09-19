import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, type MaintenanceState } from '../api'
import type { Event, Level } from '../api'

export type Tone = 'good' | 'warn' | 'bad' | 'info' | 'muted'

export function Pill({ tone, children, title }: { tone: Tone; children: ComponentChildren; title?: string }) {
  const cls = {
    good: 'bg-good/15 text-good', warn: 'bg-warn/15 text-warn', bad: 'bg-bad/15 text-bad',
    info: 'bg-info/15 text-info', muted: 'bg-panel-2 text-muted',
  }[tone]
  return <span class={`pill ${cls}`} title={title}>{children}</span>
}

export function stateTone(state: string): Tone {
  switch (state) {
    case 'ready': case 'done': case 'running': case 'maintenance': case 'joined': return 'good'
    case 'failed': case 'error': case 'down': return 'bad'
    case 'cancelled': case 'unknown': return 'muted'
    case 'provisioning': case 'installing': case 'bootstrapped': case 'booting': case 'pending': case 'discovered': case 'setup': case 'updating': case 'degraded': return 'warn'
    case 'amt': case 'off': case 'labhost': case 'configured': return 'info'
    default: return 'muted'
  }
}

/** The cluster pill: the lifecycle state, overridden by the watcher's verdict once ready. */
export function ClusterPill({ state, status }: { state: string; status?: { health?: string; openAlerts?: number } | null }) {
  const h = state === 'ready' && status?.health && status.health !== 'healthy' ? status.health : ''
  const label = h || state
  const title = h === 'degraded' ? `${status?.openAlerts ?? 0} open alert${status?.openAlerts === 1 ? '' : 's'}` : h === 'down' ? 'API, etcd or a node is unreachable' : undefined
  return <Pill tone={stateTone(label)} title={title}>{label}</Pill>
}

export function StatusDot({ tone, pulse }: { tone: Tone; pulse?: boolean }) {
  const bg = { good: 'bg-good', warn: 'bg-warn', bad: 'bg-bad', info: 'bg-info', muted: 'bg-border' }[tone]
  return <span class={`inline-block h-2 w-2 rounded-full ${bg} ${pulse ? 'animate-pulse' : ''}`} />
}

export function Meter({ label, used, cap, format }: { label: string; used: number; cap: number; format: (n: number) => string }) {
  const pct = cap ? Math.min(100, Math.round((used / cap) * 100)) : 0
  const tone = pct > 90 ? 'var(--bad)' : pct > 75 ? 'var(--warn)' : 'var(--accent)'
  return (
    <div class="flex flex-col gap-1.5">
      <div class="flex items-baseline justify-between">
        <span class="label">{label}</span>
        <span class="num text-[13px]"><strong>{format(used)}</strong> <span class="text-muted">/ {format(cap)} · {pct}%</span></span>
      </div>
      <div class="h-1 w-full bg-panel-2 overflow-hidden">
        <div class="h-full transition-[width]" style={{ width: pct + '%', background: tone }} />
      </div>
    </div>
  )
}

export function EventLine({ e, showStep = true }: { e: Event; showStep?: boolean }) {
  const color: Record<Level, string> = { info: 'text-text', warn: 'text-warn', error: 'text-bad', done: 'text-good' }
  return (
    <div class={`flex gap-2 leading-5 ${color[e.level] ?? 'text-text'}`}>
      {(e.time || e.clock) && <span class="text-muted shrink-0 select-none">{e.clock ?? new Date(e.time).toLocaleTimeString()}</span>}
      {showStep && e.step && <span class="text-muted shrink-0">[{e.step}]</span>}
      {e.node && <span class="shrink-0 text-accent">{e.node}</span>}
      <span class="min-w-0 break-words whitespace-pre-wrap">{e.message}</span>
    </div>
  )
}

export function EventLog({ events, empty = 'No output yet.', className = '' }: { events: Event[]; empty?: string; className?: string }) {
  return (
    <div class={`log ${className}`} ref={(el) => { if (el) el.scrollTop = el.scrollHeight }}>
      {events.length === 0 ? <span class="text-muted">{empty}</span> : events.map((e, i) => <EventLine key={i} e={e} />)}
    </div>
  )
}

export function Dialog({ title, onClose, children, width = 'max-w-lg', footer }: { title: string; onClose: () => void; children: ComponentChildren; width?: string; footer?: ComponentChildren }) {
  return (
    <div class="fixed inset-0 z-40 flex items-start justify-center bg-black/50 p-6 overflow-auto" onClick={(e) => { if (e.target === e.currentTarget) onClose() }} onKeyDown={(e) => { if (e.key === 'Escape') onClose() }}>
      <div class={`panel w-full ${width} mt-10 flex flex-col`} role="dialog" aria-modal="true">
        <div class="flex items-center justify-between px-5 py-4 border-b border-border">
          <h2 class="text-base font-semibold">{title}</h2>
          <button class="btn !px-2 !py-1" onClick={onClose} aria-label="Close">✕</button>
        </div>
        <div class="px-5 py-4 flex flex-col gap-4">{children}</div>
        {footer && <div class="px-5 py-3 border-t border-border flex justify-end gap-2">{footer}</div>}
      </div>
    </div>
  )
}

/** Confirm with an explicit impact list; the primary action names what happens. */
export function ConfirmDialog({ title, impact, action, tone = 'primary', onConfirm, onClose, typed, cluster }: { title: string; impact: ComponentChildren; action: string; tone?: 'primary' | 'danger'; onConfirm: () => void | Promise<unknown>; onClose: () => void; typed?: string; cluster?: string }) {
  let value = ''
  // One click only: the button stays disabled until a returned promise settles.
  const [busy, setBusy] = useState(false)
  const confirm = () => {
    if (busy) return
    const r = onConfirm()
    if (r && typeof (r as Promise<unknown>).then === 'function') {
      setBusy(true)
      ;(r as Promise<unknown>).finally(() => setBusy(false))
    }
  }
  return (
    <Dialog title={title} onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class={`btn ${tone === 'danger' ? 'btn-danger' : 'btn-primary'}`} id="confirm-action" disabled={!!typed || busy} onClick={confirm}>{busy ? 'Working' : action}</button>
      </>
    }>
      {cluster && <MaintenanceNotice cluster={cluster} />}
      <div class="text-[13px] flex flex-col gap-2">{impact}</div>
      {typed && (
        <Field label="To confirm, type">
          <input class="input mono" placeholder={typed} onInput={(e) => {
            value = (e.target as HTMLInputElement).value
            const btn = document.getElementById('confirm-action') as HTMLButtonElement | null
            // The label style uppercases text, so the comparison must not care about case.
            if (btn) btn.disabled = busy || value.trim().toLowerCase() !== typed.toLowerCase()
          }} />
        </Field>
      )}
    </Dialog>
  )
}

/** Warns when a disruptive action is about to start outside the cluster's maintenance window. */
export function MaintenanceNotice({ cluster }: { cluster: string }) {
  const [state, setState] = useState<MaintenanceState | null>(null)
  useEffect(() => { api.maintenance(cluster).then(setState).catch(() => {}) }, [cluster])
  if (!state || !state.window || state.open) return null
  return <Notice tone="warn">Outside the maintenance window <span class="mono">{state.window}{state.timezone ? ` ${state.timezone}` : ''}</span>{state.next ? `; it next opens ${new Date(state.next).toLocaleString()}` : ''}. Confirming runs it anyway.</Notice>
}

/** Small warning marker for a table row that has an open watcher alert. */
export function AlertPill({ e }: { e?: { severity: string; message: string; kind: string } }) {
  if (!e) return null
  return <Pill tone={e.severity === 'critical' ? 'bad' : 'warn'} title={e.message}>{e.kind.split('.')[1]}</Pill>
}

/** One-line command with a copy button. */
export function Code({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  const copy = () => { navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1500) }) }
  return (
    <div class="flex items-stretch gap-1">
      <code class="mono flex-1 min-w-0 rounded bg-bg border border-border px-3 py-2 select-all break-all">{text}</code>
      <button class="btn shrink-0" onClick={copy} title="Copy">{done ? 'Copied' : 'Copy'}</button>
    </div>
  )
}

export function Field({ label, children, hint }: { label: string; children: ComponentChildren; hint?: string }) {
  return (
    <label class="flex flex-col gap-1">
      <span class="label">{label}</span>
      {children}
      {hint && <span class="text-[12px] text-muted">{hint}</span>}
    </label>
  )
}

export function ErrorBox({ error }: { error: string | null | undefined }) {
  if (!error) return null
  return <div class="rounded-[var(--r)] border border-bad/40 bg-bad/10 px-3 py-2 text-[13px] text-bad break-words">{error}</div>
}

export function Notice({ tone = 'info', children }: { tone?: Tone; children: ComponentChildren }) {
  const cls = { good: 'border-good/40 bg-good/10 text-good', warn: 'border-warn/40 bg-warn/10 text-warn', bad: 'border-bad/40 bg-bad/10 text-bad', info: 'border-info/40 bg-info/10 text-info', muted: 'border-border bg-panel-2 text-muted' }[tone]
  return <div class={`rounded-[var(--r)] border px-3 py-2 text-[13px] ${cls}`}>{children}</div>
}

export function EmptyState({ title, children, action }: { title: string; children?: ComponentChildren; action?: ComponentChildren }) {
  return (
    <div class="panel p-8 flex flex-col items-start gap-2">
      <h3 class="font-semibold">{title}</h3>
      {children && <div class="text-[13px] text-muted max-w-prose">{children}</div>}
      {action && <div class="mt-2">{action}</div>}
    </div>
  )
}

export function KeyValue({ rows }: { rows: [string, ComponentChildren][] }) {
  return (
    <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5 text-[13px]">
      {rows.map(([k, v]) => (
        <>
          <dt class="text-muted">{k}</dt>
          <dd class="min-w-0 break-words">{v ?? <span class="text-muted">—</span>}</dd>
        </>
      ))}
    </dl>
  )
}

export function Breadcrumbs({ items }: { items: { label: string; href?: string }[] }) {
  return (
    <nav class="flex items-center gap-1.5 text-[13px] text-muted">
      {items.map((it, i) => (
        <>
          {i > 0 && <span class="text-border">/</span>}
          {it.href ? <a href={it.href} class="hover:text-text hover:underline">{it.label}</a> : <span class="text-text">{it.label}</span>}
        </>
      ))}
    </nav>
  )
}

export function Section({ title, children, actions, help }: { title: string; children: ComponentChildren; actions?: ComponentChildren; help?: string }) {
  return (
    <section class="flex flex-col gap-3">
      <div class="flex items-start justify-between gap-4">
        <div>
          <h2 class="font-semibold">{title}</h2>
          {help && <p class="text-[12px] text-muted mt-0.5 max-w-prose">{help}</p>}
        </div>
        {actions && <div class="flex gap-2 shrink-0">{actions}</div>}
      </div>
      {children}
    </section>
  )
}
