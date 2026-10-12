import { useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type NodeStatus } from '../api'
import { roleLabel } from '../machine'
import { applyRuns, plans, toast, versions, writeClusterYaml } from '../store'
import { latestStable, talosUpdate } from '../versions'
import type { ClusterCtx } from '../pages/cluster/ClusterPage'
import { ConfirmDialog, Menu, type MenuItem } from './ui'

type Action = 'restart' | 'shutdown' | 'power-on' | 'update'

const up = (n: NodeStatus) => n.talosReachable && !n.off
const workersFirst = (nodes: NodeStatus[]) => [...nodes.filter((n) => n.role !== 'controlplane'), ...nodes.filter((n) => n.role === 'controlplane')]
const named = (verb: string, nodes: NodeStatus[]) => `${verb} ${nodes.length === 1 ? nodes[0].hostname : `${nodes.length} nodes`}`

export function NodeActions({ ctx, picked, onDone }: { ctx: ClusterCtx; picked: NodeStatus[]; onDone: () => void }) {
  const { name, status, cluster } = ctx
  const [open, setOpen] = useState<Action | null>(null)
  const { route } = useLocation()
  const nodes = status?.nodes ?? []
  const macOf = (n: NodeStatus) => cluster.spec.spec.nodes.find((s) => s.hostname === n.hostname)?.mac
  const latest = latestStable(versions.value)

  const restart = status?.apiReachable ? picked.filter(up) : []
  const shutdown = workersFirst(picked.filter(up))
  const whole = shutdown.length > 0 && nodes.filter((n) => n.declared).every((n) => shutdown.includes(n) || !up(n))
  const wake = picked.filter((n) => !up(n) && !!macOf(n))
  const update = latest ? picked.filter((n) => n.declared && talosUpdate(n.talosTarget || n.talosVersion, versions.value)) : []
  const busy = !!applyRuns.value.get(name)?.running || plans.value.get(name)?.state === 'applying'

  const items: MenuItem[] = []
  if (restart.length) items.push({ label: 'Restart', onSelect: () => setOpen('restart') })
  if (wake.length) items.push({ label: 'Power on', onSelect: () => setOpen('power-on') })
  if (update.length) items.push({ label: `Update to Talos ${latest}`, onSelect: () => setOpen('update') })
  if (shutdown.length) items.push({ label: whole ? `Shut down ${name}` : 'Shut down', danger: true, onSelect: () => setOpen('shutdown') })

  const close = () => setOpen(null)
  const power = (action: 'restart' | 'shutdown' | 'power-on', targets: NodeStatus[]) =>
    api.nodePower(name, action, targets.map((n) => n.hostname), action === 'shutdown' && whole)
      .then(() => { close(); onDone() })
      .catch((e) => toast(e.message, 'error'))

  return (
    <>
      <Menu label="Actions" items={items} disabled={busy} />
      {open === 'restart' && (
        <ConfirmDialog title={named('Restart', restart)} action="Restart" impact={<NodeList nodes={restart} />} onClose={close} onConfirm={() => power('restart', restart)} />
      )}
      {open === 'shutdown' && (
        <ConfirmDialog title={whole ? `Shut down ${name}` : named('Shut down', shutdown)} action="Shut down" tone="danger" impact={<NodeList nodes={shutdown} />} onClose={close} onConfirm={() => power('shutdown', shutdown)} />
      )}
      {open === 'power-on' && (
        <ConfirmDialog title={named('Power on', wake)} action="Power on" impact={<NodeList nodes={wake} detail={macOf} />} onClose={close} onConfirm={() => power('power-on', wake)} />
      )}
      {open === 'update' && latest && (
        <ConfirmDialog title={`${named('Update', update)} to Talos ${latest}`} action="Write cluster.yaml" onClose={close}
          impact={<NodeList nodes={update} detail={(n) => `${n.talosTarget || n.talosVersion} → ${latest}`} />}
          onConfirm={() => writeClusterYaml(name, (hash) => api.setVersions(name, { talosVersion: latest, hostnames: update.map((n) => n.hostname), hash }))
            .then(() => { close(); onDone(); route(`/clusters/${name}/changes`) })
            .catch((e) => toast(e.message, 'error'))} />
      )}
    </>
  )
}

function NodeList({ nodes, detail }: { nodes: NodeStatus[]; detail?: (n: NodeStatus) => string | undefined }) {
  return (
    <table class="data">
      <tbody>
        {nodes.map((n) => (
          <tr key={n.hostname}>
            <td class="mono">{n.hostname}</td>
            <td>{roleLabel(n.role)}</td>
            <td class="mono text-muted">{detail ? detail(n) : n.ip}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
