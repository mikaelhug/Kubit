import { useState } from 'preact/hooks'
import { api } from '../api'
import { toast } from '../store'
import { ConfirmDialog, Notice } from './ui'

export function FluxNotice({ cluster, repo, repoName, missing, reads }: { cluster: string; repo: number; repoName: string; missing: number; reads: boolean }) {
  const [confirm, setConfirm] = useState(false)
  if (reads && missing === 0) return null
  const files = `${missing} file${missing === 1 ? '' : 's'}`
  return (
    <Notice tone="warn">
      <span class="flex flex-wrap items-center gap-2">
        <span>{missing > 0 ? `Flux in ${cluster} can't decrypt ${files} in ${repoName}.` : `Flux in ${cluster} won't be able to decrypt new secrets in ${repoName}.`}</span>
        <button class="btn btn-sm ml-auto" onClick={() => setConfirm(true)}>Let Flux decrypt</button>
      </span>
      {confirm && (
        <ConfirmDialog title="Let Flux decrypt" action="Re-encrypt" onClose={() => setConfirm(false)}
          impact={<span>Adds {cluster}'s Flux key to {repoName}/.sops.yaml{missing > 0 ? ` and re-encrypts ${files}` : ''}.</span>}
          onConfirm={() => api.letFluxDecrypt(cluster, repo).then((r) => { setConfirm(false); toast(`${r.rekeyed} file${r.rekeyed === 1 ? '' : 's'} re-encrypted`, 'good') }).catch((e) => toast(e.message, 'error'))} />
      )}
    </Notice>
  )
}
