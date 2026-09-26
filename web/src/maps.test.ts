import { signal } from '@preact/signals'
import { describe, expect, it } from 'vitest'
import { copyMap, editMap, setIn } from './maps'

describe('map helpers', () => {
  it('copyMap edits a copy', () => {
    const a = new Map([['x', 1]])
    const b = copyMap(a, (m) => m.set('y', 2))
    expect([...a.keys()]).toEqual(['x'])
    expect([...b.entries()]).toEqual([['x', 1], ['y', 2]])
  })

  it('setIn and editMap replace the signal value', () => {
    const s = signal(new Map<string, number>())
    const before = s.value
    setIn(s, 'a', 1)
    expect(s.value).not.toBe(before)
    expect(s.value.get('a')).toBe(1)
    editMap(s, (m) => m.delete('a'))
    expect(s.value.size).toBe(0)
  })
})
