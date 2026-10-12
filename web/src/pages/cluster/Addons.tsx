import { addonCatalog } from '../../addons'
import { api, fmt, type AddonStatus } from '../../api'
import { ErrorBox, Notice, Pending, Pill, Section, StatusDot } from '../../components/ui'
import { stateTone } from '../../tone'
import { useLive } from '../../useLive'
import { AddonToggle } from '../../components/AddonToggle'
import { BuildList, FluxSync } from './AddonPanels'
import type { ClusterCtx } from './ClusterPage'

const stateText: Record<AddonStatus['state'], string> = { disabled: 'disabled', pending: 'enabled, not applied yet', deploying: 'deploying', ready: 'ready', degraded: 'degraded', failed: 'release failed', orphaned: 'disabled in cluster.yaml, still installed' }

export function Addons({ ctx }: { ctx: ClusterCtx }) {
  const { name, status, cluster } = ctx
  const { data: addons, error } = useLive(() => api.addons(name), [name], [[name, 'addons'], [name, 'config']])
  const drift = (addons ?? []).some((a) => a.state === 'pending' || a.state === 'orphaned')
  const { data: image } = useLive(() => api.imageStatus(name), [name], [[name, 'config'], [name, 'nodes']], { onError: 'null' })
  const stateOf = (key: string) => addons?.find((x) => x.key === key)?.state ?? 'disabled'
  const isInstalled = (key: string) => !!addons?.find((x) => x.key === key)?.installed
  const longhornWaits = image?.outdated && stateOf('longhorn') !== 'disabled'
  const declaredRepo = cluster.spec.spec.platform.flux?.repository
  const { data: sync } = useLive(() => api.flux(name), [name], [[name, 'flux']], { onError: 'null', enabled: isInstalled('flux') })
  const { data: buildList } = useLive(() => api.builds(name), [name], [[name, 'workloads']], { onError: 'null', enabled: isInstalled('builds') })
  const source = sync?.find((o) => o.kind === 'GitRepository' && o.name === 'flux-system')
  const pool = status?.loadBalancer.pool?.join(', ')
  const installed = addons ? addonCatalog.filter((d) => stateOf(d.key) !== 'disabled') : []
  const available = addons ? addonCatalog.filter((d) => stateOf(d.key) === 'disabled') : []

  return (
      <Section title="Platform add-ons">
        <ErrorBox error={error} />
        {longhornWaits && <Notice tone="warn">Nodes lack the Talos extensions Longhorn needs.</Notice>}
        {drift && <Notice tone="warn">cluster.yaml differs from what is installed. <a class="underline" href={`/clusters/${name}/changes`}>Review changes</a></Notice>}
        {!addons && !error && <div class="panel p-4 text-[13px] text-muted">Loading</div>}
        {installed.length > 0 && (
          <div class="grid grid-cols-1 lg:grid-cols-2 gap-3 items-start">
            {installed.map((d) => {
              const a = addons?.find((x) => x.key === d.key)
              const link = d.key === 'traefik' && status?.loadBalancer.ip ? `http://${status.loadBalancer.ip}` : undefined
              const st = a?.state ?? (addons ? 'disabled' : null)
              return (
                <div key={d.key} class="panel p-4 flex gap-3">
                  <div class="pt-1"><StatusDot tone={st ? stateTone(st) : 'muted'} pulse={st === 'deploying'} /></div>
                  <div class="flex flex-col gap-1.5 min-w-0 flex-1">
                    <div class="flex items-center gap-2">
                      <span class="font-medium">{d.name}</span>
                      {st && <Pill tone={stateTone(st)}>{stateText[st]}</Pill>}
                      <span class="ml-auto inline-flex gap-1">
                        {link && <a href={link} target="_blank" rel="noreferrer" class="btn btn-sm">Open ↗</a>}
                        {d.key === 'backup' ? <a href={`/clusters/${name}/backups`} class="btn btn-sm">Backups</a> : <AddonToggle cluster={name} info={d} platform={cluster.spec.spec.platform} />}
                      </span>
                    </div>
                    {a && (a.release || a.readiness || a.key === 'metallb' || a.key === 'flux' || a.key === 'builds') && (
                      <div class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 text-[12px] mt-1">
                        {a.release && <><span class="text-muted">Installed</span><span class="mono">{a.release.chart} {a.release.chartVersion}{a.release.appVersion ? ` (app ${a.release.appVersion})` : ''}{a.pinnedVersion && a.pinnedVersion !== a.release.chartVersion && <span class="text-warn"> · pinned {a.pinnedVersion}</span>}</span></>}
                        {a.release && <><span class="text-muted">Helm status</span><span class={a.release.status === 'deployed' ? '' : 'text-bad'}>{a.release.status}{a.release.lastDeployed ? ` · ${fmt.when(new Date(a.release.lastDeployed * 1000).toISOString())}` : ''}</span></>}
                        {a.readiness && <><span class="text-muted">Workloads</span><span class={a.readiness.ready === a.readiness.total ? '' : 'text-warn'}>{a.readiness.ready}/{a.readiness.total} available in {a.readiness.namespace}{a.readiness.detail?.length ? ` — ${a.readiness.detail.join(', ')}` : ''}</span></>}
                        {a.key === 'metallb' && <><span class="text-muted">Pool</span><span class="flex items-center gap-2"><span class="mono">{pool || '—'}</span>{status?.apiReachable && cluster.spec.spec.platform.metallb.enabled && cluster.spec.spec.platform.metallb.range !== (pool ?? '') && <Pending cluster={name} />}</span></>}
                        {a.key === 'builds' && <><span class="text-muted">Registry</span><span class="mono">registry.kubit → {a.address ?? '—'}</span></>}
                        {a.key === 'flux' && <><span class="text-muted self-center">Repository</span><span class="flex items-center gap-2 min-w-0"><span class="mono truncate min-w-0" title={source?.url}>{source?.url ?? '—'}</span>{source && declaredRepo && declaredRepo.url !== source.url && <Pending cluster={name} />}<a class="btn btn-sm ml-auto shrink-0" href={`/clusters/${name}/settings?view=apps`}>Settings</a></span></>}
                        {a.release?.values && a.release.values.length > 0 && <><span class="text-muted">Values</span><span class="mono truncate" title={a.release.values.join(', ')}>{a.release.values.join(', ')}</span></>}
                      </div>
                    )}
                    {a?.key === 'flux' && sync && sync.length > 0 && <FluxSync objects={sync} />}
                    {a?.key === 'builds' && buildList && buildList.length > 0 && <BuildList cluster={name} builds={buildList} />}
                  </div>
                </div>
              )
            })}
          </div>
        )}
        {available.length > 0 && (
          <div class="flex flex-col gap-2 mt-2">
            <span class="label">Available</span>
            <div class="panel divide-y divide-border/60">
              {available.map((d) => (
                <div key={d.key} class="flex items-center gap-3 px-4 py-2.5 text-[13px]">
                  <span class="font-medium">{d.name}</span>
                  <span class="ml-auto">{d.key === 'backup' ? <a href={`/clusters/${name}/backups`} class="btn btn-sm">Backups</a> : <AddonToggle cluster={name} info={d} platform={cluster.spec.spec.platform} />}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </Section>
  )
}
