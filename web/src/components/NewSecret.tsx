import { useEffect, useRef, useState } from 'preact/hooks'
import { api } from '../api'
import { defaultFile, dockerConfig, fileProblem, keyProblems, nameProblem, namespaceProblem, newRow, type Row } from '../secrets'
import { toast } from '../store'
import { useNamespaces } from './NamespaceScope'
import { Dialog, ErrorBox, Field, inputValue } from './ui'

export interface SecretTarget { repo: number; name: string; root?: string }

type Template = 'opaque' | 'tls' | 'registry'

const templates: [Template, string][] = [['opaque', 'Opaque'], ['tls', 'TLS'], ['registry', 'Registry']]
const secretKind = { kind: 'Secret' }
const formId = 'new-secret'

const required = (v: string) => (v ? '' : 'Required')
const noSpaces = (v: string) => (/\s/.test(v) ? 'No spaces' : '')
const pemProblem = (v: string, block: RegExp, what: string) => (!v.trim() ? 'Required' : block.test(v) ? '' : `Not a PEM ${what}`)

export function NewSecretDialog({ cluster, targets, onClose, onCreated }: { cluster: string; targets: SecretTarget[]; onClose: () => void; onCreated: (repo: number, path: string) => void }) {
  const [target, setTarget] = useState(0)
  const [template, setTemplate] = useState<Template>('opaque')
  const [name, setName] = useState('')
  const [namespace, setNamespace] = useState('')
  const [path, setPath] = useState('')
  const [rows, setRows] = useState<Row[]>([newRow(secretKind)])
  const [tls, setTls] = useState({ crt: '', key: '' })
  const [reg, setReg] = useState({ server: '', username: '', password: '', email: '' })
  const [touched, setTouched] = useState<ReadonlySet<string>>(new Set())
  const [attempts, setAttempts] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const form = useRef<HTMLFormElement>(null)
  const namespaces = useNamespaces(cluster)

  useEffect(() => { if (attempts) form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus() }, [attempts])

  const t = targets[target]
  const file = path || defaultFile(t?.root, namespace, name)
  const filled = rows.filter((r) => r.key.trim() || r.value)
  const keyErrors = keyProblems(secretKind, filled)
  const noKeys = filled.length === 0

  const problems: Record<string, string> = {
    name: nameProblem(name),
    namespace: namespaceProblem(namespace),
    file: file || path ? fileProblem(file) : '',
    ...(template === 'tls' && {
      crt: pemProblem(tls.crt, /-----BEGIN CERTIFICATE-----/, 'certificate'),
      key: pemProblem(tls.key, /-----BEGIN [A-Z ]*PRIVATE KEY-----/, 'private key'),
    }),
    ...(template === 'registry' && {
      server: required(reg.server) || noSpaces(reg.server),
      username: required(reg.username) || noSpaces(reg.username),
      password: required(reg.password),
    }),
  }
  const typing: Record<string, string> = { name: nameProblem(name, true), namespace: namespaceProblem(namespace, true), server: noSpaces(reg.server), username: noSpaces(reg.username) }
  const shown = (id: string) => (attempts || touched.has(id) ? problems[id] : typing[id]) || undefined
  const blur = (id: string) => () => setTouched((s) => (s.has(id) ? s : new Set(s).add(id)))
  const keyError = (r: Row) => {
    const e = keyErrors.get(r.id)
    if (e === 'Required' || (!e && noKeys && r === rows[0])) return attempts ? 'Required' : undefined
    return e
  }
  const valid = !!t && Object.values(problems).every((p) => !p) && (template !== 'opaque' || (!noKeys && keyErrors.size === 0))

  const create = () => {
    setAttempts((n) => n + 1)
    if (!valid || busy) return
    const stringData: Record<string, string> =
      template === 'tls' ? { 'tls.crt': tls.crt, 'tls.key': tls.key }
      : template === 'registry' ? { '.dockerconfigjson': dockerConfig(reg.server, reg.username, reg.password, reg.email) }
      : Object.fromEntries(filled.map((r) => [r.key.trim(), r.value]))
    const type = template === 'tls' ? 'kubernetes.io/tls' : template === 'registry' ? 'kubernetes.io/dockerconfigjson' : 'Opaque'
    setBusy(true)
    setError(null)
    api.newSecret(t.repo, file, { name, namespace, type, stringData })
      .then((r) => { toast(r.kustomizations.length ? `Secret created and listed in ${r.kustomizations.join(', ')}` : 'Secret created', 'good'); onCreated(t.repo, file) })
      .catch((e) => setError(e.message))
      .finally(() => setBusy(false))
  }

  return (
    <Dialog title="New Secret" width="max-w-2xl" onClose={onClose}
      footer={<><button type="button" class="btn" onClick={onClose}>Cancel</button><button type="submit" form={formId} class="btn btn-primary" disabled={busy}>{busy ? 'Creating' : 'Create'}</button></>}>
      <form id={formId} ref={form} class="flex flex-col gap-4" noValidate onSubmit={(e) => { e.preventDefault(); create() }}>
        <ErrorBox error={error} />
        <div class="inline-flex rounded-[var(--r)] border border-border overflow-hidden w-fit" role="radiogroup" aria-label="Template">
          {templates.map(([id, label]) => (
            <button type="button" key={id} role="radio" aria-checked={template === id} class={`px-3 py-1.5 text-[13px] ${template === id ? 'bg-panel-2 text-text font-medium' : 'text-muted hover:text-text'}`} onClick={() => setTemplate(id)}>{label}</button>
          ))}
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Field label="Name" error={shown('name')}>
            <input class="input mono" value={name} autofocus spellcheck={false} autocomplete="off" aria-invalid={!!shown('name')} onInput={(e) => setName(inputValue(e))} onBlur={blur('name')} />
          </Field>
          <Field label="Namespace" error={shown('namespace')}>
            <input class="input mono" value={namespace} placeholder="default" list="new-secret-namespaces" spellcheck={false} autocomplete="off" aria-invalid={!!shown('namespace')} onInput={(e) => setNamespace(inputValue(e))} onBlur={blur('namespace')} />
            <datalist id="new-secret-namespaces">{(namespaces ?? []).map((n) => <option key={n.name} value={n.name} />)}</datalist>
          </Field>
        </div>
        {template === 'opaque' && <OpaqueRows rows={rows} setRows={setRows} errorOf={keyError} />}
        {template === 'tls' && (
          <>
            <PemField label="Certificate (tls.crt)" value={tls.crt} error={shown('crt')} onBlur={blur('crt')} onChange={(crt) => setTls({ ...tls, crt })} />
            <PemField label="Private key (tls.key)" value={tls.key} error={shown('key')} onBlur={blur('key')} onChange={(key) => setTls({ ...tls, key })} />
          </>
        )}
        {template === 'registry' && (
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <Field label="Server" class="sm:col-span-2" error={shown('server')}>
              <input class="input mono" value={reg.server} spellcheck={false} autocomplete="off" aria-invalid={!!shown('server')} onInput={(e) => setReg({ ...reg, server: inputValue(e) })} onBlur={blur('server')} />
            </Field>
            <Field label="Username" error={shown('username')}>
              <input class="input mono" value={reg.username} spellcheck={false} autocomplete="off" aria-invalid={!!shown('username')} onInput={(e) => setReg({ ...reg, username: inputValue(e) })} onBlur={blur('username')} />
            </Field>
            <Field label="Password or token" error={shown('password')}>
              <input class="input mono" type="password" value={reg.password} autocomplete="new-password" aria-invalid={!!shown('password')} onInput={(e) => setReg({ ...reg, password: inputValue(e) })} onBlur={blur('password')} />
            </Field>
            <Field label="Email">
              <input class="input mono" value={reg.email} placeholder="optional" spellcheck={false} autocomplete="off" onInput={(e) => setReg({ ...reg, email: inputValue(e).trim() })} />
            </Field>
          </div>
        )}
        <div class={`grid grid-cols-1 gap-3 ${targets.length > 1 ? 'sm:grid-cols-[auto_1fr]' : ''}`}>
          {targets.length > 1 && (
            <Field label="Repo">
              <select class="input" value={target} onChange={(e) => { setTarget(Number((e.target as HTMLSelectElement).value)); setPath('') }}>
                {targets.map((x, i) => <option key={x.repo} value={i}>{x.name}</option>)}
              </select>
            </Field>
          )}
          <Field label="File" error={shown('file')}>
            <input class="input mono" value={file} spellcheck={false} autocomplete="off" aria-invalid={!!shown('file')} onInput={(e) => setPath(inputValue(e).trim())} onBlur={blur('file')} />
          </Field>
        </div>
      </form>
    </Dialog>
  )
}

