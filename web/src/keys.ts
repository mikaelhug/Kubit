import { useEffect, useRef } from 'preact/hooks'

export const isTyping = (t: EventTarget | null) => t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || t instanceof HTMLSelectElement || (t instanceof HTMLElement && t.isContentEditable)

const overlays: object[] = []

export const overlayOpen = () => overlays.length > 0

export function useEscape(onClose: () => void) {
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    const token = {}
    overlays.push(token)
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || overlays[overlays.length - 1] !== token) return
      e.preventDefault()
      close.current()
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      overlays.splice(overlays.indexOf(token), 1)
    }
  }, [])
}
