import { describe, expect, it } from 'vitest'
import { csvLine } from './list'

describe('csvLine', () => {
  it('quotes fields that need it', () => {
    expect(csvLine(['plain', 'a,b', 'say "hi"', 'two\nlines', ''])).toBe('plain,"a,b","say ""hi""","two\nlines",')
  })
})
