// One EventSource for the whole app; components subscribe to operation events.
import { useEffect, useState } from 'preact/hooks'
import { getToken, type Event, type Message, type Operation } from './api'

type Listener = (m: Message) => void
const listeners = new Set<Listener>()
let source: EventSource | null = null

function connect() {
  if (source) return
  const t = getToken()
  source = new EventSource('/api/v1/events' + (t ? `?token=${encodeURIComponent(t)}` : ''))
  source.onmessage = (e) => {
    const m = JSON.parse(e.data) as Message
    listeners.forEach((l) => l(m))
  }
  source.onerror = () => {
    source?.close()
    source = null
    setTimeout(connect, 3000)
  }
}

export function subscribe(l: Listener) {
  connect()
  listeners.add(l)
  return () => { listeners.delete(l) }
}

/** Live events of one operation (or all when id is undefined). */
export function useOperationEvents(id?: number) {
  const [events, setEvents] = useState<Event[]>([])
  const [op, setOp] = useState<Operation | undefined>()
  useEffect(() => {
    setEvents([])
    return subscribe((m) => {
      if (id !== undefined && m.operationId !== id) return
      if (m.kind === 'event' && m.event) setEvents((prev) => [...prev, m.event!])
      if (m.kind === 'operation' && m.operation) setOp(m.operation)
    })
  }, [id])
  return { events, op }
}

/** Operation status changes for every operation; used by lists and to refresh views. */
export function useOperationUpdates(onUpdate: (op: Operation) => void) {
  useEffect(() => subscribe((m) => { if (m.kind === 'operation' && m.operation) onUpdate(m.operation) }), [onUpdate])
}
