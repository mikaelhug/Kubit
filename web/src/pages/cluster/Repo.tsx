import type { RepoView } from '../../api'
import { CopyButton, ErrorBox, Notice, Pill, Section } from '../../components/ui'

export function Repo({ repo: r, error }: { repo: RepoView | null; error: string | null }) {
  const git = r?.git
  return (
    <Section
      actions={r && <CopyButton text={r.dir} className="btn btn-sm" label="Copy path" />}>
      <ErrorBox error={error} />
      {r && (
        <div class="panel p-3 flex flex-col gap-2 text-[13px]">
          <div class="flex flex-wrap items-center gap-2">
            <span class="mono break-all">{r.dir}</span>
            {git?.repo && git.branch && <Pill tone="muted">{git.branch}</Pill>}
            {git?.repo && (git.changes.length === 0 ? <Pill tone="good">committed</Pill> : <Pill tone="warn">{git.changes.length} uncommitted</Pill>)}
            {git?.repo && git.ahead > 0 && <Pill tone="warn">{git.ahead} not pushed</Pill>}
            {git?.repo && !git.upstream && <Pill tone="muted">no remote</Pill>}
          </div>
          {git && !git.repo && <Notice tone="warn">Not a git repository.</Notice>}
          {git && git.changes.length > 0 && (
            <ul class="mono text-[12px] flex flex-col">
              {git.changes.map((c) => <li key={c.path}><span class="inline-block w-6 text-muted">{c.status}</span>{c.path}</li>)}
            </ul>
          )}
        </div>
      )}
    </Section>
  )
}
