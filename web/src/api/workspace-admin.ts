import { api } from '@ploykit/client'

// 工作区管理面端点封装（settings 域设计 docs/settings-domain-design-2026-10-14.md §3.4/3.5，
// wire 形状从 ploykit workspace/adapters/http 冻结提取——严格解码，body 字段不得多传）。
// 成员列表读取（listMembers/membersQueryOptions）留在 identity.ts 不动（W1/W2 文件不相交）。
// 权限：读=member（members:read / invites:read），写=admin（members:write / members:invite）。

/** PATCH /api/workspaces/{id} → 200 Workspace（只有 name 可改，slug 不可改）。 */
export interface WorkspaceAdminRecord {
  id: string
  slug: string
  name: string
  plan_code: string
  created_at: string
}

/** PATCH /api/workspaces/{id} body {name}（只有一个字段，多传 → 400）。 */
export function renameWorkspace(
  workspaceId: string,
  name: string,
): Promise<WorkspaceAdminRecord> {
  return api.patch<WorkspaceAdminRecord>(`/api/workspaces/${workspaceId}`, { name })
}

/** 工作区邀请（GET wire 上 status 恒为 "pending"；前端不做状态列）。 */
export interface WorkspaceInvitation {
  id: string
  workspace_id: string
  email: string
  role: 'admin' | 'member'
  status: string
  expires_at: string
  created_at: string
}

/** GET /api/workspaces/{id}/invitations → 信封 {items,total}，归一 items。 */
export async function listInvitations(workspaceId: string): Promise<WorkspaceInvitation[]> {
  const page = await api.get<{ items: WorkspaceInvitation[] | null; total?: number }>(
    `/api/workspaces/${workspaceId}/invitations`,
  )
  return page.items ?? []
}

/** POST /api/workspaces/{id}/invitations body {email, role}（role 仅 admin|member；
 *  TTL 固定 7 天不随请求传）→ 201 Invitation；已 pending / 已是成员 → 409。 */
export function createInvitation(
  workspaceId: string,
  email: string,
  role: 'admin' | 'member',
): Promise<WorkspaceInvitation> {
  return api.post<WorkspaceInvitation>(`/api/workspaces/${workspaceId}/invitations`, {
    email,
    role,
  })
}

/** DELETE /api/workspaces/{id}/invitations/{invId} → 204（非 pending → 404）。 */
export function revokeInvitation(workspaceId: string, invId: string): Promise<void> {
  return api.delete<void>(`/api/workspaces/${workspaceId}/invitations/${invId}`)
}

/** 分享链接（GET 仅返回未撤销未过期的行；完整 code 创建时仅此一次，不落库回传）。 */
export interface WorkspaceShareLink {
  id: string
  code_prefix: string
  role: 'admin' | 'member'
  max_uses: number
  uses: number
  expires_at: string
  created_at: string
}

/** GET /api/workspaces/{id}/share-links → 信封 {items,total}，归一 items。 */
export async function listShareLinks(workspaceId: string): Promise<WorkspaceShareLink[]> {
  const page = await api.get<{ items: WorkspaceShareLink[] | null; total?: number }>(
    `/api/workspaces/${workspaceId}/share-links`,
  )
  return page.items ?? []
}

/** POST /api/workspaces/{id}/share-links body 三字段全必填（role 仅 admin|member；
 *  max_uses 1-1000、ttl_hours 1-720，0 不合法）→ 201 {link, code}，code 明文仅此一次。 */
export function createShareLink(
  workspaceId: string,
  role: 'admin' | 'member',
  maxUses: number,
  ttlHours: number,
): Promise<{ link: WorkspaceShareLink; code: string }> {
  return api.post<{ link: WorkspaceShareLink; code: string }>(
    `/api/workspaces/${workspaceId}/share-links`,
    { role, max_uses: maxUses, ttl_hours: ttlHours },
  )
}

/** DELETE /api/workspaces/{id}/share-links/{linkId} → 204。 */
export function revokeShareLink(workspaceId: string, linkId: string): Promise<void> {
  return api.delete<void>(`/api/workspaces/${workspaceId}/share-links/${linkId}`)
}

/** PATCH /api/workspaces/{id}/members/{userId} body {role}（role 仅 admin|member，
 *  不可指派 owner；不能改自己；最后一个 owner 不可降级 → 409）→ 200 {"status":"ok"}。 */
export function updateMemberRole(
  workspaceId: string,
  userId: string,
  role: 'admin' | 'member',
): Promise<{ status: string }> {
  return api.patch<{ status: string }>(`/api/workspaces/${workspaceId}/members/${userId}`, {
    role,
  })
}

/** DELETE /api/workspaces/{id}/members/{userId} → 200（非 204，有 body）；
 *  不能移除自己（403 "use leave instead"）；可移除非最后 owner。 */
export function removeMember(
  workspaceId: string,
  userId: string,
): Promise<{ status: string }> {
  return api.delete<{ status: string }>(`/api/workspaces/${workspaceId}/members/${userId}`)
}
