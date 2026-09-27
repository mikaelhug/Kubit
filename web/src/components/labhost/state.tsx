import { useEffect, useState } from 'preact/hooks'
import { fmt, type NodeRow } from '../../api'
import { labHostKey, onMac } from '../../machine'
import { loadHealth, openAlerts } from '../../store'
import { AlertGroup } from '../Alerts'
import { Code, Notice } from '../ui'
import { MakeLabHostDialog } from './dialogs'

const installStages: Record<string, string> = { installer: 'installer started', partitioning: 'partitioning', packages: 'packages installed', 'late-done': 'rebooting', booted: 'booted into Debian' }

export function HostStateNotice({ host }: { host: NodeRow }) {
  const lh = host.labhost
  const [retry, setRetry] = useState(false)
  if (!lh) return null
  return (
    <>
      {lh.state === 'error' && <Notice tone="bad"><span class="flex items-center gap-3">Lab host setup failed: {lh.error}{!onMac(lh) && <button class="btn btn-sm ml-auto shrink-0" onClick={() => setRetry(true)}>Make lab host</button>}</span></Notice>}
      {retry && <MakeLabHostDialog m={host} onClose={() => setRetry(false)} />}
      {lh.state === 'installing' && <Notice tone="warn">Installing Debian{lh.install ? <> · {installStages[lh.install.stage] ?? lh.install.stage} · {fmt.when(lh.install.at)}</> : ' · waiting for the installer to boot'}</Notice>}
      {lh.state === 'installing' && !lh.install && lh.boot && (
        <div class="panel p-3 flex flex-col gap-2">
          <span class="label">Boot the installer with</span>
          <div class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12px] items-center">
            <span class="text-muted">kernel</span><Code text={lh.boot.kernel} />
            <span class="text-muted">initrd</span><Code text={lh.boot.initrd} />
            <span class="text-muted">cmdline</span><Code text={lh.boot.cmdline} />
          </div>
        </div>
      )}
      {lh.state === 'setup' && <Notice tone="warn">{onMac(lh) ? 'Checking vfkit and fetching the Talos ISO' : 'Verifying KVM and fetching Talos boot assets'}</Notice>}
      {lh.state === 'updating' && <Notice tone="warn">Host maintenance running</Notice>}
    </>
  )
}

export function HostAlerts({ mac }: { mac: string }) {
  const key = labHostKey(mac)
  useEffect(() => { loadHealth(key) }, [key])
  return <AlertGroup id={key} alerts={openAlerts(key)} />
}
