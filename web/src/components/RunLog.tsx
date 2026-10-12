import { useEffect, useRef } from 'preact/hooks'
import { fmt, type ApplyLine, type ApplyRun, type RunKind } from '../api'
import { ErrorBox, Pill } from './ui'

const titles: Record<RunKind, string> = { apply: 'Apply', destroy: 'Destroy', restart: 'Restart', shutdown: 'Shut down', 'power-on': 'Power on', snapshot: 'Snapshot' }

export function RunLog({ run, capped, onClose }: { run: ApplyRun; capped?: boolean; onClose?: () => void }) {
  const log = useRef<HTMLPreElement>(null)
  const pinned = useRef(true)
  useEffect(() => {
    const el = log.current
    if (capped && el && pinned.current) el.scrollTop = el.scrollHeight
  }, [run.lines.length, capped])
  const follow = () => {
    const el = log.current
    if (el) pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
  }
  return (
    <div class="panel flex flex-col">
      <div class="flex items-center gap-2 px-3 py-2 border-b border-border text-[13px]">
        <span class="font-medium">{titles[run.kind ?? 'apply']}</span>
        {run.running ? <Pill tone="info">running</Pill> : run.error ? <Pill tone="bad">failed</Pill> : <Pill tone="good">done</Pill>}
        {run.started && <span class="text-muted">{fmt.datetime(run.started)}</span>}
        {onClose && !run.running && <button class="btn btn-sm ml-auto" onClick={onClose} aria-label="Close">✕</button>}
      </div>
      {run.error && <div class="px-3 pt-2"><ErrorBox error={run.error} /></div>}
      <pre ref={log} onScroll={follow} class={`log !rounded-none !border-0 ${capped ? '' : '!max-h-none'}`}>{run.lines.map(logLine).join('\n')}</pre>
    </div>
  )
}

const logLine = (l: ApplyLine) => {
  const time = new Date(l.ts).toLocaleTimeString()
  const level = l.level === 'info' || l.level === 'done' ? '' : `${l.level.toUpperCase()} `
  if (l.message === l.step) return `${time} ${level}${l.step}`
  return `${time} ${level}  ${l.node ? `${l.node}: ` : ''}${l.message}`
}
