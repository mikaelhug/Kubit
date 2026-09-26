import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, fmt, type CertInfo, type ImageStatus, type Versions } from '../../api'
import { latestTalos, operations, toast, watch, refreshKey } from '../../store'
import { ConfirmDialog, ErrorBox, Field, MaintenanceNotice, Notice, Pill, Section } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'
import { verLess } from './Overview'
import { DataTable, type Column } from '../../components/DataTable'

/** Operations on the cluster as a whole: upgrades, credentials, hand-over, forgetting it. */
export function Lifecycle({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  return (
    <div class="flex flex-col gap-5">
      <UpgradesSection name={name} talos={spec.talosVersion} k8s={spec.kubernetesVersion} updatedAt={cluster.updatedAt} />
      <CredentialsSection name={name} />
      <Section title="Export" help="Everything needed to run this cluster without Kubit: Talos secrets and configs, kubeconfig, an OpenTofu root.">
        <div class="flex gap-2">
          <button class="btn" onClick={() => api.exportCluster(name).then((r) => toast(`Exported to ${r.dir}`, 'good')).catch((e) => toast(e.message, 'error'))}>Export to ~/.kubit/clusters/{name}/export</button>
          <a class="btn" href={`/api/v1/clusters/${name}/kubeconfig`} download="kubeconfig">Download kubeconfig</a>
        </div>
      </Section>
      <ForgetSection ctx={ctx} />
    </div>
  )
}

export function useImageStatus(name: string, updatedAt: string) {
  const [image, setImage] = useState<ImageStatus | null>(null)
  useEffect(() => { api.imageStatus(name).then(setImage).catch(() => setImage(null)) }, [name, updatedAt])
  return image
}

function UpgradesSection({ name, talos, k8s, updatedAt }: { name: string; talos: string; k8s: string; updatedAt: string }) {
  const image = useImageStatus(name, updatedAt)
  const [versions, setVersions] = useState<Versions | null>(null)
  const [upgrade, setUpgrade] = useState({ talos, k8s })
  useEffect(() => { api.versions().then(setVersions).catch(() => {}) }, [latestTalos.value])
  useEffect(() => { setUpgrade({ talos, k8s }) }, [talos, k8s])
  const k8sMinor = k8s.split('.').slice(0, 2).join('.')
  const k8sTargets = (versions?.kubernetesMinors ?? []).filter((m) => m >= k8sMinor)
  const latest = versions?.talos.find((v) => !v.includes('-'))
  const talosNew = latest && verLess(talos, latest) ? latest : ''
  const k8sNew = versions && verLess(k8s, versions.kubernetesLatest) ? versions.kubernetesLatest : ''
  const busy = [...operations.value.values()].some((o) => o.cluster === name && o.status === 'running')
  return (
    <Section title="Upgrades" help={`Rolling, one node at a time, control planes first. Pre-flight checks and a pre-upgrade etcd snapshot come first. ${versions?.note ?? ''}`}>
      <MaintenanceNotice cluster={name} />
      {image?.outdated && <Notice tone="warn">The nodes run an image without the current extensions ({(image.extensions ?? []).join(', ')}). Upgrade Talos, at {talos} or newer, to re-image them.</Notice>}
      {(talosNew || k8sNew) && <Notice tone="info">Update available: {[talosNew && `Talos ${talosNew}`, k8sNew && `Kubernetes ${k8sNew}`].filter(Boolean).join(' · ')}</Notice>}
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label={`Talos (now ${talos})`} hint={versions ? `Releases from ${versions.talosSource}; A/B partition swap with automatic rollback on boot failure.` : 'A/B partition swap; Talos rolls back on its own if the new system does not boot.'}>
          <div class="flex gap-2">
            <input class="input mono" list="talos-versions" value={upgrade.talos} onInput={(e) => setUpgrade({ ...upgrade, talos: (e.target as HTMLInputElement).value })} />
            <datalist id="talos-versions">{versions?.talos.map((v) => <option key={v} value={v} />)}</datalist>
            <button class="btn btn-primary shrink-0" disabled={busy || (upgrade.talos === talos && !image?.outdated)} onClick={() => api.upgradeTalos(name, upgrade.talos).then(watch).catch((e) => toast(e.message, 'error'))}>Upgrade</button>
          </div>
        </Field>
        <Field label={`Kubernetes (now ${k8s})`} hint={`Re-applies machine configs with the new component images. Supported minors with Talos ${versions?.machinery ?? ''}: ${(versions?.kubernetesMinors ?? []).join(', ')}; no downgrades.`}>
          <div class="flex gap-2">
            <input class="input mono" list="k8s-versions" value={upgrade.k8s} onInput={(e) => setUpgrade({ ...upgrade, k8s: (e.target as HTMLInputElement).value })} />
            <datalist id="k8s-versions">{k8sTargets.map((m) => <option key={m} value={m === versions?.kubernetesLatest.split('.').slice(0, 2).join('.') ? versions.kubernetesLatest : m + '.0'} />)}</datalist>
            <button class="btn btn-primary shrink-0" disabled={busy || upgrade.k8s === k8s} onClick={() => api.upgradeKubernetes(name, upgrade.k8s).then(watch).catch((e) => toast(e.message, 'error'))}>Upgrade</button>
          </div>
        </Field>
      </div>
    </Section>
  )
}

/** What Kubit holds to talk to the cluster, and when each stops working. */
function CredentialsSection({ name }: { name: string }) {
  const [certs, setCerts] = useState<CertInfo[]>([])
  const [error, setError] = useState<string | null>(null)
  const finished = [...operations.value.values()].filter((o) => o.cluster === name && o.status !== 'running').length
  useEffect(() => { api.certificates(name).then(setCerts).catch((e) => setError(e.message)) }, [name, finished, refreshKey(name, 'certificates')])
  const label: Record<string, string> = { talosconfig: 'Admin talosconfig', kubeconfig: 'Admin kubeconfig', 'talos-ca': 'Talos API CA', 'kubernetes-ca': 'Kubernetes CA', 'etcd-ca': 'etcd CA', 'aggregator-ca': 'Aggregator CA' }
  const tone = (d: number) => d <= 7 ? 'bad' : d <= 30 ? 'warn' : 'good'
  const columns: Column<CertInfo>[] = [
    { id: 'name', header: 'Credential', cell: (c) => <span class="font-medium">{label[c.name] ?? c.name}</span> },
    { id: 'expires', header: 'Expires', cell: (c) => c.error ? <span class="text-bad">{c.error}</span> : <span class="flex items-center gap-2"><Pill tone={tone(c.daysLeft)}>{c.daysLeft} days</Pill><span class="num text-muted">{fmt.datetime(c.notAfter)}</span></span> },
    { id: 'issued', header: 'Issued', cell: (c) => <span class="num text-muted">{c.notBefore ? fmt.datetime(c.notBefore) : '—'}</span> },
    { id: 'subject', header: 'Subject', cell: (c) => <span class="block mono text-[11px] text-muted truncate max-w-[260px]" title={c.subject}>{c.subject}</span> },
    { id: 'actions', header: '', align: 'right', cell: (c) => c.rotatable && <button class="btn !py-1" onClick={() => api.rotateCredential(name, c.name as 'talosconfig' | 'kubeconfig').then((r) => watch(r)).catch((e) => toast(e.message, 'error'))}>Rotate</button> },
  ]
  return (
    <Section title="Credentials" help="Client certificates last one year and can be rotated here; CAs last ten. Kubit alerts 30 days before expiry.">
      <ErrorBox error={error} />
      <DataTable search={false} columns={columns} rows={certs} rowKey={(c) => c.name} />
    </Section>
  )
}

function ForgetSection({ ctx }: { ctx: ClusterCtx }) {
  const { route } = useLocation()
  const { name, cluster } = ctx
  const [forget, setForget] = useState(false)
  const n = cluster.spec.spec.nodes.length
  return (
    <Section title="Forget cluster">
      <Notice tone="bad">
        <div class="flex items-center gap-3">
          <span>Removes Kubit's records and secrets; the nodes keep running but can never be managed again. Export first.</span>
          <button class="btn btn-danger ml-auto shrink-0" onClick={() => setForget(true)}>Forget cluster</button>
        </div>
      </Notice>
      {forget && (
        <ConfirmDialog title={`Forget ${name}`} action="Forget cluster" tone="danger" typed={name} onClose={() => setForget(false)}
          onConfirm={() => api.forgetCluster(name).then(() => { route('/') }).catch((e) => toast(e.message, 'error'))}
          impact={<><p>Deletes the stored cluster.yaml, secrets bundle, talosconfig, kubeconfig and per-node machine configs.</p><p>The {n} node{n === 1 ? '' : 's'} are not touched and stay in the cluster.</p></>} />
      )}
    </Section>
  )
}
