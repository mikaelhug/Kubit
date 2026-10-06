import { useState } from 'preact/hooks'
import { api, type SecretFile } from '../api'
import { NewSecretDialog } from '../components/NewSecret'
import { SecretsTable } from '../components/SecretsTable'
import { SecretView } from '../components/SecretView'
import { ErrorBox, Notice, Section } from '../components/ui'
import { useQueryParams } from '../query'
import { useLive } from '../useLive'

const fileHref = (f: SecretFile) => {
  const q = `repo=${f.repo}&file=${encodeURIComponent(f.path)}`
  return f.cluster ? `/clusters/${f.cluster}/secrets?${q}` : `/secrets?${q}`
}

export function Secrets() {
  const { data, error, loading } = useLive(() => api.secrets(), [], [['', 'secrets']])
  const [query, setQuery] = useQueryParams()
  const [creating, setCreating] = useState(false)
  const repos = data?.repos ?? []
  const files = repos.flatMap((r) => r.files)
  const repoName = (i: number) => repos.find((r) => r.index === i)?.name ?? ''

  if (query.file && data) {
    const file = files.find((f) => String(f.repo) === query.repo && f.path === query.file)
    return (
      <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
        {file
          ? <SecretView key={`${file.repo}/${file.path}`} file={file} labels={data.labels} repoName={repoName(file.repo)} back={{ href: '/secrets', label: 'All secrets' }}
              onMoved={(to) => setQuery({ file: to })} onDeleted={() => setQuery({ repo: undefined, file: undefined })} />
          : <Notice tone="muted"><span class="mono">{query.file}</span> is gone. <a href="/secrets" class="text-accent hover:underline">All secrets</a></Notice>}
      </div>
    )
  }

  return (
    <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
      <Section title="Secrets"
        actions={<button class="btn btn-primary btn-sm" disabled={repos.length === 0} onClick={() => setCreating(true)}>New Secret</button>}>
        <ErrorBox error={error} />
        {data && repos.length === 0 && <Notice tone="muted">No repo served. Start Kubit with one: <span class="mono">kubit &lt;dir&gt;</span></Notice>}
        {repos.filter((r) => r.error).map((r) => <ErrorBox key={r.index} error={`${r.name}: ${r.error}`} />)}
        <SecretsTable id="secrets" files={files} loading={loading && !data} hrefOf={fileHref} repoOf={(f) => repoName(f.repo)} showCluster empty="No SOPS files." />
      </Section>
      {creating && <NewSecretDialog targets={repos.map((r) => ({ repo: r.index, name: r.name }))} onClose={() => setCreating(false)}
        onCreated={(repo, path) => { setCreating(false); setQuery({ repo: String(repo), file: path }) }} />}
    </div>
  )
}
