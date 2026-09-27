import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { addonCatalog } from '../../addons'
import { api, fmt, type AddonStatus } from '../../api'
import { ErrorBox, Notice, Pill, Section, StatusDot } from '../../components/ui'
import { useImageStatus } from '../../imageStatus'
import { operations, opsFor, runOp, watch } from '../../ops'
import { later } from '../../time'
import { stateTone } from '../../tone'
import { useLive } from '../../useLive'
import { ConfigureDialog } from './AddonConfigure'
import { BuildList, FluxSync, ImportKeyDialog, SOPSRow } from './AddonPanels'
import type { ClusterCtx } from './ClusterPage'

const stateText: Record<AddonStatus['state'], string> = { disabled: 'disabled', pending: 'enabled, not applied yet', deploying: 'deploying', ready: 'ready', degraded: 'degraded', failed: 'release failed', orphaned: 'disabled in cluster.yaml, still installed' }

export function Addons({ ctx }: { ctx: ClusterCtx }) {
  const { route } = useLocation()
  const { name, status, cluster } = ctx
  const { data: addons, error, set: setAddons } = useLive(() => api.addons(name), [name], [[name, 'addons']], { refresh: [cluster.updatedAt] })
  const [edit, setEdit] = useState<AddonStatus | null>(null)
  const ops = opsFor(name)
  const lastPlan = ops.filter((o) => o.kind === 'platform.plan' && o.status === 'done').sort((a, b) => b.id - a.id)[0]
  const planStale = lastPlan && later(cluster.updatedAt, lastPlan.startedAt)
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
  const plan = () => runOp(api.platformPlan(name).then((r) => { setPendingPlan(r.operationId); return r }), 'Planning; the review opens when done', false)
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
          {addonCatalog.map((d) => {
            const a = addons?.find((x) => x.key === d.key)
            const link = d.key === 'ingressNginx' && status?.platform?.outputs?.ingress_ip ? `http://${status.platform.outputs.ingress_ip}` : undefined
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
      {edit && <ConfigureDialog ctx={ctx} addon={edit} def={addonCatalog.find((d) => d.key === edit.key)!} onClose={() => setEdit(null)} onSaved={(list) => { setAddons(list); setEdit(null) }} />}
    </>
  )
}
