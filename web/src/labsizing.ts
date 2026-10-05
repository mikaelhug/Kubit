import type { VMSize } from './api'
import { DEBIAN, FS_SHARE, GiB, effectiveSlots, imageable, parseChoice, wholeable, type DiskOpt } from './labdisks'

export const MIN_VM_MIB = 2048
export const PREFERRED_MIB = 3072
export const MAC_MAX_MIB = 4096
export const EPH_CHOICES = [40, 30, 25, 20]
export const ADD_EPH_GIB = 40

const USE = 0.9
const TALOS_GIB = 3
const MIN_SYSTEM_DATA_GIB = 10
const MIN_DATA_GIB = 10
const EPH_FLOOR = 25
const VOLUMES_MIN_GIB = 15
const LONGHORN_SHARE = 0.95
const WHOLE_SHARE = 0.93
const MAX_VMS = 6
const THREE_CP_VMS = 5
const ROOMY_MIB = 4096
const ROOMY_EPH_GIB = 30
const ROOMY_LONGHORN_GIB = 20
const CP_MAX_MIB = 8192
const HOST_CPUS = 2
const CP_MAX_CPUS = 4
const VM_MAX_CPUS = 16
const MAC_SHARE = 0.6
const MAC_MAX_GIB = 60
const UNKNOWN_DISK_GIB = 60

export interface SizingInput { memMiB: number; cpus: number; vcpusUsed?: number; disks?: DiskOpt[]; macFreeGiB?: number; cluster: boolean; mac?: boolean; most?: number; eph?: number }
export type Limit = 'memory' | 'cpu' | 'disk' | null
export interface Suggestion { rows: VMSize[]; ephGiB: number; longhornGiB: number; replicas: number; limit: Limit; fits: boolean }

interface Store { key: string; budget: number; max: number; fast: boolean }
interface Whole { key: string; gib: number; fast: boolean }
interface Place { system: string; systemGiB: number; data: string; dataGiB: number }
interface Layout { kind: 'A' | 'B' | 'C' | 'D'; places: Place[]; longhorn: number[]; slow: boolean }

const order = { A: 0, C: 1, B: 2, D: 3 }
const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v))

export function imageBudgetGiB(d: DiskOpt) {
  return Math.floor((Math.max(0, Math.min(d.freeBytes ?? Infinity, d.capBytes - d.committedGiB * GiB)) * USE) / GiB)
}

export function volumeGiB(longhorn: number[], replicas: number) {
  if (longhorn.length === 0 || replicas < 1) return 0
  let v = Math.floor(longhorn.reduce((s, l) => s + l, 0) / replicas)
  for (;;) {
    const next = Math.floor(longhorn.reduce((s, l) => s + Math.min(l, v), 0) / replicas)
    if (next >= v) return v
    v = next
  }
}

function spread(n: number, stores: Store[]): { key: string; gib: number }[] | null {
  if (stores.length === 0) return null
  const count = new Map(stores.map((s) => [s.key, 0]))
  const picks: Store[] = []
  for (let i = 0; i < n; i++) {
    const s = [...stores].sort((a, b) => b.budget / (count.get(b.key)! + 1) - a.budget / (count.get(a.key)! + 1))[0]
    count.set(s.key, count.get(s.key)! + 1)
    picks.push(s)
  }
  return picks.map((s) => ({ key: s.key, gib: Math.min(s.max, Math.floor(s.budget / count.get(s.key)!)) }))
}

function placeFixed(n: number, gib: number, stores: Store[]): string[] | null {
  const left = new Map(stores.map((s) => [s.key, s.budget]))
  const out: string[] = []
  for (let i = 0; i < n; i++) {
    const s = stores.filter((x) => left.get(x.key)! >= gib && x.max >= gib).sort((a, b) => Number(b.fast) - Number(a.fast) || Number(b.key === DEBIAN) - Number(a.key === DEBIAN) || left.get(b.key)! - left.get(a.key)!)[0]
    if (!s) return null
    left.set(s.key, left.get(s.key)! - gib)
    out.push(s.key)
  }
  return out
}

