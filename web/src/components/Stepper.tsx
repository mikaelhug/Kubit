import type { Step } from '../api'
import { fmt } from '../api'

const icon: Record<Step['status'], string> = { pending: '○', running: '◐', done: '●', failed: '✕', skipped: '–', cancelled: '⊘' }
const color: Record<Step['status'], string> = { pending: 'text-muted', running: 'text-accent', done: 'text-good', failed: 'text-bad', skipped: 'text-muted', cancelled: 'text-warn' }

/** Vertical list of an operation's steps with status and duration. */
export function Stepper({ steps, selected, onSelect, compact }: { steps: Step[]; selected?: string; onSelect?: (id: string) => void; compact?: boolean }) {
  if (!steps.length) return <div class="text-[12px] text-muted">No steps reported yet.</div>
  return (
    <ol class={`flex flex-col ${compact ? 'gap-0.5' : 'gap-1'}`}>
      {steps.map((s) => (
        <li key={s.id}>
          <button
            class={`w-full text-left flex items-center gap-2 rounded px-2 py-1 text-[13px] ${onSelect ? 'hover:bg-panel-2' : 'cursor-default'} ${selected === s.id ? 'bg-panel-2' : ''}`}
            onClick={() => onSelect?.(s.id)}
          >
            <span class={`w-4 text-center ${color[s.status]} ${s.status === 'running' ? 'animate-pulse' : ''}`}>{icon[s.status]}</span>
            <span class={`flex-1 truncate ${s.status === 'pending' || s.status === 'skipped' ? 'text-muted' : ''}`}>{s.title}</span>
            {s.node && !compact && <span class="mono text-muted">{s.node}</span>}
            <span class="num text-[11px] text-muted w-14 text-right">{s.status === 'skipped' ? 'skipped' : s.status === 'cancelled' ? 'cancelled' : s.startedAt ? fmt.duration(s.startedAt, s.finishedAt) : ''}</span>
          </button>
        </li>
      ))}
    </ol>
  )
}
