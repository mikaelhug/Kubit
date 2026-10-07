import { useEffect, useRef, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api } from '../api'
import { Dialog, ErrorBox, Field, inputValue } from './ui'

export function DestroyCluster({ cluster }: { cluster: string }) {
  const { route } = useLocation()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => { if (open) input.current?.focus() }, [open])
  const close = () => { setOpen(false); setTyped(''); setError(null) }
  const destroy = () => {
    setBusy(true)
    api.destroy(cluster, typed).then(() => { close(); route(`/clusters/${cluster}/changes`) }).catch((e) => setError(e.message)).finally(() => setBusy(false))
  }
  return (
    <>
      <button class="btn btn-sm btn-danger" onClick={() => setOpen(true)}>Destroy cluster</button>
      {open && (
        <Dialog title={`Destroy ${cluster}`} onClose={close}
          footer={<><button class="btn" onClick={close}>Cancel</button><button class="btn btn-danger" disabled={busy || typed !== cluster} onClick={destroy}>{busy ? 'Destroying' : 'Destroy cluster'}</button></>}>
          <ErrorBox error={error} />
          <span class="text-[13px] text-bad">Wipes every node's Talos state and data and reboots it into maintenance mode.</span>
          <Field label="Cluster name"><input class="input mono" value={typed} placeholder={cluster} ref={input} onInput={(e) => setTyped(inputValue(e).trim())} onKeyDown={(e) => { if (e.key === 'Enter' && typed === cluster) destroy() }} /></Field>
        </Dialog>
      )}
    </>
  )
}
