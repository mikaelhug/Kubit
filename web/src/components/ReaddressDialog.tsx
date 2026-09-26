import { useState } from 'preact/hooks'
import { api, splitList, type ClusterRow, type NodeNetwork, type NodeSpec, type NodeStatus } from '../api'
import { staticNetwork } from '../net'
import { watch } from '../store'
import { Dialog, ErrorBox, Field, MaintenanceNotice } from './ui'

export function ReaddressDialog({ cluster, n, spec, onClose }: { cluster: ClusterRow; n: NodeStatus; spec?: NodeSpec; onClose: () => void }) {
  const isEndpoint = cluster.spec.spec.controlPlane.endpoint === `https://${n.ip}:6443`
  const [mode, setMode] = useState<'dhcp' | 'static'>(spec?.network ? 'static' : 'dhcp')
  const seen = n.seenAt && n.seenAt !== n.ip ? n.seenAt : ''
  const [ip, setIp] = useState(seen || n.ip)
  const [network, setNetwork] = useState<NodeNetwork>(spec?.network ?? staticNetwork(seen || n.ip))
  const [error, setError] = useState<string | null>(null)
  const submit = () => api.readdressNode(cluster.name, n.hostname, mode === 'static' ? network : null, mode === 'static' ? network.addresses[0].split('/')[0] : ip)
    .then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Update address of ${n.hostname}`} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={isEndpoint && ip !== n.ip} onClick={submit}>Apply</button></>}>
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
      {mode === 'dhcp' ? (
        <Field label="Address Kubit should use" hint="Where the Talos API answers now"><input class="input mono" value={ip} onInput={(e) => setIp((e.target as HTMLInputElement).value.trim())} /></Field>
      ) : (
        <div class="grid grid-cols-2 gap-3">
          <Field label="Address (CIDR)"><input class="input mono" value={network.addresses[0] ?? ''} onInput={(e) => setNetwork({ ...network, addresses: [(e.target as HTMLInputElement).value.trim()] })} /></Field>
          <Field label="Gateway"><input class="input mono" value={network.gateway ?? ''} onInput={(e) => setNetwork({ ...network, gateway: (e.target as HTMLInputElement).value.trim() || undefined })} /></Field>
          <Field label="Nameservers" hint="Optional, comma-separated"><input class="input mono" value={(network.nameservers ?? []).join(', ')} onInput={(e) => { const v = splitList((e.target as HTMLInputElement).value); setNetwork({ ...network, nameservers: v.length ? v : undefined }) }} /></Field>
          <Field label="VLAN"><input class="input mono" type="number" min={0} max={4094} value={network.vlan ?? ''} onInput={(e) => { const v = Number((e.target as HTMLInputElement).value); setNetwork({ ...network, vlan: v > 0 ? v : undefined }) }} /></Field>
        </div>
      )}
      <p class="text-[12px] text-muted">Applies without a reboot and saves cluster.yaml.</p>
    </Dialog>
  )
}
