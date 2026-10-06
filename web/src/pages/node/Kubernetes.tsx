import { fmt, type NodeDetail } from '../../api'
import { PodTable } from '../../components/PodTable'
import { Notice, Section, StatusDot } from '../../components/ui'

export function KubernetesTab({ cluster, k8s, err }: { cluster: string; k8s: NodeDetail | null; err: string | null }) {
  if (err) return <Notice tone={err.includes('not a cluster member') ? 'muted' : 'bad'}>{err}</Notice>
  if (!k8s) return <div class="text-muted">Loading</div>
  const pods = k8s.pods ?? []
  return (
    <div class="flex flex-col gap-5">
      <Section title="Conditions">
        <div class="panel divide-y divide-border/60">
          {(k8s.conditions ?? []).map((c) => {
            const bad = (c.type === 'Ready') !== (c.status === 'True')
            return (
              <div key={c.type} class="flex items-center gap-3 px-4 py-2 text-[13px]">
                <StatusDot tone={bad ? 'bad' : 'good'} />
                <span class="w-40 font-medium">{c.type}</span>
                <span class="mono w-14">{c.status}</span>
                <span class="text-muted truncate" title={c.message}>{c.message}</span>
                <span class="ml-auto text-muted text-[12px]">since {fmt.datetime(c.since ?? '')}</span>
              </div>
            )
          })}
        </div>
      </Section>
      <Section title={`Pods on this node (${pods.length})`}>
        <PodTable cluster={cluster} id="node-pods" pods={pods} hide={['node']} />
      </Section>
      <Section title="Labels">
        <div class="panel p-3 flex flex-wrap gap-1.5">
          {Object.entries(k8s.labels ?? {}).sort().map(([k, v]) => <span key={k} class="mono text-[11.5px] rounded bg-panel-2 px-1.5 py-0.5">{k}{v ? `=${v}` : ''}</span>)}
        </div>
      </Section>
    </div>
  )
}
