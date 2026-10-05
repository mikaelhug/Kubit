import { fmt, type Inventory, type LabHost, type LabVM, type VMSize } from './api'

export const DEBIAN = 'debian'

export interface DiskOpt { key: string; name: string; sizeBytes: number; use: 'os' | 'pool' | 'free' | 'busy'; mounted: boolean; pool?: string; owner?: string; images: number; committedGiB: number; capBytes: number; freeBytes?: number; rotational?: boolean; signature?: string }

export const GiB = 1073741824
export const FS_SHARE = 0.98
export const debianUsable = (size: number) => Math.max(0, size * 0.93 - 12 * GiB)
export const systemPoolUsable = (size: number) => Math.max(0, size * 0.95 - 8 * GiB)
export interface Choice { value: string; label: string; disabled?: boolean }
export interface Slots { system: string; data: string }

const devName = (dev?: string, key = '') => (dev ? dev.replace(/^\/dev\//, '') : key)
export const invKey = (d: Inventory['disks'][number]) => d.key || d.devPath

export function parseChoice(c: string) {
  const i = c.indexOf(':')
  return i < 0 ? { whole: false, key: '' } : { whole: c.slice(0, i) === 'whole', key: c.slice(i + 1) }
}

export function defaultDebian(cands: Inventory['disks']): string {
  if (cands.length === 0) return ''
  const pick = cands.length > 1 ? [...cands].sort((a, b) => a.sizeBytes - b.sizeBytes)[0] : cands[0]
  return invKey(pick)
}

export function inventoryDisks(cands: Inventory['disks'], debianKey: string): DiskOpt[] {
  return cands.map((d) => {
    const key = invKey(d)
    const os = key === debianKey
    return { key, name: devName(d.devPath, key), sizeBytes: d.sizeBytes, use: os ? 'os' : 'free', mounted: true, images: 0, committedGiB: 0, capBytes: os ? debianUsable(d.sizeBytes) : d.sizeBytes * FS_SHARE, rotational: d.rotational }
  })
}

export function hostDisks(lh: LabHost): DiskOpt[] {
  const pools = lh.capacity.pools ?? []
  const vms = lh.vms ?? []
  return (lh.capacity.disks ?? []).map((d) => {
    const pool = d.use === 'os' ? 'system' : d.pool
    const p = pools.find((x) => x.name === pool)
    const onPool = vms.flatMap((v) => (v.disks ?? []).filter((x) => !x.device && (x.pool || 'system') === pool))
    const owner = vms.find((v) => (v.disks ?? []).some((x) => x.device === d.id))?.name
    const images = new Set(vms.filter((v) => (v.disks ?? []).some((x) => !x.device && (x.pool || 'system') === pool)).map((v) => v.name)).size
    const capBytes = d.use === 'os' ? (p?.sizeBytes ? systemPoolUsable(p.sizeBytes) : debianUsable(d.sizeBytes)) : p?.mounted && p.sizeBytes ? p.sizeBytes : d.sizeBytes * FS_SHARE
    return { key: d.key, name: devName(d.devPath, d.key), sizeBytes: d.sizeBytes, use: d.use, mounted: d.use !== 'pool' || !!p?.mounted, pool, owner, images, committedGiB: onPool.reduce((s, x) => s + x.gib, 0), capBytes, freeBytes: p?.mounted ? p.freeBytes : undefined, rotational: d.rotational, signature: d.signature }
  })
}

export const imageable = (d: DiskOpt) => (d.use === 'free' && !d.owner) || (d.use === 'pool' && d.mounted)
export const wholeable = (d: DiskOpt) => (d.use === 'free' && !d.owner) || (d.use === 'pool' && d.images === 0)

export function effectiveSlots(rows: VMSize[], disks: DiskOpt[]): Slots[] {
  const whole = new Set(rows.flatMap((r) => [r.systemDisk ?? '', r.dataDisk ?? '']).map(parseChoice).filter((c) => c.whole).map((c) => c.key))
  const best = disks.filter((d) => d.use !== 'os' && imageable(d) && !whole.has(d.key)).sort((a, b) => b.sizeBytes - a.sizeBytes)[0]
  const system = best ? `image:${best.key}` : `image:${DEBIAN}`
  return rows.map((r) => ({ system: r.systemDisk ?? system, data: r.dataDisk ?? '' }))
}

export function diskChoices(rows: VMSize[], disks: DiskOpt[], i: number, slot: keyof Slots): Choice[] {
  const slots = effectiveSlots(rows, disks)
  const others = slots.flatMap((s, j) => (['system', 'data'] as const).filter((k) => j !== i || k !== slot).map((k) => parseChoice(s[k])))
  const wholeElsewhere = new Set(others.filter((c) => c.whole).map((c) => c.key))
  const imagedElsewhere = new Set(others.filter((c) => !c.whole && c.key).map((c) => c.key))
  const size = (d: DiskOpt) => fmt.bytes(d.sizeBytes)
  const out: Choice[] = slot === 'data' ? [{ value: '', label: 'On the system disk' }] : []
  for (const d of disks) {
    if (d.use === 'os') out.push({ value: `image:${DEBIAN}`, label: `Image on ${d.name} · ${size(d)} (Debian)` })
    else if (imageable(d)) out.push({ value: `image:${d.key}`, label: `Image on ${d.name} · ${size(d)}`, disabled: wholeElsewhere.has(d.key) })
  }
  for (const d of disks) {
    if (d.use !== 'os' && wholeable(d)) out.push({ value: `whole:${d.key}`, label: `Whole ${d.name} · ${size(d)}`, disabled: wholeElsewhere.has(d.key) || imagedElsewhere.has(d.key) })
  }
  return out
}

export function storagePlan(rows: VMSize[], disks: DiskOpt[]) {
  const slots = effectiveSlots(rows, disks)
  const byKey = new Map(disks.map((d) => [d.key, d]))
  const osDisk = disks.find((d) => d.use === 'os')
  const disk = (key: string) => (key === DEBIAN ? osDisk : byKey.get(key))
  const formats = new Set<string>(), wholes = new Set<string>()
  const committed = new Map<DiskOpt, number>()
  let problem: string | null = null
  const seenWhole = new Set<string>(), seenImage = new Set<string>()
  slots.forEach((s, i) => {
    const r = rows[i]
    for (const slot of ['system', 'data'] as const) {
      const c = parseChoice(s[slot])
      if (!c.key) continue
      const d = disk(c.key)
      if (!d) { problem = `disk ${c.key} is not available`; continue }
      if (c.whole) {
        if (d.use === 'os') problem = `${d.name} holds Debian`
        else if (seenWhole.has(d.key)) problem = `${d.name} is chosen twice`
        else if (seenImage.has(d.key)) problem = `${d.name} cannot hold images and be used whole`
        seenWhole.add(d.key)
        wholes.add(d.name)
        continue
      }
      if (seenWhole.has(d.key)) problem = `${d.name} cannot hold images and be used whole`
      seenImage.add(d.key)
      if (d.use === 'free') formats.add(d.name)
      committed.set(d, (committed.get(d) ?? d.committedGiB) + (slot === 'system' ? r.diskGiB : r.dataGiB))
    }
  })
  const over = [...committed].filter(([d, gib]) => gib * GiB > d.capBytes).map(([d]) => d.name)
  return { slots, formats: [...formats], wholes: [...wholes], over, problem: problem as string | null }
}

export function withSlots(rows: VMSize[], disks: DiskOpt[]): VMSize[] {
  const slots = effectiveSlots(rows, disks)
  return rows.map((r, i) => ({ ...r, systemDisk: slots[i].system, dataDisk: slots[i].data, dataGiB: slots[i].data.startsWith('image:') ? r.dataGiB : 0 }))
}

export function diskText(lh: LabHost, d: { pool?: string; device?: string; gib: number }) {
  if (d.device) {
    const disk = (lh.capacity.disks ?? []).find((x) => x.id === d.device)
    return `whole ${disk ? devName(disk.devPath, disk.key) : d.device.replace(/^.*\//, '')}`
  }
  const pool = (lh.capacity.pools ?? []).find((p) => p.name === (d.pool || 'system'))
  const disk = pool?.name !== 'system' && pool?.disk ? (lh.capacity.disks ?? []).find((x) => x.key === pool.disk) : undefined
  return `${d.gib} GiB${disk ? ` on ${devName(disk.devPath, disk.key)}` : ''}`
}

export function vmDisksText(lh: LabHost | undefined, vm: LabVM) {
  if (!lh || !vm.disks?.length) return `${vm.diskGiB} GiB${vm.dataGiB ? ` + ${vm.dataGiB} GiB data` : ''}`
  const system = vm.disks.find((d) => d.target === 'vda')
  const data = vm.disks.find((d) => d.target === 'vdb')
  return `${system ? diskText(lh, system) : `${vm.diskGiB} GiB`}${data ? ` + data ${diskText(lh, data)}` : ''}`
}
