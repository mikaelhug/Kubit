import type { ComponentChildren } from 'preact'
import type { Event, Level } from '../api'

export function Pill({ tone, children }: { tone: 'good' | 'warn' | 'bad' | 'info' | 'muted'; children: ComponentChildren }) {
  const cls = {
    good: 'bg-good/15 text-good', warn: 'bg-warn/15 text-warn', bad: 'bg-bad/15 text-bad',
    info: 'bg-info/15 text-info', muted: 'bg-panel-2 text-muted',
  }[tone]
  return <span class={`pill ${cls}`}>{children}</span>
}

export function stateTone(state: string): 'good' | 'warn' | 'bad' | 'info' | 'muted' {
  switch (state) {
    case 'ready': case 'done': case 'running': case 'maintenance': return 'good'
    case 'failed': return 'bad'
    case 'provisioning': case 'installing': case 'bootstrapped': case 'booting': return 'warn'
    default: return 'muted'
  }
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
      <div class="h-1.5 w-full rounded-full bg-panel-2 overflow-hidden">
        <div class="h-full rounded-full transition-[width]" style={{ width: pct + '%', background: tone }} />
      </div>
    </div>
  )
}

export function EventLine({ e }: { e: Event }) {
  const color: Record<Level, string> = { info: 'text-text', warn: 'text-warn', error: 'text-bad', done: 'text-good' }
  return (
    <div class={`flex gap-2 ${color[e.level]}`}>
      <span class="text-muted shrink-0">{new Date(e.time).toLocaleTimeString()}</span>
      <span class="text-muted shrink-0">[{e.step}]</span>
      {e.node && <span class="shrink-0">{e.node}:</span>}
      <span class="min-w-0 break-words">{e.message}</span>
    </div>
  )
}

export function EventLog({ events, empty = 'No output yet.' }: { events: Event[]; empty?: string }) {
  return (
    <div class="log" ref={(el) => { if (el) el.scrollTop = el.scrollHeight }}>
      {events.length === 0 ? <span class="text-muted">{empty}</span> : events.map((e, i) => <EventLine key={i} e={e} />)}
    </div>
  )
}

export function Dialog({ title, onClose, children, width = 'max-w-lg' }: { title: string; onClose: () => void; children: ComponentChildren; width?: string }) {
  return (
    <div class="fixed inset-0 z-40 flex items-start justify-center bg-black/50 p-6 overflow-auto" onClick={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div class={`panel w-full ${width} mt-10 p-5 flex flex-col gap-4`} role="dialog" aria-modal="true">
        <div class="flex items-center justify-between">
          <h2 class="text-base font-semibold">{title}</h2>
          <button class="btn !px-2" onClick={onClose} aria-label="Close">✕</button>
        </div>
        {children}
      </div>
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

export function ErrorBox({ error }: { error: string | null }) {
  if (!error) return null
  return <div class="rounded-md border border-bad/40 bg-bad/10 px-3 py-2 text-[13px] text-bad break-words">{error}</div>
}
