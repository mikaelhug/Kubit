import { Fragment, type ComponentChildren } from 'preact'
import { createPortal } from 'preact/compat'
import { useRef, useState } from 'preact/hooks'
import { fmt, type PlanSummary } from '../api'
import { now } from '../clock'
import { useEscape } from '../keys'
import { toast } from '../store'
import { stateTone, toneBg, toneBorder, tonePill, toneText, type Tone } from '../tone'

export function Pill({ tone, children, title }: { tone: Tone; children: ComponentChildren; title?: string }) {
  return <span class={`pill ${tonePill[tone]}`} title={title}>{children}</span>
}

export function planLabel(p?: PlanSummary): { text: string; tone: Tone; title?: string } {
  if (!p) return { text: 'checking', tone: 'muted' }
  switch (p.state) {
    case 'applying': return { text: 'applying', tone: 'info', title: p.holder ? `kubit apply by ${p.holder}` : undefined }
    case 'failed': return { text: 'plan failed', tone: 'bad', title: p.error }
    case 'blocked': return { text: `${p.problems} problem${p.problems === 1 ? '' : 's'}`, tone: 'bad' }
    case 'checking': return { text: 'planning', tone: 'muted' }
  }
  if (p.changes > 0) return { text: `${p.changes} change${p.changes === 1 ? '' : 's'}`, tone: 'info' }
  return { text: 'in sync', tone: 'good', title: p.oneTime ? 'One-time housekeeping pending' : undefined }
}

export function PlanPill({ plan, href }: { plan?: PlanSummary; href?: string }) {
  const l = planLabel(plan)
  const pill = <Pill tone={l.tone} title={l.title}>{l.text}</Pill>
  return href ? <a href={href} class="hover:opacity-80">{pill}</a> : pill
}

export function ClusterPill({ state, status }: { state: string; status?: { health?: string; openAlerts?: number; observerError?: string } | null }) {
  const h = state === 'ready' && status?.health && status.health !== 'healthy' ? status.health : ''
  const label = h || state
  const title = h === 'degraded' ? `${status?.openAlerts ?? 0} open alert${status?.openAlerts === 1 ? '' : 's'}` : h === 'unknown' ? `Kubit cannot reach the network${status?.observerError ? ` (${status.observerError})` : ''}` : undefined
  return <Pill tone={state === 'ready' && !status ? 'muted' : stateTone(label)} title={state === 'ready' && !status ? 'not observed yet' : title}>{label}</Pill>
}

export function SeenAgo({ contact, observed, blind }: { contact?: string; observed?: string; blind?: boolean }) {
  const at = contact || observed
  if (!at) return <span class="text-[12px] text-muted">not observed yet</span>
  const sec = (now.value - Date.parse(at)) / 1000
  const text = fmt.age(sec)
  const stale = blind || sec > 120
  return <span class={`text-[12px] ${stale ? 'text-warn' : 'text-muted'}`} title={`Last contact ${fmt.datetime(at)}`}>{stale && blind ? `not seen for ${text}` : `seen ${fmt.ago(sec)}`}</span>
}

export function StatusDot({ tone, pulse }: { tone: Tone; pulse?: boolean }) {
  return <span class={`inline-block h-2 w-2 rounded-full ${toneBg[tone]} ${pulse ? 'animate-pulse' : ''}`} />
}

export function Meter({ label, used, cap, format }: { label: string; used: number; cap: number; format: (n: number) => string }) {
  const pct = cap ? Math.min(100, Math.round((used / cap) * 100)) : 0
  const tone = pct > 90 ? 'var(--bad)' : pct > 75 ? 'var(--warn)' : 'var(--accent)'
  return (
    <div class="flex flex-col gap-1.5">
      <div class="flex items-baseline justify-between">
        <span class="label">{label}</span>
        <span class="text-[13px]"><strong>{format(used)}</strong> <span class="text-muted">/ {format(cap)} · {pct}%</span></span>
      </div>
      <div class="h-1 w-full bg-panel-2 overflow-hidden">
        <div class="h-full transition-[width]" style={{ width: pct + '%', background: tone }} />
      </div>
    </div>
  )
}


