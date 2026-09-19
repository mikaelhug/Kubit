import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, fmt, type AddonStatus } from '../../api'
import { operations, toast, watch, refreshKey } from '../../store'
import { Dialog, ErrorBox, Field, Notice, Pill, Section, StatusDot, type Tone } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

interface AddonDef { key: string; name: string; what: string; docs: string; link?: (ctx: ClusterCtx) => string | undefined; hint?: string }

const defs: AddonDef[] = [
  { key: 'metallb', name: 'MetalLB', what: 'Hands out LAN IPs to Services of type LoadBalancer and answers ARP for them (Layer 2).', docs: 'https://metallb.universe.tf/configuration/', hint: 'Values go to the metallb chart. The pool range is a first-class setting.' },
  { key: 'ingressNginx', name: 'Ingress-NGINX', what: 'HTTP(S) ingress controller; takes the first MetalLB IP and is the default IngressClass.', docs: 'https://github.com/kubernetes/ingress-nginx/blob/main/charts/ingress-nginx/values.yaml', link: (c) => c.status?.platform?.outputs?.ingress_ip ? `http://${c.status.platform.outputs.ingress_ip}` : undefined },
  { key: 'gvisor', name: 'gVisor', what: 'RuntimeClasses gvisor (runsc) and gvisor-kvm (runsc-kvm) for sandboxed pods; nodes are labelled by what they support.', docs: 'https://gvisor.dev/docs/user_guide/containerd/quick_start/', hint: 'Plain manifests; values are not used.' },
  { key: 'metricsServer', name: 'metrics-server', what: 'Pod and node CPU/memory usage for kubectl top, autoscaling and this UI.', docs: 'https://github.com/kubernetes-sigs/metrics-server/blob/master/charts/metrics-server/values.yaml', hint: 'Runs in kube-system with --kubelet-insecure-tls (Talos kubelets use self-signed serving certs).' },
  { key: 'certManager', name: 'cert-manager', what: 'Issues and renews TLS certificates for ingresses.', docs: 'https://cert-manager.io/docs/installation/helm/' },
  { key: 'longhorn', name: 'Longhorn', what: 'Replicated block storage on the nodes\' data disks: the default StorageClass, snapshots, backups to S3.', docs: 'https://longhorn.io/docs/latest/advanced-resources/deploy/customizing-default-settings/', hint: 'Replicas live on nodes with data disks (Nodes tab); replica count defaults to 3 or the number of such nodes. Talos gets the iscsi-tools and util-linux-tools extensions on the next upgrade.' },
  { key: 'argocd', name: 'ArgoCD', what: 'GitOps for your workloads; Kubit keeps managing the platform.', docs: 'https://github.com/argoproj/argo-helm/blob/main/charts/argo-cd/values.yaml', link: (c) => c.status?.platform?.outputs?.argocd_ip ? `http://${c.status.platform.outputs.argocd_ip}` : undefined, hint: 'Exposed on its own MetalLB IP (plain HTTP). The initial admin password is in secret argocd/argocd-initial-admin-secret.' },
]

const stateTone: Record<AddonStatus['state'], Tone> = { disabled: 'muted', pending: 'warn', deploying: 'warn', ready: 'good', degraded: 'warn', failed: 'bad', orphaned: 'warn' }
const stateText: Record<AddonStatus['state'], string> = { disabled: 'disabled', pending: 'enabled, not applied yet', deploying: 'deploying', ready: 'ready', degraded: 'degraded', failed: 'release failed', orphaned: 'disabled in cluster.yaml, still installed' }

