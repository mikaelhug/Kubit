import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, type AppsRepo, type AppsRequest, type AppsReview } from '../api'
import { DirPicker } from './DirPicker'
import { Field, KeyValue, inputValue } from './ui'

export interface AppsChoice { dir: string; path: string }

export const noApps: AppsChoice = { dir: '', path: './' }

export const appsRequest = (c: AppsChoice): AppsRequest | undefined =>
  c.dir.trim() ? { dir: c.dir.trim(), path: c.path } : undefined

export const appsReady = (c: AppsChoice, repo: AppsRepo | null) => !c.dir.trim() || !!repo

export function useAppsRepo(dir: string) {
  const [repo, setRepo] = useState<AppsRepo | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    setRepo(null)
    setError(null)
    const d = dir.trim()
    if (!d) return
    let live = true
    const t = setTimeout(() => api.inspectApps(d).then((r) => { if (live) setRepo(r) }).catch((e) => { if (live) setError(e.message) }), 350)
    return () => { live = false; clearTimeout(t) }
  }, [dir])
  return { repo, error }
}

export function AppsRepoFields({ choice, onChange, repo, error, optional }: { choice: AppsChoice; onChange: (c: AppsChoice) => void; repo: AppsRepo | null; error: string | null; optional?: boolean }) {
  const set = (p: Partial<AppsChoice>) => onChange({ ...choice, ...p })
  return (
    <>
      <Field label="Checkout" hint={error ? <span class="text-bad">{error}</span> : optional && !choice.dir ? 'Optional' : undefined}>
        <div class="flex items-center gap-2">
          <input class="input mono" value={choice.dir} placeholder="~/git/apps" onInput={(e) => set({ dir: inputValue(e) })} />
          <DirPicker value={choice.dir} onPick={(dir) => set({ dir })} />
        </div>
        {repo && <span class="mono text-[12px] text-muted truncate" title={repo.remote}>{repo.url} · {repo.branch}</span>}
      </Field>
      {repo?.kind === 'other' && (
        <Field label="Path"><input class="input mono" value={choice.path} onInput={(e) => set({ path: inputValue(e).trim() })} /></Field>
      )}
    </>
  )
}

export function AppsReviewBlock({ review }: { review: AppsReview }) {
  const rows: [string, ComponentChildren][] = [
    ['Repository', <span class="mono break-all">{review.url} · {review.branch}</span>],
    ['Flux path', <span class="mono">{review.path}</span>],
  ]
  if (review.deployKey) rows.push(['Deploy key', 'new, read-only'])
  return (
    <div class="flex flex-col gap-2">
      <KeyValue rows={rows} />
      {review.files.length > 0 && (
        <div class="panel text-[12px]">
          <div class="px-3 py-1.5 border-b border-border mono text-muted">{review.repo.display}</div>
          <div class="px-3 py-1.5 flex flex-col gap-0.5 max-h-[220px] overflow-auto">
            {review.files.map((f) => (
              <span key={f.path} class="mono flex gap-2">
                <span class={`w-3 shrink-0 ${f.action === 'create' ? 'text-good' : 'text-warn'}`}>{f.action === 'create' ? '+' : '~'}</span>
                <span class="truncate">{f.path}</span>
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
