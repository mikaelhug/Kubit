import { useMemo, useState } from 'preact/hooks'
import { fmt, type LabHost, type VMSize } from '../../api'
import { diskChoices, effectiveSlots, parseChoice, storagePlan, withSlots, type DiskOpt } from '../../labdisks'
import { EPH_CHOICES, MIN_VM_MIB, longhorn, suggestPlan, type SizingInput, type Suggestion } from '../../labsizing'
import { isDnsLabel } from '../../net'
import { DataTable, type Column } from '../DataTable'
import { Field, Meter, Notice } from '../ui'

export { MAC_MAX_MIB, MIN_VM_MIB, PREFERRED_MIB } from '../../labsizing'
export const RESERVED_MIB = 2048
const MIN_CP_MIB = 2048
export const reserveOf = (lh?: LabHost | null) => lh?.capacity.reserveMiB || RESERVED_MIB
export const mib = (n: number) => n * 1048576

export type VMRow = VMSize & { key: number }
let vmKey = 0
const keyed = (rows: VMSize[]): VMRow[] => rows.map((r) => ({ ...r, key: ++vmKey }))

export function useSuggested(input: SizingInput) {
  const sig = JSON.stringify(input)
  const s = useMemo(() => { const out = suggestPlan(input); return { ...out, rows: keyed(out.rows) } }, [sig])
  const [own, setOwn] = useState<VMRow[] | null>(null)
  return { s, rows: own ?? s.rows, setRows: (rows: VMRow[]) => setOwn(rows), edited: own !== null, reset: () => setOwn(null) }
}

