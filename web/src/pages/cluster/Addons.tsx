import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, fmt, podLogsUrl, type AddonStatus, type Build, type FluxObject, type SOPSKey } from '../../api'
import { Ago } from '../../components/Time'
import { CopyButton, Dialog, ErrorBox, Field, Notice, Pill, Section, StatusDot } from '../../components/ui'
import { can, operations, opsFor, toast, watch } from '../../store'
import { stateTone, type Tone } from '../../tone'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'
import { useImageStatus } from './Lifecycle'

interface AddonDef { key: string; name: string; what: string; docs: string; link?: (ctx: ClusterCtx) => string | undefined; hint?: string }

const defs: AddonDef[] = [
  { key: 'metallb', name: 'MetalLB', what: 'LAN addresses for LoadBalancer services, announced over ARP.', docs: 'https://metallb.universe.tf/configuration/', hint: 'The pool range is a setting of its own.' },
  { key: 'ingressNginx', name: 'Ingress-NGINX', what: 'HTTP(S) ingress controller on the first MetalLB address; the default IngressClass.', docs: 'https://github.com/kubernetes/ingress-nginx/blob/main/charts/ingress-nginx/values.yaml', link: (c) => c.status?.platform?.outputs?.ingress_ip ? `http://${c.status.platform.outputs.ingress_ip}` : undefined },
  { key: 'gvisor', name: 'gVisor', what: 'RuntimeClasses gvisor and gvisor-kvm for sandboxed pods.', docs: 'https://gvisor.dev/docs/user_guide/containerd/quick_start/', hint: 'Plain manifests; no values.' },
  { key: 'metricsServer', name: 'metrics-server', what: 'Pod and node CPU and memory usage.', docs: 'https://github.com/kubernetes-sigs/metrics-server/blob/master/charts/metrics-server/values.yaml' },
  { key: 'certManager', name: 'cert-manager', what: 'Issues and renews TLS certificates for ingresses.', docs: 'https://cert-manager.io/docs/installation/helm/' },
  { key: 'longhorn', name: 'Longhorn', what: 'Replicated block storage and the default StorageClass.', docs: 'https://longhorn.io/docs/latest/advanced-resources/deploy/customizing-default-settings/' },
  { key: 'builds', name: 'Builds', what: 'Builds images from the apps repository into a private registry.', docs: 'https://github.com/moby/buildkit', hint: 'Pulled as registry.kubit/<app>:<version>.' },
  { key: 'flux', name: 'Flux', what: 'GitOps sync from a Git repository.', docs: 'https://github.com/fluxcd-community/helm-charts/blob/main/charts/flux2/values.yaml' },
]

const noValues = new Set(['gvisor', 'builds'])

const stateText: Record<AddonStatus['state'], string> = { disabled: 'disabled', pending: 'enabled, not applied yet', deploying: 'deploying', ready: 'ready', degraded: 'degraded', failed: 'release failed', orphaned: 'disabled in cluster.yaml, still installed' }

