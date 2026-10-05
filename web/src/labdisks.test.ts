import { describe, expect, it } from 'vitest'
import type { Inventory, LabHost, VMSize } from './api'
import { debianUsable, defaultDebian, diskChoices, diskText, effectiveSlots, GiB, hostDisks, inventoryDisks, storagePlan, systemPoolUsable, withSlots } from './labdisks'

const GB = 1e9
const inv: Inventory['disks'] = [
  { devPath: '/dev/sda', sizeBytes: 300 * GB, rotational: true, readonly: false, cdrom: false, key: 'wwn-a' },
  { devPath: '/dev/sdb', sizeBytes: 2000 * GB, rotational: true, readonly: false, cdrom: false, key: 'wwn-b' },
  { devPath: '/dev/sdc', sizeBytes: 900 * GB, rotational: false, readonly: false, cdrom: false, key: 'wwn-c' },
]
const vm = (patch: Partial<VMSize> = {}): VMSize => ({ role: 'worker', cpus: 2, memMiB: 3072, diskGiB: 60, dataGiB: 0, ...patch })

describe('lab host disks', () => {
  it('puts Debian on the smallest disk and VM images on the largest other one', () => {
    expect(defaultDebian(inv)).toBe('wwn-a')
    expect(defaultDebian(inv.slice(1, 2))).toBe('wwn-b')
    expect(defaultDebian([])).toBe('')
    const disks = inventoryDisks(inv, 'wwn-a')
    expect(effectiveSlots([vm(), vm({ systemDisk: 'image:debian' })], disks)).toEqual([{ system: 'image:wwn-b', data: '' }, { system: 'image:debian', data: '' }])
    expect(effectiveSlots([vm(), vm({ systemDisk: 'whole:wwn-b' })], disks)[0].system).toBe('image:wwn-c')
    expect(effectiveSlots([vm()], inventoryDisks(inv.slice(0, 1), 'wwn-a'))[0].system).toBe('image:debian')
  })

  it('offers images on any disk and whole disks once', () => {
    const disks = inventoryDisks(inv, 'wwn-a')
    const rows = [vm({ systemDisk: 'whole:wwn-c' }), vm({ systemDisk: 'image:wwn-b' })]
    const system = diskChoices(rows, disks, 1, 'system')
    expect(system.map((c) => c.value)).toEqual(['image:debian', 'image:wwn-b', 'image:wwn-c', 'whole:wwn-b', 'whole:wwn-c'])
    expect(system.find((c) => c.value === 'image:wwn-c')?.disabled).toBe(true)
    expect(system.find((c) => c.value === 'whole:wwn-c')?.disabled).toBe(true)
    expect(system.find((c) => c.value === 'whole:wwn-b')?.disabled).toBe(false)
    const data = diskChoices(rows, disks, 1, 'data')
    expect(data[0]).toEqual({ value: '', label: 'On the system disk' })
    expect(data.find((c) => c.value === 'whole:wwn-b')?.disabled).toBe(true)
    expect(system[0].label).toMatch(/^Image on sda · .* \(Debian\)$/)
  })

  it('lists what gets wiped, what may outgrow its disk, and conflicts', () => {
    const disks = inventoryDisks(inv, 'wwn-a')
    const plan = storagePlan([vm({ systemDisk: 'image:wwn-b', dataDisk: 'image:wwn-b', dataGiB: 1900 }), vm({ systemDisk: 'whole:wwn-c' })], disks)
    expect(plan.formats).toEqual(['sdb'])
    expect(plan.wholes).toEqual(['sdc'])
    expect(plan.over).toEqual(['sdb'])
    expect(plan.problem).toBeNull()
    expect(storagePlan([vm({ systemDisk: 'whole:wwn-c' }), vm({ dataDisk: 'whole:wwn-c' })], disks).problem).toBe('sdc is chosen twice')
    expect(storagePlan([vm({ systemDisk: 'image:wwn-c' }), vm({ dataDisk: 'whole:wwn-c' })], disks).problem).toBe('sdc cannot hold images and be used whole')
    expect(withSlots([vm({ dataGiB: 50 })], disks)[0]).toMatchObject({ systemDisk: 'image:wwn-b', dataDisk: '', dataGiB: 0 })
  })

  it('reads the host: storage disks, owners and committed images', () => {
    const lh = {
      state: 'ready', updatedAt: '', vms: [
        { name: 'db', mac: 'm1', state: 'running', cpus: 2, memMiB: 4096, diskGiB: 60, boot: 'disk', disks: [{ target: 'vda', pool: 'pool1', gib: 60 }, { target: 'vdb', device: '/dev/disk/by-id/wwn-c', gib: 838 }] },
        { name: 'web', mac: 'm2', state: 'running', cpus: 2, memMiB: 4096, diskGiB: 60, boot: 'disk', disks: [{ target: 'vda', pool: 'system', gib: 60 }] },
      ],
      capacity: {
        cpus: 16, memMiB: 65536, diskGiB: 0, kvm: true, kernel: '', libvirt: '', hostname: 'lab', arch: 'amd64', bridge: 'br0', ready: true, checkedAt: '',
        disks: [
          { key: 'wwn-a', id: '/dev/disk/by-id/wwn-a', devPath: '/dev/sda', sizeBytes: 300 * GB, use: 'os' },
          { key: 'wwn-b', id: '/dev/disk/by-id/wwn-b', devPath: '/dev/sdb', sizeBytes: 2000 * GB, use: 'pool', pool: 'pool1' },
          { key: 'wwn-c', id: '/dev/disk/by-id/wwn-c', devPath: '/dev/sdc', sizeBytes: 900 * GB, use: 'free' },
        ],
        pools: [{ name: 'system', dir: '/var/lib/kubit/vms', disk: 'wwn-a', sizeBytes: 290 * GB, freeBytes: 200 * GB, mounted: true }, { name: 'pool1', dir: '/var/lib/kubit/pools/pool1', disk: 'wwn-b', sizeBytes: 1990 * GB, freeBytes: 1900 * GB, mounted: true }],
      },
    } as LabHost
    const disks = hostDisks(lh)
    expect(disks.map((d) => [d.name, d.use, d.images, d.committedGiB, d.owner])).toEqual([['sda', 'os', 1, 60, undefined], ['sdb', 'pool', 1, 60, undefined], ['sdc', 'free', 0, 0, 'db']])
    const choices = diskChoices([vm()], disks, 0, 'system').map((c) => c.value)
    expect(choices).toEqual(['image:debian', 'image:wwn-b'])
    expect(diskText(lh, lh.vms![0].disks![0])).toBe('60 GiB on sdb')
    expect(diskText(lh, lh.vms![0].disks![1])).toBe('whole sdc')
    expect(diskText(lh, lh.vms![1].disks![0])).toBe('60 GiB')
  })

  it('models what Debian leaves for VM images', () => {
    expect(Math.round(debianUsable(128 * GB) / GiB)).toBe(99)
    expect(debianUsable(10 * GB)).toBe(0)
    expect(Math.round(systemPoolUsable(110 * GiB) / GiB)).toBe(97)
    const disks = inventoryDisks(inv, 'wwn-a')
    expect(Math.round(disks[0].capBytes / GiB)).toBe(Math.round(debianUsable(300 * GB) / GiB))
    expect(disks[1].capBytes).toBe(2000 * GB * 0.98)
    expect(storagePlan([vm({ systemDisk: 'whole:wwn-a' })], disks).problem).toBe('sda holds Debian')
  })
})