export function Dialog({ title, onClose, children, width = 'max-w-lg', footer }: { title: string; onClose: () => void; children?: ComponentChildren; width?: string; footer?: ComponentChildren }) {
  useEscape(onClose)
  const pressed = useRef(false)
  return createPortal(
    <div class="fixed inset-0 z-40 flex items-start justify-center bg-black/50 p-6 overflow-auto" onMouseDown={(e) => { pressed.current = e.target === e.currentTarget }} onClick={(e) => { if (pressed.current && e.target === e.currentTarget) onClose(); pressed.current = false }}>
      <div class={`panel w-full ${width} mt-10 flex flex-col`} role="dialog" aria-modal="true">
        <div class="flex items-center justify-between px-5 py-4 border-b border-border">
          <h2 class="text-base font-semibold">{title}</h2>
          <button class="btn btn-sm" onClick={onClose} aria-label="Close">✕</button>
        </div>
        {children != null && <div class="px-5 py-4 flex flex-col gap-4">{children}</div>}
        {footer && <div class={`px-5 py-3 flex justify-end gap-2 ${children != null ? 'border-t border-border' : ''}`}>{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}

export function ConfirmDialog({ title, impact, action, tone = 'primary', onConfirm, onClose }: { title: string; impact?: ComponentChildren; action: string; tone?: 'primary' | 'danger'; onConfirm: () => void | Promise<unknown>; onClose: () => void }) {
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
        <button class={`btn ${tone === 'danger' ? 'btn-danger' : 'btn-primary'}`} disabled={busy} onClick={confirm}>{busy ? 'Working' : action}</button>
      </>
    }>
      {impact != null ? <div class="text-[13px] flex flex-col gap-2">{impact}</div> : null}
    </Dialog>
  )
}


function copy(text: string) {
  return navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject(new Error('Clipboard unavailable'))
}

export function CopyButton({ text, className = 'btn', label = 'Copy' }: { text: string | (() => string); className?: string; label?: string }) {
  const [copiedAt, setCopiedAt] = useState(0)
  const click = () => copy(typeof text === 'function' ? text() : text).then(() => setCopiedAt(Date.now())).catch((e) => toast(e.message, 'error'))
  return <button class={className} onClick={click}>{now.value - copiedAt < 1500 ? 'Copied' : label}</button>
}

export function Code({ text }: { text: string }) {
  return (
    <div class="flex items-stretch gap-1">
      <code class="mono flex-1 min-w-0 rounded bg-bg border border-border px-3 py-2 select-all break-all">{text}</code>
      <CopyButton text={text} className="btn shrink-0" />
    </div>
  )
}

export function Field({ label, children, hint }: { label: string; children: ComponentChildren; hint?: ComponentChildren }) {
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
  return <div class={`rounded-[var(--r)] border px-3 py-2 text-[13px] ${toneBorder[tone]}`}>{children}</div>
}

export function KeyValue({ rows }: { rows: [string, ComponentChildren][] }) {
  return (
    <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5 text-[13px]">
      {rows.map(([k, v]) => (
        <Fragment key={k}>
          <dt class="text-muted">{k}</dt>
          <dd class="min-w-0 break-words">{v ?? <span class="text-muted">—</span>}</dd>
        </Fragment>
      ))}
    </dl>
  )
}

export function Breadcrumbs({ items }: { items: { label: string; href?: string }[] }) {
  return (
    <nav class="flex items-center gap-1.5 text-[13px] text-muted">
      {items.map((it, i) => (
        <Fragment key={`${i}:${it.label}`}>
          {i > 0 && <span class="text-border">/</span>}
          {it.href ? <a href={it.href} class="hover:text-text hover:underline">{it.label}</a> : <span class="text-text">{it.label}</span>}
        </Fragment>
      ))}
    </nav>
  )
}

export function Section({ title, children, actions, help }: { title?: string; children: ComponentChildren; actions?: ComponentChildren; help?: string }) {
  return (
    <section class="flex flex-col gap-3">
      <div class="flex items-start justify-between gap-4">
        <div>
          {title && <h2 class="font-semibold">{title}</h2>}
          {help && <p class={`text-[12px] text-muted max-w-prose ${title ? 'mt-0.5' : ''}`}>{help}</p>}
        </div>
        {actions && <div class="flex gap-2 shrink-0">{actions}</div>}
      </div>
      {children}
    </section>
  )
}

const tileSize = { lg: 'text-lg', xl: 'text-xl', '2xl': 'text-2xl' }

export function Tile({ label, value, sub, tone, href, title, size = '2xl', compact }: { label: string; value: ComponentChildren; sub?: string; tone?: Tone; href?: string; title?: string; size?: keyof typeof tileSize; compact?: boolean }) {
  const box = compact ? 'panel px-3 py-2 gap-0.5' : 'panel p-3 gap-1'
  const cls = `${box} flex flex-col min-w-0 ${href ? 'hover:border-accent' : ''}`
  const body = (
    <>
      <span class="label">{label}</span>
      <span class={`${tileSize[size]} font-semibold truncate ${tone ? toneText[tone] : ''}`} title={title ?? (typeof value === 'string' ? value : undefined)}>{value}</span>
      {sub && <span class="text-[12px] text-muted truncate" title={sub}>{sub}</span>}
    </>
  )
  return href ? <a href={href} class={cls}>{body}</a> : <div class={cls}>{body}</div>
}

export const inputValue = (e: Event) => (e.target as HTMLInputElement).value
