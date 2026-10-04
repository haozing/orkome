import { EditorContent, useEditor, useEditorState } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import Placeholder from '@tiptap/extension-placeholder'
import {
  Bold,
  Italic,
  Strikethrough,
  Code,
  Heading2,
  Heading3,
  List,
  ListOrdered,
  Quote,
  Undo2,
  Redo2,
} from 'lucide-react'
import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  createMentionExtension,
  normalizeMentionLinks,
  setMentionItemsProvider,
  type MentionItem,
} from '@/components/editor/mention'
export interface RichEditorProps {
  content: string
  onChange: (markdown: string) => void
  onBlur?: () => void
  onKeyDown?: (e: KeyboardEvent) => void
  placeholder?: string
  autoFocus?: boolean
  ariaLabel?: string
  className?: string
  minRows?: number
  bubbleMenu?: boolean
  mentionItems?: () => MentionItem[]
}
type Editor = NonNullable<ReturnType<typeof useEditor>>
export function RichEditor({
  content,
  onChange,
  onBlur,
  onKeyDown,
  placeholder,
  autoFocus,
  ariaLabel,
  className,
  minRows = 5,
  bubbleMenu = true,
  mentionItems,
}: RichEditorProps) {
  const { t } = useTranslation()
  const onChangeRef = useRef(onChange)
  useEffect(() => {
    onChangeRef.current = onChange
  })
  const onBlurRef = useRef(onBlur)
  useEffect(() => {
    onBlurRef.current = onBlur
  })
  const onKeyDownRef = useRef(onKeyDown)
  useEffect(() => {
    onKeyDownRef.current = onKeyDown
  })
  useEffect(() => {
    setMentionItemsProvider(mentionItems ?? null)
    return () => {
      if (mentionItems !== undefined) setMentionItemsProvider(null)
    }
  }, [mentionItems])
  const withMention = mentionItems !== undefined
  const extensions = useMemo(
    () => [
      StarterKit.configure({ heading: { levels: [2, 3] } }),
      Markdown,
      Placeholder.configure({ placeholder: placeholder ?? '' }),
      ...(withMention ? [createMentionExtension()] : []),
    ],
    [placeholder, withMention],
  )
  const editor = useEditor({
    extensions,
    content,
    contentType: 'markdown',
    autofocus: autoFocus ? 'end' : false,
    editorProps: {
      attributes: {
        'aria-label': ariaLabel ?? t('editor.aria'),
        class: cn('rich-editor-content outline-none', className),
        style: `min-height: ${minRows * 1.75}rem`,
      },
      handleKeyDown: (_view, event) => {
        onKeyDownRef.current?.(event)
        return false
      },
    },
    onUpdate: ({ editor: ed }) => {
      onChangeRef.current(ed.getMarkdown())
    },
    onCreate: ({ editor: ed }) => {
      if (ed.markdown) {
        const mgr = ed.markdown
        ed.extensionManager.baseExtensions.forEach((ext) => mgr.registerExtension(ext))
      }
      normalizeMentionLinks(ed)
    },
    onBlur: () => {
      onBlurRef.current?.()
    },
  })
  const editorRef = editor
  const contentRef = useRef(content)
  useEffect(() => {
    if (editorRef && contentRef.current !== content && content === '') {
      editorRef.commands.clearContent()
    }
    contentRef.current = content
  }, [content, editorRef])
  if (!editor) return null
  return (
    <div>
      <EditorToolbar editor={editor} />
      <EditorContent editor={editor} />
      {bubbleMenu && (
        <BubbleMenu
          editor={editor}
          options={{ placement: 'top', offset: 8 }}
          shouldShow={({ editor: ed, state }) => ed.isFocused && !state.selection.empty}
        >
          <SelectionToolbar editor={editor} />
        </BubbleMenu>
      )}
    </div>
  )
}
interface ToolbarState {
  bold: boolean
  italic: boolean
  strike: boolean
  code: boolean
  h2: boolean
  h3: boolean
  bulletList: boolean
  orderedList: boolean
  blockquote: boolean
  canUndo: boolean
  canRedo: boolean
}
function useToolbarState(editor: Editor): ToolbarState {
  return useEditorState({
    editor,
    selector: ({ editor: ed }) => ({
      bold: ed.isActive('bold'),
      italic: ed.isActive('italic'),
      strike: ed.isActive('strike'),
      code: ed.isActive('code'),
      h2: ed.isActive('heading', { level: 2 }),
      h3: ed.isActive('heading', { level: 3 }),
      bulletList: ed.isActive('bulletList'),
      orderedList: ed.isActive('orderedList'),
      blockquote: ed.isActive('blockquote'),
      canUndo: ed.can().undo(),
      canRedo: ed.can().redo(),
    }),
  })
}
const BTN =
  'flex size-7 cursor-default items-center justify-center rounded text-zinc-600 outline-none hover:bg-zinc-100 disabled:pointer-events-none disabled:opacity-40 dark:text-zinc-300 dark:hover:bg-zinc-800'
