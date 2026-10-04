import { WsClient } from '@/lib/ws'
import { useWorkspaceStore } from '@/stores/workspace'
function appWsUrl(): string {
  const proto =
    typeof location === 'undefined' ? 'ws' : location.protocol === 'https:' ? 'wss' : 'ws'
  const host = typeof location === 'undefined' ? 'localhost' : location.host
  const workspaceId = useWorkspaceStore.getState().currentId
  return workspaceId
    ? `${proto}://${host}/ws?workspace_id=${encodeURIComponent(workspaceId)}`
    : `${proto}://${host}/ws`
}
export const wsClient = new WsClient({ url: appWsUrl })
