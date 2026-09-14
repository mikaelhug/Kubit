import { useEffect, useRef, useState } from 'preact/hooks'
import { api, fmt, type Operation } from '../api'
import { drawerHeight, drawerOpen, drawerTab, loadOperationLog, opEvents, operations, persist, running, toast } from '../store'
import { Stepper } from './Stepper'
import { EventLine, Pill, stateTone } from './ui'

/**
 * Bottom drawer with one tab per running (or recently watched) operation: full width,
 * resizable, searchable log next to the stepper. Toggle with `a`.
 */
export function ActivityDrawer() {
  const open = drawerOpen.value
  const height = drawerHeight.value
  const dragging = useRef(false)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'a' && !e.metaKey && !e.ctrlKey && !(e.target instanceof HTMLInputElement) && !(e.target instanceof HTMLTextAreaElement)) {
        drawerOpen.value = !drawerOpen.value
        persist('kubit.drawer', drawerOpen.value)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    const move = (e: MouseEvent) => {
      if (!dragging.current) return
      const h = Math.min(window.innerHeight - 120, Math.max(140, window.innerHeight - e.clientY))
      drawerHeight.value = h
    }
    const up = () => { if (dragging.current) { dragging.current = false; persist('kubit.drawerHeight', drawerHeight.value) } }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    return () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up) }
  }, [])

  const tabs = tabList()
  const active = drawerTab.value !== null && operations.value.has(drawerTab.value) ? drawerTab.value : tabs[0]?.id ?? null

  if (!open) return (
    <button class="fixed bottom-3 right-3 z-30 btn shadow-lg" onClick={() => { drawerOpen.value = true; persist('kubit.drawer', true) }} title="Activity (a)">
      Activity {running.value.length > 0 && <Pill tone="warn">{running.value.length}</Pill>}
    </button>
  )

  return (
    <div class="fixed left-56 right-0 bottom-0 z-30 flex flex-col bg-panel border-t border-border shadow-[0_-8px_24px_rgba(0,0,0,0.25)]" style={{ height }}>
      <div class="h-1.5 cursor-row-resize hover:bg-accent/40" onMouseDown={() => { dragging.current = true }} title="Drag to resize" />
      <div class="flex items-center gap-1 px-2 border-b border-border overflow-x-auto">
        <span class="label px-2">Activity</span>
        {tabs.map((t) => (
          <button key={t.id} class={`px-3 py-1.5 text-[13px] whitespace-nowrap border-b-2 -mb-px flex items-center gap-2 ${t.id === active ? 'border-accent text-text' : 'border-transparent text-muted hover:text-text'}`} onClick={() => { drawerTab.value = t.id }}>
            {t.label} <Pill tone={stateTone(t.status)}>{t.status}</Pill>
          </button>
        ))}
        {tabs.length === 0 && <span class="text-[13px] text-muted px-2 py-1.5">No operations running. Anything you start shows up here.</span>}
        <div class="ml-auto flex items-center gap-1">
          <a href="/operations" class="btn !py-1 !px-2 text-[12px]">History</a>
          <button class="btn !py-1 !px-2 text-[12px]" onClick={() => { drawerOpen.value = false; persist('kubit.drawer', false) }} title="Close (a)">✕</button>
        </div>
      </div>
      {active !== null && <OperationView id={active} />}
    </div>
  )
}

function tabList() {
  const ops = operations.value
  const list: Operation[] = [...ops.values()].filter((o) => o.status === 'running')
  const focused = drawerTab.value !== null ? ops.get(drawerTab.value) : undefined
  if (focused && !list.find((o) => o.id === focused.id)) list.push(focused)
  return list.sort((a, b) => a.id - b.id).map((o) => ({ id: o.id, label: `${fmt.kind(o.kind)}${o.cluster ? ` · ${o.cluster}` : ''} #${o.id}`, status: o.status }))
}

/** Stepper on the left, filtered log on the right; used by the drawer and the operation page. */
export function OperationView({ id, tall }: { id: number; tall?: boolean }) {
  const op = operations.value.get(id)
  const events = opEvents.value.get(id) ?? []
  const [step, setStep] = useState<string | undefined>()
  const [q, setQ] = useState('')
  const logRef = useRef<HTMLDivElement>(null)
  const [follow, setFollow] = useState(true)

  useEffect(() => { if (op && !opEvents.value.has(id) && op.status !== 'running') loadOperationLog(id).catch(() => {}) }, [id, op?.status])
  useEffect(() => { if (follow && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight }, [events.length, follow, step])

  const shown = events.filter((e) => (!step || e.step === step) && (!q || (e.message + ' ' + (e.node ?? '')).toLowerCase().includes(q.toLowerCase())))
  const copy = () => navigator.clipboard.writeText(shown.map((e) => `${e.time ? new Date(e.time).toLocaleTimeString() + ' ' : ''}[${e.step}] ${e.node ? e.node + ': ' : ''}${e.message}`).join('\n')).then(() => toast('Log copied'))

  if (!op) return <div class="p-3 text-muted text-[13px]">Loading…</div>
  return (
    <div class="flex-1 min-h-0 grid grid-cols-[280px_1fr]">
      <div class="border-r border-border overflow-auto p-2 flex flex-col gap-2">
        <div class="flex items-center gap-2 px-2 pt-1">
          <span class="text-[12px] text-muted num">{fmt.duration(op.startedAt, op.finishedAt)}</span>
          {op.status === 'running' && <button class="btn btn-danger !py-0.5 !px-2 text-[12px] ml-auto" onClick={() => api.cancelOperation(id).catch((e) => toast(e.message, 'error'))}>Cancel</button>}
          {op.status !== 'running' && ['cluster.create', 'node.add', 'platform.plan', 'platform.apply', 'discover'].includes(op.kind) && op.status !== 'done' && (
            <button class="btn !py-0.5 !px-2 text-[12px] ml-auto" onClick={() => api.retryOperation(id).then((r) => { drawerTab.value = r.operationId }).catch((e) => toast(e.message, 'error'))}>Retry</button>
          )}
        </div>
        <Stepper steps={op.steps ?? []} selected={step} onSelect={(s) => setStep(step === s ? undefined : s)} compact />
      </div>
      <div class="flex flex-col min-h-0">
        <div class="flex items-center gap-2 px-2 py-1 border-b border-border">
          <input class="input !w-56 !py-0.5" placeholder="Search log…" value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
          {step && <button class="btn !py-0.5 !px-2 text-[12px]" onClick={() => setStep(undefined)}>step: {step} ✕</button>}
          <span class="text-[12px] text-muted">{shown.length} lines</span>
          <label class="ml-auto text-[12px] text-muted flex items-center gap-1"><input type="checkbox" checked={follow} onChange={(e) => setFollow((e.target as HTMLInputElement).checked)} /> follow</label>
          <button class="btn !py-0.5 !px-2 text-[12px]" onClick={copy}>Copy</button>
        </div>
        <div ref={logRef} class={`flex-1 overflow-auto p-2 mono ${tall ? '' : ''}`} onScroll={(e) => { const el = e.currentTarget; setFollow(el.scrollTop + el.clientHeight >= el.scrollHeight - 8) }}>
          {shown.length === 0 ? <span class="text-muted">{op.status === 'running' ? 'Waiting for output…' : 'No output.'}</span> : shown.map((e, i) => <EventLine key={i} e={e} showStep={!step} />)}
        </div>
      </div>
    </div>
  )
}
