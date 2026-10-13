import { api } from '@ploykit/client'

// 批量处理（batch）前端数据封装。后端契约：
//  - handler: internal/orchestration/adapters/http/handlers_batch.go
//  - 路由:   internal/orchestration/adapters/http/routes.go:117-124
//  - 类型:   internal/orchestration/app/batch.go（Batch / BatchItem，json tag 以此为准）
//  - 状态枚举: internal/orchestration/domain/batch.go
// workspace 经 X-Workspace-Id 头传递（apiFetch 的 workspaceIdProvider 自动附带，
// 与 listPackEntries 同机制，不需要手动设）。

/**
 * 批次（app.Batch）。status ∈ draft | pending | running | paused |
 * completed | failed | cancelled（domain/batch.go 常量）。
 * draft 为 v2.0 新增：source 通道创建后先物化不开跑，需调 startBatch 转移。
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
  /** 物料总数 = 创建时 payloads 长度（source 通道 = 物化条目数） */
  total: number
  succeeded: number
  failed: number
  skipped: number
  concurrency: number
  chunk_size: number
  pace_interval_seconds: number
  originator_user_id: string
  /** v2.0：source 通道时为触发创建的 agent id（handoff 直接关联） */
  originator_agent_id?: string
  /**
   * v2.0：source 物化对账。draft/创建期非空；
   * source_count=数据源命中数，materialized=实际落条目数，skipped=校验未过跳过数。
   */
  source_materialization?: {
    source_count: number
    materialized: number
    skipped: number
  } | null
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
  /** v2.0：skipped 条目的跳过原因（如 required 字段校验未过） */
  skip_reason?: string
}

/** 创建请求：通道 A——手动条目（payloads 内联，创建即跑） */
export interface BatchCreatePayloads {
  kind: string
  params?: Record<string, unknown>
  concurrency?: number
  chunk_size?: number
  payloads: Record<string, unknown>[]
}

/** 创建请求 source 形态（v2.0 通道 B）：创建即物化但不开跑（status='draft'） */
export interface BatchCreateSource {
  source: {
    /** 数据源工具名（须是该智能体工具面上已注册的工具） */
    tool: string
    /** 工具入参（单对象，可选） */
    args?: Record<string, unknown>
    /** JSONPath 抽取路径，如 $.attachments[*] */
    pick?: string
  }
  /** 成品入参模板，值可为 JSONPath 引用，如 {"image_ref":"$.file_id","lang":"zh"} */
  payload_template: Record<string, unknown>
  /** 成品入参必填字段名列表（缺失 → skipped + skip_reason） */
  required?: string[]
  kind: string
  params?: Record<string, unknown>
  concurrency?: number
  chunk_size?: number
}

/**
 * POST /api/work-items/{workItemId}/batches 请求体（handler decode struct）。
 * 联合：payloads 与 source 两形态互斥（后端二选一校验）。
 */
export type BatchCreateInput = BatchCreatePayloads | BatchCreateSource

/**
 * 创建接口的扩展响应（v2.0 source 通道）：Batch + 预览 sample；
 * 物化对账直接复用 Batch.source_materialization。兼容仅返回 Batch 的
 * 旧形态（此时前端再 GET 一次批次补拉 source_materialization）。
 */
export interface BatchCreateSourceResult extends Batch {
  /** 前 3 条成品 payload（预览） */
  sample?: Record<string, unknown>[]
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

/** POST /api/work-items/{workItemId}/batches → 201 + Batch（source 通道或含物化预览字段） */
export function createBatch(workItemId: string, input: BatchCreateInput) {
  return api.post<Batch | BatchCreateSourceResult>(
    `/api/work-items/${workItemId}/batches`,
    input,
  )
}

/** POST /api/batches/{batchId}/start → { result: "started" }（draft→pending 开跑；非 draft 时 409） */
export function startBatch(batchId: string) {
  return api.post<{ result: string }>(`/api/batches/${batchId}/start`)
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
