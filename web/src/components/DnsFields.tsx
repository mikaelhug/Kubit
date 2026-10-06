import { Field } from './ui'

export type DnsPair = [string, string]

export const dnsPair = (list?: string[]): DnsPair => [list?.[0] ?? '', list?.[1] ?? '']
export const dnsList = (pair: DnsPair) => pair.map((s) => s.trim()).filter(Boolean)

export function DnsFields({ value, onChange, onBlur, defaults }: { value: DnsPair; onChange: (v: DnsPair) => void; onBlur?: () => void; defaults?: string[] }) {
  const input = (i: 0 | 1) => (
    <input class="input mono" value={value[i]} placeholder={defaults?.[i] ?? (i === 1 ? 'Optional' : '')} onBlur={onBlur}
      onInput={(e) => { const next: DnsPair = [...value]; next[i] = (e.target as HTMLInputElement).value; onChange(next) }} />
  )
  return (
    <>
      <Field label="Primary DNS">{input(0)}</Field>
      <Field label="Secondary DNS">{input(1)}</Field>
    </>
  )
}
