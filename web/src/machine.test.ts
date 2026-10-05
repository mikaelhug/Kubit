import { describe, expect, it } from 'vitest'
import type { NodeRow } from './api'
import { nodeEntry } from './machine'

const row = (inv: Partial<NonNullable<NodeRow['inventory']>>): NodeRow => ({
  ip: '10.0.0.21', mac: 'aa:bb:cc:dd:ee:21', cluster: '', hostname: '', pool: '', arch: 'amd64', role: '', source: 'scan', state: 'maintenance', kind: 'maintenance', talos: true, talosVersion: 'v1.11.0', firstSeen: '', lastSeen: '',
  inventory: { ip: '10.0.0.21', cpus: 4, memoryBytes: 8 << 30, kvm: false, arch: 'amd64', talosVersion: 'v1.11.0', platform: 'metal', stage: 'maintenance', disks: [], links: [], ...inv },
})

describe('nodeEntry', () => {
  it('prefers the largest SSD and carries hardware flags', () => {
    const m = row({ kvm: true, tpm: true, disks: [
      { devPath: '/dev/sda', sizeBytes: 4e12, rotational: true, readonly: false, cdrom: false },
      { devPath: '/dev/nvme0n1', sizeBytes: 5e11, rotational: false, readonly: false, cdrom: false },
      { devPath: '/dev/sdb', sizeBytes: 3e10, rotational: false, readonly: false, cdrom: false, transport: 'usb' },
    ] })
    expect(nodeEntry(m, 'w-01')).toBe('- hostname: w-01\n  ip: 10.0.0.21\n  mac: "aa:bb:cc:dd:ee:21"\n  role: worker\n  arch: amd64\n  installDisk: { path: /dev/nvme0n1 }\n  kvm: true\n  tpm: true\n')
  })
  it('names an unnamed machine after its MAC', () => {
    expect(nodeEntry(row({}))).toContain('- hostname: node-ee21\n')
  })
})
