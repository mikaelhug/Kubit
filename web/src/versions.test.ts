import { describe, expect, it } from 'vitest'
import { updatesFor } from './versions'

describe('updatesFor', () => {
  it('offers newer stable releases only', () => {
    const v = { talos: ['v1.16.0-beta.0', 'v1.15.2'], talosSource: '', kubernetesMinors: [], kubernetesLatest: '1.35.1', machinery: '', minTalos: '', note: '' }
    expect(updatesFor('v1.15.0', '1.35.1', v)).toEqual({ talos: 'v1.15.2', kubernetes: '' })
    expect(updatesFor('v1.15.10', '1.34.0', v)).toEqual({ talos: '', kubernetes: '1.35.1' })
  })
})
