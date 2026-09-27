import { Fragment } from 'preact'
import { fmt } from '../../api'
import { addonCatalog } from '../../addons'
import { topologyText, type Draft } from './draft'

export function Summary({ draft }: { draft: Draft }) {
  const c = draft.cluster
  const chosen = draft.machines.filter((m) => draft.selected.includes(m.mac))
  const rows: [string, string][] = [['Name', draft.name]]
  if (!c) {
    rows.push(['Machines', String(chosen.length)], ['Topology', topologyText(chosen.length)])
    const cpu = chosen.reduce((s, m) => s + (m.inventory?.cpus ?? 0), 0), mem = chosen.reduce((s, m) => s + (m.inventory?.memoryBytes ?? 0), 0)
    if (chosen.length) rows.push(['Capacity', `${cpu} CPU · ${fmt.bytes(mem)}`])
  } else {
    for (const p of c.spec.pools ?? []) { const n = c.spec.nodes.filter((x) => x.pool === p.name).length; if (n) rows.push([`Pool ${p.name}`, `${n} node${n === 1 ? '' : 's'}${p.role === 'controlplane' ? ' · control plane' : ''}`]) }
    const cps = c.spec.nodes.filter((n) => n.role === 'controlplane').length
    rows.push(['etcd', cps >= 3 ? `${cps} members, HA` : `${cps} member${cps === 1 ? '' : 's'}, no HA`])
    rows.push(['Endpoint', c.spec.controlPlane.vip ? `VIP ${c.spec.controlPlane.vip}` : c.spec.controlPlane.endpoint || '—'])
    const st = c.spec.nodes.filter((n) => n.network).length
    rows.push(['Addressing', st === 0 ? 'DHCP' : st === c.spec.nodes.length ? 'static' : `${st} static, ${c.spec.nodes.length - st} DHCP`])
    const data = c.spec.nodes.reduce((s, n) => s + (n.dataDisks?.length ?? 0), 0)
    const dataNodes = c.spec.nodes.filter((n) => n.dataDisks?.length).length
    if (data) rows.push(['Data disks', `${data} on ${dataNodes} node${dataNodes === 1 ? '' : 's'}`])
    rows.push(['Talos', `${c.spec.talosVersion} · k8s ${c.spec.kubernetesVersion}`])
    const on = draft.skipPlatform ? [] : addonCatalog.filter((a) => c.spec.platform[a.key].enabled).map((a) => a.name)
    rows.push(['Add-ons', on.length ? on.join(', ') : draft.skipPlatform ? 'skipped' : 'none'])
    if (c.spec.platform.metallb.enabled && !draft.skipPlatform) rows.push(['MetalLB', c.spec.platform.metallb.range ?? '—'])
  }
  const warn = draft.warnings.filter((w) => w.level === 'warn').length
  return (
    <aside class="panel p-3 flex flex-col gap-3 xl:sticky xl:top-4">
      <span class="label">Summary</span>
      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[12.5px]">
        {rows.map(([k, v]) => <Fragment key={k}><dt class="text-muted whitespace-nowrap">{k}</dt><dd class="min-w-0 break-words">{v}</dd></Fragment>)}
      </dl>
      {c && draft.warnings.length > 0 && <div class="text-[12px] text-muted border-t border-border pt-2">{warn} warning{warn === 1 ? '' : 's'}, {draft.warnings.length - warn} note{draft.warnings.length - warn === 1 ? '' : 's'}; see Review.</div>}
    </aside>
  )
}
