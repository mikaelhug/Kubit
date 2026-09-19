import { toasts } from '../store'

export function Toasts() {
  return (
    <div class="fixed top-3 right-3 z-50 flex flex-col gap-2 w-80">
      {toasts.value.map((t) => (
        <div key={t.id} class={`rounded-[var(--r)] border px-3 py-2 text-[13px] bg-panel ${t.tone === 'error' ? 'border-bad/50 text-bad' : t.tone === 'good' ? 'border-good/50 text-good' : 'border-border text-text'}`}>{t.text}</div>
      ))}
    </div>
  )
}
