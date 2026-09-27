import { useState } from 'preact/hooks'
import { api, type ClusterRow, type NodeNetwork, type NodeSpec, type NodeStatus } from '../api'
import { addrOf, staticNetwork } from '../net'
import { watch } from '../ops'
import { StaticNetworkFields } from './StaticNetworkFields'
import { Dialog, ErrorBox, Field, MaintenanceNotice } from './ui'

export function ReaddressDialog({ cluster, n, spec, onClose }: { cluster: ClusterRow; n: NodeStatus; spec?: NodeSpec; onClose: () => void }) {
  const isEndpoint = cluster.spec.spec.controlPlane.endpoint === `https://${n.ip}:6443`
  const [mode, setMode] = useState<'dhcp' | 'static'>(spec?.network ? 'static' : 'dhcp')
  const seen = n.seenAt && n.seenAt !== n.ip ? n.seenAt : ''
  const [ip, setIp] = useState(seen || n.ip)
  const [network, setNetwork] = useState<NodeNetwork>(spec?.network ?? staticNetwork(seen || n.ip))
  const [error, setError] = useState<string | null>(null)
  const target = mode === 'static' ? addrOf(network.addresses[0] ?? '') : ip
  const submit = () => api.readdressNode(cluster.name, n.hostname, mode === 'static' ? network : null, target)
    .then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Update address of ${n.hostname}`} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={!target || (isEndpoint && target !== n.ip)} onClick={submit}>Apply</button></>}>
      <ErrorBox error={error} />
      <MaintenanceNotice cluster={cluster.name} />
      {seen && <p class="text-[13px]">Declared <span class="mono">{n.ip}</span>; last seen at <span class="mono">{seen}</span>.</p>}
      {isEndpoint && <p class="text-[13px] text-warn">This node is the API endpoint; set a VIP before changing its address.</p>}
      <Field label="Mode">
        <select class="input" value={mode} onChange={(e) => setMode((e.target as HTMLSelectElement).value as 'dhcp' | 'static')}>
          <option value="dhcp">DHCP — record the new lease</option>
          <option value="static">Static — pin in the machine config</option>
        </select>
      </Field>
      {mode === 'dhcp'
        ? <Field label="Address Kubit should use" hint="Where the Talos API answers now"><input class="input mono" value={ip} onInput={(e) => setIp((e.target as HTMLInputElement).value.trim())} /></Field>
        : <StaticNetworkFields value={network} lease={seen || n.ip} onChange={setNetwork} />}
      <p class="text-[12px] text-muted">Applies without a reboot and saves cluster.yaml.</p>
    </Dialog>
  )
}
