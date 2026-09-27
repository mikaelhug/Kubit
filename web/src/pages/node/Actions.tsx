import { useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, type ClusterRow, type Inventory, type NodeDetail, type NodeRow, type NodeSpec } from '../../api'
import { LabVMControls, MakeLabHostDialog } from '../../components/labhost'
import { RetireDialog, useAdopt } from '../../components/Machine'
import { ReaddressDialog } from '../../components/ReaddressDialog'
import { RemoteManagement } from '../../components/RemoteManagement'
import { Action, ConfirmDialog, Dialog, Field, GroupHeading, MaintenanceNotice, Notice } from '../../components/ui'
import { canAdopt, canMakeLabHost, canRetire, isLabVM, readyClusters, wake } from '../../machine'
import { isDnsLabel } from '../../net'
import { runOp } from '../../ops'
import { statuses, toast } from '../../store'

type Confirm = 'drain' | 'reboot' | 'reboot-drain' | 'upgrade' | 'rename' | 'pool' | 'readdress'

const delta = (a?: Record<string, string>, b?: Record<string, string>) => {
  const out: string[] = []
  for (const k of Object.keys(a ?? {})) if (!(k in (b ?? {}))) out.push(`− ${k}`)
  for (const [k, v] of Object.entries(b ?? {})) if (a?.[k] !== v) out.push(`+ ${k}=${v}`)
  return out
}

function Delta({ label, items }: { label: string; items: string[] }) {
  return <li>{label}: {items.length ? items.map((d) => <span key={d} class="mono mr-2">{d}</span>) : 'unchanged'}</li>
}

