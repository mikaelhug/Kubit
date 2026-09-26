import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type NodeRow } from '../api'
import { AlertGroup } from '../components/Alerts'
import { ThisMacDialog } from '../components/labhost'
import { Elapsed } from '../components/Time'
import { ClusterPill, Notice, Pill, Section, StatusDot, Tile } from '../components/ui'
import { groupOf, hostName, labHostKey, labOffline, labState, vmsOf, type MachineGroup } from '../machine'
import { authState, clusters, daemon, kubitKey, labHosts, loadAllHealth, machineList, machines, observer, openAlerts, opList, settings, statuses, versions } from '../store'
import { stateTone, type Tone } from '../tone'
import { useLive } from '../useLive'
import { defaultTalos, talosIso, updatesFor } from '../versions'

export function Home() {
  const list = clusters.value
  const hosts = labHosts.value
  const physical = machineList.value.filter((m) => !m.host)
  const keys = [kubitKey, ...list.map((c) => c.name), ...hosts.map((h) => labHostKey(h.mac))]
  useEffect(() => { loadAllHealth(keys) }, [keys.join(',')])
  const armed = machineList.value.some((m) => m.provision)
  const { data: pxe } = useLive(() => api.pxe(), [], [['', 'pxe']], { onError: 'silent', enabled: armed })
  const offsiteOn = !!settings.value?.offsite.type
  const { data: off } = useLive(() => api.offsiteStatus(), [], [['', 'offsite']], { onError: 'silent', enabled: offsiteOn })

  if (list.length === 0 && hosts.length === 0 && physical.length === 0) return <Welcome />

  const updates = new Map(list.map((c) => [c.name, updatesFor(c.spec.spec.talosVersion, c.spec.spec.kubernetesVersion, versions.value)]))
  const groups = [
    ...list.map((c) => ({ key: c.name, label: c.name, href: `/clusters/${c.name}/overview`, alerts: openAlerts(c.name) })),
    ...hosts.map((h) => ({ key: labHostKey(h.mac), label: hostName(h), href: `/labhosts/${h.mac}/overview`, alerts: openAlerts(labHostKey(h.mac)) })),
    { key: kubitKey, label: 'Kubit', href: '/settings/general', alerts: openAlerts(kubitKey).filter((e) => e.kind !== 'test') },
  ].filter((g) => g.alerts.length > 0)
  const notices: { tone: Tone; text: ComponentChildren }[] = []
  const obs = observer.value
  if (!obs.online) notices.push({ tone: 'bad', text: <>Kubit cannot reach the local network{obs.since ? ` since ${fmt.when(obs.since)}` : ''}{obs.error ? ` (${obs.error})` : ''}; cluster alerts are paused. <a class="underline" href="/settings/general">Details</a></> })
  if (authState.value.setup) notices.push({ tone: 'warn', text: <>No accounts: anyone reaching this address is an administrator. <a class="underline" href="/settings/accounts">Add the first account</a></> })
  if (armed && pxe && !pxe.running) notices.push({ tone: 'bad', text: <>A machine is armed for a network boot but the PXE server is not running. <a class="underline" href="/fleet/network-boot">Network boot</a></> })
  if (off?.error) notices.push({ tone: 'bad', text: <>Off-site copies failing: {off.error} <a class="underline" href="/settings/offsite">Off-site</a></> })
  for (const c of list) {
    const u = updates.get(c.name)!
    if (u.talos || u.kubernetes) notices.push({ tone: 'info', text: <>{c.name}: update available{u.talos ? ` · Talos ${u.talos}` : ''}{u.kubernetes ? ` · Kubernetes ${u.kubernetes}` : ''}. <a class="underline" href={`/clusters/${c.name}/lifecycle`}>Lifecycle</a></> })
  }
  const quiet = groups.length === 0 && notices.length === 0
  const counts: Record<MachineGroup, number> = { available: 0, boot: 0, 'in-use': 0 }
  for (const m of physical) counts[groupOf(m)]++

  return (
    <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
      <Section title="Needs attention" help={quiet ? undefined : 'Open alerts and Kubit notices.'}>
        {quiet && <div class="panel p-3 text-[13px] text-muted">Nothing needs attention.</div>}
        {notices.map((n, i) => <Notice key={i} tone={n.tone}>{n.text}</Notice>)}
        {groups.map((g) => <AlertGroup key={g.key} id={g.key} alerts={g.alerts} label={g.label} href={g.href} />)}
      </Section>

      <Section title="Clusters" actions={<a href="/clusters/new" class="btn btn-primary">+ New cluster</a>}>
        {list.length === 0 && <div class="panel p-3 text-[13px] text-muted">No cluster yet.</div>}
        <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {list.map((c) => { const u = updates.get(c.name)!; return <ClusterCard key={c.name} name={c.name} state={c.state} talos={c.spec.spec.talosVersion} k8s={c.spec.spec.kubernetesVersion} update={!!(u.talos || u.kubernetes)} alerts={openAlerts(c.name).length} /> })}
        </div>
      </Section>

      {hosts.length > 0 && (
        <Section title="Lab hosts">
          <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {hosts.map((h) => <LabHostCard key={h.mac} h={h} alerts={openAlerts(labHostKey(h.mac)).length} />)}
          </div>
        </Section>
      )}

      <Section title="Machines" actions={<a href="/fleet/inventory" class="btn">Inventory</a>}>
        <div class="grid grid-cols-3 gap-4">
          <Tile label="Available" value={counts.available} href="/fleet/inventory?filter=available" sub="in Talos maintenance mode" tone={counts.available > 0 ? 'good' : undefined} />
          <Tile label="Needs boot" value={counts.boot} href="/fleet/inventory?filter=boot" sub="not running Talos yet" tone={counts.boot > 0 ? 'warn' : undefined} />
          <Tile label="In use" value={counts['in-use']} href="/fleet/inventory?filter=in-use" sub="cluster members and lab hosts" />
        </div>
      </Section>

      <Section title="Activity" actions={<a href="/operations" class="btn">All activity</a>}>
        <ActivityList />
      </Section>
    </div>
  )
}

