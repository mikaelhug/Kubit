import { Notice, Section } from '../../components/ui'

export function Backup() {
  return (
    <Section title="Kubit backup" help="Sealed archive of Kubit's own state: database, kubeconfigs, talosconfigs, OpenTofu state. Cluster data is under each cluster's Backups tab.">
      <Notice tone="muted">
        <div class="flex flex-col gap-2">
          <span>The archive is sealed with this host's master key. To restore elsewhere, export the key here and set <span class="mono">KUBIT_MASTER_KEY</span> there:</span>
          <code class="mono block rounded bg-bg border border-border px-3 py-2">kubit key export</code>
          <span>Restore with the daemon stopped: <span class="mono">kubit restore file.kubitbak</span></span>
        </div>
      </Notice>
      <a class="btn btn-primary self-start" href="/api/v1/backup" download>Download backup</a>
    </Section>
  )
}