export function VMTable({ rows, onChange, roles, disks, onSuggest }: { rows: VMRow[]; onChange: (rows: VMRow[]) => void; roles: boolean; disks?: DiskOpt[]; onSuggest?: () => void }) {
  const set = (i: number, patch: Partial<VMSize>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const num = (i: number, k: 'cpus' | 'memMiB' | 'diskGiB' | 'dataGiB', min: number, step = 1) => (
    <input class="input !py-1 w-full" type="number" min={min} step={step} value={rows[i][k]} onInput={(e) => set(i, { [k]: Number((e.target as HTMLInputElement).value) })} />
  )
  const slots = disks ? effectiveSlots(rows, disks) : []
  const place = (i: number, slot: 'system' | 'data') => {
    const value = slots[i][slot]
    const c = parseChoice(value)
    const pick = (v: string) => set(i, slot === 'system' ? { systemDisk: v } : { dataDisk: v, dataGiB: v.startsWith('image:') ? rows[i].dataGiB || 100 : 0 })
    return (
      <div class="flex gap-1">
        <select class="input !py-1 min-w-0 flex-1" value={value} onChange={(e) => pick((e.target as HTMLSelectElement).value)}>
          {diskChoices(rows, disks ?? [], i, slot).map((o) => <option key={o.value} value={o.value} disabled={o.disabled}>{o.label}</option>)}
        </select>
        {c.key && !c.whole && <span class="w-20 shrink-0" title="GiB">{slot === 'system' ? num(i, 'diskGiB', 8) : num(i, 'dataGiB', 1, 10)}</span>}
      </div>
    )
  }
  const sizes: Column<number>[] = disks
    ? [
        { id: 'system', header: <span title="Talos and /var; image size in GiB">System disk</span>, cell: (i) => place(i, 'system') },
        { id: 'data', header: <span title="Mounted at /var/mnt/data-1">Data disk</span>, cell: (i) => place(i, 'data') },
      ]
    : [
        { id: 'disk', header: 'Disk (GiB)', width: '6rem', cell: (i) => num(i, 'diskGiB', 8) },
        { id: 'data', header: <span title="0 = none; mounted at /var/mnt/data-1">Data (GiB)</span>, width: '6rem', cell: (i) => num(i, 'dataGiB', 0, 10) },
      ]
  const columns: Column<number>[] = [
    { id: 'n', header: '#', width: '2rem', cell: (i) => <span class="text-muted">{i + 1}</span> },
    ...(roles ? [{ id: 'role', header: 'Role', width: '9rem', cell: (i: number) => { const r = rows[i]; return <select class="input !py-1 w-full" value={r.role} onChange={(e) => { const role = (e.target as HTMLSelectElement).value as VMSize['role']; set(i, { role, memMiB: role === 'controlplane' ? Math.max(r.memMiB, MIN_CP_MIB) : r.memMiB }) }}><option value="controlplane">control plane</option><option value="worker">worker</option></select> } }] : []),
    { id: 'cpus', header: 'vCPU', width: '5rem', cell: (i) => num(i, 'cpus', 1) },
    { id: 'mem', header: 'RAM (MiB)', width: '7rem', cell: (i) => num(i, 'memMiB', MIN_VM_MIB, 256) },
    ...sizes,
    { id: 'remove', header: '', width: '2rem', align: 'right', cell: (i) => <button class="btn btn-sm" title="Remove" disabled={rows.length <= 1} onClick={() => onChange(rows.filter((_, j) => j !== i))}>✕</button> },
  ]
  const shared = (c?: string) => (c && parseChoice(c).whole ? undefined : c)
  const add = () => {
    const last = rows[rows.length - 1]
    onChange([...rows, { ...last, key: ++vmKey, name: undefined, role: 'worker', systemDisk: shared(last.systemDisk), dataDisk: shared(last.dataDisk) }])
  }
  return (
    <div class="flex flex-col gap-2">
      <DataTable search={false} columns={columns} rows={rows.map((_, i) => i)} rowKey={(i) => String(rows[i].key)} />
      <div class="flex gap-2">
        <button class="btn btn-sm" onClick={add}>+ Add VM</button>
        {onSuggest && <button class="btn btn-sm" title="Size the VMs for this host" onClick={onSuggest}>Suggest</button>}
      </div>
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

const limitText: Record<string, string> = { memory: 'Memory limits', cpu: 'CPUs limit', disk: 'Disk space limits' }

export function SizingNotes({ s, edited, count }: { s: Suggestion; edited: boolean; count: number }) {
  if (!s.fits) return <Notice tone="warn">Not enough free disk for a VM.</Notice>
  if (edited || !s.limit) return null
  return <span class="text-[12px] text-muted">{limitText[s.limit]} this host to {count} VM{count === 1 ? '' : 's'}.</span>
}

export function StorageNotes({ rows, disks }: { rows: VMSize[]; disks?: DiskOpt[] }) {
  if (!disks) return null
  const s = storagePlan(rows, disks)
  return (
    <>
      {s.problem && <Notice tone="bad">{s.problem[0].toUpperCase() + s.problem.slice(1)}.</Notice>}
      {s.formats.length > 0 && <Notice tone="bad">Formats {s.formats.join(', ')} for VM images.</Notice>}
      {s.wholes.length > 0 && <Notice tone="bad">Wipes {s.wholes.join(', ')} for whole-disk VMs.</Notice>}
      {s.over.length > 0 && <Notice tone="warn">Images on {s.over.join(', ')} may outgrow the disk.</Notice>}
    </>
  )
}

export interface PlanInput { memMiB: number; reserve: number; cpus: number; name: string; disks?: DiskOpt[]; choices?: boolean; macFreeGiB?: number; mac?: boolean; most?: number }

export function usePlan(input: PlanInput) {
  const { memMiB, reserve, cpus, disks, choices } = input
  const [withVMs, setWithVMs] = useState(true)
  const [withCluster, setWithCluster] = useState(true)
  const sug = useSuggested({ memMiB: memMiB > 0 ? memMiB - reserve : 0, cpus, disks, macFreeGiB: input.macFreeGiB, cluster: true, mac: input.mac, most: input.most })
  const [eph, setEph] = useState<number | null>(null)
  const [name, setName] = useState(input.name)
  const rows = sug.rows
  const ephGiB = eph ?? sug.s.ephGiB
  const cps = cpCount(rows)
  const need = withVMs ? totalMem(rows) : 0
  const over = memMiB > 0 && withVMs && need > memMiB - reserve
  const cpuOver = cpus > 0 && withVMs && rows.reduce((s, v) => s + v.cpus, 0) > cpus * 2
  const topologyOk = cps === 1 || cps === 3
  const vmProblem = withVMs ? rowsProblem(withCluster ? rows : rows.map((v) => ({ ...v, role: 'worker' as const }))) : null
  const diskProblem = withVMs && disks ? storagePlan(rows, disks).problem : null
  const blocked = over || !!vmProblem || !!diskProblem || (withVMs && withCluster && (!isDnsLabel(name) || !topologyOk))
  const splits = (disks ? effectiveSlots(rows, disks).map((s) => s.data === '') : rows.map((r) => r.dataGiB === 0)).some(Boolean)
  const each = rows.map(({ key: _k, ...v }) => (withCluster ? v : { ...v, role: 'worker' as const }))
  const vms = choices && disks ? withSlots(each, disks) : each.map(({ systemDisk: _s, dataDisk: _d, ...v }) => v)
  const body = withVMs ? { vms: { each: vms }, cluster: withCluster ? { name, controlPlanes: cps as 1 | 3, ephemeralSize: splits ? `${ephGiB}GiB` : undefined } : undefined } : {}
  const reset = () => { sug.reset(); setEph(null) }
  return { withVMs, setWithVMs, withCluster, setWithCluster, rows, setRows: sug.setRows, name, setName, cps, need, over, cpuOver, vmProblem, topologyOk, blocked, body, memMiB, reserve, disks, choices, suggestion: sug.s, edited: sug.edited || eph !== null, reset, ephGiB, setEph, splits }
}

export function PlanFields({ p, keeps }: { p: ReturnType<typeof usePlan>; keeps: string }) {
  const avail = p.memMiB - p.reserve
  const lh = longhorn(p.rows, p.disks, p.ephGiB)
  const tight = lh.tight.map((i) => i + 1)
  return (
    <>
      <label class="flex items-center gap-2 text-[13px] font-medium"><input type="checkbox" checked={p.withVMs} onChange={(e) => p.setWithVMs((e.target as HTMLInputElement).checked)} /> Add Talos VMs</label>
      {p.withVMs && (
        <div class="flex flex-col gap-3">
          <VMTable rows={p.rows} onChange={p.setRows} roles={p.withCluster} disks={p.choices ? p.disks : undefined} onSuggest={p.edited ? p.reset : undefined} />
          <SizingNotes s={p.suggestion} edited={p.edited} count={p.rows.length} />
          {p.memMiB > 0 ? <Meter label={`Memory: ${fmt.bytes(mib(p.need))} of ${fmt.bytes(mib(avail))} (${keeps} keeps ${fmt.bytes(mib(p.reserve))})`} used={p.need} cap={Math.max(1, avail)} format={(n) => fmt.bytes(mib(n))} color={p.over ? 'var(--bad)' : 'var(--accent)'} /> : <span class="text-[12px] text-muted">{fmt.bytes(mib(p.need))} of memory for VMs; checked after the install.</span>}
          {p.over && <Notice tone="bad">Not enough memory.</Notice>}
          {!p.over && p.vmProblem && <Notice tone="bad">{p.vmProblem}.</Notice>}
          {p.cpuOver && <Notice tone="warn">More than 2× the host's CPUs; the VMs will contend.</Notice>}
          <StorageNotes rows={p.rows} disks={p.disks} />
          {p.withCluster && tight.length > 0 && <Notice tone="warn">VM{tight.length > 1 ? 's' : ''} {tight.join(', ')}: under 10 GiB left for Longhorn.</Notice>}
        </div>
      )}
      {p.withVMs && <label class="flex items-center gap-2 text-[13px] font-medium"><input type="checkbox" checked={p.withCluster} onChange={(e) => p.setWithCluster((e.target as HTMLInputElement).checked)} /> Create a cluster from them</label>}
      {p.withVMs && p.withCluster && (
        <div class="grid grid-cols-3 gap-3 items-start">
          <Field label="Cluster name"><input class="input mono" value={p.name} onInput={(e) => p.setName((e.target as HTMLInputElement).value.toLowerCase())} /></Field>
          {p.splits ? (
            <Field label="/var per VM" hint="Rest of each system disk goes to Longhorn">
              <select class="input" value={p.ephGiB} onChange={(e) => p.setEph(Number((e.target as HTMLSelectElement).value))}>
                {[...new Set([...EPH_CHOICES, p.ephGiB])].sort((a, b) => b - a).map((g) => <option key={g} value={g}>{g} GiB</option>)}
              </select>
            </Field>
          ) : <div />}
          <div class="text-[13px] pt-6 flex flex-col">
            {p.topologyOk ? <span>{p.cps} control plane{p.cps === 1 ? '' : 's'}, {p.rows.length - p.cps} worker{p.rows.length - p.cps === 1 ? '' : 's'}</span> : <span class="text-bad">Choose 1 or 3 control planes.</span>}
            <span class="text-[12px] text-muted">Longhorn: {lh.gib} GiB, {lh.replicas} replica{lh.replicas === 1 ? '' : 's'}</span>
          </div>
        </div>
      )}
    </>
  )
}
