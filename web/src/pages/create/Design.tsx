import { fmt, type Pool } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { PoolsEditor } from '../../components/PoolsEditor'
import { dataCandidates, diskLabel, installCandidates, modelOf, TypePill } from '../../machine'
import { machineOf, updateNode, type Draft, type SetCluster } from './draft'

export function DesignStep({ draft, setCluster, reset, busy }: { draft: Draft; setCluster: SetCluster; reset: () => Promise<void>; busy: boolean }) {
  const c = draft.cluster!
  const pools = c.spec.pools ?? []
  const poolOf = (name?: string) => pools.find((p) => p.name === name)
  const setNode = (i: number, patch: Parameters<typeof updateNode>[2]) => updateNode(setCluster, i, patch)
  const setPools = (ps: Pool[]) => setCluster((c) => {
    const nodes = c.spec.nodes.map((n) => { const p = ps.find((x) => x.name === n.pool); return p ? { ...n, role: p.role } : n })
    return { ...c, spec: { ...c.spec, pools: ps, nodes } }
  })
  const autoName = () => setCluster((c) => {
    const counters: Record<string, number> = {}
    return { ...c, spec: { ...c.spec, nodes: c.spec.nodes.map((n) => { const p = n.pool ?? 'node'; counters[p] = (counters[p] ?? 0) + 1; return { ...n, hostname: `${c.metadata.name}-${p === 'controlplane' ? 'cp' : p}-${String(counters[p]).padStart(2, '0')}` } }) } }
  })
  const cps = c.spec.nodes.filter((n) => n.role === 'controlplane').length
  const spare = c.spec.nodes.reduce((s, n) => s + dataCandidates(machineOf(draft, n), n.installDisk?.path).length, 0)
  const claimed = c.spec.nodes.reduce((s, n) => s + (n.dataDisks?.length ?? 0), 0)
  const allData = (on: boolean) => setCluster((c) => ({ ...c, spec: { ...c.spec, nodes: c.spec.nodes.map((n) => ({ ...n, dataDisks: on ? dataCandidates(machineOf(draft, n), n.installDisk?.path).map((d) => d.devPath) : undefined })) } }))
  const columns: Column<number>[] = [
    { id: 'machine', header: 'Machine', cell: (i) => { const n = c.spec.nodes[i]; const m = machineOf(draft, n); return (
      <span class="flex flex-col">
        <span class="flex items-center gap-2"><span class="font-medium">{modelOf(m)}</span><TypePill m={m} /></span>
        <span class="text-[10px] text-muted mono">{n.ip} · {n.mac} · {m?.inventory?.cpus ?? '?'} CPU · {fmt.bytes(m?.inventory?.memoryBytes ?? 0)}{n.kvm ? ' · kvm' : ''}</span>
      </span>
    ) } },
    { id: 'pool', header: 'Pool', cell: (i) => (
      <select class="input !py-1" value={c.spec.nodes[i].pool} onChange={(e) => { const p = poolOf((e.target as HTMLSelectElement).value)!; setNode(i, { pool: p.name, role: p.role }) }}>
        {pools.map((p) => <option key={p.name} value={p.name}>{p.name}{p.role === 'controlplane' ? ' (control plane)' : ''}</option>)}
      </select>
    ) },
    { id: 'hostname', header: 'Hostname', cell: (i) => <input class="input !py-1 mono w-48" value={c.spec.nodes[i].hostname} onInput={(e) => setNode(i, { hostname: (e.target as HTMLInputElement).value })} /> },
    { id: 'install', header: 'Install disk', cell: (i) => { const n = c.spec.nodes[i]; const disks = installCandidates(machineOf(draft, n)); return (
      <select class="input !py-1 mono" value={n.installDisk?.path ?? ''} onChange={(e) => { const v = (e.target as HTMLSelectElement).value; const data = (n.dataDisks ?? []).filter((d) => d !== v); setNode(i, { installDisk: v ? { path: v } : undefined, dataDisks: data.length ? data : undefined }) }}>
        {poolOf(n.pool)?.installDisk && <option value="">pool policy</option>}
        {disks.map((d) => <option key={d.devPath} value={d.devPath}>{diskLabel(d)}</option>)}
        {disks.length === 0 && <option value="">no disk</option>}
      </select>
    ) } },
    { id: 'data', header: <>Data disks{spare > 0 && <button class="btn btn-xs ml-2 font-normal" onClick={() => allData(claimed < spare)}>{claimed < spare ? 'Use all' : 'None'}</button>}</>, cell: (i) => {
      const n = c.spec.nodes[i]
      const data = dataCandidates(machineOf(draft, n), n.installDisk?.path)
      const toggle = (path: string, on: boolean) => {
        const cur = (n.dataDisks ?? []).filter((d) => d !== path)
        const next = on ? data.map((d) => d.devPath).filter((d) => d === path || cur.includes(d)) : cur
        setNode(i, { dataDisks: next.length ? next : undefined })
      }
      return (
        <span class="flex flex-col mono text-[12px]">
          {data.length === 0 && <span class="text-muted">—</span>}
          {data.map((d) => <label key={d.devPath} class="flex items-center gap-1.5"><input type="checkbox" checked={n.dataDisks?.includes(d.devPath) ?? false} onChange={(e) => toggle(d.devPath, (e.target as HTMLInputElement).checked)} />{d.devPath} <span class="text-muted">{fmt.bytes(d.sizeBytes)}{d.model ? ` · ${d.model}` : ''}</span></label>)}
        </span>
      )
    } },
    { id: 'labels', header: 'Node labels', cell: (i) => { const n = c.spec.nodes[i]; return <span class="text-[12px] text-muted mono">{Object.entries({ ...(poolOf(n.pool)?.labels ?? {}), ...(n.labels ?? {}) }).map(([k, v]) => `${k}=${v}`).join(' ') || '—'}</span> } },
  ]
  return (
    <>
      <div class="panel p-3 flex flex-col gap-1">
        <div class="flex items-center gap-3">
          <span class="label">Proposal</span>
          <span class="text-[13px]">{cps} control plane{cps === 1 ? '' : 's'}{cps >= 3 ? ' (etcd HA)' : ''}, {c.spec.nodes.length - cps} worker{c.spec.nodes.length - cps === 1 ? '' : 's'}{c.spec.controlPlane.allowScheduling ? ', control planes schedulable' : ', dedicated control planes'}</span>
          <span class="ml-auto flex gap-2">
            <button class="btn !py-1" onClick={autoName}>Auto-name</button>
            <button class="btn !py-1" disabled={busy} onClick={() => reset().catch(() => {})}>Reset to proposal</button>
          </span>
        </div>
        <p class="text-[13px] text-muted">Change any cell; data disks are wiped and mounted at <span class="mono">/var/mnt/data-N</span>.</p>
      </div>
      <DataTable search={false} columns={columns} rows={c.spec.nodes.map((_, i) => i)} rowKey={(i) => c.spec.nodes[i].mac ?? c.spec.nodes[i].ip} />
      <div class="flex flex-col gap-2">
        <div><span class="label">Pools</span><p class="text-[13px] text-muted">Role, labels, taints, extensions and disk policy, inherited by their nodes.</p></div>
        <PoolsEditor pools={pools} onChange={setPools} inUse={(name) => c.spec.nodes.filter((n) => n.pool === name).length} defaultExtensions={c.spec.extensions} />
      </div>
    </>
  )
}
