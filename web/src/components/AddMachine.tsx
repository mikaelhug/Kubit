import { useEffect, useRef, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, fmt, type DesignNode, type DesignRequest, type DesignView, type NodeRow } from '../api'
import { modelOf, roleLabel, specsOf } from '../machine'
import { addressOf } from '../net'
import { clusters, toast, upsertCluster } from '../store'
import { Dialog, ErrorBox, Field, Notice, Pill } from './ui'
import { DnsFields, dnsList, dnsPair, type DnsPair } from './DnsFields'
import { DirPicker } from './DirPicker'
import { AppsRepoFields, AppsReviewBlock, appsReady, appsRequest, noApps, useAppsRepo, type AppsChoice } from './AppsRepo'

const newCluster = ''
const auto = ''
const maxName = 50
const validName = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/
const nameFrom = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, maxName).replace(/-+$/, '')
const nameOk = (s: string) => validName.test(s) && s.length <= maxName

export function AddMachines({ machines, label = 'Add', primary = true, onDone }: { machines: NodeRow[]; label?: string; primary?: boolean; onDone?: () => void }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button class={`btn btn-sm ${primary ? 'btn-primary' : ''}`} disabled={machines.length === 0} onClick={() => setOpen(true)}>{label}</button>
      {open && <AddDialog machines={machines} onClose={(done) => { setOpen(false); if (done) onDone?.() }} />}
    </>
  )
}

export function Declared({ m }: { m: NodeRow }) {
  const d = m.declared!
  return (
    <span class="inline-flex items-center gap-2">
      <Pill tone="info" title={`In ${d.cluster}'s cluster.yaml`}>{d.hostname} · not applied</Pill>
      <a class="btn btn-sm btn-primary" href={`/clusters/${d.cluster}/changes`}>Review changes</a>
    </span>
  )
}

