import type { Signal } from '@preact/signals'

export function copyMap<K, V>(m: Map<K, V>, edit: (m: Map<K, V>) => void): Map<K, V> {
  const next = new Map(m)
  edit(next)
  return next
}

export function editMap<K, V>(s: Signal<Map<K, V>>, edit: (m: Map<K, V>) => void) {
  s.value = copyMap(s.value, edit)
}

export function setIn<K, V>(s: Signal<Map<K, V>>, key: K, value: V) {
  s.value = new Map(s.value).set(key, value)
}