export function Addons({ ctx }: { ctx: ClusterCtx }) {
  const { route } = useLocation()
  const { name, status, cluster } = ctx
  const [addons, setAddons] = useState<AddonStatus[]>([])
  const [error, setError] = useState<string | null>(null)
  const [edit, setEdit] = useState<AddonStatus | null>(null)
  const ops = [...operations.value.values()].filter((o) => o.cluster === name)
  const finished = ops.filter((o) => o.status !== 'running').length
  useEffect(() => { api.addons(name).then(setAddons).catch((e) => setError(e.message)) }, [name, finished, cluster.updatedAt, refreshKey(name, 'addons'), refreshKey(name, 'workloads')])
  const lastPlan = ops.filter((o) => o.kind === 'platform.plan' && o.status === 'done').sort((a, b) => b.id - a.id)[0]
  const planStale = lastPlan && cluster.updatedAt > lastPlan.startedAt
  const busy = ops.some((o) => o.status === 'running' && o.kind.startsWith('platform'))
  const drift = addons.some((a) => a.state === 'pending' || a.state === 'orphaned')
  const [pendingPlan, setPendingPlan] = useState<number | null>(null)
  const plan = () => api.platformPlan(name).then((r) => { watch(r, false); toast('Planning… the review opens when it finishes'); setPendingPlan(r.operationId) }).catch((e) => toast(e.message, 'error'))
  // The plan's completion arrives over SSE; open the review then.
  const pending = pendingPlan !== null ? operations.value.get(pendingPlan) : undefined
  useEffect(() => {
    if (!pending || pending.status === 'running') return
    setPendingPlan(null)
    if (pending.status === 'done') route(`/clusters/${name}/addons/${pending.id}`); else watch(pending.id)
  }, [pending?.status]) // eslint-disable-line

  return (
    <>
      <Section title="Platform add-ons"
        help="Configure changes the declaration, Plan shows what would change, Apply executes the reviewed plan."
        actions={
          <>
            <button class="btn btn-primary" disabled={busy} onClick={plan}>{busy ? 'Working…' : 'Plan changes'}</button>
            {lastPlan && <a href={`/clusters/${name}/addons/${lastPlan.id}`} class="btn">Last plan #{lastPlan.id}{planStale ? ' (stale)' : ''}</a>}
          </>
        }>
        <ErrorBox error={error} />
        {status?.platform?.error && <Notice tone="bad">Last apply failed: {status.platform.error}</Notice>}
        {drift && <Notice tone="warn">cluster.yaml differs from what is installed. Plan to review the change.</Notice>}
        {!drift && status?.platform?.appliedAt && <Notice tone="muted">In sync with cluster.yaml; last applied {fmt.datetime(status.platform.appliedAt)}.</Notice>}
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
          {defs.map((d) => {
            const a = addons.find((x) => x.key === d.key)
            const link = d.link?.(ctx)
            const st = a?.state ?? 'disabled'
            return (
              <div key={d.key} class={`panel p-4 flex gap-3 ${st === 'disabled' ? 'opacity-75' : ''}`}>
                <div class="pt-1"><StatusDot tone={stateTone[st]} pulse={st === 'deploying'} /></div>
                <div class="flex flex-col gap-1.5 min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="font-medium">{d.name}</span>
                    <Pill tone={stateTone[st]}>{stateText[st]}</Pill>
                    <span class="ml-auto flex gap-1">
                      {link && <a href={link} target="_blank" rel="noreferrer" class="btn !py-0.5 !px-2 text-[12px]">Open ↗</a>}
                      <button class="btn !py-0.5 !px-2 text-[12px]" disabled={!a} onClick={() => a && setEdit(a)}>Configure</button>
                    </span>
                  </div>
                  <p class="text-[12.5px] text-muted">{d.what}</p>
                  {a && (a.release || a.readiness || a.key === 'metallb') && (
                    <div class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 text-[12px] mt-1">
                      {a.release && <><span class="text-muted">Installed</span><span class="mono">{a.release.chart} {a.release.chartVersion}{a.release.appVersion ? ` (app ${a.release.appVersion})` : ''}{a.pinnedVersion && a.pinnedVersion !== a.release.chartVersion && <span class="text-warn"> · pinned {a.pinnedVersion}</span>}</span></>}
                      {a.release && <><span class="text-muted">Helm status</span><span class={a.release.status === 'deployed' ? '' : 'text-bad'}>{a.release.status}{a.release.lastDeployed ? ` · ${fmt.when(new Date(a.release.lastDeployed * 1000).toISOString())}` : ''}</span></>}
                      {a.readiness && <><span class="text-muted">Workloads</span><span class={a.readiness.ready === a.readiness.total ? '' : 'text-warn'}>{a.readiness.ready}/{a.readiness.total} available in {a.readiness.namespace}{a.readiness.detail?.length ? ` — ${a.readiness.detail.join(', ')}` : ''}</span></>}
                      {a.key === 'metallb' && <><span class="text-muted">Pool</span><span class="mono">{cluster.spec.spec.platform.metallb.range || '—'}</span></>}
                      {a.values && Object.keys(a.values).length > 0 && <><span class="text-muted">Values</span><span class="mono truncate" title={JSON.stringify(a.values)}>{Object.keys(a.values).join(', ')} overridden</span></>}
                    </div>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      </Section>
      {edit && <ConfigureDialog ctx={ctx} addon={edit} def={defs.find((d) => d.key === edit.key)!} onClose={() => setEdit(null)} onSaved={(list) => { setAddons(list); setEdit(null) }} />}
    </>
  )
}

function ConfigureDialog({ ctx, addon, def, onClose, onSaved }: { ctx: ClusterCtx; addon: AddonStatus; def: AddonDef; onClose: () => void; onSaved: (list: AddonStatus[]) => void }) {
  const [enabled, setEnabled] = useState(addon.enabled)
  const [range, setRange] = useState(ctx.cluster.spec.spec.platform.metallb.range ?? '')
  const [values, setValues] = useState(toYaml(addon.values ?? {}))
  const [error, setError] = useState<string | null>(null)
  const save = () => api.updateAddon(ctx.name, addon.key, { enabled, range: addon.key === 'metallb' ? range : undefined, valuesYaml: def.key === 'gvisor' ? undefined : values })
    .then((list) => { toast('Saved to cluster.yaml. Plan to review the change.', 'good'); onSaved(list) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Configure ${def.name}`} onClose={onClose} width="max-w-2xl" footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" onClick={save}>Save to cluster.yaml</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">{def.what} {def.hint}</p>
      <label class="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={enabled} onChange={(e) => setEnabled((e.target as HTMLInputElement).checked)} /> Enabled {addon.release && !enabled && <span class="text-warn">— disabling removes the installed release on the next apply</span>}</label>
      {addon.key === 'metallb' && <Field label="Address pool" hint="start-end, inside your LAN and outside DHCP's range; every LoadBalancer service takes one address."><input class="input mono" value={range} onInput={(e) => setRange((e.target as HTMLInputElement).value)} /></Field>}
      {def.key !== 'gvisor' && (
        <Field label="Helm values (YAML)" hint={<>Merged over Kubit's defaults. <a class="underline" href={def.docs} target="_blank" rel="noreferrer">Chart values reference ↗</a></> as any}>
          <textarea class="input mono !text-[12px] h-52" value={values} spellcheck={false} onInput={(e) => setValues((e.target as HTMLTextAreaElement).value)} placeholder={'# e.g.\nreplicaCount: 2'} />
        </Field>
      )}
    </Dialog>
  )
}

/** Small YAML emitter for the values editor (objects, arrays, scalars); the server re-parses it. */
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
