import { useState } from 'preact/hooks'
import { fmt, type LabHost, type VMSize } from '../../api'
import { isDnsLabel } from '../../net'
import { DataTable, type Column } from '../DataTable'
import { Field, Meter, Notice } from '../ui'

export const RESERVED_MIB = 2048
const MIN_CP_MIB = 2048
export const MIN_VM_MIB = 2048
export const PREFERRED_MIB = 3072
export const reserveOf = (lh?: LabHost | null) => lh?.capacity.reserveMiB || RESERVED_MIB
export const mib = (n: number) => n * 1048576

export type VMRow = VMSize & { key: number }
let vmKey = 0
const defaultVM = (role: VMSize['role'], mem = 3072): VMRow => ({ key: ++vmKey, role, cpus: 2, memMiB: Math.max(mem, MIN_VM_MIB), diskGiB: 60, dataGiB: 0 })

export function planFor(hostMiB: number, cluster = true, reserve = RESERVED_MIB, most = Infinity): VMRow[] {
  if (hostMiB <= 0) return [defaultVM('controlplane'), defaultVM('worker'), defaultVM('worker'), defaultVM('worker')]
  const avail = hostMiB - reserve
  let n = Math.min(4, Math.floor(avail / PREFERRED_MIB))
  if (n < 2) n = Math.min(4, Math.floor(avail / MIN_VM_MIB))
  if (n < 1) return [defaultVM('controlplane', MIN_VM_MIB)]
  const each = Math.min(most, Math.floor(avail / n / 256) * 256)
  return Array.from({ length: n }, (_, i) => defaultVM(cluster && i === 0 ? 'controlplane' : 'worker', each))
}

