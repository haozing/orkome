import { api } from '@ploykit/client'
export interface Squad {
  id: string
  name: string
  instructions: string
  leader_agent_id: string
  members: string[] | null
}
export interface SquadMemberRow {
  agent_id: string
  name: string
  role: string
  archived: boolean
}
export interface SquadMemberStatus {
  agent_id: string
  name: string
  role: string
  status: 'idle' | 'running' | 'queued' | string
  active_tasks:
    | {
        id: string
        status: string
      }[]
    | null
}
export interface SquadInput {
  name: string
  instructions: string
  leader_agent_id?: string
}
export async function listSquads(): Promise<Squad[]> {
  const res = await api.get<{
    squads: Squad[] | null
  }>('/api/squads')
  return res.squads ?? []
}
export async function getSquad(id: string): Promise<Squad> {
  return api.get<Squad>(`/api/squads/${id}`)
}
export function createSquad(input: SquadInput) {
  return api.post<Squad>('/api/squads', input)
}
export function updateSquad(id: string, input: SquadInput) {
  return api.put<Squad>(`/api/squads/${id}`, input)
}
export function deleteSquad(id: string) {
  return api.delete<{
    result: string
  }>(`/api/squads/${id}`)
}
export async function listSquadMembers(id: string): Promise<SquadMemberRow[]> {
  const res = await api.get<{
    members: SquadMemberRow[] | null
  }>(`/api/squads/${id}/members`)
  return res.members ?? []
}
export function addSquadMember(id: string, agentId: string, role?: string) {
  return api.post<{
    result: string
  }>(`/api/squads/${id}/members`, {
    agent_id: agentId,
    ...(role?.trim() ? { role: role.trim() } : {}),
  })
}
export function updateSquadMemberRole(id: string, agentId: string, role: string) {
  return api.patch<{
    result: string
  }>(`/api/squads/${id}/members/${agentId}`, { role })
}
export function removeSquadMember(id: string, agentId: string) {
  return api.delete<{
    result: string
  }>(`/api/squads/${id}/members/${agentId}`)
}
export async function squadMembersStatus(id: string): Promise<SquadMemberStatus[]> {
  const res = await api.get<{
    members: SquadMemberStatus[] | null
  }>(`/api/squads/${id}/members/status`)
  return res.members ?? []
}
