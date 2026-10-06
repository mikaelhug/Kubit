import { useRef, useState } from 'preact/hooks'
import { api } from '../api'
import { defaultFile, dockerConfig, keyProblems, nameProblem, namespaceProblem, newRow, type Row } from '../secrets'
import { toast } from '../store'
import { Dialog, ErrorBox, Field, inputValue } from './ui'

export interface SecretTarget { repo: number; name: string; root?: string }

type Template = 'opaque' | 'tls' | 'registry'

const templates: [Template, string][] = [['opaque', 'Opaque'], ['tls', 'TLS'], ['registry', 'Registry']]
const secretKind = { kind: 'Secret' }


export function NewSecretDialog({ targets, onClose, onCreated }: { targets: SecretTarget[]; onClose: () => void; onCreated: (repo: number, path: string) => void }) {
  const [target, setTarget] = useState(0)
  const [template, setTemplate] = useState<Template>('opaque')
  const [name, setName] = useState('')
  const [namespace, setNamespace] = useState('')
  const [path, setPath] = useState('')
  const [rows, setRows] = useState<Row[]>([newRow(secretKind)])
  const [tls, setTls] = useState({ crt: '', key: '' })
  const [reg, setReg] = useState({ server: '', username: '', password: '', email: '' })
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const t = targets[target]
  const file = path || defaultFile(t?.root, namespace, name)
  const filled = rows.filter((r) => r.key.trim() || r.value)
  const problems = keyProblems(secretKind, filled)
  const missing = template === 'tls' ? !tls.crt.trim() || !tls.key.trim() : template === 'registry' ? !reg.server || !reg.username || !reg.password : problems.size > 0
  const ok = !!t && !nameProblem(name) && !namespaceProblem(namespace) && /\.sops\.ya?ml$/.test(file) && !missing

  const create = () => {
    const stringData: Record<string, string> =
      template === 'tls' ? { 'tls.crt': tls.crt, 'tls.key': tls.key }
      : template === 'registry' ? { '.dockerconfigjson': dockerConfig(reg.server, reg.username, reg.password, reg.email) }
      : Object.fromEntries(filled.map((r) => [r.key.trim(), r.value]))
    const type = template === 'tls' ? 'kubernetes.io/tls' : template === 'registry' ? 'kubernetes.io/dockerconfigjson' : 'Opaque'
    setBusy(true)
    api.newSecret(t.repo, file, { name, namespace, type, stringData })
      .then((r) => { toast(r.kustomizations.length ? `Secret created and listed in ${r.kustomizations.join(', ')}` : 'Secret created', 'good'); onCreated(t.repo, file) })
      .catch((e) => setError(e.message))
      .finally(() => setBusy(false))
  }

  return (
    <Dialog title="New Secret" width="max-w-2xl" onClose={onClose}
      footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={!ok || busy} onClick={create}>{busy ? 'Creating' : 'Create'}</button></>}>
      <ErrorBox error={error} />
      <div class="inline-flex rounded-[var(--r)] border border-border overflow-hidden w-fit" role="radiogroup" aria-label="Template">
        {templates.map(([id, label]) => (
          <button key={id} role="radio" aria-checked={template === id} class={`px-3 py-1.5 text-[13px] ${template === id ? 'bg-panel-2 text-text font-medium' : 'text-muted hover:text-text'}`} onClick={() => setTemplate(id)}>{label}</button>
        ))}
      </div>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Field label="Name" hint={name && nameProblem(name)}><input class="input mono" value={name} autofocus onInput={(e) => setName(inputValue(e).trim())} /></Field>
        <Field label="Namespace" hint={namespaceProblem(namespace)}><input class="input mono" value={namespace} placeholder="default" onInput={(e) => setNamespace(inputValue(e).trim())} /></Field>
      </div>
      {template === 'opaque' && <OpaqueRows rows={rows} setRows={setRows} problems={problems} />}
      {template === 'tls' && (
        <>
          <PemField label="Certificate (tls.crt)" value={tls.crt} onChange={(crt) => setTls({ ...tls, crt })} />
          <PemField label="Private key (tls.key)" value={tls.key} onChange={(key) => setTls({ ...tls, key })} />
        </>
      )}
      {template === 'registry' && (
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Field label="Server"><input class="input mono" value={reg.server} placeholder="ghcr.io" onInput={(e) => setReg({ ...reg, server: inputValue(e).trim() })} /></Field>
          <Field label="Email"><input class="input mono" value={reg.email} placeholder="optional" onInput={(e) => setReg({ ...reg, email: inputValue(e).trim() })} /></Field>
          <Field label="Username"><input class="input mono" value={reg.username} autocomplete="off" onInput={(e) => setReg({ ...reg, username: inputValue(e).trim() })} /></Field>
          <Field label="Password or token"><input class="input mono" type="password" value={reg.password} autocomplete="new-password" onInput={(e) => setReg({ ...reg, password: inputValue(e) })} /></Field>
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
        <Field label="File"><input class="input mono" value={file} onInput={(e) => setPath(inputValue(e).trim())} /></Field>
      </div>
    </Dialog>
  )
}

function OpaqueRows({ rows, setRows, problems }: { rows: Row[]; setRows: (r: Row[]) => void; problems: Map<string, string> }) {
  const update = (id: string, patch: Partial<Row>) => setRows(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)))
  return (
    <div class="flex flex-col gap-2">
      <span class="label">Keys</span>
      {rows.map((r) => (
        <div key={r.id} class="grid grid-cols-[12rem_1fr_auto] gap-2 items-start">
          <div class="flex flex-col gap-1">
            <input class="input mono" value={r.key} placeholder="Key" aria-label="Key" onInput={(e) => update(r.id, { key: inputValue(e) })} />
            {(r.key || r.value) && problems.get(r.id) && <span class="text-[11px] text-bad">{problems.get(r.id)}</span>}
          </div>
          <textarea class="input mono !text-[12px] resize-y" rows={1} value={r.value} placeholder="Value" spellcheck={false} autocomplete="off" aria-label="Value" onInput={(e) => update(r.id, { value: (e.target as HTMLTextAreaElement).value })} />
          <button class="btn btn-sm" aria-label="Remove key" disabled={rows.length === 1} onClick={() => setRows(rows.filter((x) => x.id !== r.id))}>✕</button>
        </div>
      ))}
      <button class="btn btn-sm w-fit" onClick={() => setRows([...rows, newRow(secretKind)])}>Add key</button>
    </div>
  )
}

function PemField({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  const picker = useRef<HTMLInputElement>(null)
  const load = (e: Event) => {
    const f = (e.target as HTMLInputElement).files?.[0]
    if (f) f.text().then(onChange).catch((err) => toast(err.message, 'error'))
  }
  return (
    <div class="flex flex-col gap-1">
      <span class="flex items-center gap-2">
        <span class="label">{label}</span>
        <button class="ml-auto text-[12px] text-accent hover:underline" onClick={() => picker.current?.click()}>Load from file</button>
        <input ref={picker} type="file" class="hidden" onChange={load} />
      </span>
      <textarea class="input mono !text-[11px] h-28 resize-y" aria-label={label} value={value} spellcheck={false} autocomplete="off" placeholder="-----BEGIN" onInput={(e) => onChange((e.target as HTMLTextAreaElement).value)} />
    </div>
  )
}
