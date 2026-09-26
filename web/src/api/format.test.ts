import { describe, expect, it } from 'vitest'
import { fmt } from './format'

describe('fmt.age', () => {
  it('uses seconds, minutes, hours and days', () => {
    expect(fmt.age(0)).toBe('0 s')
    expect(fmt.age(59.9)).toBe('59 s')
    expect(fmt.age(90)).toBe('2 min')
    expect(fmt.age(2 * 3600)).toBe('2 h')
    expect(fmt.age(3 * 86400)).toBe('3 d')
  })

  it('never goes negative', () => {
    expect(fmt.age(-5)).toBe('0 s')
  })
})

describe('fmt.duration', () => {
  const from = '2026-01-01T00:00:00Z'
  const at = (sec: number) => new Date(Date.parse(from) + sec * 1000).toISOString()

  it('spans from seconds to days', () => {
    expect(fmt.duration(from, at(0.5))).toBe('<1s')
    expect(fmt.duration(from, at(42))).toBe('42s')
    expect(fmt.duration(from, at(125))).toBe('2m 5s')
    expect(fmt.duration(from, at(2 * 3600 + 5 * 60))).toBe('2h 5m')
    expect(fmt.duration(from, at(3 * 86400 + 4 * 3600))).toBe('3d 4h')
  })

  it('measures an open span against the given clock', () => {
    expect(fmt.duration(from, undefined, Date.parse(from) + 61_000)).toBe('1m 1s')
    expect(fmt.duration(undefined)).toBe('')
  })
})
