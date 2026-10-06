import { useEffect, useState } from 'preact/hooks'
import { api, type SecretFile } from '../api'
import { gitLabel, isSecret, keyProblems, newRow, patchOf, reviewEmpty, reviewOf, rowsOf, typeLabel, type Review, type Row } from '../secrets'
import { toast } from '../store'
import { FluxCell } from './SecretsTable'
import { Ago } from './Time'
import { ConfirmDialog, CopyButton, Dialog, ErrorBox, Field, Notice, Pill } from './ui'

const rowsFor = (v: string) => Math.min(10, Math.max(1, v.split('\n').reduce((n, line) => n + Math.max(1, Math.ceil(line.length / 70)), 0)))

const masked = <span class="text-muted tracking-widest">••••••••</span>

interface Loaded { hash: string; rows: Row[] }

export function SecretView({ file, labels, repoName, back, onMoved, onDeleted }: { file: SecretFile; labels: Record<string, string>; repoName: string; back: { href: string; label: string }; onMoved: (path: string) => void; onDeleted: () => void }) {
  const [loaded, setLoaded] = useState<Loaded | null>(null)
  const [shown, setShown] = useState(false)
  const [draft, setDraft] = useState<Row[] | null>(null)
  const [reviewing, setReviewing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [renaming, setRenaming] = useState(false)
  const [deleting, setDeleting] = useState(false)

  const load = () => api.secretValues(file.repo, file.path).then((r) => {
    const l = { hash: r.hash, rows: rowsOf(file, r.values) }
    setLoaded(l)
    return l
  }).catch((e) => { setError(e.message); return null })

  useEffect(() => {
    if (draft) return
    setLoaded(null)
    if (shown) load()
  }, [file.hash])

  const toggle = () => {
    if (shown) { setShown(false); return }
    setShown(true)
    if (!loaded) load()
  }
  const edit = () => {
    setError(null)
    ;(loaded ? Promise.resolve(loaded) : load()).then((l) => { if (l) setDraft(l.rows.map((r) => ({ ...r }))) })
  }
  const discard = () => { setDraft(null); setError(null); setLoaded(null); if (shown) load() }

  const listed = file.keys.filter((k) => !k.list && (!isSecret(file) || k.path[0] === 'data' || k.path[0] === 'stringData'))
  const lists = file.keys.filter((k) => k.list && (!isSecret(file) || k.path[0] === 'data' || k.path[0] === 'stringData'))
  const keyName = (path: string[]) => (isSecret(file) ? path.slice(1) : path).join('.')
  const recipients = file.recipients.map((r) => labels[r] ?? `${r.slice(0, 12)}…`)

  return (
    <div class="flex flex-col gap-4 min-w-0">
      <a href={back.href} class="text-[13px] text-muted hover:text-text w-fit">← {back.label}</a>
      <div class="panel p-4 flex flex-col gap-3">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold">{file.namespace && <span class="text-muted font-normal">{file.namespace} / </span>}{file.name || file.path}</h2>
          {isSecret(file) && <Pill tone="info">{typeLabel(file.type)}</Pill>}
          {file.cluster && <FluxCell f={file} long />}
          {gitLabel(file.git) && <Pill tone="info">{gitLabel(file.git)}</Pill>}
          {!draft && (
            <span class="ml-auto inline-flex flex-wrap gap-1">
              <button class="btn btn-sm" disabled={!!file.error} onClick={toggle}>{shown ? 'Hide values' : 'Show values'}</button>
              <button class="btn btn-sm btn-primary" disabled={!!file.error} onClick={edit}>Edit</button>
              <button class="btn btn-sm" onClick={() => setRenaming(true)}>Rename</button>
              <button class="btn btn-sm" onClick={() => setDeleting(true)}>Delete</button>
            </span>
          )}
        </div>
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-muted">
          <span class="mono">{repoName}/{file.path}</span>
          <span title={file.recipients.join('\n')}>Encrypted for {recipients.join(', ') || 'nobody'}</span>
          {file.modified && <span>Modified <Ago iso={file.modified} /></span>}
        </div>
        {file.cluster && file.skipped && <Notice tone="warn">Not applied by Flux: {file.skipped}.</Notice>}
        <ErrorBox error={file.error} />
        <ErrorBox error={error} />
        {error && draft && <button class="btn btn-sm w-fit" onClick={discard}>Discard edits and reload</button>}
        {lists.length > 0 && <Notice tone="muted">{lists.map((k) => keyName(k.path)).join(', ')} {lists.length === 1 ? 'is a list' : 'are lists'}; not editable here.</Notice>}
        {draft
          ? <EditRows file={file} draft={draft} setDraft={setDraft} onCancel={discard} onReview={() => setReviewing(true)} before={loaded?.rows ?? []} />
          : <ValueTable rows={shown ? loaded?.rows : undefined} keys={listed.map((k) => ({ key: keyName(k.path), encrypted: k.encrypted }))} />}
      </div>
      {reviewing && draft && loaded && (
        <ReviewDialog review={reviewOf(loaded.rows, draft)} onClose={() => setReviewing(false)} onSave={() => {
          const { set, remove } = patchOf(loaded.rows, draft)
          return api.patchSecret(file.repo, file.path, loaded.hash, set, remove).then(() => {
            setReviewing(false)
            setDraft(null)
            setError(null)
            toast('Saved', 'good')
          }).catch((e) => { setReviewing(false); setError(e.message) })
        }} />
      )}
      {renaming && <RenameDialog file={file} onClose={() => setRenaming(false)} onDone={(to) => { setRenaming(false); onMoved(to) }} />}
      {deleting && (
        <ConfirmDialog title={`Delete ${file.name || file.path}`} action="Delete" tone="danger" onClose={() => setDeleting(false)}
          impact={<p class="mono">{file.path}</p>}
          onConfirm={() => api.deleteSecretFile(file.repo, file.path, file.hash).then(() => { setDeleting(false); toast('Deleted', 'good'); onDeleted() }).catch((e) => toast(e.message, 'error'))} />
      )}
    </div>
  )
}

function ValueTable({ rows, keys }: { rows?: Row[]; keys: { key: string; encrypted: boolean }[] }) {
  const shown = rows !== undefined
  const list = shown ? rows.map((r) => ({ key: r.key, value: r.value, binary: r.binary })) : keys.map((k) => ({ key: k.key, value: undefined as string | undefined, binary: undefined as number | undefined }))
  if (list.length === 0) return <span class="text-[13px] text-muted">No keys yet.</span>
  return (
    <table class="w-full text-[13px]">
      <tbody>
        {list.map((r) => (
          <tr key={r.key} class="border-t border-border/60 align-top">
            <td class="py-2 pr-4 mono whitespace-nowrap">{r.key}</td>
            <td class="py-2 pr-4 w-full min-w-0">
              {r.value === undefined ? masked : r.binary !== undefined ? <span class="text-muted">Binary, {r.binary} bytes</span> : <pre class="mono whitespace-pre-wrap break-all m-0 max-h-60 overflow-auto">{r.value}</pre>}
            </td>
            <td class="py-2 text-right">{r.value !== undefined && <CopyButton text={r.value} className="btn btn-sm" />}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function EditRows({ file, draft, before, setDraft, onCancel, onReview }: { file: SecretFile; draft: Row[]; before: Row[]; setDraft: (rows: Row[]) => void; onCancel: () => void; onReview: () => void }) {
  const problems = keyProblems(file, draft)
  const unchanged = reviewEmpty(reviewOf(before, draft))
  const update = (id: string, patch: Partial<Row>) => setDraft(draft.map((r) => (r.id === id ? { ...r, ...patch } : r)))
  return (
    <div class="flex flex-col gap-2">
      {draft.length === 0 && <span class="text-[13px] text-muted">No keys.</span>}
      {draft.map((r) => (
        <div key={r.id} class="grid grid-cols-1 md:grid-cols-[14rem_1fr_auto] gap-2 items-start border-t border-border/60 pt-2">
          <div class="flex flex-col gap-1">
            <input class="input mono" value={r.key} placeholder="Key" aria-label="Key" onInput={(e) => update(r.id, { key: (e.target as HTMLInputElement).value })} />
            {problems.get(r.id) && <span class="text-[11px] text-bad">{problems.get(r.id)}</span>}
          </div>
          {r.binary !== undefined
            ? <span class="text-[13px] text-muted py-1.5">Binary, {r.binary} bytes; kept as is</span>
            : <textarea class="input mono !text-[12px] resize-y" rows={rowsFor(r.value)} value={r.value} spellcheck={false} autocomplete="off" aria-label={`Value of ${r.key}`}
                onInput={(e) => update(r.id, { value: (e.target as HTMLTextAreaElement).value })} />}
          <button class="btn btn-sm" aria-label={`Remove ${r.key}`} onClick={() => setDraft(draft.filter((x) => x.id !== r.id))}>✕</button>
        </div>
      ))}
      <div class="flex flex-wrap items-center gap-2 pt-2 border-t border-border/60">
        <button class="btn btn-sm" onClick={() => setDraft([...draft, newRow(file)])}>Add key</button>
        <span class="ml-auto inline-flex gap-2">
          <button class="btn btn-sm" onClick={onCancel}>Cancel</button>
          <button class="btn btn-sm btn-primary" disabled={problems.size > 0 || unchanged} onClick={onReview}>Review</button>
        </span>
      </div>
    </div>
  )
}

function ReviewDialog({ review, onClose, onSave }: { review: Review; onClose: () => void; onSave: () => Promise<unknown> }) {
  const [busy, setBusy] = useState(false)
  const groups: [string, string[]][] = [
    ['Added', review.added],
    ['Changed', review.changed],
    ['Renamed', review.renamed.map(([a, b]) => `${a} → ${b}`)],
    ['Removed', review.removed],
  ]
  return (
    <Dialog title="Review changes" onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Back</button>
        <button class="btn btn-primary" disabled={busy} onClick={() => { setBusy(true); onSave().finally(() => setBusy(false)) }}>{busy ? 'Saving' : 'Save'}</button>
      </>
    }>
      <div class="flex flex-col gap-2 text-[13px]">
        {groups.filter(([, keys]) => keys.length > 0).map(([label, keys]) => (
          <div key={label} class="flex gap-3">
            <span class="label w-20 shrink-0 pt-0.5">{label}</span>
            <span class="mono break-all">{keys.join(', ')}</span>
          </div>
        ))}
      </div>
    </Dialog>
  )
}

function RenameDialog({ file, onClose, onDone }: { file: SecretFile; onClose: () => void; onDone: (to: string) => void }) {
  const [to, setTo] = useState(file.path)
  const [error, setError] = useState<string | null>(null)
  const valid = /\.sops\.ya?ml$/.test(to) && to !== file.path
  const save = () => api.moveSecret(file.repo, file.path, to.trim(), file.hash).then(() => onDone(to.trim())).catch((e) => setError(e.message))
  return (
    <Dialog title="Rename file" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={!valid} onClick={save}>Rename</button></>}>
      <ErrorBox error={error} />
      <Field label="Path"><input class="input mono" value={to} autofocus onInput={(e) => setTo((e.target as HTMLInputElement).value)} /></Field>
    </Dialog>
  )
}
