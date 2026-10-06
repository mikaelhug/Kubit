import { describe, expect, it } from 'vitest'
import { later } from './time'

describe('later', () => {
  it('compares Go RFC3339Nano instants, not strings', () => {
    expect(later('2026-01-01T10:00:00+02:00', '2026-01-01T09:30:00Z')).toBe(false)
    expect(later('2026-01-01T10:00:00.5Z', '2026-01-01T10:00:00Z')).toBe(true)
    expect(later('2026-01-02T00:00:00Z', '2026-01-01T23:59:59.999999999Z')).toBe(true)
  })
})