function layouts(n: number, eph: number, stores: Store[], wholes: Whole[]): Layout[] {
  const out: Layout[] = []
  const fastOf = new Map(stores.map((s) => [s.key, s.fast]))
  const systemGiB = eph + TALOS_GIB
  if (wholes.length >= n) {
    const used = wholes.slice(0, n)
    const free = stores.filter((s) => !used.some((w) => w.key === s.key))
    const systems = placeFixed(n, systemGiB, free)
    if (systems) out.push({ kind: 'A', places: used.map((w, i) => ({ system: `image:${systems[i]}`, systemGiB, data: `whole:${w.key}`, dataGiB: 0 })), longhorn: used.map((w) => WHOLE_SHARE * w.gib), slow: systems.some((k) => !fastOf.get(k)) })
    const split = used.map((w) => LONGHORN_SHARE * (FS_SHARE * w.gib - eph - TALOS_GIB))
    if (split.every((l) => l >= LONGHORN_SHARE * MIN_SYSTEM_DATA_GIB)) out.push({ kind: 'B', places: used.map((w) => ({ system: `whole:${w.key}`, systemGiB: 0, data: '', dataGiB: 0 })), longhorn: split, slow: used.some((w) => !w.fast) })
  }
  const systems = stores.some((s) => s.key !== DEBIAN) ? placeFixed(n, systemGiB, stores) : null
  if (systems) {
    const left = stores.map((s) => ({ ...s, budget: s.budget - systemGiB * systems.filter((k) => k === s.key).length }))
    const data = spread(n, left.filter((s) => s.budget >= MIN_DATA_GIB))
    if (data && data.every((d) => d.gib >= MIN_DATA_GIB)) out.push({ kind: 'C', places: data.map((d, i) => ({ system: `image:${systems[i]}`, systemGiB, data: `image:${d.key}`, dataGiB: d.gib })), longhorn: data.map((d) => LONGHORN_SHARE * d.gib), slow: systems.some((k) => !fastOf.get(k)) })
  }
  const need = eph + TALOS_GIB + MIN_SYSTEM_DATA_GIB
  const fast = stores.filter((s) => s.fast)
  for (const pool of fast.length < stores.length ? [fast, stores] : [stores]) {
    const split = spread(n, pool)
    if (split && split.every((s) => s.gib >= need)) {
      out.push({ kind: 'D', places: split.map((s) => ({ system: `image:${s.key}`, systemGiB: s.gib, data: '', dataGiB: 0 })), longhorn: split.map((s) => LONGHORN_SHARE * (s.gib - eph - TALOS_GIB)), slow: split.some((s) => !fastOf.get(s.key)) })
      break
    }
  }
  return out
}

function bestLayout(n: number, eph: number, stores: Store[], wholes: Whole[]): Layout | null {
  const all = layouts(n, eph, stores, wholes)
  const r = Math.min(n, 3)
  all.sort((a, b) => Number(a.slow) - Number(b.slow) || volumeGiB(b.longhorn, r) - volumeGiB(a.longhorn, r) || order[a.kind] - order[b.kind])
  return all[0] ?? null
}

const greater = (a: number[], b: number[]) => {
  const i = a.findIndex((v, j) => v !== b[j])
  return i >= 0 && a[i] > b[i]
}

const roundMem = (m: number) => Math.max(MIN_VM_MIB, m >= 8192 ? Math.floor(m / 1024) * 1024 : Math.floor(m / 256) * 256)

