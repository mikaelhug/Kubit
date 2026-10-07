import type { ComponentChildren } from 'preact'
import { useState } from 'preact/hooks'
import { api, type AppsReview, type DeployKey } from '../../api'
import { AppsRepoFields, AppsReviewBlock, appsReady, appsRequest, noApps, useAppsRepo, type AppsChoice } from '../../components/AppsRepo'
import { ConfirmDialog, CopyButton, Dialog, ErrorBox, Pill, Section } from '../../components/ui'
import { toast, writeClusterYaml } from '../../store'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

function githubKeysUrl(url: string) {
  try {
    const u = new URL(url)
    const path = u.pathname.replace(/^\/+|\.git$|\/+$/g, '')
    return u.hostname === 'github.com' && path.split('/').length === 2 ? `https://github.com/${path}/settings/keys/new` : undefined
  } catch {
    return undefined
  }
}

function Row({ label, children, actions }: { label: string; children: ComponentChildren; actions?: ComponentChildren }) {
  return (
    <>
      <dt class="text-muted py-1">{label}</dt>
      <dd class="flex items-center gap-2 min-w-0 py-0.5">
        <span class="min-w-0 flex-1 flex items-center gap-2">{children}</span>
        {actions && <span class="flex items-center gap-1 shrink-0">{actions}</span>}
      </dd>
    </>
  )
}

