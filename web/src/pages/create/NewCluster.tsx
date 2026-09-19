import { useEffect, useState } from 'preact/hooks'
import { api, type ClusterSpec, type NodeRow, type Warning } from '../../api'
import { watch } from '../../store'
import { ErrorBox } from '../../components/ui'
import { OperationView } from '../../components/ActivityDrawer'
import { MachinesStep, DesignStep, NetworkStep, PlatformStep, ReviewStep, Summary } from './steps'

export type StepId = 'machines' | 'design' | 'network' | 'platform' | 'review'
const steps: { id: StepId; label: string }[] = [
  { id: 'machines', label: 'Machines' }, { id: 'design', label: 'Design' }, { id: 'network', label: 'Network' }, { id: 'platform', label: 'Platform' }, { id: 'review', label: 'Review' },
]

export interface Draft {
  name: string
  machines: NodeRow[]          // every unassigned maintenance-mode machine known
  selected: string[]           // MACs, in selection order
  designedFor: string          // MAC set the proposal was computed for
  cluster: ClusterSpec | null
  skipPlatform: boolean
  warnings: Warning[]
}

/**
 * Create-cluster wizard. The daemon proposes a declaration for the chosen machines
 * (POST /config/design); every later step edits that object, and Review lints it.
 */
export function NewCluster() {
  const [step, setStep] = useState<StepId>('machines')
  const [draft, setDraft] = useState<Draft>({ name: 'homelab', machines: [], selected: [], designedFor: '', cluster: null, skipPlatform: false, warnings: [] })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [createOp, setCreateOp] = useState<number | null>(null)
  const [defaultRange, setDefaultRange] = useState('')
  useEffect(() => { api.settings().then((s) => setDefaultRange(s.defaultMetalLBRange)).catch(() => {}) }, [])

  const patch = (p: Partial<Draft>) => setDraft((d) => ({ ...d, ...p }))
  const setCluster = (fn: (c: ClusterSpec) => ClusterSpec) => setDraft((d) => d.cluster ? { ...d, cluster: fn(d.cluster) } : d)
  const idx = steps.findIndex((s) => s.id === step)

  const design = (force = false) => {
    const key = [...draft.selected].sort().join(',')
    if (!force && draft.cluster && draft.designedFor === key && draft.cluster.metadata.name === draft.name) return Promise.resolve()
    setBusy(true)
    return api.design(draft.name, draft.selected, defaultRange || undefined)
      .then((d) => { patch({ cluster: d.cluster, designedFor: key, warnings: d.warnings ?? [] }); setError(null) })
      .catch((e) => { setError(e.message); throw e })
      .finally(() => setBusy(false))
  }
  const next = () => {
    if (step === 'machines') design().then(() => setStep('design')).catch(() => {})
    else setStep(steps[idx + 1].id)
  }
  const create = (yaml: string) => {
    setBusy(true)
    api.validate(yaml).then((v) => api.createCluster(v.yaml, draft.skipPlatform))
      .then((r) => { setCreateOp(r.operationId); watch(r, false); setError(null) })
      .catch((e) => setError(e.message)).finally(() => setBusy(false))
  }

  if (createOp !== null && draft.cluster) {
    return (
      <div class="p-6 max-w-[1100px] flex flex-col gap-3">
        <h1 class="text-xl font-semibold">Creating {draft.cluster.metadata.name}</h1>
        <div class="panel h-[65vh] flex flex-col overflow-hidden"><OperationView id={createOp} tall /></div>
        <div class="flex gap-2 items-center">
          <a href={`/clusters/${draft.cluster.metadata.name}/overview`} class="btn btn-primary">Open cluster</a>
          <span class="text-[12px] text-muted">Provisioning keeps running if you leave; it stays in the Activity drawer.</span>
        </div>
      </div>
    )
  }

  const canNext = step === 'machines' ? draft.selected.length > 0 && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(draft.name) : !!draft.cluster
  return (
    <div class="p-5 flex flex-col gap-4 max-w-[1400px]">
      <header class="flex flex-wrap items-center gap-4">
        <h1 class="text-xl font-semibold">New cluster</h1>
        <ol class="flex gap-3 text-[12px]">
          {steps.map((s, i) => (
            <li key={s.id} class={`flex items-center gap-1.5 ${s.id === step ? 'text-text' : 'text-muted'}`}>
              <button class={`inline-flex h-5 w-5 items-center justify-center rounded-[var(--r-sm)] border text-[11px] ${s.id === step ? 'border-accent text-accent' : i < idx ? 'border-good text-good' : 'border-border'} ${i < idx ? 'cursor-pointer' : 'cursor-default'}`} disabled={i > idx} onClick={() => i < idx && setStep(s.id)}>{i < idx ? '✓' : i + 1}</button>
              {s.label}
            </li>
          ))}
        </ol>
      </header>
      <ErrorBox error={error} />
      <div class="grid grid-cols-1 xl:grid-cols-[1fr_300px] gap-5 items-start">
        <div class="flex flex-col gap-4 min-w-0">
          {step === 'machines' && <MachinesStep draft={draft} patch={patch} setError={setError} />}
          {step === 'design' && draft.cluster && <DesignStep draft={draft} setCluster={setCluster} reset={() => design(true)} busy={busy} />}
          {step === 'network' && draft.cluster && <NetworkStep draft={draft} setCluster={setCluster} />}
          {step === 'platform' && draft.cluster && <PlatformStep draft={draft} setCluster={setCluster} patch={patch} />}
          {step === 'review' && draft.cluster && <ReviewStep draft={draft} setCluster={setCluster} patch={patch} onCreate={create} busy={busy} />}
          <div class="flex items-center gap-2">
            {idx > 0 && <button class="btn" onClick={() => setStep(steps[idx - 1].id)}>← Back</button>}
            {step !== 'review' && <button class="btn btn-primary ml-auto" disabled={!canNext || busy} onClick={next}>{busy ? 'Working…' : `Continue to ${steps[idx + 1].label} →`}</button>}
          </div>
        </div>
        <Summary draft={draft} />
      </div>
    </div>
  )
}
