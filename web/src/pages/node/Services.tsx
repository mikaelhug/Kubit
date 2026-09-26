import { api, type NodeRow, type Service } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { ErrorBox, Pill, Section } from '../../components/ui'
import { stateTone } from '../../tone'
import { useLive } from '../../useLive'

const columns: Column<Service>[] = [
  { id: 'id', header: 'Service', mono: true, sort: (s) => s.id, cell: (s) => s.id },
  { id: 'state', header: 'State', sort: (s) => s.state, cell: (s) => { const ok = s.healthy || (s.unknown && s.state === 'Running'); return <Pill tone={ok ? 'good' : s.state === 'Running' ? 'warn' : stateTone(s.state.toLowerCase())}>{s.state}{ok ? '' : ' · unhealthy'}</Pill> } },
  { id: 'last', header: 'Last event', cell: (s) => <span class="text-muted">{s.last}</span> },
]

export function talosLive(node: NodeRow | null) {
  return { scopes: [[node?.cluster ?? '', 'nodes']] as const, refresh: [node?.cluster ? '' : node?.lastSeen] }
}

export function useServices(ip: string, node: NodeRow | null) {
  const talos = talosLive(node)
  return useLive(() => api.services(ip), [ip], talos.scopes, { refresh: talos.refresh })
}

export function ServicesTab({ ip, node }: { ip: string; node: NodeRow | null }) {
  const { data: services, error } = useServices(ip, node)
  return (
    <Section title="Talos services" help="Talos system services and their health checks.">
      <ErrorBox error={error} />
      <DataTable search={false} columns={columns} rows={services ?? []} rowKey={(s) => s.id} />
    </Section>
  )
}
