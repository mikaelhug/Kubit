import { useState } from 'preact/hooks'
import type { AddonInfo } from '../addons'
import { api, type PlatformSpec } from '../api'
import { clusters, toast } from '../store'
import { Dialog, ErrorBox, Field, inputValue } from './ui'

type PlatformKey = keyof PlatformSpec

const hashOf = (cluster: string) => clusters.value.find((c) => c.name === cluster)?.hash ?? ''


export function AddonToggle({ cluster, info, platform, label }: { cluster: string; info: AddonInfo; platform: PlatformSpec; label?: string }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  if (info.key === 'backup') return null
  const key = info.key as PlatformKey
  const on = !!platform[key]?.enabled
  const settings = !on && (key === 'metallb' || key === 'flux')
  const toggle = () => {
    if (settings) { setOpen(true); return }
    setBusy(true)
    api.setAddon(cluster, key, { enabled: !on, hash: hashOf(cluster) }).then(() => toast('cluster.yaml updated', 'good')).catch((e) => toast(e.message, 'error')).finally(() => setBusy(false))
  }
  return (
    <>
      <button class="btn btn-sm" disabled={busy} onClick={toggle}>{label ?? (on ? 'Disable' : 'Enable')}</button>
      {open && <AddonDialog cluster={cluster} name={info.name} addon={key} on={on} platform={platform} onClose={() => setOpen(false)} />}
    </>
  )
}

function AddonDialog({ cluster, name, addon, on, platform, onClose }: { cluster: string; name: string; addon: PlatformKey; on: boolean; platform: PlatformSpec; onClose: () => void }) {
  const repo = platform.flux.repository
  const [range, setRange] = useState(platform.metallb.range ?? '')
  const [url, setUrl] = useState(repo?.url ?? '')
  const [branch, setBranch] = useState(repo?.branch ?? 'main')
  const [path, setPath] = useState(repo?.path ?? './')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const enabling = !on
  const needsRange = enabling && addon === 'metallb' && !range
  const save = () => {
    setBusy(true)
    api.setAddon(cluster, addon, {
      hash: hashOf(cluster),
      enabled: enabling,
      ...(enabling && addon === 'metallb' ? { range } : {}),
      ...(enabling && addon === 'flux' ? { repository: { url, branch, path } } : {}),
    }).then(() => { toast('cluster.yaml updated', 'good'); onClose() }).catch((e) => setError(e.message)).finally(() => setBusy(false))
  }
  return (
    <Dialog title={`${enabling ? 'Enable' : 'Disable'} ${name}`} onClose={onClose}
      footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={busy || needsRange} onClick={save}>{busy ? 'Writing' : 'Write cluster.yaml'}</button></>}>
      <ErrorBox error={error} />
      {enabling && addon === 'metallb' && <Field label="Address range"><input class="input mono" value={range} placeholder="192.168.1.200-192.168.1.220" autofocus onInput={(e) => setRange(inputValue(e).trim())} /></Field>}
      {enabling && addon === 'flux' && (
        <>
          <Field label="Repository" hint="Optional"><input class="input mono" value={url} placeholder="https://github.com/you/apps.git" autofocus onInput={(e) => setUrl(inputValue(e).trim())} /></Field>
          <div class="grid grid-cols-2 gap-3">
            <Field label="Branch"><input class="input mono" value={branch} onInput={(e) => setBranch(inputValue(e).trim())} /></Field>
            <Field label="Path"><input class="input mono" value={path} onInput={(e) => setPath(inputValue(e).trim())} /></Field>
          </div>
        </>
      )}
    </Dialog>
  )
}