export function AppsSettings({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const flux = cluster.spec.spec.platform.flux
  const ssh = !!flux?.repository?.url.startsWith('ssh://')
  const { data: status, error } = useLive(() => api.appsStatus(name), [name], [[name, 'apps']], { onError: 'box', refresh: [cluster.hash] })
  const { data: deployKey, set: setDeployKey } = useLive(() => api.deployKey(name), [name], [[name, 'sops']], { onError: 'null', enabled: ssh, refresh: [cluster.hash] })
  const { data: sops } = useLive(() => api.sopsKey(name), [name], [[name, 'sops']], { onError: 'null' })
  const [connecting, setConnecting] = useState(false)
  const connected = !!status?.url
  const git = status?.checkout?.git
  const uncommitted = git?.changes.length ?? 0
  const unpushed = !!git && (!git.upstream || git.ahead > 0)
  return (
    <Section actions={status && <button class={`btn btn-sm ${connected ? '' : 'btn-primary'}`} onClick={() => setConnecting(true)}>{connected ? 'Change' : 'Connect'}</button>}>
      <ErrorBox error={error} />
      {status && (
        <div class="panel p-4 text-[13px]">
          <dl class="grid grid-cols-[max-content_1fr] gap-x-6 items-center">
            <Row label="Repository">{connected ? <span class="mono truncate" title={status.url}>{status.url} · {status.branch}</span> : <span class="text-muted">Not connected</span>}</Row>
            {connected && <Row label="Flux path"><span class="mono">{status.path}</span></Row>}
            {status.checkout?.environment && <Row label="Environment"><span class="mono">{status.checkout.environment}</span></Row>}
            {connected && (
              <Row label="Checkout">
                {status.checkout
                  ? <><span class="mono truncate" title={status.checkout.dir}>{status.checkout.dir}</span>
                      {uncommitted > 0 && <Pill tone="warn">{uncommitted} uncommitted</Pill>}
                      {unpushed && <Pill tone="warn">not pushed</Pill>}
                      {uncommitted === 0 && !unpushed && <Pill tone="good">pushed</Pill>}</>
                  : <span class="text-muted">not served</span>}
              </Row>
            )}
            {ssh && deployKey && <DeployKeyRows cluster={name} url={flux!.repository!.url} deployKey={deployKey} onChange={setDeployKey} />}
            {sops && <Row label="SOPS recipient" actions={<CopyButton text={sops.recipient} className="btn btn-sm" />}><span class="mono truncate" title={sops.recipient}>{sops.recipient}</span></Row>}
          </dl>
        </div>
      )}
      {connecting && <ConnectApps cluster={name} start={status?.checkout?.dir ?? ''} environment={status?.checkout?.environment ?? ''} connected={connected} onClose={() => setConnecting(false)} />}
    </Section>
  )
}

function DeployKeyRows({ cluster, url, deployKey, onChange }: { cluster: string; url: string; deployKey: DeployKey; onChange: (k: DeployKey) => void }) {
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const github = githubKeysUrl(url)
  const hosts = deployKey.hosts ?? []
  const write = (hostsOnly: boolean) => {
    setBusy(true)
    return api.newDeployKey(cluster, deployKey.hash, hostsOnly)
      .then((k) => { setConfirm(false); onChange(k) })
      .catch((e) => toast(e.message, 'error'))
      .finally(() => setBusy(false))
  }
  return (
    <>
      <Row label="Deploy key" actions={<>
        {deployKey.publicKey && <CopyButton text={deployKey.publicKey} className="btn btn-sm" />}
        {deployKey.publicKey && github && <a href={github} target="_blank" rel="noreferrer" class="btn btn-sm">Add to GitHub ↗</a>}
        <button class="btn btn-sm" disabled={busy} onClick={() => deployKey.publicKey ? setConfirm(true) : write(false)}>{deployKey.publicKey ? 'Replace' : 'Generate'}</button>
      </>}>
        {deployKey.publicKey ? <span class="mono truncate" title={deployKey.fingerprint}>{deployKey.publicKey}</span> : <span class="text-muted">none</span>}
      </Row>
      {hosts.length > 0 && (
        <Row label="Host keys" actions={<button class="btn btn-sm" disabled={busy} onClick={() => write(true)}>Rescan</button>}>
          <span class="mono truncate" title={hosts.map((h) => `${h.type} ${h.fingerprint}`).join('\n')}>{hosts[0].host} · {hosts.map((h) => h.type.replace(/^ssh-|-sha2-nistp256$/g, '')).join(', ')}</span>
        </Row>
      )}
      {confirm && <ConfirmDialog title="Replace deploy key" action="Replace" onClose={() => setConfirm(false)} onConfirm={() => write(false)} />}
    </>
  )
}

function ConnectApps({ cluster, start, environment, connected, onClose }: { cluster: string; start: string; environment: string; connected: boolean; onClose: () => void }) {
  const [choice, setChoice] = useState<AppsChoice>({ ...noApps, dir: start, environment })
  const { repo, error: repoError } = useAppsRepo(choice.dir)
  const [review, setReview] = useState<AppsReview | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const title = connected ? 'Apps repository' : 'Connect apps repository'
  const run = (f: () => Promise<void>) => {
    setBusy(true)
    setError(null)
    f().catch((e) => setError(e.message)).finally(() => setBusy(false))
  }
  const body = appsRequest(choice)
  if (review) {
    return (
      <Dialog title={title} width="max-w-2xl" onClose={onClose} footer={
        <>
          <button class="btn" onClick={() => setReview(null)}>Back</button>
          <button class="btn btn-primary" disabled={busy} onClick={() => run(async () => {
            await writeClusterYaml(cluster, (hash) => api.connectApps(cluster, { ...body!, hash }))
            toast(`${cluster} connected to ${review.repo.display}`, 'good')
            onClose()
          })}>{busy ? 'Writing' : 'Write files'}</button>
        </>
      }>
        <ErrorBox error={error} />
        <AppsReviewBlock review={review} />
      </Dialog>
    )
  }
  return (
    <Dialog title={title} width="max-w-2xl" onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class="btn btn-primary" disabled={busy || !body || !!repoError || !appsReady(choice, repo)} onClick={() => run(async () => setReview(await api.reviewApps(cluster, body!)))}>{busy ? 'Reviewing' : 'Review'}</button>
      </>
    }>
      <ErrorBox error={error} />
      <AppsRepoFields choice={choice} onChange={setChoice} repo={repo} error={repoError} />
    </Dialog>
  )
}
