import { useMemo, useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'
import { persist, read } from '../local'

export interface Column<T> {
  id: string
  header: ComponentChildren
  cell: (row: T) => ComponentChildren
  sort?: (row: T) => string | number | boolean
  text?: (row: T) => string
  align?: 'left' | 'right'
  width?: string
  mono?: boolean
  wrap?: boolean
}

const searchFrom = 10

export const withoutColumn = <T,>(cols: Column<T>[], id: string, hide: boolean) => (hide ? cols.filter((c) => c.id !== id) : cols)

interface Props<T> {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  empty?: ComponentChildren
  search?: boolean
  defaultSort?: { id: string; dir: 'asc' | 'desc' }
  id?: string
  loading?: boolean
  toolbar?: ComponentChildren
}

export function DataTable<T>({ columns, rows, rowKey, empty = 'Nothing to show.', search = true, defaultSort, id, loading, toolbar }: Props<T>) {
  const pref = id ? `kubit.table.${id}` : ''
  const [sort, setSort] = useState<{ id: string; dir: 'asc' | 'desc' } | undefined>(pref ? read(pref + '.sort', defaultSort) : defaultSort)
  const [q, setQ] = useState('')
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
  const current = Math.min(page, pages - 1)
  const visible = filtered.slice(current * pageSize, (current + 1) * pageSize)

  const toggleSort = (c: Column<T>) => {
    if (!c.sort) return
    const next = sort?.id === c.id && sort.dir === 'asc' ? { id: c.id, dir: 'desc' as const } : { id: c.id, dir: 'asc' as const }
    setSort(next)
    if (pref) persist(pref + '.sort', next)
  }

  if (rows.length === 0 && !loading) {
    return <div class="panel px-4 py-6 text-center text-[13px] text-muted">{empty}</div>
  }
  const searchable = search && (rows.length >= searchFrom || q !== '')
  return (
    <div class="panel flex flex-col overflow-hidden">
      {(searchable || toolbar) && (
        <div class="flex items-center gap-2 px-3 py-2 border-b border-border">
          {searchable && <input class="input !w-64" placeholder="Filter" data-table-filter value={q} onInput={(e) => { setQ((e.target as HTMLInputElement).value); setPage(0) }} aria-label="Filter rows" />}
          {searchable && <span class="text-[12px] text-muted">{filtered.length === rows.length ? `${rows.length} rows` : `${filtered.length} of ${rows.length}`}</span>}
          {toolbar && <span class="ml-auto flex items-center gap-2">{toolbar}</span>}
        </div>
      )}
      <div class="scroll-x">
        <table class="data">
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
              <tr key={rowKey(r)}>
                {columns.map((c) => <td key={c.id} class={`${c.align === 'right' ? 'text-right' : ''} ${c.mono ? 'mono' : ''} ${c.wrap ? '!whitespace-normal' : ''}`}>{c.cell(r)}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {pages > 1 && (
        <div class="flex items-center gap-2 px-3 py-2 border-t border-border text-[12px] text-muted">
          <button class="btn btn-sm" disabled={current === 0} onClick={() => setPage(current - 1)}>‹</button>
          <span>page {current + 1} / {pages}</span>
          <button class="btn btn-sm" disabled={current >= pages - 1} onClick={() => setPage(current + 1)}>›</button>
        </div>
      )}
    </div>
  )
}
