import { describe, expect, it } from 'vitest'
import { ipAt, isDnsLabel, staticNetwork, subnet24 } from './net'

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
