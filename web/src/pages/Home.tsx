import type { ComponentChildren } from 'preact'
import { fmt } from '../api'
import { AlertGroup } from '../components/Alerts'
import { Upgrade } from '../components/Upgrade'
import { ClusterPill, Notice, Pill, PlanPill, Section, Tile, planLabel } from '../components/ui'
import { clusters, kubitKey, machineList, openAlerts, plans, statuses, versions } from '../store'
import { addable } from '../machine'
import { type Tone } from '../tone'
import { updateText, updatesFor } from '../versions'

export function Home() {
  const list = clusters.value
  const machines = machineList.value

  const updates = new Map(list.map((c) => [c.name, updatesFor(c.spec.spec.talosVersion, c.spec.spec.kubernetesVersion, versions.value)]))
  const groups = [
    ...list.map((c) => ({ key: c.name, label: c.name, href: `/clusters/${c.name}/overview`, alerts: openAlerts(c.name) })),
    { key: kubitKey, label: 'Kubit', href: '/', alerts: openAlerts(kubitKey) },
  ].filter((g) => g.alerts.length > 0)
  const notices: { tone: Tone; text: ComponentChildren }[] = []
  for (const c of list) {
    const u = updates.get(c.name)!
    if (u.talos || u.kubernetes) notices.push({ tone: 'info', text: <span class="flex flex-wrap items-center gap-2"><span>{c.name}: {updateText(u.talos, u.kubernetes)} available.</span><Upgrade cluster={c.name} talos={u.talos} kubernetes={u.kubernetes} /></span> })
    const p = plans.value.get(c.name)
    if (p && (p.state === 'blocked' || p.state === 'failed')) notices.push({ tone: 'warn', text: <>{c.name}: cannot apply ({planLabel(p).text}). <a class="underline" href={`/clusters/${c.name}/changes`}>Review</a></> })
  }
  const quiet = groups.length === 0 && notices.length === 0
  const available = machines.filter(addable).length
  const members = machines.filter((m) => m.kind === 'member').length
  const other = machines.filter((m) => m.kind !== 'maintenance' && m.kind !== 'member').length

  return (
    <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
      <Section title="Needs attention">
        {quiet && <div class="panel p-3 text-[13px] text-muted">Nothing needs attention.</div>}
        {notices.map((n, i) => <Notice key={i} tone={n.tone}>{n.text}</Notice>)}
        {groups.map((g) => <AlertGroup key={g.key} alerts={g.alerts} label={g.label} href={g.href} />)}
      </Section>

      <Section title="Clusters">
        {list.length === 0 && <div class="panel p-3 text-[13px] text-muted">No cluster yet.</div>}
        <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {list.map((c) => { const u = updates.get(c.name)!; return <ClusterCard key={c.name} name={c.name} state={c.state} talos={c.spec.spec.talosVersion} k8s={c.spec.spec.kubernetesVersion} update={!!(u.talos || u.kubernetes)} alerts={openAlerts(c.name).length} /> })}
        </div>
      </Section>

      <Section title="Machines">
        <div class="grid grid-cols-3 gap-4">
          <Tile label="Ready to add" value={available} href="/discovery" tone={available > 0 ? 'good' : undefined} />
          <Tile label="Cluster members" value={members} href="/discovery" />
          <Tile label="Other" value={other} href="/discovery" tone={other > 0 ? 'warn' : undefined} />
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
      <div class="flex items-center gap-2"><span class="font-semibold truncate">{name}</span><ClusterPill state={state} status={st} /><PlanPill plan={plans.value.get(name)} />{alerts > 0 && <Pill tone="warn">{alerts} alert{alerts === 1 ? '' : 's'}</Pill>}</div>
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
