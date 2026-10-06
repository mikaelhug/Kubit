import { Fragment } from 'preact'
import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { isTyping, useEscape } from '../keys'
import { readText, writeText } from '../local'
import { kindLabel } from '../machine'
import { sectionList } from '../routes'
import { api } from '../api'
import { checkNow, clusters, machineList, toast } from '../store'
import { Pill } from './ui'

interface Item { label: string; hint?: string; href?: string; run?: () => Promise<unknown>; done?: string; group: string }

function paletteItems(): Item[] {
  const out: Item[] = []
  for (const c of clusters.value) {
    for (const [id, label] of sectionList) out.push({ label: `${c.name} › ${label}`, href: `/clusters/${c.name}/${id}`, group: 'Clusters' })
    out.push(
      { label: `Plan ${c.name} again`, run: () => api.replan(c.name), done: `Planning ${c.name}`, group: 'Actions' },
      { label: `Check ${c.name} now`, run: () => checkNow(c.name), done: `${c.name} checked`, group: 'Actions' },
    )
    for (const n of c.spec.spec.nodes) out.push({ label: n.hostname, hint: `${c.name} · ${n.ip} · ${n.role ?? 'worker'}`, href: n.mac ? `/machines/${n.mac}` : `/nodes/${n.ip}`, group: 'Nodes' })
  }
  for (const m of machineList.value) {
    if (m.kind !== 'member') out.push({ label: m.hostname || m.mac, hint: `${kindLabel[m.kind]} · ${m.ip || m.mac}`, href: `/machines/${m.mac}`, group: 'Machines' })
  }
  out.push({ label: 'Scan the network', run: () => api.discover([]), done: 'Scan finished', group: 'Actions' })
  out.push({ label: 'Home', href: '/', group: 'Kubit' }, { label: 'Discovery', href: '/discovery', group: 'Kubit' }, { label: 'Secrets', href: '/secrets', group: 'Kubit' })
  return out
}

export function Palette() {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setOpen((o) => !o) }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  return open ? <PaletteDialog onClose={() => setOpen(false)} /> : null
}

function PaletteDialog({ onClose }: { onClose: () => void }) {
  const [q, setQ] = useState('')
  const [cursor, setCursor] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const { route } = useLocation()
  useEscape(onClose)
  useEffect(() => { input.current?.focus() }, [])
  const items = useMemo(paletteItems, [clusters.value, machineList.value])
  const matches = useMemo(() => {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean)
    return items.filter((it) => words.every((w) => (it.label + ' ' + (it.hint ?? '')).toLowerCase().includes(w))).slice(0, 12)
  }, [items, q])
  const go = (it: Item) => {
    onClose()
    if (it.run) it.run().then(() => it.done && toast(it.done, 'good')).catch((e) => toast(e.message, 'error'))
    else if (it.href) route(it.href)
  }
  return (
    <div class="fixed inset-0 z-50 flex items-start justify-center bg-black/50 p-6" onClick={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div class="panel w-full max-w-lg mt-16 overflow-hidden" role="dialog" aria-label="Jump to">
        <input ref={input} class="w-full bg-transparent px-4 py-3 text-[15px] outline-none border-b border-border" placeholder="Jump to a page or run an action" value={q}
          onInput={(e) => { setQ((e.target as HTMLInputElement).value); setCursor(0) }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') { e.preventDefault(); setCursor((c) => Math.min(c + 1, matches.length - 1)) }
            if (e.key === 'ArrowUp') { e.preventDefault(); setCursor((c) => Math.max(c - 1, 0)) }
            if (e.key === 'Enter' && matches[cursor]) go(matches[cursor])
          }} />
        <ul class="max-h-[50vh] overflow-auto py-1">
          {matches.length === 0 && <li class="px-4 py-3 text-muted text-[13px]">No match.</li>}
          {matches.map((it, i) => (
            <li key={`${it.group}:${it.href ?? ''}:${it.label}`}>
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

const shortcuts: [string, string][] = [['⌘K / Ctrl+K', 'Jump to a cluster, node or page'], ['/', 'Focus the table filter'], ['?', 'This sheet'], ['Esc', 'Close dialogs']]

export function Shortcuts() {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = isTyping(e.target)
      if (e.key === '?' && !typing) setOpen((o) => !o)
      if (e.key === '/' && !typing) { const f = document.querySelector<HTMLInputElement>('input[data-table-filter]'); if (f) { e.preventDefault(); f.focus() } }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  return open ? <ShortcutSheet onClose={() => setOpen(false)} /> : null
}

function ShortcutSheet({ onClose }: { onClose: () => void }) {
  useEscape(onClose)
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-6" onClick={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div class="panel w-full max-w-sm p-5">
        <h2 class="font-semibold mb-3">Keyboard shortcuts</h2>
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-[13px]">
          {shortcuts.map(([k, v]) => <Fragment key={k}><dt><kbd class="mono rounded border border-border bg-panel-2 px-1.5 py-0.5 text-[11px]">{k}</kbd></dt><dd class="text-muted">{v}</dd></Fragment>)}
        </dl>
      </div>
    </div>
  )
}

export function ThemeToggle() {
  const [theme, setTheme] = useState(() => readText('kubit.theme', 'system'))
  useEffect(() => {
    const root = document.documentElement
    if (theme === 'system') root.removeAttribute('data-theme'); else root.setAttribute('data-theme', theme)
    writeText('kubit.theme', theme === 'system' ? null : theme)
  }, [theme])
  const next = theme === 'system' ? 'light' : theme === 'light' ? 'dark' : 'system'
  const icon = theme === 'light' ? '☀' : theme === 'dark' ? '☾' : '◐'
  return <button class="hover:text-text" title={`Theme: ${theme} (click for ${next})`} onClick={() => setTheme(next)}>{icon}</button>
}
