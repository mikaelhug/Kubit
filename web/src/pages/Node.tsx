import { useEffect, useRef, useState } from 'preact/hooks'
import { api, fmt, logsUrl, type ClusterRow, type Inventory, type NodeDetail, type NodeRow, type NodeSpec, type PodSummary, type Service } from '../api'
import { useLocation } from 'preact-iso'
import { clusters, connected, machineList, machines, operations, refreshKey, resyncing, statuses, toast, watch } from '../store'
import { ReaddressDialog } from './cluster/Nodes'
import { RemoteManagement } from '../components/RemoteManagement'
import { AddVMsDialog, HostStateNotice, HostSystem, LabHostPanel, LabVMControls, MakeLabHostDialog, ReleaseHostDialog } from '../components/LabHost'
import { canAdopt, canMakeLabHost, canRetire, hostName, hostOf, isLabVM, kindDetail, kindLabel, KindPill, modelOf, TypePill } from '../machine'
import { Tabs } from '../components/Tabs'
import { DataTable, type Column } from '../components/DataTable'
import { Breadcrumbs, ConfirmDialog, Dialog, ErrorBox, Field, KeyValue, MaintenanceNotice, Meter, Notice, Pill, Section, StatusDot, stateTone } from '../components/ui'
import { elapsed } from '../clock'

type TabId = 'overview' | 'hardware' | 'kubernetes' | 'services' | 'logs' | 'actions' | 'labhost'

/** One machine: what Talos says, what Kubernetes says, and what can be done to it. */
export function NodePage({ ip: ipParam, mac }: { ip?: string; mac?: string; tab?: string }) {
  const [node, setNode] = useState<NodeRow | null>(null)
  const ip = node?.ip ?? ipParam ?? ''
  const [inv, setInv] = useState<Inventory | null>(null)
  const [invErr, setInvErr] = useState<string | null>(null)
  const [k8s, setK8s] = useState<NodeDetail | null>(null)
  const [k8sErr, setK8sErr] = useState<string | null>(null)
  const [tab, setTab] = useState<TabId>(() => (typeof location !== 'undefined' && (location.hash === '#actions' || location.hash === '#oob') ? 'actions' : location.hash === '#labhost' || location.pathname.startsWith('/labhosts/') ? 'labhost' : 'overview'))
  const [error, setError] = useState<string | null>(null)
  const finished = [...operations.value.values()].filter((o) => o.status !== 'running').length

  // The machine row is live state; the two Talos/Kubernetes detail calls follow the
  // cluster's status ticks and node-scope changes.
  const live = mac ? machines.value.get(mac.toLowerCase()) ?? null : machineList.value.find((n) => n.ip === ipParam) ?? null
  useEffect(() => {
    setNode(live)
    if (live && !mac) history.replaceState(null, '', `/machines/${live.mac}`)
    if (!live && connected.value && !resyncing.value) setError(`No machine ${mac ?? ipParam} is known.`)
  }, [live, mac, ipParam]) // eslint-disable-line
  const observed = live?.cluster ? statuses.value.get(live.cluster)?.observedAt : undefined
  useEffect(() => {
    if (!live) return
    if (live.talos) api.inventory(live.ip).then((i) => { setInv(i); setInvErr(null) }).catch((e) => setInvErr(e.message))
    else { setInv(null); setInvErr(null) }
    if (live.kind === 'member') api.nodeKubernetes(live.ip).then((d) => { setK8s(d); setK8sErr(null) }).catch((e) => setK8sErr(e.message))
    else { setK8s(null); setK8sErr(null) }
  }, [live?.ip, live?.kind, live?.talos, finished, observed, refreshKey(live?.cluster ?? '', 'nodes')]) // eslint-disable-line

  const cluster = node?.cluster ? clusters.value.find((c) => c.name === node.cluster) : undefined
  const spec: NodeSpec | undefined = cluster?.spec.spec.nodes.find((n) => (node?.mac && n.mac === node.mac) || n.hostname === node?.hostname)
  const title = node?.hostname || node?.labhost?.capacity.hostname || ip || mac || ''
  const running = [...operations.value.values()].filter((o) => o.status === 'running' && ((o.cluster === node?.cluster && (o.request as any)?.hostname === node?.hostname) || (o.request as any)?.mac === node?.mac))
  const kind = node?.kind
  const host = hostOf(node)
  const tabs: { id: TabId; label: string; badge?: number }[] = [
    { id: 'overview', label: 'Overview' }, { id: 'hardware', label: 'Hardware' },
    ...(kind === 'member' ? [{ id: 'kubernetes' as TabId, label: 'Kubernetes', badge: k8s?.pods?.length }] : []),
    ...(node?.talos ? [{ id: 'services' as TabId, label: 'Services' }, { id: 'logs' as TabId, label: 'Logs' }] : []),
    ...(kind === 'labhost' ? [{ id: 'labhost' as TabId, label: 'Lab host', badge: (node?.labhost?.vms ?? []).length }] : []),
    { id: 'actions', label: 'Actions' },
  ]
  const shown = node && !tabs.some((t) => t.id === tab) ? 'overview' : tab

  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 border-b border-border bg-panel/60">
        <Breadcrumbs items={node?.cluster ? [{ label: node.cluster, href: `/clusters/${node.cluster}/overview` }, { label: 'Nodes', href: `/clusters/${node.cluster}/nodes` }, { label: title }] : [{ label: 'Inventory', href: '/fleet/inventory' }, { label: title }]} />
        <div class="flex flex-wrap items-center gap-3 mt-2 mb-3">
          <h1 class="text-xl font-semibold">{title}</h1>
          {node && !node.cluster && <KindPill m={node} />}
          {node && node.kind !== 'labhost' && <TypePill m={node} />}
          {host && <a class="pill bg-panel-2 text-muted hover:text-fg" href={`/machines/${host.mac}#labhost`}>on {hostName(host)}</a>}
          {inv ? <Pill tone="good">Talos {inv.talosVersion}</Pill> : invErr ? <Pill tone="bad" title={invErr}>Talos not answering</Pill> : null}
          {k8s ? <Pill tone={k8s.ready ? 'good' : 'warn'}>{k8s.ready ? 'Ready' : 'NotReady'}</Pill> : null}
          {k8s?.unschedulable && <Pill tone="warn">cordoned</Pill>}
          {running.map((o) => <Pill key={o.id} tone="warn">{fmt.kind(o.kind)} running</Pill>)}
          <span class="mono text-muted text-[12px]">{[ip, node?.mac, node?.arch].filter(Boolean).join(' · ')}{spec ? ` · pool ${spec.pool}` : ''}{spec?.network ? ' · static' : ''}</span>
        </div>
        <Tabs active={shown} onSelect={(t) => setTab(t as TabId)} tabs={tabs} />
      </header>
      <div class="p-6 flex flex-col gap-5 max-w-[1300px]">
        <ErrorBox error={error} />
        {shown === 'overview' && <OverviewTab inv={inv} invErr={invErr} k8s={k8s} k8sErr={k8sErr} node={node} spec={spec} />}
        {shown === 'hardware' && <HardwareTab inv={inv} invErr={invErr} node={node} />}
        {shown === 'kubernetes' && <KubernetesTab k8s={k8s} err={k8sErr} />}
        {shown === 'services' && <ServicesTab ip={ip} cluster={node?.cluster} />}
        {shown === 'logs' && <LogsTab ip={ip} />}
        {shown === 'labhost' && node?.labhost && <LabHostPanel host={node} />}
        {shown === 'actions' && <ActionsTab node={node} k8s={k8s} inv={inv} cluster={cluster} spec={spec} talosVersion={cluster?.spec.spec.talosVersion} />}
      </div>
    </div>
  )
}

