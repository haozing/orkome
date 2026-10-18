import {
  infiniteQueryOptions,
  type InfiniteData,
  type QueryFunctionContext,
  type UseInfiniteQueryOptions,
} from '@tanstack/react-query'
import { api } from '@ploykit/client'
export interface Task {
  id: string
  workspace_id: string
  agent_id: string
  work_item_id?: string
  status: string
  priority: number
  attempt: number
  max_attempts: number
  context: Record<string, unknown>
  originator_user_id?: string
  accountable_user_id?: string
  originator_source: string
  trigger_evidence_kind?: string
  failure_reason?: string
  error?: string
  result?: string
  summary?: string
  retry_of_task_id?: string
  created_at: string
  completed_at?: string | null
}
export interface TaskMessage {
  id: string
  task_id: string
  seq: number
  kind: 'text' | 'tool_use' | 'tool_result' | 'error' | 'approval' | string
  call_id?: string
  content: string
  truncated: boolean
  created_at: string
}
/**
 * job-handle 协议（docs/pack-tool-contract-design-2026-10-11.md §2.1）：
 * `<pack>_submit_*` 类工具秒回的句柄，形状固定为 `{job_id}`。
 * 字段名是唯一权威来源，勿增改。
 */
export interface JobHandle {
  job_id: string
}
/**
 * JobStatus 固定 schema（docs/pack-tool-contract-design-2026-10-11.md §2.1）。
 * `state` 五态 / `result{url,summary}` / `metrics`（pack 自定义键值对）/
 * `error{code,retryable,stage_failed}`——失败时 stage_failed 标记副作用落点：
 * pre_publish 幂等可重试，post_publish 已对外生效禁自动重试。
 */
export type JobState = 'queued' | 'running' | 'awaiting_review' | 'published' | 'failed'
export interface JobStatusResult {
  url?: string
  summary?: string
}
export interface JobStatusError {
  code: string
  retryable: boolean
  stage_failed: 'pre_publish' | 'post_publish' | null
}
export interface JobStatus extends JobHandle {
  job_id: string
  state: JobState
  stage: string
  result?: JobStatusResult
  metrics?: Record<string, unknown>
  error?: JobStatusError
}
export interface AgentRuntimeConfig {
  provider?: string
  model?: string
  base_url?: string
  credential_id?: string
  params?: Record<string, unknown>
  env?: Record<string, string>
  [key: string]: unknown
}
export interface ConversationStarter {
  label: string
}
export interface InvocationTarget {
  type: 'workspace' | 'member' | string
  user_id?: string
}
export interface AgentMCPServer {
  id: string
  agent_id: string
  name: string
  transport: string
  url: string
  enabled: boolean
  has_credentials: boolean
}
export interface ResolvedMCPServer {
  name: string
  transport: string
  url: string
  enabled: boolean
}
export interface Agent {
  id: string
  workspace_id: string
  name: string
  description: string
  runtime_config: AgentRuntimeConfig
  max_concurrent_tasks: number
  visibility: string
  owner_user_id?: string
  archived: boolean
  created_at: string
  conversation_starters?: ConversationStarter[]
  invocation_targets?: InvocationTarget[]
  /**
   * 运行时类型（docs/assistant-access-design-2026-10-14.md §4.1）：
   * engine=引擎执行 | external=外部工人（退役中）| assistant_proxy=助理代理行。
   * 代理行代表"用户自己的 AI 助理"（姓名渲染、@mention 候选），不可指派；
   * 后端可能不发该字段（旧响应），故可选——缺省按非 proxy 处理。
   */
  runtime_kind?: string
}
export interface AgentInput {
  name: string
  description: string
  runtime_config?: AgentRuntimeConfig
  max_concurrent_tasks?: number
  visibility?: string
  conversation_starters?: ConversationStarter[]
  invocation_targets?: InvocationTarget[]
}
/**
 * GET /api/agents 列表。默认过滤 runtime_kind='assistant_proxy' 的代理行
 * （设计 v4 §4.1：代理行代表用户自己的 AI 助理，不是可指派/可选择的对象，
 * 智能体列表页、@mention 候选、自动化/工作流配置等消费方都不该看到它）；
 * 需要"看到代理行"的渲染面（任务面板/时间线/汇报的名字映射）传
 * includeAssistantProxy: true。过滤收口在 API 函数内——十几处消费方一处改。
 */
