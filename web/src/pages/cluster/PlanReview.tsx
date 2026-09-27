import { api, fmt, type PlanDiff } from '../../api'
import { DiffView } from '../../components/DiffView'
import { Breadcrumbs, ErrorBox, Notice, Pill } from '../../components/ui'
import { operations, opsFor, runOp } from '../../ops'
import { later } from '../../time'
import { stateTone } from '../../tone'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

export function PlanReview({ ctx, planId }: { ctx: ClusterCtx; planId: number }) {
  const { name, cluster } = ctx
  const liveStatus = operations.value.get(planId)?.status
  const { data: op, error } = useLive(() => api.operation(planId), [planId], [], { refresh: [liveStatus === 'running'] })
  const ops = opsFor(name)
  const diff = op?.artifact as PlanDiff | undefined
  const newer = ops.filter((o) => o.kind === 'platform.plan' && o.status === 'done' && o.id > planId).sort((a, b) => b.id - a.id)[0]
  const stale = !!op && later(cluster.updatedAt, op.startedAt)
  const applied = ops.find((o) => o.kind === 'platform.apply' && (o.request as { planId?: number } | undefined)?.planId === planId)
  const total = diff ? diff.summary.Add + diff.summary.Change + diff.summary.Remove : 0
  const apply = () => runOp(api.platformApplyPlan(name, planId))

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
            <button class="btn" onClick={() => runOp(api.platformPlan(name), 'Planning again')}>Plan again</button>
            <button class="btn btn-primary" disabled={op.status !== 'done' || !!newer || stale || total === 0 || !!applied} onClick={apply}>
              {applied ? `Applied in #${applied.id}` : total === 0 ? 'Nothing to apply' : `Apply these ${total} change${total === 1 ? '' : 's'}`}
            </button>
          </div>
        </div>
      )}
      {op?.status === 'running' && <Notice tone="warn">Still planning</Notice>}
      {op?.status === 'failed' && <Notice tone="bad">The plan failed; the log has the provider's error.</Notice>}
      {newer && <Notice tone="warn">Superseded by <a class="underline" href={`/clusters/${name}/addons/${newer.id}`}>plan #{newer.id}</a>; only the newest plan can be applied.</Notice>}
      {stale && !newer && <Notice tone="warn">cluster.yaml changed after this plan; plan again.</Notice>}
      {applied && <Notice tone={stateTone(applied.status)}>Apply operation <a class="underline" href={`/operations/${applied.id}`}>#{applied.id}</a> is {applied.status}.</Notice>}
      {diff ? <DiffView diff={diff} /> : op?.status === 'done' && <Notice tone="muted">This plan has no stored diff.</Notice>}
    </div>
  )
}
