import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type Operation, type PlanDiff } from '../../api'
import { operations, toast, watch } from '../../store'
import { DiffView } from '../../components/DiffView'
import { Breadcrumbs, ErrorBox, Notice, Pill, stateTone } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

/** Full-page review of one plan; the only place a reviewed plan can be applied from. */
export function PlanReview({ ctx, planId }: { ctx: ClusterCtx; planId: number }) {
  const { name, cluster } = ctx
  const [op, setOp] = useState<Operation | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { api.operation(planId).then(setOp).catch((e) => setError(e.message)) }, [planId])
  const live = operations.value.get(planId)
  useEffect(() => { if (live && live.status !== 'running' && op?.status === 'running') api.operation(planId).then(setOp) }, [live?.status])

  const diff = op?.artifact as PlanDiff | undefined
  const newer = [...operations.value.values()].filter((o) => o.cluster === name && o.kind === 'platform.plan' && o.status === 'done' && o.id > planId).sort((a, b) => b.id - a.id)[0]
  const stale = !!(op && cluster.updatedAt > op.startedAt)
  const applied = [...operations.value.values()].find((o) => o.cluster === name && o.kind === 'platform.apply' && (o.request as any)?.planId === planId)
  const total = diff ? diff.summary.Add + diff.summary.Change + diff.summary.Remove : 0
  const apply = () => api.platformApplyPlan(name, planId).then((r) => watch(r)).catch((e) => setError(e.message))

  return (
    <div class="flex flex-col gap-4">
      <Breadcrumbs items={[{ label: name, href: `/clusters/${name}/overview` }, { label: 'Add-ons', href: `/clusters/${name}/addons` }, { label: `Plan #${planId}` }]} />
      <ErrorBox error={error} />
      {op && (
        <div class="flex flex-wrap items-center gap-3">
          <h2 class="font-semibold text-lg">Plan #{planId}</h2>
          <Pill tone={stateTone(op.status)}>{op.status}</Pill>
          <span class="text-[13px] text-muted">{fmt.datetime(op.startedAt)}</span>
          <a href={`/operations/${planId}`} class="text-[13px] text-accent hover:underline">log</a>
          <div class="ml-auto flex gap-2">
            <button class="btn" onClick={() => api.platformPlan(name).then((r) => { watch(r); toast('Planning again…') }).catch((e) => toast(e.message, 'error'))}>Plan again</button>
            <button class="btn btn-primary" disabled={op.status !== 'done' || !!newer || stale || total === 0 || !!applied} onClick={apply}>
              {applied ? `Applied in #${applied.id}` : total === 0 ? 'Nothing to apply' : `Apply these ${total} change${total === 1 ? '' : 's'}`}
            </button>
          </div>
        </div>
      )}
      {op?.status === 'running' && <Notice tone="warn">Still planning… this page updates when it finishes.</Notice>}
      {op?.status === 'failed' && <Notice tone="bad">The plan failed; open the log for the provider's error.</Notice>}
      {newer && <Notice tone="warn">Superseded by <a class="underline" href={`/clusters/${name}/addons/${newer.id}`}>plan #{newer.id}</a>; only the newest plan can be applied.</Notice>}
      {stale && !newer && <Notice tone="warn">cluster.yaml changed after this plan was made. Plan again to review the current state.</Notice>}
      {applied && <Notice tone={applied.status === 'done' ? 'good' : applied.status === 'running' ? 'warn' : 'bad'}>Apply operation <a class="underline" href={`/operations/${applied.id}`}>#{applied.id}</a> is {applied.status}.</Notice>}
      {diff ? <DiffView diff={diff} /> : op?.status === 'done' && <Notice tone="muted">This plan has no stored diff.</Notice>}
    </div>
  )
}
