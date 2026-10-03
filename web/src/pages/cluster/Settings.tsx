import type { ComponentChildren } from 'preact'
import { useState } from 'preact/hooks'
import { api, fmt } from '../../api'
import { editableForm, submittedForm } from '../../cluster'
import { behindText, useConfigStatus } from '../../configStatus'
import { PoolsEditor } from '../../components/PoolsEditor'
import { Tabs } from '../../components/Tabs'
import { ErrorBox, Field, MovedNotice, Notice, Section } from '../../components/ui'
import { WarningLine } from '../../components/WarningLine'
import { runOp } from '../../ops'
import { toast } from '../../store'
import { useDraft } from '../../useDraft'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

export function Settings({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const spec = cluster.spec.spec
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [error, setError] = useState<string | null>(null)
  const f = useDraft(editableForm(spec), `cluster:${name}:form`)
  const { data: saved, error: loadError } = useLive(() => api.clusterYaml(name), [name], [], { refresh: [cluster.updatedAt] })
  const y = useDraft(saved, `cluster:${name}:yaml`)
  const form = f.draft ?? editableForm(spec)
  const yaml = y.draft ?? ''
  const cps = spec.nodes.filter((n) => n.role === 'controlplane').length
  const set = (patch: Partial<typeof form>) => f.set({ ...form, ...patch })
  const text = (key: keyof typeof form) => (e: Event) => set({ [key]: (e.target as HTMLInputElement).value } as Partial<typeof form>)
  const oidc = (patch: Partial<typeof form.oidc>) => set({ oidc: { ...form.oidc, ...patch } })
  const saveForm = () => api.saveClusterForm(name, submittedForm(form))
    .then(() => { f.commit(); setError(null); toast('Saved; apply node configs to push it', 'good') }).catch((e) => setError(e.message))
  const apply = () => runOp(api.applyCluster(name))
  const behind = useConfigStatus(name)?.behind ?? []
  const dirty = tab === 'form' ? f.dirty : y.dirty

  return (
    <div class="flex flex-col gap-5">
      <Section title="Declaration" actions={<span class="text-[12px] text-muted">created {fmt.date(cluster.createdAt)} · changed {fmt.when(cluster.updatedAt)}</span>} help="Save stores cluster.yaml; Apply node configs pushes it to the nodes.">
        <ErrorBox error={error ?? loadError} />
        <MovedNotice show={tab === 'form' ? f.moved : y.moved} onDiscard={tab === 'form' ? f.discard : y.discard} />
        {behind.length > 0 && <Notice tone="warn"><span class="flex items-center gap-3"><span title={behind.join(', ')}>{behindText(behind.length)}</span><button class="btn btn-sm ml-auto shrink-0" disabled={dirty} title={dirty ? 'Save first' : undefined} onClick={apply}>Apply node configs</button></span></Notice>}
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
              <Field label="Network policies" hint="NetworkPolicy objects are enforced"><OnOff value={form.networkPolicies} onChange={(networkPolicies) => set({ networkPolicies })} /></Field>
              <Field label="Discovery service" hint="Nodes register with discovery.talos.dev"><OnOff value={form.discovery} onChange={(discovery) => set({ discovery })} /></Field>
              <Field label="Host firewall" hint="Blocks node ports from outside the cluster"><OnOff value={form.firewall} onChange={(firewall) => set({ firewall })} /></Field>
            </Group>
            <Group title="Nodes">
              <Field label="System extensions" hint="Comma-separated; used by new nodes and upgrades"><input class="input mono" value={form.extensions} onInput={text('extensions')} /></Field>
              <Field label="Maintenance window" hint='"<days> HH:MM-HH:MM"; empty is anytime'><input class="input mono" value={form.maintenanceWindow} placeholder="anytime" onInput={text('maintenanceWindow')} /></Field>
              <Field label="Window time zone" hint="IANA name; empty uses the daemon host's zone"><input class="input mono" value={form.maintenanceTimezone} placeholder={Intl.DateTimeFormat().resolvedOptions().timeZone} onInput={text('maintenanceTimezone')} /></Field>
              <Field label="Disk encryption" hint="Fixed after install"><input class="input" value={spec.storage?.encryption === 'tpm' ? 'TPM' : spec.storage?.encryption === 'nodeID' ? 'Node ID' : 'Off'} disabled /></Field>
            </Group>
            <Group title="kubectl SSO" help="OpenID Connect for kubectl; an empty issuer turns it off.">
              <Field label="Issuer URL"><input class="input mono" value={form.oidc.issuer} placeholder="https://login.example.com/realms/ops" onInput={(e) => oidc({ issuer: (e.target as HTMLInputElement).value.trim() })} /></Field>
              <Field label="Client ID" hint="Audience the API server accepts"><input class="input mono" value={form.oidc.clientID} onInput={(e) => oidc({ clientID: (e.target as HTMLInputElement).value.trim() })} /></Field>
              <Field label="Claims" hint="Username and groups claim; prefixed oidc: in RBAC"><span class="flex gap-2"><input class="input mono" value={form.oidc.usernameClaim ?? ''} onInput={(e) => oidc({ usernameClaim: (e.target as HTMLInputElement).value.trim() })} /><input class="input mono" value={form.oidc.groupsClaim ?? ''} onInput={(e) => oidc({ groupsClaim: (e.target as HTMLInputElement).value.trim() })} /></span></Field>
              <Field label="Admin group" hint="Bound to cluster-admin"><input class="input mono" value={form.oidc.adminGroup ?? ''} onInput={(e) => oidc({ adminGroup: (e.target as HTMLInputElement).value.trim() })} /></Field>
            </Group>
            <div class="flex items-center gap-2">
              <button class="btn btn-primary" disabled={!f.dirty} onClick={saveForm}>Save</button>
              <button class="btn" disabled={f.dirty} title={f.dirty ? 'Save first' : 'Regenerate and apply every machine config from the saved declaration'} onClick={apply}>Apply node configs</button>
              {f.dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
            </div>
          </div>
        )}
        {tab === 'yaml' && (
          <>
            <textarea class="input mono !text-[12px] h-[420px]" value={yaml} spellcheck={false} onInput={(e) => y.set((e.target as HTMLTextAreaElement).value)} />
            <div class="flex gap-2 items-center">
              <button class="btn" onClick={() => api.validate(yaml).then((v) => { y.set(v.yaml); setError(null); toast('Valid') }).catch((e) => setError(e.message))}>Validate</button>
              <button class="btn btn-primary" disabled={!y.dirty} onClick={() => api.saveClusterYaml(name, yaml).then((v) => { y.commit(v.yaml); setError(null); toast('Saved', 'good') }).catch((e) => setError(e.message))}>Save</button>
              <button class="btn" disabled={y.dirty} onClick={apply}>Apply node configs</button>
              <a href={`/clusters/${name}/addons`} class="btn">Plan add-ons</a>
              {y.dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
            </div>
          </>
        )}
      </Section>
      <PoolsSection ctx={ctx} />
    </div>
  )
}

function OnOff({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <select class="input" value={String(value)} onChange={(e) => onChange((e.target as HTMLSelectElement).value === 'true')}>
      <option value="true">On</option>
      <option value="false">Off</option>
    </select>
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
  const p = useDraft(spec.pools ?? [], `cluster:${name}:pools`)
  const pools = p.draft ?? []
  const [error, setError] = useState<string | null>(null)
  const { data: warnings } = useLive(() => api.lint(JSON.stringify(cluster.spec)).then((r) => r.warnings), [name], [], { onError: 'silent', refresh: [cluster.updatedAt] })
  const dirty = p.dirty
  const save = () => api.savePools(name, pools).then((saved) => { p.commit(saved); setError(null); toast('Pools saved; apply node configs to push labels and taints', 'good') }).catch((e) => setError(e.message))
  return (
    <Section title="Pools" help="A pool is a class of nodes: role, labels, taints, extensions, disk policy." actions={<button class="btn btn-primary" disabled={!dirty} onClick={save}>Save pools</button>}>
      <ErrorBox error={error} />
      <MovedNotice show={p.moved} onDiscard={p.discard} />
      {warnings && warnings.length > 0 && <div class="flex flex-col gap-1">{warnings.map((w, i) => <WarningLine key={`${w.code}:${w.node ?? ''}:${i}`} w={w} />)}</div>}
      <PoolsEditor pools={pools} onChange={p.set} inUse={(p) => spec.nodes.filter((n) => n.pool === p).length} defaultExtensions={spec.extensions} />
      {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
    </Section>
  )
}
