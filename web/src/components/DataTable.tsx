import { useMemo, useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'

export interface Column<T> {
  id: string
  header: string
  cell: (row: T) => ComponentChildren
  /** Sort key; omit for unsortable columns. */
  sort?: (row: T) => string | number | boolean
  /** Text used by the search box; defaults to the sort key. */
  text?: (row: T) => string
  align?: 'left' | 'right'
  width?: string
  mono?: boolean
}

interface Props<T> {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  empty?: ComponentChildren
  search?: boolean
  defaultSort?: { id: string; dir: 'asc' | 'desc' }
  onRowClick?: (row: T) => void
  rowClass?: (row: T) => string
  /** Persisted table id for sort/density preferences. */
  id?: string
  toolbar?: ComponentChildren
  /** Rows are still being fetched: show placeholders instead of the empty message. */
  loading?: boolean
}

function read<T>(key: string, fallback: T): T {
  try { const v = localStorage.getItem(key); return v ? JSON.parse(v) : fallback } catch { return fallback }
}

/** Sortable, searchable table with sticky header; rows beyond 300 are windowed by page. */
export function DataTable<T>({ columns, rows, rowKey, empty = 'Nothing to show.', search = true, defaultSort, onRowClick, rowClass, id, toolbar, loading }: Props<T>) {
  const pref = id ? `kubit.table.${id}` : ''
  const [sort, setSort] = useState<{ id: string; dir: 'asc' | 'desc' } | undefined>(pref ? read(pref + '.sort', defaultSort) : defaultSort)
  const [q, setQ] = useState('')
  const [dense, setDense] = useState<boolean>(pref ? read(pref + '.dense', false) : false)
  const [page, setPage] = useState(0)
  const pageSize = 300

  const filtered = useMemo(() => {
    let out = rows
    if (q.trim()) {
      const needle = q.trim().toLowerCase()
      out = rows.filter((r) => columns.some((c) => {
        const t = c.text ? c.text(r) : c.sort ? String(c.sort(r)) : ''
        return t.toLowerCase().includes(needle)
      }))
    }
    if (sort) {
      const col = columns.find((c) => c.id === sort.id)
      if (col?.sort) {
        const key = col.sort
        out = [...out].sort((a, b) => {
          const x = key(a), y = key(b)
          const cmp = typeof x === 'number' && typeof y === 'number' ? x - y : String(x).localeCompare(String(y), undefined, { numeric: true })
          return sort.dir === 'asc' ? cmp : -cmp
        })
      }
    }
    return out
  }, [rows, q, sort, columns])

  const pages = Math.max(1, Math.ceil(filtered.length / pageSize))
  const visible = filtered.slice(page * pageSize, (page + 1) * pageSize)

  const toggleSort = (c: Column<T>) => {
    if (!c.sort) return
    const next = sort?.id === c.id && sort.dir === 'asc' ? { id: c.id, dir: 'desc' as const } : { id: c.id, dir: 'asc' as const }
    setSort(next)
    if (pref) try { localStorage.setItem(pref + '.sort', JSON.stringify(next)) } catch {}
  }

  return (
    <div class="panel flex flex-col overflow-hidden">
      {(search || toolbar) && (
        <div class="flex items-center gap-2 px-3 py-2 border-b border-border">
          {search && <input class="input !w-64" placeholder="Filter…" value={q} onInput={(e) => { setQ((e.target as HTMLInputElement).value); setPage(0) }} aria-label="Filter rows" />}
          <span class="text-[12px] text-muted">{filtered.length === rows.length ? `${rows.length} rows` : `${filtered.length} of ${rows.length}`}</span>
          <div class="ml-auto flex items-center gap-2">
            {toolbar}
            <button class="btn !py-1 !px-2 text-[12px]" title="Toggle row density" onClick={() => { setDense(!dense); if (pref) try { localStorage.setItem(pref + '.dense', JSON.stringify(!dense)) } catch {} }}>{dense ? 'Comfortable' : 'Compact'}</button>
          </div>
        </div>
      )}
      <div class="overflow-x-auto">
        <table class={`data ${dense ? 'dense' : ''}`}>
          <thead>
            <tr>
              {columns.map((c) => (
                <th key={c.id} style={c.width ? { width: c.width } : undefined} class={`${c.align === 'right' ? 'text-right' : ''} ${c.sort ? 'cursor-pointer select-none hover:text-text' : ''}`} onClick={() => toggleSort(c)} aria-sort={sort?.id === c.id ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}>
                  {c.header}{sort?.id === c.id && <span class="ml-1 text-accent">{sort.dir === 'asc' ? '▲' : '▼'}</span>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {visible.length === 0 && loading && [0, 1, 2].map((i) => <tr key={`sk${i}`} aria-hidden="true">{columns.map((c) => <td key={c.id}><span class="inline-block h-3 rounded bg-panel-2 animate-pulse" style={{ width: `${40 + ((i * 7 + c.id.length * 13) % 50)}%` }} /></td>)}</tr>)}
            {visible.length === 0 && !loading && <tr><td colSpan={columns.length} class="text-muted !py-6 text-center">{empty}</td></tr>}
            {visible.map((r) => (
              <tr key={rowKey(r)} class={`${onRowClick ? 'cursor-pointer hover:bg-panel-2' : ''} ${rowClass?.(r) ?? ''}`} onClick={() => onRowClick?.(r)}>
                {columns.map((c) => <td key={c.id} class={`${c.align === 'right' ? 'text-right num' : ''} ${c.mono ? 'mono' : ''}`}>{c.cell(r)}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {pages > 1 && (
        <div class="flex items-center gap-2 px-3 py-2 border-t border-border text-[12px] text-muted">
          <button class="btn !py-0.5 !px-2" disabled={page === 0} onClick={() => setPage(page - 1)}>‹</button>
          <span>page {page + 1} / {pages}</span>
          <button class="btn !py-0.5 !px-2" disabled={page >= pages - 1} onClick={() => setPage(page + 1)}>›</button>
        </div>
      )}
    </div>
  )
}
