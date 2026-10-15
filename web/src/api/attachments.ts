import { api } from '@ploykit/client'

// 工单附件（content 域现成后端面的前端封装，零产品后端改动）：
//   POST   /api/upload-file                      multipart（file + host_kind + host_id）
//   GET    /api/work-items/{id}/attachments      {items}
//   GET    /api/attachments/{id}/content         inline 预览流（同源 cookie 即凭证）
//   DELETE /api/attachments/{id}
// 图片 .md 拓展名嗅探 contentTypeFor 已支持 text/markdown（本次补）。

export interface Attachment {
  id: string
  workspace_id: string
  host_kind: string
  host_id?: string
  filename: string
  content_type: string
  size_bytes: number
  sha256: string
  created_by?: string
  created_at: string
}

export async function listWorkItemAttachments(workItemId: string): Promise<Attachment[]> {
  const res = await api.get<{ items: Attachment[] | null }>(
    `/api/work-items/${workItemId}/attachments`,
  )
  return res.items ?? []
}

export async function uploadWorkItemAttachment(
  workItemId: string,
  file: File,
): Promise<Attachment> {
  const form = new FormData()
  form.append('host_kind', 'work_item')
  form.append('host_id', workItemId)
  form.append('file', file)
  return api.post<Attachment>('/api/upload-file', form)
}

export function attachmentContentUrl(id: string): string {
  return `/api/attachments/${id}/content`
}

export function deleteAttachment(id: string): Promise<void> {
  return api.delete<void>(`/api/attachments/${id}`)
}

export function formatAttachmentSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`
  return `${(bytes / 1024 / 1024).toFixed(1)}MB`
}
