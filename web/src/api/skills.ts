import { api } from '@ploykit/client'
export interface Skill {
  id: string
  workspace_id: string
  name: string
  description: string
  origin: 'manual' | 'import' | 'url' | string
  origin_ref?: string
  created_by?: string
  created_at: string
}
export interface SkillFile {
  id: string
  path: string
  content: string
  sha256: string
  size_bytes: number
}
export interface AgentSkill {
  skill_id: string
  name: string
  description: string
  enabled: boolean
}
export interface SkillAgent {
  agent_id: string
  name: string
  enabled: boolean
}
export async function listSkills(q?: string): Promise<Skill[]> {
  const path =
    q && q.trim() ? `/api/skills/search?q=${encodeURIComponent(q.trim())}` : '/api/skills'
  const res = await api.get<{
    skills: Skill[] | null
  }>(path)
  return res.skills ?? []
}
export function createSkill(name: string, description: string) {
  return api.post<Skill>('/api/skills', { name, description })
}
export function importSkillFromUrl(url: string) {
  return api.post<Skill>('/api/skills/import', { url })
}
export function importSkillZip(file: File) {
  const form = new FormData()
  form.append('file', file)
  return api.post<Skill>('/api/skills/import', form)
}
export function getSkill(id: string) {
  return api.get<Skill>(`/api/skills/${id}`)
}
export async function getSkillFiles(id: string): Promise<{
  files: SkillFile[]
  manifest_sha256: string
}> {
  const res = await api.get<{
    files: SkillFile[] | null
    manifest_sha256: string
  }>(`/api/skills/${id}/files`)
  return { files: res.files ?? [], manifest_sha256: res.manifest_sha256 }
}
export function putSkillFile(id: string, path: string, content: string) {
  return api.put<{
    file: SkillFile
    manifest_sha256: string
  }>(`/api/skills/${id}/files`, {
    path,
    content,
  })
}
export function deleteSkillFile(id: string, fileId: string) {
  return api.delete<{
    result: string
    manifest_sha256: string
  }>(`/api/skills/${id}/files/${fileId}`)
}
export function refreshSkill(id: string) {
  return api.post<{
    refreshed: number
  }>(`/api/skills/${id}/refresh`)
}
export function deleteSkill(id: string) {
  return api.delete<{
    result: string
  }>(`/api/skills/${id}`)
}
export async function listAgentSkills(agentId: string): Promise<AgentSkill[]> {
  const res = await api.get<{
    skills: AgentSkill[] | null
  }>(`/api/agents/${agentId}/skills`)
  return res.skills ?? []
}
export function addAgentSkill(agentId: string, skillId: string) {
  return api.post<{
    result: string
  }>(`/api/agents/${agentId}/skills/add`, { skill_id: skillId })
}
export function setAgentSkillEnabled(agentId: string, skillId: string, enabled: boolean) {
  return api.put<{
    enabled: boolean
  }>(`/api/agents/${agentId}/skills/${skillId}/enabled`, {
    enabled,
  })
}
export function removeAgentSkill(agentId: string, skillId: string) {
  return api.delete<{
    result: string
  }>(`/api/agents/${agentId}/skills/${skillId}`)
}
export async function listSkillAgents(skillId: string): Promise<SkillAgent[]> {
  const res = await api.get<{
    agents: SkillAgent[] | null
  }>(`/api/skills/${skillId}/agents`)
  return res.agents ?? []
}
