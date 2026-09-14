export interface Tab { id: string; label: string; href?: string; badge?: string | number }

/** Horizontal tab strip. With hrefs it is a navigation (cluster sections); otherwise local. */
export function Tabs({ tabs, active, onSelect }: { tabs: Tab[]; active: string; onSelect?: (id: string) => void }) {
  return (
    <div class="flex gap-1 border-b border-border overflow-x-auto" role="tablist">
      {tabs.map((t) => {
        const cls = `px-3 py-2 -mb-px border-b-2 text-[13px] whitespace-nowrap ${t.id === active ? 'border-accent text-text font-medium' : 'border-transparent text-muted hover:text-text'}`
        const inner = <>{t.label}{t.badge !== undefined && t.badge !== 0 && <span class="ml-1.5 rounded-full bg-panel-2 px-1.5 text-[11px] text-muted">{t.badge}</span>}</>
        return t.href
          ? <a key={t.id} href={t.href} class={cls} role="tab" aria-selected={t.id === active}>{inner}</a>
          : <button key={t.id} class={cls} role="tab" aria-selected={t.id === active} onClick={() => onSelect?.(t.id)}>{inner}</button>
      })}
    </div>
  )
}
