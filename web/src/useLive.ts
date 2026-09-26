import { useEffect, useRef, useState } from 'preact/hooks'
import { refreshKey } from './store'

type ScopeRef = readonly [cluster: string, scope: string]

interface LiveOptions { onError?: 'box' | 'silent' | 'null'; enabled?: boolean }

export function useLive<T>(load: () => Promise<T>, deps: readonly unknown[], scopes: readonly ScopeRef[] = [], { onError = 'box', enabled = true }: LiveOptions = {}) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(enabled)
  const current = useRef({ load, onError, enabled, seq: 0 })
  Object.assign(current.current, { load, onError, enabled })
  const reload = useRef(() => {
    const c = current.current
    const id = ++c.seq
    if (!c.enabled) {
      setData(null)
      setError(null)
      setLoading(false)
      return Promise.resolve()
    }
    return c.load().then(
      (v) => { if (id !== c.seq) return; setData(() => v); setError(null); setLoading(false) },
      (e) => {
        if (id !== c.seq) return
        setLoading(false)
        if (c.onError === 'box') setError(e?.message ?? String(e))
        else if (c.onError === 'null') setData(null)
      },
    )
  }).current
  const keys = scopes.map(([cluster, scope]) => refreshKey(cluster, scope))
  useEffect(() => { reload() }, [...deps, enabled, ...keys])
  return { data, error, loading, reload, set: setData }
}