function ActivityList() {
  const ops = [...opList.value].sort((a, b) => b.id - a.id)
  const shown = [...ops.filter((o) => o.status === 'running'), ...ops.filter((o) => o.status !== 'running').slice(0, 5)]
  const byHostKey = new Map([...machines.value.values()].filter((m) => m.labhost).map((m) => [labHostKey(m.mac), m]))
  return (
    <div class="panel divide-y divide-border/60">
      {shown.length === 0 && <div class="p-4 text-[13px] text-muted">Nothing yet.</div>}
      {shown.map((o) => {
        const step = (o.steps ?? []).find((s) => s.status === 'running')
        return (
          <a key={o.id} href={`/operations/${o.id}`} class="flex items-center gap-3 px-4 py-2 text-[13px] hover:bg-panel-2">
            <StatusDot tone={stateTone(o.status)} pulse={o.status === 'running'} />
            <span class="font-medium">{fmt.kind(o.kind)}</span>
            {o.cluster && <span class="text-muted">{o.cluster.startsWith('labhost:') ? hostName(byHostKey.get(o.cluster)) : o.cluster}</span>}
            {step && <span class="text-muted truncate">{step.title}</span>}
            <span class="ml-auto text-muted">{o.status === 'running' ? <Elapsed from={o.startedAt} /> : fmt.when(o.startedAt)}</span>
            <Pill tone={stateTone(o.status)}>{o.status}</Pill>
          </a>
        )
      })}
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

function LabHostCard({ h, alerts }: { h: NodeRow; alerts: number }) {
  const lh = h.labhost!
  const m = lh.metrics
  const vms = vmsOf(lh)
  const u = lh.updates
  const pct = (a: number, b: number) => (b ? `${fmt.pct(a, b)}%` : '—')
  const offline = labOffline(lh)
  return (
    <a href={`/labhosts/${h.mac}/overview`} class="panel p-3 flex flex-col gap-2 hover:border-accent min-w-0">
      <div class="flex items-center gap-2"><span class="font-semibold truncate">{hostName(h)}</span><Pill tone={stateTone(labState(lh))}>{labState(lh)}</Pill>{alerts > 0 && <Pill tone="warn">{alerts} alert{alerts === 1 ? '' : 's'}</Pill>}</div>
      <div class={`text-[13px] flex gap-3 ${offline ? 'text-muted' : ''}`}>
        <span>{m ? `${Math.round(m.cpuPct)}%` : '—'} <span class="text-muted">cpu</span></span>
        <span>{m ? pct(m.memUsed, m.memTotal) : '—'} <span class="text-muted">memory</span></span>
        <span>{m ? pct(m.diskUsed, m.diskTotal) : '—'} <span class="text-muted">vm disk</span></span>
      </div>
      <div class="text-[12px] text-muted">{offline ? `${vms.length} VM${vms.length === 1 ? '' : 's'}` : `${vms.filter((v) => v.state === 'running').length}/${vms.length} VMs running`}{u && (u.count > 0 || u.rebootRequired) ? ` · ${u.count} update${u.count === 1 ? '' : 's'}${u.rebootRequired ? ', reboot required' : ''}` : ''}</div>
    </a>
  )
}

function Welcome() {
  const factory = settings.value?.factoryUrl ?? 'https://factory.talos.dev'
  const talos = defaultTalos(versions.value)
  const [mac, setMac] = useState(false)
  return (
    <div class="p-8 max-w-3xl flex flex-col gap-6">
      <div>
        <h1 class="text-2xl font-semibold">Welcome to Kubit</h1>
        <p class="text-muted mt-1">Three steps from bare machines to a Talos Kubernetes cluster.</p>
      </div>
      <Step n={1} title="Boot the machines into Talos maintenance mode">
        <p>Boot the ISO from USB or a VM; Talos {talos} runs in memory and leaves the disk untouched.</p>
        <div class="flex flex-wrap gap-2 mt-2">
          <a class="btn btn-primary" href={talosIso(factory, talos, 'amd64')}>Download ISO · amd64</a>
          <a class="btn" href={talosIso(factory, talos, 'arm64')}>Download ISO · arm64</a>
          <a class="btn" href="/fleet/network-boot">Boot over the network</a>
        </div>
      </Step>
      <Step n={2} title="Discover them">
        <p>Scan the subnet from Inventory.</p>
        <a class="btn mt-2 self-start" href="/fleet/inventory">Open Inventory</a>
      </Step>
      <Step n={3} title="Design and create the cluster">
        <p>Pick the machines; Kubit proposes and checks the design.</p>
        <a class="btn btn-primary mt-2 self-start" href="/clusters/new">Create a cluster</a>
      </Step>
      {daemon.value?.os === 'darwin' && (
        <div class="panel p-4 flex items-center gap-4">
          <div class="flex-1 text-[13px]"><div class="font-medium">Or run a cluster on this Mac</div><p class="text-muted">Talos VMs under vfkit, created and joined in one step.</p></div>
          <button class="btn shrink-0" onClick={() => setMac(true)}>Lab host on this Mac</button>
        </div>
      )}
      <p class="text-[13px] text-muted">A machine with AMT or a BMC can become a lab host from Inventory.</p>
      {mac && <ThisMacDialog onClose={() => setMac(false)} />}
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
