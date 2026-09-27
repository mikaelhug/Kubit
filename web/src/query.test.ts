import { describe, expect, it } from 'vitest'
import { withQuery } from './query'

describe('withQuery', () => {
  it('sets, replaces and drops keys', () => {
    expect(withQuery('?view=pods&ns=a', { ns: 'b' })).toBe('view=pods&ns=b')
    expect(withQuery('view=pods&ns=a', { ns: undefined, view: '' })).toBe('')
    expect(withQuery('', { cluster: 'lab', view: 'audit' })).toBe('cluster=lab&view=audit')
  })
})
