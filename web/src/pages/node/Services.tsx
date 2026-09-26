import { api, type Service } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { ErrorBox, Pill, Section } from '../../components/ui'
import { stateTone } from '../../tone'
import { useLive } from '../../useLive'

const columns: Column<Service>[] = [
  { id: 'id', header: 'Service', mono: true, sort: (s) => s.id, cell: (s) => s.id },
  { id: 'state', header: 'State', sort: (s) => s.state, cell: (s) => { const ok = s.healthy || (s.unknown && s.state === 'Running'); return <Pill tone={ok ? 'good' : s.state === 'Running' ? 'warn' : stateTone(s.state.toLowerCase())}>{s.state}{ok ? '' : ' · unhealthy'}</Pill> } },
  { id: 'last', header: 'Last event', cell: (s) => <span class="text-muted">{s.last}</span> },
]

export function useServices(ip: string, cluster?: string) {
  return useLive(() => api.services(ip), [ip], [[cluster ?? '', 'nodes']])
}

export function ServicesTab({ ip, cluster }: { ip: string; cluster?: string }) {
  const { data: services, error } = useServices(ip, cluster)
  return (
    <Section title="Talos services" help="Talos system services and their health checks.">
      <ErrorBox error={error} />
      <DataTable search={false} columns={columns} rows={services ?? []} rowKey={(s) => s.id} />
    </Section>
  )
}
