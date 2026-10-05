import { useState } from 'preact/hooks'
import { api, fmt } from '../../api'
import { Elapsed } from '../../components/Time'
import { ConfirmDialog, ErrorBox, Field, KeyValue, MovedNotice, Section } from '../../components/ui'
import { runningCount } from '../../ops'
import { can, connected, daemon, stopped, toast } from '../../store'
import { SaveBar, useSettingsSlice } from './Layout'

export function General() {
  const f = useSettingsSlice('general', (s) => ({ factoryUrl: s.factoryUrl, watchIntervalSec: s.watchIntervalSec, defaultMetalLBRange: s.defaultMetalLBRange }), (s, v) => ({ ...s, ...v }))
  const d = daemon.value
  const [stopping, setStopping] = useState(false)
  if (!f.draft) return <div class="text-muted">{f.error ?? 'Loading'}</div>
  const v = f.draft
  return (
    <>
      <Section title="General" help="Where images come from and how often clusters are observed.">
        <ErrorBox error={f.error} />
        <MovedNotice show={f.movedUnderneath} onDiscard={f.discard} />
        <div class="panel p-3 grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label="Image Factory URL" hint="Installer images, ISOs and PXE assets"><input class="input mono" value={v.factoryUrl} onInput={(e) => f.setDraft({ ...v, factoryUrl: (e.target as HTMLInputElement).value })} /></Field>
          <Field label="Health poll interval (seconds)" hint="How often every cluster is queried; minimum 5"><input class="input" type="number" min={5} value={v.watchIntervalSec} onInput={(e) => f.setDraft({ ...v, watchIntervalSec: Number((e.target as HTMLInputElement).value) })} /></Field>
          <Field label="Default MetalLB range" hint="Proposed for new clusters; empty derives one from the nodes' subnet"><input class="input mono" value={v.defaultMetalLBRange} placeholder="192.168.1.200-192.168.1.220" onInput={(e) => f.setDraft({ ...v, defaultMetalLBRange: (e.target as HTMLInputElement).value })} /></Field>
        </div>
        <SaveBar dirty={f.dirty} onSave={f.save} />
      </Section>
      <Section title="Daemon" actions={can('admin') && <button class="btn btn-danger" disabled={!connected.value || runningCount.value > 0} title={runningCount.value > 0 ? 'Operations are running' : ''} onClick={() => setStopping(true)}>Stop Kubit</button>}>
        <div class="panel p-3">
          <KeyValue rows={[
            ['Version', <span class="mono">{d?.version ?? '—'}</span>],
            ['Up since', d ? <>{fmt.datetime(d.startedAt)} (<Elapsed from={d.startedAt} />)</> : '—'],
          ]} />
        </div>
        {stopping && (
          <ConfirmDialog title="Stop Kubit" action="Stop Kubit" tone="danger" onClose={() => setStopping(false)}
            onConfirm={() => api.stopDaemon().then(() => { stopped.value = true; setStopping(false) }).catch((e) => toast(e.message, 'error'))}
            impact={<p>Alerts and scheduled snapshots pause until <span class="mono">kubit</span> runs again.</p>} />
        )}
      </Section>
    </>
  )
}
