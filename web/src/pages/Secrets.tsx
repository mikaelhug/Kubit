import { useEffect, useState } from 'preact/hooks'
import { api, type SecretFile, type SecretKey, type SecretRepo } from '../api'
import { ConfirmDialog, CopyButton, Dialog, ErrorBox, Field, Notice, Pill, Section } from '../components/ui'
import { useQueryParams } from '../query'
import { toast } from '../store'
import { useLive } from '../useLive'

const isSecret = (f: SecretFile) => f.kind === 'Secret'
const label = (f: SecretFile, k: SecretKey) => (isSecret(f) && (k.path[0] === 'stringData' || k.path[0] === 'data') ? k.path.slice(1) : k.path).join('.')

export function Secrets() {
  const { data: repos, error } = useLive(() => api.secrets(), [], [['', 'secrets']])
  const [query, setQuery] = useQueryParams()
  const [creating, setCreating] = useState<SecretRepo | null>(null)
  const repo = repos?.find((r) => String(r.index) === query.repo)
  const file = repo?.files.find((f) => f.path === query.file)
  return (
    <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
      <Section title="Secrets" help="SOPS files in the repos Kubit serves. Values are written encrypted; commit them yourself.">
        <ErrorBox error={error} />
        {repos && repos.length === 0 && <Notice tone="muted">No repo served. Start Kubit with one: <span class="mono">kubit lab</span></Notice>}
        {repos && repos.length > 0 && (
          <div class="grid grid-cols-1 lg:grid-cols-[18rem_1fr] gap-4 items-start">
            <div class="panel p-2 flex flex-col gap-3">
              {repos.map((r) => (
                <div key={r.index} class="flex flex-col gap-0.5">
                  <div class="flex items-center gap-2 px-2 pt-1">
                    <span class="label truncate" title={r.dir}>{r.name}</span>
                    {r.error && <Pill tone="bad" title={r.error}>error</Pill>}
                    <button class="ml-auto text-[12px] text-accent hover:underline" onClick={() => setCreating(r)}>+ New</button>
                  </div>
                  {r.files.length === 0 && <span class="px-2 text-[12px] text-muted">No SOPS files.</span>}
                  {r.files.map((f) => (
                    <button key={f.path} class={`text-left px-2 py-1 rounded-[var(--r-sm)] text-[13px] mono truncate ${repo?.index === r.index && file?.path === f.path ? 'bg-panel-2 text-text' : 'hover:bg-panel-2 text-muted'}`}
                      title={f.path} onClick={() => setQuery({ repo: String(r.index), file: f.path })}>{f.path}</button>
                  ))}
                </div>
              ))}
            </div>
            {repo && file ? <FileView key={`${repo.index}/${file.path}`} repo={repo} file={file} /> : <div class="panel p-4 text-[13px] text-muted">Pick a file.</div>}
          </div>
        )}
      </Section>
      {creating && <NewSecretDialog repo={creating} onClose={() => setCreating(null)} onDone={(path) => { setCreating(null); setQuery({ repo: String(creating.index), file: path }) }} />}
    </div>
  )
}

