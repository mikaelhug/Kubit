import type { ComponentType } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, fmt, kubeconfigUrl, type CertInfo } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { Tabs } from '../../components/Tabs'
import { CopyButton, ErrorBox, Notice, Pill, Section } from '../../components/ui'
import { useQueryParams } from '../../query'
import { toast } from '../../store'
import { useLive } from '../../useLive'
import { Repo } from './Repo'
import type { ClusterCtx } from './ClusterPage'

const certColumns: Column<CertInfo>[] = [
  { id: 'name', header: 'Certificate', sort: (c) => c.name, text: (c) => `${c.name} ${c.subject}`, cell: (c) => <span class="flex flex-col"><span class="font-medium">{c.name}</span><span class="text-[11px] text-muted mono truncate">{c.subject}</span></span> },
  { id: 'expires', header: 'Expires', sort: (c) => c.notAfter, cell: (c) => c.error ? <span class="text-bad">{c.error}</span> : fmt.date(c.notAfter) },
  { id: 'left', header: 'Left', align: 'right', sort: (c) => c.daysLeft, cell: (c) => <Pill tone={c.daysLeft < 14 ? 'bad' : c.daysLeft < 60 ? 'warn' : 'good'}>{c.daysLeft} days</Pill> },
]

const views = ['yaml', 'git', 'certs'] as const
type View = (typeof views)[number]

export function Repository({ ctx }: { ctx: ClusterCtx }) {
  const { name } = ctx
  const [query, setQuery] = useQueryParams()
  const { data: repo, error: repoError } = useLive(() => api.repo(name), [name], [[name, 'repo']], { onError: 'box' })
  const { data: certs } = useLive(() => api.certificates(name), [name], [[name, 'certificates']], { onError: 'null' })
  const uncommitted = repo?.git.changes.length ?? 0
  const expiring = certs?.filter((c) => c.daysLeft < 60).length ?? 0
  const view: View = views.includes(query.view as View) && (query.view !== 'certs' || !!certs?.length) ? query.view as View : 'yaml'
  return (
    <>
      <Tabs active={view} onSelect={(v) => setQuery({ view: v === 'yaml' ? undefined : v })}
        actions={<a class="btn btn-sm" href={kubeconfigUrl(name)} download="kubeconfig">kubeconfig</a>}
        tabs={[
          { id: 'yaml', label: 'cluster.yaml' },
          { id: 'git', label: 'Git', badge: uncommitted || undefined, tone: 'warn' },
          ...(certs?.length ? [{ id: 'certs', label: 'Certificates', badge: expiring || undefined, tone: 'warn' as const }] : []),
        ]} />
      {view === 'yaml' && <ClusterYAML name={name} />}
      {view === 'git' && <Repo repo={repo} error={repoError} />}
      {view === 'certs' && !!certs?.length && (
        <Section>
          <DataTable id="certs" search={false} columns={certColumns} rows={certs} rowKey={(c) => c.name} defaultSort={{ id: 'left', dir: 'asc' }} />
        </Section>
      )}
    </>
  )
}

type EditorProps = { value: string; onChange: (v: string) => void; onSave: () => void }

function ClusterYAML({ name }: { name: string }) {
  const { data: file, error: loadError } = useLive(() => api.clusterYaml(name), [name], [[name, 'repo'], [name, 'config']], { onError: 'box' })
  const [Editor, setEditor] = useState<ComponentType<EditorProps> | null>(null)
  const [known, setKnown] = useState<{ yaml: string; hash: string } | null>(null)
  const [draft, setDraft] = useState<{ text: string; base: string } | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { import('../../components/YamlEditor').then((m) => setEditor(() => m.default)) }, [])
  useEffect(() => { if (file) setKnown({ yaml: file.yaml, hash: file.hash }) }, [file?.hash])
  const dirty = !!draft && !!known && draft.text !== known.yaml
  const moved = dirty && draft!.base !== known!.hash
  const save = () => {
    if (!known || !draft || !dirty || saving) return
    const text = draft.text
    setSaving(true)
    setError(null)
    api.saveClusterYaml(name, text, draft.base)
      .then((r) => { setKnown({ yaml: text, hash: r.hash }); setDraft(null); toast('cluster.yaml saved; planning', 'good') })
      .catch((e) => setError(e.message))
      .finally(() => setSaving(false))
  }
  return (
    <Section
      actions={known && <span class="flex items-center gap-2">
        {dirty && <button class="btn btn-sm" onClick={() => { setDraft(null); setError(null) }}>Discard</button>}
        {!dirty && <CopyButton text={known.yaml} className="btn btn-sm" />}
        <button class="btn btn-primary btn-sm" disabled={!dirty || saving} onClick={save}>{saving ? 'Saving' : 'Save'}</button>
      </span>}>
      <ErrorBox error={loadError ?? error} />
      {moved && <Notice tone="warn">cluster.yaml changed on disk.</Notice>}
      {known && Editor && <Editor value={draft?.text ?? known.yaml} onChange={(text) => setDraft((d) => ({ text, base: d?.base ?? known.hash }))} onSave={save} />}
      {known && !Editor && <pre class="log !max-h-none">{known.yaml}</pre>}
    </Section>
  )
}
