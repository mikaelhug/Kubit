import { useCallback, useMemo } from 'preact/hooks'
import { api, type Namespace } from '../api'
import { useQueryParams } from '../query'
import { useLive } from '../useLive'

type Scope = 'apps' | 'platform' | 'all'
const scopes: { id: Scope; label: string }[] = [{ id: 'apps', label: 'Apps' }, { id: 'platform', label: 'Platform' }, { id: 'all', label: 'All' }]
const none: Namespace[] = []

export function useNamespaces(cluster: string) {
  const { data, loading } = useLive(() => api.namespaces(cluster), [cluster], [[cluster, 'workloads']], { onError: 'silent' })
  return data ?? (loading ? null : none)
}

export function useNamespaceScope(cluster: string) {
  const [query, setQuery] = useQueryParams()
  const namespaces = useNamespaces(cluster)
  const platform = useMemo(() => new Set((namespaces ?? []).filter((n) => n.platform).map((n) => n.name)), [namespaces])
  const ns = query.ns ?? ''
  const scope: Scope = scopes.some((s) => s.id === query.scope) ? (query.scope as Scope) : ns && platform.has(ns) ? 'platform' : 'apps'
  const { inScope, keep } = useMemo(() => {
    const inScope = (n: string, s: Scope = scope) => s === 'all' || (s === 'platform') === platform.has(n)
    return { inScope, keep: (n: string) => (ns ? n === ns : inScope(n)) }
  }, [platform, scope, ns])
  const set = useCallback((next: { scope?: Scope; ns?: string }) => setQuery(next.scope !== undefined ? { scope: next.scope, ns: undefined } : { ns: next.ns }), [setQuery])
  return { scope, ns, keep, inScope, set, namespaces, loading: namespaces === null }
}

export function NamespaceScope({ s, rows }: { s: ReturnType<typeof useNamespaceScope>; rows: string[] }) {
  const names = [...new Set([...(s.namespaces ?? []).map((n) => n.name), ...rows])].filter((n) => s.inScope(n)).sort()
  return (
    <span class="flex items-center gap-1">
      {scopes.map((o) => (
        <button key={o.id} class={`btn btn-sm ${s.scope === o.id ? 'border-accent text-accent' : ''}`} onClick={() => s.set({ scope: o.id })}>
          {o.label} <span class="text-muted">{s.loading ? '' : rows.filter((n) => s.inScope(n, o.id)).length}</span>
        </button>
      ))}
      <select class="input !w-48 ml-1" value={s.ns} aria-label="Namespace" onChange={(e) => s.set({ ns: (e.target as HTMLSelectElement).value })}>
        <option value="">{s.scope === 'apps' ? 'All app namespaces' : s.scope === 'platform' ? 'All platform namespaces' : 'All namespaces'}</option>
        {names.map((n) => <option key={n} value={n}>{n}</option>)}
      </select>
    </span>
  )
}
