import { useEffect, useState } from 'preact/hooks'
import { api, fmt, kubitKey, labHostKey, type HealthEvent, type NodeRow, type OffsiteStatus, type PxeStatus, type Versions } from '../api'
import { authState, clusters, health, latestTalos, loadAllHealth, machineList, observer, operations, refreshKey, settings, statuses, ack } from '../store'
import { ClusterPill, Notice, Pill, Section, StatusDot, stateTone } from '../components/ui'
import { EventRow, verLess } from './cluster/Overview'
import { groupOf, hostName, type MachineGroup } from '../machine'
import { elapsed } from '../clock'

const vanillaSchematic = '376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba'

/** The fleet: what needs attention, every cluster and lab host, machines by next step, activity. */
export function Home() {
  const list = clusters.value
  const machines = machineList.value
  const hosts = machines.filter((m) => m.labhost)
  const physical = machines.filter((m) => !m.host)
  const keys = [kubitKey, ...list.map((c) => c.name), ...hosts.map((h) => labHostKey(h.mac))]
  useEffect(() => { loadAllHealth(keys) }, [keys.join(',')]) // eslint-disable-line
  const [versions, setVersions] = useState<Versions | null>(null)
  useEffect(() => { api.versions().then(setVersions).catch(() => {}) }, [latestTalos.value])
  const armed = machines.some((m) => m.provision)
  const [pxe, setPxe] = useState<PxeStatus | null>(null)
  useEffect(() => { if (armed) api.pxe().then(setPxe).catch(() => {}); else setPxe(null) }, [armed, refreshKey('', 'pxe')])
  const offsiteOn = !!settings.value?.offsite.type
  const finished = [...operations.value.values()].filter((o) => o.status !== 'running').length
  const [off, setOff] = useState<OffsiteStatus | null>(null)
  useEffect(() => { if (offsiteOn) api.offsiteStatus().then(setOff).catch(() => {}); else setOff(null) }, [offsiteOn, finished])

  if (list.length === 0 && hosts.length === 0 && physical.length === 0) return <Welcome versions={versions} />

  const latest = versions?.talos.find((v) => !v.includes('-'))
  const updates = list.filter((c) => (latest && verLess(c.spec.spec.talosVersion, latest)) || (versions && verLess(c.spec.spec.kubernetesVersion, versions.kubernetesLatest)))
  const alertsOf = (key: string) => (health.value.get(key) ?? []).filter((e) => !e.acked && e.severity !== 'info')
  const groups: { key: string; label: string; href: string; alerts: HealthEvent[] }[] = [
    ...list.map((c) => ({ key: c.name, label: c.name, href: `/clusters/${c.name}/overview`, alerts: alertsOf(c.name) })),
    ...hosts.map((h) => ({ key: labHostKey(h.mac), label: hostName(h), href: `/labhosts/${h.mac}/overview`, alerts: alertsOf(labHostKey(h.mac)) })),
    { key: kubitKey, label: 'Kubit', href: '/settings/general', alerts: alertsOf(kubitKey).filter((e) => e.kind !== 'test') },
  ].filter((g) => g.alerts.length > 0)
  const notices: { tone: 'warn' | 'bad' | 'info'; text: preact.ComponentChildren }[] = []
  const obs = observer.value
  if (!obs.online) notices.push({ tone: 'bad', text: <>Kubit cannot reach the local network{obs.since ? ` since ${fmt.when(obs.since)}` : ''}{obs.error ? ` (${obs.error})` : ''}. Cluster alerts are paused. <a class="underline" href="/settings/general">Why</a></> })
  if (obs.gaps24h >= 3) notices.push({ tone: 'warn', text: <>Observation paused {obs.gaps24h} times in 24 h: Kubit's host sleeps. Run Kubit on an always-on machine (<span class="mono">kubit service install</span>).</> })
  if (authState.value.setup) notices.push({ tone: 'warn', text: <>No accounts: anyone reaching this address is an administrator. <a class="underline" href="/settings/accounts">Add the first account</a></> })
  if (armed && pxe && !pxe.running) notices.push({ tone: 'bad', text: <>A machine is armed for a network boot but the PXE server is not running. <a class="underline" href="/fleet/network-boot">Network boot</a></> })
  if (off?.error) notices.push({ tone: 'bad', text: <>Off-site copies failing: {off.error} <a class="underline" href="/settings/offsite">Off-site</a></> })
  for (const c of updates) notices.push({ tone: 'info', text: <>{c.name}: update available{latest && verLess(c.spec.spec.talosVersion, latest) ? ` · Talos ${latest}` : ''}{versions && verLess(c.spec.spec.kubernetesVersion, versions.kubernetesLatest) ? ` · Kubernetes ${versions.kubernetesLatest}` : ''}. <a class="underline" href={`/clusters/${c.name}/lifecycle`}>Lifecycle</a></> })
  const quiet = groups.length === 0 && notices.length === 0
  const counts: Record<MachineGroup, number> = { available: 0, boot: 0, 'in-use': 0 }
  for (const m of physical) counts[groupOf(m)]++
  const ops = [...operations.value.values()].sort((a, b) => b.id - a.id)
  const running = ops.filter((o) => o.status === 'running')
  const recent = ops.filter((o) => o.status !== 'running').slice(0, 5)

  return (
    <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
      <Section title="Needs attention" help={quiet ? undefined : 'Open alerts across every cluster and lab host, and Kubit notices.'}>
        {quiet && <div class="panel p-3 text-[13px] text-muted">Nothing needs attention.</div>}
        {notices.map((n, i) => <Notice key={i} tone={n.tone}>{n.text}</Notice>)}
        {groups.map((g) => (
          <div key={g.key} class="panel border-warn/50">
            <div class="flex items-center gap-2 px-4 py-2 border-b border-border">
              <a href={g.href} class="font-semibold hover:underline">{g.label}</a>
              <span class="text-[12px] text-muted">{g.alerts.length} active alert{g.alerts.length === 1 ? '' : 's'}</span>
              <button class="btn !py-0.5 !px-2 text-[12px] ml-auto" onClick={() => ack(g.key)}>Acknowledge all</button>
            </div>
            {g.alerts.map((e) => <EventRow key={e.id} e={e} onAck={() => ack(g.key, e.id)} />)}
          </div>
        ))}
      </Section>

      <Section title="Clusters" actions={<a href="/clusters/new" class="btn btn-primary">+ New cluster</a>}>
        {list.length === 0 && <div class="panel p-3 text-[13px] text-muted">No cluster yet.</div>}
        <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {list.map((c) => <ClusterCard key={c.name} name={c.name} state={c.state} talos={c.spec.spec.talosVersion} k8s={c.spec.spec.kubernetesVersion} update={updates.includes(c)} alerts={alertsOf(c.name).length} />)}
        </div>
      </Section>

      {hosts.length > 0 && (
        <Section title="Lab hosts">
          <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {hosts.map((h) => <LabHostCard key={h.mac} h={h} alerts={alertsOf(labHostKey(h.mac)).length} />)}
          </div>
        </Section>
      )}

      <Section title="Machines" actions={<a href="/fleet/inventory" class="btn">Inventory</a>}>
        <div class="grid grid-cols-3 gap-4">
          <Counter label="Available" value={counts.available} href="/fleet/inventory?filter=available" sub="in Talos maintenance mode" tone={counts.available > 0 ? 'good' : 'muted'} />
          <Counter label="Needs boot" value={counts.boot} href="/fleet/inventory?filter=boot" sub="not running Talos yet" tone={counts.boot > 0 ? 'warn' : 'muted'} />
          <Counter label="In use" value={counts['in-use']} href="/fleet/inventory?filter=in-use" sub="cluster members and lab hosts" tone="muted" />
        </div>
      </Section>

      <Section title="Activity" actions={<a href="/operations" class="btn">All activity</a>}>
        <div class="panel divide-y divide-border/60">
          {running.length === 0 && recent.length === 0 && <div class="p-4 text-[13px] text-muted">Nothing yet.</div>}
          {[...running, ...recent].map((o) => {
            const step = (o.steps ?? []).find((s) => s.status === 'running')
            return (
              <a key={o.id} href={`/operations/${o.id}`} class="flex items-center gap-3 px-4 py-2 text-[13px] hover:bg-panel-2">
                <StatusDot tone={stateTone(o.status)} pulse={o.status === 'running'} />
                <span class="font-medium">{fmt.kind(o.kind)}</span>
                {o.cluster && <span class="text-muted">{o.cluster.startsWith('labhost:') ? hostName(machines.find((m) => labHostKey(m.mac) === o.cluster)) : o.cluster}</span>}
                {step && <span class="text-muted truncate">{step.title}</span>}
                <span class="ml-auto text-muted num">{o.status === 'running' ? elapsed(o.startedAt) : fmt.when(o.startedAt)}</span>
                <Pill tone={stateTone(o.status)}>{o.status}</Pill>
              </a>
            )
          })}
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
      <div class="text-[13px] num flex gap-3">
        <span class={t && t.nodesReady < t.nodes ? 'text-warn' : ''}>{t ? `${t.nodesReady}/${t.nodes}` : '—'} <span class="text-muted">nodes</span></span>
        <span class={st && !st.etcd.healthy ? 'text-bad' : ''}>{st ? `${st.etcd.members}/${st.etcd.expected}` : '—'} <span class="text-muted">etcd</span></span>
        <span>{t?.pods ?? '—'} <span class="text-muted">pods</span></span>
      </div>
      <div class="text-[12px] text-muted mono flex items-center gap-2 min-w-0"><span class="truncate">Talos {talos} · Kubernetes {k8s}</span>{update && <Pill tone="info">update</Pill>}</div>
      <div class="text-[12px] text-muted">{st?.lastSnapshotAt ? `last etcd snapshot ${fmt.when(st.lastSnapshotAt)}` : 'no etcd snapshot yet'}</div>
    </a>
  )
}

function LabHostCard({ h, alerts }: { h: NodeRow; alerts: number }) {
  const lh = h.labhost!
  const m = lh.metrics
  const vms = lh.vms ?? []
  const u = lh.updates
  const pct = (a: number, b: number) => (b ? `${fmt.pct(a, b)}%` : '—')
  return (
    <a href={`/labhosts/${h.mac}/overview`} class="panel p-3 flex flex-col gap-2 hover:border-accent min-w-0">
      <div class="flex items-center gap-2"><span class="font-semibold truncate">{hostName(h)}</span><Pill tone={stateTone(lh.state)}>{lh.state}</Pill>{alerts > 0 && <Pill tone="warn">{alerts} alert{alerts === 1 ? '' : 's'}</Pill>}</div>
      <div class="text-[13px] num flex gap-3">
        <span>{m ? `${Math.round(m.cpuPct)}%` : '—'} <span class="text-muted">cpu</span></span>
        <span>{m ? pct(m.memUsed, m.memTotal) : '—'} <span class="text-muted">memory</span></span>
        <span>{m ? pct(m.diskUsed, m.diskTotal) : '—'} <span class="text-muted">vm disk</span></span>
      </div>
      <div class="text-[12px] text-muted">{vms.filter((v) => v.state === 'running').length}/{vms.length} VMs running{u && (u.count > 0 || u.rebootRequired) ? ` · ${u.count} update${u.count === 1 ? '' : 's'}${u.rebootRequired ? ', reboot required' : ''}` : ''}</div>
    </a>
  )
}

function Counter({ label, value, href, sub, tone }: { label: string; value: number; href: string; sub: string; tone: 'good' | 'warn' | 'muted' }) {
  const color = { good: 'text-good', warn: 'text-warn', muted: '' }[tone]
  return (
    <a href={href} class="panel p-3 flex flex-col gap-1 hover:border-accent">
      <span class="label">{label}</span>
      <span class={`text-2xl font-semibold num ${color}`}>{value}</span>
      <span class="text-[12px] text-muted">{sub}</span>
    </a>
  )
}

/** Nothing known yet: the three steps to a cluster. */
function Welcome({ versions }: { versions: Versions | null }) {
  const factory = settings.value?.factoryUrl ?? 'https://factory.talos.dev'
  const talos = versions?.talos.find((v) => !v.includes('-')) ?? versions?.minTalos ?? 'v1.14.0'
  const iso = (arch: 'amd64' | 'arm64') => `${factory}/image/${vanillaSchematic}/${talos}/metal-${arch}.iso`
  return (
    <div class="p-8 max-w-3xl flex flex-col gap-6">
      <div>
        <h1 class="text-2xl font-semibold">Welcome to Kubit</h1>
        <p class="text-muted mt-1">Three steps from bare machines to a Talos Kubernetes cluster.</p>
      </div>
      <Step n={1} title="Boot the machines into Talos maintenance mode">
        <p>Write the ISO to a USB stick or attach it to a VM. Talos {talos} starts in memory and waits on port 50000; the disk is untouched.</p>
        <div class="flex flex-wrap gap-2 mt-2">
          <a class="btn btn-primary" href={iso('amd64')}>Download ISO · amd64</a>
          <a class="btn" href={iso('arm64')}>Download ISO · arm64</a>
          <a class="btn" href="/fleet/network-boot">Boot over the network</a>
        </div>
      </Step>
      <Step n={2} title="Discover them">
        <p>Scan the subnet from Inventory. Machines are identified by MAC.</p>
        <a class="btn mt-2 self-start" href="/fleet/inventory">Open Inventory</a>
      </Step>
      <Step n={3} title="Design and create the cluster">
        <p>Pick the machines; Kubit proposes roles, a VIP and a LoadBalancer range and lints the design.</p>
        <a class="btn btn-primary mt-2 self-start" href="/clusters/new">Create a cluster</a>
      </Step>
      <p class="text-[13px] text-muted">One machine with AMT or a BMC can instead become a lab host: Debian + KVM installed by Kubit, Talos VMs carved from it. Add it by remote management in Inventory, then <b>Make lab host</b>.</p>
    </div>
  )
}

function Step({ n, title, children }: { n: number; title: string; children: preact.ComponentChildren }) {
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
