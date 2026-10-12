import { fmt } from '../api'

export function ReplicasSelect({ value, auto, max, usable, disabled, onChange }: { value: number; auto: number; max: number; usable?: number[]; disabled?: boolean; onChange: (replicas: number) => void }) {
  const counts = Array.from({ length: Math.max(max, value) }, (_, i) => i + 1)
  const size = (r: number) => (usable && r <= usable.length ? ` · ${fmt.bytes(usable[r - 1])}` : '')
  return (
    <label class="inline-flex items-center gap-2 text-[13px]">
      <span class="text-muted">Replicas</span>
      <select class="input !w-auto" value={value} disabled={disabled} onChange={(e) => onChange(Number((e.target as HTMLSelectElement).value))}>
        <option value={0}>Auto ({auto}){size(auto)}</option>
        {counts.map((r) => <option key={r} value={r}>{r}{size(r)}</option>)}
      </select>
    </label>
  )
}
