import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type NodeRow } from '../../api'
import { toast, watch } from '../../store'
import { DataTable, type Column } from '../../components/DataTable'
import { ErrorBox, Pill, Section, stateTone } from '../../components/ui'
import { operations } from '../../store'

/** Every machine Kubit knows: maintenance-mode candidates and cluster members. */
export function Inventory() {
  const [nodes, setNodes] = useState<NodeRow[]>([])
  const [targets, setTargets] = useState('')
  const [error, setError] = useState<string | null>(null)
  const load = () => api.nodes().then((ns) => { setNodes(ns); if (!targets && ns[0]) setTargets(ns[0].ip.replace(/\.\d+$/, '.0/24')) }).catch((e) => setError(e.message))
  useEffect(() => { load() }, []) // eslint-disable-line
  const finishedDiscoveries = [...operations.value.values()].filter((o) => o.kind === 'discover' && o.status !== 'running').length
  useEffect(() => { load() }, [finishedDiscoveries]) // eslint-disable-line
  const scanning = [...operations.value.values()].some((o) => o.kind === 'discover' && o.status === 'running')

  const columns: Column<NodeRow>[] = [
    { id: 'ip', header: 'IP', sort: (n) => n.ip, mono: true, cell: (n) => <a href={`/nodes/${n.ip}`} class="hover:underline">{n.ip}</a> },
    { id: 'state', header: 'State', sort: (n) => n.state, cell: (n) => <Pill tone={stateTone(n.state)}>{n.state}</Pill> },
    { id: 'cluster', header: 'Cluster', sort: (n) => n.cluster, cell: (n) => n.cluster ? <a href={`/clusters/${n.cluster}/nodes`} class="text-accent hover:underline">{n.cluster}</a> : <span class="text-muted">unassigned</span> },
    { id: 'hostname', header: 'Hostname', sort: (n) => n.hostname, cell: (n) => n.hostname || <span class="text-muted">—</span> },
    { id: 'role', header: 'Role', sort: (n) => n.role, cell: (n) => n.role || <span class="text-muted">—</span> },
    { id: 'arch', header: 'Arch', sort: (n) => n.arch, cell: (n) => n.arch },
    { id: 'mac', header: 'MAC', sort: (n) => n.mac, mono: true, cell: (n) => n.mac },
    { id: 'talos', header: 'Talos', sort: (n) => n.talosVersion, mono: true, cell: (n) => n.talosVersion },
    { id: 'cpu', header: 'CPU', align: 'right', sort: (n) => n.inventory?.cpus ?? 0, cell: (n) => n.inventory?.cpus ?? '—' },
    { id: 'ram', header: 'RAM', align: 'right', sort: (n) => n.inventory?.memoryBytes ?? 0, cell: (n) => n.inventory ? fmt.bytes(n.inventory.memoryBytes) : '—' },
    { id: 'disks', header: 'Disks', mono: true, text: (n) => (n.inventory?.disks ?? []).map((d) => d.devPath).join(' '), cell: (n) => (n.inventory?.disks.filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb').map((d) => `${d.devPath} ${fmt.bytes(d.sizeBytes)}`).join(', ')) || '—' },
    { id: 'kvm', header: 'KVM', sort: (n) => n.inventory?.kvm ? 1 : 0, cell: (n) => n.inventory?.kvm ? 'yes' : 'no' },
    { id: 'seen', header: 'Last seen', sort: (n) => n.lastSeen, cell: (n) => <span class="num text-muted">{fmt.datetime(n.lastSeen)}</span> },
  ]

  return (
    <div class="p-6 flex flex-col gap-5">
      <Section title="Inventory" help="Machines booted from a Talos ISO or over PXE wait in maintenance mode on port 50000. Scanning records their hardware so they can be adopted into a cluster.">
        <ErrorBox error={error} />
        <div class="panel p-4 flex gap-2">
          <input class="input mono" value={targets} onInput={(e) => setTargets((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24, 10.0.0.5" aria-label="Subnets or addresses to scan" />
          <button class="btn btn-primary shrink-0" disabled={scanning} onClick={() => api.discover(targets.split(/[,\s]+/).filter(Boolean)).then((r) => watch(r)).catch((e) => toast(e.message, 'error'))}>{scanning ? 'Scanning…' : 'Scan'}</button>
        </div>
        <DataTable id="inventory" columns={columns} rows={nodes} rowKey={(n) => n.ip} defaultSort={{ id: 'ip', dir: 'asc' }} empty="No machines known yet. Scan a subnet." />
      </Section>
    </div>
  )
}