function OverviewTab({ inv, invErr, k8s, k8sErr, node, spec }: { inv: Inventory | null; invErr: string | null; k8s: NodeDetail | null; k8sErr: string | null; node: NodeRow | null; spec?: NodeSpec }) {
  if (!node) return <div class="text-muted">Loading…</div>
  const host = hostOf(node)
  const identity = (
    <Section title="Machine" help="What Kubit has recorded about this machine.">
      <div class="panel p-4">
        <KeyValue rows={[
          ['Kind', `${kindLabel[node.kind]}${kindDetail(node) ? ` · ${kindDetail(node)}` : ''}`],
          ['Model', [modelOf(node), inv?.platform].filter(Boolean).join(' · ')],
          ...(host ? [['Lab host', <a class="text-accent hover:underline" href={`/machines/${host.mac}#labhost`}>{hostName(host)}</a>] as [string, any]] : []),
          ['Identity', <span class="mono text-[12px]">{node.mac}{node.uuid ? ` · ${node.uuid}` : ''}{node.serial ? ` · ${node.serial}` : ''}</span>],
          ['Addresses seen', <span class="mono text-[12px]">{[...new Set([...(node.ipsSeen ?? []), node.ip])].filter(Boolean).join(' → ') || '—'}</span>],
          ['Last seen', fmt.datetime(node.lastSeen)],
          ...(spec ? [['Install disk', <span class="mono">{spec.installDisk?.path ?? (spec.installDisk?.selector ? JSON.stringify(spec.installDisk.selector) : 'pool policy')}</span>] as [string, any]] : []),
          ...(spec?.dataDisks?.length ? [['Data disks', <span class="mono">{spec.dataDisks.map((d, i) => `${d} → /var/mnt/data-${i + 1}`).join(' · ')}</span>] as [string, any]] : []),
        ]} />
      </div>
    </Section>
  )
  if (node.kind === 'labhost' && node.labhost) {
    const lh = node.labhost
    const vms = lh.vms ?? []
    return (
      <div class="flex flex-col gap-4">
        <HostStateNotice host={node} />
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {identity}
          <Section title="Capacity" help="Read over SSH; VMs are listed on the Lab host tab.">
            <div class="panel p-4">
              <KeyValue rows={[
                ['CPUs', String(lh.capacity.cpus || '—')],
                ['Memory', lh.capacity.memMiB ? fmt.bytes(lh.capacity.memMiB * 1048576) : '—'],
                ['VM disk free', lh.metrics?.diskTotal ? `${fmt.bytes(lh.metrics.diskTotal - lh.metrics.diskUsed)} of ${fmt.bytes(lh.metrics.diskTotal)}` : lh.capacity.diskGiB ? `${lh.capacity.diskGiB} GiB` : '—'],
                ['KVM', lh.capacity.kvm ? 'available' : lh.state === 'ready' ? 'absent' : '—'],
                ['VMs', <a class="text-accent hover:underline" href={`/machines/${node.mac}#labhost`}>{vms.length} defined · {vms.filter((v) => v.state === 'running').length} running</a>],
              ]} />
            </div>
          </Section>
        </div>
        {lh.state !== 'installing' && <HostSystem host={node} lh={lh} busy={lh.state !== 'ready'} />}
      </div>
    )
  }
  if (!node.talos) {
    const next = node.kind === 'configured' ? 'Runs Talos with a config Kubit did not apply. Reset it to maintenance mode to adopt it.'
      : node.kind === 'booting' ? 'Waiting for Talos maintenance mode; progress is in Activity.'
      : host ? 'The VM is off. Start it from the Actions tab.'
      : node.oobType ? 'Not running Talos. Boot into Talos from the Actions tab, or make it a lab host.'
      : 'Not running Talos. Boot it into Talos, or configure AMT or a BMC under Actions to do that from here.'
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
      <Section title="Talos" help="Read live from the machine over the Talos API.">
        <div class="panel p-4">
          {invErr && <Notice tone="bad">{invErr}</Notice>}
          {!inv && !invErr && <div class="text-muted">Loading…</div>}
          {inv && <KeyValue rows={[
            ['Version', <span class="mono">{inv.talosVersion}</span>],
            ['Stage', <Pill tone={inv.stage === 'running' ? 'good' : 'warn'}>{inv.stage}</Pill>],
            ['Booted', inv.bootTime ? `${fmt.datetime(inv.bootTime)} (up ${elapsed(inv.bootTime)})` : '—'],
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
          <div class="panel p-4 flex flex-col gap-4">
            {k8sErr && <Notice tone="bad">{k8sErr}</Notice>}
            {k8s && (
              <>
                <KeyValue rows={[
                  ['Kubelet', <span class="mono">{k8s.kubeletVersion}</span>],
                  ['Runtime', <span class="mono">{k8s.containerRuntime}</span>],
                  ['Kernel', <span class="mono">{k8s.kernel}</span>],
                  ['Internal IP', <span class="mono">{k8s.internalIP}</span>],
                  ['Taints', k8s.taints?.length ? k8s.taints.map((t) => <span class="mono block">{t}</span>) : 'none'],
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

function HardwareTab({ inv: live, invErr, node }: { inv: Inventory | null; invErr: string | null; node: NodeRow | null }) {
  if (!node) return <div class="text-muted">Loading…</div>
  const inv = live ?? node.inventory ?? null
  const lh = node.labhost
  const stored = !live && !!node.inventory
  if (node.talos && !inv && !invErr) return <div class="text-muted">Loading…</div>
  const note = invErr ? `${invErr} Showing what was recorded ${fmt.when(node.lastSeen)}.`
    : lh ? 'Recorded before the Debian install; capacity is read from the host.'
    : stored && inv?.disks.some((d) => !d.devPath) ? `Reported by the ${node.oobType === 'redfish' ? 'BMC' : 'management engine'}; device names arrive when the machine boots Talos.`
    : stored ? `Recorded ${fmt.when(node.lastSeen)}; the machine is not running Talos now.`
    : ''
  if (!inv || (!inv.cpus && inv.disks.length === 0 && !lh)) {
    return <Notice tone="muted">{inv ? `${modelOf(node)}. ` : ''}Hardware details arrive when the machine boots Talos.</Notice>
  }
  return (
    <div class="flex flex-col gap-5">
      {note && <Notice tone={invErr ? 'bad' : 'muted'}>{note}</Notice>}
      {lh && (
        <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <Stat label="CPUs" value={String(lh.capacity.cpus || inv.cpus)} sub={lh.capacity.arch || inv.arch} />
          <Stat label="Memory" value={lh.capacity.memMiB ? fmt.bytes(lh.capacity.memMiB * 1048576) : fmt.bytes(inv.memoryBytes)} />
          <Stat label="KVM" value={lh.capacity.kvm ? 'available' : lh.state === 'ready' ? 'absent' : '—'} sub="for the VMs" />
          <Stat label="VM disk free" value={lh.metrics?.diskTotal ? fmt.bytes(lh.metrics.diskTotal - lh.metrics.diskUsed) : lh.capacity.diskGiB ? `${lh.capacity.diskGiB} GiB` : '—'} sub={lh.disk ? `on ${lh.disk}` : undefined} />
        </div>
      )}
      {!lh && <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <Stat label="CPUs" value={String(inv.cpus)} sub={inv.arch} />
        <Stat label="Memory" value={fmt.bytes(inv.memoryBytes)} />
        <Stat label="KVM" value={inv.kvm ? 'available' : 'absent'} sub={inv.kvm ? 'runsc-kvm eligible' : 'gVisor uses systrap'} />
        <Stat label="Disks" value={String(inv.disks.length)} sub={fmt.bytes(inv.disks.reduce((a, d) => a + d.sizeBytes, 0)) + ' total'} />
      </div>}
      <Section title="Disks">
        <DataTable search={false} columns={[
          { id: 'dev', header: 'Device', mono: true, sort: (d) => d.devPath, cell: (d) => d.devPath || <span class="text-muted">—</span> },
          { id: 'size', header: 'Size', align: 'right', sort: (d) => d.sizeBytes, cell: (d) => fmt.bytes(d.sizeBytes) },
          { id: 'model', header: 'Model', sort: (d) => d.model ?? '', cell: (d) => d.model || <span class="text-muted">—</span> },
          { id: 'transport', header: 'Transport', sort: (d) => d.transport ?? '', cell: (d) => d.transport || '—' },
          { id: 'type', header: 'Type', cell: (d) => d.cdrom ? 'cdrom' : d.rotational ? 'HDD' : 'SSD/flash' },
          { id: 'flags', header: '', cell: (d) => d.readonly ? <Pill tone="muted">read-only</Pill> : null },
        ] as Column<Inventory['disks'][number]>[]} rows={inv.disks} rowKey={(d) => d.devPath} />
      </Section>
      <Section title="Network links" help="Physical links only; virtual interfaces (bridges, tunnels, CNI) are omitted.">
        <DataTable search={false} columns={[
          { id: 'name', header: 'Interface', mono: true, sort: (l) => l.name, cell: (l) => l.name },
          { id: 'mac', header: 'MAC', mono: true, cell: (l) => l.mac },
          { id: 'state', header: 'State', cell: (l) => <Pill tone={l.up ? 'good' : 'muted'}>{l.up ? 'up' : 'down'}</Pill> },
          { id: 'addr', header: 'Addresses', mono: true, cell: (l) => (l.addresses ?? []).join(', ') || '—' },
        ] as Column<Inventory['links'][number]>[]} rows={inv.links} rowKey={(l) => l.name} />
      </Section>
    </div>
  )
}

function KubernetesTab({ k8s, err }: { k8s: NodeDetail | null; err: string | null }) {
  if (err) return <Notice tone={err.includes('not a cluster member') ? 'muted' : 'bad'}>{err}</Notice>
  if (!k8s) return <div class="text-muted">Loading…</div>
  const pods = k8s.pods ?? []
  const columns: Column<PodSummary>[] = [
    { id: 'ns', header: 'Namespace', sort: (p) => p.namespace, cell: (p) => p.namespace },
    { id: 'name', header: 'Pod', sort: (p) => p.name, mono: true, cell: (p) => p.name },
    { id: 'phase', header: 'Phase', sort: (p) => p.phase, cell: (p) => <Pill tone={p.phase === 'Running' || p.phase === 'Succeeded' ? 'good' : p.phase === 'Pending' ? 'warn' : 'bad'}>{p.phase}</Pill> },
    { id: 'ready', header: 'Ready', cell: (p) => <span class="num">{p.ready}</span> },
    { id: 'restarts', header: 'Restarts', align: 'right', sort: (p) => p.restarts, cell: (p) => <span class={p.restarts > 3 ? 'text-warn' : ''}>{p.restarts}</span> },
    { id: 'owner', header: 'Owner', sort: (p) => p.owner ?? '', cell: (p) => p.owner || '—' },
    { id: 'cpu', header: 'CPU use / req', align: 'right', sort: (p) => p.usageCpuMilli ?? 0, cell: (p) => <>{fmt.cores(p.usageCpuMilli ?? 0)}<span class="text-muted"> / {p.cpuMilli ? fmt.cores(p.cpuMilli) : '—'}</span></> },
    { id: 'mem', header: 'Mem use / req', align: 'right', sort: (p) => p.usageMemBytes ?? 0, cell: (p) => <>{fmt.bytes(p.usageMemBytes ?? 0)}<span class="text-muted"> / {p.memBytes ? fmt.bytes(p.memBytes) : '—'}</span></> },
    { id: 'age', header: 'Age', cell: (p) => <span class="num text-muted">{p.age}</span> },
  ]
  return (
    <div class="flex flex-col gap-5">
      <Section title="Conditions">
        <div class="panel divide-y divide-border/60">
          {k8s.conditions.map((c) => {
            const bad = (c.type === 'Ready') !== (c.status === 'True')
            return (
              <div key={c.type} class="flex items-center gap-3 px-4 py-2 text-[13px]">
                <StatusDot tone={bad ? 'bad' : 'good'} />
                <span class="w-40 font-medium">{c.type}</span>
                <span class="mono w-14">{c.status}</span>
                <span class="text-muted truncate" title={c.message}>{c.message}</span>
                <span class="ml-auto text-muted num text-[12px]">since {fmt.datetime(c.since ?? '')}</span>
              </div>
            )
          })}
        </div>
      </Section>
      <Section title={`Pods on this node (${pods.length})`} help="Usage from metrics-server; requests from the pod specs. Draining evicts everything not owned by a DaemonSet.">
        <DataTable id="node-pods" columns={columns} rows={pods} rowKey={(p) => p.namespace + '/' + p.name} defaultSort={{ id: 'ns', dir: 'asc' }} />
      </Section>
      <Section title="Labels">
        <div class="panel p-3 flex flex-wrap gap-1.5">
          {Object.entries(k8s.labels).sort().map(([k, v]) => <span key={k} class="mono text-[11.5px] rounded bg-panel-2 px-1.5 py-0.5">{k}{v ? `=${v}` : ''}</span>)}
        </div>
      </Section>
    </div>
  )
}

function ServicesTab({ ip, cluster }: { ip: string; cluster?: string }) {
  const [services, setServices] = useState<Service[]>([])
  const [error, setError] = useState<string | null>(null)
  const tick = statuses.value.get(cluster ?? '')?.observedAt
  useEffect(() => { api.services(ip).then(setServices).catch((e) => setError(e.message)) }, [ip, tick])
  return (
    <Section title="Talos services" help="Talos system services and their own health checks.">
      <ErrorBox error={error} />
      <DataTable search={false} columns={[
        { id: 'id', header: 'Service', mono: true, sort: (s) => s.id, cell: (s) => s.id },
        { id: 'state', header: 'State', sort: (s) => s.state, cell: (s) => <Pill tone={s.healthy ? 'good' : s.state === 'Running' ? 'warn' : stateTone(s.state.toLowerCase())}>{s.state}{s.healthy ? '' : ' · unhealthy'}</Pill> },
        { id: 'last', header: 'Last event', cell: (s) => <span class="text-muted">{s.last}</span> },
      ] as Column<Service>[]} rows={services} rowKey={(s) => s.id} />
    </Section>
  )
}

function LogsTab({ ip }: { ip: string }) {
  const [services, setServices] = useState<Service[]>([])
  const [service, setService] = useState('')
  const [follow, setFollow] = useState(false)
  const [log, setLog] = useState('')
  const [q, setQ] = useState('')
  const [error, setError] = useState<string | null>(null)
  const abort = useRef<AbortController | null>(null)
  useEffect(() => { api.services(ip).then(setServices).catch(() => {}) }, [ip])
  useEffect(() => {
    abort.current?.abort()
    const ac = new AbortController()
    abort.current = ac
    setLog('')
    fetch(logsUrl(ip, service, follow), { signal: ac.signal }).then(async (res) => {
      if (!res.ok) throw new Error(await res.text())
      const reader = res.body!.getReader()
      const dec = new TextDecoder()
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        setLog((prev) => (prev + dec.decode(value)).slice(-300000))
      }
    }).catch((e) => { if (e.name !== 'AbortError') setError(e.message) })
    return () => ac.abort()
  }, [ip, service, follow])
  const lines = q ? log.split('\n').filter((l) => l.toLowerCase().includes(q.toLowerCase())).join('\n') : log
  return (
    <Section title={service ? `${service} log` : 'Kernel log (dmesg)'} actions={
      <>
        <select class="input !w-48" value={service} onChange={(e) => setService((e.target as HTMLSelectElement).value)}>
          <option value="">dmesg</option>
          {services.map((s) => <option key={s.id} value={s.id}>{s.id}</option>)}
        </select>
        <input class="input !w-56" placeholder="Search…" value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        <label class="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={follow} onChange={(e) => setFollow((e.target as HTMLInputElement).checked)} /> Follow</label>
        <button class="btn" onClick={() => navigator.clipboard.writeText(lines).then(() => toast('Copied'))}>Copy</button>
      </>
    }>
      <ErrorBox error={error} />
      <pre class="log !max-h-[70vh]" ref={(el) => { if (el && follow) el.scrollTop = el.scrollHeight }}>{lines || 'Loading…'}</pre>
    </Section>
  )
}

function ActionsTab({ node, k8s, inv, cluster, spec, talosVersion }: { node: NodeRow | null; k8s: NodeDetail | null; inv: Inventory | null; cluster?: ClusterRow; spec?: NodeSpec; talosVersion?: string }) {
  const [confirm, setConfirm] = useState<'drain' | 'reboot' | 'reboot-drain' | 'upgrade' | 'rename' | 'pool' | 'readdress' | null>(null)
  const [to, setTo] = useState('')
  const [pool, setPool] = useState('')
  if (!node?.cluster || !cluster || !node.hostname) return <MachineActions node={node} />
  const name = cluster.name
  const host = node.hostname
  const pods = (k8s?.pods ?? []).filter((p) => p.owner !== 'DaemonSet' && p.phase === 'Running').length
  const cp = node.role === 'controlplane'
  const run = (p: Promise<{ operationId: number }>) => p.then((r) => { setConfirm(null); watch(r) }).catch((e) => toast(e.message, 'error'))
  const needsUpgrade = inv && talosVersion && inv.talosVersion !== talosVersion
  const pools = (cluster.spec.spec.pools ?? []).filter((p) => p.role === node.role && p.name !== spec?.pool)
  const target = pools.find((p) => p.name === pool)
  const current = (cluster.spec.spec.pools ?? []).find((p) => p.name === spec?.pool)
  const delta = (a?: Record<string, string>, b?: Record<string, string>) => { const out: string[] = []; for (const k of Object.keys(a ?? {})) if (!(k in (b ?? {}))) out.push(`− ${k}`); for (const [k, v] of Object.entries(b ?? {})) if (a?.[k] !== v) out.push(`+ ${k}=${v}`); return out }
  const status = statuses.value.get(name)?.nodes.find((n) => n.hostname === host)
  return (
    <div class="flex flex-col gap-3 max-w-3xl">
      <Action title={k8s?.unschedulable ? 'Uncordon' : 'Cordon'} what={k8s?.unschedulable ? 'Allow new pods to be scheduled here again.' : 'Stop new pods from being scheduled here. Running pods are untouched.'}
        button={k8s?.unschedulable ? 'Uncordon' : 'Cordon'} disabled={!k8s} onClick={() => run(k8s?.unschedulable ? api.uncordon(name, host) : api.cordon(name, host))} />
      <Action title="Drain" what={`Cordon, then evict ${pods} running pod${pods === 1 ? '' : 's'} (DaemonSet pods stay). Use before maintenance; uncordon afterwards.`} button="Drain" disabled={!k8s} onClick={() => setConfirm('drain')} />
      <Action title="Reboot" what="Reboot via the Talos API and wait for Ready. Drain first to move pods." button="Reboot" disabled={!inv} onClick={() => setConfirm('reboot')} secondary={{ label: 'Drain, reboot, uncordon', onClick: () => setConfirm('reboot-drain') }} />
      <Action title="Upgrade Talos on this node" what={needsUpgrade ? `This node runs ${inv?.talosVersion}; the cluster declares ${talosVersion}. Upgrades just this node (A/B slot, automatic rollback on boot failure).` : `Already on the cluster's declared version ${talosVersion}. Change the version under the cluster's Settings to upgrade.`} button="Upgrade" disabled={!needsUpgrade} onClick={() => setConfirm('upgrade')} />
      <Action title="Rename" what="Change the hostname and the Kubernetes Node name. Pods are drained once and the old Node object is deleted; no reboot." button="Rename" disabled={!inv || !k8s} onClick={() => { setTo(host); setConfirm('rename') }} />
      <Action title="Move to another pool" what={pools.length ? `Same-role pools: ${pools.map((p) => p.name).join(', ')}. Labels and taints follow the pool; a different extension set means a re-image.` : `No other ${cp ? 'control-plane' : 'worker'} pool exists. Add one under Settings → Pools.`} button="Move" disabled={!inv || pools.length === 0} onClick={() => { setPool(pools[0]?.name ?? ''); setConfirm('pool') }} />
      <Action title="Update address" what={spec?.network ? `Static ${spec.network.addresses.join(', ')}${spec.network.vlan ? ` on VLAN ${spec.network.vlan}` : ''}. Change it or go back to DHCP.` : `DHCP; declared ${node.ip}${status?.seenAt && status.seenAt !== node.ip ? `, last seen at ${status.seenAt}` : ''}. Record a new lease or pin a static address.`} button="Update" disabled={!inv} onClick={() => setConfirm('readdress')} />
      <Action title="Remove from cluster" what={`Drain, delete the Node object, and reset Talos to maintenance mode. ${cp ? 'etcd membership is reduced by one.' : ''}`} button="Remove" href={`/clusters/${name}/nodes`} />
      {isLabVM(node) ? <LabVMControls vm={node} /> : <RemoteManagement node={node} />}
      {confirm === 'drain' && <ConfirmDialog title={`Drain ${host}`} action="Drain" cluster={name} onClose={() => setConfirm(null)} onConfirm={() => run(api.drain(name, host))}
        impact={<ul class="list-disc pl-5"><li>Cordons the node.</li><li>Evicts {pods} pod{pods === 1 ? '' : 's'}; controllers reschedule them on other nodes.</li><li>Waits up to 5 minutes; PodDisruptionBudgets are respected.</li></ul>} />}
      {(confirm === 'reboot' || confirm === 'reboot-drain') && <ConfirmDialog title={`Reboot ${host}`} action={confirm === 'reboot-drain' ? 'Drain and reboot' : 'Reboot'} tone="danger" cluster={name} onClose={() => setConfirm(null)} onConfirm={() => run(api.rebootNode(name, host, confirm === 'reboot-drain'))}
        impact={<ul class="list-disc pl-5">{confirm === 'reboot-drain' && <li>Drains {pods} pod{pods === 1 ? '' : 's'} first and uncordons afterwards.</li>}{confirm === 'reboot' && <li class="text-warn">Pods on this node go down until it returns (~1–2 min).</li>}{cp && <li>Control plane: etcd loses this member's vote while it is down.</li>}<li>Waits for the machine to answer with a new boot ID, then for Kubernetes Ready.</li></ul>} />}
      {confirm === 'upgrade' && <ConfirmDialog title={`Upgrade ${host} to Talos ${talosVersion}`} action="Upgrade node" cluster={name} onClose={() => setConfirm(null)} onConfirm={() => run(api.upgradeNode(name, host, talosVersion ?? ''))}
        impact={<ul class="list-disc pl-5"><li>Installs the new image into the inactive slot and reboots.</li><li>Talos rolls back on its own if the new system fails to boot.</li><li>Pods on this node are not drained first.</li></ul>} />}
      {confirm === 'rename' && (
        <Dialog title={`Rename ${host}`} onClose={() => setConfirm(null)} footer={<><button class="btn" onClick={() => setConfirm(null)}>Cancel</button><button class="btn btn-primary" disabled={!/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(to) || to === host || cluster.spec.spec.nodes.some((n) => n.hostname === to)} onClick={() => run(api.renameNode(name, host, to))}>Rename</button></>}>
          <MaintenanceNotice cluster={name} />
          <Field label="New hostname" hint="DNS label, unique in the cluster."><input class="input mono" value={to} onInput={(e) => setTo((e.target as HTMLInputElement).value.trim().toLowerCase())} /></Field>
          <ul class="list-disc pl-5 text-[13px] flex flex-col gap-1">
            <li>Drains {pods} pod{pods === 1 ? '' : 's'} (they reschedule elsewhere once), applies the new hostname without a reboot.</li>
            <li>The kubelet registers as <span class="mono">{to || '…'}</span>; the old Node object is deleted and the node is uncordoned.</li>
            {cp && <li>etcd is unaffected: members are tracked by ID, verified after the rename.</li>}
            <li>cluster.yaml, the machine record and the stored machine config are updated.</li>
          </ul>
        </Dialog>
      )}
      {confirm === 'pool' && (
        <Dialog title={`Move ${host} to another pool`} onClose={() => setConfirm(null)} footer={<><button class="btn" onClick={() => setConfirm(null)}>Cancel</button><button class="btn btn-primary" disabled={!target} onClick={() => run(api.moveNodePool(name, host, pool))}>Move</button></>}>
          <MaintenanceNotice cluster={name} />
          <Field label="Target pool">
            <select class="input" value={pool} onChange={(e) => setPool((e.target as HTMLSelectElement).value)}>{pools.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}</select>
          </Field>
          {target && (
            <ul class="list-disc pl-5 text-[13px] flex flex-col gap-1">
              <li>Labels: {delta(current?.labels, target.labels).length ? delta(current?.labels, target.labels).map((d) => <span class="mono mr-2">{d}</span>) : 'unchanged'}</li>
              <li>Taints: {delta(current?.taints, target.taints).length ? delta(current?.taints, target.taints).map((d) => <span class="mono mr-2">{d}</span>) : 'unchanged'}</li>
              {JSON.stringify(target.extensions ?? cluster.spec.spec.extensions ?? []) !== JSON.stringify(current?.extensions ?? cluster.spec.spec.extensions ?? []) ? <li class="text-warn">Different extensions: the node is re-imaged with the pool's installer (upgrade path, one reboot).</li> : <li>Same installer image: config is re-applied without a reboot.</li>}
            </ul>
          )}
        </Dialog>
      )}
      {confirm === 'readdress' && status && <ReaddressDialog cluster={cluster} n={status} spec={spec} onClose={() => setConfirm(null)} />}
    </div>
  )
}

/** Actions on a machine that is not (yet) a cluster member. */
function MachineActions({ node }: { node: NodeRow | null }) {
  const { route } = useLocation()
  const [retire, setRetire] = useState(false)
  const [lab, setLab] = useState(false)
  const [addVMs, setAddVMs] = useState(false)
  const [release, setRelease] = useState(false)
  if (!node) return <Notice tone="muted">Loading…</Notice>
  const ready = clusters.value.filter((c) => c.state === 'ready' || c.state === 'bootstrapped')
  const refresh = () => Promise.resolve()
  const lh = node.labhost
  const vm = isLabVM(node)
  const summary = node.kind === 'labhost' ? 'Lab host: Debian + KVM; Talos runs in its VMs.'
    : node.kind === 'maintenance' ? 'In Talos maintenance mode; not a cluster member.'
    : node.kind === 'configured' ? 'Runs Talos with a config Kubit did not apply. Reset it to maintenance mode to adopt it.'
    : node.kind === 'booting' ? 'Armed for a network boot; waiting for Talos.'
    : vm ? 'The VM is off.' : 'Not running Talos.'
  const adoptHint = node.kind === 'maintenance' ? '' : node.kind === 'labhost' ? '' : ' Needs Talos maintenance mode first.'
  return (
    <div class="flex flex-col gap-3 max-w-3xl">
      <Notice tone="muted">{summary}</Notice>
      {node.kind !== 'labhost' && <Action title="Adopt into a cluster" what={(ready.length ? `Join ${ready.map((c) => c.name).join(', ')} as a new node.` : 'No ready cluster yet; create one with this machine.') + adoptHint} button={ready.length ? 'Adopt' : 'New cluster'} disabled={!canAdopt(node)}
        onClick={() => { if (!ready.length) { route('/clusters/new'); return } const t = ready.length === 1 ? ready[0].name : prompt(`Adopt into which cluster? (${ready.map((c) => c.name).join(', ')})`, ready[0].name); if (t && ready.find((c) => c.name === t)) route(`/clusters/${t}/nodes?adopt=${node.ip}`) }} />}
      {lh && <Action title="Add VMs" what={`${(lh.vms ?? []).length} VM${(lh.vms ?? []).length === 1 ? '' : 's'} defined. New VMs boot Talos in maintenance mode and appear in the inventory.`} button="Add VMs" disabled={lh.state !== 'ready'} onClick={() => setAddVMs(true)} />}
      {lh && <Action title="Release lab host" what="Deletes every VM and drops the lab-host role. Debian stays on the disk." button="Release" disabled={lh.state === 'installing' || lh.state === 'setup' || lh.state === 'updating'} onClick={() => setRelease(true)} />}
      {vm ? <LabVMControls vm={node} /> : <RemoteManagement node={node} />}
      {canMakeLabHost(node) && <Action title="Make lab host" what={node.oobType ? 'Install Debian + KVM/libvirt on this machine (disk wiped) and carve Talos VMs from it. Reset by remote management; kubit pxe serves the installer.' : 'Install Debian + KVM/libvirt on this machine (disk wiped) and carve Talos VMs from it. No remote management here: you boot the installer yourself with the boot line Kubit prints.'} button="Make lab host" onClick={() => setLab(true)} />}
      {lab && <MakeLabHostDialog m={node} onClose={() => setLab(false)} />}
      {addVMs && <AddVMsDialog host={node} onClose={() => setAddVMs(false)} />}
      {release && <ReleaseHostDialog host={node} onClose={() => setRelease(false)} />}
      {!vm && <Action title="Wake-on-LAN" what={node.wol ? 'Enabled: Kubit can send a magic packet from this host to power the machine on.' : 'Off. Enable when the firmware supports WoL on the uplink NIC.'} button={node.wol ? 'Wake now' : 'Enable'}
        onClick={() => (node.wol ? api.wake(node.mac).then(() => toast('Magic packet sent', 'good')) : api.setWOL(node.mac, true).then(refresh)).catch((e) => toast(e.message, 'error'))}
        secondary={node.wol ? { label: 'Disable', onClick: () => api.setWOL(node.mac, false).then(refresh).catch((e) => toast(e.message, 'error')) } : undefined} />}
      {canRetire(node) && <Action title="Retire" what="Delete this machine's record. It reappears on the next scan if still on the network." button="Retire" onClick={() => setRetire(true)} />}
      {retire && <ConfirmDialog title={`Retire ${node.hostname || node.mac}`} action="Retire" tone="danger" onClose={() => setRetire(false)} onConfirm={() => api.retireMachine(node.mac).then(() => route('/fleet/inventory')).catch((e) => toast(e.message, 'error'))}
        impact={<p>Deletes the inventory row for <span class="mono">{node.mac}</span>. Nothing is sent to the machine.</p>} />}
    </div>
  )
}

function Action({ title, what, button, disabled, onClick, href, secondary }: { title: string; what: string; button: string; disabled?: boolean; onClick?: () => void; href?: string; secondary?: { label: string; onClick: () => void } }) {
  return (
    <div class="panel p-4 flex items-center gap-4">
      <div class="flex-1 min-w-0">
        <div class="font-medium">{title}</div>
        <p class="text-[12.5px] text-muted">{what}</p>
      </div>
      <div class="flex gap-2 shrink-0">
        {secondary && <button class="btn" disabled={disabled} onClick={secondary.onClick}>{secondary.label}</button>}
        {href ? <a href={href} class="btn">{button}</a> : <button class="btn btn-primary" disabled={disabled} onClick={onClick}>{button}</button>}
      </div>
    </div>
  )
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div class="panel p-4 flex flex-col gap-1">
      <span class="label">{label}</span>
      <span class="text-2xl font-semibold num">{value}</span>
      {sub && <span class="text-[12px] text-muted">{sub}</span>}
    </div>
  )
}
