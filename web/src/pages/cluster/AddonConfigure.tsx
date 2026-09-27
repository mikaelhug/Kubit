import { useState } from 'preact/hooks'
import type { AddonInfo } from '../../addons'
import { api, type AddonStatus } from '../../api'
import { Dialog, ErrorBox, Field } from '../../components/ui'
import { toast } from '../../store'
import { toYaml } from '../../yaml'
import type { ClusterCtx } from './ClusterPage'

const noValues = new Set(['gvisor', 'builds'])

export function ConfigureDialog({ ctx, addon, def, onClose, onSaved }: { ctx: ClusterCtx; addon: AddonStatus; def: AddonInfo; onClose: () => void; onSaved: (list: AddonStatus[]) => void }) {
  const [enabled, setEnabled] = useState(addon.enabled)
  const [range, setRange] = useState(ctx.cluster.spec.spec.platform.metallb.range ?? '')
  const [repo, setRepo] = useState({ url: '', branch: '', path: '', interval: '', ...ctx.cluster.spec.spec.platform.flux?.repository })
  const [values, setValues] = useState(toYaml(addon.values ?? {}))
  const [error, setError] = useState<string | null>(null)
  const save = () => api.updateAddon(ctx.name, addon.key, { enabled, range: addon.key === 'metallb' ? range : undefined, repository: addon.key === 'flux' ? repo : undefined, valuesYaml: noValues.has(def.key) ? undefined : values })
    .then((list) => { toast('Saved to cluster.yaml; plan to review', 'good'); onSaved(list) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Configure ${def.name}`} onClose={onClose} width="max-w-2xl" footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" onClick={save}>Save to cluster.yaml</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">{def.what}{def.hint ? ` ${def.hint}` : ''}</p>
      <label class="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={enabled} onChange={(e) => setEnabled((e.target as HTMLInputElement).checked)} /> Enabled {addon.release && !enabled && <span class="text-warn">{addon.key === 'flux' ? '— removes Flux on the next apply; running apps stay' : '— removes the release on the next apply'}</span>}</label>
      {addon.key === 'metallb' && <Field label="Address pool" hint="start-end, on the LAN and outside the DHCP range"><input class="input mono" value={range} onInput={(e) => setRange((e.target as HTMLInputElement).value)} /></Field>}
      {addon.key === 'flux' && (
        <>
          <Field label="Repository" hint="Public HTTPS Git URL; empty installs Flux without a sync"><input class="input mono" value={repo.url} placeholder="https://github.com/you/apps.git" onInput={(e) => setRepo({ ...repo, url: (e.target as HTMLInputElement).value })} /></Field>
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <Field label="Branch"><input class="input mono" value={repo.branch} placeholder="main" onInput={(e) => setRepo({ ...repo, branch: (e.target as HTMLInputElement).value })} /></Field>
            <Field label="Path"><input class="input mono" value={repo.path} placeholder="./" onInput={(e) => setRepo({ ...repo, path: (e.target as HTMLInputElement).value })} /></Field>
            <Field label="Interval"><input class="input mono" value={repo.interval} placeholder="5m" onInput={(e) => setRepo({ ...repo, interval: (e.target as HTMLInputElement).value })} /></Field>
          </div>
        </>
      )}
      {!noValues.has(def.key) && (
        <Field label="Helm values (YAML)" hint={<>Merged over Kubit's defaults. <a class="underline" href={def.docs} target="_blank" rel="noreferrer">Chart values ↗</a></>}>
          <textarea class="input mono !text-[12px] h-52" value={values} spellcheck={false} onInput={(e) => setValues((e.target as HTMLTextAreaElement).value)} />
        </Field>
      )}
    </Dialog>
  )
}
