import { useEffect, useMemo, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, fmt, type CertInfo } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { ConfirmDialog, ErrorBox, Field, MaintenanceNotice, Notice, Pill, Section } from '../../components/ui'
import { runningFor, toast, versions, watch } from '../../store'
import type { Tone } from '../../tone'
import { useLive } from '../../useLive'
import { updatesFor } from '../../versions'
import type { ClusterCtx } from './ClusterPage'

export function Lifecycle({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  return (
    <div class="flex flex-col gap-5">
      <UpgradesSection name={name} talos={spec.talosVersion} k8s={spec.kubernetesVersion} updatedAt={cluster.updatedAt} />
      <CredentialsSection name={name} />
      <Section title="Export" help="Everything needed to run this cluster without Kubit.">
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
  return useLive(() => api.imageStatus(name), [name], [], { onError: 'null', refresh: [updatedAt] }).data
}

function UpgradesSection({ name, talos, k8s, updatedAt }: { name: string; talos: string; k8s: string; updatedAt: string }) {
  const image = useImageStatus(name, updatedAt)
  const v = versions.value
  const [upgrade, setUpgrade] = useState({ talos, k8s })
  useEffect(() => { setUpgrade({ talos, k8s }) }, [talos, k8s])
  const k8sMinor = k8s.split('.').slice(0, 2).join('.')
  const k8sTargets = (v?.kubernetesMinors ?? []).filter((m) => m >= k8sMinor)
  const available = updatesFor(talos, k8s, v)
  const busy = runningFor(name).length > 0
  return (
    <Section title="Upgrades" help="Rolling, one node at a time, control planes first, after pre-flight checks and an etcd snapshot.">
      <MaintenanceNotice cluster={name} />
      {image?.outdated && <Notice tone="warn">The nodes run an image without the current extensions ({(image.extensions ?? []).join(', ')}); upgrade Talos, at {talos} or newer, to re-image them.</Notice>}
      {(available.talos || available.kubernetes) && <Notice tone="info">Update available: {[available.talos && `Talos ${available.talos}`, available.kubernetes && `Kubernetes ${available.kubernetes}`].filter(Boolean).join(' · ')}</Notice>}
      {v?.note && <p class="text-[12px] text-muted">{v.note}</p>}
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label={`Talos (now ${talos})`} hint={v ? `Releases from ${v.talosSource}; rolls back if the new system does not boot` : 'Rolls back if the new system does not boot'}>
          <div class="flex gap-2">
            <input class="input mono" list="talos-versions" value={upgrade.talos} onInput={(e) => setUpgrade({ ...upgrade, talos: (e.target as HTMLInputElement).value })} />
            <datalist id="talos-versions">{v?.talos.map((x) => <option key={x} value={x} />)}</datalist>
            <button class="btn btn-primary shrink-0" disabled={busy || (upgrade.talos === talos && !image?.outdated)} onClick={() => api.upgradeTalos(name, upgrade.talos).then(watch).catch((e) => toast(e.message, 'error'))}>Upgrade</button>
          </div>
        </Field>
        <Field label={`Kubernetes (now ${k8s})`} hint={`Supported with Talos ${v?.machinery ?? ''}: ${(v?.kubernetesMinors ?? []).join(', ')}; no downgrades`}>
          <div class="flex gap-2">
            <input class="input mono" list="k8s-versions" value={upgrade.k8s} onInput={(e) => setUpgrade({ ...upgrade, k8s: (e.target as HTMLInputElement).value })} />
            <datalist id="k8s-versions">{k8sTargets.map((m) => <option key={m} value={m === v?.kubernetesLatest.split('.').slice(0, 2).join('.') ? v.kubernetesLatest : m + '.0'} />)}</datalist>
            <button class="btn btn-primary shrink-0" disabled={busy || upgrade.k8s === k8s} onClick={() => api.upgradeKubernetes(name, upgrade.k8s).then(watch).catch((e) => toast(e.message, 'error'))}>Upgrade</button>
          </div>
        </Field>
      </div>
    </Section>
  )
}

const certLabel: Record<string, string> = { talosconfig: 'Admin talosconfig', kubeconfig: 'Admin kubeconfig', 'talos-ca': 'Talos API CA', 'kubernetes-ca': 'Kubernetes CA', 'etcd-ca': 'etcd CA', 'aggregator-ca': 'Aggregator CA' }
const daysTone = (d: number): Tone => d <= 7 ? 'bad' : d <= 30 ? 'warn' : 'good'

function CredentialsSection({ name }: { name: string }) {
  const { data: certs, error } = useLive(() => api.certificates(name), [name], [[name, 'certificates']])
  const columns = useMemo<Column<CertInfo>[]>(() => [
    { id: 'name', header: 'Credential', cell: (c) => <span class="font-medium">{certLabel[c.name] ?? c.name}</span> },
    { id: 'expires', header: 'Expires', cell: (c) => c.error ? <span class="text-bad">{c.error}</span> : <span class="flex items-center gap-2"><Pill tone={daysTone(c.daysLeft)}>{c.daysLeft} days</Pill><span class="text-muted">{fmt.datetime(c.notAfter)}</span></span> },
    { id: 'issued', header: 'Issued', cell: (c) => <span class="text-muted">{c.notBefore ? fmt.datetime(c.notBefore) : '—'}</span> },
    { id: 'subject', header: 'Subject', cell: (c) => <span class="block mono text-[11px] text-muted truncate max-w-[260px]" title={c.subject}>{c.subject}</span> },
    { id: 'actions', header: '', align: 'right', cell: (c) => c.rotatable && <button class="btn !py-1" onClick={() => api.rotateCredential(name, c.name as 'talosconfig' | 'kubeconfig').then((r) => watch(r)).catch((e) => toast(e.message, 'error'))}>Rotate</button> },
  ], [name])
  return (
    <Section title="Credentials" help="Client certificates last a year and rotate here; CAs last ten.">
      <ErrorBox error={error} />
      <DataTable search={false} columns={columns} rows={certs ?? []} rowKey={(c) => c.name} />
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
          <span>Removes Kubit's records and secrets; export first, the nodes keep running unmanaged.</span>
          <button class="btn btn-danger ml-auto shrink-0" onClick={() => setForget(true)}>Forget cluster</button>
        </div>
      </Notice>
      {forget && (
        <ConfirmDialog title={`Forget ${name}`} action="Forget cluster" tone="danger" typed={name} onClose={() => setForget(false)}
          onConfirm={() => api.forgetCluster(name).then(() => { route('/') }).catch((e) => toast(e.message, 'error'))}
          impact={<p>Deletes cluster.yaml, secrets, talosconfig, kubeconfig and machine configs; the {n} node{n === 1 ? '' : 's'} keep running.</p>} />
      )}
    </Section>
  )
}
