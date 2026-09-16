import { signal } from '@preact/signals'
import { fmt } from './api'

export const now = signal(Date.now())
setInterval(() => { now.value = Date.now() }, 1000)

export function elapsed(from?: string, to?: string) {
  if (!to) void now.value
  return fmt.duration(from, to)
}

export function ageSec(iso?: string) {
  return iso ? (now.value - new Date(iso).getTime()) / 1000 : Infinity
}