function sized(n: number, input: SizingInput): VMSize[] {
  const most = input.most ?? Infinity
  const mem = input.memMiB
  const cps = input.cluster ? (n >= THREE_CP_VMS ? 3 : 1) : 0
  const workers = n - cps
  const cpus = input.cpus
  const used = input.vcpusUsed ?? 0
  const budget = cpus > 0 ? (input.mac ? Math.max(2, Math.floor(cpus / 2)) : Math.max(2, cpus - HOST_CPUS - used)) : 2 * n
  const per = cpus > 0 ? Math.min(VM_MAX_CPUS, cpus) : 2
  const byMem = (m: number) => Math.max(2, Math.floor(m / 1024))
  if (cps > 0 && workers > 0) {
    const cpMem = roundMem(Math.min(most, clamp(mem / n, MIN_VM_MIB, CP_MAX_MIB)))
    const wMem = roundMem(Math.min(most, (mem - cps * cpMem) / workers))
    const cpCpu = clamp(Math.min(Math.floor(budget / n), byMem(cpMem)), 2, CP_MAX_CPUS)
    const wCpu = clamp(Math.min(Math.floor((budget - cps * cpCpu) / workers), byMem(wMem)), 2, per)
    return Array.from({ length: n }, (_, i) => (i < cps ? { role: 'controlplane', cpus: cpCpu, memMiB: cpMem, diskGiB: 0, dataGiB: 0 } : { role: 'worker', cpus: wCpu, memMiB: wMem, diskGiB: 0, dataGiB: 0 }))
  }
  const each = roundMem(Math.min(most, mem / n))
  const c = clamp(Math.min(Math.floor(budget / n), byMem(each)), 2, per)
  return Array.from({ length: n }, (_, i) => ({ role: cps > 0 && i < cps ? 'controlplane' : 'worker', cpus: c, memMiB: each, diskGiB: 0, dataGiB: 0 }))
}

