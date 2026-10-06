import { useMemo } from 'preact/hooks'
import type { SecretFile } from '../api'
import { gitLabel, isSecret, typeLabel } from '../secrets'
import { DataTable, withoutColumn, type Column } from './DataTable'
import { Ago } from './Time'
import { Pill } from './ui'

const keyCount = (f: SecretFile) => f.keys.filter((k) => !isSecret(f) || k.path[0] === 'data' || k.path[0] === 'stringData').length

export function FluxCell({ f, long }: { f: SecretFile; long?: boolean }) {
  if (!f.cluster) return <span class="text-muted">—</span>
  if (f.skipped) return <Pill tone="warn" title={f.skipped}>{long ? 'Not applied by Flux' : 'not applied'}</Pill>
  return f.fluxReads ? <Pill tone="good">{long ? 'Flux can decrypt' : 'readable'}</Pill> : <Pill tone="bad">{long ? "Flux can't decrypt" : 'unreadable'}</Pill>
}

export function SecretsTable({ files, hrefOf, repoOf, showCluster, empty, loading, id }: { files: SecretFile[]; hrefOf: (f: SecretFile) => string; repoOf?: (f: SecretFile) => string; showCluster?: boolean; empty: string; loading?: boolean; id: string }) {
  const columns = useMemo<Column<SecretFile>[]>(() => withoutColumn([
    { id: 'name', header: 'Secret', sort: (f) => f.name || f.path, text: (f) => `${f.name ?? ''} ${f.path}`, cell: (f) => (
      <a href={hrefOf(f)} class="flex flex-col hover:underline">
        <span class="font-medium">{f.name || f.path}</span>
        <span class="text-[11px] text-muted mono">{f.path}</span>
      </a>
    ) },
    { id: 'repo', header: 'Repo', sort: (f) => repoOf?.(f) ?? '', cell: (f) => repoOf?.(f) },
    { id: 'cluster', header: 'Cluster', sort: (f) => f.cluster ?? '', cell: (f) => f.cluster ? <a href={`/clusters/${f.cluster}/secrets`} class="hover:underline">{f.cluster}</a> : <span class="text-muted">—</span> },
    { id: 'namespace', header: 'Namespace', sort: (f) => `${f.namespace ?? ''} ${f.name ?? f.path}`, mono: true, cell: (f) => f.namespace || <span class="text-muted">—</span> },
    { id: 'type', header: 'Type', sort: (f) => typeLabel(f.type), cell: (f) => f.error ? <Pill tone="bad" title={f.error}>unreadable</Pill> : isSecret(f) ? typeLabel(f.type) : <span class="text-muted">{f.kind || 'file'}</span> },
    { id: 'keys', header: 'Keys', align: 'right', sort: keyCount, cell: keyCount },
    { id: 'flux', header: 'Flux', sort: (f) => (f.cluster ? (f.skipped ? 1 : f.fluxReads ? 3 : 0) : 2), text: (f) => f.skipped ? 'not applied' : f.fluxReads ? 'readable' : f.cluster ? 'unreadable' : '', cell: (f) => <FluxCell f={f} /> },
    { id: 'git', header: 'Git', sort: (f) => gitLabel(f.git), cell: (f) => gitLabel(f.git) ? <Pill tone="info">{gitLabel(f.git)}</Pill> : <span class="text-muted">—</span> },
    { id: 'modified', header: 'Modified', align: 'right', sort: (f) => f.modified ?? '', cell: (f) => <span class="text-muted whitespace-nowrap"><Ago iso={f.modified} /></span> },
  ], 'repo', !repoOf), [hrefOf, repoOf])
  const shown = useMemo(() => withoutColumn(columns, 'cluster', !showCluster), [columns, showCluster])
  return <DataTable id={id} columns={shown} rows={files} loading={loading} rowKey={(f) => `${f.repo}/${f.path}`} empty={empty} defaultSort={{ id: 'namespace', dir: 'asc' }} />
}
