import { useState } from 'preact/hooks'
import type { PlanChange, PlanDiff } from '../api'
import { Pill, type Tone } from './ui'

const actionTone: Record<PlanChange['action'], Tone> = { create: 'good', update: 'warn', replace: 'warn', delete: 'bad' }
const typeLabel: Record<string, string> = { helm_release: 'Helm release', kubectl_manifest: 'Manifest', kubernetes_namespace_v1: 'Namespace' }

/** Reviewable rendering of an OpenTofu plan: per add-on, per resource, per attribute. */
export function DiffView({ diff }: { diff: PlanDiff }) {
  const total = diff.summary.Add + diff.summary.Change + diff.summary.Remove
  return (
    <div class="flex flex-col gap-4">
      <div class="flex flex-wrap items-center gap-3 text-[13px]">
        <span class="font-medium">{total === 0 ? 'No changes.' : `${total} change${total === 1 ? '' : 's'}`}</span>
        {diff.summary.Add > 0 && <Pill tone="good">+{diff.summary.Add} create</Pill>}
        {diff.summary.Change > 0 && <Pill tone="warn">~{diff.summary.Change} update</Pill>}
        {diff.summary.Remove > 0 && <Pill tone="bad">−{diff.summary.Remove} destroy</Pill>}
        <span class="text-muted ml-auto">planned {new Date(diff.timestamp).toLocaleString()}</span>
      </div>
      {total === 0 && <p class="text-[13px] text-muted">The cluster already matches cluster.yaml's platform section; applying would do nothing.</p>}
      {(diff.groups ?? []).map((g) => (
        <div key={g.addon} class="panel overflow-hidden">
          <div class="px-4 py-2.5 border-b border-border flex items-center gap-2">
            <span class="font-medium">{g.addon}</span>
            <span class="text-[12px] text-muted">{g.changes.length} resource{g.changes.length === 1 ? '' : 's'}</span>
          </div>
          {g.changes.map((c) => <ChangeRow key={c.address} c={c} />)}
        </div>
      ))}
      {diff.warnings && diff.warnings.length > 0 && <Warnings items={diff.warnings} />}
    </div>
  )
}

function ChangeRow({ c }: { c: PlanChange }) {
  const [open, setOpen] = useState(c.action !== 'create' || (c.attrs?.length ?? 0) <= 6)
  return (
    <div class="border-b border-border/60 last:border-b-0">
      <button class="w-full flex items-center gap-3 px-4 py-2 text-left hover:bg-panel-2" onClick={() => setOpen(!open)}>
        <Pill tone={actionTone[c.action]}>{c.action}</Pill>
        <span class="text-[13px]">{typeLabel[c.type] ?? c.type}</span>
        <span class="mono text-muted">{c.address}</span>
        <span class="ml-auto text-muted text-[12px]">{c.attrs?.length ?? 0} attribute{(c.attrs?.length ?? 0) === 1 ? '' : 's'} {open ? '▾' : '▸'}</span>
      </button>
      {open && c.attrs && c.attrs.length > 0 && (
        <div class="px-4 pb-3 overflow-x-auto">
          <table class="text-[12.5px] w-full">
            <tbody>
              {c.attrs.map((a) => (
                <tr key={a.key} class="align-top">
                  <td class="mono text-muted pr-4 py-1 whitespace-nowrap">{a.key}</td>
                  <td class="py-1 w-full">
                    {a.sensitive ? <span class="text-muted italic">sensitive</span>
                      : a.unknown ? <span class="text-muted italic">known after apply</span>
                      : <Values before={a.before} after={a.after} action={c.action} />}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function Values({ before, after, action }: { before?: string; after?: string; action: PlanChange['action'] }) {
  const multi = (before?.includes('\n') || after?.includes('\n') || (before?.length ?? 0) + (after?.length ?? 0) > 100)
  if (action === 'create') return <Val v={after} cls="text-good" multi={multi} />
  if (action === 'delete') return <Val v={before} cls="text-bad line-through" multi={multi} />
  if (multi) return (
    <div class="grid grid-cols-2 gap-2">
      <Val v={before} cls="text-bad" multi label="before" />
      <Val v={after} cls="text-good" multi label="after" />
    </div>
  )
  return <span><Val v={before} cls="text-bad" /> <span class="text-muted">→</span> <Val v={after} cls="text-good" /></span>
}

function Val({ v, cls, multi, label }: { v?: string; cls: string; multi?: boolean; label?: string }) {
  if (v === undefined || v === '') return <span class="text-muted italic">{label ? `${label}: ` : ''}empty</span>
  if (multi) return (
    <div class="min-w-0">
      {label && <div class="label mb-1">{label}</div>}
      <pre class={`mono whitespace-pre-wrap break-words rounded bg-bg border border-border p-2 max-h-64 overflow-auto ${cls}`}>{v}</pre>
    </div>
  )
  return <span class={`mono ${cls}`}>{v}</span>
}

function Warnings({ items }: { items: string[] }) {
  const [open, setOpen] = useState(false)
  return (
    <div class="rounded-md border border-warn/40 bg-warn/10 text-[13px]">
      <button class="w-full text-left px-3 py-2 text-warn" onClick={() => setOpen(!open)}>{items.length} provider warning{items.length === 1 ? '' : 's'} (deprecations; harmless) {open ? '▾' : '▸'}</button>
      {open && <ul class="px-3 pb-2 text-muted list-disc pl-7">{items.map((w, i) => <li key={i}>{w}</li>)}</ul>}
    </div>
  )
}
