import type { ComponentChildren } from 'preact'
import { EmptyState } from '../../components/ui'

export function Placeholder({ title, milestone, children }: { title: string; milestone: string; children: ComponentChildren }) {
  return <EmptyState title={`${title} — not built yet${milestone ? ` (${milestone})` : ''}`}>{children}</EmptyState>
}
