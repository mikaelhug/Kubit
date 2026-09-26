import { useEffect, useState } from 'preact/hooks'
import { api, type PxeStatus } from '../api'
import { refreshKey } from '../store'
import { Code, Dialog, Notice } from './ui'

export function PxeGate({ what, command, onReady, onClose }: { what: string; command?: string; onReady: () => void; onClose: () => void }) {
  const [st, setSt] = useState<PxeStatus | null>(null)
  const check = () => api.pxe().then(setSt).catch(() => {})
  useEffect(() => { check() }, [refreshKey('', 'pxe')])
  useEffect(() => { if (st?.running) onReady() }, [st?.running])
  return (
    <Dialog title="Start the PXE server first" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn" onClick={check}>Check again</button></>}>
      <p class="text-[13px]">{what} needs the PXE server on this LAN. Run it in a terminal and keep it open:</p>
      <Code text={command ?? st?.command ?? 'sudo kubit pxe --iface en0'} />
      <Notice tone="muted"><span class="inline-block h-1.5 w-1.5 rounded-full bg-warn animate-pulse mr-2" />Waiting — this continues automatically.</Notice>
    </Dialog>
  )
}

export function usePxeGated<T>(run: () => Promise<T>, onDone: (r: T) => void, onError: (msg: string) => void) {
  const [gate, setGate] = useState<{ what: string; command?: string } | null>(null)
  const attempt = (what: string) => run().then(onDone).catch((e) => { if (e?.code === 'pxe-down') setGate({ what, command: e.command }); else onError(e.message) })
  const element = gate ? <PxeGate what={gate.what} command={gate.command} onClose={() => setGate(null)} onReady={() => { const w = gate.what; setGate(null); attempt(w) }} /> : null
  return { attempt, element }
}
