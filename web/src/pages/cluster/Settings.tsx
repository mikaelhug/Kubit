import { useEffect, useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'
import { api, fmt, type Pool, type Warning } from '../../api'
import { PoolsEditor } from '../../components/PoolsEditor'
import { WarningLine } from '../create/steps'
import { toast, watch } from '../../store'
import { Tabs } from '../../components/Tabs'
import { ErrorBox, Field, Section } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

/** The declaration: cluster.yaml as a form or as text, and the pools. Nothing here runs an operation except Apply node configs. */
export function Settings({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState('')
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [form, setForm] = useState(fromSpec(spec))
  useEffect(() => { api.clusterYaml(name).then((y) => { setYaml(y); setDirty(false) }).catch((e) => setError(e.message)); setForm(fromSpec(spec)) }, [name, cluster.updatedAt])
  const formDirty = JSON.stringify(form) !== JSON.stringify(fromSpec(spec))
  const cps = spec.nodes.filter((n) => n.role === 'controlplane').length
  const list = (s: string) => s.split(/[,\s]+/).filter(Boolean)
  const set = (patch: Partial<typeof form>) => setForm({ ...form, ...patch })
  const text = (key: keyof typeof form) => (e: Event) => set({ [key]: (e.target as HTMLInputElement).value } as Partial<typeof form>)
  const saveForm = () => api.saveClusterForm(name, { ...form, extensions: list(form.extensions), nameservers: list(form.nameservers), ntp: list(form.ntp), etcdSnapshotKeep: Number(form.etcdSnapshotKeep) || 0 })
    .then(() => { setError(null); toast('Saved. Apply node configs to push machine changes.', 'good') }).catch((e) => setError(e.message))
  const apply = () => api.applyCluster(name).then((r) => watch(r)).catch((e) => setError(e.message))

  return (
    <div class="flex flex-col gap-5">
      <Section title="Declaration" actions={<span class="text-[12px] text-muted num">created {new Date(cluster.createdAt).toLocaleDateString()} · changed {fmt.when(cluster.updatedAt)}</span>} help="Save changes cluster.yaml; Apply node configs pushes it to the nodes. Add-ons are planned under Add-ons.">
        <ErrorBox error={error} />
        <Tabs active={tab} onSelect={(t) => setTab(t as any)} tabs={[{ id: 'form', label: 'Form' }, { id: 'yaml', label: 'YAML' }]} />
        {tab === 'form' && (
          <div class="flex flex-col gap-4">
            <Group title="Endpoint">
              <Field label="API endpoint" hint="What kubeconfig and joining nodes use; normally https://<VIP or first control plane>:6443."><input class="input mono" value={form.endpoint} onInput={text('endpoint')} /></Field>
              <Field label="Control plane VIP" hint="Layer-2 address shared by control planes; empty = none. Changing it re-applies every control plane."><input class="input mono" value={form.vip} placeholder="none" onInput={text('vip')} /></Field>
              <Field label="Workloads on control planes" hint={`${cps} control plane${cps === 1 ? '' : 's'}; Kubit defaults to schedulable below 6 nodes.`}>
                <select class="input" value={form.allowScheduling === null ? 'auto' : String(form.allowScheduling)} onChange={(e) => { const v = (e.target as HTMLSelectElement).value; set({ allowScheduling: v === 'auto' ? null : v === 'true' }) }}>
                  <option value="true">Allowed (control planes also run pods)</option>
                  <option value="false">Dedicated (NoSchedule taint)</option>
                </select>
              </Field>
            </Group>
            <Group title="Network">
              <Field label="Pod CIDR" hint="Fixed for the cluster's lifetime."><input class="input mono" value={form.podCIDR} onInput={text('podCIDR')} /></Field>
              <Field label="Service CIDR" hint="Fixed for the cluster's lifetime."><input class="input mono" value={form.serviceCIDR} onInput={text('serviceCIDR')} /></Field>
              <Field label="Nameservers" hint="Every node; comma-separated. Empty keeps DHCP's."><input class="input mono" value={form.nameservers} placeholder="from DHCP" onInput={text('nameservers')} /></Field>
              <Field label="NTP servers" hint="Empty uses Talos' default."><input class="input mono" value={form.ntp} placeholder="time.cloudflare.com" onInput={text('ntp')} /></Field>
            </Group>
            <Group title="Nodes">
              <Field label="System extensions" hint="Image Factory extensions in the installer, comma-separated. New nodes and upgrades use the new schematic."><input class="input mono" value={form.extensions} onInput={text('extensions')} /></Field>
              <Field label="Maintenance window" hint='Disruptive operations are refused outside it unless overridden. "<days> HH:MM-HH:MM", e.g. "Sat,Sun 22:00-04:00" or "daily 01:00-05:00"; empty = anytime.'><input class="input mono" value={form.maintenanceWindow} placeholder="anytime" onInput={text('maintenanceWindow')} /></Field>
              <Field label="Window time zone" hint="IANA name; empty uses the daemon host's zone."><input class="input mono" value={form.maintenanceTimezone} placeholder={Intl.DateTimeFormat().resolvedOptions().timeZone} onInput={text('maintenanceTimezone')} /></Field>
            </Group>
            <Group title="kubectl SSO" help="OpenID Connect for kubectl users; empty issuer = admin kubeconfig only. Applies on Apply node configs and the next add-on apply.">
              <Field label="Issuer URL"><input class="input mono" value={form.oidc.issuer} placeholder="https://login.example.com/realms/ops" onInput={(e) => set({ oidc: { ...form.oidc, issuer: (e.target as HTMLInputElement).value.trim() } })} /></Field>
              <Field label="Client ID" hint="Audience the API server accepts."><input class="input mono" value={form.oidc.clientID} onInput={(e) => set({ oidc: { ...form.oidc, clientID: (e.target as HTMLInputElement).value.trim() } })} /></Field>
              <Field label="Claims" hint="Username claim, groups claim. Prefixed oidc: in RBAC."><span class="flex gap-2"><input class="input mono" value={form.oidc.usernameClaim ?? ''} onInput={(e) => set({ oidc: { ...form.oidc, usernameClaim: (e.target as HTMLInputElement).value.trim() } })} /><input class="input mono" value={form.oidc.groupsClaim ?? ''} onInput={(e) => set({ oidc: { ...form.oidc, groupsClaim: (e.target as HTMLInputElement).value.trim() } })} /></span></Field>
              <Field label="Admin group" hint="Bound to cluster-admin."><input class="input mono" value={form.oidc.adminGroup ?? ''} onInput={(e) => set({ oidc: { ...form.oidc, adminGroup: (e.target as HTMLInputElement).value.trim() } })} /></Field>
            </Group>
            <div class="flex items-center gap-2">
              <button class="btn btn-primary" disabled={!formDirty} onClick={saveForm}>Save</button>
              <button class="btn" disabled={formDirty} title={formDirty ? 'Save first' : 'Regenerate and apply every machine config from the saved declaration'} onClick={apply}>Apply node configs</button>
              {formDirty && <span class="text-[12px] text-warn">unsaved changes</span>}
            </div>
          </div>
        )}
        {tab === 'yaml' && (
          <>
            <textarea class="input mono !text-[12px] h-[420px]" value={yaml} spellcheck={false} onInput={(e) => { setYaml((e.target as HTMLTextAreaElement).value); setDirty(true) }} />
            <div class="flex gap-2 items-center">
              <button class="btn" onClick={() => api.validate(yaml).then((v) => { setYaml(v.yaml); setError(null); toast('Valid') }).catch((e) => setError(e.message))}>Validate</button>
              <button class="btn btn-primary" disabled={!dirty} onClick={() => api.saveClusterYaml(name, yaml).then((v) => { setYaml(v.yaml); setDirty(false); setError(null); toast('Saved.', 'good') }).catch((e) => setError(e.message))}>Save</button>
              <button class="btn" disabled={dirty} onClick={apply}>Apply node configs</button>
              <a href={`/clusters/${name}/addons`} class="btn">Plan add-ons</a>
              {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
            </div>
          </>
        )}
      </Section>
      <PoolsSection ctx={ctx} />
    </div>
  )
}

function Group({ title, help, children }: { title: string; help?: string; children: ComponentChildren }) {
  return (
    <div class="panel p-3 flex flex-col gap-3">
      <div><span class="label">{title}</span>{help && <p class="text-[12px] text-muted mt-0.5">{help}</p>}</div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">{children}</div>
    </div>
  )
}

function fromSpec(spec: ClusterCtx['cluster']['spec']['spec']) {
  return {
    talosVersion: spec.talosVersion, kubernetesVersion: spec.kubernetesVersion, endpoint: spec.controlPlane.endpoint, vip: spec.controlPlane.vip ?? '',
    allowScheduling: spec.controlPlane.allowScheduling ?? null, podCIDR: spec.network.podCIDR, serviceCIDR: spec.network.serviceCIDR, extensions: (spec.extensions ?? []).join(', '),
    nameservers: (spec.network.nameservers ?? []).join(', '), ntp: (spec.network.ntp ?? []).join(', '),
    etcdSnapshotInterval: spec.backup?.etcd.interval ?? '6h', etcdSnapshotKeep: String(spec.backup?.etcd.keep ?? 28),
    maintenanceWindow: spec.maintenance?.window ?? '', maintenanceTimezone: spec.maintenance?.timezone ?? '',
    oidc: spec.auth?.oidc ?? { issuer: '', clientID: '', usernameClaim: 'preferred_username', groupsClaim: 'groups', adminGroup: '' },
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
  const save = () => api.savePools(name, pools).then(() => { setError(null); toast('Pools saved. Labels and taints apply on Apply node configs; a changed extension set on the next upgrade or move.', 'good') }).catch((e) => setError(e.message))
  return (
    <Section title="Pools" help="A pool is a class of nodes: role, labels, taints, extensions, disk policy." actions={<button class="btn btn-primary" disabled={!dirty} onClick={save}>Save pools</button>}>
      <ErrorBox error={error} />
      {warnings.length > 0 && <div class="flex flex-col gap-1">{warnings.map((w, i) => <WarningLine key={i} w={w} />)}</div>}
      <PoolsEditor pools={pools} onChange={setPools} inUse={(p) => spec.nodes.filter((n) => n.pool === p).length} defaultExtensions={spec.extensions} />
      {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
    </Section>
  )
}
