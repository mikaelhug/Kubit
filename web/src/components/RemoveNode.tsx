import { useState } from 'preact/hooks'
import { api } from '../api'
import { toast, writeClusterYaml } from '../store'
import { ConfirmDialog } from './ui'

export function RemoveNode({ cluster, hostname, label = 'Remove' }: { cluster: string; hostname: string; label?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button class="btn btn-sm btn-danger" onClick={() => setOpen(true)}>{label}</button>
      {open && <ConfirmDialog title={`Remove ${hostname}`} action="Remove" tone="danger" onClose={() => setOpen(false)}
        impact={<span>Removes {hostname} from cluster.yaml. Apply then drains and resets it.</span>}
        onConfirm={() => writeClusterYaml(cluster, (hash) => api.removeNode(cluster, hostname, hash)).then(() => { setOpen(false); toast(`${hostname} removed from cluster.yaml`, 'good') }).catch((e) => toast(e.message, 'error'))} />}
    </>
  )
}
