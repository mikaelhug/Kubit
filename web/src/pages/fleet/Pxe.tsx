import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type PxeStatus } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { KeyValue, Notice, Pill, Section, type Tone } from '../../components/ui'

type Boot = NonNullable<PxeStatus['boots']>[number]
const stageTone: Record<string, Tone> = { dhcp: 'warn', ipxe: 'info', kernel: 'good' }
const stageText: Record<string, string> = { dhcp: 'firmware asked (DHCP)', ipxe: 'iPXE fetched boot script', kernel: 'Talos kernel downloaded' }

/** State of the separate `kubit pxe` process and what has booted through it. */
export function Pxe() {
  const [st, setSt] = useState<PxeStatus | null>(null)
  useEffect(() => {
    const load = () => api.pxe().then(setSt).catch(() => {})
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  }, [])
  const cols: Column<Boot>[] = [
    { id: 'mac', header: 'MAC', mono: true, sort: (b) => b.mac, cell: (b) => b.mac },
    { id: 'ip', header: 'IP', mono: true, sort: (b) => b.ip ?? '', cell: (b) => b.ip ? <a href={`/nodes/${b.ip}`} class="hover:underline">{b.ip}</a> : <span class="text-muted">—</span> },
    { id: 'arch', header: 'Arch', cell: (b) => b.arch || '—' },
    { id: 'stage', header: 'Stage', sort: (b) => b.stage, cell: (b) => <Pill tone={stageTone[b.stage] ?? 'muted'}>{stageText[b.stage] ?? b.stage}</Pill> },
    { id: 'count', header: 'Requests', align: 'right', sort: (b) => b.count, cell: (b) => b.count },
    { id: 'first', header: 'First seen', sort: (b) => b.firstSeen, cell: (b) => <span class="num text-muted">{fmt.when(b.firstSeen)}</span> },
    { id: 'last', header: 'Last seen', sort: (b) => b.lastSeen, cell: (b) => <span class="num text-muted">{fmt.when(b.lastSeen)}</span> },
  ]
  return (
    <div class="p-6 flex flex-col gap-5">
      <Section title="PXE boot" help="Zero-touch onboarding: machines that network-boot get iPXE over proxyDHCP/TFTP and a boot script that loads Talos into maintenance mode from a local cache of Image Factory assets. The LAN's own DHCP server keeps handing out addresses. Kubit's daemon cannot bind ports 67/69 itself, so the PXE server runs as a separate root process on the same machine.">
        {!st && <div class="text-muted">Checking…</div>}
        {st && !st.running && (
          <Notice tone="muted">
            <div class="flex flex-col gap-2">
              <span>The PXE server is not running ({st.error}). Start it on this machine, on the interface facing the machines to boot:</span>
              <code class="mono block rounded bg-bg border border-border px-3 py-2 select-all">{st.command}</code>
              <span class="text-muted">Status is read from {st.statusUrl} (Settings → Kubit).</span>
            </div>
          </Notice>
        )}
        {st?.running && (
          <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <div class="panel p-4">
              <KeyValue rows={[
                ['State', <Pill tone="good">running since {fmt.when(st.startedAt ?? '')}</Pill>],
                ['Interface', <span class="mono">{st.interface} ({st.ip})</span>],
                ['Boot script', <span class="mono">http://{st.ip}:{st.httpPort}/boot.ipxe</span>],
                ['Talos', <span class="mono">{st.talosVersion}</span>],
                ['Schematic', <span class="mono text-[12px] break-all">{st.schematicId}</span>],
              ]} />
            </div>
            <div class="panel p-4 flex flex-col gap-2">
              <span class="label">Log</span>
              <pre class="log !max-h-[220px]">{(st.log ?? []).slice(-60).join('\n') || 'Nothing yet.'}</pre>
            </div>
          </div>
        )}
      </Section>
      {st?.running && (
        <Section title={`Machines seen (${st.boots?.length ?? 0})`} help="A machine reaching the kernel stage boots into maintenance mode within a minute and then appears under Inventory after a scan.">
          <DataTable id="pxe" columns={cols} rows={st.boots ?? []} rowKey={(b) => b.mac} defaultSort={{ id: 'last', dir: 'desc' }} empty="No PXE requests yet." />
        </Section>
      )}
    </div>
  )
}
