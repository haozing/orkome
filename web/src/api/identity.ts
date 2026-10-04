import { queryOptions } from '@tanstack/react-query'
import { api } from '@/lib/api'
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
  work_item_counter: number
  created_at: string
}
export interface Member {
  id: string
  workspace_id: string
  user_id: string
  role: 'owner' | 'admin' | 'member'
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
  return api.post<{
    sent: boolean
  }>('/auth/send-code', { email })
}
export function verifyLoginCode(email: string, code: string) {
  return api.post<{
    user: User
    csrf_token: string
  }>('/auth/verify-code', { email, code })
}
export function logout() {
  return api.post<void>('/auth/logout')
}
export function createWorkspace(name: string, slug: string) {
  return api.post<Workspace>('/api/workspaces', { name, slug })
}
export function joinByShareCode(code: string) {
  return api.post<Member>('/api/share-links/join', { code })
}
export interface Member {
  id: string
  workspace_id: string
  user_id: string
  role: 'owner' | 'admin' | 'member'
  email: string
  display_name: string
  created_at: string
}
export async function listMembers(workspaceId: string): Promise<Member[]> {
  const items = await api.get<Member[] | null>(`/api/workspaces/${workspaceId}/members`)
  return items ?? []
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
export function createToken(name: string, ttlSeconds = 0) {
  return api.post<PatSecret>('/api/tokens', { name, ttl_seconds: ttlSeconds })
}
export function revokeToken(id: string) {
  return api.delete<void>(`/api/tokens/${id}`)
}
export const configQueryOptions = queryOptions({
  queryKey: ['config'] as const,
  queryFn: getServerConfig,
  staleTime: 5 * 60000,
})
export const meQueryOptions = queryOptions({
  queryKey: ['me'] as const,
  queryFn: async () => {
    const me = await api.get<Me>('/api/me')
    return { ...me, workspaces: me.workspaces ?? [] }
  },
  staleTime: 30000,
})
export const membersQueryOptions = (workspaceId: string) =>
  queryOptions({
    queryKey: ['workspace', workspaceId, 'members'] as const,
    queryFn: () => listMembers(workspaceId),
    enabled: !!workspaceId,
    staleTime: 5 * 60000,
  })
export const tokensQueryOptions = queryOptions({
  queryKey: ['tokens'] as const,
  queryFn: listTokens,
})
