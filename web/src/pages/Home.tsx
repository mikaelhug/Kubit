import type { ComponentChildren } from 'preact'
import { useEffect } from 'preact/hooks'
import { fmt } from '../api'
import { AlertGroup } from '../components/Alerts'
import { ClusterPill, Code, Notice, Pill, Section, Tile } from '../components/ui'
import { clusters, kubitKey, loadAllHealth, machineList, observer, openAlerts, statuses, versions } from '../store'
import { type Tone } from '../tone'
import { defaultTalos, talosIso, updatesFor } from '../versions'

const factory = 'https://factory.talos.dev'

export function Home() {
  const list = clusters.value
  const machines = machineList.value
  const keys = [kubitKey, ...list.map((c) => c.name)]
  useEffect(() => { loadAllHealth(keys) }, [keys.join(',')])

  if (list.length === 0 && machines.length === 0) return <Welcome />

  const updates = new Map(list.map((c) => [c.name, updatesFor(c.spec.spec.talosVersion, c.spec.spec.kubernetesVersion, versions.value)]))
  const groups = [
    ...list.map((c) => ({ key: c.name, label: c.name, href: `/clusters/${c.name}/overview`, alerts: openAlerts(c.name) })),
    { key: kubitKey, label: 'Kubit', href: '/', alerts: openAlerts(kubitKey).filter((e) => e.kind !== 'test') },
  ].filter((g) => g.alerts.length > 0)
  const notices: { tone: Tone; text: ComponentChildren }[] = []
  const obs = observer.value
  if (!obs.online) notices.push({ tone: 'bad', text: <>Kubit cannot reach the local network{obs.since ? ` since ${fmt.when(obs.since)}` : ''}{obs.error ? ` (${obs.error})` : ''}; cluster alerts are paused.</> })
  for (const c of list) {
    const u = updates.get(c.name)!
    if (u.talos || u.kubernetes) notices.push({ tone: 'info', text: <>{c.name}: update available{u.talos ? ` · Talos ${u.talos}` : ''}{u.kubernetes ? ` · Kubernetes ${u.kubernetes}` : ''}.</> })
  }
  const quiet = groups.length === 0 && notices.length === 0
  const available = machines.filter((m) => m.kind === 'maintenance').length
  const other = machines.filter((m) => m.kind !== 'maintenance' && m.kind !== 'member').length

  return (
    <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
      <Section title="Needs attention" help={quiet ? undefined : 'Open alerts and Kubit notices.'}>
        {quiet && <div class="panel p-3 text-[13px] text-muted">Nothing needs attention.</div>}
        {notices.map((n, i) => <Notice key={i} tone={n.tone}>{n.text}</Notice>)}
        {groups.map((g) => <AlertGroup key={g.key} id={g.key} alerts={g.alerts} label={g.label} href={g.href} />)}
      </Section>

      <Section title="Clusters">
        {list.length === 0 && <div class="panel p-3 text-[13px] text-muted">No cluster yet.</div>}
        <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {list.map((c) => { const u = updates.get(c.name)!; return <ClusterCard key={c.name} name={c.name} state={c.state} talos={c.spec.spec.talosVersion} k8s={c.spec.spec.kubernetesVersion} update={!!(u.talos || u.kubernetes)} alerts={openAlerts(c.name).length} /> })}
        </div>
      </Section>

      <Section title="Machines">
        <div class="grid grid-cols-2 gap-4">
          <Tile label="Maintenance mode" value={available} href="/discovery" sub="ready to join a cluster" tone={available > 0 ? 'good' : undefined} />
          <Tile label="Other" value={other} href="/discovery" sub="not members, not in maintenance" />
        </div>
      </Section>

    </div>
  )
}


function ClusterCard({ name, state, talos, k8s, update, alerts }: { name: string; state: string; talos: string; k8s: string; update: boolean; alerts: number }) {
  const st = statuses.value.get(name)
  const t = st?.totals
  return (
    <a href={`/clusters/${name}/overview`} class="panel p-3 flex flex-col gap-2 hover:border-accent min-w-0">
      <div class="flex items-center gap-2"><span class="font-semibold truncate">{name}</span><ClusterPill state={state} status={st} />{alerts > 0 && <Pill tone="warn">{alerts} alert{alerts === 1 ? '' : 's'}</Pill>}</div>
      <div class="text-[13px] flex gap-3">
        <span class={t && t.nodesReady < t.nodes ? 'text-warn' : ''}>{t ? `${t.nodesReady}/${t.nodes}` : '—'} <span class="text-muted">nodes</span></span>
        <span class={st && !st.etcd.healthy ? 'text-bad' : ''}>{st ? `${st.etcd.members}/${st.etcd.expected}` : '—'} <span class="text-muted">etcd</span></span>
        <span>{st?.apiReachable ? t?.pods ?? '—' : '—'} <span class="text-muted">pods</span></span>
      </div>
      <div class="text-[12px] text-muted mono flex items-center gap-2 min-w-0"><span class="truncate">Talos {talos} · Kubernetes {k8s}</span>{update && <Pill tone="info">update</Pill>}</div>
      <div class="text-[12px] text-muted">{st?.lastSnapshotAt ? `last etcd snapshot ${fmt.when(st.lastSnapshotAt)}` : 'no etcd snapshot yet'}</div>
    </a>
  )
}


function Welcome() {
  const talos = defaultTalos(versions.value)
  return (
    <div class="p-8 max-w-3xl flex flex-col gap-6">
      <div>
        <h1 class="text-2xl font-semibold">Welcome to Kubit</h1>
        <p class="text-muted mt-1">Talos clusters declared in cluster.yaml.</p>
      </div>
      <Step n={1} title="Boot machines into Talos maintenance mode">
        <div class="flex flex-wrap gap-2 mt-1">
          <a class="btn btn-primary" href={talosIso(factory, talos, 'amd64')}>ISO · amd64</a>
          <a class="btn" href={talosIso(factory, talos, 'arm64')}>ISO · arm64</a>
        </div>
        <span class="text-muted mt-1">Or over the network:</span>
        <Code text="sudo kubit pxe" />
      </Step>
      <Step n={2} title="Declare the cluster in a repo">
        <Code text="kubit init lab --nodes 192.168.1.0/24" />
      </Step>
      <Step n={3} title="Apply it">
        <Code text="kubit apply lab" />
      </Step>
    </div>
  )
}

function Step({ n, title, children }: { n: number; title: string; children: ComponentChildren }) {
  return (
    <div class="panel p-5 flex gap-4">
      <span class="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-[var(--r-sm)] border border-accent text-accent text-[11px] font-semibold">{n}</span>
      <div class="flex flex-col gap-1 min-w-0 text-[13.5px]">
        <h2 class="font-semibold text-[15px]">{title}</h2>
        {children}
      </div>
    </div>
  )
}
