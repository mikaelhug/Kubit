import { useState } from 'preact/hooks'
import { fmt, type HealthEvent } from '../api'
import { runbookFor, type Runbook } from '../runbooks'
import { ack, clusters } from '../store'
import { severityTone } from '../tone'
import { StatusDot } from './ui'

function objectLink(e: HealthEvent): string | null {
  const m = /^(\w+)\/([^/]+)\/(.+)$/.exec(e.node ?? '')
  if (!m) return null
  const [, kind, ns] = m
  const page = kind === 'PersistentVolumeClaim' ? 'storage' : kind === 'Service' || kind === 'Ingress' || kind === 'MetalLB' ? 'network' : 'workloads'
  return `/clusters/${e.cluster}/${page}?ns=${encodeURIComponent(ns)}`
}

function RunbookPanel({ rb }: { rb: Runbook }) {
  return (
    <div class="mx-4 mb-3 rounded-[var(--r)] border border-border bg-panel-2/60 px-4 py-3 text-[13px] flex flex-col gap-2">
      <span class="font-medium">{rb.title}</span>
      <ol class="list-decimal pl-5 flex flex-col gap-1">
        {rb.steps.map((s) => <li key={s.text}>{s.text} {s.link && <a href={s.link.href} class="text-accent hover:underline whitespace-nowrap">{s.link.label} →</a>}</li>)}
      </ol>
    </div>
  )
}

export function EventRow({ e, onAck }: { e: HealthEvent; onAck?: () => void }) {
  const [open, setOpen] = useState(false)
  const mac = e.node ? clusters.value.find((c) => c.name === e.cluster)?.spec.spec.nodes.find((n) => n.hostname === e.node)?.mac : undefined
  const rb = onAck ? runbookFor(e.kind, { cluster: e.cluster, node: e.node, nodeHref: mac ? `/machines/${mac}` : undefined }) : null
  const link = objectLink(e)
  return (
    <div class="flex flex-col">
      <div class="flex items-center gap-3 px-4 py-2 text-[13px]">
        <StatusDot tone={severityTone(e.severity)} />
        <span class="text-muted text-[12px] whitespace-nowrap shrink-0">{fmt.when(e.ts)}</span>
        <span class={`min-w-0 truncate ${e.acked ? 'text-muted' : ''}`} title={e.message}>{e.message}</span>
        {link && <a href={link} class="text-[11px] text-accent hover:underline shrink-0">open</a>}
        <span class="ml-auto mono text-[11px] text-muted">{e.kind}</span>
        {rb && <button class={`btn btn-sm ${open ? 'border-accent' : ''}`} onClick={() => setOpen(!open)}>{open ? 'Hide' : 'What to do'}</button>}
        {onAck && <button class="btn btn-sm" onClick={onAck}>Ack</button>}
      </div>
      {open && rb && <RunbookPanel rb={rb} />}
    </div>
  )
}

export function AlertGroup({ id, alerts, label, href }: { id: string; alerts: HealthEvent[]; label?: string; href?: string }) {
  if (alerts.length === 0) return null
  const count = `${alerts.length} active alert${alerts.length === 1 ? '' : 's'}`
  return (
    <div class="panel border-warn/50">
      <div class="flex items-center gap-2 px-4 py-2 border-b border-border">
        {label ? <><a href={href} class="font-semibold hover:underline">{label}</a><span class="text-[12px] text-muted">{count}</span></> : <span class="font-semibold">{count}</span>}
        <button class="btn btn-sm ml-auto" onClick={() => ack(id)}>Acknowledge all</button>
      </div>
      {alerts.map((e) => <EventRow key={e.id} e={e} onAck={() => ack(id, e.id)} />)}
    </div>
  )
}
