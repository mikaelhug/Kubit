import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type NodeRow } from '../api'
import { useOperationEvents } from '../events'
import { ErrorBox, EventLog, Pill, stateTone } from '../components/ui'

/** Every node Kubit knows: maintenance-mode candidates and cluster members. */
export function Inventory() {
  const [nodes, setNodes] = useState<NodeRow[]>([])
  const [targets, setTargets] = useState('')
  const [op, setOp] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const { events, op: status } = useOperationEvents(op ?? undefined)
  const load = () => api.nodes().then((ns) => { setNodes(ns); if (!targets && ns[0]) setTargets(ns[0].ip.replace(/\.\d+$/, '.0/24')) }).catch((e) => setError(e.message))
  useEffect(() => { load() }, []) // eslint-disable-line
  useEffect(() => { if (status && status.status !== 'running') load() }, [status]) // eslint-disable-line
  return (
    <div class="p-6 flex flex-col gap-4 max-w-[1200px]">
      <h1 class="text-xl font-semibold">Discovered nodes</h1>
      <ErrorBox error={error} />
      <div class="panel p-4 flex flex-col gap-3">
        <div class="flex gap-2">
          <input class="input mono" value={targets} onInput={(e) => setTargets((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24" />
          <button class="btn btn-primary shrink-0" disabled={status?.status === 'running'} onClick={() => api.discover(targets.split(/[,\s]+/).filter(Boolean)).then((r) => setOp(r.operationId)).catch((e) => setError(e.message))}>Scan</button>
        </div>
        {op !== null && <EventLog events={events} empty="Scanning…" />}
      </div>
      <div class="panel overflow-x-auto">
        <table class="data">
          <thead><tr><th class="pl-4">IP</th><th>Cluster</th><th>Hostname</th><th>Role</th><th>State</th><th>Arch</th><th>MAC</th><th>CPU</th><th>RAM</th><th>KVM</th><th class="pr-4">Last seen</th></tr></thead>
          <tbody>
            {nodes.map((n) => (
              <tr key={n.ip}>
                <td class="pl-4 mono"><a href={`/nodes/${n.ip}`} class="hover:underline">{n.ip}</a></td>
                <td>{n.cluster ? <a href={`/clusters/${n.cluster}`} class="text-accent hover:underline">{n.cluster}</a> : <span class="text-muted">unassigned</span>}</td>
                <td>{n.hostname || '—'}</td><td>{n.role || '—'}</td>
                <td><Pill tone={stateTone(n.state)}>{n.state}</Pill></td>
                <td>{n.arch}</td><td class="mono">{n.mac}</td>
                <td class="num">{n.inventory?.cpus ?? '—'}</td><td class="num">{n.inventory ? fmt.bytes(n.inventory.memoryBytes) : '—'}</td>
                <td>{n.inventory?.kvm ? 'yes' : 'no'}</td>
                <td class="pr-4 text-muted num">{n.lastSeen ? new Date(n.lastSeen).toLocaleString() : ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
