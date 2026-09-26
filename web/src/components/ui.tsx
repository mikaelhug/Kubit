import { Fragment, type ComponentChildren } from 'preact'
import { memo } from 'preact/compat'
import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type Event, type Level } from '../api'
import { now } from '../clock'
import { clusters, toast } from '../store'
import { severityTone, stateTone, toneBg, toneBorder, tonePill, toneText, type Tone } from '../tone'
import { useLive } from '../useLive'

export function Pill({ tone, children, title }: { tone: Tone; children: ComponentChildren; title?: string }) {
  return <span class={`pill ${tonePill[tone]}`} title={title}>{children}</span>
}

export function ClusterPill({ state, status }: { state: string; status?: { health?: string; openAlerts?: number; observerError?: string } | null }) {
  const h = state === 'ready' && status?.health && status.health !== 'healthy' ? status.health : ''
  const label = h || state
  const title = h === 'degraded' ? `${status?.openAlerts ?? 0} open alert${status?.openAlerts === 1 ? '' : 's'}` : h === 'down' ? 'Confirmed outage: API, etcd or a node' : h === 'unknown' ? `Kubit cannot reach the network${status?.observerError ? ` (${status.observerError})` : ''}` : undefined
  return <Pill tone={state === 'ready' && !status ? 'muted' : stateTone(label)} title={state === 'ready' && !status ? 'not observed yet' : title}>{label}</Pill>
}

export function SeenAgo({ contact, observed, blind }: { contact?: string; observed?: string; blind?: boolean }) {
  const at = contact || observed
  if (!at) return <span class="text-[12px] text-muted">not observed yet</span>
  const sec = (now.value - Date.parse(at)) / 1000
  const text = fmt.age(sec)
  const stale = blind || sec > 120
  return <span class={`text-[12px] ${stale ? 'text-warn' : 'text-muted'}`} title={`Last contact ${fmt.datetime(at)}`}>{stale && blind ? `not seen for ${text}` : `seen ${text} ago`}</span>
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

const levelColor: Record<Level, string> = { info: 'text-text', warn: 'text-warn', error: 'text-bad', done: 'text-good' }

export const EventLine = memo(function EventLine({ e, showStep = true }: { e: Event; showStep?: boolean }) {
  return (
    <div class={`flex gap-2 leading-5 ${levelColor[e.level] ?? 'text-text'}`}>
      {(e.time || e.clock) && <span class="text-muted shrink-0 select-none">{e.clock ?? fmt.when(e.time)}</span>}
      {showStep && e.step && <span class="text-muted shrink-0">[{e.step}]</span>}
      {e.node && <span class="shrink-0 text-accent">{e.node}</span>}
      <span class="min-w-0 break-words whitespace-pre-wrap">{e.message}</span>
    </div>
  )
})

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

export function ConfirmDialog({ title, impact, action, tone = 'primary', onConfirm, onClose, typed, cluster }: { title: string; impact: ComponentChildren; action: string; tone?: 'primary' | 'danger'; onConfirm: () => void | Promise<unknown>; onClose: () => void; typed?: string; cluster?: string }) {
  const [busy, setBusy] = useState(false)
  const [value, setValue] = useState('')
  const confirm = () => {
    if (busy) return
    const r = onConfirm()
    if (r && typeof (r as Promise<unknown>).then === 'function') {
      setBusy(true)
      ;(r as Promise<unknown>).finally(() => setBusy(false))
    }
  }
  const blocked = !!typed && value.trim().toLowerCase() !== typed.toLowerCase()
  return (
    <Dialog title={title} onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class={`btn ${tone === 'danger' ? 'btn-danger' : 'btn-primary'}`} disabled={blocked || busy} onClick={confirm}>{busy ? 'Working' : action}</button>
      </>
    }>
      {cluster && <MaintenanceNotice cluster={cluster} />}
      <div class="text-[13px] flex flex-col gap-2">{impact}</div>
      {typed && <Field label="To confirm, type"><input class="input mono" placeholder={typed} value={value} onInput={(e) => setValue((e.target as HTMLInputElement).value)} /></Field>}
    </Dialog>
  )
}

