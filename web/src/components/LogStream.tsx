import { useEffect, useRef, useState } from 'preact/hooks'
import { toast } from '../store'

/** Streams a text log from a URL into a scrolling pane with search, follow and copy. */
export function LogStream({ url, follow, onFollow, className = '' }: { url: string; follow: boolean; onFollow: (f: boolean) => void; className?: string }) {
  const [log, setLog] = useState('')
  const [q, setQ] = useState('')
  const [error, setError] = useState<string | null>(null)
  const abort = useRef<AbortController | null>(null)
  useEffect(() => {
    abort.current?.abort()
    const ac = new AbortController()
    abort.current = ac
    setLog('')
    setError(null)
    fetch(url, { signal: ac.signal }).then(async (res) => {
      if (!res.ok) throw new Error(await res.text())
      const reader = res.body!.getReader()
      const dec = new TextDecoder()
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        setLog((prev) => (prev + dec.decode(value)).slice(-300000))
      }
    }).catch((e) => { if (e.name !== 'AbortError') setError(e.message) })
    return () => ac.abort()
  }, [url])
  const lines = q ? log.split('\n').filter((l) => l.toLowerCase().includes(q.toLowerCase())).join('\n') : log
  return (
    <div class={`flex flex-col gap-2 min-w-0 ${className}`}>
      <div class="flex items-center gap-2">
        <input class="input !w-56" placeholder="Search…" value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        <label class="flex items-center gap-2 text-[13px]"><input type="checkbox" checked={follow} onChange={(e) => onFollow((e.target as HTMLInputElement).checked)} /> Follow</label>
        <button class="btn ml-auto" onClick={() => navigator.clipboard.writeText(lines).then(() => toast('Copied'))}>Copy</button>
      </div>
      {error && <div class="text-bad text-[13px]">{error}</div>}
      <pre class="log !max-h-[60vh]" ref={(el) => { if (el && follow) el.scrollTop = el.scrollHeight }}>{lines || (error ? '' : 'Loading…')}</pre>
    </div>
  )
}
