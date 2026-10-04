import { api } from '@/lib/api'
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
export async function listAgents(): Promise<Agent[]> {
  const res = await api.get<{
    agents: Agent[] | null
  }>('/api/agents')
  return res.agents ?? []
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
export function upsertAgentMcp(
  agentId: string,
  input: {
    name: string
    url: string
    enabled?: boolean
    credentials?: Record<string, string>
  },
) {
  return api.post<AgentMCPServer>(`/api/agents/${agentId}/mcp-servers`, input)
}
export function updateAgentMcp(
  agentId: string,
  serverId: string,
  input: {
    name: string
    url: string
    enabled: boolean
    credentials?: Record<string, string>
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
export async function listTaskMessages(taskId: string, limit = 200): Promise<TaskMessage[]> {
  const res = await api.get<{
    messages: TaskMessage[] | null
  }>(`/api/tasks/${taskId}/messages?limit=${limit}`)
  return res.messages ?? []
}
export function settleTask(taskId: string, approved: boolean, note?: string) {
  return api.post<unknown>(`/api/tasks/${taskId}/${approved ? 'approve' : 'deny'}`, { note })
}
export function cancelWorkItemTask(workItemId: string, taskId: string) {
  return api.post<unknown>(`/api/work-items/${workItemId}/tasks/${taskId}/cancel`)
}
