import type { ComponentChildren } from 'preact'
import { fmt, type ClusterSpec, type Inventory, type NodeDetail, type NodeRow, type NodeSpec } from '../../api'
import { Elapsed } from '../../components/Time'
import { identityRows } from '../../components/Machine'
import { KeyValue, Meter, Notice, Pill, Section } from '../../components/ui'
import { hostName, hostOf, kindDetail, kindLabel, modelOf } from '../../machine'

type Row = [string, ComponentChildren]

export function OverviewTab({ inv, invErr, k8s, k8sErr, node, spec, storage }: { inv: Inventory | null; invErr: string | null; k8s: NodeDetail | null; k8sErr: string | null; node: NodeRow | null; spec?: NodeSpec; storage?: ClusterSpec['spec']['storage'] }) {
  if (!node) return <div class="text-muted">Loading</div>
  const host = hostOf(node)
  const rows: Row[] = [
    ['Kind', `${kindLabel[node.kind]}${kindDetail(node) ? ` · ${kindDetail(node)}` : ''}`],
    ['Model', [modelOf(node), inv?.platform].filter(Boolean).join(' · ')],
    ...(host ? [['Lab host', <a class="text-accent hover:underline" href={`/labhosts/${host.mac}/overview`}>{hostName(host)}</a>] as Row] : []),
    ...identityRows(node),
    ...(spec ? [['Install disk', <span class="mono">{spec.installDisk?.path ?? (spec.installDisk?.selector ? JSON.stringify(spec.installDisk.selector) : 'pool policy')}</span>] as Row] : []),
    ...(spec?.dataDisks?.length ? [['Data disks', <span class="mono">{spec.dataDisks.map((d, i) => `${d} → /var/mnt/data-${i + 1}`).join(' · ')}</span>] as Row] : []),
    ...(spec && !spec.dataDisks?.length && storage?.systemDisk ? [['System disk', <span class="mono">/var {storage.ephemeralSize ?? '40GiB'} · rest → /var/mnt/data-system</span>] as Row] : []),
  ]
  const identity = (
    <Section title="Machine" help="What Kubit has recorded about this machine.">
      <div class="panel p-3"><KeyValue rows={rows} /></div>
    </Section>
  )
  if (!node.talos) {
    const next = node.kind === 'configured' ? 'Runs Talos with a config Kubit did not apply; reset it to maintenance mode to adopt it.'
      : node.kind === 'booting' ? 'Waiting for Talos maintenance mode.'
      : host ? 'The VM is off; start it from the Actions tab.'
      : node.oobType ? 'Not running Talos; boot into Talos or make it a lab host from the Actions tab.'
      : 'Not running Talos; boot it into Talos, or configure remote management under Actions.'
    return (
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {identity}
        <Section title="Next" help="What this machine is waiting for.">
          <Notice tone={node.kind === 'booting' ? 'warn' : 'muted'}>{next}</Notice>
        </Section>
      </div>
    )
  }
  return (
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
      {identity}
      <Section title="Talos" help="Read live over the Talos API.">
        <div class="panel p-3">
          {invErr && <Notice tone="bad">{invErr}</Notice>}
          {!inv && !invErr && <div class="text-muted">Loading</div>}
          {inv && <KeyValue rows={[
            ['Version', <span class="mono">{inv.talosVersion}</span>],
            ['Stage', <Pill tone={inv.stage === 'running' ? 'good' : 'warn'}>{inv.stage}</Pill>],
            ['Booted', inv.bootTime ? <>{fmt.datetime(inv.bootTime)} (up <Elapsed from={inv.bootTime} />)</> : '—'],
            ['Extensions', inv.extensions?.filter((e) => e.name !== 'schematic').map((e) => `${e.name} ${e.version}`).join(', ') || 'none'],
          ]} />}
          {inv?.etcd && (
            <div class="mt-4 pt-4 border-t border-border">
              <div class="label mb-2">etcd member</div>
              <KeyValue rows={[
                ['Role', inv.etcd.leader ? <Pill tone="good">leader</Pill> : inv.etcd.learner ? <Pill tone="warn">learner</Pill> : <Pill tone="muted">follower</Pill>],
                ['Member ID', <span class="mono">{inv.etcd.memberId}</span>],
                ['Database', `${fmt.bytes(inv.etcd.dbInUseBytes)} in use of ${fmt.bytes(inv.etcd.dbSizeBytes)}`],
                ['Raft', `index ${inv.etcd.raftIndex}, term ${inv.etcd.raftTerm}`],
                ['Errors', inv.etcd.errors?.length ? <span class="text-bad">{inv.etcd.errors.join('; ')}</span> : 'none'],
              ]} />
            </div>
          )}
        </div>
      </Section>
      {node.kind === 'member' ? (
        <Section title="Kubernetes" help="What the API server knows about this node.">
          <div class="panel p-3 flex flex-col gap-4">
            {k8sErr && <Notice tone="bad">{k8sErr}</Notice>}
            {k8s && (
              <>
                <KeyValue rows={[
                  ['Kubelet', <span class="mono">{k8s.kubeletVersion}</span>],
                  ['Runtime', <span class="mono">{k8s.containerRuntime}</span>],
                  ['Kernel', <span class="mono">{k8s.kernel}</span>],
                  ['Internal IP', <span class="mono">{k8s.internalIP}</span>],
                  ['Taints', k8s.taints?.length ? k8s.taints.map((t) => <span key={t} class="mono block">{t}</span>) : 'none'],
                ]} />
                <Meter label="CPU requested" used={k8s.requests.cpuMilli} cap={k8s.allocatable.cpuMilli} format={fmt.cores} />
                <Meter label="Memory requested" used={k8s.requests.memBytes} cap={k8s.allocatable.memBytes} format={fmt.bytes} />
                <Meter label="Pods" used={k8s.requests.pods} cap={k8s.allocatable.pods} format={String} />
              </>
            )}
          </div>
        </Section>
      ) : (
        <Section title="Kubernetes" help="Joins a cluster from the Actions tab.">
          <Notice tone="muted">In maintenance mode; not a cluster member.</Notice>
        </Section>
      )}
    </div>
  )
}
