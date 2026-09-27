import { computed } from '@preact/signals'
import { useEffect, useRef } from 'preact/hooks'
import { fmt } from '../api'
import { isTyping, overlayOpen } from '../keys'
import { persist } from '../local'
import { operations, running, runningCount } from '../ops'
import { drawerHeight, drawerOpen, drawerTab, setDrawer } from '../store'
import { stateTone } from '../tone'
import { OperationView } from './OperationView'
import { Pill } from './ui'

const heightStyle = computed(() => `height:${drawerHeight.value}px`)

export function ActivityDrawer() {
  const dragging = useRef(false)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || overlayOpen()) return
      const typing = isTyping(e.target)
      if (e.key === 'a' && !e.metaKey && !e.ctrlKey && !e.altKey && !typing) setDrawer(!drawerOpen.value)
      if (e.key !== 'Escape' || !drawerOpen.value) return
      if (typing) (e.target as HTMLElement).blur(); else setDrawer(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    const move = (e: MouseEvent) => {
      if (dragging.current) drawerHeight.value = Math.min(window.innerHeight - 120, Math.max(140, window.innerHeight - e.clientY))
    }
    const up = () => { if (dragging.current) { dragging.current = false; persist('kubit.drawerHeight', drawerHeight.value) } }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    return () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up) }
  }, [])

  if (!drawerOpen.value) return (
    <button class="fixed bottom-3 right-3 z-30 btn" onClick={() => setDrawer(true)} title="Activity (a)">
      Activity {runningCount.value > 0 && <Pill tone="warn">{runningCount.value}</Pill>}
    </button>
  )

  return (
    <div class="fixed left-52 right-0 bottom-0 z-30 flex flex-col bg-panel border-t border-border" style={heightStyle}>
      <div class="h-1.5 cursor-row-resize hover:bg-accent/40" onMouseDown={() => { dragging.current = true }} title="Drag to resize" />
      <DrawerTabs />
    </div>
  )
}

function DrawerTabs() {
  const focused = drawerTab.value !== null ? operations.value.get(drawerTab.value) : undefined
  const tabs = focused && focused.status !== 'running' ? [...running.value, focused].sort((a, b) => a.id - b.id) : running.value
  const active = focused ? focused.id : tabs[0]?.id ?? null
  return (
    <>
      <div class="flex items-center gap-1 px-2 border-b border-border scroll-x">
        <span class="label px-2">Activity</span>
        {tabs.map((o) => (
          <button key={o.id} class={`px-3 py-1.5 text-[13px] whitespace-nowrap border-b-2 -mb-px flex items-center gap-2 ${o.id === active ? 'border-accent text-text' : 'border-transparent text-muted hover:text-text'}`} onClick={() => { drawerTab.value = o.id }}>
            {fmt.kind(o.kind)}{o.cluster ? ` · ${o.cluster}` : ''} #{o.id} <Pill tone={stateTone(o.status)}>{o.status}</Pill>
          </button>
        ))}
        {tabs.length === 0 && <span class="text-[13px] text-muted px-2 py-1.5">No operations running.</span>}
        <div class="ml-auto flex items-center gap-1">
          <a href="/operations" class="btn btn-sm">History</a>
          <button class="btn btn-sm" onClick={() => setDrawer(false)} title="Close (a)">✕</button>
        </div>
      </div>
      {active !== null && <OperationView key={active} id={active} />}
    </>
  )
}
