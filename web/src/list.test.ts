import { describe, expect, it } from 'vitest'
import { csvLine, joinList, kvLines, parseKV, syncKVText, syncListText } from './list'

describe('syncListText', () => {
  it('keeps what is being typed while it parses to the same list', () => {
    expect(syncListText('a,', ['a'])).toBe('a,')
    expect(syncListText('a, b ', ['a', 'b'])).toBe('a, b ')
    expect(syncListText('', [])).toBe('')
  })

  it('takes the prop when it changed elsewhere', () => {
    expect(syncListText('a,', ['a', 'c'])).toBe('a, c')
    expect(syncListText('a', undefined)).toBe('')
    expect(joinList(['x', 'y'])).toBe('x, y')
  })
})

describe('key=value text', () => {
  it('parses lines', () => {
    expect(parseKV('a=1\n\n b = 2 \nflag')).toEqual({ a: '1', b: '2', flag: '' })
    expect(parseKV(' \n')).toBeUndefined()
    expect(kvLines({ a: '1', flag: '' })).toBe('a=1\nflag')
  })

  it('resyncs only on an outside change', () => {
    expect(syncKVText('a=1\n', { a: '1' })).toBe('a=1\n')
    expect(syncKVText('a=1', { a: '2' })).toBe('a=2')
    expect(syncKVText('', undefined)).toBe('')
  })
})

describe('csvLine', () => {
  it('quotes fields that need it', () => {
    expect(csvLine(['plain', 'a,b', 'say "hi"', 'two\nlines', ''])).toBe('plain,"a,b","say ""hi""","two\nlines",')
  })
})
