import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type Namespace } from '../api'
import { refreshKey } from '../store'

export type Scope = 'apps' | 'platform' | 'all'
const scopes: { id: Scope; label: string }[] = [{ id: 'apps', label: 'Apps' }, { id: 'platform', label: 'Platform' }, { id: 'all', label: 'All' }]

export function useNamespaces(cluster: string) {
  const [namespaces, setNamespaces] = useState<Namespace[] | null>(null)
  useEffect(() => { api.namespaces(cluster).then(setNamespaces).catch(() => setNamespaces([])) }, [cluster, refreshKey(cluster, 'workloads')])
  return namespaces
}

export function useNamespaceScope(cluster: string) {
  const { path, query, route } = useLocation()
  const namespaces = useNamespaces(cluster)
  const platform = new Set((namespaces ?? []).filter((n) => n.platform).map((n) => n.name))
  const isPlatform = (ns: string) => platform.has(ns)
  const ns = query.ns ?? ''
  const scope: Scope = scopes.some((s) => s.id === query.scope) ? (query.scope as Scope) : ns && isPlatform(ns) ? 'platform' : 'apps'
  const inScope = (n: string, s: Scope = scope) => s === 'all' || (s === 'platform') === isPlatform(n)
  const keep = (n: string) => (ns ? n === ns : inScope(n))
  const set = (next: { scope?: Scope; ns?: string }) => {
    const q = new URLSearchParams(typeof location !== 'undefined' ? location.search : '')
    if (next.scope !== undefined) { q.set('scope', next.scope); q.delete('ns') }
    if (next.ns !== undefined) { if (next.ns) q.set('ns', next.ns); else q.delete('ns') }
    const s = q.toString()
    route(s ? `${path}?${s}` : path, true)
  }
  return { scope, ns, keep, inScope, set, namespaces, loading: namespaces === null }
}

export function NamespaceScope({ s, rows }: { s: ReturnType<typeof useNamespaceScope>; rows: string[] }) {
  const names = [...new Set([...(s.namespaces ?? []).map((n) => n.name), ...rows])].filter((n) => s.inScope(n)).sort()
  return (
    <span class="flex items-center gap-1">
      {scopes.map((o) => (
        <button key={o.id} class={`btn !py-1 ${s.scope === o.id ? 'border-accent text-accent' : ''}`} onClick={() => s.set({ scope: o.id })}>
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
