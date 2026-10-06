import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { CopyButton, ErrorBox } from './ui'

function errorText(body: string) {
  try { return JSON.parse(body).error ?? body } catch { return body }
}

export function LogStream({ url, follow, onFollow, toolbar, height = '!max-h-[60vh]' }: { url: string; follow: boolean; onFollow: (f: boolean) => void; toolbar?: ComponentChildren; height?: string }) {
  const [log, setLog] = useState('')
  const [q, setQ] = useState('')
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    const ac = new AbortController()
    setLog('')
    setError(null)
    fetch(url, { signal: ac.signal }).then(async (res) => {
      if (!res.ok) throw new Error(errorText(await res.text()) || res.statusText)
      const reader = res.body!.getReader()
      const dec = new TextDecoder()
      for (;;) {
        const { value, done } = await reader.read()
        const text = dec.decode(value, { stream: !done })
        if (text) setLog((prev) => (prev + text).slice(-300000))
        if (done) break
      }
    }).catch((e) => { if (e.name !== 'AbortError') setError(e.message) })
    return () => ac.abort()
  }, [url])
  const lines = q ? log.split('\n').filter((l) => l.toLowerCase().includes(q.toLowerCase())).join('\n') : log
  return (
    <div class="flex flex-col gap-2 min-w-0">
      <div class="flex items-center gap-2">
        {toolbar}
        <input class="input !w-56" placeholder="Search" value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        <label class="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={follow} onChange={(e) => onFollow((e.target as HTMLInputElement).checked)} /> Follow</label>
        <CopyButton text={() => lines} className="btn ml-auto" />
      </div>
      <ErrorBox error={error} />
      <pre class={`log ${height}`} ref={(el) => { if (el && follow) el.scrollTop = el.scrollHeight }}>{lines || (error ? '' : 'Loading')}</pre>
    </div>
  )
}
