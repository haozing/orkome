import { EditorContent, useEditor } from '@tiptap/react'
import type { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { act, render, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import {
  createMentionExtension,
  normalizeMentionLinks,
  setMentionItemsProvider,
  type MentionItem,
} from '@/components/editor/mention'
const ITEMS: MentionItem[] = [
  { id: 'u-1', label: 'Alice Zhang', type: 'user' },
  { id: 'a-1', label: 'writer', type: 'agent' },
]
function EditorHarness({ onReady }: { onReady?: (ed: Editor) => void }) {
  const editor = useEditor({
    extensions: [
      StarterKit.configure({ heading: { levels: [2, 3] } }),
      Markdown,
      createMentionExtension(),
    ],
    content: '<p></p>',
    contentType: 'markdown',
    onCreate: ({ editor: ed }) => {
      if (ed.markdown) {
        const mgr = ed.markdown
        ed.extensionManager.baseExtensions.forEach((ext) => mgr.registerExtension(ext))
      }
      normalizeMentionLinks(ed)
    },
  })
  if (editor && onReady) onReady(editor)
  return <EditorContent editor={editor} />
}
function renderHarness(capture: (ed: Editor) => void) {
  return render(<EditorHarness onReady={capture} />)
}
function popupButtons(): string[] {
  return Array.prototype.map.call(
    Array.prototype.filter.call(document.querySelectorAll('div.react-renderer'), (d) =>
      d.querySelector('button'),
    ),
    (d) => d.textContent?.trim(),
  ) as string[]
}
describe('mention suggestion (full React mount)', () => {
  it('typing @ renders popup candidates (members + agents)', async () => {
    setMentionItemsProvider(() => ITEMS)
    let ed: unknown = null
    renderHarness((e) => {
      ed = e
    })
    await waitFor(() => expect(ed).not.toBeNull())
    act(() => {
      ;(
        ed as {
          commands: {
            insertContent: (s: string) => void
          }
        }
      ).commands.insertContent('@')
    })
    await waitFor(() => {
      const btns = popupButtons()
      expect(btns.join('|')).toContain('writer')
    })
    const all = popupButtons().join('|')
    expect(all).toContain('Alice Zhang')
    expect(all).toContain('writer')
  })
  it('items filtered by query with cap 8', async () => {
    const many: MentionItem[] = Array.from({ length: 12 }, (_, i) => ({
      id: 'u-' + i,
      label: 'user' + i,
      type: 'user' as const,
    }))
    setMentionItemsProvider(() => many)
    let ed: unknown = null
    renderHarness((e) => {
      ed = e
    })
    await waitFor(() => expect(ed).not.toBeNull())
    act(() => {
      ;(
        ed as {
          commands: {
            insertContent: (s: string) => void
          }
        }
      ).commands.insertContent('@user1')
    })
    await waitFor(() => {
      const btns = popupButtons()
      expect(btns.length).toBeGreaterThanOrEqual(1)
      expect(btns.length).toBeLessThanOrEqual(8)
    })
    expect(popupButtons().join('|')).not.toContain('user2')
  })
  it('mention insert → getMarkdown serializes to mention:// syntax', async () => {
    setMentionItemsProvider(() => ITEMS)
    let ed: Editor | null = null
    renderHarness((e) => {
      ed = e
    })
    await waitFor(() => expect(ed).not.toBeNull())
    const editor = ed as unknown as Editor
    const WRITER_ID = 'a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6'
    act(() => {
      editor
        .chain()
        .focus()
        .insertContentAt(1, [
          { type: 'mention', attrs: { id: WRITER_ID, label: 'writer', type: 'agent' } },
          { type: 'text', text: ' work' },
        ])
        .run()
    })
    expect(editor.getMarkdown()).toBe(`[@writer](mention://agent/${WRITER_ID}) work`)
  })
})
