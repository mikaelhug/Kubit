import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, type Settings } from '../../api'
import { can, loadSettings, settings, toast } from '../../store'
import { settingsPages, type SettingsPage } from '../../routes'
import { useDraft } from '../../useDraft'

const adminOnly: SettingsPage[] = ['accounts', 'sso']

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

export function useSettingsSlice<T>(key: string, pick: (s: Settings) => T, put: (s: Settings, v: T) => Settings) {
  const pushed = settings.value
  const d = useDraft(pushed ? pick(pushed) : null, `settings:${key}`)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { if (!pushed) loadSettings() }, [])
  const save = () => {
    if (!pushed || !d.draft) return Promise.resolve()
    return api.saveSettings(put(pushed, d.draft)).then((v) => { d.commit(pick(v)); setError(null); toast('Settings saved', 'good') }).catch((e) => setError(e.message))
  }
  return { draft: d.draft, setDraft: d.set, dirty: d.dirty, save, error, movedUnderneath: d.moved, discard: d.discard, pushed }
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
