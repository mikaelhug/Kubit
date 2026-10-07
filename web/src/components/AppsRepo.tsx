import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, type AppsRepo, type AppsRequest, type AppsReview } from '../api'
import { DirPicker } from './DirPicker'
import { Field, KeyValue, inputValue } from './ui'

export interface AppsChoice { dir: string; environment: string; environments: string[]; path: string }

export const noApps: AppsChoice = { dir: '', environment: '', environments: [], path: './' }

const presets = ['test', 'staging', 'production']
const validEnv = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

export const appsRequest = (c: AppsChoice): AppsRequest | undefined =>
  c.dir.trim() ? { dir: c.dir.trim(), environment: c.environment, environments: c.environments, path: c.path } : undefined

export function appsReady(c: AppsChoice, repo: AppsRepo | null) {
  if (!c.dir.trim()) return true
  if (!repo) return false
  if (repo.kind === 'other') return true
  return validEnv.test(c.environment) && c.environment !== 'base' && (repo.kind === 'layout' || c.environments.includes(c.environment))
}

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

function Segmented({ options, value, onSelect, label }: { options: string[]; value: string; onSelect: (v: string) => void; label: string }) {
  return (
    <div class="inline-flex flex-wrap rounded-[var(--r)] border border-border overflow-hidden w-fit" role="radiogroup" aria-label={label}>
      {options.map((o) => (
        <button key={o} type="button" role="radio" aria-checked={value === o} class={`px-3 py-1.5 text-[13px] mono ${value === o ? 'bg-panel-2 text-text font-medium' : 'text-muted hover:text-text'}`} onClick={() => onSelect(o)}>{o}</button>
      ))}
    </div>
  )
}

export function AppsRepoFields({ choice, onChange, repo, error, optional }: { choice: AppsChoice; onChange: (c: AppsChoice) => void; repo: AppsRepo | null; error: string | null; optional?: boolean }) {
  const [custom, setCustom] = useState('')
  const [adding, setAdding] = useState(false)
  const set = (p: Partial<AppsChoice>) => onChange({ ...choice, ...p })
  useEffect(() => {
    if (!repo) return
    if (repo.kind === 'empty' && choice.environments.length === 0) set({ environments: ['production'], environment: 'production' })
    if (repo.kind === 'layout' && !choice.environment) set({ environment: repo.environments.find((e) => !repo.clusters.some((c) => c.environment === e)) ?? repo.environments[0] ?? 'production' })
  }, [repo])
  const toggle = (env: string) => {
    const on = choice.environments.includes(env)
    const environments = on ? choice.environments.filter((e) => e !== env) : [...choice.environments, env]
    set({ environments, environment: on && choice.environment === env ? environments[0] ?? '' : choice.environment || env })
  }
  const addCustom = () => {
    const env = custom.trim()
    if (!validEnv.test(env) || env === 'base' || choice.environments.includes(env)) return
    set({ environments: [...choice.environments, env], environment: choice.environment || env })
    setCustom('')
  }
  const envOptions = [...presets, ...choice.environments.filter((e) => !presets.includes(e))]
  const chosen = envOptions.filter((e) => choice.environments.includes(e))
  const known = repo?.environments ?? []
  return (
    <>
      <Field label="Checkout" hint={error ? <span class="text-bad">{error}</span> : optional && !choice.dir ? 'Optional' : undefined}>
        <div class="flex items-center gap-2">
          <input class="input mono" value={choice.dir} placeholder="~/git/apps" onInput={(e) => set({ dir: inputValue(e) })} />
          <DirPicker value={choice.dir} onPick={(dir) => set({ dir })} />
        </div>
        {repo && <span class="mono text-[12px] text-muted truncate" title={repo.remote}>{repo.url} · {repo.branch}</span>}
      </Field>
      {repo?.kind === 'empty' && (
        <>
          <Field label="Environments">
            <div class="flex flex-wrap items-center gap-1.5">
              {envOptions.map((env) => (
                <button key={env} type="button" aria-pressed={choice.environments.includes(env)} class={`btn btn-sm mono ${choice.environments.includes(env) ? 'border-accent text-accent' : 'text-muted'}`} onClick={() => toggle(env)}>{env}</button>
              ))}
              <input class="input mono !w-28 !py-1 text-[12px]" value={custom} placeholder="other" aria-label="Other environment"
                onInput={(e) => setCustom(inputValue(e).toLowerCase())} onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addCustom() } }} onBlur={addCustom} />
            </div>
          </Field>
          {chosen.length > 0 && <Field label="This cluster"><Segmented label="This cluster" options={chosen} value={choice.environment} onSelect={(environment) => set({ environment })} /></Field>}
        </>
      )}
      {repo?.kind === 'layout' && (
        <Field label="Environment">
          <div class="flex flex-wrap items-center gap-2">
            <Segmented label="Environment" options={known} value={adding ? '' : choice.environment} onSelect={(environment) => { setAdding(false); set({ environment }) }} />
            {adding
              ? <input class="input mono !w-36 !py-1 text-[12px]" value={choice.environment} placeholder="name" autofocus aria-label="New environment" onInput={(e) => set({ environment: inputValue(e).trim().toLowerCase() })} />
              : <button type="button" class="btn btn-sm" onClick={() => { setAdding(true); set({ environment: '' }) }}>New environment</button>}
          </div>
        </Field>
      )}
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
