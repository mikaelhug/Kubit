import { describe, expect, it } from 'vitest'
import type { Inventory, VMSize } from './api'
import { inventoryDisks, type DiskOpt } from './labdisks'
import { longhorn, suggestPlan, volumeGiB } from './labsizing'

const GB = 1e9
const MiB = (gib: number) => Math.round(gib * 1024)
const disk = (devPath: string, gb: number, rotational = false): Inventory['disks'][number] => ({ devPath, sizeBytes: gb * GB, rotational, readonly: false, cdrom: false, key: devPath.replace('/dev/', 'wwn-') })
const plan = (cands: Inventory['disks'][number][], debian = cands[0]) => inventoryDisks(cands, debian.key!)
const shape = (rows: VMSize[]) => rows.map((r) => `${r.role === 'controlplane' ? 'cp' : 'w'} ${r.cpus}c ${r.memMiB}M ${r.systemDisk ?? ''}:${r.diskGiB} ${r.dataDisk ?? ''}:${r.dataGiB}`)

describe('lab VM sizing', () => {
  it('fits a 128 GB server disk instead of overprovisioning it', () => {
    const s = suggestPlan({ memMiB: MiB(125.8) - 2048, cpus: 32, disks: plan([disk('/dev/sda', 128)]), cluster: true })
    expect(s.rows.map((r) => r.role)).toEqual(['controlplane', 'worker'])
    expect(s.rows[0]).toMatchObject({ cpus: 4, memMiB: 8192, systemDisk: 'image:debian', dataDisk: '' })
    expect(s.rows[1].cpus).toBe(16)
    expect(s.rows.every((r) => r.diskGiB === 44)).toBe(true)
    expect(s).toMatchObject({ ephGiB: 25, longhornGiB: 15, replicas: 2, limit: 'disk', fits: true })
    expect(s.rows.reduce((t, r) => t + r.diskGiB, 0)).toBeLessThanOrEqual(99)
  })

  it('sizes a small desktop by disk and memory', () => {
    const elite = suggestPlan({ memMiB: MiB(15.5) - 2048, cpus: 4, disks: plan([disk('/dev/nvme0n1', 256)]), cluster: true })
    expect(elite.rows.map((r) => r.role)).toEqual(['controlplane', 'worker', 'worker'])
    expect(elite).toMatchObject({ ephGiB: 40, replicas: 3, limit: 'memory' })
    expect(elite.rows.every((r) => r.cpus === 2 && r.diskGiB >= 53)).toBe(true)
    const mini = suggestPlan({ memMiB: MiB(7.7) - 2048, cpus: 2, disks: plan([disk('/dev/sda', 256)]), cluster: true })
    expect(mini.rows).toHaveLength(2)
    expect(mini.rows.every((r) => r.memMiB >= 2048)).toBe(true)
    expect(mini.limit).toBe('memory')
  })

  it('puts system images on the SSD and data on the large HDD', () => {
    const s = suggestPlan({ memMiB: 64 * 1024 - 2048, cpus: 16, disks: plan([disk('/dev/sda', 240), disk('/dev/sdb', 2000, true)]), cluster: true })
    expect(s.rows.filter((r) => r.role === 'controlplane')).toHaveLength(3)
    expect(s.rows).toHaveLength(5)
    expect(s.rows.every((r) => r.systemDisk === 'image:debian' && r.dataDisk === 'image:wwn-sdb' && r.dataGiB > 300)).toBe(true)
    expect(s.ephGiB).toBe(30)
  })

  it('gives each VM a whole data disk when there are enough disks', () => {
    const cands = [disk('/dev/sda', 240), ...['sdb', 'sdc', 'sdd', 'sde'].map((n) => disk(`/dev/${n}`, 1000, true))]
    const s = suggestPlan({ memMiB: 32 * 1024 - 2048, cpus: 8, disks: plan(cands), cluster: true })
    expect(s.rows).toHaveLength(3)
    expect(s.rows.every((r) => r.systemDisk === 'image:debian' && r.dataDisk?.startsWith('whole:'))).toBe(true)
    expect(new Set(s.rows.map((r) => r.dataDisk)).size).toBe(3)
    expect(s.limit).toBe('cpu')
  })

  it('fills one large disk with up to six VMs and three control planes', () => {
    const s = suggestPlan({ memMiB: 64 * 1024 - 2048, cpus: 16, disks: plan([disk('/dev/sda', 2000, true)]), cluster: true })
    expect(s.rows).toHaveLength(6)
    expect(s.rows.filter((r) => r.role === 'controlplane')).toHaveLength(3)
    expect(s).toMatchObject({ ephGiB: 40, limit: null })
  })

  it('keeps VMs on a Mac small', () => {
    const s = suggestPlan({ memMiB: 24 * 1024 - 6144, cpus: 10, macFreeGiB: 200, cluster: true, mac: true, most: 4096 })
    expect(s.rows).toHaveLength(2)
    expect(s.rows.every((r) => r.memMiB === 4096 && r.diskGiB === 60 && r.systemDisk === undefined)).toBe(true)
    expect(suggestPlan({ memMiB: 24 * 1024 - 6144, cpus: 10, macFreeGiB: 2000, cluster: true, mac: true, most: 4096 }).rows.every((r) => r.diskGiB <= 60)).toBe(true)
  })

  it('falls back when nothing is known', () => {
    expect(suggestPlan({ memMiB: 0, cpus: 0, cluster: true }).rows.map((r) => r.diskGiB)).toEqual([60, 60, 60, 60])
    const unknown = suggestPlan({ memMiB: 16 * 1024, cpus: 8, cluster: true })
    expect(unknown.rows).toHaveLength(4)
    expect(unknown.rows.every((r) => r.diskGiB === 60 && r.systemDisk === undefined)).toBe(true)
  })

  it('adds VMs where there is still room and never claims a disk with data', () => {
    const disks: DiskOpt[] = [
      { key: 'wwn-a', name: 'sda', sizeBytes: 240 * GB, use: 'os', mounted: true, images: 2, committedGiB: 86, capBytes: 210 * 1073741824, freeBytes: 120 * 1073741824 },
      { key: 'wwn-b', name: 'sdb', sizeBytes: 500 * GB, use: 'pool', pool: 'pool1', mounted: true, images: 3, committedGiB: 430, capBytes: 459 * 1073741824 },
      { key: 'wwn-c', name: 'sdc', sizeBytes: 1000 * GB, use: 'free', mounted: true, images: 0, committedGiB: 0, capBytes: 900 * 1073741824, signature: 'xfs' },
    ]
    const s = suggestPlan({ memMiB: 30 * 1024, cpus: 16, vcpusUsed: 8, disks, cluster: false, eph: 40 })
    expect(s.rows).toHaveLength(2)
    expect(s.rows.every((r) => r.role === 'worker' && r.systemDisk === 'image:debian' && r.dataGiB >= 10)).toBe(true)
    expect(s.rows.reduce((t, r) => t + r.diskGiB + r.dataGiB, 0)).toBeLessThanOrEqual(108 + 26)
    expect(s.rows.some((r) => r.systemDisk?.includes('wwn-c') || r.dataDisk?.includes('wwn-c'))).toBe(false)
  })

  it('computes Longhorn space from the rows as edited', () => {
    expect(volumeGiB([10, 20], 2)).toBe(10)
    expect(volumeGiB([100, 100, 100, 100], 3)).toBe(133)
    const disks = plan([disk('/dev/sda', 128)])
    const rows: VMSize[] = [{ role: 'controlplane', cpus: 2, memMiB: 4096, diskGiB: 44, dataGiB: 0 }, { role: 'worker', cpus: 2, memMiB: 4096, diskGiB: 30, dataGiB: 0 }]
    const l = longhorn(rows, disks, 25)
    expect(l.replicas).toBe(2)
    expect(l.tight).toEqual([1])
    expect(l.gib).toBe(1)
  })

  it('never suggests more than the hardware holds', () => {
    const tiny = suggestPlan({ memMiB: MiB(3) - 2048, cpus: 2, disks: plan([disk('/dev/sda', 64)]), cluster: true })
    expect(tiny.rows).toHaveLength(1)
    expect(tiny.rows[0].memMiB).toBe(2048)
    for (const [mem, cpus, gb] of [[8, 4, 120], [32, 8, 500], [126, 32, 128], [256, 64, 4000]]) {
      const s = suggestPlan({ memMiB: MiB(mem) - 2048, cpus, disks: plan([disk('/dev/sda', gb)]), cluster: true })
      expect(s.rows.reduce((t, r) => t + r.memMiB, 0)).toBeLessThanOrEqual(MiB(mem) - 2048)
      expect(s.rows.every((r) => r.memMiB >= 2048 && r.cpus >= 2)).toBe(true)
      expect([1, 3]).toContain(s.rows.filter((r) => r.role === 'controlplane').length)
      expect(shape(s.rows).length).toBeGreaterThan(0)
    }
  })
})