export async function listAgents(opts: { includeAssistantProxy?: boolean } = {}): Promise<Agent[]> {
  const res = await api.get<{
    agents: Agent[] | null
  }>('/api/agents')
  const rows = res.agents ?? []
  if (opts.includeAssistantProxy) return rows
  return rows.filter((a) => a.runtime_kind !== 'assistant_proxy')
}
export async function getAgent(id: string): Promise<Agent> {
  return api.get<Agent>(`/api/agents/${id}`)
}
export function createAgent(input: AgentInput) {
  return api.post<Agent>('/api/agents', input)
}
export function updateAgent(id: string, input: AgentInput) {
  return api.put<Agent>(`/api/agents/${id}`, input)
}
export function archiveAgent(id: string, archived: boolean) {
  return api.post<{
    result: string
  }>(`/api/agents/${id}/${archived ? 'archive' : 'restore'}`)
}
export function cancelAgentTasks(id: string) {
  return api.post<{
    cancelled: number
  }>(`/api/agents/${id}/cancel-tasks`)
}
export async function getAgentEnv(id: string): Promise<Record<string, string>> {
  const res = await api.get<{
    env: Record<string, string> | null
  }>(`/api/agents/${id}/env`)
  return res.env ?? {}
}
export function putAgentEnv(id: string, env: Record<string, string>) {
  return api.put<unknown>(`/api/agents/${id}/env`, { env })
}
export async function listWorkingAgents(): Promise<
  {
    agent_id: string
    name: string
    active: number
  }[]
> {
  const res = await api.get<{
    agents:
      | {
          agent_id: string
          name: string
          active: number
        }[]
      | null
  }>('/api/working-agents')
  return res.agents ?? []
}
export async function listAgentMcp(agentId: string): Promise<AgentMCPServer[]> {
  const res = await api.get<{
    servers: AgentMCPServer[] | null
  }>(`/api/agents/${agentId}/mcp-servers`)
  return res.servers ?? []
}
export async function resolvedAgentMcp(agentId: string): Promise<ResolvedMCPServer[]> {
  const res = await api.get<{
    servers: ResolvedMCPServer[] | null
  }>(`/api/agents/${agentId}/mcp-servers?resolved=1`)
  return res.servers ?? []
}
/**
 * POST /api/agents/{agentId}/mcp-servers（创建语义，W2-3 起重名返回 409，
 * 不再静默覆盖既有条目——FP-07 P1-4）。credentials 缺省 = 空凭证。
 */
export function createAgentMcp(
  agentId: string,
  input: {
    name: string
    url: string
    enabled?: boolean
    credentials?: Record<string, string> | null
  },
) {
  return api.post<AgentMCPServer>(`/api/agents/${agentId}/mcp-servers`, input)
}
/**
 * PUT /api/agents/{agentId}/mcp-servers/{serverId}（W2-3 三态，与 ws 级统一）：
 * credentials 不传（键不存在）= 保留已存凭证；null/{} = 清空；值 = 覆盖。
 */
export function updateAgentMcp(
  agentId: string,
  serverId: string,
  input: {
    name: string
    url: string
    enabled: boolean
    credentials?: Record<string, string> | null
  },
) {
  return api.put<AgentMCPServer>(`/api/agents/${agentId}/mcp-servers/${serverId}`, input)
}
export function deleteAgentMcp(agentId: string, serverId: string) {
  return api.delete<{
    result: string
  }>(`/api/agents/${agentId}/mcp-servers/${serverId}`)
}
export async function listAgentTasks(id: string): Promise<Task[]> {
  const res = await api.get<{
    tasks: Task[] | null
  }>(`/api/agents/${id}/tasks`)
  return res.tasks ?? []
}
export interface WorkspaceTaskRow extends Task {
  agent_name: string
  work_item_title?: string
}
export interface WorkspaceTaskFilters {
  status?: string
  agent_id?: string
  cursor?: string
}
export async function listWorkspaceTasks(filters: WorkspaceTaskFilters = {}): Promise<{
  tasks: WorkspaceTaskRow[]
  next_cursor: string | null
}> {
  const params = new URLSearchParams()
  if (filters.status) params.set('status', filters.status)
  if (filters.agent_id) params.set('agent_id', filters.agent_id)
  if (filters.cursor) params.set('cursor', filters.cursor)
  const qs = params.toString()
  const res = await api.get<{
    tasks: WorkspaceTaskRow[] | null
    next_cursor: string | null
  }>('/api/tasks' + (qs ? `?${qs}` : ''))
  return { tasks: res.tasks ?? [], next_cursor: res.next_cursor ?? null }
}
export async function listWorkItemTasks(workItemId: string): Promise<Task[]> {
  const res = await api.get<{
    tasks: Task[] | null
  }>(`/api/work-items/${workItemId}/tasks`)
  return res.tasks ?? []
}
/**
 * messages 端点一页（FP-05 P1-1）。始终以 order=desc 拉取（最新一页），
 * 返回前已反转为 seq 升序；`next_before_seq` 是更早一页的游标（后端仅在
 * “desc 且满页”时携带，缺失/空即历史耗尽）。
 */
