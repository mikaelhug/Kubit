import { Fragment, type ComponentChildren } from 'preact'
import { createPortal } from 'preact/compat'
import { useEffect, useRef, useState } from 'preact/hooks'
import { fmt, type PlanSummary, type RunKind } from '../api'
import { now } from '../clock'
import { useEscape } from '../keys'
import { toast } from '../store'
import { stateTone, toneBg, toneBorder, tonePill, toneText, usageColor, usageLevel, usageText, type Tone } from '../tone'

export function Pill({ tone, children, title }: { tone: Tone; children: ComponentChildren; title?: string }) {
  return <span class={`pill ${tonePill[tone]}`} title={title}>{children}</span>
}

const runLabels: Partial<Record<RunKind, string>> = { destroy: 'destroying', restart: 'restarting', shutdown: 'shutting down', 'power-on': 'powering on', snapshot: 'snapshotting' }

export function planLabel(p?: PlanSummary): { text: string; tone: Tone; title?: string } {
  if (!p) return { text: 'checking', tone: 'muted' }
  switch (p.state) {
    case 'applying': return { text: (p.run && runLabels[p.run]) || 'applying', tone: 'info', title: p.holder ? `kubit apply by ${p.holder}` : undefined }
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

export function Bar({ used, cap }: { used: number; cap: number }) {
  const pct = cap > 0 ? Math.min(100, (used / cap) * 100) : 0
  const color = usageColor[usageLevel(used, cap)]
  return (
    <div class="h-1.5 w-full rounded-full overflow-hidden" style={{ background: `color-mix(in srgb, ${color} 18%, transparent)` }}>
      <div class="h-full rounded-full transition-[width]" style={{ width: pct + '%', background: color }} />
    </div>
  )
}

export function UsedOf({ used, cap }: { used: number; cap: number }) {
  return <><span class={usageText[usageLevel(used, cap)]}>{fmt.bytes(used)}</span><span class="text-muted">/{fmt.bytes(cap)}</span></>
}

export function Meter({ label, used, cap, format }: { label: string; used: number; cap: number; format: (n: number) => string }) {
  const pct = cap ? Math.min(100, Math.round((used / cap) * 100)) : 0
  return (
    <div class="flex flex-col gap-1.5">
      <div class="flex items-baseline justify-between">
        <span class="label">{label}</span>
        <span class="text-[13px]"><strong>{format(used)}</strong> <span class="text-muted">/ {format(cap)} · {pct}%</span></span>
      </div>
      <Bar used={used} cap={cap} />
    </div>
  )
}


export function Dialog({ title, onClose, children, width = 'max-w-lg', footer }: { title: string; onClose: () => void; children?: ComponentChildren; width?: string; footer?: ComponentChildren }) {
  useEscape(onClose)
  const pressed = useRef(false)
  const panel = useRef<HTMLDivElement>(null)
  useEffect(() => { panel.current?.querySelector<HTMLElement>('[autofocus]')?.focus() }, [])
  return createPortal(
    <div class="fixed inset-0 z-40 flex items-start justify-center bg-black/50 p-6 overflow-auto" onMouseDown={(e) => { pressed.current = e.target === e.currentTarget }} onClick={(e) => { if (pressed.current && e.target === e.currentTarget) onClose(); pressed.current = false }}>
      <div ref={panel} class={`panel w-full ${width} mt-10 flex flex-col`} role="dialog" aria-modal="true">
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

export interface MenuItem { label: string; onSelect: () => void; danger?: boolean }

export function Menu({ label, items, disabled }: { label: string; items: MenuItem[]; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  const trigger = useRef<HTMLButtonElement>(null)
  const close = (refocus: boolean) => {
    setOpen(false)
    if (refocus) trigger.current?.focus()
  }
  return (
    <>
      <button ref={trigger} class="btn btn-sm" aria-haspopup="menu" aria-expanded={open} disabled={disabled || items.length === 0} onClick={() => setOpen(!open)}>
        {label}
        <svg class="text-muted" width="10" height="10" viewBox="0 0 12 12" aria-hidden="true"><path d="M2.5 4.5 6 8l3.5-3.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </button>
      {open && trigger.current && <MenuList anchor={trigger.current} items={items} onClose={close} />}
    </>
  )
}

const menuRow = 30

function MenuList({ anchor, items, onClose }: { anchor: HTMLElement; items: MenuItem[]; onClose: (refocus: boolean) => void }) {
  useEscape(() => onClose(true))
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => {
    list.current?.querySelector('button')?.focus({ preventScroll: true })
    const away = (e: MouseEvent) => { if (!list.current?.contains(e.target as Node) && !anchor.contains(e.target as Node)) onClose(false) }
    const gone = () => onClose(false)
    document.addEventListener('mousedown', away)
    window.addEventListener('resize', gone)
    window.addEventListener('scroll', gone, true)
    return () => {
      document.removeEventListener('mousedown', away)
      window.removeEventListener('resize', gone)
      window.removeEventListener('scroll', gone, true)
    }
  }, [])
  const keys = (e: KeyboardEvent) => {
    if (e.key === 'Tab') return onClose(false)
    const buttons = Array.from(list.current?.querySelectorAll('button') ?? [])
    const at = buttons.indexOf(document.activeElement as HTMLButtonElement)
    const to = ({ ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: buttons.length - 1 } as Record<string, number>)[e.key]
    if (to === undefined) return
    e.preventDefault()
    buttons[(to + buttons.length) % buttons.length]?.focus({ preventScroll: true })
  }
  const r = anchor.getBoundingClientRect()
  const up = r.bottom + 4 + items.length * menuRow + 8 > window.innerHeight && r.top > items.length * menuRow + 8
  const place = up ? { bottom: window.innerHeight - r.top + 4 } : { top: r.bottom + 4 }
  return createPortal(
    <div ref={list} role="menu" class="panel fixed z-50 min-w-[180px] py-1 shadow-lg shadow-black/20" style={{ ...place, right: Math.max(8, window.innerWidth - r.right) }} onKeyDown={keys}>
      {items.map((it) => (
        <button key={it.label} type="button" role="menuitem" class={`w-full text-left px-3 py-1.5 text-[13px] whitespace-nowrap outline-none hover:bg-panel-2 focus:bg-panel-2 ${it.danger ? 'text-bad' : ''}`} onClick={() => { onClose(false); it.onSelect() }}>{it.label}</button>
      ))}
    </div>,
    document.body,
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

export function Field({ label, children, hint, error, class: cls = '' }: { label: string; children: ComponentChildren; hint?: ComponentChildren; error?: ComponentChildren; class?: string }) {
  return (
    <label class={`flex flex-col gap-1 ${cls}`}>
      <span class="label">{label}</span>
      {children}
      {error ? <span class="text-[12px] text-bad">{error}</span> : hint && <span class="text-[12px] text-muted">{hint}</span>}
    </label>
  )
}

export function Check({ checked, disabled, onChange, children }: { checked: boolean; disabled?: boolean; onChange: (on: boolean) => void; children: ComponentChildren }) {
  return (
    <label class={`inline-flex items-center gap-2 text-[13px] ${disabled ? 'text-muted' : ''}`}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange((e.target as HTMLInputElement).checked)} />
      {children}
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

export function Tile({ label, value, sub, tone, href, title, size = '2xl', compact, pending }: { label: string; value: ComponentChildren; sub?: string; tone?: Tone; href?: string; title?: string; size?: keyof typeof tileSize; compact?: boolean; pending?: boolean }) {
  const box = compact ? 'panel px-3 py-2 gap-0.5' : 'panel p-3 gap-1'
  const cls = `${box} flex flex-col min-w-0 ${href ? 'hover:border-accent' : ''}`
  const body = (
    <>
      <span class="label">{label}</span>
      <span class={`${tileSize[size]} font-semibold truncate ${tone ? toneText[tone] : ''}`} title={title ?? (typeof value === 'string' ? value : undefined)}>{value}</span>
      {(sub || pending) && (
        <span class="flex items-center gap-1.5 min-w-0">
          {pending && <span class="shrink-0"><Pill tone="info">pending</Pill></span>}
          {sub && <span class="text-[12px] text-muted truncate" title={sub}>{sub}</span>}
        </span>
      )}
    </>
  )
  return href ? <a href={href} class={cls}>{body}</a> : <div class={cls}>{body}</div>
}

export function Pending({ cluster }: { cluster: string }) {
  return <a href={`/clusters/${cluster}/changes`}><Pill tone="info">pending</Pill></a>
}

export const inputValue = (e: Event) => (e.target as HTMLInputElement).value
