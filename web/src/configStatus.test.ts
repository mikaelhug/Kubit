import { describe, expect, it } from 'vitest'
import { behindText } from './configStatus'

describe('config status copy', () => {
  it('counts the nodes behind', () => {
    expect(behindText(1)).toBe('1 node is behind the declaration.')
    expect(behindText(3)).toBe('3 nodes are behind the declaration.')
  })
})
