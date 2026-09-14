import { useOperationEvents } from '../events'
import { EventLog, Pill, stateTone } from './ui'

/** Live log of one operation, driven by the SSE stream. */
export function OperationPanel({ id, title, onDone }: { id: number; title: string; onDone?: (status: string) => void }) {
  const { events, op } = useOperationEvents(id)
  if (op && op.status !== 'running' && onDone) queueMicrotask(() => onDone(op.status))
  return (
    <div class="flex flex-col gap-2">
      <div class="flex items-center gap-2">
        <span class="text-[13px] font-medium">{title}</span>
        <span class="text-muted text-[12px]">op #{id}</span>
        <Pill tone={op ? stateTone(op.status) : 'warn'}>{op?.status ?? 'running'}</Pill>
      </div>
      <EventLog events={events} empty="Waiting for the first event…" />
    </div>
  )
}
