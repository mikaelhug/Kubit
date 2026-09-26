import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type Pool } from '../../api'
import { editableForm, submittedForm } from '../../cluster'
import { PoolsEditor } from '../../components/PoolsEditor'
import { Tabs } from '../../components/Tabs'
import { ErrorBox, Field, Section } from '../../components/ui'
import { WarningLine } from '../../components/WarningLine'
import { toast, watch } from '../../store'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

export function Settings({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState('')
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [form, setForm] = useState(() => editableForm(spec))
  const { data: saved, error: loadError } = useLive(() => api.clusterYaml(name), [name, cluster.updatedAt])
  useEffect(() => { if (saved !== null) { setYaml(saved); setDirty(false) } }, [saved])
  useEffect(() => { setForm(editableForm(spec)) }, [name, cluster.updatedAt])
  const formDirty = JSON.stringify(form) !== JSON.stringify(editableForm(spec))
  const cps = spec.nodes.filter((n) => n.role === 'controlplane').length
  const set = (patch: Partial<typeof form>) => setForm({ ...form, ...patch })
  const text = (key: keyof typeof form) => (e: Event) => set({ [key]: (e.target as HTMLInputElement).value } as Partial<typeof form>)
  const oidc = (patch: Partial<typeof form.oidc>) => set({ oidc: { ...form.oidc, ...patch } })
  const saveForm = () => api.saveClusterForm(name, submittedForm(form))
    .then(() => { setError(null); toast('Saved; apply node configs to push it', 'good') }).catch((e) => setError(e.message))
  const apply = () => api.applyCluster(name).then((r) => watch(r)).catch((e) => setError(e.message))

  return (
    <div class="flex flex-col gap-5">
      <Section title="Declaration" actions={<span class="text-[12px] text-muted">created {fmt.date(cluster.createdAt)} · changed {fmt.when(cluster.updatedAt)}</span>} help="Save stores cluster.yaml; Apply node configs pushes it to the nodes.">
        <ErrorBox error={error ?? loadError} />
        <Tabs active={tab} onSelect={(t) => setTab(t as 'form' | 'yaml')} tabs={[{ id: 'form', label: 'Form' }, { id: 'yaml', label: 'YAML' }]} />
        {tab === 'form' && (
          <div class="flex flex-col gap-4">
            <Group title="Endpoint">
              <Field label="API endpoint" hint="Used by kubeconfig and joining nodes"><input class="input mono" value={form.endpoint} onInput={text('endpoint')} /></Field>
              <Field label="Control plane VIP" hint="Shared layer-2 address; changing it re-applies every control plane"><input class="input mono" value={form.vip} placeholder="none" onInput={text('vip')} /></Field>
              <Field label="Workloads on control planes" hint={`${cps} control plane${cps === 1 ? '' : 's'}; schedulable by default below 6 nodes`}>
                <select class="input" value={form.allowScheduling === null ? 'auto' : String(form.allowScheduling)} onChange={(e) => { const v = (e.target as HTMLSelectElement).value; set({ allowScheduling: v === 'auto' ? null : v === 'true' }) }}>
                  <option value="true">Allowed (control planes also run pods)</option>
                  <option value="false">Dedicated (NoSchedule taint)</option>
                </select>
              </Field>
            </Group>
            <Group title="Network">
              <Field label="Pod CIDR" hint="Fixed for the cluster's lifetime"><input class="input mono" value={form.podCIDR} onInput={text('podCIDR')} /></Field>
              <Field label="Service CIDR" hint="Fixed for the cluster's lifetime"><input class="input mono" value={form.serviceCIDR} onInput={text('serviceCIDR')} /></Field>
              <Field label="Nameservers" hint="Comma-separated; empty keeps DHCP's"><input class="input mono" value={form.nameservers} placeholder="from DHCP" onInput={text('nameservers')} /></Field>
              <Field label="NTP servers" hint="Empty uses Talos' default"><input class="input mono" value={form.ntp} placeholder="time.cloudflare.com" onInput={text('ntp')} /></Field>
            </Group>
            <Group title="Nodes">
              <Field label="System extensions" hint="Comma-separated; used by new nodes and upgrades"><input class="input mono" value={form.extensions} onInput={text('extensions')} /></Field>
              <Field label="Maintenance window" hint='"<days> HH:MM-HH:MM"; empty is anytime'><input class="input mono" value={form.maintenanceWindow} placeholder="anytime" onInput={text('maintenanceWindow')} /></Field>
              <Field label="Window time zone" hint="IANA name; empty uses the daemon host's zone"><input class="input mono" value={form.maintenanceTimezone} placeholder={Intl.DateTimeFormat().resolvedOptions().timeZone} onInput={text('maintenanceTimezone')} /></Field>
            </Group>
            <Group title="kubectl SSO" help="OpenID Connect for kubectl; an empty issuer turns it off.">
              <Field label="Issuer URL"><input class="input mono" value={form.oidc.issuer} placeholder="https://login.example.com/realms/ops" onInput={(e) => oidc({ issuer: (e.target as HTMLInputElement).value.trim() })} /></Field>
              <Field label="Client ID" hint="Audience the API server accepts"><input class="input mono" value={form.oidc.clientID} onInput={(e) => oidc({ clientID: (e.target as HTMLInputElement).value.trim() })} /></Field>
              <Field label="Claims" hint="Username and groups claim; prefixed oidc: in RBAC"><span class="flex gap-2"><input class="input mono" value={form.oidc.usernameClaim ?? ''} onInput={(e) => oidc({ usernameClaim: (e.target as HTMLInputElement).value.trim() })} /><input class="input mono" value={form.oidc.groupsClaim ?? ''} onInput={(e) => oidc({ groupsClaim: (e.target as HTMLInputElement).value.trim() })} /></span></Field>
              <Field label="Admin group" hint="Bound to cluster-admin"><input class="input mono" value={form.oidc.adminGroup ?? ''} onInput={(e) => oidc({ adminGroup: (e.target as HTMLInputElement).value.trim() })} /></Field>
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
              <button class="btn btn-primary" disabled={!dirty} onClick={() => api.saveClusterYaml(name, yaml).then((v) => { setYaml(v.yaml); setDirty(false); setError(null); toast('Saved', 'good') }).catch((e) => setError(e.message))}>Save</button>
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

function PoolsSection({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  const [pools, setPools] = useState<Pool[]>(spec.pools ?? [])
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { setPools(spec.pools ?? []) }, [cluster.updatedAt])
  const { data: warnings } = useLive(() => api.lint(JSON.stringify(cluster.spec)).then((r) => r.warnings), [cluster.updatedAt], [], { onError: 'silent' })
  const dirty = JSON.stringify(pools) !== JSON.stringify(spec.pools ?? [])
  const save = () => api.savePools(name, pools).then(() => { setError(null); toast('Pools saved; apply node configs to push labels and taints', 'good') }).catch((e) => setError(e.message))
  return (
    <Section title="Pools" help="A pool is a class of nodes: role, labels, taints, extensions, disk policy." actions={<button class="btn btn-primary" disabled={!dirty} onClick={save}>Save pools</button>}>
      <ErrorBox error={error} />
      {warnings && warnings.length > 0 && <div class="flex flex-col gap-1">{warnings.map((w, i) => <WarningLine key={`${w.code}:${w.node ?? ''}:${i}`} w={w} />)}</div>}
      <PoolsEditor pools={pools} onChange={setPools} inUse={(p) => spec.nodes.filter((n) => n.pool === p).length} defaultExtensions={spec.extensions} />
      {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
    </Section>
  )
}
