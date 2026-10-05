import { describe, expect, it } from 'vitest'
import { ipAt, subnet24 } from './net'

describe('address helpers', () => {
  it('derive the /24 of an address', () => {
    expect(subnet24('192.168.1.42')).toBe('192.168.1.0/24')
  })

  it('walk a range across an octet', () => {
    expect(ipAt('192.168.1.254-192.168.2.4', 3)).toBe('192.168.2.1')
    expect(ipAt('bogus', 0)).toBe('')
  })
})
