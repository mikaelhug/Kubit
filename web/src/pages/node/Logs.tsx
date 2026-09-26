import { useState } from 'preact/hooks'
import { logsUrl } from '../../api'
import { LogStream } from '../../components/LogStream'
import { Section } from '../../components/ui'
import { useServices } from './Services'

export function LogsTab({ ip, cluster }: { ip: string; cluster?: string }) {
  const { data: services } = useServices(ip, cluster)
  const [service, setService] = useState('')
  const [follow, setFollow] = useState(false)
  return (
    <Section title={service ? `${service} log` : 'Kernel log (dmesg)'}>
      <LogStream url={logsUrl(ip, service, follow)} follow={follow} onFollow={setFollow} height="!max-h-[70vh]" toolbar={
        <select class="input !w-48" value={service} onChange={(e) => setService((e.target as HTMLSelectElement).value)}>
          <option value="">dmesg</option>
          {(services ?? []).map((s) => <option key={s.id} value={s.id}>{s.id}</option>)}
        </select>
      } />
    </Section>
  )
}
