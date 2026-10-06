import { useState } from 'preact/hooks'
import { api } from '../api'
import { clusters, toast } from '../store'
import { ConfirmDialog } from './ui'

export function RemoveNode({ cluster, hostname }: { cluster: string; hostname: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button class="btn btn-sm" onClick={() => setOpen(true)}>Remove</button>
      {open && <ConfirmDialog title={`Remove ${hostname}`} action="Remove" tone="danger" onClose={() => setOpen(false)}
        impact={<span>Removes {hostname} from cluster.yaml. Apply then drains and resets it.</span>}
        onConfirm={() => api.removeNode(cluster, hostname, clusters.value.find((c) => c.name === cluster)?.hash ?? '').then(() => { setOpen(false); toast(`${hostname} removed from cluster.yaml`, 'good') }).catch((e) => toast(e.message, 'error'))} />}
    </>
  )
}
