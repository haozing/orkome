import { infiniteQueryOptions, type QueryFunctionContext } from '@tanstack/react-query'
import { api } from '@/lib/api'
export interface WorkItem {
  id: string
  workspace_id: string
  number: number
  title: string
  description: string
  acceptance_criteria?: string
  status: string
  status_category: string
  priority: number
  assignee_type: string
  assignee_id?: string
  creator_type: string
  creator_id: string
  parent_work_item_id?: string
  project_id?: string
  due_date?: string
  start_date?: string
  revision: number
  created_at: string
  updated_at: string
  last_activity_at: string
}
export interface WorkItemPageInput {
  limit?: number
  cursor?: string
  project?: string
  search?: string
  statuses?: string[]
  assignee?: string
  creator?: string
  subscriber?: string
}
export async function listWorkItems(params: WorkItemPageInput = {}): Promise<{
  items: WorkItem[]
  next_cursor: string | null
}> {
  const qs = new URLSearchParams()
  if (params.limit) qs.set('limit', String(params.limit))
  if (params.cursor) qs.set('cursor', params.cursor)
  if (params.project) qs.set('project', params.project)
  if (params.search) qs.set('search', params.search)
  for (const key of params.statuses ?? []) qs.append('status', key)
  if (params.assignee) qs.set('assignee', params.assignee)
  if (params.creator) qs.set('creator', params.creator)
  if (params.subscriber) qs.set('subscriber', params.subscriber)
  const query = qs.toString()
  const page = await api.get<{
    items: WorkItem[] | null
    next_cursor: string | null
  }>(`/api/work-items${query ? `?${query}` : ''}`)
  return { items: page.items ?? [], next_cursor: page.next_cursor ?? null }
}
export function quickCreateWorkItem(input: { title: string; description?: string }) {
  return api.post<WorkItem>('/api/work-items/quick-create', input)
}
export interface WorkItemCreateInput {
  title: string
  description?: string
  status?: string
  priority?: number
  assignee_type?: 'none' | 'user' | 'agent'
  assignee_id?: string
  project_id?: string
}
export function createWorkItem(input: WorkItemCreateInput) {
  return api.post<WorkItem>('/api/work-items', {
    ...input,
    assignee_type: input.assignee_type ?? 'none',
    assignee_id: input.assignee_id ?? '',
    project_id: input.project_id ?? '',
  })
}
export async function listChildWorkItems(parentId: string): Promise<WorkItem[]> {
  const res = await api.get<{
    items: WorkItem[] | null
  }>(`/api/work-items/children?parent=${parentId}`)
  return res.items ?? []
}
export function getWorkItem(id: string) {
  return api.get<WorkItem>(`/api/work-items/${id}`)
}
export interface WorkItemStatus {
  key: string
  name: string
  category: string
  is_builtin: boolean
  position: number
  icon?: string
}
export async function listStatuses(): Promise<WorkItemStatus[]> {
  const items = await api.get<WorkItemStatus[] | null>('/api/work-item-statuses')
  return items ?? []
}
export interface WorkItemPatch {
  title?: string
  description?: string
  status?: string
  priority?: number
  assignee_type?: string
  assignee_id?: string
  due_date?: string
  start_date?: string
  parent_work_item_id?: string
}
export function updateWorkItem(id: string, patch: WorkItemPatch, expectedRevision: number) {
  return api.put<WorkItem>(`/api/work-items/${id}`, {
    ...patch,
    expected_revision: expectedRevision,
  })
}
export function deleteWorkItem(id: string) {
  return api.delete<void>(`/api/work-items/${id}`)
}
export interface Comment {
  id: string
  workspace_id?: string
  work_item_id: string
  author_type: string
  author_id?: string
  content: string
  type: string
  parent_id?: string
  resolved: boolean
  deleted: boolean
  revision: number
  created_at: string
  updated_at: string
}
export async function listComments(workItemId: string): Promise<Comment[]> {
  const res = await api.get<{
    items: Comment[] | null
  }>(`/api/work-items/${workItemId}/comments`)
  return res.items ?? []
}
export interface TriggerHit {
  agent_id: string
  reason: string
}
export interface TriggerPreview {
  result: 'would_trigger' | 'blocked'
  triggers?: TriggerHit[]
  suppressions?: TriggerHit[]
  reason_code?: string
}
export function previewCommentTrigger(workItemId: string, content: string) {
  return api.post<TriggerPreview>(`/api/work-items/${workItemId}/comments/trigger-preview`, {
    content,
  })
}
export function createComment(workItemId: string, content: string, parentId?: string) {
  return api.post<{
    comment: Comment
    admission: unknown
  }>(`/api/work-items/${workItemId}/comments`, { content, parent_id: parentId ?? '' })
}
export interface Label {
  id: string
  name: string
  color?: string
  count?: number
}
export async function listLabels(): Promise<Label[]> {
  const items = await api.get<Label[] | null>('/api/labels')
  return items ?? []
}
export function createLabel(name: string, color = '') {
  return api.post<Label>('/api/labels', { name, color })
}
export async function listWorkItemLabels(workItemId: string): Promise<Label[]> {
  const items = await api.get<Label[] | null>(`/api/work-items/${workItemId}/labels`)
  return items ?? []
}
export function attachLabel(workItemId: string, labelId: string) {
  return api.post<Label[]>(`/api/work-items/${workItemId}/labels`, { label_id: labelId })
}
export function detachLabel(workItemId: string, labelId: string) {
  return api.delete<Label[]>(`/api/work-items/${workItemId}/labels/${labelId}`)
}
export interface TimelineEntry {
  kind: 'comment' | 'activity' | string
  id: string
  actor_type?: string
  actor_id?: string
  content?: string
  payload?: Record<string, unknown>
  created_at: string
}
export async function listTimeline(workItemId: string, limit = 50): Promise<TimelineEntry[]> {
  const res = await api.get<{
    items: TimelineEntry[] | null
  }>(`/api/work-items/${workItemId}/timeline?limit=${limit}`)
  return res.items ?? []
}
export interface WorkItemListFilters {
  project?: string
  search?: string
  statuses?: string[]
  assignee?: string
  creator?: string
  subscriber?: string
}
export const workItemsInfiniteQuery = (limit = 30, filters: WorkItemListFilters = {}) =>
  infiniteQueryOptions({
    queryKey: ['work-items', filters] as const,
    queryFn: ({
      pageParam,
    }: QueryFunctionContext<readonly ['work-items', WorkItemListFilters], string>) =>
      listWorkItems({ limit, cursor: pageParam || undefined, ...filters }),
    initialPageParam: '',
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  })
export interface Project {
  id: string
  name: string
  description: string
  created_at: string
}
export async function listProjects(): Promise<Project[]> {
  const items = await api.get<Project[] | null>('/api/projects')
  return items ?? []
}
export function createProject(input: { name: string; description?: string }) {
  return api.post<Project>('/api/projects', input)
}
