import { describe, expect, it } from 'vitest'
import { minorAtLeast, updatesFor } from './versions'

describe('minorAtLeast', () => {
  it('compares minors numerically', () => {
    expect(minorAtLeast('1.10', '1.9')).toBe(true)
    expect(minorAtLeast('1.9', '1.10')).toBe(false)
    expect(minorAtLeast('1.34', '1.34')).toBe(true)
  })
})

describe('updatesFor', () => {
  it('offers newer stable releases only', () => {
    const v = { talos: ['v1.16.0-beta.0', 'v1.15.2'], talosSource: '', kubernetesMinors: [], kubernetesLatest: '1.35.1', machinery: '', minTalos: '', note: '' }
    expect(updatesFor('v1.15.0', '1.35.1', v)).toEqual({ talos: 'v1.15.2', kubernetes: '' })
    expect(updatesFor('v1.15.10', '1.34.0', v)).toEqual({ talos: '', kubernetes: '1.35.1' })
  })
})
