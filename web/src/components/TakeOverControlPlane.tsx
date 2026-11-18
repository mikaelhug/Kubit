import { useState } from 'preact/hooks'
import { api, type NodeSpec } from '../api'
import { toast, writeClusterYaml } from '../store'
import { ConfirmDialog, Field } from './ui'

function nextControlPlane(cluster: string, nodes: NodeSpec[]): string {
  for (let i = 1; ; i++) {
    const h = `${cluster}-cp-${String(i).padStart(2, '0')}`
    if (!nodes.some((n) => n.hostname === h)) return h
  }
}

export function TakeOverControlPlane({ cluster, hostname, nodes }: { cluster: string; hostname: string; nodes: NodeSpec[] }) {
  const cps = nodes.filter((n) => n.role === 'controlplane')
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [replaces, setReplaces] = useState('')
  const start = () => { setName(nextControlPlane(cluster, nodes)); setReplaces(cps[0]?.hostname ?? ''); setOpen(true) }
  if (cps.length === 0) return null
  return (
    <>
      <button class="btn btn-sm" onClick={start}>Take over control plane</button>
      {open && <ConfirmDialog title={`${hostname} takes over ${replaces}`} action="Write cluster.yaml" tone="danger" onClose={() => setOpen(false)}
        impact={<>
          <Field label="Hostname"><input class="input mono" value={name} autofocus onInput={(e) => setName((e.target as HTMLInputElement).value)} /></Field>
          {cps.length > 1 && <Field label="Replaces"><select class="input" value={replaces} onChange={(e) => setReplaces((e.target as HTMLSelectElement).value)}>{cps.map((n) => <option value={n.hostname}>{n.hostname}</option>)}</select></Field>}
          <span>Apply erases the install disk on {hostname}.</span>
        </>}
        onConfirm={() => writeClusterYaml(cluster, (hash) => api.takeOver(cluster, hostname, { hostname: name.trim(), replaces, hash })).then(() => { setOpen(false); toast(`${name.trim()} replaces ${replaces} in cluster.yaml`, 'good') }).catch((e) => toast(e.message, 'error'))} />}
    </>
  )
}
