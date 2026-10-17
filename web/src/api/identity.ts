import { queryOptions, type UseQueryOptions } from '@tanstack/react-query'
import { api } from '@ploykit/client'
export interface User {
  id: string
  email: string
  display_name: string
  avatar_url?: string
  language?: string
  timezone?: string
}
export interface Workspace {
  id: string
  name: string
  slug: string
  /** Legacy /api/me carried it; the framework /api/workspaces list does not. */
  work_item_counter?: number
  plan_code?: string
  role?: string
  created_at: string
}
export interface Member {
  user_id: string
  role: 'owner' | 'admin' | 'member'
  email: string
  display_name: string
  created_at: string
}
export interface ServerConfig {
  version: string
  csrf_token: string
}
export interface Me {
  user: User
  workspaces: Workspace[]
}
// ---- 账户侧设置域（design settings-domain §3.1–§3.3/§3.5，wire 形状照抄 ploykit 0.7.0） ----

/** PATCH /auth/me → app.User（v0.7.0 部分化语义：指针字段——undefined=保留现值，
 *  avatar_url 显式空串=清空，两字段都缺=400）。调用方只传要改的字段。 */
export function updateProfile(patch: { display_name?: string; avatar_url?: string }) {
  return api.patch<User>('/auth/me', patch)
}

/** GET /auth/mfa/status → {enabled, pending}。 */
export function getMfaStatus() {
  return api.get<{ enabled: boolean; pending: boolean }>('/auth/mfa/status')
}

/** POST /auth/mfa/enroll → {otpauth_uri, secret, issuer}（secret 一次性明文）。 */
export function mfaEnroll() {
  return api.post<{ otpauth_uri: string; secret: string; issuer: string }>('/auth/mfa/enroll', {})
}

/** POST /auth/mfa/enable body {code} → {backup_codes: string[]}（一次性明文）。 */
export function mfaEnable(code: string) {
  return api.post<{ backup_codes: string[] }>('/auth/mfa/enable', { code })
}

/** POST /auth/mfa/disable（无 body）→ {disabled:true}；吊销全部 session 含当前。 */
export function mfaDisable() {
  return api.post<{ disabled: boolean }>('/auth/mfa/disable', {})
}

/** 一条登录会话（GET /auth/sessions 元素）。 */
export interface SessionInfo {
  id: string
  ip_hash: string
  user_agent: string
  last_seen_at: string
  expires_at: string
  created_at: string
  current: boolean
  impersonated_by?: string
}

/** GET /auth/sessions → 裸数组（无信封，可能 null，归一 []）。 */
export async function listSessions(): Promise<SessionInfo[]> {
  const items = await api.get<SessionInfo[] | null>('/auth/sessions')
  return items ?? []
}

/** DELETE /auth/sessions/{id} → 204。 */
export function revokeSession(id: string) {
  return api.delete<void>(`/auth/sessions/${id}`)
}

/** DELETE /auth/sessions → 200 {"status":"ok"}，清 cookie：调用方须走登出流程。 */
export function revokeAllSessions() {
  return api.delete<{ status: string }>('/auth/sessions')
}

/** 我的待处理邀请（收件方视角，Invitation + workspace_name/slug）。 */
export interface MyInvitation {
  id: string
  workspace_id: string
  email: string
  role: string
  status: string
  expires_at: string
  created_at: string
  workspace_name: string
  workspace_slug: string
}

/** GET /api/invitations/mine → 裸数组（可能 null，归一 []）。 */
export async function listMyInvitations(): Promise<MyInvitation[]> {
  const items = await api.get<MyInvitation[] | null>('/api/invitations/mine')
  return items ?? []
}

/** POST /api/invitations/{id}/accept → Workspace；成功后调用方刷新 me 缓存。 */
export function acceptInvitation(id: string) {
  return api.post<Workspace>(`/api/invitations/${id}/accept`, {})
}

