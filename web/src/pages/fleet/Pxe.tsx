import { useEffect, useState } from 'preact/hooks'
import { refreshKey, settings, toast } from '../../store'
import { api, fmt, type PxeStatus } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { Code, KeyValue, Notice, Pill, Section, type Tone } from '../../components/ui'

type Boot = NonNullable<PxeStatus['boots']>[number]
const stageTone: Record<string, Tone> = { dhcp: 'warn', ipxe: 'info', kernel: 'good' }
const stageText: Record<string, string> = { dhcp: 'firmware asked (DHCP)', ipxe: 'iPXE fetched boot script', debian: 'Debian installer script fetched', kernel: 'kernel downloaded' }

/** State of the separate `kubit pxe` process and what has booted through it. */
export function Pxe() {
  const [st, setSt] = useState<PxeStatus | null>(null)
  useEffect(() => {
    api.pxe().then(setSt).catch(() => {})
  }, [refreshKey('', 'pxe')])
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
      <Section title="Network boot" help="Boots machines into Talos maintenance mode next to the LAN's own DHCP. Cluster members are left alone. Runs as a separate root process."
        actions={<EnrollmentSwitch />}>
        {!st && <div class="text-muted">Checking…</div>}
        {st && !st.running && (
          <Notice tone="muted">
            <div class="flex flex-col gap-2">
              <span>Not running. Start it in a terminal (ports 67/69 need root):</span>
              <Code text={st.command ?? ''} />
              <span class="text-muted">Safe to leave running. As a service instead: <span class="mono select-all">{st.serviceCommand}</span></span>
            </div>
          </Notice>
        )}
        {st?.running && (
          <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <div class="panel p-4">
              <KeyValue rows={[
                ['State', <span class="flex items-center gap-2"><Pill tone="good">running since {fmt.when(st.startedAt ?? '')}</Pill>{st.httpOnly && <Pill tone="warn" title="No DHCP or TFTP: machines must be booted by hand from the boot assets">HTTP only</Pill>}</span>],
                ['Interface', <span class="flex flex-col"><span class="mono">{st.interface} ({st.ip})</span><span class="text-[11px] text-muted">Clients must share this segment. Wi-Fi works only if the access point forwards DHCP both ways.</span></span>],
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
        <Section title={`Machines seen (${st.boots?.length ?? 0})`} help="Kernel stage means maintenance mode within a minute; then it appears in Inventory.">
          <DataTable id="pxe" columns={cols} rows={st.boots ?? []} rowKey={(b) => b.mac} defaultSort={{ id: 'last', dir: 'desc' }} empty="No PXE requests yet." />
        </Section>
      )}
    </div>
  )
}

/** Open: any unknown machine that network-boots gets Talos. Closed: only machines Kubit already knows or armed with Boot into Talos. */
function EnrollmentSwitch() {
  const s = settings.value
  if (!s) return null
  const set = (v: 'open' | 'closed') => api.saveSettings({ ...s, pxeEnrollment: v }).then(() => toast(v === 'open' ? 'Enrollment open: unknown machines get Talos' : 'Enrollment closed: only known or armed machines get Talos', 'good')).catch((e) => toast(e.message, 'error'))
  return (
    <label class="flex items-center gap-2 text-[13px]"><span class="text-muted">Enrollment</span>
      <select class="input !py-1 w-auto" value={s.pxeEnrollment ?? 'open'} onChange={(e) => set((e.target as HTMLSelectElement).value as any)}>
        <option value="open">Open — unknown machines get Talos</option>
        <option value="closed">Closed — only known or armed machines</option>
      </select>
    </label>
  )
}
