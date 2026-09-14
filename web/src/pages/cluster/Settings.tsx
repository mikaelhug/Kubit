import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type Pool, type Versions, type Warning } from '../../api'
import { PoolsEditor } from '../../components/PoolsEditor'
import { WarningLine } from '../create/steps'
import { reloadClusters, toast, watch } from '../../store'
import { Tabs } from '../../components/Tabs'
import { ConfirmDialog, ErrorBox, Field, KeyValue, Notice, Section } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

export function Settings({ ctx }: { ctx: ClusterCtx }) {
  const { route } = useLocation()
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState('')
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [forget, setForget] = useState(false)
  const [versions, setVersions] = useState<Versions | null>(null)
  const [form, setForm] = useState(fromSpec(spec))
  const [upgrade, setUpgrade] = useState<{ talos: string; k8s: string }>({ talos: spec.talosVersion, k8s: spec.kubernetesVersion })
  useEffect(() => { api.versions().then(setVersions).catch(() => {}) }, [])
  useEffect(() => { api.clusterYaml(name).then((y) => { setYaml(y); setDirty(false) }).catch((e) => setError(e.message)); setForm(fromSpec(spec)) }, [name, cluster.updatedAt])
  const k8sMinor = spec.kubernetesVersion.split('.').slice(0, 2).join('.')
  const k8sTargets = (versions?.kubernetesMinors ?? []).filter((m) => m >= k8sMinor)
  const formDirty = JSON.stringify(form) !== JSON.stringify(fromSpec(spec))
  const cps = spec.nodes.filter((n) => n.role === 'controlplane').length
  const list = (s: string) => s.split(/[,\s]+/).filter(Boolean)
  const saveForm = () => api.saveClusterForm(name, { ...form, extensions: list(form.extensions), nameservers: list(form.nameservers), ntp: list(form.ntp) })
    .then(() => { setError(null); reloadClusters(); toast('Saved. Apply node configs to push machine changes; add-ons are planned under Add-ons.', 'good') }).catch((e) => setError(e.message))

  return (
    <div class="flex flex-col gap-6">
      <Section title="Declaration" help="cluster.yaml is the source of truth. Saving changes only the declaration; Apply node configs pushes machine-level changes (endpoint, VIP, CIDRs, extensions, versions used for new nodes) to every node. Add-ons are reviewed under Add-ons → Plan.">
        <ErrorBox error={error} />
        <Tabs active={tab} onSelect={(t) => setTab(t as any)} tabs={[{ id: 'form', label: 'Form' }, { id: 'yaml', label: 'YAML' }]} />
        {tab === 'form' && (
          <div class="panel p-4 flex flex-col gap-4">
            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <Field label="API endpoint" hint="What kubeconfig and joining nodes use; normally https://<VIP or first control plane>:6443."><input class="input mono" value={form.endpoint} onInput={(e) => setForm({ ...form, endpoint: (e.target as HTMLInputElement).value })} /></Field>
              <Field label="Control plane VIP" hint="Layer-2 address shared by control planes; empty = none. Changing it re-applies every control plane."><input class="input mono" value={form.vip} onInput={(e) => setForm({ ...form, vip: (e.target as HTMLInputElement).value })} placeholder="none" /></Field>
              <Field label="Pod CIDR" hint="Cannot be changed on a running cluster without recreating it."><input class="input mono" value={form.podCIDR} onInput={(e) => setForm({ ...form, podCIDR: (e.target as HTMLInputElement).value })} /></Field>
              <Field label="Service CIDR" hint="Same: fixed for the cluster's lifetime in practice."><input class="input mono" value={form.serviceCIDR} onInput={(e) => setForm({ ...form, serviceCIDR: (e.target as HTMLInputElement).value })} /></Field>
              <Field label="System extensions" hint="Image Factory extensions baked into the installer (comma-separated). A new schematic is used by new nodes and upgrades."><input class="input mono" value={form.extensions} onInput={(e) => setForm({ ...form, extensions: (e.target as HTMLInputElement).value })} /></Field>
              <Field label="Nameservers" hint="Cluster-wide DNS for every node (comma-separated); empty keeps DHCP's."><input class="input mono" value={form.nameservers} placeholder="from DHCP" onInput={(e) => setForm({ ...form, nameservers: (e.target as HTMLInputElement).value })} /></Field>
              <Field label="NTP servers" hint="Empty uses Talos' default."><input class="input mono" value={form.ntp} placeholder="time.cloudflare.com" onInput={(e) => setForm({ ...form, ntp: (e.target as HTMLInputElement).value })} /></Field>
              <Field label="Workloads on control planes" hint={`${cps} control plane${cps === 1 ? '' : 's'}; Kubit defaults to schedulable below 6 nodes.`}>
                <select class="input" value={form.allowScheduling === null ? 'auto' : String(form.allowScheduling)} onChange={(e) => { const v = (e.target as HTMLSelectElement).value; setForm({ ...form, allowScheduling: v === 'auto' ? null : v === 'true' }) }}>
                  <option value="true">Allowed (control planes also run pods)</option>
                  <option value="false">Dedicated (NoSchedule taint)</option>
                </select>
              </Field>
            </div>
            <div class="flex items-center gap-2">
              <button class="btn btn-primary" disabled={!formDirty} onClick={saveForm}>Save</button>
              <button class="btn" disabled={formDirty} title={formDirty ? 'Save first' : 'Regenerate and apply every machine config from the saved declaration'} onClick={() => api.applyCluster(name).then((r) => watch(r)).catch((e) => setError(e.message))}>Apply node configs</button>
              {formDirty && <span class="text-[12px] text-warn">unsaved changes</span>}
            </div>
          </div>
        )}
        {tab === 'yaml' && (
          <>
            <textarea class="input mono !text-[12px] h-[420px]" value={yaml} spellcheck={false} onInput={(e) => { setYaml((e.target as HTMLTextAreaElement).value); setDirty(true) }} />
            <div class="flex gap-2 items-center">
              <button class="btn" onClick={() => api.validate(yaml).then((v) => { setYaml(v.yaml); setError(null); toast('Valid') }).catch((e) => setError(e.message))}>Validate</button>
              <button class="btn btn-primary" disabled={!dirty} onClick={() => api.saveClusterYaml(name, yaml).then((v) => { setYaml(v.yaml); setDirty(false); setError(null); reloadClusters(); toast('Saved.', 'good') }).catch((e) => setError(e.message))}>Save</button>
              <button class="btn" disabled={dirty} onClick={() => api.applyCluster(name).then((r) => watch(r)).catch((e) => setError(e.message))}>Apply node configs</button>
              <a href={`/clusters/${name}/addons`} class="btn">Plan add-ons →</a>
              {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
            </div>
          </>
        )}
      </Section>

      <PoolsSection ctx={ctx} />

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

      <Section title="Identity">
        <div class="panel p-4">
          <KeyValue rows={[
            ['Schematic', <span class="mono text-[12px] break-all">{spec.schematicID}</span>],
            ['Nodes', `${spec.nodes.length} (${cps} control plane${cps === 1 ? '' : 's'}) — edit under Nodes`],
            ['Created', new Date(cluster.createdAt).toLocaleString()],
            ['Last changed', new Date(cluster.updatedAt).toLocaleString()],
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

function fromSpec(spec: ClusterCtx['cluster']['spec']['spec']) {
  return {
    talosVersion: spec.talosVersion, kubernetesVersion: spec.kubernetesVersion, endpoint: spec.controlPlane.endpoint, vip: spec.controlPlane.vip ?? '',
    allowScheduling: spec.controlPlane.allowScheduling ?? null, podCIDR: spec.network.podCIDR, serviceCIDR: spec.network.serviceCIDR, extensions: (spec.extensions ?? []).join(', '),
    nameservers: (spec.network.nameservers ?? []).join(', '), ntp: (spec.network.ntp ?? []).join(', '),
  }
}

/** Pools live in cluster.yaml; saving resolves a schematic per distinct extension set. */
function PoolsSection({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  const [pools, setPools] = useState<Pool[]>(spec.pools ?? [])
  const [warnings, setWarnings] = useState<Warning[]>([])
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { setPools(spec.pools ?? []) }, [cluster.updatedAt])
  useEffect(() => { api.lint(JSON.stringify(cluster.spec)).then((r) => setWarnings(r.warnings)).catch(() => {}) }, [cluster.updatedAt])
  const dirty = JSON.stringify(pools) !== JSON.stringify(spec.pools ?? [])
  const save = () => api.savePools(name, pools).then(() => { setError(null); reloadClusters(); toast('Pools saved. Existing nodes pick up label/taint changes on Apply node configs; a changed extension set applies on the next upgrade or move.', 'good') }).catch((e) => setError(e.message))
  return (
    <Section title="Pools" help="A pool is a class of nodes: role, labels, taints, system extensions and disk policy. Nodes reference a pool; move a node between pools from its Actions tab." actions={<button class="btn btn-primary" disabled={!dirty} onClick={save}>Save pools</button>}>
      <ErrorBox error={error} />
      {warnings.length > 0 && <div class="flex flex-col gap-1">{warnings.map((w, i) => <WarningLine key={i} w={w} />)}</div>}
      <PoolsEditor pools={pools} onChange={setPools} inUse={(p) => spec.nodes.filter((n) => n.pool === p).length} defaultExtensions={spec.extensions} />
      {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
    </Section>
  )
}