export function MaintenanceNotice({ cluster }: { cluster: string }) {
  const updatedAt = clusters.value.find((c) => c.name === cluster)?.updatedAt
  const { data: state, reload } = useLive(() => api.maintenance(cluster), [cluster], [], { onError: 'silent', refresh: [updatedAt] })
  const edge = state?.open ? state.closes : state?.next
  const due = !!edge && now.value >= Date.parse(edge)
  useEffect(() => { if (due) reload() }, [due])
  if (!state || !state.window || state.open) return null
  return <Notice tone="warn">Outside the maintenance window <span class="mono">{state.window}{state.timezone ? ` ${state.timezone}` : ''}</span>{state.next ? `; next opens ${fmt.datetime(state.next)}` : ''}. Confirming runs it anyway.</Notice>
}

export function AlertPill({ e }: { e?: { severity: string; message: string; kind: string } }) {
  if (!e) return null
  return <Pill tone={severityTone(e.severity)} title={e.message}>{e.kind.split('.')[1]}</Pill>
}

function copy(text: string) {
  return navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject(new Error('Clipboard unavailable'))
}

export function CopyButton({ text, className = 'btn', label = 'Copy' }: { text: string | (() => string); className?: string; label?: string }) {
  const [done, setDone] = useState(false)
  const click = () => copy(typeof text === 'function' ? text() : text).then(() => { setDone(true); setTimeout(() => setDone(false), 1500) }).catch((e) => toast(e.message, 'error'))
  return <button class={className} onClick={click}>{done ? 'Copied' : label}</button>
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

const tileSize = { lg: 'text-lg', xl: 'text-xl', '2xl': 'text-2xl' }

export function Tile({ label, value, sub, tone, href, title, size = '2xl', compact, plain, children }: { label: string; value: ComponentChildren; sub?: string; tone?: Tone; href?: string; title?: string; size?: keyof typeof tileSize; compact?: boolean; plain?: boolean; children?: ComponentChildren }) {
  const box = plain ? '' : compact ? 'panel px-3 py-2 gap-0.5' : 'panel p-3 gap-1'
  const cls = `${box} flex flex-col min-w-0 ${plain ? 'gap-1' : ''} ${href ? 'hover:border-accent' : ''}`
  const body = (
    <>
      <span class="label">{label}</span>
      <span class={`${tileSize[size]} font-semibold truncate ${tone ? toneText[tone] : ''}`} title={title ?? (typeof value === 'string' ? value : undefined)}>{value}</span>
      {sub && <span class="text-[12px] text-muted truncate" title={sub}>{sub}</span>}
      {children}
    </>
  )
  return href ? <a href={href} class={cls}>{body}</a> : <div class={cls}>{body}</div>
}

export function Action({ title, what, button, disabled, onClick, href, secondary }: { title: string; what: string; button: string; disabled?: boolean; onClick?: () => void; href?: string; secondary?: { label: string; onClick: () => void } }) {
  return (
    <div class="panel p-3 flex items-center gap-4">
      <div class="flex-1 min-w-0">
        <div class="font-medium">{title}</div>
        <p class="text-[12.5px] text-muted">{what}</p>
      </div>
      <div class="flex gap-2 shrink-0">
        {secondary && <button class="btn" disabled={disabled} onClick={secondary.onClick}>{secondary.label}</button>}
        {href ? <a href={href} class="btn">{button}</a> : <button class="btn btn-primary" disabled={disabled} onClick={onClick}>{button}</button>}
      </div>
    </div>
  )
}

export function GroupHeading({ title, help }: { title: string; help: string }) {
  return <div class="mt-2"><span class="label">{title}</span><p class="text-[12px] text-muted">{help}</p></div>
}
