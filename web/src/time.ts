export const later = (a?: string, b?: string) => !!a && !!b && Date.parse(a) > Date.parse(b)

export function appendWithin<T extends { ts: string }>(list: T[] | null, point: T, span: number): T[] | null {
  const last = list?.[list.length - 1]
  if (last && !later(point.ts, last.ts)) return list
  const from = Date.parse(point.ts) - span
  return [...(list ?? []).filter((s) => Date.parse(s.ts) >= from), point]
}

export const createdSort = (x: { createdAt?: string }) => (x.createdAt ? -Date.parse(x.createdAt) : 0)
