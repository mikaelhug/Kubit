import { describe, expect, it } from 'vitest'
import { committed, draftFlags, rebase, type DraftState } from './useDraft'

describe('draft rebase', () => {
  it('follows pushes while clean', () => {
    expect(rebase({ draft: { a: 1 }, anchor: { a: 1 } }, { a: 2 })).toEqual({ draft: { a: 2 }, anchor: { a: 2 } })
    expect(rebase({ draft: null, anchor: null }, { a: 1 })).toEqual({ draft: { a: 1 }, anchor: { a: 1 } })
  })

  it('keeps edits and flags a move underneath', () => {
    const s = { draft: { a: 5 }, anchor: { a: 1 } }
    expect(rebase(s, { a: 2 })).toBe(s)
    expect(draftFlags(s, { a: 2 })).toEqual({ dirty: true, moved: true })
    expect(draftFlags(s, { a: 1 })).toEqual({ dirty: true, moved: false })
  })

  it('adopts a push that landed before the save returned', () => {
    const s: DraftState<Record<string, number>> = { draft: { a: 5 }, anchor: { a: 1 } }
    const pushed = { a: 5, b: 0 }
    const next = committed(s, pushed)
    expect(next).toEqual({ draft: pushed, anchor: pushed })
    expect(draftFlags({ ...next, draft: { a: 6, b: 0 } }, pushed)).toEqual({ dirty: true, moved: false })
  })

  it('commits the draft or the saved value otherwise', () => {
    expect(committed({ draft: { a: 5 }, anchor: { a: 1 } }, { a: 1 })).toEqual({ draft: { a: 5 }, anchor: { a: 5 } })
    expect(committed({ draft: { a: 5 }, anchor: { a: 1 } }, { a: 2 }, { a: 7 })).toEqual({ draft: { a: 7 }, anchor: { a: 7 } })
  })

  it('is clean when the edit returns to the anchor', () => {
    expect(draftFlags({ draft: { a: 1 }, anchor: { a: 1 } }, { a: 2 })).toEqual({ dirty: false, moved: false })
  })
})
