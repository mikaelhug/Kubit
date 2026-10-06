import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type ApplyLine, type Plan, type PlanChange } from '../../api'
import { Ago } from '../../components/Time'
import { ConfirmDialog, ErrorBox, Notice, Pill, PlanPill, Section } from '../../components/ui'
import { roleLabel } from '../../machine'
import { applyRuns, loadApplyRun, plans } from '../../store'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

const symbol: Record<string, string> = { create: '+', add: '+', remove: '−' }

export function Changes({ ctx }: { ctx: ClusterCtx }) {
  const { name } = ctx
  const { data, error: loadError } = useLive(() => api.plan(name), [name], [[name, 'plan']], { onError: 'box' })
  const summary = plans.value.get(name) ?? data?.summary
  const plan = data?.plan ?? null
  const [error, setError] = useState<string | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [allowRemoval, setAllowRemoval] = useState(false)
  const run = applyRuns.value.get(name)
  const running = !!run?.running
  useEffect(() => { loadApplyRun(name) }, [name])
  const checking = summary?.state === 'checking'
  const removals = plan?.changes.some((c) => c.blocked) ?? false
  const applicable = !!plan && summary?.state === 'ready' && plan.changes.length > 0 && !running
  const replan = () => { setError(null); api.replan(name).catch((e) => setError(e.message)) }

  return (
    <Section>
      <div class="flex flex-wrap items-center gap-2 text-[13px]">
        <PlanPill plan={summary} />
        {summary?.plannedAt && <span class="text-muted">planned <Ago iso={summary.plannedAt} /></span>}
        {summary?.state === 'applying' && summary.holder && <span class="text-muted">held by {summary.holder}</span>}
        <span class="ml-auto flex items-center gap-2">
          <button class="btn btn-sm" disabled={checking || running} onClick={replan}>{checking ? 'Planning' : 'Plan'}</button>
          <button class="btn btn-primary btn-sm" disabled={!applicable} onClick={() => setConfirm(true)}>Apply</button>
        </span>
      </div>
      <ErrorBox error={error ?? loadError ?? (summary?.state === 'failed' ? summary.error ?? null : null)} />
      {plan && <div class={checking ? 'opacity-60' : ''}><PlanView plan={plan} /></div>}
      {!plan && !checking && summary?.state !== 'failed' && <Notice tone="muted">No plan yet.</Notice>}
      {run && (run.running || run.lines.length > 0) && (
        <div class="panel flex flex-col">
          <div class="flex items-center gap-2 px-3 py-2 border-b border-border text-[13px]">
            <span class="font-medium">Apply</span>
            {run.running ? <Pill tone="info">running</Pill> : run.error ? <Pill tone="bad">failed</Pill> : <Pill tone="good">done</Pill>}
            {run.started && <span class="text-muted">{fmt.datetime(run.started)}</span>}
          </div>
          {run.error && <div class="px-3 pt-2"><ErrorBox error={run.error} /></div>}
          <pre class="log !max-h-none !rounded-none !border-0">{run.lines.map(logLine).join('\n')}</pre>
        </div>
      )}
      {confirm && plan && (
        <ConfirmDialog title={`Apply ${name}`} action="Apply" tone={removals ? 'danger' : 'primary'} onClose={() => setConfirm(false)}
          impact={impactOf(plan) || removals ? <>
            {impactOf(plan) && <span>{impactOf(plan)}</span>}
            {removals && <label class="flex items-center gap-2"><input type="checkbox" checked={allowRemoval} onChange={(e) => setAllowRemoval((e.target as HTMLInputElement).checked)} />Drain, delete and reset removed nodes</label>}
          </> : null}
          onConfirm={() => api.apply(name, allowRemoval, plan.hash).then(() => setConfirm(false)).catch((e) => { setError(e.message); setConfirm(false) })} />
      )}
    </Section>
  )
}

const logLine = (l: ApplyLine) => {
  const time = new Date(l.ts).toLocaleTimeString()
  const level = l.level === 'info' || l.level === 'done' ? '' : `${l.level.toUpperCase()} `
  if (l.message === l.step) return `${time} ${level}${l.step}`
  return `${time} ${level}  ${l.node ? `${l.node}: ` : ''}${l.message}`
}

const oneTime = (c: PlanChange) => c.action === 'platform' && c.target === 'state'

function impactOf(plan: Plan) {
  const n = plan.installs?.length ?? 0
  return n > 0 ? `Erases the install disk on ${n} machine${n === 1 ? '' : 's'}.` : ''
}

function PlanView({ plan }: { plan: Plan }) {
  if (!plan.problems?.length && plan.changes.length === 0) return <Notice tone="good">No changes.</Notice>
  const changes = plan.changes.filter((c) => !oneTime(c))
  const once = plan.changes.filter(oneTime)
  return (
    <div class="panel divide-y divide-border/60 text-[13px]">
      {plan.problems?.map((p) => <div key={p} class="px-3 py-2 text-bad">{p}</div>)}
      {changes.length === 0 && !plan.problems?.length && <div class="px-3 py-2 text-good">No cluster changes.</div>}
      {changes.map((c, i) => <ChangeRow key={i} c={c} />)}
      {once.map((c, i) => (
        <div key={`once-${i}`} class="px-3 py-2 flex items-center gap-2">
          <Pill tone="muted">one-time</Pill>
          <span class="text-muted">{c.detail}</span>
        </div>
      ))}
      {!plan.problems?.length && (plan.installs?.length ?? 0) > 0 && (
        <div class="px-3 py-2 flex flex-col gap-1">
          <span class="text-muted">Installs Talos on</span>
          <table class="data">
            <tbody>
              {plan.installs!.map((i) => (
                <tr key={i.hostname}>
                  <td class="mono">{i.hostname}</td>
                  <td>{roleLabel(i.role)}</td>
                  <td class="mono">{i.ip}</td>
                  <td class="text-warn">erases <span class="mono">{i.disk}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function ChangeRow({ c }: { c: PlanChange }) {
  const [open, setOpen] = useState(false)
  const config = c.action === 'config'
  return (
    <div class="px-3 py-2 flex flex-col gap-1">
      <div class="flex items-center gap-2">
        <span class="mono w-3 text-center">{symbol[c.action] ?? '~'}</span>
        <span class="font-medium">{c.action}</span>
        {c.target && <span class="mono">{c.target}</span>}
        {!config && c.detail && <span class="text-muted truncate">{c.detail}</span>}
        {c.blocked && <Pill tone="warn">needs removal allowed</Pill>}
        {config && <button class="btn btn-sm ml-auto" onClick={() => setOpen(!open)}>{open ? 'Hide diff' : 'Diff'}</button>}
      </div>
      {config && open && <pre class="log !max-h-none">{c.detail}</pre>}
    </div>
  )
}
