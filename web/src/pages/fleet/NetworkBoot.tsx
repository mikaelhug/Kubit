import { api, fmt, type PxeStatus } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { Code, KeyValue, Notice, Pill, Section } from '../../components/ui'
import { settings, toast } from '../../store'
import type { Tone } from '../../tone'
import { useLive } from '../../useLive'

type Boot = NonNullable<PxeStatus['boots']>[number]
const stageTone: Record<string, Tone> = { dhcp: 'warn', ipxe: 'info', debian: 'info', kernel: 'good' }
const stageText: Record<string, string> = { dhcp: 'firmware asked (DHCP)', ipxe: 'iPXE fetched boot script', debian: 'Debian installer script fetched', kernel: 'kernel downloaded' }

const cols: Column<Boot>[] = [
  { id: 'mac', header: 'MAC', mono: true, sort: (b) => b.mac, cell: (b) => <a href={`/machines/${b.mac}`} class="hover:underline">{b.mac}</a> },
  { id: 'ip', header: 'IP', mono: true, sort: (b) => b.ip ?? '', cell: (b) => b.ip || <span class="text-muted">—</span> },
  { id: 'arch', header: 'Arch', cell: (b) => b.arch || '—' },
  { id: 'stage', header: 'Stage', sort: (b) => b.stage, cell: (b) => <Pill tone={stageTone[b.stage] ?? 'muted'}>{stageText[b.stage] ?? b.stage}</Pill> },
  { id: 'count', header: 'Requests', align: 'right', sort: (b) => b.count, cell: (b) => b.count },
  { id: 'first', header: 'First seen', sort: (b) => b.firstSeen, cell: (b) => <span class="text-muted">{fmt.when(b.firstSeen)}</span> },
  { id: 'last', header: 'Last seen', sort: (b) => b.lastSeen, cell: (b) => <span class="text-muted">{fmt.when(b.lastSeen)}</span> },
]

export function NetworkBoot() {
  const { data: st } = useLive(() => api.pxe(), [], [['', 'pxe']], { onError: 'silent' })
  return (
    <div class="p-5 flex flex-col gap-4">
      <Section title="Network boot" help="The PXE server boots machines into Talos maintenance mode next to the LAN's DHCP." actions={<EnrollmentSwitch />}>
        {!st && <div class="text-muted">Checking</div>}
        {st && !st.running && (
          <Notice tone="muted">
            <div class="flex flex-col gap-2">
              <span>Not running; start it with:</span>
              <Code text={st.command ?? ''} />
              <span class="text-muted">As a service: <span class="mono select-all">{st.serviceCommand}</span></span>
            </div>
          </Notice>
        )}
        {st?.running && (
          <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <div class="panel p-3">
              <KeyValue rows={[
                ['State', <span class="flex items-center gap-2"><Pill tone="good">running since {fmt.when(st.startedAt ?? '')}</Pill>{st.httpOnly && <Pill tone="warn" title="No DHCP or TFTP; boot machines by hand from the boot assets">HTTP only</Pill>}{!st.ip && <Pill tone="bad">no address</Pill>}</span>],
                ['Interface', <span class="flex flex-col"><span class="mono">{st.ip ? `${st.interface} (${st.ip})` : st.interface}</span><span class="text-[11px] text-muted">Clients must share this segment.</span></span>],
                ['Boot script', st.ip ? <span class="mono">http://{st.ip}:{st.httpPort}/boot.ipxe</span> : <span class="text-muted">No address on {st.interface}</span>],
                ['Talos', <span class="mono">{st.talosVersion}</span>],
                ['Schematic', <span class="mono text-[12px] break-all">{st.schematicId}</span>],
              ]} />
            </div>
            <div class="panel p-3 flex flex-col gap-2">
              <span class="label">Log</span>
              <pre class="log !max-h-[220px]">{(st.log ?? []).slice(-60).join('\n') || 'Nothing yet.'}</pre>
            </div>
          </div>
        )}
      </Section>
      {st?.running && (
        <Section title={`Machines seen (${st.boots?.length ?? 0})`}>
          <DataTable id="pxe" columns={cols} rows={st.boots ?? []} rowKey={(b) => b.mac} defaultSort={{ id: 'last', dir: 'desc' }} empty="No PXE requests yet." />
        </Section>
      )}
    </div>
  )
}

function EnrollmentSwitch() {
  const s = settings.value
  if (!s) return null
  const set = (v: 'open' | 'closed') => api.saveSettings({ ...s, pxeEnrollment: v }).then(() => toast(v === 'open' ? 'Enrollment open' : 'Enrollment closed', 'good')).catch((e) => toast(e.message, 'error'))
  return (
    <label class="flex items-center gap-2 text-[13px]"><span class="text-muted">Enrollment</span>
      <select class="input !py-1 w-auto" value={s.pxeEnrollment ?? 'open'} onChange={(e) => set((e.target as HTMLSelectElement).value as 'open' | 'closed')}>
        <option value="open">Open — unknown machines get Talos</option>
        <option value="closed">Closed — only known or armed machines</option>
      </select>
    </label>
  )
}
