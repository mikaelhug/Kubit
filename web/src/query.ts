import { useCallback } from 'preact/hooks'
import { useLocation } from 'preact-iso'

export type QueryPatch = Record<string, string | undefined>

export function withQuery(search: string, patch: QueryPatch) {
  const q = new URLSearchParams(search)
  for (const [k, v] of Object.entries(patch)) if (v) q.set(k, v); else q.delete(k)
  return q.toString()
}

export function useQueryParams() {
  const { path, query, route } = useLocation()
  const set = useCallback((patch: QueryPatch) => {
    const s = withQuery(location.search, patch)
    route(s ? `${path}?${s}` : path, true)
  }, [path, route])
  return [query as QueryPatch, set] as const
}

export function useQueryParam(key: string, fallback = ''): [string, (v: string) => void] {
  const [query, set] = useQueryParams()
  return [query[key] ?? fallback, (v) => set({ [key]: v === fallback ? undefined : v })]
}
