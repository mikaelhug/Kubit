import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, fmt, type ClusterSpec, type NodeRow, type NodeSpec, type Pool, type Warning } from '../../api'
import { operations, toast, watch } from '../../store'
import { Field, Notice, Pill } from '../../components/ui'
import { Tabs } from '../../components/Tabs'
import { PoolsEditor } from '../../components/PoolsEditor'
import type { Draft } from './NewCluster'
import { addrOf, guessGateway, inRange, ip4, parseRange, prefixOf, sameSubnet } from './net'

type SetCluster = (fn: (c: ClusterSpec) => ClusterSpec) => void
const GiB = 1024 ** 3

export function machineOf(draft: Draft, n: NodeSpec) { return draft.machines.find((m) => m.mac === n.mac) }
export function installCandidates(m?: NodeRow) { return (m?.inventory?.disks ?? []).filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb').sort((a, b) => b.sizeBytes - a.sizeBytes) }

export function machineWarnings(m: NodeRow, all: NodeRow[]): string[] {
  const out: string[] = []
  const disks = installCandidates(m)
  if (disks.length === 0) out.push('no install disk')
  else if (disks[0].sizeBytes < 20 * GiB) out.push(`largest disk ${fmt.bytes(disks[0].sizeBytes)} (< 20 GiB)`)
  if ((m.inventory?.memoryBytes ?? 0) > 0 && (m.inventory?.memoryBytes ?? 0) < 2 * GiB) out.push(`${fmt.bytes(m.inventory!.memoryBytes)} RAM`)
  const arches = new Map<string, number>()
  for (const x of all) arches.set(x.arch, (arches.get(x.arch) ?? 0) + 1)
  if (arches.size > 1) { const majority = [...arches.entries()].sort((a, b) => b[1] - a[1])[0][0]; if (m.arch !== majority) out.push(`${m.arch} among ${majority} machines`) }
  return out
}

// ─── 1 · Machines ────────────────────────────────────────────────────────────

export function MachinesStep({ draft, patch, setError }: { draft: Draft; patch: (p: Partial<Draft>) => void; setError: (e: string | null) => void }) {
  const [targets, setTargets] = useState('')
  const load = () => api.nodes().then((ns) => {
    const free = ns.filter((n) => n.state === 'maintenance' && !n.cluster)
    patch({ machines: free, selected: draft.selected.filter((mac) => free.some((m) => m.mac === mac)) })
    if (!targets && free[0]) setTargets(free[0].ip.replace(/\.\d+$/, '.0/24'))
  }).catch((e) => setError(e.message))
  useEffect(() => { load(); api.settings().then((s) => { if (s.discoverySubnets.length) setTargets(s.discoverySubnets.join(', ')) }).catch(() => {}) }, []) // eslint-disable-line
  const finished = [...operations.value.values()].filter((o) => o.kind === 'discover' && o.status !== 'running').length
  useEffect(() => { load() }, [finished]) // eslint-disable-line
  const scanning = [...operations.value.values()].some((o) => o.kind === 'discover' && o.status === 'running')
  const toggle = (mac: string) => patch({ selected: draft.selected.includes(mac) ? draft.selected.filter((x) => x !== mac) : [...draft.selected, mac] })
  const chosen = draft.machines.filter((m) => draft.selected.includes(m.mac))
  return (
    <>
      <div class="panel p-4 flex flex-col gap-3">
        <p class="text-[13px] text-muted">Machines booted from a Talos ISO or over the network wait in maintenance mode on port 50000. Kubit identifies each by its uplink MAC, so a machine keeps its identity when DHCP hands it a new address. <a class="text-accent hover:underline" href="https://factory.talos.dev" target="_blank" rel="noreferrer">Download an ISO</a> · <a class="text-accent hover:underline" href="/fleet/pxe">Network boot</a></p>
        <div class="flex gap-2">
          <input class="input mono" value={targets} onInput={(e) => setTargets((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24, 10.0.0.5" aria-label="Subnets or addresses to scan" />
          <button class="btn shrink-0" disabled={scanning || !targets.trim()} onClick={() => api.discover(targets.split(/[,\s]+/).filter(Boolean)).then((r) => watch(r, false)).catch((e) => setError(e.message))}>{scanning ? 'Scanning…' : 'Scan'}</button>
        </div>
      </div>
      <div class="panel overflow-x-auto">
        <table class="data">
          <thead><tr><th class="pl-4 w-8"><input type="checkbox" checked={draft.machines.length > 0 && chosen.length === draft.machines.length} onChange={(e) => patch({ selected: (e.target as HTMLInputElement).checked ? draft.machines.map((m) => m.mac) : [] })} aria-label="Select all" /></th><th>MAC</th><th>IP</th><th>Arch</th><th>Talos</th><th class="num">CPU</th><th class="num">RAM</th><th>Install disk</th><th>KVM</th><th>Hardware</th><th>Notes</th></tr></thead>
          <tbody>
            {draft.machines.length === 0 && <tr><td colSpan={11} class="pl-4 text-muted py-3">{scanning ? 'Scanning…' : 'No unassigned machines in maintenance mode. Boot one from a Talos ISO and scan its subnet.'}</td></tr>}
            {draft.machines.map((m) => {
              const disks = installCandidates(m)
              const warns = machineWarnings(m, chosen.length ? chosen : draft.machines)
              return (
                <tr key={m.mac} class="cursor-pointer" onClick={() => toggle(m.mac)}>
                  <td class="pl-4"><input type="checkbox" class="pointer-events-none" checked={draft.selected.includes(m.mac)} readOnly /></td>
                  <td class="mono">{m.mac}</td><td class="mono">{m.ip}</td><td>{m.arch}</td><td class="mono">{m.talosVersion}</td>
                  <td class="num">{m.inventory?.cpus ?? '—'}</td><td class="num">{fmt.bytes(m.inventory?.memoryBytes ?? 0)}</td>
                  <td class="mono">{disks[0] ? `${disks[0].devPath} ${fmt.bytes(disks[0].sizeBytes)}${disks.length > 1 ? ` +${disks.length - 1}` : ''}` : <span class="text-bad">none</span>}</td>
                  <td>{m.inventory?.kvm ? <Pill tone="good">yes</Pill> : <span class="text-muted">no</span>}</td>
                  <td class="text-muted truncate max-w-[200px]">{[m.inventory?.manufacturer, m.inventory?.product].filter(Boolean).join(' ') || '—'}</td>
                  <td>{warns.length ? <span class="text-warn text-[12px]">{warns.join('; ')}</span> : <span class="text-muted">—</span>}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <div class="flex items-end gap-3">
        <Field label="Cluster name" hint="DNS label; prefixes hostnames and names the kubeconfig context.">
          <input class="input w-56 mono" value={draft.name} onInput={(e) => patch({ name: (e.target as HTMLInputElement).value.toLowerCase() })} />
        </Field>
        <span class="text-[13px] text-muted pb-2">{chosen.length} machine{chosen.length === 1 ? '' : 's'} selected · {topologyText(chosen.length)}</span>
      </div>
    </>
  )
}

export function topologyText(n: number) {
  if (n === 0) return 'select at least one'
  if (n < 3) return '1 control plane (no HA), ' + (n - 1) + ' worker' + (n - 1 === 1 ? '' : 's')
  if (n < 6) return '3 schedulable control planes, ' + (n - 3) + ' worker' + (n - 3 === 1 ? '' : 's')
  return '3 dedicated control planes, ' + (n - 3) + ' workers'
}

// ─── 2 · Design ──────────────────────────────────────────────────────────────

export function DesignStep({ draft, setCluster, reset, busy }: { draft: Draft; setCluster: SetCluster; reset: () => Promise<void>; busy: boolean }) {
  const c = draft.cluster!
  const pools = c.spec.pools ?? []
  const poolOf = (name?: string) => pools.find((p) => p.name === name)
  const updateNode = (i: number, patch: Partial<NodeSpec>) => setCluster((c) => ({ ...c, spec: { ...c.spec, nodes: c.spec.nodes.map((n, j) => j === i ? { ...n, ...patch } : n) } }))
  const setPools = (ps: Pool[]) => setCluster((c) => {
    // Renaming a pool with nodes is blocked in the editor, so only role changes matter.
    const nodes = c.spec.nodes.map((n) => { const p = ps.find((x) => x.name === n.pool); return p ? { ...n, role: p.role } : n })
    return { ...c, spec: { ...c.spec, pools: ps, nodes } }
  })
  const autoName = () => setCluster((c) => {
    const counters: Record<string, number> = {}
    return { ...c, spec: { ...c.spec, nodes: c.spec.nodes.map((n) => { const p = n.pool ?? 'node'; counters[p] = (counters[p] ?? 0) + 1; return { ...n, hostname: `${c.metadata.name}-${p === 'controlplane' ? 'cp' : p}-${String(counters[p]).padStart(2, '0')}` } }) } }
  })
  const cps = c.spec.nodes.filter((n) => n.role === 'controlplane').length
  return (
    <>
      <div class="panel p-4 flex flex-col gap-1">
        <div class="flex items-center gap-3">
          <span class="label">Proposal</span>
          <span class="text-[13px]">{cps} control plane{cps === 1 ? '' : 's'}{cps >= 3 ? ' (etcd HA)' : ''}, {c.spec.nodes.length - cps} worker{c.spec.nodes.length - cps === 1 ? '' : 's'}{c.spec.controlPlane.allowScheduling ? ', control planes schedulable' : ', dedicated control planes'}</span>
          <span class="ml-auto flex gap-2">
            <button class="btn !py-1" onClick={autoName}>Auto-name</button>
            <button class="btn !py-1" disabled={busy} onClick={() => reset().catch(() => {})}>Reset to proposal</button>
          </span>
        </div>
        <p class="text-[13px] text-muted">Kubit put the most alike, smallest machines in the control plane and kept KVM-capable ones as workers for gVisor. Change any cell; pools decide role, labels, taints and installer image.</p>
      </div>
      <div class="panel overflow-x-auto">
        <table class="data">
          <thead><tr><th class="pl-4">Machine</th><th>Pool</th><th>Hostname</th><th>Install disk</th><th>Node labels</th></tr></thead>
          <tbody>
            {c.spec.nodes.map((n, i) => {
              const m = machineOf(draft, n)
              const disks = installCandidates(m)
              return (
                <tr key={n.mac ?? n.ip}>
                  <td class="pl-4 whitespace-nowrap"><span class="mono">{n.ip}</span><br /><span class="text-[11px] text-muted mono">{n.mac} · {m?.inventory?.cpus ?? '?'} CPU · {fmt.bytes(m?.inventory?.memoryBytes ?? 0)}{n.kvm ? ' · kvm' : ''}</span></td>
                  <td>
                    <select class="input !py-1" value={n.pool} onChange={(e) => { const p = poolOf((e.target as HTMLSelectElement).value)!; updateNode(i, { pool: p.name, role: p.role }) }}>
                      {pools.map((p) => <option key={p.name} value={p.name}>{p.name}{p.role === 'controlplane' ? ' (control plane)' : ''}</option>)}
                    </select>
                  </td>
                  <td><input class="input !py-1 mono w-48" value={n.hostname} onInput={(e) => updateNode(i, { hostname: (e.target as HTMLInputElement).value })} /></td>
                  <td>
                    <select class="input !py-1 mono" value={n.installDisk?.path ?? ''} onChange={(e) => { const v = (e.target as HTMLSelectElement).value; updateNode(i, { installDisk: v ? { path: v } : undefined }) }}>
                      {poolOf(n.pool)?.installDisk && <option value="">pool policy</option>}
                      {disks.map((d) => <option key={d.devPath} value={d.devPath}>{d.devPath} · {fmt.bytes(d.sizeBytes)}{d.model ? ` · ${d.model}` : ''}{d.transport ? ` · ${d.transport}` : ''}</option>)}
                      {disks.length === 0 && <option value="">no disk</option>}
                    </select>
                  </td>
                  <td class="text-[12px] text-muted mono">{Object.entries({ ...(poolOf(n.pool)?.labels ?? {}), ...(n.labels ?? {}) }).map(([k, v]) => `${k}=${v}`).join(' ') || '—'}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <div class="flex flex-col gap-2">
        <div><span class="label">Pools</span><p class="text-[13px] text-muted">A pool is a class of machines: it owns the role, labels, taints, system extensions (hence the installer image) and the install-disk policy. Nodes inherit the pool and may add their own labels.</p></div>
        <PoolsEditor pools={pools} onChange={setPools} inUse={(name) => c.spec.nodes.filter((n) => n.pool === name).length} defaultExtensions={c.spec.extensions} />
      </div>
    </>
  )
}

// ─── 3 · Network ─────────────────────────────────────────────────────────────

export function NetworkStep({ draft, setCluster }: { draft: Draft; setCluster: SetCluster }) {
  const c = draft.cluster!
  const cp = c.spec.controlPlane
  const setCP = (p: Partial<typeof cp>) => setCluster((c) => ({ ...c, spec: { ...c.spec, controlPlane: { ...c.spec.controlPlane, ...p } } }))
  const setNet = (p: Partial<ClusterSpec['spec']['network']>) => setCluster((c) => ({ ...c, spec: { ...c.spec, network: { ...c.spec.network, ...p } } }))
  const setRange = (range: string) => setCluster((c) => ({ ...c, spec: { ...c.spec, platform: { ...c.spec.platform, metallb: { ...c.spec.platform.metallb, range } } } }))
  const updateNode = (i: number, patch: Partial<NodeSpec>) => setCluster((c) => ({ ...c, spec: { ...c.spec, nodes: c.spec.nodes.map((n, j) => j === i ? { ...n, ...patch } : n) } }))
  const cps = c.spec.nodes.filter((n) => n.role === 'controlplane')
  const first = c.spec.nodes[0]?.ip ?? ''
  const range = c.spec.platform.metallb.range ?? ''
  const list = (s: string) => s.split(/[,\s]+/).filter(Boolean)

  const checks: { tone: 'good' | 'warn' | 'bad'; text: string }[] = []
  if (cp.vip) {
    if (ip4(cp.vip) === null) checks.push({ tone: 'bad', text: 'VIP is not an IPv4 address.' })
    else if (first && !sameSubnet(cp.vip, first, 24)) checks.push({ tone: 'warn', text: `VIP ${cp.vip} is not in the /24 of the nodes (${first}).` })
    else if (range && inRange(cp.vip, range)) checks.push({ tone: 'bad', text: 'VIP lies inside the MetalLB range.' })
    else if (c.spec.nodes.some((n) => addrOf(n.network?.addresses?.[0] ?? n.ip) === cp.vip)) checks.push({ tone: 'bad', text: 'VIP equals a node address.' })
    if (cps.length === 1) checks.push({ tone: 'warn', text: 'A VIP with one control plane adds nothing; it becomes useful once you add more.' })
  } else if (cps.length > 1) checks.push({ tone: 'warn', text: 'No VIP: kubeconfig and joining nodes depend on the first control plane staying up.' })
  if (c.spec.platform.metallb.enabled) {
    const rr = parseRange(range)
    if (!rr) checks.push({ tone: 'bad', text: 'MetalLB range must be a.b.c.d-a.b.c.e.' })
    else {
      if (first && !sameSubnet(range.split('-')[0], first, 24)) checks.push({ tone: 'warn', text: 'MetalLB range is outside the nodes\' /24; ARP announcements will not reach clients.' })
      const hit = c.spec.nodes.filter((n) => inRange(n.network?.addresses?.[0] ?? n.ip, range))
      if (hit.length) checks.push({ tone: 'bad', text: `MetalLB range overlaps node address${hit.length === 1 ? '' : 'es'}: ${hit.map((n) => n.hostname).join(', ')}.` })
      else checks.push({ tone: 'good', text: `${rr[1] - rr[0] + 1} LoadBalancer addresses available.` })
    }
  }
  const staticAddrs = c.spec.nodes.flatMap((n) => n.network?.addresses ?? []).map(addrOf)
  const dupes = staticAddrs.filter((a, i) => staticAddrs.indexOf(a) !== i)
  if (dupes.length) checks.push({ tone: 'bad', text: `Duplicate static address: ${[...new Set(dupes)].join(', ')}.` })

  return (
    <>
      <div class="panel p-4 grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="Control plane VIP" hint="Layer-2 address the control planes share for the API. Empty = clients talk to the first control plane directly.">
          <input class="input mono" value={cp.vip ?? ''} placeholder="none" onInput={(e) => { const vip = (e.target as HTMLInputElement).value.trim(); setCP({ vip: vip || undefined, endpoint: vip ? `https://${vip}:6443` : cps[0] ? `https://${cps[0].ip}:6443` : cp.endpoint }) }} />
        </Field>
        <Field label="API endpoint" hint="Derived from the VIP (or the first control plane); override for DNS names or an external load balancer.">
          <input class="input mono" value={cp.endpoint ?? ''} onInput={(e) => setCP({ endpoint: (e.target as HTMLInputElement).value.trim() })} />
        </Field>
        <Field label="Workloads on control planes" hint={`${cps.length} control plane${cps.length === 1 ? '' : 's'}; below 6 nodes Kubit proposes schedulable so capacity is not wasted.`}>
          <select class="input" value={String(cp.allowScheduling ?? true)} onChange={(e) => setCP({ allowScheduling: (e.target as HTMLSelectElement).value === 'true' })}>
            <option value="true">Allowed (control planes also run pods)</option>
            <option value="false">Dedicated (NoSchedule taint)</option>
          </select>
        </Field>
        <Field label="MetalLB range" hint="Addresses handed to LoadBalancer services; must be free on the LAN and outside the DHCP pool.">
          <input class="input mono" value={range} disabled={!c.spec.platform.metallb.enabled} onInput={(e) => setRange((e.target as HTMLInputElement).value.trim())} />
        </Field>
        <Field label="Nameservers" hint="Cluster-wide DNS for every node (comma-separated). Empty keeps what DHCP provides.">
          <input class="input mono" value={(c.spec.network.nameservers ?? []).join(', ')} placeholder="from DHCP" onInput={(e) => { const v = list((e.target as HTMLInputElement).value); setNet({ nameservers: v.length ? v : undefined }) }} />
        </Field>
        <Field label="NTP servers" hint="Empty uses Talos' default (time.cloudflare.com).">
          <input class="input mono" value={(c.spec.network.ntp ?? []).join(', ')} placeholder="time.cloudflare.com" onInput={(e) => { const v = list((e.target as HTMLInputElement).value); setNet({ ntp: v.length ? v : undefined }) }} />
        </Field>
        <div class="md:col-span-2 grid grid-cols-2 gap-4">
          <Field label="Pod CIDR" hint="Fixed for the cluster's lifetime."><input class="input mono" value={c.spec.network.podCIDR} onInput={(e) => setNet({ podCIDR: (e.target as HTMLInputElement).value.trim() })} /></Field>
          <Field label="Service CIDR" hint="Fixed for the cluster's lifetime."><input class="input mono" value={c.spec.network.serviceCIDR} onInput={(e) => setNet({ serviceCIDR: (e.target as HTMLInputElement).value.trim() })} /></Field>
        </div>
      </div>
      {checks.length > 0 && <div class="flex flex-col gap-1">{checks.map((k, i) => <Notice key={i} tone={k.tone}>{k.text}</Notice>)}</div>}
      <div class="flex flex-col gap-2">
        <div><span class="label">Node addressing</span><p class="text-[13px] text-muted">DHCP is the default: the machine keeps asking the LAN's server and Kubit follows it by MAC. Static pins the address in the machine config — use it when the DHCP pool is small, for control planes you want stable without a VIP, or when a VLAN is required.</p></div>
        <div class="panel overflow-x-auto">
          <table class="data">
            <thead><tr><th class="pl-4">Node</th><th>Mode</th><th>Address (CIDR)</th><th>Gateway</th><th>DNS</th><th>VLAN</th></tr></thead>
            <tbody>
              {c.spec.nodes.map((n, i) => {
                const st = !!n.network
                const prefix = prefixOf(n.network?.addresses?.[0] ?? '', 24)
                return (
                  <tr key={n.mac ?? n.ip}>
                    <td class="pl-4 whitespace-nowrap"><span class="mono">{n.hostname}</span><br /><span class="text-[11px] text-muted mono">lease {n.ip}</span></td>
                    <td>
                      <select class="input !py-1" value={st ? 'static' : 'dhcp'} onChange={(e) => {
                        if ((e.target as HTMLSelectElement).value === 'dhcp') updateNode(i, { network: undefined })
                        else updateNode(i, { network: { addresses: [`${n.ip}/24`], gateway: guessGateway(n.ip, 24) } })
                      }}>
                        <option value="dhcp">DHCP</option>
                        <option value="static">Static</option>
                      </select>
                    </td>
                    <td><input class="input !py-1 mono w-52" disabled={!st} value={n.network?.addresses?.[0] ?? ''} placeholder={`${n.ip}/24`} onInput={(e) => updateNode(i, { network: { ...n.network!, addresses: [(e.target as HTMLInputElement).value.trim()] } })} /></td>
                    <td><input class="input !py-1 mono w-36" disabled={!st} value={n.network?.gateway ?? ''} placeholder={guessGateway(n.ip, prefix)} onInput={(e) => updateNode(i, { network: { ...n.network!, gateway: (e.target as HTMLInputElement).value.trim() || undefined } })} /></td>
                    <td><input class="input !py-1 mono w-44" disabled={!st} value={(n.network?.nameservers ?? []).join(', ')} placeholder="cluster default" onInput={(e) => { const v = list((e.target as HTMLInputElement).value); updateNode(i, { network: { ...n.network!, nameservers: v.length ? v : undefined } }) }} /></td>
                    <td><input class="input !py-1 mono w-20" type="number" min={0} max={4094} disabled={!st} value={n.network?.vlan ?? ''} placeholder="none" onInput={(e) => { const v = Number((e.target as HTMLInputElement).value); updateNode(i, { network: { ...n.network!, vlan: v > 0 ? v : undefined } }) }} /></td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>
    </>
  )
}

// ─── 4 · Platform ────────────────────────────────────────────────────────────

const addons: { key: keyof ClusterSpec['spec']['platform']; title: string; what: string; size: string }[] = [
  { key: 'metallb', title: 'MetalLB', what: 'LoadBalancer services on bare metal: announces addresses from the range over ARP.', size: '~120 MiB, 1 controller + 1 speaker per node' },
  { key: 'ingressNginx', title: 'ingress-nginx', what: 'HTTP(S) ingress controller behind a LoadBalancer address; the default IngressClass.', size: '~250 MiB, 1 pod' },
  { key: 'gvisor', title: 'gVisor runtime class', what: 'RuntimeClass "gvisor" for sandboxed pods; uses KVM acceleration on machines that expose it.', size: 'no running pods' },
  { key: 'metricsServer', title: 'metrics-server', what: 'Resource metrics for kubectl top, HPA and Kubit\'s capacity views.', size: '~100 MiB, 1 pod' },
  { key: 'certManager', title: 'cert-manager', what: 'X.509 certificates from ACME (Let\'s Encrypt) or internal CAs; issuers are configured afterwards.', size: '~300 MiB, 3 pods' },
  { key: 'argocd', title: 'Argo CD', what: 'GitOps: applications from Git repositories. Recommended home for everything above the platform layer.', size: '~1 GiB, 7 pods' },
]

export function PlatformStep({ draft, setCluster, patch }: { draft: Draft; setCluster: SetCluster; patch: (p: Partial<Draft>) => void }) {
  const c = draft.cluster!
  const toggle = (key: keyof ClusterSpec['spec']['platform'], enabled: boolean) => setCluster((c) => ({ ...c, spec: { ...c.spec, platform: { ...c.spec.platform, [key]: { ...c.spec.platform[key], enabled } } } }))
  return (
    <>
      <div class="panel p-4"><p class="text-[13px] text-muted">Kubit converges these add-ons with OpenTofu after the nodes are Ready. Each can be enabled, configured and planned later under Add-ons; ingress-nginx needs MetalLB for an external address.</p></div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        {addons.map((a) => {
          const on = c.spec.platform[a.key].enabled
          return (
            <label key={a.key} class={`panel p-4 flex gap-3 cursor-pointer ${on ? 'border-accent/60' : ''} ${draft.skipPlatform ? 'opacity-50' : ''}`}>
              <input type="checkbox" class="mt-1" checked={on} disabled={draft.skipPlatform} onChange={(e) => toggle(a.key, (e.target as HTMLInputElement).checked)} />
              <div class="flex-1 min-w-0">
                <div class="font-medium">{a.title}</div>
                <p class="text-[12.5px] text-muted">{a.what}</p>
                <p class="text-[11px] text-muted mt-1">{a.size}</p>
              </div>
            </label>
          )
        })}
      </div>
      <label class="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={draft.skipPlatform} onChange={(e) => patch({ skipPlatform: (e.target as HTMLInputElement).checked })} /> Skip the platform layer for now (nodes only; plan it later under Add-ons)</label>
    </>
  )
}

// ─── 5 · Review ──────────────────────────────────────────────────────────────

export function ReviewStep({ draft, setCluster, patch, onCreate, busy }: { draft: Draft; setCluster: SetCluster; patch: (p: Partial<Draft>) => void; onCreate: (yaml: string) => void; busy: boolean }) {
  const c = draft.cluster!
  const [tab, setTab] = useState<'summary' | 'yaml'>('summary')
  const [yaml, setYaml] = useState('')
  const [dirty, setDirty] = useState(false)
  const [lintErr, setLintErr] = useState<string | null>(null)
  const [linting, setLinting] = useState(false)
  const key = useMemo(() => JSON.stringify(c), [c])
  useEffect(() => {
    setLinting(true)
    api.lint(key).then((r) => { patch({ warnings: r.warnings ?? [] }); setYaml(r.yaml); setDirty(false); setLintErr(null) })
      .catch((e) => setLintErr(e.message)).finally(() => setLinting(false))
  }, [key]) // eslint-disable-line
  const applyYaml = () => api.validate(yaml).then((v) => { setCluster(() => v.cluster); toast('Declaration updated from YAML', 'good') }).catch((e) => setLintErr(e.message))
  const errors = draft.warnings.filter((w) => w.level !== 'info')
  return (
    <>
      <Tabs active={tab} onSelect={(t) => setTab(t as any)} tabs={[{ id: 'summary', label: 'Summary' }, { id: 'yaml', label: 'cluster.yaml' }]} />
      {tab === 'summary' && (
        <>
          <div class="flex flex-col gap-1">
            <span class="label">Checks {linting && <span class="text-muted">· linting…</span>}</span>
            {lintErr && <Notice tone="bad">{lintErr}</Notice>}
            {!lintErr && draft.warnings.length === 0 && !linting && <Notice tone="good">No findings. The declaration is valid and follows the recommendations.</Notice>}
            {draft.warnings.map((w, i) => <WarningLine key={i} w={w} />)}
          </div>
          <div class="panel overflow-x-auto">
            <table class="data">
              <thead><tr><th class="pl-4">Hostname</th><th>Pool</th><th>Address</th><th>Install disk</th><th>Labels</th><th>Taints</th></tr></thead>
              <tbody>
                {c.spec.nodes.map((n) => {
                  const p = (c.spec.pools ?? []).find((x) => x.name === n.pool)
                  return (
                    <tr key={n.hostname}>
                      <td class="pl-4 mono">{n.hostname}</td>
                      <td><span class="mono">{n.pool}</span> <span class="text-muted text-[11px]">{n.role === 'controlplane' ? 'control plane' : 'worker'}</span></td>
                      <td class="mono">{n.network ? <>{n.network.addresses.join(', ')}{n.network.vlan ? ` vlan ${n.network.vlan}` : ''} <span class="text-muted text-[11px]">static</span></> : <>{n.ip} <span class="text-muted text-[11px]">dhcp</span></>}</td>
                      <td class="mono">{n.installDisk?.path ?? (p?.installDisk?.selector ? `selector ${Object.values(p.installDisk.selector).join(' ')}` : '—')}</td>
                      <td class="mono text-[11px]">{Object.entries({ ...(p?.labels ?? {}), ...(n.labels ?? {}) }).map(([k, v]) => `${k}=${v}`).join(' ') || '—'}</td>
                      <td class="mono text-[11px]">{Object.entries({ ...(p?.taints ?? {}), ...(n.taints ?? {}) }).map(([k, v]) => `${k}=${v}`).join(' ') || (n.role === 'controlplane' && c.spec.controlPlane.allowScheduling === false ? 'control-plane:NoSchedule' : '—')}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
      {tab === 'yaml' && (
        <>
          <textarea class="input mono !text-[12px] h-[460px]" value={yaml} spellcheck={false} onInput={(e) => { setYaml((e.target as HTMLTextAreaElement).value); setDirty(true) }} />
          <div class="flex gap-2 items-center">
            <button class="btn" disabled={!dirty} onClick={applyYaml}>Validate and use this YAML</button>
            {dirty && <span class="text-[12px] text-warn">edited; validate to bring the summary in sync</span>}
          </div>
        </>
      )}
      <div class="flex items-center gap-3 justify-end">
        {errors.length > 0 && <span class="text-[12px] text-warn">{errors.length} warning{errors.length === 1 ? '' : 's'} — creating anyway is allowed</span>}
        <button class="btn btn-primary" disabled={busy || dirty || !yaml || !!lintErr} title={dirty ? 'Validate the edited YAML first' : ''} onClick={() => onCreate(yaml)}>{busy ? 'Starting…' : `Create ${c.metadata.name}`}</button>
      </div>
    </>
  )
}

export function WarningLine({ w }: { w: Warning }) {
  return <Notice tone={w.level === 'warn' ? 'warn' : 'info'}><span class="mono text-[11px] opacity-70 mr-2">{w.code}</span>{w.node && <span class="mono mr-1">{w.node}:</span>}{w.message}</Notice>
}

// ─── Right-hand summary ──────────────────────────────────────────────────────

export function Summary({ draft }: { draft: Draft }) {
  const c = draft.cluster
  const chosen = draft.machines.filter((m) => draft.selected.includes(m.mac))
  const rows: [string, string][] = [['Name', draft.name]]
  if (!c) {
    rows.push(['Machines', String(chosen.length)], ['Topology', topologyText(chosen.length)])
    const cpu = chosen.reduce((s, m) => s + (m.inventory?.cpus ?? 0), 0), mem = chosen.reduce((s, m) => s + (m.inventory?.memoryBytes ?? 0), 0)
    if (chosen.length) rows.push(['Capacity', `${cpu} CPU · ${fmt.bytes(mem)}`])
  } else {
    const pools = c.spec.pools ?? []
    for (const p of pools) { const n = c.spec.nodes.filter((x) => x.pool === p.name).length; if (n) rows.push([`Pool ${p.name}`, `${n} node${n === 1 ? '' : 's'}${p.role === 'controlplane' ? ' · control plane' : ''}`]) }
    const cps = c.spec.nodes.filter((n) => n.role === 'controlplane').length
    rows.push(['etcd', cps >= 3 ? `${cps} members, HA` : `${cps} member${cps === 1 ? '' : 's'}, no HA`])
    rows.push(['Endpoint', c.spec.controlPlane.vip ? `VIP ${c.spec.controlPlane.vip}` : c.spec.controlPlane.endpoint || '—'])
    const st = c.spec.nodes.filter((n) => n.network).length
    rows.push(['Addressing', st === 0 ? 'DHCP' : st === c.spec.nodes.length ? 'static' : `${st} static, ${c.spec.nodes.length - st} DHCP`])
    rows.push(['Talos', `${c.spec.talosVersion} · k8s ${c.spec.kubernetesVersion}`])
    const on = draft.skipPlatform ? [] : addons.filter((a) => c.spec.platform[a.key].enabled).map((a) => a.title)
    rows.push(['Add-ons', on.length ? on.join(', ') : draft.skipPlatform ? 'skipped' : 'none'])
    if (c.spec.platform.metallb.enabled && !draft.skipPlatform) rows.push(['MetalLB', c.spec.platform.metallb.range ?? '—'])
  }
  const warn = draft.warnings.filter((w) => w.level === 'warn').length
  return (
    <aside class="panel p-4 flex flex-col gap-3 xl:sticky xl:top-4">
      <span class="label">Summary</span>
      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[12.5px]">
        {rows.map(([k, v]) => <><dt class="text-muted whitespace-nowrap">{k}</dt><dd class="min-w-0 break-words">{v}</dd></>)}
      </dl>
      {c && draft.warnings.length > 0 && <div class="text-[12px] text-muted border-t border-border pt-2">{warn} warning{warn === 1 ? '' : 's'}, {draft.warnings.length - warn} note{draft.warnings.length - warn === 1 ? '' : 's'} — see Review.</div>}
    </aside>
  )
}
