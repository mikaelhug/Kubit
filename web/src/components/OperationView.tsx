import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { api, fmt, type Event } from '../api'
import { opEvents, operations, runOp, viewOp } from '../ops'
import { toast } from '../store'
import { Stepper } from './Stepper'
import { Elapsed } from './Time'
import { CopyButton, EventLine } from './ui'

const retryable = new Set(['cluster.create', 'node.add', 'platform.plan', 'platform.apply', 'discover'])

const logText = (events: Event[]) => events.map((e) => `${e.time ? fmt.when(e.time) + ' ' : e.clock ? e.clock + ' ' : ''}[${e.step}] ${e.node ? e.node + ': ' : ''}${e.message}`).join('\n')

export function OperationView({ id }: { id: number }) {
  const op = operations.value.get(id)
  const events = opEvents.value.get(id)
  const [step, setStep] = useState<string | undefined>()
  const [q, setQ] = useState('')
  const logRef = useRef<HTMLDivElement>(null)
  const [follow, setFollow] = useState(true)

  useEffect(() => viewOp(id), [id])
  const shown = useMemo(() => {
    const list = events ?? []
    if (!step && !q) return list
    const needle = q.toLowerCase()
    return list.filter((e) => (!step || e.step === step) && (!needle || (e.message + ' ' + (e.node ?? '')).toLowerCase().includes(needle)))
  }, [events, step, q])
  useEffect(() => { if (follow && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight }, [shown, follow])

  if (!op) return <div class="p-3 text-muted text-[13px]">Loading</div>
  return (
    <div class="flex-1 min-h-0 grid grid-cols-[280px_1fr]">
      <div class="border-r border-border overflow-auto p-2 flex flex-col gap-2">
        <div class="flex items-center gap-2 px-2 pt-1">
          <span class="text-[12px] text-muted"><Elapsed from={op.startedAt} to={op.finishedAt} /></span>
          {op.status === 'running' && <button class="btn btn-danger btn-sm ml-auto" onClick={() => api.cancelOperation(id).catch((e) => toast(e.message, 'error'))}>Cancel</button>}
          {op.status !== 'running' && op.status !== 'done' && retryable.has(op.kind) && <button class="btn btn-sm ml-auto" onClick={() => runOp(api.retryOperation(id), undefined, false)}>Retry</button>}
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
