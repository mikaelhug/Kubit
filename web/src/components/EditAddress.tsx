import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, type NodeNetworkView } from '../api'
import { toast } from '../store'
import { DnsFields, dnsList, dnsPair, type DnsPair } from './DnsFields'
import { Dialog, ErrorBox, Field } from './ui'

export function EditAddress({ cluster, hostname, children }: { cluster: string; hostname: string; children: ComponentChildren }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button class="hover:underline text-left" title="Change address" onClick={() => setOpen(true)}>{children}</button>
      {open && <AddressDialog cluster={cluster} hostname={hostname} onClose={() => setOpen(false)} />}
    </>
  )
}

function AddressDialog({ cluster, hostname, onClose }: { cluster: string; hostname: string; onClose: () => void }) {
  const [view, setView] = useState<NodeNetworkView | null>(null)
  const [mode, setMode] = useState<'dhcp' | 'static'>('dhcp')
  const [address, setAddress] = useState('')
  const [gateway, setGateway] = useState('')
  const [dns, setDns] = useState<DnsPair>(['', ''])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    api.nodeNetwork(cluster, hostname).then((v) => {
      setView(v)
      setMode(v.declared ? 'static' : 'dhcp')
      setAddress(v.declared?.addresses[0] ?? v.live.address ?? `${v.ip}/24`)
      setGateway(v.declared?.gateway ?? v.live.gateway ?? '')
      setDns(dnsPair(v.declared?.nameservers ?? (v.clusterNameservers?.length ? [] : v.live.nameservers)))
    }).catch((e) => setError(e.message))
  }, [])
  const isStatic = mode === 'static'
  const unchanged = view && !isStatic && !view.declared
  const save = () => {
    setBusy(true)
    setError(null)
    api.setNodeNetwork(cluster, hostname, isStatic ? { static: true, address: address.trim(), gateway: gateway.trim(), nameservers: dnsList(dns), hash: view!.hash } : { static: false, hash: view!.hash })
      .then(() => { toast('cluster.yaml updated', 'good'); onClose() })
      .catch((e) => setError(e.message))
      .finally(() => setBusy(false))
  }
  return (
    <Dialog title={`Address of ${hostname}`} onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class="btn btn-primary" disabled={!view || busy || !!unchanged} onClick={save}>{busy ? 'Writing' : 'Write cluster.yaml'}</button>
      </>
    }>
      <ErrorBox error={error} />
      {view && (
        <>
          <Field label="Address">
            <span class="flex gap-2">
              <select class="input !w-28" value={mode} onChange={(e) => setMode((e.target as HTMLSelectElement).value as 'dhcp' | 'static')}>
                <option value="dhcp">DHCP</option>
                <option value="static">Static</option>
              </select>
              <input class="input mono" value={isStatic ? address : view.ip} disabled={!isStatic} autofocus placeholder="192.168.5.51/24" aria-label="Address" onInput={(e) => setAddress((e.target as HTMLInputElement).value)} />
            </span>
          </Field>
          {isStatic && (
            <>
              <Field label="Gateway"><input class="input mono" value={gateway} onInput={(e) => setGateway((e.target as HTMLInputElement).value)} /></Field>
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <DnsFields value={dns} onChange={setDns} defaults={view.clusterNameservers} />
              </div>
            </>
          )}
        </>
      )}
    </Dialog>
  )
}
