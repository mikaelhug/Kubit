import { useLocation } from 'preact-iso'
import { api, fmt, type PlatformSpec } from '../../api'
import { operations, toast, watch } from '../../store'
import { Notice, Pill, Section, StatusDot } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

interface AddonDef { key: keyof PlatformSpec; name: string; what: string; detail?: (ctx: ClusterCtx) => string | undefined; link?: (ctx: ClusterCtx) => string | undefined }

const addons: AddonDef[] = [
  { key: 'metallb', name: 'MetalLB', what: 'Hands out LAN IPs to Services of type LoadBalancer and answers ARP for them (Layer 2).', detail: (c) => c.cluster.spec.spec.platform.metallb.range },
  { key: 'ingressNginx', name: 'Ingress-NGINX', what: 'HTTP(S) ingress controller; takes the first MetalLB IP.', detail: (c) => c.status?.platform?.outputs?.ingress_ip ? `http://${c.status.platform.outputs.ingress_ip}` : undefined, link: (c) => c.status?.platform?.outputs?.ingress_ip ? `http://${c.status.platform.outputs.ingress_ip}` : undefined },
  { key: 'gvisor', name: 'gVisor', what: 'RuntimeClasses gvisor (runsc) and gvisor-kvm (runsc-kvm) for sandboxed pods; nodes are labelled by what they support.', detail: (c) => `${c.status?.nodes.filter((n) => n.gvisor).length ?? 0}/${c.status?.nodes.length ?? 0} nodes` },
  { key: 'metricsServer', name: 'metrics-server', what: 'Pod and node CPU/memory usage for kubectl top, autoscaling and this UI.' },
  { key: 'certManager', name: 'cert-manager', what: 'Issues and renews TLS certificates for ingresses.' },
  { key: 'argocd', name: 'ArgoCD', what: 'GitOps for your workloads; Kubit keeps managing the platform.', detail: (c) => c.status?.platform?.outputs?.argocd_ip ? `http://${c.status.platform.outputs.argocd_ip}` : undefined, link: (c) => c.status?.platform?.outputs?.argocd_ip ? `http://${c.status.platform.outputs.argocd_ip}` : undefined },
]

export function Addons({ ctx }: { ctx: ClusterCtx }) {
  const { route } = useLocation()
  const { cluster, name, status } = ctx
  const platform = cluster.spec.spec.platform
  const ops = [...operations.value.values()].filter((o) => o.cluster === name)
  const lastPlan = ops.filter((o) => o.kind === 'platform.plan' && o.status === 'done').sort((a, b) => b.id - a.id)[0]
  const planStale = lastPlan && cluster.updatedAt > lastPlan.startedAt
  const busy = ops.some((o) => o.status === 'running' && o.kind.startsWith('platform'))
  const plan = () => api.platformPlan(name).then((r) => { watch(r, false); toast('Planning… the review opens when it finishes'); waitAndOpen(r.operationId) }).catch((e) => toast(e.message, 'error'))
  const waitAndOpen = (id: number) => {
    const t = setInterval(() => {
      const o = operations.value.get(id)
      if (o && o.status !== 'running') { clearInterval(t); if (o.status === 'done') route(`/clusters/${name}/addons/${id}`); else watch(id) }
    }, 500)
  }

  return (
    <>
      <Section title="Platform add-ons"
        help="Kubit converges these with OpenTofu from the platform section of cluster.yaml. Plan renders what would change and lets you review it; Apply executes exactly the plan you reviewed."
        actions={
          <>
            <button class="btn btn-primary" disabled={busy} onClick={plan}>{busy ? 'Working…' : 'Plan changes'}</button>
            {lastPlan && <a href={`/clusters/${name}/addons/${lastPlan.id}`} class="btn">Last plan #{lastPlan.id}{planStale ? ' (stale)' : ''}</a>}
          </>
        }>
        {status?.platform?.error && <Notice tone="bad">Last apply failed: {status.platform.error}</Notice>}
        {status?.platform?.appliedAt && !status.platform.error && <Notice tone="muted">Last applied {fmt.datetime(status.platform.appliedAt)}. Enable or disable add-ons in Settings → cluster.yaml, then Plan.</Notice>}
        {!status?.platform?.appliedAt && !status?.platform?.error && <Notice tone="warn">The platform layer has not been applied to this cluster yet.</Notice>}
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
          {addons.map((a) => {
            const on = (platform[a.key] as { enabled: boolean }).enabled
            const detail = a.detail?.(ctx)
            const link = a.link?.(ctx)
            return (
              <div key={a.key} class={`panel p-4 flex gap-3 ${on ? '' : 'opacity-70'}`}>
                <StatusDot tone={on ? (status?.platform?.appliedAt ? 'good' : 'warn') : 'muted'} />
                <div class="flex flex-col gap-1 min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="font-medium">{a.name}</span>
                    <Pill tone={on ? 'good' : 'muted'}>{on ? 'enabled' : 'disabled'}</Pill>
                    {link && <a href={link} target="_blank" rel="noreferrer" class="ml-auto text-accent text-[12px] hover:underline">Open ↗</a>}
                  </div>
                  <p class="text-[12.5px] text-muted">{a.what}</p>
                  {on && detail && <span class="mono text-[12px] truncate" title={detail}>{detail}</span>}
                </div>
              </div>
            )
          })}
        </div>
      </Section>
    </>
  )
}
