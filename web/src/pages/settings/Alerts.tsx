import { api } from '../../api'
import { toast } from '../../store'
import { ErrorBox, Field, Section } from '../../components/ui'
import { MovedNotice, SaveBar, useSettingsSlice } from './Layout'

export function Alerts() {
  const f = useSettingsSlice('alerts', (s) => s.alerts, (s, v) => ({ ...s, alerts: v }))
  if (!f.draft) return <div class="text-muted">{f.error ?? 'Loading…'}</div>
  const a = f.draft
  const set = (patch: Partial<typeof a>) => f.setDraft({ ...a, ...patch })
  const smtp = (patch: Partial<typeof a.smtp>) => set({ smtp: { ...a.smtp, ...patch } })
  return (
    <Section title="Alerts" help="Health events at or above a severity go to a webhook and/or e-mail; a heartbeat proves the daemon is alive.">
      <ErrorBox error={f.error} />
      <MovedNotice show={f.movedUnderneath} onDiscard={f.discard} />
      <div class="panel p-3 grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="Minimum severity">
          <select class="input" value={a.minSeverity} onChange={(e) => set({ minSeverity: (e.target as HTMLSelectElement).value as any })}>
            <option value="info">info (everything)</option><option value="warn">warn</option><option value="critical">critical only</option>
          </select>
        </Field>
        <Field label="Heartbeat (hours)" hint="A summary this often regardless of severity; 0 = off. Silence means the daemon is down."><input class="input num" type="number" min={0} value={a.heartbeatHours ?? 0} onInput={(e) => set({ heartbeatHours: Number((e.target as HTMLInputElement).value) })} /></Field>
        <Field label="Ignore namespaces" hint="No workload alerts for these; comma-separated."><input class="input mono" value={(a.ignoreNamespaces ?? []).join(', ')} placeholder="dev, ci" onInput={(e) => set({ ignoreNamespaces: (e.target as HTMLInputElement).value.split(/[,\s]+/).filter(Boolean) })} /></Field>
        <Field label="Webhook URL" hint="Slack, Discord, Teams or generic JSON. Empty = off."><input class="input mono" value={a.webhookUrl} placeholder="https://hooks.slack.com/services/…" onInput={(e) => set({ webhookUrl: (e.target as HTMLInputElement).value.trim() })} /></Field>
      </div>
      <div class="panel p-3 grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="SMTP host" hint="Empty = off."><input class="input mono" value={a.smtp.host} placeholder="smtp.example.com" onInput={(e) => smtp({ host: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <div class="grid grid-cols-2 gap-3">
          <Field label="SMTP port"><input class="input num" type="number" value={a.smtp.port} onInput={(e) => smtp({ port: Number((e.target as HTMLInputElement).value) })} /></Field>
          <Field label="Encryption">
            <select class="input" value={a.smtp.tls ?? (a.smtp.startTLS ? 'starttls' : 'none')} onChange={(e) => { const tls = (e.target as HTMLSelectElement).value as any; smtp({ tls, port: tls === 'tls' && a.smtp.port === 587 ? 465 : tls === 'starttls' && a.smtp.port === 465 ? 587 : a.smtp.port }) }}>
              <option value="starttls">STARTTLS (587)</option><option value="tls">Implicit TLS (465)</option><option value="none">None (local relay)</option>
            </select>
          </Field>
        </div>
        <Field label="From"><input class="input mono" value={a.smtp.from} placeholder="kubit@example.com" onInput={(e) => smtp({ from: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="To" hint="Comma-separated."><input class="input mono" value={a.smtp.to.join(', ')} onInput={(e) => smtp({ to: (e.target as HTMLInputElement).value.split(/[,\s]+/).filter(Boolean) })} /></Field>
        <Field label="Username" hint="Empty = no authentication."><input class="input mono" value={a.smtp.username} onInput={(e) => smtp({ username: (e.target as HTMLInputElement).value })} /></Field>
        <Field label="Password" hint="Sealed at rest, shown masked."><input class="input mono" type="password" value={a.smtp.password} onInput={(e) => smtp({ password: (e.target as HTMLInputElement).value })} /></Field>
      </div>
      <SaveBar dirty={f.dirty} onSave={f.save}>
        <button class="btn" disabled={f.dirty} title={f.dirty ? 'Save first' : 'Send a test alert through the saved sinks'} onClick={() => api.testAlerts().then((r) => toast(r.ok ? 'Test alert delivered' : r.errors.join('; '), r.ok ? 'good' : 'error')).catch((e) => toast(e.message, 'error'))}>Send test alert</button>
      </SaveBar>
    </Section>
  )
}