export function suggestPlan(input: SizingInput): Suggestion {
  const mem = input.memMiB
  if (mem <= 0) {
    const rows: VMSize[] = Array.from({ length: input.cluster ? 4 : 1 }, (_, i) => ({ role: input.cluster && i === 0 ? 'controlplane' : 'worker', cpus: 2, memMiB: PREFERRED_MIB, diskGiB: UNKNOWN_DISK_GIB, dataGiB: 0 }))
    return { rows, ephGiB: 40, longhornGiB: 0, replicas: Math.min(rows.length, 3), limit: null, fits: true }
  }
  const nRam = Math.floor(mem / PREFERRED_MIB) >= 2 ? Math.floor(mem / PREFERRED_MIB) : Math.max(1, Math.floor(mem / MIN_VM_MIB))
  const nCpu = input.cpus > 0 ? Math.max(1, Math.floor((2 * input.cpus - (input.vcpusUsed ?? 0)) / 2)) : Infinity
  const maxN = Math.max(1, Math.min(MAX_VMS, nRam, nCpu))
  let stores: Store[] = []
  let wholes: Whole[] = []
  if (input.mac) {
    stores = [{ key: DEBIAN, budget: Math.floor((input.macFreeGiB ?? 0) * MAC_SHARE), max: MAC_MAX_GIB, fast: true }]
  } else if (input.disks?.length) {
    const usable = input.disks.filter((d) => !(d.use === 'free' && d.signature))
    stores = usable.filter((d) => d.use === 'os' || imageable(d)).map((d) => ({ key: d.use === 'os' ? DEBIAN : d.key, budget: imageBudgetGiB(d), max: Infinity, fast: d.rotational !== true }))
    if (!stores.some((s) => s.fast)) stores = stores.map((s) => ({ ...s, fast: true }))
    wholes = usable.filter((d) => d.use !== 'os' && wholeable(d)).sort((a, b) => b.sizeBytes - a.sizeBytes).map((d) => ({ key: d.key, gib: d.sizeBytes / GiB, fast: d.rotational !== true }))
  }
  if (stores.length === 0) {
    const n = Math.min(input.cluster ? 4 : maxN, maxN)
    const rows = sized(n, input).map((r) => ({ ...r, diskGiB: UNKNOWN_DISK_GIB }))
    return { rows, ephGiB: 40, longhornGiB: 0, replicas: Math.min(n, 3), limit: n < 4 ? (n >= nRam ? 'memory' : 'cpu') : null, fits: true }
  }
  const vcpuBudget = input.cpus > 0 ? input.cpus - HOST_CPUS - (input.vcpusUsed ?? 0) : Infinity
  const ephs = input.eph ? [input.eph] : EPH_CHOICES
  let best: { n: number; eph: number; layout: Layout; vol: number; key: number[] } | null = null
  const roomy = (n: number, eph: number, l: Layout) => mem / n >= ROOMY_MIB && eph >= ROOMY_EPH_GIB && Math.min(...l.longhorn) >= ROOMY_LONGHORN_GIB && vcpuBudget >= 2 * n
  for (let n = 1; n <= maxN; n++) {
    for (const eph of ephs) {
      const layout = bestLayout(n, eph, stores, wholes)
      if (!layout) continue
      if (n > 3 && !roomy(n, eph, layout)) continue
      const vol = volumeGiB(layout.longhorn, Math.min(n, 3))
      const ok = eph >= EPH_FLOOR && (n === 1 || vol >= VOLUMES_MIN_GIB)
      const key = [Number(ok), Math.min(n, 3), Number(!layout.slow), n, eph, vol]
      if (!best || greater(key, best.key)) best = { n, eph, layout, vol, key }
    }
  }
  if (!best) {
    const largest = [...stores].sort((a, b) => b.budget - a.budget)[0]
    const rows = sized(1, input).map((r) => ({ ...r, diskGiB: Math.max(33, Math.min(largest.max, largest.budget)), systemDisk: input.mac ? undefined : `image:${largest.key}`, dataDisk: input.mac ? undefined : '' }))
    return { rows, ephGiB: 20, longhornGiB: 0, replicas: 1, limit: 'disk', fits: false }
  }
  const { n, eph, layout, vol } = best
  const rows = sized(n, input).map((r, i) => {
    const p = layout.places[i]
    const sys = parseChoice(p.system)
    return { ...r, diskGiB: sys.whole ? eph + TALOS_GIB + MIN_SYSTEM_DATA_GIB : p.systemGiB, dataGiB: p.data.startsWith('image:') ? p.dataGiB : 0, ...(input.mac ? {} : { systemDisk: p.system, dataDisk: p.data }) }
  })
  let limit: Limit = null
  if (n < MAX_VMS) {
    const next = n + 1
    limit = next > nRam ? 'memory' : next > nCpu ? 'cpu' : next > 3 && mem / next < ROOMY_MIB ? 'memory' : next > 3 && vcpuBudget < 2 * next ? 'cpu' : 'disk'
  }
  return { rows, ephGiB: eph, longhornGiB: vol, replicas: Math.min(n, 3), limit, fits: true }
}

export function longhorn(rows: VMSize[], disks: DiskOpt[] | undefined, eph: number) {
  const slots = disks ? effectiveSlots(rows, disks) : rows.map((r) => ({ system: '', data: r.dataGiB > 0 ? 'image:' : '' }))
  const byKey = new Map((disks ?? []).map((d) => [d.key, d]))
  const per = rows.map((r, i) => {
    const sys = parseChoice(slots[i].system)
    const data = parseChoice(slots[i].data)
    if (data.whole) return WHOLE_SHARE * (byKey.get(data.key)?.sizeBytes ?? 0) / GiB
    if (slots[i].data) return LONGHORN_SHARE * r.dataGiB
    const systemGiB = sys.whole ? FS_SHARE * (byKey.get(sys.key)?.sizeBytes ?? 0) / GiB : r.diskGiB
    return Math.max(0, LONGHORN_SHARE * (systemGiB - eph - TALOS_GIB))
  })
  const replicas = Math.min(rows.length, 3)
  return { gib: volumeGiB(per, replicas), replicas, tight: per.flatMap((l, i) => (l < LONGHORN_SHARE * MIN_SYSTEM_DATA_GIB ? [i] : [])) }
}