export function ActionsTab({ node, k8s, inv, cluster, spec }: { node: NodeRow | null; k8s: NodeDetail | null; inv: Inventory | null; cluster?: ClusterRow; spec?: NodeSpec }) {
  const [confirm, setConfirm] = useState<Confirm | null>(null)
  const [to, setTo] = useState('')
  const [pool, setPool] = useState('')
  if (!node?.cluster || !cluster || !node.hostname) return <MachineActions node={node} />
  const name = cluster.name
  const host = node.hostname
  const talosVersion = cluster.spec.spec.talosVersion
  const pods = (k8s?.pods ?? []).filter((p) => p.owner !== 'DaemonSet' && p.phase === 'Running').length
  const podText = `${pods} pod${pods === 1 ? '' : 's'}`
  const cp = node.role === 'controlplane'
  const run = (p: Promise<{ operationId: number }>) => runOp(p).then((ok) => { if (ok) setConfirm(null) })
  const needsUpgrade = !!inv && inv.talosVersion !== talosVersion
  const pools = (cluster.spec.spec.pools ?? []).filter((p) => p.role === node.role && p.name !== spec?.pool)
  const target = pools.find((p) => p.name === pool)
  const current = (cluster.spec.spec.pools ?? []).find((p) => p.name === spec?.pool)
  const status = statuses.value.get(name)?.nodes.find((n) => n.hostname === host)
  const extensions = (p?: { extensions?: string[] }) => JSON.stringify(p?.extensions ?? cluster.spec.spec.extensions ?? [])
  return (
    <div class="flex flex-col gap-3 max-w-3xl">
      <GroupHeading title="Node" help="Kubernetes and Talos operations on this member." />
      <Action title={k8s?.unschedulable ? 'Uncordon' : 'Cordon'} what={k8s?.unschedulable ? 'Allow new pods here again.' : 'Stop new pods from landing here; running pods stay.'}
        button={k8s?.unschedulable ? 'Uncordon' : 'Cordon'} disabled={!k8s} onClick={() => run(k8s?.unschedulable ? api.uncordon(name, host) : api.cordon(name, host))} />
      <Action title="Drain" what={`Cordon, then evict ${podText}; DaemonSet pods stay.`} button="Drain" disabled={!k8s} onClick={() => setConfirm('drain')} />
      <Action title="Reboot" what="Reboot through the Talos API and wait for Ready." button="Reboot" disabled={!inv} onClick={() => setConfirm('reboot')} secondary={{ label: 'Drain, reboot, uncordon', onClick: () => setConfirm('reboot-drain') }} />
      <Action title="Upgrade Talos on this node" what={needsUpgrade ? `Runs ${inv?.talosVersion}; the cluster declares ${talosVersion}.` : `Already on the declared ${talosVersion}.`} button="Upgrade" disabled={!needsUpgrade} onClick={() => setConfirm('upgrade')} />
      <Action title="Rename" what="Change the hostname and Node name without a reboot." button="Rename" disabled={!inv || !k8s} onClick={() => { setTo(host); setConfirm('rename') }} />
      <Action title="Move to another pool" what={pools.length ? `Same-role pools: ${pools.map((p) => p.name).join(', ')}.` : `No other ${cp ? 'control-plane' : 'worker'} pool; add one under Settings.`} button="Move" disabled={!inv || pools.length === 0} onClick={() => { setPool(pools[0]?.name ?? ''); setConfirm('pool') }} />
      <Action title="Update address" what={spec?.network ? `Static ${spec.network.addresses.join(', ')}${spec.network.vlan ? ` on VLAN ${spec.network.vlan}` : ''}.` : `DHCP; declared ${node.ip}${status?.seenAt && status.seenAt !== node.ip ? `, last seen at ${status.seenAt}` : ''}.`} button="Update" disabled={!inv} onClick={() => setConfirm('readdress')} />
      <Action title="Remove from cluster" what={`Drain, delete the Node and reset Talos to maintenance mode.${cp ? ' etcd loses a member.' : ''}`} button="Remove" href={`/clusters/${name}/nodes`} />
      <GroupHeading title="Machine" help="The hardware under the node." />
      {isLabVM(node) ? <LabVMControls vm={node} /> : <RemoteManagement node={node} />}
      {!isLabVM(node) && <WakeAction node={node} />}
      {confirm === 'drain' && <ConfirmDialog title={`Drain ${host}`} action="Drain" cluster={name} onClose={() => setConfirm(null)} onConfirm={() => run(api.drain(name, host))}
        impact={<p>Cordons the node and evicts {podText} within 5 minutes, respecting PodDisruptionBudgets.</p>} />}
      {(confirm === 'reboot' || confirm === 'reboot-drain') && <ConfirmDialog title={`Reboot ${host}`} action={confirm === 'reboot-drain' ? 'Drain and reboot' : 'Reboot'} tone="danger" cluster={name} onClose={() => setConfirm(null)} onConfirm={() => run(api.rebootNode(name, host, confirm === 'reboot-drain'))}
        impact={<ul class="list-disc pl-5">{confirm === 'reboot-drain' ? <li>Drains {podText} first and uncordons afterwards.</li> : <li class="text-warn">Pods on this node are down for 1–2 minutes.</li>}{cp && <li>etcd loses this member's vote while it is down.</li>}</ul>} />}
      {confirm === 'upgrade' && <ConfirmDialog title={`Upgrade ${host} to Talos ${talosVersion}`} action="Upgrade node" cluster={name} onClose={() => setConfirm(null)} onConfirm={() => run(api.upgradeNode(name, host, talosVersion))}
        impact={<p>Installs the new image and reboots without draining; Talos rolls back if it fails to boot.</p>} />}
      {confirm === 'rename' && (
        <Dialog title={`Rename ${host}`} onClose={() => setConfirm(null)} footer={<><button class="btn" onClick={() => setConfirm(null)}>Cancel</button><button class="btn btn-primary" disabled={!isDnsLabel(to) || to === host || cluster.spec.spec.nodes.some((n) => n.hostname === to)} onClick={() => run(api.renameNode(name, host, to))}>Rename</button></>}>
          <MaintenanceNotice cluster={name} />
          <Field label="New hostname" hint="DNS label, unique in the cluster"><input class="input mono" value={to} onInput={(e) => setTo((e.target as HTMLInputElement).value.trim().toLowerCase())} /></Field>
          <p class="text-[13px]">Drains {podText} once and registers the node as <span class="mono">{to || host}</span> without a reboot.</p>
        </Dialog>
      )}
      {confirm === 'pool' && (
        <Dialog title={`Move ${host} to another pool`} onClose={() => setConfirm(null)} footer={<><button class="btn" onClick={() => setConfirm(null)}>Cancel</button><button class="btn btn-primary" disabled={!target} onClick={() => run(api.moveNodePool(name, host, pool))}>Move</button></>}>
          <MaintenanceNotice cluster={name} />
          <Field label="Target pool">
            <select class="input" value={pool} onChange={(e) => setPool((e.target as HTMLSelectElement).value)}>{pools.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}</select>
          </Field>
          {target && (
            <ul class="list-disc pl-5 text-[13px] flex flex-col gap-1">
              <Delta label="Labels" items={delta(current?.labels, target.labels)} />
              <Delta label="Taints" items={delta(current?.taints, target.taints)} />
              {extensions(target) !== extensions(current) ? <li class="text-warn">Different extensions: the node is re-imaged with one reboot.</li> : <li>Same image: re-applied without a reboot.</li>}
            </ul>
          )}
        </Dialog>
      )}
      {confirm === 'readdress' && status && <ReaddressDialog cluster={cluster} n={status} spec={spec} onClose={() => setConfirm(null)} />}
    </div>
  )
}

function MachineActions({ node }: { node: NodeRow | null }) {
  const { route } = useLocation()
  const [retire, setRetire] = useState(false)
  const [lab, setLab] = useState(false)
  const adopt = useAdopt()
  if (!node) return <Notice tone="muted">Loading</Notice>
  const ready = readyClusters()
  const vm = isLabVM(node)
  const summary = node.kind === 'maintenance' ? 'In Talos maintenance mode; not a cluster member.'
    : node.kind === 'configured' ? 'Runs Talos with a config Kubit did not apply; reset it to maintenance mode to adopt it.'
    : node.kind === 'booting' ? 'Armed for a network boot; waiting for Talos.'
    : vm ? 'The VM is off.' : 'Not running Talos.'
  return (
    <div class="flex flex-col gap-3 max-w-3xl">
      <Notice tone="muted">{summary}</Notice>
      <GroupHeading title="Machine" help="What can be done with this hardware." />
      <Action title="Adopt into a cluster" what={(ready.length ? `Join ${ready.map((c) => c.name).join(', ')} as a new node.` : 'No ready cluster yet; create one with this machine.') + (canAdopt(node) ? '' : ' Needs Talos maintenance mode first.')} button={ready.length ? 'Adopt' : 'New cluster'} disabled={!canAdopt(node)} onClick={() => adopt.start(node)} />
      {adopt.element}
      {vm ? <LabVMControls vm={node} /> : <RemoteManagement node={node} />}
      {canMakeLabHost(node) && <Action title="Make lab host" what={`Installs Debian with KVM (disk wiped) to run Talos VMs${node.oobType ? '' : '; you boot the installer'}.`} button="Make lab host" onClick={() => setLab(true)} />}
      {lab && <MakeLabHostDialog m={node} onClose={() => setLab(false)} />}
      {!vm && <WakeAction node={node} />}
      {canRetire(node) && <Action title="Retire" what="Delete this machine's record; a scan finds it again while it is online." button="Retire" onClick={() => setRetire(true)} />}
      {retire && <RetireDialog m={node} onClose={() => setRetire(false)} onDone={() => route('/fleet/inventory')} />}
    </div>
  )
}

function WakeAction({ node }: { node: NodeRow }) {
  const setWOL = (on: boolean) => api.setWOL(node.mac, on).catch((e) => toast(e.message, 'error'))
  return <Action title="Wake-on-LAN" what={node.wol ? 'Enabled: Kubit can power the machine on with a magic packet.' : 'Off; enable when the firmware supports it.'} button={node.wol ? 'Wake now' : 'Enable'}
    onClick={() => (node.wol ? wake(node.mac) : setWOL(true))}
    secondary={node.wol ? { label: 'Disable', onClick: () => setWOL(false) } : undefined} />
}
