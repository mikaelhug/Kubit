import { useState } from 'preact/hooks'
import { api, authedUrl, podLogsUrl, type Build, type FluxObject, type SOPSKey } from '../../api'
import { Ago } from '../../components/Time'
import { CopyButton, Dialog, ErrorBox, Field, StatusDot } from '../../components/ui'
import { can, toast } from '../../store'
import { stateTone, type Tone } from '../../tone'

const readyTone = (o: FluxObject): Tone => o.suspended ? 'muted' : o.ready === 'True' ? 'good' : o.ready === 'False' ? 'bad' : 'warn'
const shortRevision = (r?: string) => r?.replace(/(^|@)sha\d+:([0-9a-f]{7})[0-9a-f]*/, '$1$2') ?? ''

export function FluxSync({ objects }: { objects: FluxObject[] }) {
  return (
    <div class="flex flex-col gap-1 text-[12px] mt-1 pt-2 border-t border-border">
      {objects.map((o) => (
        <div key={`${o.kind}/${o.namespace}/${o.name}`} class="flex flex-col min-w-0">
          <div class="flex items-center gap-2 min-w-0">
            <span class="inline-flex shrink-0"><StatusDot tone={readyTone(o)} pulse={o.ready === 'Unknown' && !o.suspended} /></span>
            <span class="text-muted shrink-0">{o.kind}</span>
            <span class="mono truncate min-w-0" title={`${o.namespace}/${o.name}`}>{o.namespace === 'flux-system' ? o.name : `${o.namespace}/${o.name}`}</span>
            <span class="ml-auto text-muted shrink-0" title={o.reason}>{o.suspended ? 'suspended' : <Ago iso={o.since} />}</span>
          </div>
          <span class={`mono truncate pl-4 ${o.ready === 'False' ? 'text-bad' : 'text-muted'}`} title={o.message || o.revision}>{o.ready === 'False' ? o.message : shortRevision(o.revision)}</span>
        </div>
      ))}
    </div>
  )
}

export function BuildList({ cluster, builds }: { cluster: string; builds: Build[] }) {
  return (
    <div class="flex flex-col gap-1 text-[12px] mt-1 pt-2 border-t border-border">
      {builds.slice(0, 8).map((b) => (
        <div key={b.name} class="flex flex-col min-w-0">
          <div class="flex items-center gap-2 min-w-0">
            <span class="inline-flex shrink-0"><StatusDot tone={stateTone(b.state)} pulse={b.state === 'running'} /></span>
            <span class="mono truncate min-w-0">{b.name}</span>
            <span class="ml-auto text-muted shrink-0">{b.state === 'running' ? <>building, <Ago iso={b.startedAt} bare /></> : <>{b.state} <Ago iso={b.finishedAt ?? b.startedAt} /></>}</span>
            {b.pod && <a class="text-accent hover:underline shrink-0" href={podLogsUrl(cluster, 'kubit-builds', b.pod, '', false)} target="_blank" rel="noreferrer">Logs</a>}
          </div>
          {b.image && <span class="mono truncate pl-4 text-muted" title={b.image}>{b.image.replace(/^[^/]+\//, 'registry.kubit/')}</span>}
        </div>
      ))}
    </div>
  )
}

export function SOPSRow({ cluster, sops, onImport }: { cluster: string; sops: SOPSKey; onImport: () => void }) {
  return (
    <div class="flex flex-col gap-1 text-[12px] mt-1 pt-2 border-t border-border">
      <div class="flex items-center gap-2">
        <span class="text-muted shrink-0" title="Encrypt SOPS files for this age recipient">SOPS recipient</span>
        <span class="ml-auto flex gap-1 shrink-0">
          <CopyButton text={sops.recipient} className="btn btn-sm" />
          {can('admin') && <a class="btn btn-sm" href={authedUrl(`/clusters/${cluster}/sops/identity`)} download={`${cluster}-age.txt`}>Export key</a>}
          {can('admin') && <button class="btn btn-sm" onClick={onImport}>Import key</button>}
        </span>
      </div>
      <span class="mono truncate min-w-0" title={sops.recipient}>{sops.recipient}</span>
    </div>
  )
}

export function ImportKeyDialog({ cluster, onClose, onDone }: { cluster: string; onClose: () => void; onDone: (k: SOPSKey) => void }) {
  const [keys, setKeys] = useState('')
  const [error, setError] = useState<string | null>(null)
  const save = () => api.importSOPSKey(cluster, keys).then((k) => { toast('Key imported; apply the platform to install it', 'good'); onDone(k) }).catch((e) => setError(e.message))
  return (
    <Dialog title="Import SOPS key" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-danger" disabled={!keys.trim()} onClick={save}>Replace key</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">Replaces this cluster's key on the next apply; files encrypted only for the old key stop decrypting.</p>
      <Field label="age key file"><textarea class="input mono !text-[12px] h-28" value={keys} spellcheck={false} autocomplete="off" onInput={(e) => setKeys((e.target as HTMLTextAreaElement).value)} placeholder="AGE-SECRET-KEY-1" /></Field>
    </Dialog>
  )
}