function AddDialog({ machines, onClose }: { machines: NodeRow[]; onClose: (done: boolean) => void }) {
  const { route } = useLocation()
  const list = clusters.value
  const [target, setTarget] = useState(list[0]?.name ?? newCluster)
  const [dir, setDir] = useState('')
  const [name, setName] = useState('')
  const [roles, setRoles] = useState<Record<string, string>>({})
  const [vip, setVip] = useState('')
  const [addr, setAddr] = useState<Record<string, string>>({})
  const [gateway, setGateway] = useState('')
  const [dns, setDns] = useState<DnsPair>(['', ''])
  const [rechecks, setRechecks] = useState(0)
  const [preview, setPreview] = useState<DesignView | null>(null)
  const [checking, setChecking] = useState(false)
  const seq = useRef(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [apps, setApps] = useState<AppsChoice>(noApps)
  const { repo: appsRepo, error: appsError } = useAppsRepo(apps.dir)
  const creating = target === newCluster
  const dirInput = useRef<HTMLInputElement>(null)
  useEffect(() => { if (creating && !preview) dirInput.current?.focus() }, [creating, !preview])
  const typedName = name.trim()
  const defaultName = nameFrom(dir.trim().replace(/\/+$/, '').split('/').pop() ?? '')
  const effectiveName = typedName || defaultName
  const badName = creating && !!effectiveName && !nameOk(effectiveName)
  const roleOf = (m: NodeRow) => roles[m.mac] ?? (creating ? auto : 'worker')
  const request = (withVip: string): DesignRequest => ({
    dir: creating ? dir.trim() : undefined,
    name: creating ? name.trim() || undefined : undefined,
    vip: creating ? withVip.trim() || undefined : undefined,
    apps: creating ? appsRequest(apps) : undefined,
    machines: machines.map((m) => {
      const a = addr[m.mac]?.trim()
      return { mac: m.mac, role: roleOf(m) || undefined, address: a || undefined, gateway: a ? gateway.trim() : undefined, nameservers: a ? dnsList(dns) : undefined }
    }),
  })
  const run = async (f: () => Promise<void>) => {
    setBusy(true)
    setError(null)
    try { await f() } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const show = (id: number, d: DesignView) => {
    if (id !== seq.current) return
    setPreview(d)
    setVip(d.vip ?? '')
  }
  const review = (withVip = vip) => {
    const id = ++seq.current
    const cluster = creating ? '' : target
    const body = request(withVip)
    let checked = false
    setChecking(true)
    api.designChecks(cluster, body)
      .then((d) => { checked = true; show(id, d) })
      .catch(() => {})
      .finally(() => { if (id === seq.current) setChecking(false) })
    const load = () => api.design(cluster, body).then((d) => { if (!checked) show(id, d) })
    if (!preview) return run(load)
    setError(null)
    load().catch((e) => { if (id === seq.current) setError(e.message) })
  }
  useEffect(() => { if (rechecks > 0) review() }, [rechecks])
  const recheck = () => { setChecking(true); setRechecks((r) => r + 1) }
  const setStatic = (n: DesignNode, on: boolean) => {
    const next = { ...addr }
    if (on) next[n.mac] = addressOf(n.network?.addresses[0] ?? n.live.address ?? n.ip)
    else delete next[n.mac]
    setAddr(next)
    if (on && !gateway) setGateway(n.live.gateway ?? '')
    if (on && !dnsList(dns).length) setDns(dnsPair(n.live.nameservers))
    recheck()
  }
  const anyStatic = Object.keys(addr).length > 0
  const taken = preview?.nodes.some((n) => n.inUse) ?? false
  const write = () => run(async () => {
    const d = creating ? await api.createRepo(request(vip)) : await api.addNodes(target, { ...request(''), hash: preview?.hash })
    const row = (await api.clusters()).find((c) => c.name === d.cluster)
    if (row) upsertCluster(row)
    toast(creating ? `${d.cluster} written to ${d.dir}` : `${d.nodes.length} node${d.nodes.length === 1 ? '' : 's'} added to ${d.cluster}'s cluster.yaml`, 'good')
    onClose(true)
    route(`/clusters/${d.cluster}/changes`)
  })
  const title = machines.length === 1 ? `Add ${machines[0].ip}` : `Add ${machines.length} machines`
  if (preview) {
    return (
      <Dialog title={title} width="max-w-2xl" onClose={() => onClose(false)} footer={
        <>
          <button class="btn" onClick={() => { seq.current++; setChecking(false); setPreview(null) }}>Back</button>
          <button class="btn btn-primary" disabled={busy || checking || !!preview.vipInUse || taken} onClick={write}>{busy ? 'Writing' : checking ? 'Checking addresses' : preview.apps ? 'Write files' : 'Write cluster.yaml'}</button>
        </>
      }>
        <ErrorBox error={error} />
        <div class="text-[13px] flex flex-col gap-0.5">
          <span><span class="text-muted">{preview.new ? 'New repo ' : 'Repo '}</span><span class="mono break-all">{preview.dir}</span></span>
          <span><span class="text-muted">Talos </span><span class="mono">{preview.talosVersion}</span><span class="text-muted"> · Kubernetes </span><span class="mono">{preview.kubernetesVersion}</span></span>
        </div>
        <div class="panel scroll-x">
          <table class="data">
            <thead><tr><th>Hostname</th><th>Address</th><th>Role</th><th>Install disk</th></tr></thead>
            <tbody>
              {preview.nodes.map((n) => (
                <tr key={n.mac}>
                  <td class="mono">{n.hostname}</td>
                  <td>
                    <span class="flex items-center gap-2">
                      <select class="input !w-24" value={addr[n.mac] !== undefined ? 'static' : 'dhcp'} onChange={(e) => setStatic(n, (e.target as HTMLSelectElement).value === 'static')} aria-label={`Address of ${n.hostname}`}>
                        <option value="dhcp">DHCP</option>
                        <option value="static">Static</option>
                      </select>
                      {addr[n.mac] !== undefined
                        ? <input class="input mono !w-40" value={addr[n.mac]} onInput={(e) => setAddr({ ...addr, [n.mac]: (e.target as HTMLInputElement).value })} onBlur={() => recheck()} onKeyDown={(e) => e.key === 'Enter' && recheck()} />
                        : <span class="mono">{n.ip}</span>}
                    </span>
                    {n.inUse && <span class="block text-[11px] text-bad">in use</span>}
                  </td>
                  <td>{roleLabel(n.role)}</td>
                  <td class="mono">{n.disk}{n.diskBytes ? <span class="text-muted"> · {fmt.bytes(n.diskBytes)}</span> : null}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {anyStatic && (
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <Field label="Gateway">
              <input class="input mono" value={gateway} onInput={(e) => setGateway((e.target as HTMLInputElement).value)} onBlur={() => recheck()} />
            </Field>
            <DnsFields value={dns} onChange={setDns} onBlur={() => recheck()} />
          </div>
        )}
        {preview.new && preview.vip !== undefined && (
          <Field label="Cluster VIP">
            <input class="input mono" value={vip} onInput={(e) => setVip((e.target as HTMLInputElement).value)} onBlur={() => vip !== (preview.vip ?? '') && review(vip)} onKeyDown={(e) => e.key === 'Enter' && review(vip)} />
          </Field>
        )}
        {preview.vipInUse ? <Notice tone="bad">{preview.vip} is in use.</Notice> : <div class="text-[13px]"><span class="text-muted">API </span><span class="mono">{preview.endpoint}</span></div>}
        {preview.apps && (
          <div class="flex flex-col gap-2 border-t border-border pt-4">
            <span class="text-[13px] font-medium">Apps repository</span>
            <AppsReviewBlock review={preview.apps} />
          </div>
        )}
        {preview.warnings.length > 0 && <Notice tone="warn"><ul class="flex flex-col gap-1">{preview.warnings.map((w) => <li key={w}>{w}</li>)}</ul></Notice>}
        <Notice tone="warn">Apply erases each install disk.</Notice>
      </Dialog>
    )
  }
  return (
    <Dialog title={title} width="max-w-2xl" onClose={() => onClose(false)} footer={
      <>
        <button class="btn" onClick={() => onClose(false)}>Cancel</button>
        <button class="btn btn-primary" disabled={busy || (creating && (!dir.trim() || !effectiveName || badName || !!appsError || !appsReady(apps, appsRepo)))} onClick={() => review()}>{busy ? 'Designing' : 'Review'}</button>
      </>
    }>
      <ErrorBox error={error} />
      <Field label="Cluster">
        <select class="input" value={target} onChange={(e) => { setTarget((e.target as HTMLSelectElement).value); setRoles({}) }}>
          {list.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
          <option value={newCluster}>New cluster</option>
        </select>
      </Field>
      {creating && (
        <>
          <Field label="Repo directory">
            <div class="flex items-center gap-2">
              <input ref={dirInput} class="input mono" value={dir} placeholder="~/git/home" onInput={(e) => setDir((e.target as HTMLInputElement).value)} />
              <DirPicker value={dir} onPick={setDir} />
            </div>
          </Field>
          <Field label="Name" hint={badName ? <span class="text-bad">Lowercase letters, digits and hyphens{nameFrom(effectiveName) ? `, e.g. ${nameFrom(effectiveName)}` : ''}.</span> : undefined}>
            <input class="input mono" value={name} placeholder={defaultName} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
          </Field>
        </>
      )}
      <Field label="Roles">
        <div class="panel divide-y divide-border/60">
          {machines.map((m) => (
            <div key={m.mac} class="flex items-center gap-3 px-3 py-1.5 text-[13px]">
              <span class="mono w-32 shrink-0">{m.ip}</span>
              <span class="flex flex-col min-w-0 flex-1">
                <span class="truncate">{modelOf(m)}</span>
                <span class="text-[11px] text-muted">{specsOf(m.inventory)}</span>
              </span>
              <select class="input !w-36" value={roleOf(m)} onChange={(e) => setRoles({ ...roles, [m.mac]: (e.target as HTMLSelectElement).value })}>
                {creating && <option value={auto}>auto</option>}
                <option value="controlplane">control plane</option>
                <option value="worker">worker</option>
              </select>
            </div>
          ))}
        </div>
      </Field>
      {creating && (
        <div class="flex flex-col gap-4 border-t border-border pt-4">
          <span class="text-[13px] font-medium">Apps repository</span>
          <AppsRepoFields choice={apps} onChange={setApps} repo={appsRepo} error={appsError} optional />
        </div>
      )}
    </Dialog>
  )
}
