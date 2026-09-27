import type { OIDCSettings } from '../../api'
import { ListInput } from '../../components/ListInput'
import { ErrorBox, Field, MovedNotice, Section } from '../../components/ui'
import { SaveBar, useSettingsSlice } from './Layout'

const empty: OIDCSettings = { enabled: false, name: 'SSO', issuer: '', clientId: '', clientSecret: '', usernameClaim: 'preferred_username', groupsClaim: 'groups', adminGroups: [], operatorGroups: [], viewerGroups: [], defaultRole: '' }

export function Sso() {
  const f = useSettingsSlice('sso', (s) => s.auth?.oidc ?? empty, (s, v) => ({ ...s, auth: { ...(s.auth ?? {}), oidc: v } }))
  if (!f.draft) return <div class="text-muted">{f.error ?? 'Loading'}</div>
  const o = f.draft
  const set = (patch: Partial<OIDCSettings>) => f.setDraft({ ...o, ...patch })
  const redirect = typeof location !== 'undefined' ? `${location.origin}/api/v1/auth/oidc/callback` : '/api/v1/auth/oidc/callback'
  return (
    <Section title="Single sign-on" help="OpenID Connect sign-in for the console; provider groups decide the role.">
      <ErrorBox error={f.error} />
      <MovedNotice show={f.movedUnderneath} onDiscard={f.discard} />
      <div class="panel p-3 grid grid-cols-1 md:grid-cols-2 gap-4">
        <label class="flex items-center gap-2 text-[13px] font-medium md:col-span-2"><input type="checkbox" checked={o.enabled} onChange={(e) => set({ enabled: (e.target as HTMLInputElement).checked })} /> Enable sign-in with an OpenID Connect provider</label>
        <Field label="Button label" hint="Shown on the sign-in screen"><input class="input" value={o.name} onInput={(e) => set({ name: (e.target as HTMLInputElement).value })} /></Field>
        <Field label="Issuer URL" hint="Where /.well-known/openid-configuration lives"><input class="input mono" value={o.issuer} placeholder="https://login.example.com/realms/ops" onInput={(e) => set({ issuer: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="Client ID"><input class="input mono" value={o.clientId} onInput={(e) => set({ clientId: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="Client secret" hint="Sealed at rest"><input class="input mono" type="password" value={o.clientSecret} onInput={(e) => set({ clientSecret: (e.target as HTMLInputElement).value })} /></Field>
        <Field label="Redirect URL" hint="Register this at the provider"><code class="mono text-[12px] break-all">{redirect}</code></Field>
        <Field label="Username claim"><input class="input mono" value={o.usernameClaim} onInput={(e) => set({ usernameClaim: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="Groups claim"><input class="input mono" value={o.groupsClaim} onInput={(e) => set({ groupsClaim: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="Default role" hint="For people in none of the groups below"><select class="input" value={o.defaultRole} onChange={(e) => set({ defaultRole: (e.target as HTMLSelectElement).value as OIDCSettings['defaultRole'] })}><option value="">no access</option><option value="viewer">viewer</option><option value="operator">operator</option><option value="admin">admin</option></select></Field>
        <Field label="Administrator groups" hint="Comma-separated"><ListInput value={o.adminGroups} onChange={(v) => set({ adminGroups: v })} /></Field>
        <Field label="Operator groups"><ListInput value={o.operatorGroups} onChange={(v) => set({ operatorGroups: v })} /></Field>
        <Field label="Viewer groups"><ListInput value={o.viewerGroups} onChange={(v) => set({ viewerGroups: v })} /></Field>
      </div>
      <SaveBar dirty={f.dirty} onSave={f.save} />
    </Section>
  )
}
