import { useState } from 'preact/hooks'
import type { Pool } from '../api'
import { Field, Pill } from './ui'

/** Edits a string map as "key=value" lines; taints use "key=value:Effect". */
export function KVEditor({ value, onChange, placeholder, mono = true }: { value?: Record<string, string>; onChange: (v: Record<string, string> | undefined) => void; placeholder?: string; mono?: boolean }) {
  const [text, setText] = useState(toLines(value))
  const commit = (t: string) => {
    setText(t)
    const out: Record<string, string> = {}
    for (const line of t.split('\n')) {
      const s = line.trim()
      if (!s) continue
      const i = s.indexOf('=')
      if (i <= 0) { out[s] = '' ; continue }
      out[s.slice(0, i).trim()] = s.slice(i + 1).trim()
    }
    onChange(Object.keys(out).length ? out : undefined)
  }
  return <textarea class={`input !text-[12px] min-h-[56px] ${mono ? 'mono' : ''}`} rows={Math.max(2, text.split('\n').length)} value={text} placeholder={placeholder} spellcheck={false} onInput={(e) => commit((e.target as HTMLTextAreaElement).value)} />
}

function toLines(m?: Record<string, string>) { return Object.entries(m ?? {}).map(([k, v]) => v === '' ? k : `${k}=${v}`).join('\n') }

export const knownExtensions = ['siderolabs/gvisor', 'siderolabs/iscsi-tools', 'siderolabs/util-linux-tools', 'siderolabs/intel-ucode', 'siderolabs/amd-ucode', 'siderolabs/i915', 'siderolabs/nvidia-open-gpu-kernel-modules-lts', 'siderolabs/nvidia-container-toolkit-lts', 'siderolabs/qemu-guest-agent', 'siderolabs/zfs', 'siderolabs/tailscale']

/**
 * Pools own role, labels, taints, extensions and the default install-disk policy. One
 * pool must have role controlplane; a pool in use cannot be removed.
 */
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
                <Field label="Name" hint="DNS label; becomes the kubit.dev/pool node label and the hostname prefix.">
                  <input class="input mono" value={p.name} disabled={count > 0} onInput={(e) => { const v = (e.target as HTMLInputElement).value; update(i, { name: v }); setOpen(v) }} />
                </Field>
                <Field label="Role" hint={p.role === 'controlplane' ? 'Exactly one pool may hold the control planes.' : 'Workers join without etcd.'}>
                  <select class="input" value={p.role} disabled={count > 0} onChange={(e) => update(i, { role: (e.target as HTMLSelectElement).value as Pool['role'] })}>
                    <option value="worker">Worker</option>
                    <option value="controlplane">Control plane</option>
                  </select>
                </Field>
                <Field label="Labels" hint="One per line, key=value. Applied to every node in the pool.">
                  <KVEditor value={p.labels} onChange={(v) => update(i, { labels: v })} placeholder={'workload=gpu\ntopology.kubernetes.io/zone=rack-a'} />
                </Field>
                <Field label="Taints" hint="One per line, key=value:Effect (NoSchedule, PreferNoSchedule, NoExecute).">
                  <KVEditor value={p.taints} onChange={(v) => update(i, { taints: v })} placeholder="nvidia.com/gpu=true:NoSchedule" />
                </Field>
                <Field label="System extensions" hint={`Comma-separated Image Factory extensions. Empty inherits the cluster default${defaultExtensions?.length ? ` (${defaultExtensions.join(', ')})` : ''}; a different set gets its own installer image.`}>
                  <input class="input mono" list="known-extensions" value={(p.extensions ?? []).join(', ')} onInput={(e) => { const v = (e.target as HTMLInputElement).value.split(/[,\s]+/).filter(Boolean); update(i, { extensions: v.length ? v : undefined }) }} />
                  <datalist id="known-extensions">{knownExtensions.map((x) => <option key={x} value={x} />)}</datalist>
                </Field>
                <Field label="Install disk policy" hint="Default for nodes in the pool; a node's explicit disk wins. Selector: minimum size and/or transport (nvme, sata, virtio).">
                  <div class="flex gap-2">
                    <input class="input mono" placeholder="min size, e.g. 100GB" value={p.installDisk?.selector?.minSize ?? ''} onInput={(e) => update(i, { installDisk: sel(p, { minSize: (e.target as HTMLInputElement).value }) })} />
                    <input class="input mono" placeholder="type, e.g. nvme" value={p.installDisk?.selector?.type ?? ''} onInput={(e) => update(i, { installDisk: sel(p, { type: (e.target as HTMLInputElement).value }) })} />
                  </div>
                </Field>
                <Field label="Annotations" hint="Optional, key=value per line.">
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
