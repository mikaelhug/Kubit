import { daemon } from '../../store'
import { ErrorBox, Field, KeyValue, Section } from '../../components/ui'
import { MovedNotice, SaveBar, useSettingsSlice } from './Layout'
import { elapsed } from '../../clock'

export function General() {
  const f = useSettingsSlice('general', (s) => ({ factoryUrl: s.factoryUrl, watchIntervalSec: s.watchIntervalSec, defaultMetalLBRange: s.defaultMetalLBRange }), (s, v) => ({ ...s, ...v }))
  const d = daemon.value
  if (!f.draft) return <div class="text-muted">{f.error ?? 'Loading…'}</div>
  const v = f.draft
  return (
    <>
      <Section title="General" help="Where images come from and how often clusters are observed.">
        <ErrorBox error={f.error} />
        <MovedNotice show={f.movedUnderneath} onDiscard={f.discard} />
        <div class="panel p-3 grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field label="Image Factory URL" hint="Installer images, ISOs and PXE assets. A self-hosted factory for air-gapped sites."><input class="input mono" value={v.factoryUrl} onInput={(e) => f.setDraft({ ...v, factoryUrl: (e.target as HTMLInputElement).value })} /></Field>
          <Field label="Health poll interval (seconds)" hint="How often every cluster is queried for samples and events. Minimum 5."><input class="input num" type="number" min={5} value={v.watchIntervalSec} onInput={(e) => f.setDraft({ ...v, watchIntervalSec: Number((e.target as HTMLInputElement).value) })} /></Field>
          <Field label="Default MetalLB range" hint="Proposed for new clusters; empty derives one from the nodes' subnet."><input class="input mono" value={v.defaultMetalLBRange} placeholder="192.168.1.200-192.168.1.220" onInput={(e) => f.setDraft({ ...v, defaultMetalLBRange: (e.target as HTMLInputElement).value })} /></Field>
        </div>
        <SaveBar dirty={f.dirty} onSave={f.save} />
      </Section>
      <Section title="Daemon">
        <div class="panel p-3">
          <KeyValue rows={[
            ['Version', <span class="mono">{d?.version ?? '—'}</span>],
            ['Runs as', d ? (d.service ? 'service' : 'foreground process') : '—'],
            ['Up since', d ? `${new Date(d.startedAt).toLocaleString()} (${elapsed(d.startedAt)})` : '—'],
          ]} />
        </div>
      </Section>
    </>
  )
}