export interface TaskMessagePage {
  messages: TaskMessage[]
  next_before_seq: number | null
}
export async function listTaskMessagesPage(
  taskId: string,
  opts: { limit?: number; beforeSeq?: number } = {},
): Promise<TaskMessagePage> {
  const limit = opts.limit ?? 200
  const params = new URLSearchParams({ limit: String(limit), order: 'desc' })
  if (opts.beforeSeq) params.set('before_seq', String(opts.beforeSeq))
  const res = await api.get<{
    messages: TaskMessage[] | null
    next_before_seq?: number
  }>(`/api/tasks/${taskId}/messages?${params.toString()}`)
  const desc = res.messages ?? []
  return { messages: [...desc].reverse(), next_before_seq: res.next_before_seq ?? null }
}
export async function listTaskMessages(taskId: string, limit = 200): Promise<TaskMessage[]> {
  // FP-05 P1-1：原实现不带 order，默认拉最旧 200 条——长任务尾段（含最终
  // tool_result）在工单面板与 run-logs 抽屉均不可见（摘要服务读的是最新
  // 200，同一数据两个窗口）。改为 order=desc 拉最新 200 后反转为升序返回，
  // 消费方（useQuery / TaskMessageList）无需感知方向。向上翻历史走
  // taskMessagesInfiniteQuery（next_before_seq 游标）。
  const page = await listTaskMessagesPage(taskId, { limit })
  return page.messages
}
// Explicit UseInfiniteQueryOptions annotation — TS2742 (same as work.ts).
export const taskMessagesInfiniteQuery = (
  taskId: string,
  limit = 200,
): UseInfiniteQueryOptions<
  TaskMessagePage,
  Error,
  InfiniteData<TaskMessagePage, number>,
  readonly ['task', string, 'messages', 'paged'],
  number
> =>
  infiniteQueryOptions({
    // 键刻意与 task-panel 的普通 useQuery（['task', id, 'messages']，数组形）
    // 不同尾段：两者缓存形状不同（pages vs 数组），共享同键会互相覆写
    // queryFn 导致形状串台。WS 失效链走 ['task'] 前缀，两个键都命中。
    queryKey: ['task', taskId, 'messages', 'paged'] as const,
    queryFn: ({
      pageParam,
    }: QueryFunctionContext<readonly ['task', string, 'messages', 'paged'], number>) =>
      listTaskMessagesPage(taskId, { limit, beforeSeq: pageParam || undefined }),
    initialPageParam: 0,
    // 后端仅在 desc 且满页时返回 next_before_seq；缺省即更早历史耗尽。
    getNextPageParam: (lastPage) => lastPage.next_before_seq ?? undefined,
  })
/**
 * 已加载页（新→旧）拼成 seq 全局升序的完整列表：倒序遍历页、页内本就
 * 升序，旧页天然接在新页前面，无需再排序。
 */
export function taskMessagesAscending(
  data: InfiniteData<TaskMessagePage, number> | undefined,
): TaskMessage[] {
  if (!data) return []
  const out: TaskMessage[] = []
  for (let i = data.pages.length - 1; i >= 0; i--) out.push(...data.pages[i]!.messages)
  return out
}
export function settleTask(taskId: string, approved: boolean, note?: string) {
  return api.post<unknown>(`/api/tasks/${taskId}/${approved ? 'approve' : 'deny'}`, { note })
}
export function cancelWorkItemTask(workItemId: string, taskId: string) {
  return api.post<unknown>(`/api/work-items/${workItemId}/tasks/${taskId}/cancel`)
}

/** GET /api/setup-status → 冷启动探针（wiring_setupstatus.go）：一次请求回答
 * 「这个工作区配置齐了吗」——凭证数 + 每个 agent 的 ready 态（runtime_config
 * 的 model/credential_id/base_url 三键齐全）。 */
export interface SetupStatus {
  credential_count: number
  agents: { id: string; name: string; archived: boolean; ready: boolean }[]
}
export function getSetupStatus(): Promise<SetupStatus> {
  return api.get<SetupStatus>('/api/setup-status')
}