function FileView({ repo, file }: { repo: SecretRepo; file: SecretFile }) {
  const [shown, setShown] = useState<Record<string, string>>({})
  const [editing, setEditing] = useState<SecretKey | 'new' | null>(null)
  const [removing, setRemoving] = useState<SecretKey | null>(null)
  const keys = file.keys.filter((k) => !k.list && (!isSecret(file) || k.encrypted || k.path[0] === 'stringData' || k.path[0] === 'data'))
  useEffect(() => { setShown({}) }, [file.keys.map((k) => k.path.join('\u0000')).join('\u0001')])
  const reveal = (k: SecretKey) => {
    const id = k.path.join('\u0000')
    if (id in shown) { const { [id]: _, ...rest } = shown; setShown(rest); return }
    api.secretValue(repo.index, file.path, k.path).then((r) => setShown((s) => ({ ...s, [id]: r.value }))).catch((e) => toast(e.message, 'error'))
  }
  return (
    <div class="panel p-4 flex flex-col gap-3 min-w-0">
      <div class="flex flex-wrap items-center gap-2">
        <span class="mono font-medium break-all">{file.path}</span>
        {isSecret(file) && <Pill tone="info">Secret {file.namespace ? `${file.namespace}/` : ''}{file.name}</Pill>}
        <span class="text-[12px] text-muted" title={file.recipients.join('\n')}>{file.recipients.length} recipient{file.recipients.length === 1 ? '' : 's'}</span>
        <button class="btn btn-primary btn-sm ml-auto" disabled={!!file.error} onClick={() => setEditing('new')}>Add key</button>
      </div>
      <ErrorBox error={file.error} />
      {!file.error && keys.length === 0 && <span class="text-[13px] text-muted">No keys yet.</span>}
      {keys.length > 0 && (
        <table class="w-full text-[13px]">
          <tbody>
            {keys.map((k) => {
              const id = k.path.join('\u0000')
              const value = shown[id]
              return (
                <tr key={id} class="border-t border-border/60 align-top">
                  <td class="py-2 pr-4 mono whitespace-nowrap">{label(file, k)}</td>
                  <td class="py-2 pr-4 w-full min-w-0">
                    {!k.encrypted ? <span class="text-muted">plain</span> : value === undefined ? <span class="text-muted tracking-widest">••••••••</span> : <pre class="mono whitespace-pre-wrap break-all m-0">{value}</pre>}
                  </td>
                  <td class="py-2 whitespace-nowrap text-right">
                    <span class="inline-flex gap-1">
                      {k.encrypted && <button class="btn btn-sm" onClick={() => reveal(k)}>{value === undefined ? 'Show' : 'Hide'}</button>}
                      {value !== undefined && <CopyButton text={value} className="btn btn-sm" />}
                      <button class="btn btn-sm" onClick={() => setEditing(k)}>Edit</button>
                      <button class="btn btn-sm" aria-label="Delete key" onClick={() => setRemoving(k)}>✕</button>
                    </span>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {editing && <EditDialog repo={repo} file={file} k={editing === 'new' ? null : editing} onClose={() => setEditing(null)}
        onSaved={(path, v) => { setEditing(null); setShown((s) => ({ ...s, [path.join('\u0000')]: v })) }} />}
      {removing && (
        <ConfirmDialog title={`Delete ${label(file, removing)}`} action="Delete" tone="danger" onClose={() => setRemoving(null)}
          onConfirm={() => api.deleteSecret(repo.index, file.path, removing.path).then(() => setRemoving(null)).catch((e) => toast(e.message, 'error'))}
          impact={<p>Removes the key from <span class="mono">{file.path}</span>.</p>} />
      )}
    </div>
  )
}

function EditDialog({ repo, file, k, onClose, onSaved }: { repo: SecretRepo; file: SecretFile; k: SecretKey | null; onClose: () => void; onSaved: (path: string[], value: string) => void }) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    if (k?.encrypted) api.secretValue(repo.index, file.path, k.path).then((r) => setValue(r.value)).catch((e) => setError(e.message))
  }, [])
  const path = k ? k.path : isSecret(file) ? ['stringData', key.trim()] : key.trim().split('.')
  const ok = !!k || (key.trim() !== '' && path.every((p) => p !== ''))
  const save = () => api.setSecret(repo.index, file.path, path, value).then(() => onSaved(path, value)).catch((e) => setError(e.message))
  return (
    <Dialog title={k ? `Edit ${label(file, k)}` : 'Add key'} onClose={onClose}
      footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={!ok} onClick={save}>Save</button></>}>
      <ErrorBox error={error} />
      {!k && <Field label="Key" hint={isSecret(file) ? 'Under stringData' : 'Dots nest'}><input class="input mono" value={key} autofocus onInput={(e) => setKey((e.target as HTMLInputElement).value)} /></Field>}
      <Field label="Value"><textarea class="input mono !text-[12px] h-32" value={value} spellcheck={false} autocomplete="off" onInput={(e) => setValue((e.target as HTMLTextAreaElement).value)} /></Field>
    </Dialog>
  )
}

function NewSecretDialog({ repo, onClose, onDone }: { repo: SecretRepo; onClose: () => void; onDone: (path: string) => void }) {
  const [name, setName] = useState('')
  const [namespace, setNamespace] = useState('')
  const [path, setPath] = useState('')
  const [error, setError] = useState<string | null>(null)
  const file = path || (name ? `apps/${namespace || name}/${name}.sops.yaml` : '')
  const create = () => api.newSecret(repo.index, file, name, namespace).then(() => onDone(file)).catch((e) => setError(e.message))
  return (
    <Dialog title={`New Secret in ${repo.name}`} onClose={onClose}
      footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={!name || !file} onClick={create}>Create</button></>}>
      <ErrorBox error={error} />
      <Field label="Name"><input class="input mono" value={name} autofocus onInput={(e) => setName((e.target as HTMLInputElement).value.trim())} /></Field>
      <Field label="Namespace"><input class="input mono" value={namespace} onInput={(e) => setNamespace((e.target as HTMLInputElement).value.trim())} /></Field>
      <Field label="File" hint="Ends in .sops.yaml"><input class="input mono" value={file} onInput={(e) => setPath((e.target as HTMLInputElement).value.trim())} /></Field>
    </Dialog>
  )
}
