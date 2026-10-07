import { useState } from 'preact/hooks'
import type { AddonInfo } from '../addons'
import { api, type PlatformSpec } from '../api'
import { toast, writeClusterYaml } from '../store'
import { Dialog, ErrorBox, Field, inputValue } from './ui'

type PlatformKey = keyof PlatformSpec

export function AddonToggle({ cluster, info, platform, label }: { cluster: string; info: AddonInfo; platform: PlatformSpec; label?: string }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  if (info.key === 'backup') return null
  const key = info.key as PlatformKey
  const on = !!platform[key]?.enabled
  const settings = !on && key === 'metallb'
  const toggle = () => {
    if (settings) { setOpen(true); return }
    setBusy(true)
    writeClusterYaml(cluster, (hash) => api.setAddon(cluster, key, { enabled: !on, hash })).catch((e) => toast(e.message, 'error')).finally(() => setBusy(false))
  }
  return (
    <>
      <button class="btn btn-sm" disabled={busy} onClick={toggle}>{label ?? (on ? 'Disable' : 'Enable')}</button>
      {open && <AddonDialog cluster={cluster} name={info.name} addon={key} on={on} platform={platform} onClose={() => setOpen(false)} />}
    </>
  )
}

function AddonDialog({ cluster, name, addon, on, platform, onClose }: { cluster: string; name: string; addon: PlatformKey; on: boolean; platform: PlatformSpec; onClose: () => void }) {
  const [range, setRange] = useState(platform.metallb.range ?? '')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const enabling = !on
  const needsRange = enabling && addon === 'metallb' && !range
  const save = () => {
    setBusy(true)
    writeClusterYaml(cluster, (hash) => api.setAddon(cluster, addon, { hash, enabled: enabling, ...(enabling && addon === 'metallb' ? { range } : {}) }))
      .then(onClose).catch((e) => setError(e.message)).finally(() => setBusy(false))
  }
  return (
    <Dialog title={`${enabling ? 'Enable' : 'Disable'} ${name}`} onClose={onClose}
      footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={busy || needsRange} onClick={save}>{busy ? 'Writing' : 'Write cluster.yaml'}</button></>}>
      <ErrorBox error={error} />
      {enabling && addon === 'metallb' && <Field label="Address range"><input class="input mono" value={range} placeholder="192.168.1.200-192.168.1.220" autofocus onInput={(e) => setRange(inputValue(e).trim())} /></Field>}
    </Dialog>
  )
}
