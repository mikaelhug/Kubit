import { useState } from 'preact/hooks'
import { api, podLogsUrl, type Build, type DeployKey, type FluxObject, type SOPSKey } from '../../api'
import { Ago } from '../../components/Time'
import { LogStream } from '../../components/LogStream'
import { ConfirmDialog, CopyButton, Dialog, StatusDot } from '../../components/ui'
import { toast } from '../../store'
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

export function SOPSRow({ sops }: { sops: SOPSKey }) {
  return (
    <div class="flex flex-col gap-1 text-[12px] mt-1 pt-2 border-t border-border">
      <div class="flex items-center gap-2">
        <span class="text-muted shrink-0">SOPS recipient</span>
        <span class="ml-auto flex gap-1 shrink-0">
          <CopyButton text={sops.recipient} className="btn btn-sm" />
        </span>
      </div>
      <span class="mono truncate min-w-0" title={sops.recipient}>{sops.recipient}</span>
    </div>
  )
}

function githubKeysUrl(url: string) {
  try {
    const u = new URL(url)
    const path = u.pathname.replace(/^\/+|\.git$|\/+$/g, '')
    return u.hostname === 'github.com' && path.split('/').length === 2 ? `https://github.com/${path}/settings/keys/new` : undefined
  } catch {
    return undefined
  }
}

export function DeployKeyRow({ cluster, url, deployKey, onChange }: { cluster: string; url: string; deployKey: DeployKey; onChange: (k: DeployKey) => void }) {
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const github = githubKeysUrl(url)
  const hosts = deployKey.hosts ?? []
  const write = (hostsOnly: boolean) => {
    setBusy(true)
    return api.newDeployKey(cluster, deployKey.hash, hostsOnly)
      .then((k) => { setConfirm(false); onChange(k) })
      .catch((e) => toast(e.message, 'error'))
      .finally(() => setBusy(false))
  }
  return (
    <div class="flex flex-col gap-1 text-[12px] mt-1 pt-2 border-t border-border">
      <div class="flex items-center gap-2">
        <span class="text-muted shrink-0">Deploy key</span>
        <span class="ml-auto flex gap-1 shrink-0">
          {deployKey.publicKey && <CopyButton text={deployKey.publicKey} className="btn btn-sm" />}
          {deployKey.publicKey && github && <a href={github} target="_blank" rel="noreferrer" class="btn btn-sm">Add to GitHub ↗</a>}
          <button class="btn btn-sm" disabled={busy} onClick={() => deployKey.publicKey ? setConfirm(true) : write(false)}>{deployKey.publicKey ? 'Replace' : 'Generate'}</button>
        </span>
      </div>
      {deployKey.publicKey && <span class="mono truncate min-w-0" title={deployKey.fingerprint}>{deployKey.publicKey}</span>}
      {hosts.length > 0 && (
        <>
          <div class="flex items-center gap-2 mt-1">
            <span class="text-muted shrink-0">Host keys</span>
            <span class="mono text-muted truncate min-w-0">{hosts[0].host}</span>
            <button class="btn btn-sm ml-auto shrink-0" disabled={busy} onClick={() => write(true)}>Rescan</button>
          </div>
          {hosts.map((h) => <span key={h.type} class="mono truncate min-w-0" title={h.host}>{h.type} {h.fingerprint}</span>)}
        </>
      )}
      {confirm && <ConfirmDialog title="Replace deploy key" action="Replace" onClose={() => setConfirm(false)} onConfirm={() => write(false)} />}
    </div>
  )
}
