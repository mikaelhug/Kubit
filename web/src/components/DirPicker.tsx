import { useEffect, useState } from 'preact/hooks'
import { api, type DirListing } from '../api'
import { Dialog, ErrorBox, Field, Pill, inputValue } from './ui'

const join = (dir: string, name: string) => `${dir === '/' ? '' : dir}/${name}`

export function DirPicker({ value, onPick }: { value: string; onPick: (path: string) => void }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button class="btn shrink-0" onClick={() => setOpen(true)}>Browse</button>
      {open && <DirDialog start={value.trim() || '~'} onPick={(p) => { onPick(p); setOpen(false) }} onClose={() => setOpen(false)} />}
    </>
  )
}

function DirDialog({ start, onPick, onClose }: { start: string; onPick: (path: string) => void; onClose: () => void }) {
  const [path, setPath] = useState(start)
  const [list, setList] = useState<DirListing | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [folder, setFolder] = useState('')
  useEffect(() => {
    api.dirs(path)
      .then((l) => { setList(l); setError(null); setFolder('') })
      .catch((e) => { if (!list && path !== '~') setPath('~'); else setError(e.message) })
  }, [path])
  const chosen = list ? (folder.trim() ? join(list.path, folder.trim()) : list.path) : ''
  return (
    <Dialog title="Repo directory" onClose={onClose} footer={
      <>
        <button class="btn" onClick={onClose}>Cancel</button>
        <button class="btn btn-primary" disabled={!chosen} onClick={() => onPick(chosen)}>Choose</button>
      </>
    }>
      <ErrorBox error={error} />
      <div class="flex items-center gap-2">
        <button class="btn btn-sm shrink-0" disabled={!list?.parent} onClick={() => list?.parent && setPath(list.parent)} aria-label="Parent folder">↑</button>
        <span class="mono text-[13px] truncate min-w-0" title={list?.path}>{list?.path ?? path}</span>
      </div>
      <div class="panel divide-y divide-border/60 max-h-[320px] overflow-auto text-[13px]">
        {list && list.dirs.length === 0 && <div class="px-3 py-2 text-muted">No folders.</div>}
        {list?.dirs.map((d) => (
          <button key={d.name} class="w-full flex items-center gap-2 px-3 py-1.5 text-left hover:bg-white/5" onClick={() => setPath(join(list.path, d.name))}>
            <span class="mono truncate min-w-0">{d.name}</span>
            {d.cluster && <Pill tone="info">cluster</Pill>}
            {d.git && <Pill tone="muted">git</Pill>}
          </button>
        ))}
      </div>
      <Field label="New folder" hint="Optional">
        <input class="input mono" value={folder} onInput={(e) => setFolder(inputValue(e))} />
      </Field>
      {folder.trim() && <span class="mono text-[13px] text-muted truncate" title={chosen}>{chosen}</span>}
    </Dialog>
  )
}
