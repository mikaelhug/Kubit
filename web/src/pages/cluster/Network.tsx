import { useMemo } from 'preact/hooks'
import { api, type KIngress, type KRoute, type KService } from '../../api'
import { DataTable, withoutColumn } from '../../components/DataTable'
import { NamespaceScope, useNamespaceScope } from '../../components/NamespaceScope'
import { Age } from '../../components/Time'
import { ErrorBox, KeyValue, Notice, Pill, Section } from '../../components/ui'
import { ipAt } from '../../net'
import { createdSort } from '../../time'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

const smallPool = 64

const home = (x: KService) => (x.namespace === 'default' && x.name === 'kubernetes' ? 'kube-system' : x.namespace)

export function Network({ ctx }: { ctx: ClusterCtx }) {
  const { name, status, cluster } = ctx
  const spec = cluster.spec.spec
  const { data: view, error } = useLive(() => api.network(name), [name], [[name, 'network'], [name, 'addons']])
  const pool = view?.pool
  const s = useNamespaceScope(name)
  const services = useMemo(() => (view?.services ?? []).filter((x) => s.keep(home(x))), [view, s.keep])
  const ingresses = useMemo(() => (view?.ingresses ?? []).filter((x) => s.keep(x.namespace)), [view, s.keep])
  const routes = useMemo(() => (view?.routes ?? []).filter((x) => s.keep(x.namespace)), [view, s.keep])
  const loading = (!view && !error) || s.loading
  const scols = useMemo(() => withoutColumn<KService>([
    { id: 'ns', header: 'Namespace', sort: (x) => x.namespace, cell: (x) => x.namespace },
    { id: 'name', header: 'Service', sort: (x) => x.name, cell: (x) => <span class="font-medium">{x.name}</span> },
    { id: 'type', header: 'Type', sort: (x) => x.type, cell: (x) => <Pill tone={x.type === 'LoadBalancer' ? 'info' : 'muted'}>{x.type}</Pill> },
    { id: 'cip', header: 'Cluster IP', mono: true, cell: (x) => x.clusterIP },
    { id: 'ext', header: 'External IP', mono: true, sort: (x) => (x.externalIPs ?? []).join(','), cell: (x) => (x.externalIPs ?? []).join(', ') || <span class="text-muted">—</span> },
    { id: 'ports', header: 'Ports', mono: true, text: (x) => (x.ports ?? []).join(' '), cell: (x) => (x.ports ?? []).join(', ') },
    { id: 'eps', header: 'Endpoints', align: 'right', sort: (x) => x.endpoints, cell: (x) => <span class={x.endpoints === 0 && x.selector ? 'text-warn' : ''}>{x.endpoints}</span> },
    { id: 'age', header: 'Age', sort: createdSort, cell: (x) => <span class="text-muted"><Age at={x.createdAt} fallback={x.age} /></span> },
  ], 'ns', !!s.ns), [s.ns])
  const icols = useMemo(() => withoutColumn<KIngress>([
    { id: 'ns', header: 'Namespace', sort: (i) => i.namespace, cell: (i) => i.namespace },
    { id: 'name', header: 'Ingress', sort: (i) => i.name, cell: (i) => <span class="font-medium">{i.name}</span> },
    { id: 'class', header: 'Class', cell: (i) => i.class || <span class="text-muted">default</span> },
    { id: 'rules', header: 'Host / path → service', text: (i) => (i.rules ?? []).map((r) => `${r.host}${r.path} ${r.service}`).join(' '), cell: (i) => (
      <div class="flex flex-col gap-0.5">
        {(i.rules ?? []).map((r) => <span key={`${r.host}${r.path}`} class="mono text-[12px]">{r.host || '*'}{r.path} <span class="text-muted">→</span> {r.service}:{r.port}{i.tlsHosts?.includes(r.host) && <Pill tone="good">tls</Pill>}</span>)}
      </div>
    ) },
    { id: 'addr', header: 'Address', mono: true, cell: (i) => (i.addresses ?? []).join(', ') || <span class="text-muted">pending</span> },
    { id: 'age', header: 'Age', sort: createdSort, cell: (i) => <span class="text-muted"><Age at={i.createdAt} fallback={i.age} /></span> },
  ], 'ns', !!s.ns), [s.ns])
  const rcols = useMemo(() => withoutColumn<KRoute>([
    { id: 'ns', header: 'Namespace', sort: (r) => r.namespace, cell: (r) => r.namespace },
    { id: 'name', header: 'Route', sort: (r) => r.name, cell: (r) => <span class="font-medium">{r.name}</span> },
    { id: 'rules', header: 'Host / path → backend', text: (r) => `${r.hostnames.join(' ')} ${r.rules.map((x) => `${x.path} ${x.backends.join(' ')}`).join(' ')}`, cell: (r) => (
      <div class="flex flex-col gap-0.5">
        {r.rules.map((x, i) => <span key={i} class="mono text-[12px]">{r.hostnames.join(', ') || '*'}{x.path} <span class="text-muted">→</span> {x.backends.join(', ') || '—'}</span>)}
      </div>
    ) },
    { id: 'gateway', header: 'Gateway', mono: true, cell: (r) => r.gateways.join(', ') || '—' },
    { id: 'accepted', header: 'Accepted', sort: (r) => r.accepted, cell: (r) => <Pill tone={r.accepted === 'True' ? 'good' : r.accepted === 'False' ? 'bad' : 'muted'} title={r.message}>{r.accepted === 'True' ? 'yes' : r.accepted === 'False' ? 'no' : 'unknown'}</Pill> },
    { id: 'age', header: 'Age', sort: createdSort, cell: (r) => <span class="text-muted"><Age at={r.createdAt} fallback={r.age} /></span> },
  ], 'ns', !!s.ns), [s.ns])
  const ingressIP = status?.ingressIP || pool?.allocated.find((a) => a.service === 'traefik/traefik')?.ip

  return (
    <div class="flex flex-col gap-5">
      <ErrorBox error={error} />
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Section title="Cluster addressing">
          <div class="panel p-3">
            <KeyValue rows={[
              ['API endpoint', <span class="mono">{spec.controlPlane.endpoint}</span>],
              ['Control plane VIP', spec.controlPlane.vip ? <span class="mono">{spec.controlPlane.vip}</span> : <span class="text-muted">none</span>],
              ['Pod CIDR', <span class="mono">{spec.network.podCIDR}</span>],
              ['Service CIDR', <span class="mono">{spec.network.serviceCIDR}</span>],
              ['CNI', spec.network.policies === false ? 'Flannel' : 'Flannel, NetworkPolicy enforced'],
              ['Node IPs', <span class="mono text-[12px]">{spec.nodes.map((n) => n.ip).join(', ')}</span>],
            ]} />
          </div>
        </Section>
        <Section title="MetalLB pool">
          {!spec.platform.metallb.enabled && <Notice tone="muted">MetalLB is off.</Notice>}
          {view?.poolError && <Notice tone="bad">{view.poolError}</Notice>}
          {pool && (
            <div class="panel p-3 flex flex-col gap-3">
              <div class="flex items-baseline justify-between"><span class="mono">{pool.range}</span><span class="text-[13px]"><strong>{pool.allocated.length}</strong> <span class="text-muted">/ {pool.total} in use</span></span></div>
              {pool.total <= smallPool && <div class="flex flex-wrap gap-1">
                {Array.from({ length: pool.total }, (_, i) => {
                  const ip = ipAt(pool.range, i)
                  const a = pool.allocated.find((x) => x.ip === ip)
                  return <span key={ip} title={a ? `${ip} → ${a.service}` : `${ip} free`} class={`inline-block h-3 w-3 rounded-sm ${a ? 'bg-accent' : 'bg-panel-2 border border-border'}`} />
                })}
              </div>}
              {pool.allocated.length > 0 && <KeyValue rows={pool.allocated.map((a) => [a.ip, a.service] as [string, string])} />}
            </div>
          )}
        </Section>
      </div>
      <div class="flex justify-end"><NamespaceScope s={s} rows={[...(view?.services ?? []).map(home), ...(view?.ingresses ?? []).map((x) => x.namespace), ...(view?.routes ?? []).map((x) => x.namespace)]} /></div>
      <Section title={`Services (${services.length})`}>
        <DataTable loading={loading} id="services" columns={scols} rows={services} rowKey={(x) => x.namespace + '/' + x.name} defaultSort={{ id: 'type', dir: 'desc' }} empty={s.scope === 'apps' && !s.ns ? 'No app services yet.' : 'No services.'} />
      </Section>
      {(routes.length > 0 || view?.routesError) && (
        <Section title={`HTTP routes (${routes.length})`}>
          {view?.routesError && <Notice tone="bad">{view.routesError}</Notice>}
          <DataTable loading={loading} id="routes" columns={rcols} rows={routes} rowKey={(r) => r.namespace + '/' + r.name} empty="No routes." />
        </Section>
      )}
      <Section title={`Ingresses (${ingresses.length})`} help={ingressIP ? `Traefik at ${ingressIP}` : undefined}>
        <DataTable loading={loading} id="ingresses" columns={icols} rows={ingresses} rowKey={(i) => i.namespace + '/' + i.name} empty="No Ingress objects yet." />
      </Section>
    </div>
  )
}
