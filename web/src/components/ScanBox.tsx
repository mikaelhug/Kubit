import { useState } from 'preact/hooks'
import { api, fmt, splitList } from '../api'
import { now } from '../clock'
import { useLive } from '../useLive'

const every = (s: number) => s === 60 ? 'minute' : s % 60 === 0 ? `${s / 60} minutes` : `${s}s`

export function ScanBox({ onError }: { onError: (message: string) => void }) {
  const { data } = useLive(() => api.discoverState(), [], [['', 'discovery']], { onError: 'silent' })
  const [extra, setExtra] = useState('')
  const [scanning, setScanning] = useState(false)
  const busy = scanning || !!data?.scanning
  const scan = () => {
    setScanning(true)
    api.discover(splitList(extra)).catch((e) => onError(e.message)).finally(() => setScanning(false))
  }
  return (
    <div class="flex flex-wrap items-center gap-2 text-[13px]">
      <span class="text-muted">Scans</span>
      <span class="mono">{data?.subnets.join(', ') || '—'}</span>
      <span class="text-muted">{data?.everySeconds ? `every ${every(data.everySeconds)}` : ''}{data?.pingSeconds ? ` · known machines every ${every(data.pingSeconds)}` : ''}{busy ? ' · scanning' : data?.lastScanAt ? ` · last ${fmt.age((now.value - Date.parse(data.lastScanAt)) / 1000)} ago` : ''}</span>
      <span class="ml-auto flex gap-2">
        <input class="input mono !w-56" value={extra} onInput={(e) => setExtra((e.target as HTMLInputElement).value)} onKeyDown={(e) => e.key === 'Enter' && !busy && scan()} placeholder="Other subnet or address" aria-label="Other subnet or address to scan" />
        <button class="btn btn-sm shrink-0" disabled={busy} onClick={scan}>{busy ? 'Scanning' : 'Scan now'}</button>
      </span>
    </div>
  )
}
