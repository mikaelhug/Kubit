import { describe, expect, it } from 'vitest'
import { ipAt, isDnsLabel, nextHostname, nodeHostname, staticNetwork, subnet24 } from './net'

describe('isDnsLabel', () => {
  it('accepts lowercase labels', () => {
    for (const s of ['a', 'lab', 'home-lab-01', '0x']) expect(isDnsLabel(s)).toBe(true)
  })

  it('rejects everything else', () => {
    for (const s of ['', '-lab', 'lab-', 'Lab', 'lab_1', 'lab.one']) expect(isDnsLabel(s)).toBe(false)
  })
})

describe('address helpers', () => {
  it('derive the /24 of an address', () => {
    expect(subnet24('192.168.1.42')).toBe('192.168.1.0/24')
  })

  it('pin a static /24 with the .1 gateway', () => {
    expect(staticNetwork('10.0.5.17')).toEqual({ addresses: ['10.0.5.17/24'], gateway: '10.0.5.1' })
  })

  it('walk a range across an octet', () => {
    expect(ipAt('192.168.1.254-192.168.2.4', 3)).toBe('192.168.2.1')
    expect(ipAt('bogus', 0)).toBe('')
  })
})

describe('hostnames', () => {
  it('name nodes after cluster and pool', () => {
    expect(nodeHostname('lab', 'controlplane', 1)).toBe('lab-cp-01')
    expect(nodeHostname('lab', 'gpu', 12)).toBe('lab-gpu-12')
  })

  it('skip names already taken', () => {
    expect(nextHostname('lab', 'worker', 1, ['lab-worker-01'])).toBe('lab-worker-02')
    expect(nextHostname('lab', 'worker', 1, ['lab-worker-02', 'lab-worker-03'])).toBe('lab-worker-04')
  })
})
