import { useState } from 'preact/hooks'
import { api, splitList } from '../api'
import { subnet24 } from '../net'

export function ScanBox({ fallbackIp, primary, onError }: { fallbackIp?: string; primary?: boolean; onError: (message: string) => void }) {
  const [typed, setTyped] = useState<string | null>(null)
  const targets = typed ?? (fallbackIp ? subnet24(fallbackIp) : '')
  const [scanning, setScanning] = useState(false)
  const scan = () => {
    setScanning(true)
    api.discover(splitList(targets)).catch((e) => onError(e.message)).finally(() => setScanning(false))
  }
  return (
    <div class="flex gap-2">
      <input class="input mono" value={targets} onInput={(e) => setTyped((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24, 10.0.0.5" aria-label="Subnets or addresses to scan" />
      <button class={`btn shrink-0 ${primary ? 'btn-primary' : ''}`} disabled={scanning || !targets.trim()} onClick={scan}>{scanning ? 'Scanning' : 'Scan'}</button>
    </div>
  )
}
