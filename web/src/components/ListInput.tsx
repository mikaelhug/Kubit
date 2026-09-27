import { useEffect, useState } from 'preact/hooks'
import { splitList } from '../api'
import { joinList, syncListText } from '../list'

export function ListInput({ value, onChange, class: cls = 'input mono', placeholder, disabled, list, label }: { value?: string[]; onChange: (v: string[]) => void; class?: string; placeholder?: string; disabled?: boolean; list?: string; label?: string }) {
  const [text, setText] = useState(() => joinList(value))
  const key = joinList(value)
  useEffect(() => { setText((t) => syncListText(t, value)) }, [key])
  return <input class={cls} value={text} placeholder={placeholder} disabled={disabled} list={list} aria-label={label} onInput={(e) => { const t = (e.target as HTMLInputElement).value; setText(t); onChange(splitList(t)) }} />
}
