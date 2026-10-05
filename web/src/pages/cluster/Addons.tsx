import { addonCatalog } from '../../addons'
import { api, fmt, type AddonStatus } from '../../api'
import { ErrorBox, Notice, Pill, Section, StatusDot } from '../../components/ui'
import { useImageStatus } from '../../imageStatus'
import { stateTone } from '../../tone'
import { useLive } from '../../useLive'
import { BuildList, FluxSync, SOPSRow } from './AddonPanels'
import type { ClusterCtx } from './ClusterPage'

const stateText: Record<AddonStatus['state'], string> = { disabled: 'disabled', pending: 'enabled, not applied yet', deploying: 'deploying', ready: 'ready', degraded: 'degraded', failed: 'release failed', orphaned: 'disabled in cluster.yaml, still installed' }

export function Addons({ ctx }: { ctx: ClusterCtx }) {
  const { name, status, cluster } = ctx
  const { data: addons, error } = useLive(() => api.addons(name), [name], [[name, 'addons']], { refresh: [cluster.updatedAt] })
  const drift = (addons ?? []).some((a) => a.state === 'pending' || a.state === 'orphaned')
  const image = useImageStatus(name, cluster.updatedAt)
  const longhornWaits = image?.outdated && cluster.spec.spec.platform.longhorn?.enabled
  const flux = !!cluster.spec.spec.platform.flux?.enabled
  const repo = cluster.spec.spec.platform.flux?.repository
  const builds = !!cluster.spec.spec.platform.builds?.enabled
  const { data: sops } = useLive(() => api.sopsKey(name), [name], [[name, 'sops']], { onError: 'null', enabled: flux })
  const { data: sync } = useLive(() => api.flux(name), [name], [[name, 'flux']], { onError: 'null', enabled: flux })
  const { data: buildList } = useLive(() => api.builds(name), [name], [[name, 'workloads']], { onError: 'null', enabled: builds })

  return (
      <Section title="Platform add-ons" help="Declared under spec.platform in cluster.yaml.">
        <ErrorBox error={error} />
        {status?.platform?.error && <Notice tone="bad">Last apply failed: {status.platform.error}</Notice>}
        {longhornWaits && <Notice tone="warn">Longhorn needs Talos extensions the nodes lack; upgrade Talos first.</Notice>}
        {drift && <Notice tone="warn">cluster.yaml differs from what is installed; run <span class="mono">kubit apply</span>.</Notice>}
        {!drift && status?.platform?.appliedAt && <Notice tone="muted">In sync with cluster.yaml; last applied {fmt.datetime(status.platform.appliedAt)}.</Notice>}
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
          {addonCatalog.map((d) => {
            const a = addons?.find((x) => x.key === d.key)
            const link = d.key === 'traefik' && status?.platform?.outputs?.ingress_ip ? `http://${status.platform.outputs.ingress_ip}` : undefined
            const st = a?.state ?? (addons ? 'disabled' : null)
            return (
              <div key={d.key} class={`panel p-4 flex gap-3 ${st === 'disabled' ? 'opacity-75' : ''}`}>
                <div class="pt-1"><StatusDot tone={st ? stateTone(st) : 'muted'} pulse={st === 'deploying'} /></div>
                <div class="flex flex-col gap-1.5 min-w-0 flex-1">
                  <div class="flex items-center gap-2">
                    <span class="font-medium">{d.name}</span>
                    {st && <Pill tone={stateTone(st)}>{stateText[st]}</Pill>}
                    {link && <a href={link} target="_blank" rel="noreferrer" class="btn btn-sm ml-auto">Open ↗</a>}
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
                  {a?.key === 'flux' && sops && <SOPSRow sops={sops} />}
                </div>
              </div>
            )
          })}
        </div>
      </Section>
  )
}
