import { api, fmt, type OffsiteTarget } from '../../api'
import { ErrorBox, Field, MovedNotice, Section } from '../../components/ui'
import { runOp } from '../../ops'
import { toast } from '../../store'
import { useLive } from '../../useLive'
import { SaveBar, useSettingsSlice } from './Layout'

export function Offsite() {
  const f = useSettingsSlice('offsite', (s) => s.offsite, (s, v) => ({ ...s, offsite: v }))
  const { data: off } = useLive(() => api.offsiteStatus(), [], [['', 'offsite']], { onError: 'silent' })
  if (!f.draft) return <div class="text-muted">{f.error ?? 'Loading'}</div>
  const o = f.draft
  const set = (patch: Partial<OffsiteTarget>) => f.setDraft({ ...o, ...patch })
  const text = (key: keyof OffsiteTarget, trim = true) => (e: Event) => set({ [key]: trim ? (e.target as HTMLInputElement).value.trim() : (e.target as HTMLInputElement).value } as Partial<OffsiteTarget>)
  return (
    <Section title="Off-site copies" help="etcd snapshots and a daily sealed Kubit backup, copied elsewhere.">
      <ErrorBox error={f.error} />
      <MovedNotice show={f.movedUnderneath} onDiscard={f.discard} />
      <div class="panel p-3 grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="Target">
          <select class="input" value={o.type} onChange={(e) => set({ type: (e.target as HTMLSelectElement).value as OffsiteTarget['type'] })}>
            <option value="">Off</option><option value="dir">Directory</option><option value="s3">S3-compatible bucket</option>
          </select>
        </Field>
        <Field label="Keep daily Kubit backups" hint="Older ones are deleted remotely"><input class="input" type="number" min={1} value={o.keepBackups} onInput={(e) => set({ keepBackups: Number((e.target as HTMLInputElement).value) })} /></Field>
        {o.type === 'dir' && <Field label="Directory" hint="Reachable from the daemon's host"><input class="input mono" value={o.dir} placeholder="/Volumes/backup/kubit" onInput={text('dir')} /></Field>}
        {o.type === 's3' && (<>
          <Field label="Endpoint" hint="host[:port]; https unless plain http is set"><input class="input mono" value={o.endpoint} placeholder="s3.eu-central-1.amazonaws.com" onInput={text('endpoint')} /></Field>
          <Field label="Bucket"><input class="input mono" value={o.bucket} onInput={text('bucket')} /></Field>
          <Field label="Region" hint="Empty for auto"><input class="input mono" value={o.region} onInput={text('region')} /></Field>
          <Field label="Access key"><input class="input mono" value={o.accessKey} onInput={text('accessKey')} /></Field>
          <Field label="Secret key" hint="Sealed at rest"><input class="input mono" type="password" value={o.secretKey} onInput={text('secretKey', false)} /></Field>
          <div class="flex flex-col gap-2 text-[13px]">
            <label class="flex items-center gap-2"><input type="checkbox" checked={o.insecure} onChange={(e) => set({ insecure: (e.target as HTMLInputElement).checked })} /> Plain http</label>
            <label class="flex items-center gap-2"><input type="checkbox" checked={o.pathStyle} onChange={(e) => set({ pathStyle: (e.target as HTMLInputElement).checked })} /> Path-style bucket addressing</label>
          </div>
        </>)}
        {o.type && <Field label="Prefix" hint="Optional folder inside the target"><input class="input mono" value={o.prefix} placeholder="kubit" onInput={text('prefix')} /></Field>}
      </div>
      <SaveBar dirty={f.dirty} onSave={f.save}>
        <button class="btn" disabled={!o.type} title="Write, read back and delete a probe object" onClick={() => api.offsiteTest(o).then((r) => toast(r.ok ? `Target reachable (${r.roundTripMs} ms round trip)` : r.error ?? 'failed', r.ok ? 'good' : 'error')).catch((e) => toast(e.message, 'error'))}>Test target</button>
        <button class="btn" disabled={f.dirty || !o.type} title={f.dirty ? 'Save first' : 'Upload a Kubit backup now'} onClick={() => runOp(api.offsiteBackup())}>Copy Kubit backup now</button>
        {off?.enabled && <span class="text-[12px] text-muted">{off.error ? <span class="text-bad">{off.error}</span> : `${off.target}: ${off.backups} backup(s), ${off.snapshots} snapshot(s), ${fmt.bytes(off.bytes)} · last backup ${off.lastBackup ? fmt.when(off.lastBackup) : 'never'}`}</span>}
      </SaveBar>
    </Section>
  )
}
