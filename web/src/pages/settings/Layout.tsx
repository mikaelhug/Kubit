import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, type Settings } from '../../api'
import { can, loadSettings, settings, toast } from '../../store'
import { settingsPages, type SettingsPage } from '../../app'
import { Notice } from '../../components/ui'

const adminOnly: SettingsPage[] = ['accounts', 'sso']

/** Settings frame: sub-nav on the left, one page on the right. */
export function SettingsLayout({ page, children }: { page: SettingsPage; children: ComponentChildren }) {
  const pages = settingsPages.filter(([id]) => !adminOnly.includes(id) || can('admin'))
  return (
    <div class="p-5 flex gap-5 max-w-5xl">
      <nav class="w-44 shrink-0 flex flex-col gap-0.5 pt-1">
        <span class="label px-2 pb-2">Kubit settings</span>
        {pages.map(([id, label]) => <a key={id} href={`/settings/${id}`} class={`pl-2.5 pr-2 py-1.5 text-[12.5px] border-l-2 hover:bg-panel-2 ${id === page ? 'bg-panel-2 border-accent font-medium' : 'border-transparent text-muted'}`}>{label}</a>)}
      </nav>
      <div class="flex-1 min-w-0 flex flex-col gap-6">{children}</div>
    </div>
  )
}

/**
 * A page's slice of the settings row. Edits stay local until Save, which merges the slice over
 * the latest pushed settings so a page never overwrites another page's fields.
 */
const kept = new Map<string, { draft: unknown; base: unknown }>()

export function useSettingsSlice<T>(key: string, pick: (s: Settings) => T, put: (s: Settings, v: T) => Settings) {
  const pushed = settings.value
  const [draft, setDraftRaw] = useState<T | null>(() => (kept.get(key)?.draft as T) ?? null)
  const [base, setBase] = useState<T | null>(() => (kept.get(key)?.base as T) ?? null)
  const [error, setError] = useState<string | null>(null)
  const setDraft = (v: T | null) => { setDraftRaw(v); if (v === null) kept.delete(key); else kept.set(key, { draft: v, base }) }
  useEffect(() => {
    if (!pushed) { loadSettings(); return }
    const v = pick(pushed)
    const dirtyNow = draft && base && JSON.stringify(draft) !== JSON.stringify(base)
    if (!dirtyNow) setDraftRaw(v)
    setBase(v)
    if (dirtyNow) kept.set(key, { draft, base: v }); else kept.delete(key)
  }, [pushed]) // eslint-disable-line
  const dirty = !!draft && !!base && JSON.stringify(draft) !== JSON.stringify(base)
  const movedUnderneath = dirty && !!pushed && JSON.stringify(pick(pushed)) !== JSON.stringify(base)
  const save = () => {
    if (!pushed || !draft) return Promise.resolve()
    return api.saveSettings(put(pushed, draft)).then((v) => { setDraftRaw(pick(v)); setBase(pick(v)); kept.delete(key); setError(null); toast('Settings saved', 'good') }).catch((e) => setError(e.message))
  }
  const discard = () => { if (pushed) { setDraftRaw(pick(pushed)); setBase(pick(pushed)); kept.delete(key) } }
  return { draft, setDraft, dirty, save, error, movedUnderneath, discard, pushed }
}

export function SaveBar({ dirty, onSave, children }: { dirty: boolean; onSave: () => void; children?: ComponentChildren }) {
  return (
    <div class="flex gap-2 items-center flex-wrap">
      <button class="btn btn-primary" disabled={!dirty} onClick={onSave}>Save</button>
      {children}
      {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
    </div>
  )
}

export function MovedNotice({ show, onDiscard }: { show: boolean; onDiscard: () => void }) {
  if (!show) return null
  return <Notice tone="warn">Settings were changed elsewhere while you were editing. Saving overwrites them; <button class="underline" onClick={onDiscard}>discard your edits</button> to see the current values.</Notice>
}
