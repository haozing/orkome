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
export async function createToken(name: string, ttlSeconds = 0): Promise<PatSecret> {
  // The backend takes ttl_hours only (unknown request fields are rejected via
  // DisallowUnknownFields) and answers nested {pat: {...}, token} — not a
  // flat PatSecret (audit FP-09 P0-1).
  const res = await api.post<{ pat: Omit<PatSecret, 'token'>; token: string }>('/api/tokens', {
    name,
    ttl_hours: Math.round(ttlSeconds / 3600),
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
