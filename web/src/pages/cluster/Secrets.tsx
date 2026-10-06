import { useState } from 'preact/hooks'
import { api, type ClusterSecrets } from '../../api'
import { FluxNotice } from '../../components/FluxNotice'
import { NewSecretDialog, type SecretTarget } from '../../components/NewSecret'
import { SecretsTable } from '../../components/SecretsTable'
import { SecretView } from '../../components/SecretView'
import { Code, ErrorBox, Notice, Section } from '../../components/ui'
import { useQueryParams } from '../../query'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

export function Secrets({ ctx }: { ctx: ClusterCtx }) {
  const { name } = ctx
  const { data, error, loading } = useLive(() => api.clusterSecrets(name), [name], [['', 'secrets']])
  const [query, setQuery] = useQueryParams()
  const [creating, setCreating] = useState(false)
  const base = `/clusters/${name}/secrets`
  const href = (repo: number, path: string) => `${base}?repo=${repo}&file=${encodeURIComponent(path)}`
  const sourceName = (repo: number) => data?.sources.find((s) => s.repo === repo)?.name ?? ''

  if (query.file && data) {
    const file = data.files.find((f) => String(f.repo) === query.repo && f.path === query.file)
    if (!file) return <Notice tone="muted"><span class="mono">{query.file}</span> is gone. <a href={base} class="text-accent hover:underline">All secrets</a></Notice>
    return <SecretView key={`${file.repo}/${file.path}`} file={file} labels={data.labels} repoName={sourceName(file.repo)} back={{ href: base, label: 'Secrets' }}
      onMoved={(to) => setQuery({ file: to })} onDeleted={() => setQuery({ repo: undefined, file: undefined })} />
  }

  const flux = data?.sources.filter((s) => s.flux) ?? []
  const targets: SecretTarget[] = flux.length > 0
    ? flux.map((s) => ({ repo: s.repo, name: s.name, root: data?.flux.path ?? '' }))
    : (data?.sources ?? []).filter((s) => s.cluster).map((s) => ({ repo: s.repo, name: s.name }))
  return (
    <Section title="Secrets"
      actions={<button class="btn btn-primary btn-sm" disabled={targets.length === 0} onClick={() => setCreating(true)}>New Secret</button>}>
      <ErrorBox error={error} />
      {data && <FluxSourceLine name={name} data={data} />}
      {data?.recipient && flux.map((s) => {
        const own = data.files.filter((f) => f.repo === s.repo && !f.error && !f.skipped)
        return <FluxNotice key={s.repo} cluster={name} repo={s.repo} repoName={s.name} reads={s.reads} missing={own.filter((f) => !f.fluxReads).length} />
      })}
      <SecretsTable id="cluster-secrets" files={data?.files ?? []} loading={loading && !data} hrefOf={(f) => href(f.repo, f.path)}
        repoOf={new Set(data?.files.map((f) => f.repo)).size > 1 ? (f) => sourceName(f.repo) : undefined} empty="No secrets yet." />
      {creating && <NewSecretDialog targets={targets} onClose={() => setCreating(false)} onCreated={(repo, path) => { setCreating(false); setQuery({ repo: String(repo), file: path }) }} />}
    </Section>
  )
}

function FluxSourceLine({ name, data }: { name: string; data: ClusterSecrets }) {
  const { flux, sources } = data
  if (!flux.enabled) return <Notice tone="muted">Flux is off.</Notice>
  if (!flux.url) return <Notice tone="muted">Flux has no repository.</Notice>
  const synced = sources.filter((s) => s.flux)
  if (synced.length === 0) {
    const own = sources.find((s) => s.cluster)
    return (
      <Notice tone="info">
        <div class="flex flex-col gap-2">
          <span>Serve a checkout of <span class="mono">{flux.url}</span>:</span>
          <Code text={`kubit ${own?.dir ?? name} <checkout>`} />
        </div>
      </Notice>
    )
  }
  return (
    <span class="text-[12px] text-muted">
      Flux applies <span class="mono">{synced.map((s) => s.name).join(', ')}/{flux.path || ''}</span> from <span class="mono">{flux.branch}</span>.
    </span>
  )
}
