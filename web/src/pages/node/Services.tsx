import type { NodeRow, Service } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { ErrorBox, Pill, Section } from '../../components/ui'
import { stateTone } from '../../tone'
import { useServices } from './talos'

const columns: Column<Service>[] = [
  { id: 'id', header: 'Service', mono: true, sort: (s) => s.id, cell: (s) => s.id },
  { id: 'state', header: 'State', sort: (s) => s.state, cell: (s) => { const ok = s.healthy || (s.unknown && s.state === 'Running'); return <Pill tone={ok ? 'good' : s.state === 'Running' ? 'warn' : stateTone(s.state.toLowerCase())}>{s.state}{ok ? '' : ' · unhealthy'}</Pill> } },
  { id: 'last', header: 'Last event', cell: (s) => <span class="text-muted">{s.last}</span> },
]

export function ServicesTab({ ip, node }: { ip: string; node: NodeRow | null }) {
  const { data: services, error } = useServices(ip, node)
  return (
    <Section title="Talos services">
      <ErrorBox error={error} />
      <DataTable search={false} columns={columns} rows={services ?? []} rowKey={(s) => s.id} />
    </Section>
  )
}
