import { fmt } from '../api'
import { now, nowEvery } from '../clock'

export function Ago({ iso, bare }: { iso?: string; bare?: boolean }) {
  if (!iso) return null
  const at = Date.parse(iso)
  const minute = nowEvery(60_000)
  const sec = ((minute - at >= 60_000 ? minute : now.value) - at) / 1000
  return <>{bare ? fmt.age(sec) : `${fmt.age(sec)} ago`}</>
}

export function Elapsed({ from, to }: { from?: string; to?: string }) {
  return <>{fmt.duration(from, to, to ? undefined : now.value)}</>
}

export function Age({ at, fallback }: { at?: string; fallback?: string }) {
  return at ? <Ago iso={at} bare /> : <>{fallback || '—'}</>
}
