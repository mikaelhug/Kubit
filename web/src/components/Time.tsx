import { fmt } from '../api'
import { now } from '../clock'

export function Ago({ iso, fresh, bare }: { iso?: string; fresh?: string; bare?: boolean }) {
  if (!iso) return null
  const sec = (now.value - Date.parse(iso)) / 1000
  if (fresh && sec < 60) return <>{fresh}</>
  return <>{bare ? fmt.age(sec) : `${fmt.age(sec)} ago`}</>
}

export function Elapsed({ from, to }: { from?: string; to?: string }) {
  return <>{fmt.duration(from, to, to ? undefined : now.value)}</>
}
