import { useEffect, useState } from 'preact/hooks'
import { kvLines, parseKV, syncKVText } from '../list'

export function KVEditor({ value, onChange, placeholder, mono = true }: { value?: Record<string, string>; onChange: (v: Record<string, string> | undefined) => void; placeholder?: string; mono?: boolean }) {
  const [text, setText] = useState(() => kvLines(value))
  const key = JSON.stringify(value ?? {})
  useEffect(() => { setText((t) => syncKVText(t, value)) }, [key])
  const commit = (t: string) => { setText(t); onChange(parseKV(t)) }
  return <textarea class={`input !text-[12px] min-h-[56px] ${mono ? 'mono' : ''}`} rows={Math.max(2, text.split('\n').length)} value={text} placeholder={placeholder} spellcheck={false} onInput={(e) => commit((e.target as HTMLTextAreaElement).value)} />
}
