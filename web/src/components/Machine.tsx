import type { ComponentChildren } from 'preact'
import { fmt, type NodeRow } from '../api'
import { kindLabel, kindTone, lastSeenOf, typeOf } from '../machine'
import { Pill } from './ui'

export function KindPill({ m }: { m: NodeRow }) {
  return <Pill tone={kindTone(m)}>{kindLabel[m.kind]}</Pill>
}

export function TypePill({ m }: { m?: NodeRow | null }) {
  const form = typeOf(m)
  return <Pill tone={form === 'metal' ? 'muted' : 'info'}>{form}</Pill>
}


export function identityRows(m: NodeRow): [string, ComponentChildren][] {
  return [
    ['Identity', <span class="mono text-[12px]">{m.mac}{m.uuid ? ` · ${m.uuid}` : ''}{m.serial ? ` · ${m.serial}` : ''}</span>],
    ['Addresses seen', <span class="mono text-[12px]">{[...new Set([...(m.ipsSeen ?? []), m.ip])].filter(Boolean).join(' → ') || '—'}</span>],
    ['Last seen', fmt.datetime(lastSeenOf(m))],
  ]
}
