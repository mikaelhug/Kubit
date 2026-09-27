import type { ComponentChildren } from 'preact'
import { useCallback, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, fmt, type NodeRow } from '../api'
import { adoptHref, hostName, hostOf, kindDetail, kindLabel, kindTone, lastSeenOf, onMac, readyClusters, typeOf } from '../machine'
import { toast } from '../store'
import { ConfirmDialog, Dialog, Field, Pill } from './ui'

export function KindPill({ m }: { m: NodeRow }) {
  const detail = kindDetail(m)
  return <Pill tone={kindTone(m)} title={`state ${m.state}`}>{kindLabel[m.kind]}{detail && detail !== m.kind ? ` · ${detail}` : ''}</Pill>
}

export function TypePill({ m }: { m?: NodeRow | null }) {
  const form = typeOf(m)
  const title = { 'lab host': onMac(m?.labhost) ? 'This Mac, running Talos VMs' : 'KVM host Kubit installed', 'lab VM': `Talos VM on lab host ${hostName(hostOf(m)) || m?.host}`, VM: 'Virtual machine', metal: 'Bare metal' }[form]
  return <Pill tone={form === 'metal' ? 'muted' : 'info'} title={title}>{form}</Pill>
}

export function RetireDialog({ m, onClose, onDone }: { m: NodeRow; onClose: () => void; onDone: () => void }) {
  return <ConfirmDialog title={`Retire ${m.hostname || m.mac}`} action="Retire" tone="danger" onClose={onClose} onConfirm={() => api.retireMachine(m.mac).then(onDone).catch((e) => toast(e.message, 'error'))}
    impact={<p>Deletes the record for <span class="mono">{m.mac}</span>; a scan finds it again while it is online.</p>} />
}

export function identityRows(m: NodeRow): [string, ComponentChildren][] {
  return [
    ['Identity', <span class="mono text-[12px]">{m.mac}{m.uuid ? ` · ${m.uuid}` : ''}{m.serial ? ` · ${m.serial}` : ''}</span>],
    ['Addresses seen', <span class="mono text-[12px]">{[...new Set([...(m.ipsSeen ?? []), m.ip])].filter(Boolean).join(' → ') || '—'}</span>],
    ['Last seen', fmt.datetime(lastSeenOf(m))],
  ]
}

export function useAdopt() {
  const { route } = useLocation()
  const [target, setTarget] = useState<NodeRow | null>(null)
  const start = useCallback((m: NodeRow) => {
    const ready = readyClusters()
    if (ready.length === 0) route('/clusters/new')
    else if (ready.length === 1) route(adoptHref(ready[0].name, m))
    else setTarget(m)
  }, [route])
  const element = target && <AdoptDialog m={target} onClose={() => setTarget(null)} onPick={(c) => { setTarget(null); route(adoptHref(c, target)) }} />
  return { start, element }
}

function AdoptDialog({ m, onClose, onPick }: { m: NodeRow; onClose: () => void; onPick: (cluster: string) => void }) {
  const ready = readyClusters()
  const [cluster, setCluster] = useState(ready[0]?.name ?? '')
  return (
    <Dialog title={`Adopt ${m.hostname || m.ip}`} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={!cluster} onClick={() => onPick(cluster)}>Adopt</button></>}>
      <Field label="Cluster">
        <select class="input" value={cluster} onChange={(e) => setCluster((e.target as HTMLSelectElement).value)}>
          {ready.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
        </select>
      </Field>
    </Dialog>
  )
}