const BTN_ACTIVE =
  'bg-zinc-900 text-zinc-50 hover:bg-zinc-700 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-300'
function ToolbarButton({
  active,
  onClick,
  label,
  disabled,
  children,
}: {
  active: boolean
  onClick: () => void
  label: string
  disabled?: boolean
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={active}
      disabled={disabled}
      onClick={onClick}
      onMouseDown={(e) => {
        e.preventDefault()
      }}
      className={cn(BTN, active && BTN_ACTIVE)}
    >
      {children}
    </button>
  )
}
function Separator() {
  return <span className="mx-1 h-5 w-px bg-zinc-200 dark:bg-zinc-700" aria-hidden="true" />
}
function EditorToolbar({ editor }: { editor: Editor }) {
  const { t } = useTranslation()
  const s = useToolbarState(editor)
  return (
    <div className="mb-1.5 flex flex-wrap items-center gap-0.5 border-b border-zinc-100 pb-1.5 dark:border-zinc-800">
      <ToolbarButton
        active={s.bold}
        onClick={() => editor.chain().focus().toggleBold().run()}
        label={t('editor.bold') + ' (Ctrl+B)'}
      >
        <Bold className="size-4" />
      </ToolbarButton>
      <ToolbarButton
        active={s.italic}
        onClick={() => editor.chain().focus().toggleItalic().run()}
        label={t('editor.italic') + ' (Ctrl+I)'}
      >
        <Italic className="size-4" />
      </ToolbarButton>
      <ToolbarButton
        active={s.strike}
        onClick={() => editor.chain().focus().toggleStrike().run()}
        label={t('editor.strike')}
      >
        <Strikethrough className="size-4" />
      </ToolbarButton>
      <ToolbarButton
        active={s.code}
        onClick={() => editor.chain().focus().toggleCode().run()}
        label={t('editor.inlineCode')}
      >
        <Code className="size-4" />
      </ToolbarButton>
      <Separator />
      <ToolbarButton
        active={s.h2}
        onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
        label={t('editor.heading')}
      >
        <Heading2 className="size-4" />
      </ToolbarButton>
      <ToolbarButton
        active={s.h3}
        onClick={() => editor.chain().focus().toggleHeading({ level: 3 }).run()}
        label={t('editor.subheading')}
      >
        <Heading3 className="size-4" />
      </ToolbarButton>
      <ToolbarButton
        active={s.bulletList}
        onClick={() => editor.chain().focus().toggleBulletList().run()}
        label={t('editor.bulletList')}
      >
        <List className="size-4" />
      </ToolbarButton>
      <ToolbarButton
        active={s.orderedList}
        onClick={() => editor.chain().focus().toggleOrderedList().run()}
        label={t('editor.orderedList')}
      >
        <ListOrdered className="size-4" />
      </ToolbarButton>
      <ToolbarButton
        active={s.blockquote}
        onClick={() => editor.chain().focus().toggleBlockquote().run()}
        label={t('editor.blockquote')}
      >
        <Quote className="size-4" />
      </ToolbarButton>
      <Separator />
      <ToolbarButton
        active={false}
        disabled={!s.canUndo}
        onClick={() => editor.chain().focus().undo().run()}
        label={t('editor.undo')}
      >
        <Undo2 className="size-4" />
      </ToolbarButton>
      <ToolbarButton
        active={false}
        disabled={!s.canRedo}
        onClick={() => editor.chain().focus().redo().run()}
        label={t('editor.redo')}
      >
        <Redo2 className="size-4" />
      </ToolbarButton>
    </div>
  )
}
function SelectionToolbar({ editor }: { editor: Editor }) {
  const { t } = useTranslation()
  const s = useToolbarState(editor)
  return (
    <div className="flex items-center gap-0.5 rounded-md border border-zinc-200 bg-white p-1 shadow-lg dark:border-zinc-700 dark:bg-zinc-900">
      <ToolbarButton
        active={s.bold}
        onClick={() => editor.chain().focus().toggleBold().run()}
        label={t('editor.bold')}
      >
        <Bold className="size-3.5" />
      </ToolbarButton>
      <ToolbarButton
        active={s.italic}
        onClick={() => editor.chain().focus().toggleItalic().run()}
        label={t('editor.italic')}
      >
        <Italic className="size-3.5" />
      </ToolbarButton>
      <ToolbarButton
        active={s.strike}
        onClick={() => editor.chain().focus().toggleStrike().run()}
        label={t('editor.strike')}
      >
        <Strikethrough className="size-3.5" />
      </ToolbarButton>
      <ToolbarButton
        active={s.code}
        onClick={() => editor.chain().focus().toggleCode().run()}
        label={t('editor.inlineCode')}
      >
        <Code className="size-3.5" />
      </ToolbarButton>
    </div>
  )
}
