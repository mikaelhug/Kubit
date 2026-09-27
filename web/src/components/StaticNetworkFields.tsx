import type { NodeNetwork } from '../api'
import { guessGateway, prefixOf } from '../net'
import { ListInput } from './ListInput'
import { Field } from './ui'

export function StaticNetworkFields({ value, onChange, lease, compact }: { value: NodeNetwork; onChange: (v: NodeNetwork) => void; lease?: string; compact?: boolean }) {
  const set = (p: Partial<NodeNetwork>) => onChange({ ...value, ...p })
  const cls = (w: string) => `input mono ${compact ? `!py-1 ${w}` : ''}`
  const addr = value.addresses[0] ?? ''
  const address = <input class={cls('w-44')} aria-label="Address" value={addr} placeholder={lease ? `${lease}/24` : 'address/prefix'} onInput={(e) => set({ addresses: [(e.target as HTMLInputElement).value.trim()] })} />
  const gateway = <input class={cls('w-32')} aria-label="Gateway" value={value.gateway ?? ''} placeholder={guessGateway(addr || lease || '', prefixOf(addr, 24)) || 'gateway'} onInput={(e) => set({ gateway: (e.target as HTMLInputElement).value.trim() || undefined })} />
  const dns = <ListInput class={cls('w-40')} label="Nameservers" value={value.nameservers} placeholder="cluster default" onChange={(v) => set({ nameservers: v.length ? v : undefined })} />
  const vlan = <input class={cls('w-20')} aria-label="VLAN" type="number" min={0} max={4094} value={value.vlan ?? ''} placeholder="none" onInput={(e) => { const v = Number((e.target as HTMLInputElement).value); set({ vlan: v > 0 ? v : undefined }) }} />
  if (compact) return <span class="flex gap-2">{address}{gateway}{dns}{vlan}</span>
  return (
    <div class="grid grid-cols-2 gap-3">
      <Field label="Address (CIDR)">{address}</Field>
      <Field label="Gateway">{gateway}</Field>
      <Field label="Nameservers" hint="Optional">{dns}</Field>
      <Field label="VLAN">{vlan}</Field>
    </div>
  )
}
