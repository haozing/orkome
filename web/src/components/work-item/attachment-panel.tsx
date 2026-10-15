import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileText, Paperclip, Trash2, Upload } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import {
  attachmentContentUrl,
  deleteAttachment,
  formatAttachmentSize,
  listWorkItemAttachments,
  uploadWorkItemAttachment,
  type Attachment,
} from '@/api/attachments'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

// 工单详情侧栏的附件区：上传（图片 + md 文档）/ 缩略预览 / 删除。
// 后端面全现成（content 域），本组件只做消费。图片走 /content inline 流
// （同源 cookie 即凭证）；md 文件点开新窗口查看（浏览器原生渲染 text/markdown
// 不保证，新窗口由服务端 disposition 决定下载或预览）。

const ACCEPT = 'image/png,image/jpeg,image/gif,image/svg+xml,image/webp,.md,.markdown'

function isImage(a: Attachment): boolean {
  return a.content_type.startsWith('image/')
}

function isMarkdown(a: Attachment): boolean {
  return a.content_type === 'text/markdown'
}

export function AttachmentPanel({ workItemId }: { workItemId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const inputRef = useRef<HTMLInputElement>(null)
  const [error, setError] = useState<string | null>(null)
  const [uploading, setUploading] = useState(0)

  const list = useQuery({
    queryKey: ['work-item-attachments', workItemId],
    queryFn: () => listWorkItemAttachments(workItemId),
  })

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: ['work-item-attachments', workItemId] })

  const upload = useMutation({
    mutationFn: async (files: File[]) => {
      for (const f of files) {
        await uploadWorkItemAttachment(workItemId, f)
      }
    },
    onMutate: (files) => {
      setUploading((n) => n + files.length)
    },
    onSettled: () => {
      setUploading(0)
      invalidate()
    },
    onError: (err) => {
      setError(err instanceof ApiError ? err.message : t('attachments.uploadFailed'))
    },
  })

  const remove = useMutation({
    mutationFn: (id: string) => deleteAttachment(id),
    onSuccess: invalidate,
    onError: (err) => {
      setError(err instanceof ApiError ? err.message : t('attachments.deleteFailed'))
    },
  })

  const pick = (files: FileList | null) => {
    setError(null)
    if (!files || files.length === 0) return
    upload.mutate(Array.from(files))
  }

  const items = list.data ?? []

  return (
    <section className="space-y-2" data-testid="attachment-panel">
      <div className="flex items-center justify-between">
        <p className="flex items-center gap-1.5 text-sm font-medium">
          <Paperclip className="size-3.5 text-zinc-400" aria-hidden="true" />
          {t('attachments.title')}
        </p>
        <input
          ref={inputRef}
          type="file"
          multiple
          accept={ACCEPT}
          className="hidden"
          onChange={(e) => {
            pick(e.target.files)
            e.target.value = ''
          }}
        />
        <Button
          size="xs"
          variant="outline"
          disabled={uploading > 0}
          onClick={() => inputRef.current?.click()}
        >
          <Upload className="size-3" aria-hidden="true" />
          {uploading > 0 ? t('attachments.uploading') : t('attachments.upload')}
        </Button>
      </div>

      {items.length === 0 && !list.isPending && (
        <p className="text-xs text-zinc-400 dark:text-zinc-500">{t('attachments.empty')}</p>
      )}

      {items.length > 0 && (
        <ul className="divide-y divide-zinc-100 dark:divide-zinc-800">
          {items.map((a) => (
            <AttachmentRow
              key={a.id}
              attachment={a}
              deleting={remove.isPending && remove.variables === a.id}
              onDelete={() => remove.mutate(a.id)}
            />
          ))}
        </ul>
      )}

      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
    </section>
  )
}

function AttachmentRow({
  attachment: a,
  deleting,
  onDelete,
}: {
  attachment: Attachment
  deleting: boolean
  onDelete: () => void
}) {
  const { t } = useTranslation()
  const img = isImage(a)
  const md = isMarkdown(a)
  const open = () => window.open(attachmentContentUrl(a.id), '_blank')
  return (
    <li className="flex items-center gap-2 py-2">
      {img ? (
        <button
          type="button"
          onClick={open}
          className="size-10 shrink-0 overflow-hidden rounded-md border border-zinc-200 outline-none hover:ring-2 hover:ring-ring/40 dark:border-zinc-700"
          aria-label={t('attachments.previewAria', { name: a.filename })}
        >
          <img
            src={attachmentContentUrl(a.id)}
            alt={a.filename}
            className="size-full object-cover"
            loading="lazy"
          />
        </button>
      ) : (
        <button
          type="button"
          onClick={open}
          className="flex size-10 shrink-0 items-center justify-center rounded-md border border-zinc-200 outline-none hover:ring-2 hover:ring-ring/40 dark:border-zinc-700"
          aria-label={t('attachments.previewAria', { name: a.filename })}
        >
          <FileText className="size-4 text-zinc-400" aria-hidden="true" />
        </button>
      )}
      <div className="min-w-0 flex-1">
        <button
          type="button"
          onClick={open}
          className="block max-w-full truncate text-left text-sm outline-none hover:underline"
          title={a.filename}
        >
          {a.filename}
        </button>
        <p className="text-xs text-zinc-400 dark:text-zinc-500">
          {formatAttachmentSize(a.size_bytes)}
        </p>
      </div>
      {md && <Badge variant="outline">md</Badge>}
      <Button
        variant="ghost"
        size="icon-xs"
        className="text-zinc-400 hover:text-red-600 dark:hover:text-red-400"
        disabled={deleting}
        aria-label={t('attachments.deleteAria', { name: a.filename })}
        onClick={onDelete}
      >
        <Trash2 className="size-3.5" aria-hidden="true" />
      </Button>
    </li>
  )
}
