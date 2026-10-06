import { useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api } from '../api'
import { clusters, toast } from '../store'
import { updateText } from '../versions'
import { ConfirmDialog } from './ui'

export function Upgrade({ cluster, talos, kubernetes }: { cluster: string; talos: string; kubernetes: string }) {
  const [open, setOpen] = useState(false)
  const { route } = useLocation()
  const target = updateText(talos, kubernetes)
  return (
    <>
      <button class="btn btn-sm ml-auto" onClick={() => setOpen(true)}>Upgrade</button>
      {open && (
        <ConfirmDialog title={`Upgrade ${cluster}`} action="Write cluster.yaml" onClose={() => setOpen(false)}
          impact={<p>{target}</p>}
          onConfirm={() => api.setVersions(cluster, { talosVersion: talos || undefined, kubernetesVersion: kubernetes || undefined, hash: clusters.value.find((c) => c.name === cluster)?.hash ?? '' })
            .then(() => { setOpen(false); route(`/clusters/${cluster}/changes`) })
            .catch((e) => toast(e.message, 'error'))} />
      )}
    </>
  )
}
