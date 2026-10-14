import { ApiError, api } from '@ploykit/client'

// Project Workflow API —— 形状逐字段对齐 docs/wf-interfaces-frozen.md §2/§3。
// W3 已摘除 W1 联调期的 USE_MOCK/mockStore 脚手架，八函数直连真实 HTTP。

export type WorkflowTriggerMode = 'auto' | 'manual'
export type WorkflowExecutorType = 'agent' | 'squad' | 'none'

/** workflow_stage 行（契约 §1/§3）。position 从 1 起、workflow 内唯一；executor_id none 时空缺。 */
export interface WorkflowStage {
  position: number
  name: string
  exit_status_key: string
  executor_type: WorkflowExecutorType
  executor_id?: string
  instruction: string
}

/** project_workflow 行（契约 §1/§3）。stages 按 position 升序随行。 */
export interface WorkflowDefinition {
  id: string
  workspace_id: string
  project_id: string
  name: string
  trigger_mode: WorkflowTriggerMode
  entry_stage_position: number
  enabled: boolean
  created_by: string
  created_at: string
  updated_at: string
  stages: WorkflowStage[]
}

/** PUT /api/projects/{projectId}/workflow 请求体（契约 §3，无 name 字段——流程名由后端管理）。 */
export interface WorkflowUpsertInput {
  trigger_mode: WorkflowTriggerMode
  entry_stage_position: number
  stages: WorkflowStage[]
}

/** work_item_workflow_state（契约 §2）。时间 RFC3339；finished_at 缺省 = 在途。 */
export interface WorkflowState {
  work_item_id: string
  workflow_id: string
  project_id: string
  stage_id: string
  stage_position: number
  stage_name: string
  executor_type: WorkflowExecutorType
  executor_id: string
  entry_status_key: string
  exit_status_key: string
  dispatch_count: number
  advance_count: number
  entered_at: string
  finished_at?: string
  stages: WorkflowStage[]
}

// —— 定义面（项目流程编辑器消费） ——

export async function getWorkflow(projectId: string): Promise<WorkflowDefinition | null> {
  const res = await api.get<{ workflow: WorkflowDefinition | null }>(
    `/api/projects/${projectId}/workflow`,
  )
  return res.workflow
}

export async function upsertWorkflow(
  projectId: string,
  input: WorkflowUpsertInput,
): Promise<WorkflowDefinition> {
  const res = await api.put<{ workflow: WorkflowDefinition }>(
    `/api/projects/${projectId}/workflow`,
    input,
  )
  return res.workflow
}

export function deleteWorkflow(projectId: string) {
  return api.delete<void>(`/api/projects/${projectId}/workflow`)
}

export async function setWorkflowEnabled(
  projectId: string,
  enabled: boolean,
): Promise<WorkflowDefinition> {
  const res = await api.post<{ workflow: WorkflowDefinition }>(
    `/api/projects/${projectId}/workflow/${enabled ? 'enable' : 'disable'}`,
  )
  return res.workflow
}

// —— 工作项侧（契约 §3 工作项×4；GET state 后端未挂（W2 notes「给 W3」），
// 前端降级为 404 → null，详情区块数据走 start/restart 响应 + 空态。后端挂载
// 该端点后此处无需再改——404 分支自然不再命中。 ——

export async function getWorkItemWorkflow(workItemId: string): Promise<WorkflowState | null> {
  try {
    const res = await api.get<{ state: WorkflowState | null }>(
      `/api/work-items/${workItemId}/workflow`,
    )
    return res.state
  } catch (err) {
    // 端点未挂（W2 语义）= 无 state 的另一种说法：归一为空态而非报错。
    if (err instanceof ApiError && err.status === 404) return null
    throw err
  }
}

export async function startWorkItemWorkflow(workItemId: string): Promise<WorkflowState> {
  const res = await api.post<{ state: WorkflowState }>(
    `/api/work-items/${workItemId}/workflow/start`,
  )
  return res.state
}

export async function restartWorkItemWorkflow(workItemId: string): Promise<WorkflowState> {
  const res = await api.post<{ state: WorkflowState }>(
    `/api/work-items/${workItemId}/workflow/restart`,
  )
  return res.state
}

export function detachWorkItemWorkflow(workItemId: string) {
  return api.delete<void>(`/api/work-items/${workItemId}/workflow`)
}
