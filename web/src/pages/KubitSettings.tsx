import { useEffect, useState } from 'preact/hooks'
import { api, type Settings } from '../api'
import { toast } from '../store'
import { ErrorBox, Field, Notice, Section } from '../components/ui'

export function KubitSettings() {
  const [s, setS] = useState<Settings | null>(null)
  const [orig, setOrig] = useState<Settings | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { api.settings().then((v) => { setS(v); setOrig(v) }).catch((e) => setError(e.message)) }, [])
  if (!s) return <div class="p-6 text-muted">{error ?? 'Loading…'}</div>
  const dirty = JSON.stringify(s) !== JSON.stringify(orig)
  const save = () => api.saveSettings(s).then((v) => { setS(v); setOrig(v); setError(null); toast('Settings saved', 'good') }).catch((e) => setError(e.message))
  return (
    <div class="p-6 flex flex-col gap-6 max-w-3xl">
      <Section title="Kubit settings" help="Preferences of this Kubit installation. Cluster-specific settings live under each cluster.">
        <ErrorBox error={error} />
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
      <Section title="Backup" help="A sealed archive of ~/.kubit: the database (cluster secrets stay encrypted inside it), kubeconfigs, talosconfigs and the OpenTofu roots with their state. Binaries and the asset cache are excluded.">
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
