import { computed } from '@preact/signals'
import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { api, fmt, type Event } from '../api'
import { persist } from '../local'
import { drawerHeight, drawerOpen, drawerTab, loadOperationLog, opEvents, operations, running, runningCount, setDrawer, toast } from '../store'
import { stateTone } from '../tone'
import { Stepper } from './Stepper'
import { Elapsed } from './Time'
import { CopyButton, EventLine, Pill } from './ui'

const heightStyle = computed(() => `height:${drawerHeight.value}px`)

const retryable = new Set(['cluster.create', 'node.add', 'platform.plan', 'platform.apply', 'discover'])

export function ActivityDrawer() {
  const dragging = useRef(false)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'a' && !e.metaKey && !e.ctrlKey && !(e.target instanceof HTMLInputElement) && !(e.target instanceof HTMLTextAreaElement)) setDrawer(!drawerOpen.value)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    const move = (e: MouseEvent) => {
      if (dragging.current) drawerHeight.value = Math.min(window.innerHeight - 120, Math.max(140, window.innerHeight - e.clientY))
    }
    const up = () => { if (dragging.current) { dragging.current = false; persist('kubit.drawerHeight', drawerHeight.value) } }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    return () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up) }
  }, [])

  if (!drawerOpen.value) return (
    <button class="fixed bottom-3 right-3 z-30 btn" onClick={() => setDrawer(true)} title="Activity (a)">
      Activity {runningCount.value > 0 && <Pill tone="warn">{runningCount.value}</Pill>}
    </button>
  )

  return (
    <div class="fixed left-52 right-0 bottom-0 z-30 flex flex-col bg-panel border-t border-border" style={heightStyle}>
      <div class="h-1.5 cursor-row-resize hover:bg-accent/40" onMouseDown={() => { dragging.current = true }} title="Drag to resize" />
      <DrawerTabs />
    </div>
  )
}

function DrawerTabs() {
  const focused = drawerTab.value !== null ? operations.value.get(drawerTab.value) : undefined
  const tabs = focused && focused.status !== 'running' ? [...running.value, focused].sort((a, b) => a.id - b.id) : running.value
  const active = focused ? focused.id : tabs[0]?.id ?? null
  return (
    <>
      <div class="flex items-center gap-1 px-2 border-b border-border scroll-x">
        <span class="label px-2">Activity</span>
        {tabs.map((o) => (
          <button key={o.id} class={`px-3 py-1.5 text-[13px] whitespace-nowrap border-b-2 -mb-px flex items-center gap-2 ${o.id === active ? 'border-accent text-text' : 'border-transparent text-muted hover:text-text'}`} onClick={() => { drawerTab.value = o.id }}>
            {fmt.kind(o.kind)}{o.cluster ? ` · ${o.cluster}` : ''} #{o.id} <Pill tone={stateTone(o.status)}>{o.status}</Pill>
          </button>
        ))}
        {tabs.length === 0 && <span class="text-[13px] text-muted px-2 py-1.5">No operations running.</span>}
        <div class="ml-auto flex items-center gap-1">
          <a href="/operations" class="btn btn-sm">History</a>
          <button class="btn btn-sm" onClick={() => setDrawer(false)} title="Close (a)">✕</button>
        </div>
      </div>
      {active !== null && <OperationView id={active} />}
    </>
  )
}

const logText = (events: Event[]) => events.map((e) => `${e.time ? fmt.when(e.time) + ' ' : e.clock ? e.clock + ' ' : ''}[${e.step}] ${e.node ? e.node + ': ' : ''}${e.message}`).join('\n')

export function OperationView({ id }: { id: number }) {
  const op = operations.value.get(id)
  const events = opEvents.value.get(id)
  const [step, setStep] = useState<string | undefined>()
  const [q, setQ] = useState('')
  const logRef = useRef<HTMLDivElement>(null)
  const [follow, setFollow] = useState(true)

  useEffect(() => { if (op && !events && op.status !== 'running') loadOperationLog(id).catch(() => {}) }, [id, op?.status])
  const shown = useMemo(() => (events ?? []).filter((e) => (!step || e.step === step) && (!q || (e.message + ' ' + (e.node ?? '')).toLowerCase().includes(q.toLowerCase()))), [events, step, q])
  useEffect(() => { if (follow && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight }, [shown, follow])

  if (!op) return <div class="p-3 text-muted text-[13px]">Loading</div>
  return (
    <div class="flex-1 min-h-0 grid grid-cols-[280px_1fr]">
      <div class="border-r border-border overflow-auto p-2 flex flex-col gap-2">
        <div class="flex items-center gap-2 px-2 pt-1">
          <span class="text-[12px] text-muted"><Elapsed from={op.startedAt} to={op.finishedAt} /></span>
          {op.status === 'running' && <button class="btn btn-danger btn-sm ml-auto" onClick={() => api.cancelOperation(id).catch((e) => toast(e.message, 'error'))}>Cancel</button>}
          {op.status !== 'running' && op.status !== 'done' && retryable.has(op.kind) && (
            <button class="btn btn-sm ml-auto" onClick={() => api.retryOperation(id).then((r) => { drawerTab.value = r.operationId }).catch((e) => toast(e.message, 'error'))}>Retry</button>
          )}
        </div>
        <Stepper steps={op.steps ?? []} selected={step} onSelect={(s) => setStep(step === s ? undefined : s)} compact />
      </div>
      <div class="flex flex-col min-h-0">
        <div class="flex items-center gap-2 px-2 py-1 border-b border-border">
          <input class="input !w-56 !py-0.5" placeholder="Search log" value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
          {step && <button class="btn btn-sm" onClick={() => setStep(undefined)}>step: {step} ✕</button>}
          <span class="text-[12px] text-muted">{shown.length} lines</span>
          <label class="ml-auto text-[12px] text-muted flex items-center gap-1"><input type="checkbox" checked={follow} onChange={(e) => setFollow((e.target as HTMLInputElement).checked)} /> follow</label>
          <CopyButton text={() => logText(shown)} className="btn btn-sm" />
        </div>
        <div ref={logRef} class="flex-1 overflow-auto p-2 mono" onScroll={(e) => { const el = e.currentTarget; setFollow(el.scrollTop + el.clientHeight >= el.scrollHeight - 8) }}>
          {shown.length === 0 ? <span class="text-muted">{op.status === 'running' ? 'Waiting for output' : 'No output.'}</span> : shown.map((e, i) => <EventLine key={e.seq ?? i} e={e} showStep={!step} />)}
        </div>
      </div>
    </div>
  )
}
