import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type Versions } from '../../api'
import { reloadClusters, toast, watch } from '../../store'
import { ConfirmDialog, ErrorBox, Field, KeyValue, Notice, Section } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

export function Settings({ ctx }: { ctx: ClusterCtx }) {
  const { route } = useLocation()
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  const [yaml, setYaml] = useState('')
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [forget, setForget] = useState(false)
  const [upgrade, setUpgrade] = useState<{ talos: string; k8s: string }>({ talos: spec.talosVersion, k8s: spec.kubernetesVersion })
  const [versions, setVersions] = useState<Versions | null>(null)
  useEffect(() => { api.versions().then(setVersions).catch(() => {}) }, [])
  useEffect(() => { api.clusterYaml(name).then((y) => { setYaml(y); setDirty(false) }).catch((e) => setError(e.message)) }, [name, cluster.updatedAt])
  const k8sMinor = spec.kubernetesVersion.split('.').slice(0, 2).join('.')
  const k8sTargets = (versions?.kubernetesMinors ?? []).filter((m) => m >= k8sMinor)

  return (
    <div class="flex flex-col gap-6">
      <Section title="Declaration" help="cluster.yaml is the source of truth. Save stores it; node changes reach the machines with Apply node configs (Talos applies without a reboot when it can); platform changes are reviewed under Add-ons → Plan.">
        <ErrorBox error={error} />
        <textarea class="input mono !text-[12px] h-[420px]" value={yaml} spellcheck={false} onInput={(e) => { setYaml((e.target as HTMLTextAreaElement).value); setDirty(true) }} />
        <div class="flex gap-2 items-center">
          <button class="btn" onClick={() => api.validate(yaml).then((v) => { setYaml(v.yaml); setError(null); toast('Valid') }).catch((e) => setError(e.message))}>Validate</button>
          <button class="btn btn-primary" disabled={!dirty} onClick={() => api.saveClusterYaml(name, yaml).then((v) => { setYaml(v.yaml); setDirty(false); setError(null); reloadClusters(); toast('Saved. Apply node configs or plan add-ons to make it real.', 'good') }).catch((e) => setError(e.message))}>Save</button>
          <button class="btn" disabled={dirty} title={dirty ? 'Save first' : 'Regenerate and apply every machine config from the saved declaration'} onClick={() => api.applyCluster(name).then((r) => watch(r)).catch((e) => setError(e.message))}>Apply node configs</button>
          <a href={`/clusters/${name}/addons`} class="btn">Plan add-ons →</a>
          {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
        </div>
      </Section>

      <Section title="Versions" help={`Rolling, one node at a time, control planes first; each node must come back Ready before the next starts. ${versions?.note ?? ''}`}>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label={`Talos (now ${spec.talosVersion})`} hint={versions ? `Releases from ${versions.talosSource}; A/B partition swap with automatic rollback on boot failure. Pre-releases are listed but not recommended.` : 'A/B partition swap; Talos rolls back on its own if the new system does not boot.'}>
            <div class="flex gap-2">
              <input class="input mono" list="talos-versions" value={upgrade.talos} onInput={(e) => setUpgrade({ ...upgrade, talos: (e.target as HTMLInputElement).value })} />
              <datalist id="talos-versions">{versions?.talos.map((v) => <option key={v} value={v} />)}</datalist>
              <button class="btn btn-primary shrink-0" disabled={upgrade.talos === spec.talosVersion} onClick={() => api.upgradeTalos(name, upgrade.talos).then(watch).catch((e) => toast(e.message, 'error'))}>Upgrade</button>
            </div>
          </Field>
          <Field label={`Kubernetes (now ${spec.kubernetesVersion})`} hint={`Re-applies machine configs with the new component images, then syncs bootstrap manifests. Supported minors with Talos ${versions?.machinery ?? ''}: ${(versions?.kubernetesMinors ?? []).join(', ')}; downgrades are not offered.`}>
            <div class="flex gap-2">
              <input class="input mono" list="k8s-versions" value={upgrade.k8s} onInput={(e) => setUpgrade({ ...upgrade, k8s: (e.target as HTMLInputElement).value })} />
              <datalist id="k8s-versions">{k8sTargets.map((m) => <option key={m} value={m === versions?.kubernetesLatest.split('.').slice(0, 2).join('.') ? versions.kubernetesLatest : m + '.0'} />)}</datalist>
              <button class="btn btn-primary shrink-0" disabled={upgrade.k8s === spec.kubernetesVersion} onClick={() => api.upgradeKubernetes(name, upgrade.k8s).then(watch).catch((e) => toast(e.message, 'error'))}>Upgrade</button>
            </div>
          </Field>
        </div>
      </Section>

      <Section title="Identity" >
        <div class="panel p-4">
          <KeyValue rows={[
            ['Endpoint', <span class="mono">{spec.controlPlane.endpoint}</span>],
            ['VIP', spec.controlPlane.vip ? <span class="mono">{spec.controlPlane.vip}</span> : 'none'],
            ['Pod / service CIDR', <span class="mono">{spec.network.podCIDR} / {spec.network.serviceCIDR}</span>],
            ['Schematic', <span class="mono text-[12px] break-all">{spec.schematicID}</span>],
            ['Extensions', (spec.extensions ?? []).join(', ') || 'none'],
            ['Created', new Date(cluster.createdAt).toLocaleString()],
          ]} />
        </div>
      </Section>

      <Section title="Export" help="Native Talos artefacts (secrets.yaml, talosconfig, kubeconfig, machine configs) plus an OpenTofu root for the siderolabs/talos provider: everything needed to run this cluster without Kubit.">
        <div class="flex gap-2">
          <button class="btn" onClick={() => api.exportCluster(name).then((r) => toast(`Exported to ${r.dir}`, 'good')).catch((e) => toast(e.message, 'error'))}>Export to ~/.kubit/clusters/{name}/export</button>
          <a class="btn" href={`/api/v1/clusters/${name}/kubeconfig`} download="kubeconfig">Download kubeconfig</a>
        </div>
      </Section>

      <Section title="Danger zone">
        <Notice tone="bad">
          <div class="flex items-center gap-3">
            <span>Forget removes Kubit's records and secrets for this cluster. The nodes keep running; without the secrets Kubit can never manage them again. Export first.</span>
            <button class="btn btn-danger ml-auto shrink-0" onClick={() => setForget(true)}>Forget cluster…</button>
          </div>
        </Notice>
      </Section>
      {forget && (
        <ConfirmDialog title={`Forget ${name}`} action="Forget cluster" tone="danger" typed={name} onClose={() => setForget(false)}
          onConfirm={() => api.forgetCluster(name).then(() => { reloadClusters(); route('/') }).catch((e) => toast(e.message, 'error'))}
          impact={<><p>Deletes the stored cluster.yaml, secrets bundle, talosconfig, kubeconfig and per-node machine configs.</p><p>The {spec.nodes.length} node{spec.nodes.length === 1 ? '' : 's'} are not touched and stay in the cluster.</p></>} />
      )}
    </div>
  )
}