export function VMTable({ rows, onChange, roles }: { rows: VMRow[]; onChange: (rows: VMRow[]) => void; roles: boolean }) {
  const set = (i: number, patch: Partial<VMSize>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const num = (i: number, k: 'cpus' | 'memMiB' | 'diskGiB' | 'dataGiB', min: number, step = 1) => (
    <input class="input !py-1 w-full" type="number" min={min} step={step} value={rows[i][k]} onInput={(e) => set(i, { [k]: Number((e.target as HTMLInputElement).value) })} />
  )
  const columns: Column<number>[] = [
    { id: 'n', header: '#', width: '2rem', cell: (i) => <span class="text-muted">{i + 1}</span> },
    ...(roles ? [{ id: 'role', header: 'Role', width: '9rem', cell: (i: number) => { const r = rows[i]; return <select class="input !py-1 w-full" value={r.role} onChange={(e) => { const role = (e.target as HTMLSelectElement).value as VMSize['role']; set(i, { role, memMiB: role === 'controlplane' ? Math.max(r.memMiB, MIN_CP_MIB) : r.memMiB }) }}><option value="controlplane">control plane</option><option value="worker">worker</option></select> } }] : []),
    { id: 'cpus', header: 'vCPU', width: '5rem', cell: (i) => num(i, 'cpus', 1) },
    { id: 'mem', header: 'RAM (MiB)', width: '7rem', cell: (i) => num(i, 'memMiB', MIN_VM_MIB, 256) },
    { id: 'disk', header: 'Disk (GiB)', width: '6rem', cell: (i) => num(i, 'diskGiB', 8) },
    { id: 'data', header: <span title="0 = none; mounted at /var/mnt/data-1">Data (GiB)</span>, width: '6rem', cell: (i) => num(i, 'dataGiB', 0, 10) },
    { id: 'remove', header: '', width: '2rem', align: 'right', cell: (i) => <button class="btn !px-2 !py-1" title="Remove" disabled={rows.length <= 1} onClick={() => onChange(rows.filter((_, j) => j !== i))}>✕</button> },
  ]
  return (
    <div class="flex flex-col gap-2">
      <DataTable search={false} columns={columns} rows={rows.map((_, i) => i)} rowKey={(i) => String(rows[i].key)} />
      <div><button class="btn !py-1" onClick={() => onChange([...rows, defaultVM('worker', rows[rows.length - 1]?.memMiB)])}>+ Add VM</button></div>
    </div>
  )
}

export const totalMem = (rows: { memMiB: number }[]) => rows.reduce((s, v) => s + v.memMiB, 0)
const cpCount = (rows: VMSize[]) => rows.filter((v) => v.role === 'controlplane').length

export const rowsProblem = (rows: VMSize[]): string | null => {
  for (const v of rows) {
    if (v.cpus < 1) return 'every VM needs at least 1 vCPU'
    if (v.diskGiB < 8) return 'every VM needs at least 8 GiB disk'
    if (v.memMiB < MIN_VM_MIB) return 'every VM needs at least 2048 MiB'
  }
  return null
}

export function usePlan(memMiB: number, reserve: number, cluster = 'lab', most = Infinity) {
  const [withVMs, setWithVMs] = useState(true)
  const [withCluster, setWithCluster] = useState(true)
  const [rows, setRows] = useState<VMRow[]>(() => planFor(memMiB, true, reserve, most))
  const [name, setName] = useState(cluster)
  const [repo, setRepo] = useState({ url: '', path: '' })
  const cps = cpCount(rows)
  const need = withVMs ? totalMem(rows) : 0
  const over = memMiB > 0 && withVMs && need > memMiB - reserve
  const topologyOk = cps === 1 || cps === 3
  const vmProblem = withVMs ? rowsProblem(withCluster ? rows : rows.map((v) => ({ ...v, role: 'worker' as const }))) : null
  const blocked = over || !!vmProblem || (withVMs && withCluster && (!isDnsLabel(name) || !topologyOk))
  const body = withVMs ? { vms: { each: rows.map(({ key: _k, ...v }) => (withCluster ? v : { ...v, role: 'worker' as const })) }, cluster: withCluster ? { name, controlPlanes: cps as 1 | 3, repository: repo.url.trim() ? { url: repo.url.trim(), path: repo.path.trim() || undefined } : undefined } : undefined } : {}
  return { withVMs, setWithVMs, withCluster, setWithCluster, rows, setRows, name, setName, repo, setRepo, cps, need, over, vmProblem, topologyOk, blocked, body, memMiB, reserve }
}

export function PlanFields({ p, keeps }: { p: ReturnType<typeof usePlan>; keeps: string }) {
  const avail = p.memMiB - p.reserve
  return (
    <>
      <label class="flex items-center gap-2 text-[13px] font-medium"><input type="checkbox" checked={p.withVMs} onChange={(e) => p.setWithVMs((e.target as HTMLInputElement).checked)} /> Add Talos VMs</label>
      {p.withVMs && (
        <div class="flex flex-col gap-3">
          <VMTable rows={p.rows} onChange={p.setRows} roles={p.withCluster} />
          {p.memMiB > 0 ? <Meter label={`Memory: ${fmt.bytes(mib(p.need))} of ${fmt.bytes(mib(avail))} (${keeps} keeps ${fmt.bytes(mib(p.reserve))})`} used={p.need} cap={Math.max(1, avail)} format={(n) => fmt.bytes(mib(n))} /> : <span class="text-[12px] text-muted">{fmt.bytes(mib(p.need))} of memory for VMs; checked after the install.</span>}
          {p.over && <Notice tone="bad">Not enough memory.</Notice>}
          {!p.over && p.vmProblem && <Notice tone="bad">{p.vmProblem}.</Notice>}
        </div>
      )}
      {p.withVMs && <label class="flex items-center gap-2 text-[13px] font-medium"><input type="checkbox" checked={p.withCluster} onChange={(e) => p.setWithCluster((e.target as HTMLInputElement).checked)} /> Create a cluster from them</label>}
      {p.withVMs && p.withCluster && (
        <div class="grid grid-cols-2 gap-3 items-end">
          <Field label="Cluster name"><input class="input mono" value={p.name} onInput={(e) => p.setName((e.target as HTMLInputElement).value.toLowerCase())} /></Field>
          <div class="text-[13px] pb-2">{p.topologyOk ? <span>{p.cps} control plane{p.cps === 1 ? '' : 's'}, {p.rows.length - p.cps} worker{p.rows.length - p.cps === 1 ? '' : 's'}</span> : <span class="text-bad">Choose 1 or 3 control planes.</span>}</div>
          <Field label="Apps repository" hint="Public HTTPS Git URL; optional"><input class="input mono" value={p.repo.url} placeholder="https://github.com/you/apps.git" onInput={(e) => p.setRepo({ ...p.repo, url: (e.target as HTMLInputElement).value })} /></Field>
          <Field label="Path"><input class="input mono" value={p.repo.path} placeholder="./" onInput={(e) => p.setRepo({ ...p.repo, path: (e.target as HTMLInputElement).value })} /></Field>
        </div>
      )}
    </>
  )
}
