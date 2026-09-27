import { useEffect, useState } from 'preact/hooks'
import { api } from '../api'
import { useLive } from '../useLive'
import { Code, Dialog, Notice } from './ui'

export const pxeBlocked = (e: { code?: string } | undefined) => e?.code === 'pxe-down' || e?.code === 'pxe-no-address'

export function PxeGate({ what, command, onReady, onClose }: { what: string; command?: string; onReady: () => void; onClose: () => void }) {
  const { data: st } = useLive(() => api.pxe(), [], [['', 'pxe']], { onError: 'silent' })
  const serving = !!(st?.running && st.ip)
  useEffect(() => { if (serving) onReady() }, [serving])
  const noAddress = st?.running && !st.ip
  return (
    <Dialog title={noAddress ? 'PXE server has no address' : 'Start the PXE server first'} onClose={onClose} footer={<button class="btn" onClick={onClose}>Cancel</button>}>
      {noAddress
        ? <p class="text-[13px]">No address on {st.interface}.</p>
        : <>
          <p class="text-[13px]">{what} needs the PXE server on this LAN; run it in a terminal:</p>
          <Code text={command ?? st?.command ?? 'sudo kubit pxe --iface en0'} />
        </>}
      <Notice tone="muted"><span class="inline-block h-1.5 w-1.5 rounded-full bg-warn animate-pulse mr-2" />{noAddress ? 'Waiting for an address' : 'Waiting for the PXE server'}</Notice>
    </Dialog>
  )
}

export function usePxeGated<T>(run: () => Promise<T>, onDone: (r: T) => void, onError: (msg: string) => void) {
  const [gate, setGate] = useState<{ what: string; command?: string } | null>(null)
  const attempt = (what: string) => run().then(onDone).catch((e) => { if (pxeBlocked(e)) setGate({ what, command: e.command }); else onError(e.message) })
  const element = gate ? <PxeGate what={gate.what} command={gate.command} onClose={() => setGate(null)} onReady={() => { const w = gate.what; setGate(null); attempt(w) }} /> : null
  return { attempt, element }
}
