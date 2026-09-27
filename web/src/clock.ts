import { computed, signal, type ReadonlySignal } from '@preact/signals'

export const now = signal(Date.now())
setInterval(() => { now.value = Date.now() }, 1000)

const coarse = new Map<number, ReadonlySignal<number>>()

export function nowEvery(ms: number) {
  let s = coarse.get(ms)
  if (!s) { s = computed(() => Math.floor(now.value / ms) * ms); coarse.set(ms, s) }
  return s.value
}

export const today = computed(() => new Date(now.value).toDateString())
