import type { ComponentChildren } from 'preact'
import type { Tone } from '../tone'
import { tonePill } from '../tone'

interface Tab { id: string; label: string; href?: string; badge?: string | number; tone?: Tone }

export function Tabs({ tabs, active, onSelect, actions }: { tabs: Tab[]; active: string; onSelect?: (id: string) => void; actions?: ComponentChildren }) {
  return (
    <div class="flex items-end gap-1 border-b border-border overflow-x-auto overflow-y-hidden no-scrollbar" role="tablist">
      {tabs.map((t) => {
        const cls = `px-3 py-1.5 -mb-px border-b-2 text-[12.5px] whitespace-nowrap ${t.id === active ? 'border-accent text-text font-medium' : 'border-transparent text-muted hover:text-text'}`
        const inner = <>{t.label}{t.badge !== undefined && t.badge !== 0 && <span class={`ml-1.5 rounded-[var(--r-sm)] px-1.5 text-[10.5px] ${t.tone ? tonePill[t.tone] : 'bg-panel-2 text-muted'}`}>{t.badge}</span>}</>
        return t.href
          ? <a key={t.id} href={t.href} class={cls} role="tab" aria-selected={t.id === active}>{inner}</a>
          : <button key={t.id} class={cls} role="tab" aria-selected={t.id === active} onClick={() => onSelect?.(t.id)}>{inner}</button>
      })}
      {actions && <span class="ml-auto flex items-center gap-2 pb-1.5">{actions}</span>}
    </div>
  )
}
