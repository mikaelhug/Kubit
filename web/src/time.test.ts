import { describe, expect, it } from 'vitest'
import { appendWithin, createdSort, later } from './time'

describe('later', () => {
  it('compares instants, not strings', () => {
    expect(later('2026-01-01T10:00:00+02:00', '2026-01-01T09:30:00Z')).toBe(false)
    expect(later('2026-01-01T10:00:00.5Z', '2026-01-01T10:00:00Z')).toBe(true)
    expect(later('2026-01-02T00:00:00Z', '2026-01-01T23:59:59.999999999Z')).toBe(true)
  })

  it('is false when either side is missing', () => {
    expect(later(undefined, '2026-01-01T00:00:00Z')).toBe(false)
    expect(later('2026-01-01T00:00:00Z', '')).toBe(false)
  })
})

describe('appendWithin', () => {
  const at = (m: number) => ({ ts: new Date(Date.UTC(2026, 0, 1, 0, m)).toISOString() })

  it('appends newer points and drops those outside the span', () => {
    expect(appendWithin([at(0), at(30), at(50)], at(70), 30 * 60e3)).toEqual([at(50), at(70)])
    expect(appendWithin(null, at(1), 60e3)).toEqual([at(1)])
  })

  it('ignores points that are not newer', () => {
    const list = [at(0), at(5)]
    expect(appendWithin(list, at(5), 60e3)).toBe(list)
  })
})

describe('createdSort', () => {
  it('orders newest first ascending and unknown last', () => {
    const rows = [{ createdAt: '2026-01-01T00:00:00Z' }, {}, { createdAt: '2026-03-01T00:00:00Z' }]
    expect([...rows].sort((a, b) => createdSort(a) - createdSort(b))).toEqual([rows[2], rows[0], rows[1]])
  })
})
