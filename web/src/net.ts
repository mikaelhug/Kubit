export function ip4(s: string): number | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s.trim())
  if (!m) return null
  const p = m.slice(1).map(Number)
  if (p.some((x) => x > 255)) return null
  return ((p[0] << 24) | (p[1] << 16) | (p[2] << 8) | p[3]) >>> 0
}

function fromInt(n: number) { return [n >>> 24, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join('.') }

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

export function guessGateway(ip: string, prefix: number) {
  const x = ip4(addrOf(ip))
  if (x === null) return ''
  const mask = prefix === 0 ? 0 : (~0 << (32 - prefix)) >>> 0
  return fromInt(((x & mask) >>> 0) + 1)
}

export function ipAt(range: string, i: number) {
  const start = ip4(range.split('-')[0] ?? '')
  return start === null ? '' : fromInt(start + i)
}

export const isDnsLabel = (s: string) => /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(s)

export const subnet24 = (ip: string) => ip.replace(/\.\d+$/, '.0/24')

export const staticNetwork = (ip: string) => ({ addresses: [`${ip}/24`], gateway: guessGateway(ip, 24) })

export const nodeHostname = (cluster: string, pool: string, n: number) => `${cluster}-${pool === 'controlplane' ? 'cp' : pool}-${String(n).padStart(2, '0')}`

export function nextHostname(cluster: string, pool: string, inPool: number, taken: string[]) {
  let n = inPool + 1
  while (taken.includes(nodeHostname(cluster, pool, n))) n++
  return nodeHostname(cluster, pool, n)
}
