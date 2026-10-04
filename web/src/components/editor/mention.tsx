/* eslint-disable react-refresh/only-export-components -- editor infra: component and extension factory in one file */
import i18n from '@/i18n'
import Mention from '@tiptap/extension-mention'
import { ReactRenderer } from '@tiptap/react'
import { type SuggestionProps } from '@tiptap/suggestion'
import type { Editor } from '@tiptap/core'
import { forwardRef, useImperativeHandle, useRef, useState, useEffect } from 'react'
export interface MentionItem {
  id: string
  label: string
  type: 'user' | 'agent'
}
export interface MentionNodeAttrs {
  id: string
  label: string
  type: MentionItem['type']
}
export const MENTION_SYNTAX =
  /\[@([^\]]+)\]\(mention:\/\/(user|agent|squad|work_item|all)\/([0-9a-fA-F-]{36}|)\)/g
export function mentionToMarkdown(attrs: MentionNodeAttrs): string {
  return `[@${attrs.label}](mention://${attrs.type}/${attrs.id})`
}
interface SuggestionListHandle {
  onKeyDown: (props: { event: KeyboardEvent }) => boolean
}
const SuggestionList = forwardRef<
  SuggestionListHandle,
  SuggestionProps<MentionItem> & {
    labelOf: (item: MentionItem) => string
  }
>(function SuggestionList(props, ref) {
  const [index, setIndex] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)
  console.log(
    '[SL] render items.length =',
    props.items.length,
    'query =',
    JSON.stringify(props.query),
  )
  useEffect(() => {
    setIndex(0)
  }, [props.items])
  useEffect(() => {
    listRef.current?.children[index]?.scrollIntoView({ block: 'nearest' })
  }, [index])
  useImperativeHandle(ref, () => ({
    onKeyDown: ({ event }) => {
      if (event.key === 'ArrowDown') {
        setIndex((i) => (i + 1) % props.items.length)
        return true
      }
      if (event.key === 'ArrowUp') {
        setIndex((i) => (i - 1 + props.items.length) % props.items.length)
        return true
      }
      if (event.key === 'Enter') {
        const item = props.items[index]
        if (item) props.command(item)
        return true
      }
      return false
    },
  }))
  if (!props.items.length) return null
  return (
    <div
      ref={listRef}
      className="fixed z-50 max-h-56 min-w-44 overflow-auto rounded-md border border-zinc-200 bg-white p-1 shadow-lg dark:border-zinc-700 dark:bg-zinc-900"
    >
      {props.items.map((item, i) => (
        <button
          key={item.type + item.id}
          type="button"
          onClick={() => props.command(item)}
          className={
            'flex w-full cursor-default items-center gap-1.5 rounded px-2 py-1.5 text-left text-sm outline-none ' +
            (i === index ? 'bg-zinc-100 dark:bg-zinc-800' : '')
          }
        >
          <span
            className={
              'rounded px-1 text-xs ' +
              (item.type === 'agent'
                ? 'bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300'
                : 'bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-300')
            }
          >
            {item.type === 'agent' ? i18n.t('nav.agents') : i18n.t('agents.member')}
          </span>
          {props.labelOf(item)}
        </button>
      ))}
    </div>
  )
})
let mentionItemsProvider: (() => MentionItem[]) | null = null
export function setMentionItemsProvider(fn: (() => MentionItem[]) | null): void {
  mentionItemsProvider = fn
  ;(
    globalThis as {
      __mentionProviderSet?: boolean
    }
  ).__mentionProviderSet = fn !== null
}
export function createMentionExtension(
  options: {
    labelOf?: (item: MentionItem) => string
  } = {},
) {
  const labelOf = options.labelOf ?? ((i: MentionItem) => i.label)
  return Mention.extend({
    renderMarkdown(node) {
      const attrs = node?.attrs as MentionNodeAttrs | undefined
      if (!attrs?.label) return ''
      return mentionToMarkdown(attrs)
    },
    addAttributes() {
      return {
        id: { default: null },
        label: { default: null },
        type: { default: 'user' },
      }
    },
    renderHTML({ node }) {
      const attrs = node.attrs as MentionNodeAttrs
      return [
        'span',
        {
          'data-mention': attrs.type,
          'data-id': attrs.id,
          class:
            'rounded bg-sky-100 px-1 py-0.5 text-sm text-sky-700 dark:bg-sky-950 dark:text-sky-300',
        },
        `@${attrs.label}`,
      ]
    },
    renderText({ node }) {
      return `@${(node.attrs as MentionNodeAttrs).label}`
    },
  }).configure({
    suggestion: {
      char: '@',
      render: () => {
        let component: ReactRenderer | null = null
        let popup: HTMLElement | null = null
        const position = (props: SuggestionProps<MentionItem>) => {
          if (!popup) return
          const rect = props.clientRect?.()
          if (!rect) return
          popup.style.left = `${rect.left}px`
          popup.style.top = `${rect.bottom + 6}px`
        }
        return {
          onStart: (props) => {
            component = new ReactRenderer(SuggestionList, {
              props: { ...props, labelOf },
              editor: props.editor,
            })
            popup = component.element as HTMLElement
            popup.style.position = 'fixed'
            popup.style.zIndex = '50'
            position(props)
            document.body.appendChild(popup)
          },
          onUpdate: (props) => {
            component?.updateProps({ ...props, labelOf })
            position(props)
          },
          onKeyDown: (props) => {
            const handle = component?.ref as SuggestionListHandle | null
            return handle?.onKeyDown(props) ?? false
          },
          onExit: () => {
            popup?.remove()
            component?.destroy()
            popup = null
            component = null
          },
        }
      },
      items: ({ query }) => {
        const items = mentionItemsProvider?.() ?? []
        const q = query.toLowerCase()
        return items.filter((i) => i.label.toLowerCase().includes(q)).slice(0, 8)
      },
      command: ({ editor, range, props }) => {
        const item = props as MentionItem
        editor
          .chain()
          .focus()
          .insertContentAt(range, [
            {
              type: 'mention',
              attrs: { id: item.id, label: item.label, type: item.type },
            },
            { type: 'text', text: ' ' },
          ])
          .run()
      },
    },
  })
}
export function normalizeMentionLinks(ed: Editor): void {
  const nodeType = ed.schema.nodes.mention
  if (!nodeType) return
  const ranges: Array<{
    from: number
    to: number
    attrs: Record<string, string>
  }> = []
  ed.state.doc.descendants((node, pos) => {
    const link = node.marks.find((m) => m.type.name === 'link')
    if (!link) return
    const m = /^mention:\/\/(user|agent|squad|work_item|all)\/([0-9a-fA-F-]{36}|)$/.exec(
      String(link.attrs.href ?? ''),
    )
    if (m) {
      ranges.push({
        from: pos,
        to: pos + node.nodeSize,
        attrs: { id: m[2]!, label: node.text ?? m[2]!, type: m[1]! },
      })
    }
  })
  for (const r of ranges.reverse()) {
    ed.chain()
      .insertContentAt({ from: r.from, to: r.to }, { type: 'mention', attrs: r.attrs })
      .run()
  }
}
