import { useState } from 'preact/hooks'
import { podLogsUrl, type Build, type FluxObject } from '../../api'
import { Ago } from '../../components/Time'
import { LogStream } from '../../components/LogStream'
import { Dialog, StatusDot } from '../../components/ui'
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
  const [logs, setLogs] = useState<Build | null>(null)
  const [follow, setFollow] = useState(true)
  return (
    <div class="flex flex-col gap-1 text-[12px] mt-1 pt-2 border-t border-border">
      {logs?.pod && (
        <Dialog title={`Build ${logs.name}`} width="max-w-5xl" onClose={() => setLogs(null)}>
          <LogStream url={podLogsUrl(cluster, 'kubit-builds', logs.pod, '', follow)} follow={follow} onFollow={setFollow} />
        </Dialog>
      )}
      {builds.slice(0, 8).map((b) => (
        <div key={b.name} class="flex flex-col min-w-0">
          <div class="flex items-center gap-2 min-w-0">
            <span class="inline-flex shrink-0"><StatusDot tone={stateTone(b.state)} pulse={b.state === 'running'} /></span>
            <span class="mono truncate min-w-0">{b.name}</span>
            <span class="ml-auto text-muted shrink-0">{b.state === 'running' ? <>building, <Ago iso={b.startedAt} bare /></> : <>{b.state} <Ago iso={b.finishedAt ?? b.startedAt} /></>}</span>
            {b.pod && <button class="text-accent hover:underline shrink-0" onClick={() => { setFollow(b.state === 'running'); setLogs(b) }}>Logs</button>}
          </div>
          {b.image && <span class="mono truncate pl-4 text-muted" title={b.image}>{b.image.replace(/^[^/]+\//, 'registry.kubit/')}</span>}
        </div>
      ))}
    </div>
  )
}
