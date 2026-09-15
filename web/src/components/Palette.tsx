import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { clusters } from '../store'
import { sectionList } from '../app'
import { Pill } from './ui'

interface Item { label: string; hint?: string; href: string; group: string }

/** ⌘K / Ctrl+K: jump to any cluster page, node, or Kubit page by typing. */
export function Palette() {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const [cursor, setCursor] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const { route } = useLocation()

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setOpen((o) => !o); setQ(''); setCursor(0) }
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  useEffect(() => { if (open) setTimeout(() => input.current?.focus(), 0) }, [open])

  const items = useMemo<Item[]>(() => {
    const out: Item[] = []
    for (const c of clusters.value) {
      for (const [id, label] of sectionList) out.push({ label: `${c.name} › ${label}`, href: `/clusters/${c.name}/${id}`, group: 'Clusters' })
      for (const n of c.spec.spec.nodes) out.push({ label: n.hostname, hint: `${c.name} · ${n.ip} · ${n.pool ?? n.role}`, href: n.mac ? `/machines/${n.mac}` : `/nodes/${n.ip}`, group: 'Nodes' })
    }
    out.push({ label: 'New cluster', href: '/clusters/new', group: 'Kubit' }, { label: 'Inventory', href: '/fleet/inventory', group: 'Kubit' }, { label: 'Network boot (PXE)', href: '/fleet/pxe', group: 'Kubit' }, { label: 'Activity', href: '/operations', group: 'Kubit' }, { label: 'Kubit settings', href: '/settings', group: 'Kubit' }, { label: 'Getting started', href: '/start', group: 'Kubit' })
    return out
  }, [clusters.value])

  const matches = useMemo(() => {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean)
    return items.filter((it) => words.every((w) => (it.label + ' ' + (it.hint ?? '')).toLowerCase().includes(w))).slice(0, 12)
  }, [items, q])

  if (!open) return null
  const go = (it: Item) => { setOpen(false); route(it.href) }
  return (
    <div class="fixed inset-0 z-50 flex items-start justify-center bg-black/50 p-6" onClick={(e) => { if (e.target === e.currentTarget) setOpen(false) }}>
      <div class="panel w-full max-w-lg mt-16 overflow-hidden" role="dialog" aria-label="Jump to">
        <input ref={input} class="w-full bg-transparent px-4 py-3 text-[15px] outline-none border-b border-border" placeholder="Jump to a cluster, node or page…" value={q}
          onInput={(e) => { setQ((e.target as HTMLInputElement).value); setCursor(0) }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') { e.preventDefault(); setCursor((c) => Math.min(c + 1, matches.length - 1)) }
            if (e.key === 'ArrowUp') { e.preventDefault(); setCursor((c) => Math.max(c - 1, 0)) }
            if (e.key === 'Enter' && matches[cursor]) go(matches[cursor])
          }} />
        <ul class="max-h-[50vh] overflow-auto py-1">
          {matches.length === 0 && <li class="px-4 py-3 text-muted text-[13px]">No match.</li>}
          {matches.map((it, i) => (
            <li key={it.href + it.label}>
              <button class={`w-full text-left px-4 py-2 flex items-center gap-3 text-[13.5px] ${i === cursor ? 'bg-panel-2' : 'hover:bg-panel-2/60'}`} onMouseEnter={() => setCursor(i)} onClick={() => go(it)}>
                <span class="truncate">{it.label}</span>
                {it.hint && <span class="text-muted text-[12px] truncate">{it.hint}</span>}
                <span class="ml-auto"><Pill tone="muted">{it.group}</Pill></span>
              </button>
            </li>
          ))}
        </ul>
        <div class="px-4 py-2 border-t border-border text-[11px] text-muted flex gap-3"><span>↑↓ move</span><span>↵ open</span><span>esc close</span></div>
      </div>
    </div>
  )
}

/** '?' opens the shortcut sheet. */
export function Shortcuts() {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLSelectElement
      if (e.key === '?' && !typing) setOpen((o) => !o)
      if (e.key === '/' && !typing) { const f = document.querySelector<HTMLInputElement>('input[placeholder="Filter…"]'); if (f) { e.preventDefault(); f.focus() } }
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  if (!open) return null
  const rows: [string, string][] = [['⌘K / Ctrl+K', 'Jump to a cluster, node or page'], ['a', 'Toggle the Activity drawer'], ['/', 'Focus the table filter'], ['?', 'This sheet'], ['Esc', 'Close dialogs and drawers']]
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-6" onClick={(e) => { if (e.target === e.currentTarget) setOpen(false) }}>
      <div class="panel w-full max-w-sm p-5">
        <h2 class="font-semibold mb-3">Keyboard shortcuts</h2>
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-[13px]">
          {rows.map(([k, v]) => <><dt><kbd class="mono rounded border border-border bg-panel-2 px-1.5 py-0.5 text-[11px]">{k}</kbd></dt><dd class="text-muted">{v}</dd></>)}
        </dl>
      </div>
    </div>
  )
}

/** Theme follows the OS until the user picks one; the choice is remembered. */
export function ThemeToggle() {
  const [theme, setTheme] = useState<string>(() => { try { return localStorage.getItem('kubit.theme') ?? 'system' } catch { return 'system' } })
  useEffect(() => {
    const root = document.documentElement
    if (theme === 'system') root.removeAttribute('data-theme'); else root.setAttribute('data-theme', theme)
    try { theme === 'system' ? localStorage.removeItem('kubit.theme') : localStorage.setItem('kubit.theme', theme) } catch {}
  }, [theme])
  const next = theme === 'system' ? 'light' : theme === 'light' ? 'dark' : 'system'
  const icon = theme === 'light' ? '☀' : theme === 'dark' ? '☾' : '◐'
  return <button class="hover:text-text" title={`Theme: ${theme} (click for ${next})`} onClick={() => setTheme(next)}>{icon}</button>
}
