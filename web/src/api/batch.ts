import { api } from '@ploykit/client'

// 批量处理（batch）前端数据封装。后端契约：
//  - handler: internal/orchestration/adapters/http/handlers_batch.go
//  - 路由:   internal/orchestration/adapters/http/routes.go:117-124
//  - 类型:   internal/orchestration/app/batch.go（Batch / BatchItem，json tag 以此为准）
//  - 状态枚举: internal/orchestration/domain/batch.go
// workspace 经 X-Workspace-Id 头传递（apiFetch 的 workspaceIdProvider 自动附带，
// 与 listPackEntries 同机制，不需要手动设）。

/**
 * 批次（app.Batch）。status ∈ pending | running | paused |
 * completed | failed | cancelled（domain/batch.go 常量）。
 */
export interface Batch {
  id: string
  workspace_id: string
  work_item_id: string
  /** 处理器类型，v0 仅 llm_comment（BatchProcessor.Kind()） */
  kind: string
  status: string
  params: Record<string, unknown> | null
  source: Record<string, unknown> | null
  /** 物料总数 = 创建时 payloads 长度 */
  total: number
  succeeded: number
  failed: number
  skipped: number
  concurrency: number
  chunk_size: number
  pace_interval_seconds: number
  originator_user_id: string
  last_error?: string
  created_at: string
  completed_at?: string | null
}

/**
 * 批次条目（app.BatchItem）。
 * status ∈ pending | claimed | processing | succeeded | failed | skipped。
 */
export interface BatchItem {
  id: string
  batch_id: string
  seq: number
  /** 物料引用（大对象存档后的指针；内联小对象直存） */
  payload_ref: Record<string, unknown> | null
  status: string
  attempts: number
  max_attempts: number
  error?: string
}

/** POST /api/work-items/{workItemId}/batches 请求体（handler decode struct） */
export interface BatchCreateInput {
  kind: string
  params?: Record<string, unknown>
  source?: Record<string, unknown>
  concurrency?: number
  chunk_size?: number
  payloads: Record<string, unknown>[]
}

/** GET /api/batches/{batchId} 响应：批次 + 四态计数 */
export interface BatchDetail {
  batch: Batch
  pending: number
  claimed: number
  processing: number
  done: number
}

/** GET /api/work-items/{workItemId}/batches → { batches: [...] } */
export async function listBatches(workItemId: string): Promise<Batch[]> {
  const res = await api.get<{ batches: Batch[] | null }>(
    `/api/work-items/${workItemId}/batches`,
  )
  return res.batches ?? []
}

/** POST /api/work-items/{workItemId}/batches → 201 + Batch */
export function createBatch(workItemId: string, input: BatchCreateInput) {
  return api.post<Batch>(`/api/work-items/${workItemId}/batches`, input)
}

/** GET /api/batches/{batchId} → { batch, pending, claimed, processing, done } */
export function getBatch(batchId: string) {
  return api.get<BatchDetail>(`/api/batches/${batchId}`)
}

/** GET /api/batches/{batchId}/items?status=&limit= → { items: [...] }（limit 后端默认 100，上限 1000） */
export async function listBatchItems(
  batchId: string,
  opts: { status?: string; limit?: number } = {},
): Promise<BatchItem[]> {
  const qs = new URLSearchParams()
  if (opts.status) qs.set('status', opts.status)
  if (opts.limit) qs.set('limit', String(opts.limit))
  const query = qs.toString()
  const res = await api.get<{ items: BatchItem[] | null }>(
    `/api/batches/${batchId}/items${query ? `?${query}` : ''}`,
  )
  return res.items ?? []
}

/** POST /api/batches/{batchId}/pause → { result: "paused" }（非 running 时 409） */
export function pauseBatch(batchId: string) {
  return api.post<{ result: string }>(`/api/batches/${batchId}/pause`)
}

/** POST /api/batches/{batchId}/resume → { result: "resumed" }（非 paused 时 409） */
export function resumeBatch(batchId: string) {
  return api.post<{ result: string }>(`/api/batches/${batchId}/resume`)
}

/** POST /api/batches/{batchId}/cancel → { result: "cancelled" }（已终态时 409） */
export function cancelBatch(batchId: string) {
  return api.post<{ result: string }>(`/api/batches/${batchId}/cancel`)
}

/** POST /api/batches/{batchId}/retry-failed → { requeued: n }（重新入队 failed 条目数） */
export function retryBatchFailed(batchId: string) {
  return api.post<{ requeued: number }>(`/api/batches/${batchId}/retry-failed`)
}
