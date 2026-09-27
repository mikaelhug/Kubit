import { useState } from 'preact/hooks'
import { api, splitList } from '../api'
import { subnet24 } from '../net'
import { running, watch } from '../ops'
import { settings } from '../store'

export function ScanBox({ fallbackIp, primary, openDrawer, onError }: { fallbackIp?: string; primary?: boolean; openDrawer?: boolean; onError: (message: string) => void }) {
  const [typed, setTyped] = useState<string | null>(null)
  const subnets = settings.value?.discoverySubnets ?? []
  const targets = typed ?? (subnets.length ? subnets.join(', ') : fallbackIp ? subnet24(fallbackIp) : '')
  const scanning = running.value.some((o) => o.kind === 'discover')
  const scan = () => api.discover(splitList(targets)).then((r) => watch(r, !!openDrawer)).catch((e) => onError(e.message))
  return (
    <div class="flex gap-2">
      <input class="input mono" value={targets} onInput={(e) => setTyped((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24, 10.0.0.5" aria-label="Subnets or addresses to scan" />
      <button class={`btn shrink-0 ${primary ? 'btn-primary' : ''}`} disabled={scanning || !targets.trim()} onClick={scan}>{scanning ? 'Scanning' : 'Scan'}</button>
    </div>
  )
}
