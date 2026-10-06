import { useEffect, useRef } from 'preact/hooks'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { bracketMatching, HighlightStyle, indentUnit, syntaxHighlighting } from '@codemirror/language'
import { EditorState } from '@codemirror/state'
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from '@codemirror/view'
import { tags } from '@lezer/highlight'

const theme = EditorView.theme({
  '&': { backgroundColor: 'var(--bg)', color: 'var(--text)', fontSize: '12px', border: '1px solid var(--border)', borderRadius: 'var(--r)' },
  '&.cm-focused': { outline: 'none', borderColor: 'var(--accent)' },
  '.cm-content': { fontFamily: 'var(--font-mono)', caretColor: 'var(--text)', padding: '8px 0' },
  '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.6' },
  '.cm-gutters': { backgroundColor: 'var(--panel)', color: 'var(--muted)', border: 'none', borderRight: '1px solid var(--border)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent) 6%, transparent)' },
  '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--text)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': { backgroundColor: 'color-mix(in srgb, var(--accent) 25%, transparent) !important' },
  '.cm-cursor': { borderLeftColor: 'var(--text)' },
})

const highlight = HighlightStyle.define([
  { tag: [tags.propertyName, tags.definition(tags.propertyName)], color: 'var(--accent)' },
  { tag: tags.comment, color: 'var(--muted)', fontStyle: 'italic' },
  { tag: [tags.number, tags.bool, tags.null, tags.atom], color: 'var(--good)' },
  { tag: [tags.punctuation, tags.separator, tags.meta], color: 'var(--muted)' },
])

export default function YamlEditor({ value, onChange, onSave }: { value: string; onChange: (v: string) => void; onSave: () => void }) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const handlers = useRef({ onChange, onSave })
  handlers.current = { onChange, onSave }
  useEffect(() => {
    const save = () => { handlers.current.onSave(); return true }
    view.current = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(), highlightActiveLineGutter(), history(), drawSelection(), highlightActiveLine(), bracketMatching(),
          indentUnit.of('  '), EditorState.tabSize.of(2), yaml(), syntaxHighlighting(highlight), theme,
          keymap.of([{ key: 'Mod-s', run: save, preventDefault: true }, indentWithTab, ...defaultKeymap, ...historyKeymap]),
          EditorView.updateListener.of((u) => { if (u.docChanged) handlers.current.onChange(u.state.doc.toString()) }),
        ],
      }),
    })
    return () => view.current?.destroy()
  }, [])
  useEffect(() => {
    const v = view.current
    if (v && v.state.doc.toString() !== value) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } })
  }, [value])
  return <div ref={host} />
}
