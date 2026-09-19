import { useState } from 'preact/hooks'
import { api, fmt, oobLabel, type NodeRow, type OOBConfig, type OOBInfo } from '../api'
import { settings, toast, watch } from '../store'
import { ConfirmDialog, Dialog, ErrorBox, Field, Notice, Pill } from './ui'
import { usePxeGated } from './PxeGate'
import { bootTalosBlocked } from '../machine'

const empty: OOBConfig = { type: 'amt', host: '', user: 'admin', password: '', tls: false }
const defaultUser = (t: OOBConfig['type']) => (t === 'redfish' ? settings.value?.bmc?.user || 'root' : settings.value?.amt?.user || 'admin')

/** Out-of-band management for one machine: configure AMT or a BMC, test it, use it. */
export function RemoteManagement({ node }: { node: NodeRow }) {
  // #oob in the URL (from the wizard's "set credentials") opens the dialog directly.
  const [edit, setEdit] = useState(() => typeof location !== 'undefined' && location.hash === '#oob')
  const [confirm, setConfirm] = useState<'off' | 'reset' | 'pxe' | null>(null)
  const cfg = node.oob
  // Address and user are known before any credentials are: discovery saw the machine
  // on its IP and Kubit settings hold the default AMT user.
  const seed: OOBConfig = cfg ?? { ...empty, host: node.ip, user: settings.value?.amt?.user || 'admin' }
  const gated = usePxeGated(() => api.power(node.mac, 'pxe'), (r) => { setConfirm(null); watch(r) }, (m) => toast(m, 'error'))
  const run = (action: 'on' | 'off' | 'reset' | 'cycle' | 'pxe') => action === 'pxe' ? gated.attempt('Boot into Talos') : api.power(node.mac, action).then((r) => { setConfirm(null); watch(r) }).catch((e) => toast(e.message, 'error'))
  const member = !!node.cluster
  const blocked = bootTalosBlocked(node)
  return (
    <div class="panel p-3 flex flex-col gap-3">
      <div class="flex items-center gap-3">
        <div class="flex-1 min-w-0">
          <div class="font-medium">Remote management</div>
          <p class="text-[12.5px] text-muted">{cfg ? <>{oobLabel(cfg.type)} at <span class="mono">{cfg.host}</span>{cfg.type === 'amt' ? ` (${cfg.tls ? 'TLS' : 'plain'})` : ''} — power control and one-shot network boot, even when the machine is off.</> : 'Not configured. With Intel AMT (vPro) or a server BMC (Redfish) Kubit can power the machine on/off, reset it, and boot it into Talos without touching it.'}</p>
        </div>
        <button class="btn" onClick={() => setEdit(true)}>{cfg ? 'Edit' : 'Configure'}</button>
      </div>
      {cfg && (
        <div class="flex flex-wrap gap-2">
          <button class="btn" onClick={() => run('on')}>Power on</button>
          <button class="btn" onClick={() => setConfirm('off')}>Power off</button>
          <button class="btn" onClick={() => setConfirm('reset')}>Hard reset</button>
          <button class="btn btn-primary" disabled={!!blocked} title={blocked || 'Force one network boot: Kubit\'s PXE server hands this MAC Talos in maintenance mode'} onClick={() => setConfirm('pxe')}>Boot into Talos</button>
          <button class="btn" onClick={() => api.oobTest(node.mac).then((r) => toast(r.ok ? `${r.info?.version}: power ${r.info?.power}${r.info?.model ? ', ' + r.info.model : ''}` : r.error ?? 'failed', r.ok ? 'good' : 'error')).catch((e) => toast(e.message, 'error'))}>Test</button>
        </div>
      )}
      {gated.element}
      {edit && <OOBDialog mac={node.mac} initial={seed} onClose={() => { setEdit(false); if (location.hash === '#oob') history.replaceState(null, '', location.pathname + '#actions') }} />}
      {confirm === 'off' && <ConfirmDialog title="Power off" action="Power off" tone="danger" onClose={() => setConfirm(null)} onConfirm={() => run('off')} impact={<p>Hard power-off through the management engine — like holding the power button. {member ? 'Drain the node first if it runs workloads.' : ''}</p>} />}
      {confirm === 'reset' && <ConfirmDialog title="Hard reset" action="Reset" tone="danger" onClose={() => setConfirm(null)} onConfirm={() => run('reset')} impact={<p>Immediate reset without a clean shutdown; use when the machine is hung. {member ? 'Prefer Reboot on the node\'s Actions tab when Talos still answers.' : ''}</p>} />}
      {confirm === 'pxe' && <ConfirmDialog title="Boot into Talos" action="Boot into Talos" onClose={() => setConfirm(null)} onConfirm={() => run('pxe')}
        impact={<ul class="list-disc pl-5 flex flex-col gap-1"><li>{oobLabel(cfg?.type)} forces one network boot and powers on / resets the machine.</li><li>Kubit's PXE server (<span class="mono">kubit pxe</span> must be running on this LAN) answers this MAC with Talos in maintenance mode; the disk is not touched.</li><li>The machine then appears here as <b>maintenance</b> and can be adopted or used in a new cluster.</li></ul>} />}
    </div>
  )
}

function OOBDialog({ mac, initial, onClose }: { mac: string; initial: OOBConfig; onClose: () => void }) {
  const [c, setC] = useState<OOBConfig>(initial)
  const [error, setError] = useState<string | null>(null)
  const [info, setInfo] = useState<OOBInfo | null>(null)
  const test = () => api.oobTest(mac, c).then((r) => { setInfo(r.info ?? null); setError(r.ok ? null : r.error ?? 'failed'); if (r.ok) toast(`${oobLabel(c.type)} answered`, 'good') }).catch((e) => setError(e.message))
  const save = () => api.saveOOB(mac, c).then(onClose).catch((e) => setError(e.message))
  const remove = () => api.saveOOB(mac, { ...c, type: '' }).then(onClose).catch((e) => setError(e.message))
  return (
    <Dialog title="Remote management" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button>{initial.host && <button class="btn btn-danger" onClick={remove}>Remove</button>}<button class="btn" onClick={test}>Test</button><button class="btn btn-primary" disabled={!c.host || !c.user} onClick={save}>Save</button></>}>
      <ErrorBox error={error} />
      <OOBFields c={c} onChange={setC} />
      {info && <InfoLine info={info} />}
    </Dialog>
  )
}

function OOBFields({ c, onChange }: { c: OOBConfig; onChange: (c: OOBConfig) => void }) {
  const setType = (type: OOBConfig['type']) => onChange({ ...c, type, user: c.user === defaultUser(c.type) || !c.user ? defaultUser(type) : c.user })
  return (
    <>
      <Field label="Type">
        <select class="input" value={c.type} onChange={(e) => setType((e.target as HTMLSelectElement).value as OOBConfig['type'])}>
          <option value="amt">Intel AMT (vPro desktop, NUC)</option>
          <option value="redfish">BMC via Redfish (iDRAC, iLO, XCC, Supermicro, OpenBMC)</option>
        </select>
      </Field>
      {c.type === 'amt'
        ? <Notice tone="muted">Enable AMT in the BIOS, set the MEBx password, allow network access. Ports 16992 (plain) or 16993 (TLS).</Notice>
        : <Notice tone="muted">A local BMC account with power and boot rights. HTTPS on the BMC's own address; self-signed certificates are accepted.</Notice>}
      <div class="grid grid-cols-2 gap-3">
        <Field label="Address" hint={c.type === 'amt' ? "IP or DNS name of the machine's wired interface." : 'IP or DNS name of the BMC, with :port if not 443.'}><input class="input mono" value={c.host} onInput={(e) => onChange({ ...c, host: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="User"><input class="input mono" value={c.user} onInput={(e) => onChange({ ...c, user: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="Password" hint="Stored sealed with the master key."><input class="input mono" type="password" value={c.password} onInput={(e) => onChange({ ...c, password: (e.target as HTMLInputElement).value })} /></Field>
        {c.type === 'amt' && <Field label="Transport"><select class="input" value={c.tls ? 'tls' : 'plain'} onChange={(e) => onChange({ ...c, tls: (e.target as HTMLSelectElement).value === 'tls' })}><option value="plain">Plain (16992)</option><option value="tls">TLS (16993, self-signed accepted)</option></select></Field>}
      </div>
    </>
  )
}

function InfoLine({ info }: { info: OOBInfo }) {
  return (
    <div class="text-[12.5px] flex flex-wrap gap-2 items-center">
      <Pill tone="good">{info.version}</Pill>
      <span class="mono">{info.mac}</span>
      <span class="text-muted">{[info.manufacturer, info.model].filter(Boolean).join(' ')}{info.serial ? ` · ${info.serial}` : ''}</span>
      {info.cpus ? <span class="text-muted">{info.cpus} CPU · {fmt.bytes(info.memoryBytes ?? 0)}{info.disks?.length ? ` · ${info.disks.length} disk${info.disks.length === 1 ? '' : 's'}` : ''}</span> : null}
      <Pill tone={info.power === 'on' ? 'good' : 'muted'}>power {info.power}</Pill>
    </div>
  )
}

/** Inventory: create a machine from its management engine alone, before Talos ever booted. */
export function AddAMTDialog({ onClose }: { onClose: () => void }) {
  const [c, setC] = useState<OOBConfig>({ ...empty, user: defaultUser('amt') })
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const add = () => { setBusy(true); api.addOOBMachine(c).then((r) => { toast(`Added ${r.machine.mac}${r.info.model ? ' (' + r.info.model + ')' : ''}`, 'good'); onClose() }).catch((e) => setError(e.message)).finally(() => setBusy(false)) }
  return (
    <Dialog title="Add machine by remote management" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={busy || !c.host || !c.password} onClick={add}>{busy ? 'Probing' : 'Add'}</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">Kubit reads MAC, model and serial from the management engine and adds the machine; power and network boot are then remote.</p>
      <OOBFields c={c} onChange={setC} />
    </Dialog>
  )
}
