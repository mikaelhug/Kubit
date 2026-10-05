export function ip4(s: string): number | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s.trim())
  if (!m) return null
  const p = m.slice(1).map(Number)
  if (p.some((x) => x > 255)) return null
  return ((p[0] << 24) | (p[1] << 16) | (p[2] << 8) | p[3]) >>> 0
}

function fromInt(n: number) { return [n >>> 24, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join('.') }

export function ipAt(range: string, i: number) {
  const start = ip4(range.split('-')[0] ?? '')
  return start === null ? '' : fromInt(start + i)
}

export const subnet24 = (ip: string) => ip.replace(/\.\d+$/, '.0/24')