function OpaqueRows({ rows, setRows, errorOf }: { rows: Row[]; setRows: (r: Row[]) => void; errorOf: (r: Row) => string | undefined }) {
  const update = (id: string, patch: Partial<Row>) => setRows(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)))
  return (
    <div class="flex flex-col gap-2">
      <span class="label">Keys</span>
      {rows.map((r) => {
        const error = errorOf(r)
        return (
          <div key={r.id} class="grid grid-cols-[12rem_1fr_auto] gap-2 items-start">
            <div class="flex flex-col gap-1">
              <input class="input mono" value={r.key} placeholder="Key" aria-label="Key" spellcheck={false} autocomplete="off" aria-invalid={!!error} onInput={(e) => update(r.id, { key: inputValue(e) })} />
              {error && <span class="text-[11px] text-bad">{error}</span>}
            </div>
            <textarea class="input mono !text-[12px] resize-y" rows={1} value={r.value} placeholder="Value" spellcheck={false} autocomplete="off" aria-label="Value" onInput={(e) => update(r.id, { value: (e.target as HTMLTextAreaElement).value })} />
            <button type="button" class="btn btn-sm" aria-label="Remove key" disabled={rows.length === 1} onClick={() => setRows(rows.filter((x) => x.id !== r.id))}>✕</button>
          </div>
        )
      })}
      <button type="button" class="btn btn-sm w-fit" onClick={() => setRows([...rows, newRow(secretKind)])}>Add key</button>
    </div>
  )
}

function PemField({ label, value, error, onChange, onBlur }: { label: string; value: string; error?: string; onChange: (v: string) => void; onBlur: () => void }) {
  const picker = useRef<HTMLInputElement>(null)
  const load = (e: Event) => {
    const f = (e.target as HTMLInputElement).files?.[0]
    if (f) f.text().then(onChange).catch((err) => toast(err.message, 'error'))
  }
  return (
    <div class="flex flex-col gap-1">
      <span class="flex items-center gap-2">
        <span class="label">{label}</span>
        <button type="button" class="ml-auto text-[12px] text-accent hover:underline" onClick={() => picker.current?.click()}>Load from file</button>
        <input ref={picker} type="file" class="hidden" onChange={load} />
      </span>
      <textarea class="input mono !text-[11px] h-28 resize-y" aria-label={label} aria-invalid={!!error} value={value} spellcheck={false} autocomplete="off" placeholder="-----BEGIN" onInput={(e) => onChange((e.target as HTMLTextAreaElement).value)} onBlur={onBlur} />
      {error && <span class="text-[12px] text-bad">{error}</span>}
    </div>
  )
}
