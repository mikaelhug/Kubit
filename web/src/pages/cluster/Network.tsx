import { useEffect, useState } from 'preact/hooks'
import { api, type KIngress, type KService, type NetworkView } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { AlertPill, ErrorBox, KeyValue, Notice, Pill, Section } from '../../components/ui'
import { openAlert, refreshKey } from '../../store'
import type { ClusterCtx } from './ClusterPage'

export function Network({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  const [view, setView] = useState<NetworkView | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    api.network(name).then((v) => { setView(v); setError(null) }).catch((e) => setError(e.message))
  }, [name, refreshKey(name, 'network'), refreshKey(name, 'addons')])
  const pool = view?.pool
  const scols: Column<KService>[] = [
    { id: 'ns', header: 'Namespace', sort: (s) => s.namespace, cell: (s) => s.namespace },
    { id: 'name', header: 'Service', sort: (s) => s.name, cell: (s) => <span class="flex items-center gap-2"><span class="font-medium">{s.name}</span><AlertPill e={openAlert(name, 'Service', s.namespace, s.name)} /></span> },
    { id: 'type', header: 'Type', sort: (s) => s.type, cell: (s) => <Pill tone={s.type === 'LoadBalancer' ? 'info' : 'muted'}>{s.type}</Pill> },
    { id: 'cip', header: 'Cluster IP', mono: true, cell: (s) => s.clusterIP },
    { id: 'ext', header: 'External IP', mono: true, sort: (s) => (s.externalIPs ?? []).join(','), cell: (s) => (s.externalIPs ?? []).join(', ') || <span class="text-muted">—</span> },
    { id: 'ports', header: 'Ports', mono: true, text: (s) => s.ports.join(' '), cell: (s) => s.ports.join(', ') },
    { id: 'eps', header: 'Endpoints', align: 'right', sort: (s) => s.endpoints, cell: (s) => <span class={s.endpoints === 0 && s.selector ? 'text-warn' : ''}>{s.endpoints}</span> },
    { id: 'age', header: 'Age', cell: (s) => <span class="num text-muted">{s.age}</span> },
  ]
  const icols: Column<KIngress>[] = [
    { id: 'ns', header: 'Namespace', sort: (i) => i.namespace, cell: (i) => i.namespace },
    { id: 'name', header: 'Ingress', sort: (i) => i.name, cell: (i) => <span class="flex items-center gap-2"><span class="font-medium">{i.name}</span><AlertPill e={openAlert(name, 'Ingress', i.namespace, i.name)} /></span> },
    { id: 'class', header: 'Class', cell: (i) => i.class || <span class="text-muted">default</span> },
    { id: 'rules', header: 'Host / path → service', text: (i) => i.rules.map((r) => `${r.host}${r.path} ${r.service}`).join(' '), cell: (i) => (
      <div class="flex flex-col gap-0.5">
        {i.rules.map((r, k) => <span key={k} class="mono text-[12px]">{r.host || '*'}{r.path} <span class="text-muted">→</span> {r.service}:{r.port}{i.tlsHosts?.includes(r.host) && <Pill tone="good">tls</Pill>}</span>)}
      </div>
    ) },
    { id: 'addr', header: 'Address', mono: true, cell: (i) => (i.addresses ?? []).join(', ') || <span class="text-muted">pending</span> },
    { id: 'age', header: 'Age', cell: (i) => <span class="num text-muted">{i.age}</span> },
  ]

  return (
    <div class="flex flex-col gap-6">
      <ErrorBox error={error} />
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Section title="Cluster addressing">
          <div class="panel p-4">
            <KeyValue rows={[
              ['API endpoint', <span class="mono">{spec.controlPlane.endpoint}</span>],
              ['Control plane VIP', spec.controlPlane.vip ? <span class="mono">{spec.controlPlane.vip}</span> : <span class="text-muted">none — endpoint follows the first control plane</span>],
              ['Pod CIDR', <span class="mono">{spec.network.podCIDR}</span>],
              ['Service CIDR', <span class="mono">{spec.network.serviceCIDR}</span>],
              ['CNI', 'Flannel (Talos default)'],
              ['Node IPs', <span class="mono text-[12px]">{spec.nodes.map((n) => n.ip).join(', ')}</span>],
            ]} />
          </div>
        </Section>
        <Section title="MetalLB pool" help="Layer-2 pool: each address is announced from one node via ARP. Allocation is per LoadBalancer service.">
          {!spec.platform.metallb.enabled && <Notice tone="muted">MetalLB is disabled; LoadBalancer services stay pending.</Notice>}
          {view?.poolError && <Notice tone="bad">{view.poolError}</Notice>}
          {pool && (
            <div class="panel p-4 flex flex-col gap-3">
              <div class="flex items-baseline justify-between"><span class="mono">{pool.range}</span><span class="num text-[13px]"><strong>{pool.allocated.length}</strong> <span class="text-muted">/ {pool.total} in use</span></span></div>
              <div class="flex flex-wrap gap-1">
                {Array.from({ length: pool.total }, (_, i) => {
                  const ip = ipAt(pool.range, i)
                  const a = pool.allocated.find((x) => x.ip === ip)
                  return <span key={i} title={a ? `${ip} → ${a.service}` : `${ip} free`} class={`inline-block h-3 w-3 rounded-sm ${a ? 'bg-accent' : 'bg-panel-2 border border-border'}`} />
                })}
              </div>
              {pool.allocated.length > 0 && <KeyValue rows={pool.allocated.map((a) => [a.ip, a.service] as [string, string])} />}
            </div>
          )}
        </Section>
      </div>
      <Section title={`Services (${view?.services.length ?? 0})`} help="Endpoints counts ready backends; a selector-backed service with 0 endpoints receives traffic nowhere.">
        <DataTable loading={!view && !error} id="services" columns={scols} rows={view?.services ?? []} rowKey={(s) => s.namespace + '/' + s.name} defaultSort={{ id: 'type', dir: 'desc' }} />
      </Section>
      <Section title={`Ingresses (${view?.ingresses.length ?? 0})`} help={`HTTP routes handled by Ingress-NGINX${view?.pool?.allocated.find((a) => a.service.startsWith('ingress-nginx/')) ? ` at ${view.pool.allocated.find((a) => a.service.startsWith('ingress-nginx/'))!.ip}` : ''}.`}>
        <DataTable loading={!view && !error} id="ingresses" columns={icols} rows={view?.ingresses ?? []} rowKey={(i) => i.namespace + '/' + i.name} empty="No Ingress objects yet. Create one to expose an HTTP service by hostname." />
      </Section>
    </div>
  )
}

function ipAt(range: string, i: number) {
  const start = range.split('-')[0].trim().split('.').map(Number)
  let n = ((start[0] << 24) | (start[1] << 16) | (start[2] << 8) | start[3]) >>> 0
  n += i
  return [n >>> 24, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join('.')
}
