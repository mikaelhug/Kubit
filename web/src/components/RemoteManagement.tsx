import { useState } from 'preact/hooks'
import { api, type NodeRow, type OOBConfig, type OOBInfo } from '../api'
import { settings, toast, watch } from '../store'
import { ConfirmDialog, Dialog, ErrorBox, Field, Notice, Pill } from './ui'
import { usePxeGated } from './PxeGate'

const empty: OOBConfig = { type: 'amt', host: '', user: 'admin', password: '', tls: false }

/** Out-of-band management for one machine: configure AMT, test it, use it. */
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
  return (
    <div class="panel p-4 flex flex-col gap-3">
      <div class="flex items-center gap-3">
        <div class="flex-1 min-w-0">
          <div class="font-medium">Remote management</div>
          <p class="text-[12.5px] text-muted">{cfg ? <>Intel AMT at <span class="mono">{cfg.host}</span> ({cfg.tls ? 'TLS' : 'plain'}) — power control and one-shot network boot, even when the machine is off.</> : 'Not configured. With Intel AMT (vPro) Kubit can power the machine on/off, reset it, and boot it into Talos without touching it.'}</p>
        </div>
        <button class="btn" onClick={() => setEdit(true)}>{cfg ? 'Edit…' : 'Configure…'}</button>
      </div>
      {cfg && (
        <div class="flex flex-wrap gap-2">
          <button class="btn" onClick={() => run('on')}>Power on</button>
          <button class="btn" onClick={() => setConfirm('off')}>Power off</button>
          <button class="btn" onClick={() => setConfirm('reset')}>Hard reset</button>
          <button class="btn btn-primary" disabled={member} title={member ? 'Members are removed from the cluster first; that resets them to maintenance mode from disk' : 'Force one network boot: Kubit\'s PXE server hands this MAC Talos in maintenance mode'} onClick={() => setConfirm('pxe')}>Boot into Talos</button>
          <button class="btn" onClick={() => api.oobTest(node.mac).then((r) => toast(r.ok ? `AMT ${r.info?.version}: power ${r.info?.power}${r.info?.model ? ', ' + r.info.model : ''}` : r.error ?? 'failed', r.ok ? 'good' : 'error')).catch((e) => toast(e.message, 'error'))}>Test</button>
        </div>
      )}
      {gated.element}
      {edit && <OOBDialog mac={node.mac} initial={seed} onClose={() => { setEdit(false); if (location.hash === '#oob') history.replaceState(null, '', location.pathname + '#actions') }} />}
      {confirm === 'off' && <ConfirmDialog title="Power off" action="Power off" tone="danger" onClose={() => setConfirm(null)} onConfirm={() => run('off')} impact={<p>Hard power-off through the management engine — like holding the power button. {member ? 'Drain the node first if it runs workloads.' : ''}</p>} />}
      {confirm === 'reset' && <ConfirmDialog title="Hard reset" action="Reset" tone="danger" onClose={() => setConfirm(null)} onConfirm={() => run('reset')} impact={<p>Immediate reset without a clean shutdown; use when the machine is hung. {member ? 'Prefer Reboot on the node\'s Actions tab when Talos still answers.' : ''}</p>} />}
      {confirm === 'pxe' && <ConfirmDialog title="Boot into Talos" action="Boot into Talos" onClose={() => setConfirm(null)} onConfirm={() => run('pxe')}
        impact={<ul class="list-disc pl-5 flex flex-col gap-1"><li>AMT forces one network boot and powers on / resets the machine.</li><li>Kubit's PXE server (<span class="mono">kubit pxe</span> must be running on this LAN) answers this MAC with Talos in maintenance mode; the disk is not touched.</li><li>The machine then appears here as <b>maintenance</b> and can be adopted or used in a new cluster.</li></ul>} />}
    </div>
  )
}

function OOBDialog({ mac, initial, onClose }: { mac: string; initial: OOBConfig; onClose: () => void }) {
  const [c, setC] = useState<OOBConfig>(initial)
  const [error, setError] = useState<string | null>(null)
  const [info, setInfo] = useState<OOBInfo | null>(null)
  const test = () => api.oobTest(mac, c).then((r) => { setInfo(r.info ?? null); setError(r.ok ? null : r.error ?? 'failed'); if (r.ok) toast('AMT answered', 'good') }).catch((e) => setError(e.message))
  const save = () => api.saveOOB(mac, c).then(onClose).catch((e) => setError(e.message))
  const remove = () => api.saveOOB(mac, { ...c, type: '' }).then(onClose).catch((e) => setError(e.message))
  return (
    <Dialog title="Remote management" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button>{initial.host && <button class="btn btn-danger" onClick={remove}>Remove</button>}<button class="btn" onClick={test}>Test</button><button class="btn btn-primary" disabled={!c.host || !c.user} onClick={save}>Save</button></>}>
      <ErrorBox error={error} />
      <Notice tone="muted">Enable AMT in the BIOS, set the MEBx password (Ctrl+P at boot) and allow network access; AMT shares the wired NIC's address and listens on 16992 (plain) or 16993 (TLS).</Notice>
      <div class="grid grid-cols-2 gap-3">
        <Field label="Address" hint="IP or DNS name of the machine's wired interface."><input class="input mono" value={c.host} onInput={(e) => setC({ ...c, host: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="User"><input class="input mono" value={c.user} onInput={(e) => setC({ ...c, user: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="Password" hint="Stored sealed with the master key."><input class="input mono" type="password" value={c.password} onInput={(e) => setC({ ...c, password: (e.target as HTMLInputElement).value })} /></Field>
        <Field label="Transport"><select class="input" value={c.tls ? 'tls' : 'plain'} onChange={(e) => setC({ ...c, tls: (e.target as HTMLSelectElement).value === 'tls' })}><option value="plain">Plain (16992)</option><option value="tls">TLS (16993, self-signed accepted)</option></select></Field>
      </div>
      {info && <div class="text-[12.5px] flex flex-wrap gap-2 items-center"><Pill tone="good">AMT {info.version}</Pill><span class="mono">{info.mac}</span><span class="text-muted">{[info.manufacturer, info.model].filter(Boolean).join(' ')}{info.serial ? ` · ${info.serial}` : ''}</span><Pill tone={info.power === 'on' ? 'good' : 'muted'}>power {info.power}</Pill></div>}
    </Dialog>
  )
}

/** Inventory: create a machine from its AMT alone, before Talos ever booted. */
export function AddAMTDialog({ onClose }: { onClose: () => void }) {
  const [c, setC] = useState<OOBConfig>(empty)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const add = () => { setBusy(true); api.addOOBMachine(c).then((r) => { toast(`Added ${r.machine.mac}${r.info.model ? ' (' + r.info.model + ')' : ''}`, 'good'); onClose() }).catch((e) => setError(e.message)).finally(() => setBusy(false)) }
  return (
    <Dialog title="Add machine via Intel AMT" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={busy || !c.host || !c.password} onClick={add}>{busy ? 'Probing…' : 'Add'}</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">Kubit reads the MAC, model and serial from the management engine and creates the machine, so it can be powered on and booted into Talos from here — no USB stick, no keyboard.</p>
      <div class="grid grid-cols-2 gap-3">
        <Field label="AMT address"><input class="input mono" value={c.host} placeholder="192.168.1.50" onInput={(e) => setC({ ...c, host: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="User"><input class="input mono" value={c.user} onInput={(e) => setC({ ...c, user: (e.target as HTMLInputElement).value.trim() })} /></Field>
        <Field label="Password"><input class="input mono" type="password" value={c.password} onInput={(e) => setC({ ...c, password: (e.target as HTMLInputElement).value })} /></Field>
        <Field label="Transport"><select class="input" value={c.tls ? 'tls' : 'plain'} onChange={(e) => setC({ ...c, tls: (e.target as HTMLSelectElement).value === 'tls' })}><option value="plain">Plain (16992)</option><option value="tls">TLS (16993)</option></select></Field>
      </div>
    </Dialog>
  )
}
