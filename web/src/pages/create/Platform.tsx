import type { ClusterSpec, FluxRepository } from '../../api'
import { Field } from '../../components/ui'
import { addonCatalog } from '../../addons'
import { hasData, registryCIDROK, type Draft, type PatchDraft, type SetCluster } from './draft'

export function PlatformStep({ draft, setCluster, patch }: { draft: Draft; setCluster: SetCluster; patch: PatchDraft }) {
  const c = draft.cluster!
  const repo = c.spec.platform.flux.repository
  const setRepo = (r: FluxRepository) => setCluster((c) => ({ ...c, spec: { ...c.spec, platform: { ...c.spec.platform, flux: { ...c.spec.platform.flux, repository: r.url.trim() || r.path?.trim() ? { url: r.url.trim(), path: r.path?.trim() || undefined } : undefined } } } }))
  const toggle = (key: keyof ClusterSpec['spec']['platform'], enabled: boolean) => setCluster((c) => {
    const platform = { ...c.spec.platform, [key]: { ...c.spec.platform[key], enabled } }
    if (!enabled && key === 'longhorn') platform.builds = { ...platform.builds, enabled: false }
    return { ...c, spec: { ...c.spec, platform } }
  })
  return (
    <>
      <div class="panel p-3"><p class="text-[13px] text-muted">Applied once the nodes are Ready; ingress-nginx needs MetalLB.</p></div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        {addonCatalog.map((a) => {
          const on = c.spec.platform[a.key].enabled
          const blocked = a.key === 'longhorn' && !hasData(c) ? 'No node has storage.' : a.key === 'builds' && !c.spec.platform.longhorn?.enabled ? 'Needs Longhorn.' : a.key === 'builds' && !registryCIDROK(c.spec.network.serviceCIDR) ? 'Needs an IPv4 service CIDR of /22 or larger.' : ''
          return (
            <label key={a.key} class={`panel p-4 flex gap-3 cursor-pointer ${on ? 'border-accent/60' : ''} ${draft.skipPlatform || blocked ? 'opacity-50' : ''}`}>
              <input type="checkbox" class="mt-1" checked={on && !blocked} disabled={draft.skipPlatform || !!blocked} onChange={(e) => toggle(a.key, (e.target as HTMLInputElement).checked)} />
              <div class="flex-1 min-w-0">
                <div class="font-medium">{a.name}</div>
                <p class="text-[12.5px] text-muted">{a.what}</p>
                <p class="text-[11px] text-muted mt-1">{blocked || a.size}</p>
              </div>
            </label>
          )
        })}
      </div>
      {c.spec.platform.flux.enabled && !draft.skipPlatform && (
        <div class="grid grid-cols-1 md:grid-cols-[1fr_12rem] gap-3">
          <Field label="Apps repository" hint="Public HTTPS Git URL; optional"><input class="input mono" value={repo?.url ?? ''} placeholder="https://github.com/you/apps.git" onInput={(e) => setRepo({ url: (e.target as HTMLInputElement).value, path: repo?.path })} /></Field>
          <Field label="Path"><input class="input mono" value={repo?.path ?? ''} placeholder="./" onInput={(e) => setRepo({ url: repo?.url ?? '', path: (e.target as HTMLInputElement).value })} /></Field>
        </div>
      )}
      <label class="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={draft.skipPlatform} onChange={(e) => patch({ skipPlatform: (e.target as HTMLInputElement).checked })} /> Skip the platform layer for now</label>
    </>
  )
}
