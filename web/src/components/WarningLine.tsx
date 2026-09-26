import type { Warning } from '../api'
import { Notice } from './ui'

export function WarningLine({ w }: { w: Warning }) {
  return <Notice tone={w.level === 'warn' ? 'warn' : 'info'}><span class="mono text-[11px] opacity-70 mr-2">{w.code}</span>{w.node && <span class="mono mr-1">{w.node}:</span>}{w.message}</Notice>
}
