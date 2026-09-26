import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, type NodeSpec } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { Tabs } from '../../components/Tabs'
import { Notice } from '../../components/ui'
import { WarningLine } from '../../components/WarningLine'
import { toast } from '../../store'
import type { Draft, PatchDraft, SetCluster } from './draft'

const blocking = new Set(['no-disk', 'no-schedulable-nodes', 'control-plane-undersized', 'worker-undersized'])

export function ReviewStep({ draft, setCluster, patch, onCreate, busy }: { draft: Draft; setCluster: SetCluster; patch: PatchDraft; onCreate: (yaml: string) => void; busy: boolean }) {
  const c = draft.cluster!
  const [tab, setTab] = useState<'summary' | 'yaml'>('summary')
  const [yaml, setYaml] = useState('')
  const [dirty, setDirty] = useState(false)
  const [lintErr, setLintErr] = useState<string | null>(null)
  const [linting, setLinting] = useState(false)
  const key = useMemo(() => JSON.stringify(c), [c])
  useEffect(() => {
    setLinting(true)
    api.lint(key).then((r) => { patch({ warnings: r.warnings ?? [] }); setYaml(r.yaml); setDirty(false); setLintErr(null) })
      .catch((e) => setLintErr(e.message)).finally(() => setLinting(false))
  }, [key])
  const applyYaml = () => api.validate(yaml).then((v) => { setCluster(() => v.cluster); toast('Declaration updated from YAML', 'good') }).catch((e) => setLintErr(e.message))
  const errors = draft.warnings.filter((w) => w.level !== 'info')
  const blockers = draft.warnings.filter((w) => blocking.has(w.code))
  const poolFor = (n: NodeSpec) => (c.spec.pools ?? []).find((x) => x.name === n.pool)
  const columns: Column<NodeSpec>[] = [
    { id: 'hostname', header: 'Hostname', mono: true, cell: (n) => n.hostname },
    { id: 'pool', header: 'Pool', cell: (n) => <><span class="mono">{n.pool}</span> <span class="text-muted text-[11px]">{n.role === 'controlplane' ? 'control plane' : 'worker'}</span></> },
    { id: 'addr', header: 'Address', mono: true, cell: (n) => n.network ? <>{n.network.addresses.join(', ')}{n.network.vlan ? ` vlan ${n.network.vlan}` : ''} <span class="text-muted text-[11px]">static</span></> : <>{n.ip} <span class="text-muted text-[11px]">dhcp</span></> },
    { id: 'install', header: 'Install disk', mono: true, cell: (n) => { const p = poolFor(n); return n.installDisk?.path ?? (p?.installDisk?.selector ? `selector ${Object.values(p.installDisk.selector).join(' ')}` : '—') } },
    { id: 'data', header: 'Data disks', mono: true, cell: (n) => <span class="text-[11px]">{n.dataDisks?.join(' ') ?? '—'}</span> },
    { id: 'labels', header: 'Labels', mono: true, cell: (n) => { const p = poolFor(n); return <span class="text-[11px]">{Object.entries({ ...(p?.labels ?? {}), ...(n.labels ?? {}) }).map(([k, v]) => `${k}=${v}`).join(' ') || '—'}</span> } },
    { id: 'taints', header: 'Taints', mono: true, cell: (n) => { const p = poolFor(n); return <span class="text-[11px]">{Object.entries({ ...(p?.taints ?? {}), ...(n.taints ?? {}) }).map(([k, v]) => `${k}=${v}`).join(' ') || (n.role === 'controlplane' && c.spec.controlPlane.allowScheduling === false ? 'control-plane:NoSchedule' : '—')}</span> } },
  ]
  return (
    <>
      <Tabs active={tab} onSelect={(t) => setTab(t as 'summary' | 'yaml')} tabs={[{ id: 'summary', label: 'Summary' }, { id: 'yaml', label: 'cluster.yaml' }]} />
      {tab === 'summary' && (
        <>
          <div class="flex flex-col gap-1">
            <span class="label">Checks {linting && <span class="text-muted">· linting</span>}</span>
            {lintErr && <Notice tone="bad">{lintErr}</Notice>}
            {!lintErr && draft.warnings.length === 0 && !linting && <Notice tone="good">No findings.</Notice>}
            {draft.warnings.map((w, i) => <WarningLine key={`${w.code}:${w.node ?? ''}:${i}`} w={w} />)}
          </div>
          <DataTable search={false} columns={columns} rows={c.spec.nodes} rowKey={(n) => n.hostname} />
        </>
      )}
      {tab === 'yaml' && (
        <>
          <textarea class="input mono !text-[12px] h-[460px]" value={yaml} spellcheck={false} onInput={(e) => { setYaml((e.target as HTMLTextAreaElement).value); setDirty(true) }} />
          <div class="flex gap-2 items-center">
            <button class="btn" disabled={!dirty} onClick={applyYaml}>Validate and use this YAML</button>
            {dirty && <span class="text-[12px] text-warn">edited; validate to update the summary</span>}
          </div>
        </>
      )}
      <div class="flex items-center gap-3 justify-end">
        {blockers.length > 0 ? <span class="text-[12px] text-bad">{blockers.length} issue{blockers.length === 1 ? '' : 's'} must be fixed first</span> : errors.length > 0 && <span class="text-[12px] text-warn">{errors.length} warning{errors.length === 1 ? '' : 's'}; creating is allowed</span>}
        <button class="btn btn-primary" disabled={busy || dirty || !yaml || !!lintErr || blockers.length > 0} title={blockers.length > 0 ? blockers[0].message : dirty ? 'Validate the edited YAML first' : ''} onClick={() => onCreate(yaml)}>{busy ? 'Starting' : `Create ${c.metadata.name}`}</button>
      </div>
    </>
  )
}