export function Addons({ ctx }: { ctx: ClusterCtx }) {
  const { route } = useLocation()
  const { name, status, cluster } = ctx
  const { data: addons, error, set: setAddons } = useLive(() => api.addons(name), [name, cluster.updatedAt], [[name, 'addons']])
  const [edit, setEdit] = useState<AddonStatus | null>(null)
  const ops = opsFor(name)
  const lastPlan = ops.filter((o) => o.kind === 'platform.plan' && o.status === 'done').sort((a, b) => b.id - a.id)[0]
  const planStale = lastPlan && cluster.updatedAt > lastPlan.startedAt
  const busy = ops.some((o) => o.status === 'running' && o.kind.startsWith('platform'))
  const drift = (addons ?? []).some((a) => a.state === 'pending' || a.state === 'orphaned')
  const image = useImageStatus(name, cluster.updatedAt)
  const longhornWaits = image?.outdated && cluster.spec.spec.platform.longhorn?.enabled
  const flux = !!cluster.spec.spec.platform.flux?.enabled
  const repo = cluster.spec.spec.platform.flux?.repository
  const builds = !!cluster.spec.spec.platform.builds?.enabled
  const { data: sops, set: setSops } = useLive(() => api.sopsKey(name), [name], [[name, 'sops']], { onError: 'null', enabled: flux })
  const { data: sync } = useLive(() => api.flux(name), [name], [[name, 'flux']], { onError: 'null', enabled: flux })
  const { data: buildList } = useLive(() => api.builds(name), [name], [[name, 'workloads']], { onError: 'null', enabled: builds })
  const [importing, setImporting] = useState(false)
  const [pendingPlan, setPendingPlan] = useState<number | null>(null)
  const plan = () => api.platformPlan(name).then((r) => { watch(r, false); toast('Planning; the review opens when done'); setPendingPlan(r.operationId) }).catch((e) => toast(e.message, 'error'))
  const pending = pendingPlan !== null ? operations.value.get(pendingPlan) : undefined
  useEffect(() => {
    if (!pending || pending.status === 'running') return
    setPendingPlan(null)
    if (pending.status === 'done') route(`/clusters/${name}/addons/${pending.id}`); else watch(pending.id)
  }, [pending?.status])

  return (
    <>
      <Section title="Platform add-ons"
        help="Configure edits cluster.yaml, Plan shows the change, Apply runs the reviewed plan."
        actions={
          <>
            <button class="btn btn-primary" disabled={busy} onClick={plan}>{busy ? 'Working' : 'Plan changes'}</button>
            {lastPlan && <a href={`/clusters/${name}/addons/${lastPlan.id}`} class="btn">Last plan #{lastPlan.id}{planStale ? ' (stale)' : ''}</a>}
          </>
        }>
        <ErrorBox error={error} />
        {status?.platform?.error && <Notice tone="bad">Last apply failed: {status.platform.error}</Notice>}
        {longhornWaits && <Notice tone="warn"><span class="flex items-center gap-2">Longhorn needs Talos extensions the nodes lack; upgrade Talos first.<a href={`/clusters/${name}/lifecycle`} class="ml-auto text-accent hover:underline text-[12px] shrink-0">Lifecycle →</a></span></Notice>}
        {drift && <Notice tone="warn">cluster.yaml differs from what is installed; plan to review.</Notice>}
        {!drift && status?.platform?.appliedAt && <Notice tone="muted">In sync with cluster.yaml; last applied {fmt.datetime(status.platform.appliedAt)}.</Notice>}
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
          {defs.map((d) => {
            const a = addons?.find((x) => x.key === d.key)
            const link = d.link?.(ctx)
            const st = a?.state ?? (addons ? 'disabled' : null)
            return (
              <div key={d.key} class={`panel p-4 flex gap-3 ${st === 'disabled' ? 'opacity-75' : ''}`}>
                <div class="pt-1"><StatusDot tone={st ? stateTone(st) : 'muted'} pulse={st === 'deploying'} /></div>
                <div class="flex flex-col gap-1.5 min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="font-medium">{d.name}</span>
                    {st && <Pill tone={stateTone(st)}>{stateText[st]}</Pill>}
                    <span class="ml-auto flex gap-1">
                      {link && <a href={link} target="_blank" rel="noreferrer" class="btn btn-sm">Open ↗</a>}
                      <button class="btn btn-sm" disabled={!a} onClick={() => a && setEdit(a)}>Configure</button>
                    </span>
                  </div>
                  <p class="text-[12.5px] text-muted">{d.what}</p>
                  {a && (a.release || a.readiness || a.key === 'metallb' || a.key === 'flux' || a.key === 'builds') && (
                    <div class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 text-[12px] mt-1">
                      {a.release && <><span class="text-muted">Installed</span><span class="mono">{a.release.chart} {a.release.chartVersion}{a.release.appVersion ? ` (app ${a.release.appVersion})` : ''}{a.pinnedVersion && a.pinnedVersion !== a.release.chartVersion && <span class="text-warn"> · pinned {a.pinnedVersion}</span>}</span></>}
                      {a.release && <><span class="text-muted">Helm status</span><span class={a.release.status === 'deployed' ? '' : 'text-bad'}>{a.release.status}{a.release.lastDeployed ? ` · ${fmt.when(new Date(a.release.lastDeployed * 1000).toISOString())}` : ''}</span></>}
                      {a.readiness && <><span class="text-muted">Workloads</span><span class={a.readiness.ready === a.readiness.total ? '' : 'text-warn'}>{a.readiness.ready}/{a.readiness.total} available in {a.readiness.namespace}{a.readiness.detail?.length ? ` — ${a.readiness.detail.join(', ')}` : ''}</span></>}
                      {a.key === 'metallb' && <><span class="text-muted">Pool</span><span class="mono">{cluster.spec.spec.platform.metallb.range || '—'}</span></>}
                      {a.key === 'builds' && <><span class="text-muted">Registry</span><span class="mono">registry.kubit → {a.address ?? '—'}</span></>}
                      {a.key === 'flux' && <><span class="text-muted">Repository</span><span class="mono truncate" title={repo?.url}>{repo ? `${repo.url} @ ${repo.branch} · ${repo.path}` : 'not set'}</span></>}
                      {a.values && Object.keys(a.values).length > 0 && <><span class="text-muted">Values</span><span class="mono truncate" title={JSON.stringify(a.values)}>{Object.keys(a.values).join(', ')} overridden</span></>}
                    </div>
                  )}
                  {a?.key === 'flux' && sync && sync.length > 0 && <FluxSync objects={sync} />}
                  {a?.key === 'builds' && buildList && buildList.length > 0 && <BuildList cluster={name} builds={buildList} />}
                  {a?.key === 'flux' && sops && <SOPSRow cluster={name} sops={sops} onImport={() => setImporting(true)} />}
                </div>
              </div>
            )
          })}
        </div>
      </Section>
      {importing && <ImportKeyDialog cluster={name} onClose={() => setImporting(false)} onDone={(k) => { setSops(k); setImporting(false) }} />}
      {edit && <ConfigureDialog ctx={ctx} addon={edit} def={defs.find((d) => d.key === edit.key)!} onClose={() => setEdit(null)} onSaved={(list) => { setAddons(list); setEdit(null) }} />}
    </>
  )
}

const readyTone = (o: FluxObject): Tone => o.suspended ? 'muted' : o.ready === 'True' ? 'good' : o.ready === 'False' ? 'bad' : 'warn'
const shortRevision = (r?: string) => r?.replace(/(^|@)sha\d+:([0-9a-f]{7})[0-9a-f]*/, '$1$2') ?? ''

function FluxSync({ objects }: { objects: FluxObject[] }) {
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

function BuildList({ cluster, builds }: { cluster: string; builds: Build[] }) {
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

function SOPSRow({ cluster, sops, onImport }: { cluster: string; sops: SOPSKey; onImport: () => void }) {
  return (
    <div class="flex flex-col gap-1 text-[12px] mt-1 pt-2 border-t border-border">
      <div class="flex items-center gap-2">
        <span class="text-muted shrink-0" title="Encrypt SOPS files for this age recipient">SOPS recipient</span>
        <span class="ml-auto flex gap-1 shrink-0">
          <CopyButton text={sops.recipient} className="btn btn-sm" />
          {can('admin') && <a class="btn btn-sm" href={`/api/v1/clusters/${cluster}/sops/identity`} download={`${cluster}-age.txt`}>Export key</a>}
          {can('admin') && <button class="btn btn-sm" onClick={onImport}>Import key</button>}
        </span>
      </div>
      <span class="mono truncate min-w-0" title={sops.recipient}>{sops.recipient}</span>
    </div>
  )
}

function ImportKeyDialog({ cluster, onClose, onDone }: { cluster: string; onClose: () => void; onDone: (k: SOPSKey) => void }) {
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

function ConfigureDialog({ ctx, addon, def, onClose, onSaved }: { ctx: ClusterCtx; addon: AddonStatus; def: AddonDef; onClose: () => void; onSaved: (list: AddonStatus[]) => void }) {
  const [enabled, setEnabled] = useState(addon.enabled)
  const [range, setRange] = useState(ctx.cluster.spec.spec.platform.metallb.range ?? '')
  const [repo, setRepo] = useState({ url: '', branch: '', path: '', interval: '', ...ctx.cluster.spec.spec.platform.flux?.repository })
  const [values, setValues] = useState(toYaml(addon.values ?? {}))
  const [error, setError] = useState<string | null>(null)
  const save = () => api.updateAddon(ctx.name, addon.key, { enabled, range: addon.key === 'metallb' ? range : undefined, repository: addon.key === 'flux' ? repo : undefined, valuesYaml: noValues.has(def.key) ? undefined : values })
    .then((list) => { toast('Saved to cluster.yaml; plan to review', 'good'); onSaved(list) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Configure ${def.name}`} onClose={onClose} width="max-w-2xl" footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" onClick={save}>Save to cluster.yaml</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">{def.what}{def.hint ? ` ${def.hint}` : ''}</p>
      <label class="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={enabled} onChange={(e) => setEnabled((e.target as HTMLInputElement).checked)} /> Enabled {addon.release && !enabled && <span class="text-warn">{addon.key === 'flux' ? '— removes Flux on the next apply; running apps stay' : '— removes the release on the next apply'}</span>}</label>
      {addon.key === 'metallb' && <Field label="Address pool" hint="start-end, on the LAN and outside the DHCP range"><input class="input mono" value={range} onInput={(e) => setRange((e.target as HTMLInputElement).value)} /></Field>}
      {addon.key === 'flux' && (
        <>
          <Field label="Repository" hint="Public HTTPS Git URL; empty installs Flux without a sync"><input class="input mono" value={repo.url} placeholder="https://github.com/you/apps.git" onInput={(e) => setRepo({ ...repo, url: (e.target as HTMLInputElement).value })} /></Field>
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <Field label="Branch"><input class="input mono" value={repo.branch} placeholder="main" onInput={(e) => setRepo({ ...repo, branch: (e.target as HTMLInputElement).value })} /></Field>
            <Field label="Path"><input class="input mono" value={repo.path} placeholder="./" onInput={(e) => setRepo({ ...repo, path: (e.target as HTMLInputElement).value })} /></Field>
            <Field label="Interval"><input class="input mono" value={repo.interval} placeholder="5m" onInput={(e) => setRepo({ ...repo, interval: (e.target as HTMLInputElement).value })} /></Field>
          </div>
        </>
      )}
      {!noValues.has(def.key) && (
        <Field label="Helm values (YAML)" hint={<>Merged over Kubit's defaults. <a class="underline" href={def.docs} target="_blank" rel="noreferrer">Chart values ↗</a></>}>
          <textarea class="input mono !text-[12px] h-52" value={values} spellcheck={false} onInput={(e) => setValues((e.target as HTMLTextAreaElement).value)} />
        </Field>
      )}
    </Dialog>
  )
}

function toYaml(v: unknown, indent = 0): string {
  const pad = '  '.repeat(indent)
  if (Array.isArray(v)) return v.map((x) => typeof x === 'object' && x !== null ? `${pad}-\n${toYaml(x, indent + 1)}` : `${pad}- ${scalar(x)}`).join('\n')
  if (typeof v === 'object' && v !== null) {
    const entries = Object.entries(v as Record<string, unknown>)
    if (entries.length === 0) return ''
    return entries.map(([k, x]) => typeof x === 'object' && x !== null && Object.keys(x as object).length > 0 ? `${pad}${k}:\n${toYaml(x, indent + 1)}` : `${pad}${k}: ${typeof x === 'object' && x !== null ? (Array.isArray(x) ? '[]' : '{}') : scalar(x)}`).join('\n')
  }
  return pad + scalar(v)
}
function scalar(x: unknown) { return typeof x === 'string' ? (/^[\w./:-]+$/.test(x) && !/^(true|false|null|\d+)$/.test(x) ? x : JSON.stringify(x)) : String(x) }