/** POST /api/invitations/{id}/decline → {"status":"ok"}。 */
export function declineInvitation(id: string) {
  return api.post<{ status: string }>(`/api/invitations/${id}/decline`, {})
}
export function getServerConfig() {
  return api.get<ServerConfig>('/config')
}
export function sendLoginCode(email: string) {
  // ploykit identity: {"status":"sent"} on success.
  return api.post<{
    status: string
  }>('/auth/send-code', { email })
}
export function verifyLoginCode(email: string, code: string) {
  // ploykit identity: session cookie is set here; the CSRF token keeps
  // coming from GET /config (no csrf_token in the response body anymore).
  return api.post<{
    user: User
    expires_at: string
  }>('/auth/verify-code', { email, code })
}
export function logout() {
  return api.post<void>('/auth/logout')
}
export function createWorkspace(name: string, slug: string) {
  return api.post<Workspace>('/api/workspaces', { name, slug })
}
export function joinByShareCode(code: string) {
  // POST /api/share-links/redeem returns a Workspace ({id,slug,name,...}) —
  // NOT a Member and without workspace_id (audit FP-02 P0-2/P0-3).
  return api.post<Workspace>('/api/share-links/redeem', { code })
}
export async function listMembers(workspaceId: string): Promise<Member[]> {
  // GET /api/workspaces/{id}/members is the ploykit app.Page envelope
  // ({items, total}); it is never a bare array (audit batch1 root cause A).
  const page = await api.get<{ items: Member[] | null; total?: number }>(
    `/api/workspaces/${workspaceId}/members`,
  )
  return page.items ?? []
}
export interface PatToken {
  id: string
  name: string
  prefix: string
  last_used_at?: string
  expires_at?: string
  revoked_at?: string
  created_at: string
}
export interface PatSecret {
  token: string
  id: string
  name: string
  prefix: string
}
export async function listTokens(): Promise<PatToken[]> {
  const items = await api.get<PatToken[] | null>('/api/tokens')
  return items ?? []
}
/** PAT 凭证作用域（POST /api/tokens 可选字段；两项都不带 = 不限）。 */
export interface PatScope {
  workspace_ids?: string[]
  permissions?: string[]
}
export async function createToken(
  name: string,
  ttlSeconds = 0,
  scope?: PatScope,
): Promise<PatSecret> {
  // The backend takes ttl_hours only (unknown request fields are rejected via
  // DisallowUnknownFields) and answers nested {pat: {...}, token} — not a
  // flat PatSecret (audit FP-09 P0-1). scope is a known framework field:
  // omit the key entirely when unset (undefined ≠ empty constraint).
  const res = await api.post<{ pat: Omit<PatSecret, 'token'>; token: string }>('/api/tokens', {
    name,
    ttl_hours: Math.round(ttlSeconds / 3600),
    ...(scope ? { scope } : {}),
  })
  return { ...res.pat, token: res.token }
}
export function revokeToken(id: string) {
  return api.delete<void>(`/api/tokens/${id}`)
}
// Explicit UseQueryOptions annotations: the '@tanstack/react-query' paths
// shim in tsconfig (for the module-cache ploykit bridge file) makes the
// inferred option types non-portable for declaration emit (TS2742).
export const configQueryOptions: UseQueryOptions<
  ServerConfig,
  Error,
  ServerConfig,
  readonly ['config']
> = queryOptions({
  queryKey: ['config'] as const,
  queryFn: getServerConfig,
  staleTime: 5 * 60000,
})
export const meQueryOptions: UseQueryOptions<Me, Error, Me, readonly ['me']> = queryOptions({
  queryKey: ['me'] as const,
  queryFn: async (): Promise<Me> => {
    // Wire change on the ploykit rebase: GET /api/me {user, workspaces} was
    // split into GET /auth/me (user) + GET /api/workspaces (memberships).
    const [user, workspaces] = await Promise.all([
      api.get<User>('/auth/me'),
      api.get<Workspace[] | null>('/api/workspaces'),
    ])
    return { user, workspaces: workspaces ?? [] }
  },
  staleTime: 30000,
})
export const membersQueryOptions = (
  workspaceId: string,
): UseQueryOptions<
  Member[],
  Error,
  Member[],
  readonly ['workspace', string, 'members']
> =>
  queryOptions({
    queryKey: ['workspace', workspaceId, 'members'] as const,
    queryFn: () => listMembers(workspaceId),
    enabled: !!workspaceId,
    staleTime: 5 * 60000,
  })
export const tokensQueryOptions: UseQueryOptions<
  PatToken[],
  Error,
  PatToken[],
  readonly ['tokens']
> = queryOptions({
  queryKey: ['tokens'] as const,
  queryFn: listTokens,
})

export const sessionsQueryOptions: UseQueryOptions<
  SessionInfo[],
  Error,
  SessionInfo[],
  readonly ['sessions']
> = queryOptions({
  queryKey: ['sessions'] as const,
  queryFn: listSessions,
})

export const mfaStatusQueryOptions: UseQueryOptions<
  { enabled: boolean; pending: boolean },
  Error,
  { enabled: boolean; pending: boolean },
  readonly ['mfa-status']
> = queryOptions({
  queryKey: ['mfa-status'] as const,
  queryFn: getMfaStatus,
})

export const myInvitationsQueryOptions: UseQueryOptions<
  MyInvitation[],
  Error,
  MyInvitation[],
  readonly ['my-invitations']
> = queryOptions({
  queryKey: ['my-invitations'] as const,
  queryFn: listMyInvitations,
})
