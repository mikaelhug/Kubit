// Small IPv4 helpers for the wizard's live checks; the daemon's lint is authoritative.

export function ip4(s: string): number | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s.trim())
  if (!m) return null
  const p = m.slice(1).map(Number)
  if (p.some((x) => x > 255)) return null
  return ((p[0] << 24) | (p[1] << 16) | (p[2] << 8) | p[3]) >>> 0
}

export function fromInt(n: number) { return [n >>> 24, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join('.') }

/** Address part of "a.b.c.d/nn" or a bare address. */
export function addrOf(cidr: string) { return cidr.split('/')[0].trim() }

export function prefixOf(cidr: string, dflt = 24) { const p = cidr.split('/')[1]; const n = p ? Number(p) : dflt; return Number.isFinite(n) && n >= 0 && n <= 32 ? n : dflt }

export function sameSubnet(a: string, b: string, prefix: number) {
  const x = ip4(addrOf(a)), y = ip4(addrOf(b))
  if (x === null || y === null) return false
  const mask = prefix === 0 ? 0 : (~0 << (32 - prefix)) >>> 0
  return ((x & mask) >>> 0) === ((y & mask) >>> 0)
}

export function parseRange(r: string): [number, number] | null {
  const [a, b] = r.split('-').map((s) => s.trim())
  const x = ip4(a ?? ''), y = ip4(b ?? '')
  if (x === null || y === null || y < x) return null
  return [x, y]
}

export function inRange(ip: string, r: string) {
  const x = ip4(addrOf(ip)), rr = parseRange(r)
  return x !== null && rr !== null && x >= rr[0] && x <= rr[1]
}

/** Guess the gateway (.1) for a lease; the operator can overwrite it. */
export function guessGateway(ip: string, prefix: number) {
  const x = ip4(addrOf(ip))
  if (x === null) return ''
  const mask = prefix === 0 ? 0 : (~0 << (32 - prefix)) >>> 0
  return fromInt(((x & mask) >>> 0) + 1)
}
