import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type OffsiteStatus, type Settings } from '../api'
import { loadSettings, settings, toast, watch } from '../store'
import { ErrorBox, Field, Notice, Section } from '../components/ui'

export function KubitSettings() {
  const [s, setS] = useState<Settings | null>(null)
  const [orig, setOrig] = useState<Settings | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [off, setOff] = useState<OffsiteStatus | null>(null)
  const loadOff = () => api.offsiteStatus().then(setOff).catch(() => {})
  const pushed = settings.value
  // The daemon pushes the saved row; adopt it unless the form has unsaved edits, in
  // which case a notice says the baseline moved.
  useEffect(() => {
    if (!pushed) { loadSettings(); return }
    const dirtyNow = s && orig && JSON.stringify(s) !== JSON.stringify(orig)
    if (!dirtyNow) setS(pushed)
    setOrig(pushed)
    loadOff()
  }, [pushed]) // eslint-disable-line
  if (!s) return <div class="p-6 text-muted">{error ?? 'Loading…'}</div>
  const dirty = JSON.stringify(s) !== JSON.stringify(orig)
  const movedUnderneath = dirty && pushed && JSON.stringify(pushed) !== JSON.stringify(orig)
  const save = () => api.saveSettings(s).then((v) => { setS(v); setOrig(v); setError(null); toast('Settings saved', 'good') }).catch((e) => setError(e.message))
  return (
    <div class="p-6 flex flex-col gap-6 max-w-3xl">
      <Section title="Kubit settings" help="Preferences of this Kubit installation. Cluster-specific settings live under each cluster.">
        <ErrorBox error={error} />
        {movedUnderneath && <Notice tone="warn">Settings were changed elsewhere while you were editing. Saving overwrites them; <button class="underline" onClick={() => { setS(pushed); setOrig(pushed) }}>discard your edits</button> to see the current values.</Notice>}
        <div class="panel p-4 grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label="Image Factory URL" hint="Where installer images, ISOs and PXE assets come from. Point at a self-hosted factory for air-gapped sites."><input class="input mono" value={s.factoryUrl} onInput={(e) => setS({ ...s, factoryUrl: (e.target as HTMLInputElement).value })} /></Field>
          <Field label="Health poll interval (seconds)" hint="How often every cluster is queried for samples and events. Minimum 5."><input class="input num" type="number" min={5} value={s.watchIntervalSec} onInput={(e) => setS({ ...s, watchIntervalSec: Number((e.target as HTMLInputElement).value) })} /></Field>
          <Field label="Discovery subnets" hint="Pre-filled in the scan box (comma-separated CIDRs or addresses)."><input class="input mono" value={s.discoverySubnets.join(', ')} onInput={(e) => setS({ ...s, discoverySubnets: (e.target as HTMLInputElement).value.split(/[,\s]+/).filter(Boolean) })} /></Field>
          <Field label="Default MetalLB range" hint="Suggested pool for new clusters; empty derives one from the first node's subnet."><input class="input mono" value={s.defaultMetalLBRange} onInput={(e) => setS({ ...s, defaultMetalLBRange: (e.target as HTMLInputElement).value })} placeholder="192.168.1.200-192.168.1.220" /></Field>
          <Field label="PXE status URL" hint="Where the separate kubit pxe process publishes its status."><input class="input mono" value={s.pxeStatusUrl} onInput={(e) => setS({ ...s, pxeStatusUrl: (e.target as HTMLInputElement).value })} /></Field>
        </div>
        <div class="flex gap-2 items-center">
          <button class="btn btn-primary" disabled={!dirty} onClick={save}>Save</button>
          {dirty && <span class="text-[12px] text-warn">unsaved changes</span>}
        </div>
      </Section>
      <Section title="Alert forwarding" help="Health events at or above the chosen severity (node unreachable, etcd unhealthy, crashlooping pods, unavailable workloads, pending claims, exhausted LoadBalancer pool, stale backups, expiring credentials, …) are pushed as they happen. The webhook body is JSON with a top-level text field, so Slack, Discord, Teams and generic receivers all work; mail goes through SMTP (STARTTLS, implicit TLS or a plain local relay).">
        <div class="panel p-4 grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label="Minimum severity">
            <select class="input" value={s.alerts.minSeverity} onChange={(e) => setS({ ...s, alerts: { ...s.alerts, minSeverity: (e.target as HTMLSelectElement).value as any } })}>
              <option value="info">info (everything)</option><option value="warn">warn</option><option value="critical">critical only</option>
            </select>
          </Field>
          <Field label="Heartbeat (hours)" hint="A 'still watching' summary to the sinks this often, regardless of severity; 0 = off. If it stops arriving, the daemon is down — that is the point."><input class="input num" type="number" min={0} value={s.alerts.heartbeatHours ?? 0} onInput={(e) => setS({ ...s, alerts: { ...s.alerts, heartbeatHours: Number((e.target as HTMLInputElement).value) } })} /></Field>
          <Field label="Ignore namespaces" hint="Workload alerts (crashloops, unavailable deployments, pending claims, empty services) are never raised for these namespaces. Comma-separated."><input class="input mono" value={(s.alerts.ignoreNamespaces ?? []).join(', ')} placeholder="dev, ci" onInput={(e) => setS({ ...s, alerts: { ...s.alerts, ignoreNamespaces: (e.target as HTMLInputElement).value.split(/[,\s]+/).filter(Boolean) } })} /></Field>
          <Field label="Webhook URL" hint="Empty = off."><input class="input mono" value={s.alerts.webhookUrl} placeholder="https://hooks.slack.com/services/…" onInput={(e) => setS({ ...s, alerts: { ...s.alerts, webhookUrl: (e.target as HTMLInputElement).value.trim() } })} /></Field>
          <Field label="SMTP host" hint="Empty = off."><input class="input mono" value={s.alerts.smtp.host} placeholder="smtp.example.com" onInput={(e) => setS({ ...s, alerts: { ...s.alerts, smtp: { ...s.alerts.smtp, host: (e.target as HTMLInputElement).value.trim() } } })} /></Field>
          <div class="grid grid-cols-2 gap-3">
            <Field label="SMTP port"><input class="input num" type="number" value={s.alerts.smtp.port} onInput={(e) => setS({ ...s, alerts: { ...s.alerts, smtp: { ...s.alerts.smtp, port: Number((e.target as HTMLInputElement).value) } } })} /></Field>
            <Field label="Encryption">
              <select class="input" value={s.alerts.smtp.tls ?? (s.alerts.smtp.startTLS ? 'starttls' : 'none')} onChange={(e) => { const tls = (e.target as HTMLSelectElement).value as any; setS({ ...s, alerts: { ...s.alerts, smtp: { ...s.alerts.smtp, tls, port: tls === 'tls' && s.alerts.smtp.port === 587 ? 465 : tls === 'starttls' && s.alerts.smtp.port === 465 ? 587 : s.alerts.smtp.port } } }) }}>
                <option value="starttls">STARTTLS (587)</option><option value="tls">Implicit TLS (465)</option><option value="none">None (local relay)</option>
              </select>
            </Field>
          </div>
          <Field label="From"><input class="input mono" value={s.alerts.smtp.from} placeholder="kubit@example.com" onInput={(e) => setS({ ...s, alerts: { ...s.alerts, smtp: { ...s.alerts.smtp, from: (e.target as HTMLInputElement).value.trim() } } })} /></Field>
          <Field label="To" hint="Comma-separated."><input class="input mono" value={s.alerts.smtp.to.join(', ')} onInput={(e) => setS({ ...s, alerts: { ...s.alerts, smtp: { ...s.alerts.smtp, to: (e.target as HTMLInputElement).value.split(/[,\s]+/).filter(Boolean) } } })} /></Field>
          <Field label="Username" hint="Empty = no authentication."><input class="input mono" value={s.alerts.smtp.username} onInput={(e) => setS({ ...s, alerts: { ...s.alerts, smtp: { ...s.alerts.smtp, username: (e.target as HTMLInputElement).value } } })} /></Field>
          <Field label="Password" hint="Stored sealed with the master key; shown masked."><input class="input mono" type="password" value={s.alerts.smtp.password} onInput={(e) => setS({ ...s, alerts: { ...s.alerts, smtp: { ...s.alerts.smtp, password: (e.target as HTMLInputElement).value } } })} /></Field>
        </div>
        <div class="flex gap-2 items-center">
          <button class="btn btn-primary" disabled={!dirty} onClick={save}>Save</button>
          <button class="btn" disabled={dirty} title={dirty ? 'Save first' : 'Send a test alert through the saved sinks'} onClick={() => api.testAlerts().then((r) => toast(r.ok ? 'Test alert delivered' : r.errors.join('; '), r.ok ? 'good' : 'error')).catch((e) => toast(e.message, 'error'))}>Send test alert</button>
        </div>
      </Section>
      <Section title="Off-site copies" help="A second home for what disaster recovery needs: every etcd snapshot is copied here as it is taken, and a sealed Kubit backup is uploaded daily. Losing this machine together with the cluster then still leaves a way back. Objects stay encrypted with the master key — keep `kubit key export` somewhere else again (password manager).">
        <div class="panel p-4 grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label="Target">
            <select class="input" value={s.offsite.type} onChange={(e) => setS({ ...s, offsite: { ...s.offsite, type: (e.target as HTMLSelectElement).value as any } })}>
              <option value="">Off</option><option value="dir">Directory (mounted share, USB disk, synced folder)</option><option value="s3">S3-compatible bucket</option>
            </select>
          </Field>
          <Field label="Keep daily Kubit backups" hint="Older ones are deleted remotely; snapshots follow each cluster's own retention."><input class="input num" type="number" min={1} value={s.offsite.keepBackups} onInput={(e) => setS({ ...s, offsite: { ...s.offsite, keepBackups: Number((e.target as HTMLInputElement).value) } })} /></Field>
          {s.offsite.type === 'dir' && <Field label="Directory" hint="Must be reachable from the daemon's host; an SMB/NFS mount or a folder synced elsewhere."><input class="input mono" value={s.offsite.dir} placeholder="/Volumes/backup/kubit" onInput={(e) => setS({ ...s, offsite: { ...s.offsite, dir: (e.target as HTMLInputElement).value.trim() } })} /></Field>}
          {s.offsite.type === 's3' && (<>
            <Field label="Endpoint" hint="Host[:port]; https unless marked insecure. AWS: s3.<region>.amazonaws.com; MinIO/B2/Wasabi/Hetzner all work."><input class="input mono" value={s.offsite.endpoint} placeholder="s3.eu-central-1.amazonaws.com" onInput={(e) => setS({ ...s, offsite: { ...s.offsite, endpoint: (e.target as HTMLInputElement).value.trim() } })} /></Field>
            <Field label="Bucket"><input class="input mono" value={s.offsite.bucket} onInput={(e) => setS({ ...s, offsite: { ...s.offsite, bucket: (e.target as HTMLInputElement).value.trim() } })} /></Field>
            <Field label="Region" hint="Empty = auto."><input class="input mono" value={s.offsite.region} onInput={(e) => setS({ ...s, offsite: { ...s.offsite, region: (e.target as HTMLInputElement).value.trim() } })} /></Field>
            <Field label="Access key"><input class="input mono" value={s.offsite.accessKey} onInput={(e) => setS({ ...s, offsite: { ...s.offsite, accessKey: (e.target as HTMLInputElement).value.trim() } })} /></Field>
            <Field label="Secret key" hint="Stored sealed with the master key; shown masked."><input class="input mono" type="password" value={s.offsite.secretKey} onInput={(e) => setS({ ...s, offsite: { ...s.offsite, secretKey: (e.target as HTMLInputElement).value } })} /></Field>
            <div class="flex flex-col gap-2 text-[13px]">
              <label class="flex items-center gap-2"><input type="checkbox" checked={s.offsite.insecure} onChange={(e) => setS({ ...s, offsite: { ...s.offsite, insecure: (e.target as HTMLInputElement).checked } })} /> Plain http (LAN MinIO)</label>
              <label class="flex items-center gap-2"><input type="checkbox" checked={s.offsite.pathStyle} onChange={(e) => setS({ ...s, offsite: { ...s.offsite, pathStyle: (e.target as HTMLInputElement).checked } })} /> Path-style bucket addressing</label>
            </div>
          </>)}
          {s.offsite.type && <Field label="Prefix" hint="Optional folder inside the target."><input class="input mono" value={s.offsite.prefix} placeholder="kubit" onInput={(e) => setS({ ...s, offsite: { ...s.offsite, prefix: (e.target as HTMLInputElement).value.trim() } })} /></Field>}
        </div>
        <div class="flex gap-2 items-center flex-wrap">
          <button class="btn btn-primary" disabled={!dirty} onClick={save}>Save</button>
          <button class="btn" disabled={!s.offsite.type} title="Write, read back and delete a probe object with the settings as shown (saved or not)" onClick={() => api.offsiteTest(s.offsite).then((r) => toast(r.ok ? `Target reachable (${r.roundTripMs} ms round trip)` : r.error ?? 'failed', r.ok ? 'good' : 'error')).catch((e) => toast(e.message, 'error'))}>Test target</button>
          <button class="btn" disabled={dirty || !s.offsite.type} title={dirty ? 'Save first' : 'Upload a Kubit backup now'} onClick={() => api.offsiteBackup().then((r) => { watch(r); setTimeout(loadOff, 5000) }).catch((e) => toast(e.message, 'error'))}>Copy Kubit backup now</button>
          {off?.enabled && <span class="text-[12px] text-muted">{off.error ? <span class="text-bad">{off.error}</span> : `${off.target}: ${off.backups} backup(s), ${off.snapshots} snapshot(s), ${fmt.bytes(off.bytes)} · last backup ${off.lastBackup ? fmt.when(off.lastBackup) : 'never'}`}</span>}
        </div>
      </Section>
      <Section title="Kubit backup" help="Not the cluster: etcd snapshots live under each cluster\u2019s Backups tab. This is a sealed archive of ~/.kubit: the database (cluster secrets stay encrypted inside it), kubeconfigs, talosconfigs and the OpenTofu roots with their state. Binaries and the asset cache are excluded.">
        <Notice tone="muted">
          <div class="flex flex-col gap-2">
            <span>The archive is encrypted with this Mac's master key (Keychain: service <span class="mono">kubit</span>). To restore elsewhere, export the key here and set <span class="mono">KUBIT_MASTER_KEY</span> there:</span>
            <code class="mono block rounded bg-bg border border-border px-3 py-2">kubit key export</code>
            <span>Restore with the daemon stopped: <span class="mono">kubit restore file.kubitbak</span></span>
          </div>
        </Notice>
        <a class="btn btn-primary self-start" href="/api/v1/backup" download>Download backup</a>
      </Section>
    </div>
  )
}
