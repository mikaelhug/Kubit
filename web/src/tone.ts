export type Tone = 'good' | 'warn' | 'bad' | 'info' | 'muted'

export const toneText: Record<Tone, string> = { good: 'text-good', warn: 'text-warn', bad: 'text-bad', info: 'text-info', muted: 'text-muted' }
export const toneBg: Record<Tone, string> = { good: 'bg-good', warn: 'bg-warn', bad: 'bg-bad', info: 'bg-info', muted: 'bg-border' }
export const tonePill: Record<Tone, string> = { good: 'bg-good/15 text-good', warn: 'bg-warn/15 text-warn', bad: 'bg-bad/15 text-bad', info: 'bg-info/15 text-info', muted: 'bg-panel-2 text-muted' }
export const toneBorder: Record<Tone, string> = { good: 'border-good/40 bg-good/10 text-good', warn: 'border-warn/40 bg-warn/10 text-warn', bad: 'border-bad/40 bg-bad/10 text-bad', info: 'border-info/40 bg-info/10 text-info', muted: 'border-border bg-panel-2 text-muted' }

export function stateTone(state: string): Tone {
  switch (state) {
    case 'ready': case 'done': case 'running': case 'maintenance': case 'joined': case 'succeeded': return 'good'
    case 'failed': case 'error': case 'down': case 'offline': return 'bad'
    case 'cancelled': case 'unknown': case 'disabled': return 'muted'
    case 'provisioning': case 'installing': case 'bootstrapped': case 'booting': case 'pending': case 'discovered': case 'setup': case 'updating': case 'degraded': case 'deploying': case 'orphaned': return 'warn'
    case 'amt': case 'off': case 'labhost': case 'configured': return 'info'
    default: return 'muted'
  }
}

export function phaseTone(phase: string): Tone {
  switch (phase) {
    case 'Running': case 'Succeeded': case 'Bound': return 'good'
    case 'Available': return 'info'
    case 'Pending': case 'ContainerCreating': case 'Released': return 'warn'
    default: return 'bad'
  }
}

export function severityTone(severity: string): Tone {
  return severity === 'critical' ? 'bad' : severity === 'warn' ? 'warn' : 'good'
}

export function levelTone(value: number, warn: number, bad: number): Tone | undefined {
  return value >= bad ? 'bad' : value >= warn ? 'warn' : undefined
}
