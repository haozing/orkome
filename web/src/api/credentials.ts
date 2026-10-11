import { api } from '@ploykit/client'
export interface Credential {
  id: string
  provider: string
  created_at: string
}
export async function listCredentials(): Promise<Credential[]> {
  const res = await api.get<{
    credentials: Credential[] | null
  }>('/api/credentials')
  return res.credentials ?? []
}
export function createCredential(provider: string, secret: string) {
  return api.post<Credential>('/api/credentials', { provider, secret })
}
export function deleteCredential(id: string) {
  return api.delete<{
    result: string
  }>(`/api/credentials/${id}`)
}
