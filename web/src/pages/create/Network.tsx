import { splitList, type ClusterSpec } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { Field, Notice } from '../../components/ui'
import { addrOf, guessGateway, inRange, ip4, parseRange, prefixOf, sameSubnet, staticNetwork } from '../../net'
import type { Tone } from '../../tone'
import { registryCIDROK, updateNode, type Draft, type SetCluster } from './draft'

export function NetworkStep({ draft, setCluster }: { draft: Draft; setCluster: SetCluster }) {
  const c = draft.cluster!
  const cp = c.spec.controlPlane
  const setCP = (p: Partial<typeof cp>) => setCluster((c) => ({ ...c, spec: { ...c.spec, controlPlane: { ...c.spec.controlPlane, ...p } } }))
  const setNet = (p: Partial<ClusterSpec['spec']['network']>) => setCluster((c) => {
    const network = { ...c.spec.network, ...p }
    const platform = registryCIDROK(network.serviceCIDR) ? c.spec.platform : { ...c.spec.platform, builds: { ...c.spec.platform.builds, enabled: false } }
    return { ...c, spec: { ...c.spec, network, platform } }
  })
  const setRange = (range: string) => setCluster((c) => ({ ...c, spec: { ...c.spec, platform: { ...c.spec.platform, metallb: { ...c.spec.platform.metallb, range } } } }))
  const setNode = (i: number, patch: Parameters<typeof updateNode>[2]) => updateNode(setCluster, i, patch)
  const cps = c.spec.nodes.filter((n) => n.role === 'controlplane')
  const first = c.spec.nodes[0]?.ip ?? ''
  const range = c.spec.platform.metallb.range ?? ''

  const checks: { tone: Tone; text: string }[] = []
  if (cp.vip) {
    if (ip4(cp.vip) === null) checks.push({ tone: 'bad', text: 'VIP is not an IPv4 address.' })
    else if (first && !sameSubnet(cp.vip, first, 24)) checks.push({ tone: 'warn', text: `VIP ${cp.vip} is not in the /24 of the nodes (${first}).` })
    else if (range && inRange(cp.vip, range)) checks.push({ tone: 'bad', text: 'VIP lies inside the MetalLB range.' })
    else if (c.spec.nodes.some((n) => addrOf(n.network?.addresses?.[0] ?? n.ip) === cp.vip)) checks.push({ tone: 'bad', text: 'VIP equals a node address.' })
    if (cps.length === 1) checks.push({ tone: 'warn', text: 'A VIP with one control plane adds nothing yet.' })
  } else if (cps.length > 1) checks.push({ tone: 'warn', text: 'No VIP: kubeconfig and joining nodes depend on the first control plane.' })
  if (c.spec.platform.metallb.enabled) {
    const rr = parseRange(range)
    if (!rr) checks.push({ tone: 'bad', text: 'MetalLB range must be a.b.c.d-a.b.c.e.' })
    else {
      if (first && !sameSubnet(range.split('-')[0], first, 24)) checks.push({ tone: 'warn', text: 'MetalLB range is outside the nodes\' /24; clients will not reach it.' })
      const hit = c.spec.nodes.filter((n) => inRange(n.network?.addresses?.[0] ?? n.ip, range))
      if (hit.length) checks.push({ tone: 'bad', text: `MetalLB range overlaps node address${hit.length === 1 ? '' : 'es'}: ${hit.map((n) => n.hostname).join(', ')}.` })
      else checks.push({ tone: 'good', text: `${rr[1] - rr[0] + 1} LoadBalancer addresses available.` })
    }
  }
  const staticAddrs = c.spec.nodes.flatMap((n) => n.network?.addresses ?? []).map(addrOf)
  const dupes = staticAddrs.filter((a, i) => staticAddrs.indexOf(a) !== i)
  if (dupes.length) checks.push({ tone: 'bad', text: `Duplicate static address: ${[...new Set(dupes)].join(', ')}.` })
  const columns: Column<number>[] = [
    { id: 'node', header: 'Node', cell: (i) => { const n = c.spec.nodes[i]; return <span class="flex flex-col"><span class="mono">{n.hostname}</span><span class="text-[10px] text-muted mono">lease {n.ip}</span></span> } },
    { id: 'mode', header: 'Mode', cell: (i) => { const n = c.spec.nodes[i]; return (
      <select class="input !py-1" value={n.network ? 'static' : 'dhcp'} onChange={(e) => setNode(i, { network: (e.target as HTMLSelectElement).value === 'dhcp' ? undefined : staticNetwork(n.ip) })}>
        <option value="dhcp">DHCP</option>
        <option value="static">Static</option>
      </select>
    ) } },
    { id: 'addr', header: 'Address (CIDR)', cell: (i) => { const n = c.spec.nodes[i]; return <input class="input !py-1 mono w-52" disabled={!n.network} value={n.network?.addresses?.[0] ?? ''} placeholder={`${n.ip}/24`} onInput={(e) => setNode(i, { network: { ...n.network!, addresses: [(e.target as HTMLInputElement).value.trim()] } })} /> } },
    { id: 'gw', header: 'Gateway', cell: (i) => { const n = c.spec.nodes[i]; return <input class="input !py-1 mono w-36" disabled={!n.network} value={n.network?.gateway ?? ''} placeholder={guessGateway(n.ip, prefixOf(n.network?.addresses?.[0] ?? '', 24))} onInput={(e) => setNode(i, { network: { ...n.network!, gateway: (e.target as HTMLInputElement).value.trim() || undefined } })} /> } },
    { id: 'dns', header: 'DNS', cell: (i) => { const n = c.spec.nodes[i]; return <input class="input !py-1 mono w-44" disabled={!n.network} value={(n.network?.nameservers ?? []).join(', ')} placeholder="cluster default" onInput={(e) => { const v = splitList((e.target as HTMLInputElement).value); setNode(i, { network: { ...n.network!, nameservers: v.length ? v : undefined } }) }} /> } },
    { id: 'vlan', header: 'VLAN', cell: (i) => { const n = c.spec.nodes[i]; return <input class="input !py-1 mono w-20" type="number" min={0} max={4094} disabled={!n.network} value={n.network?.vlan ?? ''} placeholder="none" onInput={(e) => { const v = Number((e.target as HTMLInputElement).value); setNode(i, { network: { ...n.network!, vlan: v > 0 ? v : undefined } }) }} /> } },
  ]

  return (
    <>
      <div class="panel p-3 grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="Control plane VIP" hint="Shared layer-2 address for the API; empty for none">
          <input class="input mono" value={cp.vip ?? ''} placeholder="none" onInput={(e) => { const vip = (e.target as HTMLInputElement).value.trim(); setCP({ vip: vip || undefined, endpoint: vip ? `https://${vip}:6443` : cps[0] ? `https://${cps[0].ip}:6443` : cp.endpoint }) }} />
        </Field>
        <Field label="API endpoint" hint="Derived from the VIP or the first control plane">
          <input class="input mono" value={cp.endpoint ?? ''} onInput={(e) => setCP({ endpoint: (e.target as HTMLInputElement).value.trim() })} />
        </Field>
        <Field label="Workloads on control planes" hint={`${cps.length} control plane${cps.length === 1 ? '' : 's'}; schedulable by default below 6 nodes`}>
          <select class="input" value={String(cp.allowScheduling ?? true)} onChange={(e) => setCP({ allowScheduling: (e.target as HTMLSelectElement).value === 'true' })}>
            <option value="true">Allowed (control planes also run pods)</option>
            <option value="false">Dedicated (NoSchedule taint)</option>
          </select>
        </Field>
        <Field label="MetalLB range" hint="Free LAN addresses outside the DHCP range">
          <input class="input mono" value={range} disabled={!c.spec.platform.metallb.enabled} onInput={(e) => setRange((e.target as HTMLInputElement).value.trim())} />
        </Field>
        <Field label="Nameservers" hint="Comma-separated; empty keeps DHCP's">
          <input class="input mono" value={(c.spec.network.nameservers ?? []).join(', ')} placeholder="from DHCP" onInput={(e) => { const v = splitList((e.target as HTMLInputElement).value); setNet({ nameservers: v.length ? v : undefined }) }} />
        </Field>
        <Field label="NTP servers" hint="Empty uses Talos' default">
          <input class="input mono" value={(c.spec.network.ntp ?? []).join(', ')} placeholder="time.cloudflare.com" onInput={(e) => { const v = splitList((e.target as HTMLInputElement).value); setNet({ ntp: v.length ? v : undefined }) }} />
        </Field>
        <div class="md:col-span-2 grid grid-cols-2 gap-4">
          <Field label="Pod CIDR" hint="Fixed for the cluster's lifetime"><input class="input mono" value={c.spec.network.podCIDR} onInput={(e) => setNet({ podCIDR: (e.target as HTMLInputElement).value.trim() })} /></Field>
          <Field label="Service CIDR" hint="Fixed for the cluster's lifetime"><input class="input mono" value={c.spec.network.serviceCIDR} onInput={(e) => setNet({ serviceCIDR: (e.target as HTMLInputElement).value.trim() })} /></Field>
        </div>
      </div>
      {checks.length > 0 && <div class="flex flex-col gap-1">{checks.map((k) => <Notice key={k.text} tone={k.tone}>{k.text}</Notice>)}</div>}
      <div class="flex flex-col gap-2">
        <div><span class="label">Node addressing</span><p class="text-[13px] text-muted">DHCP by default, tracked by MAC; Static pins the address in the machine config.</p></div>
        <DataTable search={false} columns={columns} rows={c.spec.nodes.map((_, i) => i)} rowKey={(i) => c.spec.nodes[i].mac ?? c.spec.nodes[i].ip} />
      </div>
    </>
  )
}
