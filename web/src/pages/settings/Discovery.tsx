import type { OOBConfig } from '../../api'
import { ErrorBox, Field, Section } from '../../components/ui'
import { MovedNotice, SaveBar, useSettingsSlice } from './Layout'

const blank = (type: 'amt' | 'redfish', user: string): OOBConfig => ({ type, host: '', user, password: '', tls: false })

export function Discovery() {
  const f = useSettingsSlice('discovery', (s) => ({ discoverySubnets: s.discoverySubnets, amt: s.amt ?? blank('amt', 'admin'), bmc: s.bmc ?? blank('redfish', 'root'), pxeStatusUrl: s.pxeStatusUrl }), (s, v) => ({ ...s, ...v }))
  if (!f.draft) return <div class="text-muted">{f.error ?? 'Loading…'}</div>
  const v = f.draft
  const set = (patch: Partial<typeof v>) => f.setDraft({ ...v, ...patch })
  return (
    <Section title="Discovery" help="What a scan covers and how it authenticates to management engines.">
      <ErrorBox error={f.error} />
      <MovedNotice show={f.movedUnderneath} onDiscard={f.discard} />
      <div class="panel p-4 grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="Discovery subnets" hint="Pre-filled in the scan box; comma-separated CIDRs or addresses. Include the BMC subnet."><input class="input mono" value={v.discoverySubnets.join(', ')} onInput={(e) => set({ discoverySubnets: (e.target as HTMLInputElement).value.split(/[,\s]+/).filter(Boolean) })} /></Field>
        <Field label="PXE status URL" hint="Where the separate kubit pxe process publishes its status."><input class="input mono" value={v.pxeStatusUrl} onInput={(e) => set({ pxeStatusUrl: (e.target as HTMLInputElement).value })} /></Field>
        <Field label="Default AMT user" hint="Tried on every address answering on 16992."><input class="input mono" value={v.amt.user} onInput={(e) => set({ amt: { ...v.amt, user: (e.target as HTMLInputElement).value.trim() } })} /></Field>
        <Field label="Default AMT password" hint="The MEBx password; sealed at rest, shown masked."><input class="input mono" type="password" value={v.amt.password} onInput={(e) => set({ amt: { ...v.amt, password: (e.target as HTMLInputElement).value } })} /></Field>
        <Field label="Default BMC user" hint="Tried on every address serving a Redfish root."><input class="input mono" value={v.bmc.user} onInput={(e) => set({ bmc: { ...v.bmc, user: (e.target as HTMLInputElement).value.trim() } })} /></Field>
        <Field label="Default BMC password" hint="Sealed at rest, shown masked."><input class="input mono" type="password" value={v.bmc.password} onInput={(e) => set({ bmc: { ...v.bmc, password: (e.target as HTMLInputElement).value } })} /></Field>
      </div>
      <SaveBar dirty={f.dirty} onSave={f.save} />
    </Section>
  )
}
