import { useCallback, useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, fmt, type ClusterRow, type NodeRow, type NodeSpec, type Operation, type Status } from '../api'
import { useOperationUpdates } from '../events'
import { Dialog, ErrorBox, Field, Meter, Pill, stateTone } from '../components/ui'
import { OperationPanel } from '../components/OperationPanel'

export function Dashboard({ name }: { name: string }) {
  const { route } = useLocation()
  const [cluster, setCluster] = useState<ClusterRow | null>(null)
  const [status, setStatus] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [op, setOp] = useState<{ id: number; title: string } | null>(null)
  const [dialog, setDialog] = useState<'add' | 'upgrade' | 'yaml' | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const refresh = useCallback(() => {
    api.cluster(name).then(setCluster).catch((e) => setError(e.message))
    api.status(name).then((s) => { setStatus(s); setError(null) }).catch((e) => setError(e.message))
  }, [name])
  useEffect(() => { setStatus(null); setOp(null); refresh(); const t = setInterval(refresh, 10000); return () => clearInterval(t) }, [refresh])
  useOperationUpdates(useCallback((o: Operation) => { if (o.cluster === name && o.status !== 'running') refresh() }, [name, refresh]))

  const run = (title: string, p: Promise<{ operationId: number }>) =>
    p.then((r) => setOp({ id: r.operationId, title })).catch((e) => setError(e.message))

  const forget = async () => {
    if (!confirm(`Forget cluster ${name}? Nodes are left untouched; only Kubit's records and secrets are removed.`)) return
    await api.forgetCluster(name).catch((e) => setError(e.message))
    route('/')
  }

  if (!cluster) return <div class="p-8 text-muted">{error ?? 'Loading…'}</div>
  const spec = cluster.spec.spec
  const t = status?.totals
  const busy = op !== null

  return (
    <div class="p-6 flex flex-col gap-5 max-w-[1200px]">
      <header class="flex flex-wrap items-center gap-3">
        <h1 class="text-xl font-semibold">{name}</h1>
        <Pill tone={stateTone(cluster.state)}>{cluster.state}</Pill>
        <span class="mono text-muted">Talos {spec.talosVersion} · Kubernetes {spec.kubernetesVersion} · {spec.controlPlane.endpoint}</span>
        <div class="ml-auto flex gap-2">
          <button class="btn" onClick={() => setDialog('add')}>+ Add node</button>
          <button class="btn" onClick={() => setDialog('upgrade')}>Upgrade</button>
          <button class="btn" onClick={() => setDialog('yaml')}>cluster.yaml</button>
          <button class="btn" onClick={() => api.exportCluster(name).then((r) => setNotice(`Exported to ${r.dir}`)).catch((e) => setError(e.message))}>Export</button>
          <button class="btn btn-danger" onClick={forget}>Forget</button>
        </div>
      </header>
      <ErrorBox error={error} />
      {notice && <div class="rounded-md border border-accent/40 bg-accent/10 px-3 py-2 text-[13px] flex justify-between"><span>{notice}</span><button class="text-muted" onClick={() => setNotice(null)}>✕</button></div>}

      <section class="grid grid-cols-1 md:grid-cols-4 gap-4">
        <div class="panel p-4 flex flex-col gap-3 md:col-span-2">
          <Meter label="CPU" used={t?.cpuMilli ?? 0} cap={t?.cpuCapMilli ?? 0} format={fmt.cores} />
          <Meter label="Memory" used={t?.memBytes ?? 0} cap={t?.memCapBytes ?? 0} format={fmt.bytes} />
          <Meter label="Pods" used={t?.pods ?? 0} cap={t?.podCap ?? 0} format={String} />
        </div>
        <Stat label="Nodes Ready" value={t ? `${t.nodesReady} / ${t.nodes}` : '—'} tone={t && t.nodesReady === t.nodes ? 'good' : 'warn'} />
        <Stat label="etcd quorum" value={status ? `${status.etcd.members} / ${status.etcd.expected}` : '—'}
          tone={status?.etcd.healthy ? 'good' : 'bad'} sub={status?.etcd.leader ? `leader ${status.etcd.leader}` : status?.etcd.alarms?.join(', ')} />
      </section>

      <section class="panel overflow-x-auto">
        <table class="data">
          <thead><tr><th class="pl-4">Hostname</th><th>IP</th><th>Role</th><th>Status</th><th>Talos</th><th>Kubelet</th><th>CPU</th><th>RAM</th><th>Pods</th><th>gVisor</th><th class="pr-4"></th></tr></thead>
          <tbody>
            {(status?.nodes ?? spec.nodes.map((n) => ({ ...n, ready: false, unschedulable: false, stage: '', talosVersion: '', kubeletVersion: '', cpuMilli: 0, cpuCapMilli: 0, memBytes: 0, memCapBytes: 0, pods: 0, podCap: 0, gvisor: false, talosReachable: false }))).map((n) => (
              <tr key={n.hostname}>
                <td class="pl-4 font-medium"><a href={`/nodes/${n.ip}`} class="hover:underline">{n.hostname}</a></td>
                <td class="mono">{n.ip}</td>
                <td>{n.role === 'controlplane' ? 'Control plane' : 'Worker'}</td>
                <td><Pill tone={n.ready ? 'good' : stateTone(n.stage || 'unknown')}>{n.ready ? 'Ready' : n.stage || 'unreachable'}</Pill>{n.unschedulable && <Pill tone="warn">cordoned</Pill>}</td>
                <td class="mono">{n.talosVersion || '—'}</td>
                <td class="mono">{n.kubeletVersion || '—'}</td>
                <td class="num">{fmt.cores(n.cpuMilli)}<span class="text-muted">/{fmt.cores(n.cpuCapMilli)}</span></td>
                <td class="num">{fmt.bytes(n.memBytes)}<span class="text-muted">/{fmt.bytes(n.memCapBytes)}</span></td>
                <td class="num">{n.pods}</td>
                <td>{n.gvisor ? <Pill tone="good">{n.kvm ? 'kvm' : 'runsc'}</Pill> : <span class="text-muted">—</span>}</td>
                <td class="pr-4 text-right whitespace-nowrap">
                  <a href={`/nodes/${n.ip}`} class="btn !py-1">Logs</a>{' '}
                  <button class="btn btn-danger !py-1" disabled={busy} onClick={() => { if (confirm(`Drain ${n.hostname}, delete it from Kubernetes and reset Talos?`)) run(`Remove ${n.hostname}`, api.removeNode(name, n.hostname)) }}>Remove</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div class="panel p-4 flex flex-col gap-3">
          <div class="flex items-center justify-between">
            <h2 class="font-semibold">Platform add-ons</h2>
            <div class="flex gap-2">
              <button class="btn !py-1" disabled={busy} onClick={() => run('Platform plan', api.platformPlan(name))}>Plan</button>
              <button class="btn btn-primary !py-1" disabled={busy} onClick={() => run('Platform apply', api.platformApply(name))}>Apply</button>
            </div>
          </div>
          <Addon on={spec.platform.metallb.enabled} name="MetalLB" detail={spec.platform.metallb.range} />
          <Addon on={spec.platform.ingressNginx.enabled} name="Ingress-NGINX" detail={status?.platform?.outputs?.ingress_ip ? `http://${status.platform.outputs.ingress_ip}` : undefined} />
          <Addon on={spec.platform.gvisor.enabled} name="gVisor RuntimeClass" detail="gvisor (runsc), gvisor-kvm (runsc-kvm)" />
          <Addon on={spec.platform.metricsServer.enabled} name="metrics-server" />
          <Addon on={spec.platform.certManager.enabled} name="cert-manager" />
          <Addon on={spec.platform.argocd.enabled} name="ArgoCD" detail={status?.platform?.outputs?.argocd_ip ? `http://${status.platform.outputs.argocd_ip}` : undefined} />
          <div class="text-[12px] text-muted">
            {status?.platform?.error ? <span class="text-bad">Last apply failed: {status.platform.error}</span>
              : status?.platform?.appliedAt ? `Applied ${new Date(status.platform.appliedAt).toLocaleString()}` : 'Not applied yet'}
          </div>
        </div>
        <div class="panel p-4 flex flex-col gap-3">
          <h2 class="font-semibold">Activity</h2>
          {op ? <OperationPanel id={op.id} title={op.title} /> : <span class="text-muted text-[13px]">Actions you start here stream their progress in this panel. <a href="/operations" class="underline">History</a></span>}
        </div>
      </section>

      {dialog === 'add' && <AddNodeDialog cluster={cluster} onClose={() => setDialog(null)} onStart={(id, title) => { setDialog(null); setOp({ id, title }) }} />}
      {dialog === 'upgrade' && <UpgradeDialog cluster={cluster} onClose={() => setDialog(null)} onStart={(id, title) => { setDialog(null); setOp({ id, title }) }} />}
      {dialog === 'yaml' && <YamlDialog cluster={cluster} onClose={() => setDialog(null)} onStart={(id, title) => { setDialog(null); setOp({ id, title }) }} />}
    </div>
  )
}

function Stat({ label, value, tone, sub }: { label: string; value: string; tone: 'good' | 'warn' | 'bad'; sub?: string }) {
  const color = { good: 'text-good', warn: 'text-warn', bad: 'text-bad' }[tone]
  return (
    <div class="panel p-4 flex flex-col gap-1">
      <span class="label">{label}</span>
      <span class={`text-2xl font-semibold num ${color}`}>{value}</span>
      {sub && <span class="text-[12px] text-muted truncate">{sub}</span>}
    </div>
  )
}

function Addon({ on, name, detail }: { on: boolean; name: string; detail?: string }) {
  return (
    <div class="flex items-center gap-3 text-[13px]">
      <span class={`inline-block h-2 w-2 rounded-full ${on ? 'bg-good' : 'bg-border'}`} />
      <span class={on ? '' : 'text-muted'}>{name}</span>
      {on && detail && <span class="mono text-muted ml-auto truncate">{detail}</span>}
    </div>
  )
}

function AddNodeDialog({ cluster, onClose, onStart }: { cluster: ClusterRow; onClose: () => void; onStart: (id: number, title: string) => void }) {
  const [candidates, setCandidates] = useState<NodeRow[]>([])
  const [ip, setIp] = useState('')
  const [hostname, setHostname] = useState('')
  const [role, setRole] = useState<'controlplane' | 'worker'>('worker')
  const [disk, setDisk] = useState('')
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { api.nodes().then((ns) => setCandidates(ns.filter((n) => n.state === 'maintenance' && !n.cluster))).catch((e) => setError(e.message)) }, [])
  const selected = candidates.find((c) => c.ip === ip)
  useEffect(() => {
    if (!selected) return
    const cand = selected.inventory?.disks.filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb').sort((a, b) => b.sizeBytes - a.sizeBytes)
    if (cand && cand[0]) setDisk(cand[0].devPath)
    const n = cluster.spec.spec.nodes.filter((x) => x.role === role).length + 1
    setHostname(`${cluster.name}-${role === 'controlplane' ? 'cp' : 'worker'}-${String(n).padStart(2, '0')}`)
  }, [selected, role, cluster])
  const submit = () => {
    if (!selected) return
    const node: NodeSpec = { hostname, ip, mac: selected.mac, role, arch: selected.arch, kvm: !!selected.inventory?.kvm, installDisk: { path: disk } }
    api.addNode(cluster.name, node).then((r) => onStart(r.operationId, `Add ${hostname}`)).catch((e) => setError(e.message))
  }
  return (
    <Dialog title={`Add node to ${cluster.name}`} onClose={onClose}>
      <ErrorBox error={error} />
      <Field label="Discovered node (maintenance mode)" hint={candidates.length === 0 ? 'No unassigned nodes. Run a discovery from Discovered nodes first.' : undefined}>
        <select class="input" value={ip} onChange={(e) => setIp((e.target as HTMLSelectElement).value)}>
          <option value="">Select…</option>
          {candidates.map((c) => <option key={c.ip} value={c.ip}>{c.ip} · {c.arch} · {c.inventory?.cpus ?? '?'} CPU · {fmt.bytes(c.inventory?.memoryBytes ?? 0)}{c.inventory?.kvm ? ' · kvm' : ''}</option>)}
        </select>
      </Field>
      <div class="grid grid-cols-2 gap-3">
        <Field label="Role">
          <select class="input" value={role} onChange={(e) => setRole((e.target as HTMLSelectElement).value as any)}>
            <option value="worker">Worker</option>
            <option value="controlplane">Control plane</option>
          </select>
        </Field>
        <Field label="Hostname"><input class="input" value={hostname} onInput={(e) => setHostname((e.target as HTMLInputElement).value)} /></Field>
      </div>
      <Field label="Install disk">
        <select class="input" value={disk} onChange={(e) => setDisk((e.target as HTMLSelectElement).value)}>
          {selected?.inventory?.disks.filter((d) => !d.readonly && !d.cdrom).map((d) => <option key={d.devPath} value={d.devPath}>{d.devPath} · {fmt.bytes(d.sizeBytes)} {d.model ? `· ${d.model}` : ''} {d.transport ? `· ${d.transport}` : ''}</option>)}
          {!selected && <option value="">—</option>}
        </select>
      </Field>
      <div class="flex justify-end gap-2">
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class="btn btn-primary" disabled={!selected || !hostname || !disk} onClick={submit}>Install and join</button>
      </div>
    </Dialog>
  )
}

function UpgradeDialog({ cluster, onClose, onStart }: { cluster: ClusterRow; onClose: () => void; onStart: (id: number, title: string) => void }) {
  const spec = cluster.spec.spec
  const [talos, setTalos] = useState(spec.talosVersion)
  const [k8s, setK8s] = useState(spec.kubernetesVersion)
  const [error, setError] = useState<string | null>(null)
  return (
    <Dialog title="Rolling upgrade" onClose={onClose}>
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">One node at a time, control planes first. Each node must come back Ready before the next starts.</p>
      <Field label={`Talos (now ${spec.talosVersion})`} hint="A/B partition swap with automatic rollback on boot failure.">
        <div class="flex gap-2">
          <input class="input mono" value={talos} onInput={(e) => setTalos((e.target as HTMLInputElement).value)} />
          <button class="btn btn-primary shrink-0" disabled={talos === spec.talosVersion} onClick={() => api.upgradeTalos(cluster.name, talos).then((r) => onStart(r.operationId, `Talos → ${talos}`)).catch((e) => setError(e.message))}>Upgrade Talos</button>
        </div>
      </Field>
      <Field label={`Kubernetes (now ${spec.kubernetesVersion})`} hint="Re-applies machine configs with new component images, then syncs bootstrap manifests.">
        <div class="flex gap-2">
          <input class="input mono" value={k8s} onInput={(e) => setK8s((e.target as HTMLInputElement).value)} />
          <button class="btn btn-primary shrink-0" disabled={k8s === spec.kubernetesVersion} onClick={() => api.upgradeKubernetes(cluster.name, k8s).then((r) => onStart(r.operationId, `Kubernetes → ${k8s}`)).catch((e) => setError(e.message))}>Upgrade Kubernetes</button>
        </div>
      </Field>
    </Dialog>
  )
}

function YamlDialog({ cluster, onClose, onStart }: { cluster: ClusterRow; onClose: () => void; onStart: (id: number, title: string) => void }) {
  const [yaml, setYaml] = useState('')
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { api.clusterYaml(cluster.name).then(setYaml).catch((e) => setError(e.message)) }, [cluster])
  return (
    <Dialog title="cluster.yaml" onClose={onClose} width="max-w-3xl">
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">The declaration is the source of truth. Apply regenerates every machine config and converges the platform layer.</p>
      <textarea class="input mono !text-[12px] h-[420px]" value={yaml} onInput={(e) => setYaml((e.target as HTMLTextAreaElement).value)} spellcheck={false} />
      <div class="flex justify-end gap-2">
        <button class="btn" onClick={() => api.validate(yaml).then((v) => { setYaml(v.yaml); setError(null) }).catch((e) => setError(e.message))}>Validate</button>
        <button class="btn btn-primary" onClick={() => api.validate(yaml).then(() => api.applyCluster(cluster.name, yaml)).then((r) => onStart(r.operationId, 'Apply cluster.yaml')).catch((e) => setError(e.message))}>Apply</button>
      </div>
    </Dialog>
  )
}
