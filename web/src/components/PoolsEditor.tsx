import { useState } from 'preact/hooks'
import type { Pool } from '../api'
import { KVEditor } from './KVEditor'
import { ListInput } from './ListInput'
import { Field, Pill } from './ui'

const knownExtensions = ['siderolabs/gvisor', 'siderolabs/iscsi-tools', 'siderolabs/util-linux-tools', 'siderolabs/intel-ucode', 'siderolabs/amd-ucode', 'siderolabs/i915', 'siderolabs/nvidia-open-gpu-kernel-modules-lts', 'siderolabs/nvidia-container-toolkit-lts', 'siderolabs/qemu-guest-agent', 'siderolabs/zfs', 'siderolabs/tailscale']

export function PoolsEditor({ pools, onChange, inUse, defaultExtensions }: { pools: Pool[]; onChange: (p: Pool[]) => void; inUse: (name: string) => number; defaultExtensions?: string[] }) {
  const [open, setOpen] = useState<string | null>(null)
  const update = (i: number, patch: Partial<Pool>) => onChange(pools.map((p, j) => j === i ? { ...p, ...patch } : p))
  const add = () => {
    let n = 1, name = 'pool-1'
    while (pools.some((p) => p.name === name)) name = `pool-${++n}`
    onChange([...pools, { name, role: 'worker' }])
    setOpen(name)
  }
  return (
    <div class="flex flex-col gap-2">
      {pools.map((p, i) => {
        const count = inUse(p.name)
        const expanded = open === p.name
        return (
          <div key={i} class="panel">
            <div class="flex items-center gap-3 px-4 py-2.5 cursor-pointer" onClick={() => setOpen(expanded ? null : p.name)}>
              <span class="text-muted w-3">{expanded ? '▾' : '▸'}</span>
              <span class="font-medium mono">{p.name}</span>
              <Pill tone={p.role === 'controlplane' ? 'info' : 'muted'}>{p.role === 'controlplane' ? 'control plane' : 'worker'}</Pill>
              <span class="text-[12px] text-muted">{count} node{count === 1 ? '' : 's'}</span>
              <span class="text-[12px] text-muted ml-auto truncate max-w-[50%]">
                {[Object.keys(p.labels ?? {}).length ? `${Object.keys(p.labels ?? {}).length} label${Object.keys(p.labels ?? {}).length === 1 ? '' : 's'}` : '', Object.keys(p.taints ?? {}).length ? `${Object.keys(p.taints ?? {}).length} taint${Object.keys(p.taints ?? {}).length === 1 ? '' : 's'}` : '', p.extensions?.length ? `${p.extensions.length} extension${p.extensions.length === 1 ? '' : 's'}` : ''].filter(Boolean).join(' · ') || 'no overrides'}
              </span>
            </div>
            {expanded && (
              <div class="px-4 pb-4 pt-1 border-t border-border grid grid-cols-1 md:grid-cols-2 gap-3">
                <Field label="Name" hint="DNS label; also the node label and hostname prefix">
                  <input class="input mono" value={p.name} disabled={count > 0} onInput={(e) => { const v = (e.target as HTMLInputElement).value; update(i, { name: v }); setOpen(v) }} />
                </Field>
                <Field label="Role" hint={p.role === 'controlplane' ? 'Only one control-plane pool' : 'No etcd member'}>
                  <select class="input" value={p.role} disabled={count > 0} onChange={(e) => update(i, { role: (e.target as HTMLSelectElement).value as Pool['role'] })}>
                    <option value="worker">Worker</option>
                    <option value="controlplane">Control plane</option>
                  </select>
                </Field>
                <Field label="Labels" hint="key=value per line">
                  <KVEditor value={p.labels} onChange={(v) => update(i, { labels: v })} placeholder="key=value" />
                </Field>
                <Field label="Taints" hint="key=value:Effect per line">
                  <KVEditor value={p.taints} onChange={(v) => update(i, { taints: v })} placeholder="key=value:NoSchedule" />
                </Field>
                <Field label="System extensions" hint={`Comma-separated; empty inherits ${defaultExtensions?.length ? defaultExtensions.join(', ') : 'the cluster default'}`}>
                  <ListInput list="known-extensions" value={p.extensions} onChange={(v) => update(i, { extensions: v.length ? v : undefined })} />
                  <datalist id="known-extensions">{knownExtensions.map((x) => <option key={x} value={x} />)}</datalist>
                </Field>
                <Field label="Install disk policy" hint="Minimum size and/or transport; a node's own disk wins">
                  <div class="flex gap-2">
                    <input class="input mono" placeholder="min size" value={p.installDisk?.selector?.minSize ?? ''} onInput={(e) => update(i, { installDisk: sel(p, { minSize: (e.target as HTMLInputElement).value }) })} />
                    <input class="input mono" placeholder="transport" value={p.installDisk?.selector?.type ?? ''} onInput={(e) => update(i, { installDisk: sel(p, { type: (e.target as HTMLInputElement).value }) })} />
                  </div>
                </Field>
                <Field label="Annotations" hint="key=value per line">
                  <KVEditor value={p.annotations} onChange={(v) => update(i, { annotations: v })} />
                </Field>
                <div class="flex items-end justify-end">
                  <button class="btn btn-danger" disabled={count > 0 || p.role === 'controlplane'} title={count > 0 ? 'Move its nodes to another pool first' : p.role === 'controlplane' ? 'The control-plane pool cannot be removed' : ''} onClick={() => onChange(pools.filter((_, j) => j !== i))}>Remove pool</button>
                </div>
              </div>
            )}
          </div>
        )
      })}
      <div><button class="btn" onClick={add}>+ Add pool</button></div>
    </div>
  )
}

function sel(p: Pool, patch: { minSize?: string; type?: string }) {
  const s = { ...(p.installDisk?.selector ?? {}), ...patch }
  if (!s.minSize) delete s.minSize
  if (!s.type) delete s.type
  if (!s.model) delete s.model
  return Object.keys(s).length ? { selector: s } : undefined
}
