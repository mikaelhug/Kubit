import { describe, expect, it } from 'vitest'
import { toYaml } from './yaml'

describe('toYaml', () => {
  it('renders nested values', () => {
    expect(toYaml({ a: 1, b: { c: 'x y', d: [] }, e: ['p', { q: true }], f: 'true' })).toBe('a: 1\nb:\n  c: "x y"\n  d: []\ne:\n  - p\n  -\n    q: true\nf: "true"')
    expect(toYaml({})).toBe('')
  })
})
