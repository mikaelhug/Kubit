import { useEffect, useRef, useState } from 'preact/hooks'

export interface DraftState<T> { draft: T | null; anchor: T | null }

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

export function draftFlags<T>(s: DraftState<T>, base: T | null) {
  const dirty = s.draft !== null && s.anchor !== null && !same(s.draft, s.anchor)
  return { dirty, moved: dirty && base !== null && !same(base, s.anchor) }
}

export const rebase = <T,>(s: DraftState<T>, base: T | null): DraftState<T> => (draftFlags(s, base).dirty ? s : { draft: base, anchor: base })

export function committed<T>(s: DraftState<T>, base: T | null, saved?: T): DraftState<T> {
  const d = saved ?? (base !== null && s.anchor !== null && !same(base, s.anchor) ? base : s.draft)
  return { draft: d, anchor: d }
}

const kept = new Map<string, DraftState<unknown>>()

export function useDraft<T>(base: T | null, keep?: string) {
  const init = () => ({ key: keep, ...rebase((keep ? (kept.get(keep) as DraftState<T> | undefined) : undefined) ?? { draft: base, anchor: base }, base) })
  const [state, setState] = useState(init)
  const latest = useRef(base)
  latest.current = base
  const s = state.key === keep ? state : init()
  if (s !== state) setState(s)
  const baseKey = JSON.stringify(base)
  useEffect(() => { setState((cur) => ({ ...rebase(cur, base), key: cur.key })) }, [baseKey])
  useEffect(() => {
    if (!keep || s.key !== keep) return
    if (draftFlags(s, null).dirty) kept.set(keep, { draft: s.draft, anchor: s.anchor }); else kept.delete(keep)
  }, [s, keep])
  const { dirty, moved } = draftFlags(s, base)
  return {
    draft: s.draft,
    dirty,
    moved,
    set: (draft: T) => setState((cur) => ({ ...cur, draft, anchor: cur.anchor ?? base })),
    discard: () => setState({ key: keep, draft: base, anchor: base }),
    commit: (saved?: T) => setState((cur) => ({ key: cur.key, ...committed(cur, latest.current, saved) })),
  }
}
