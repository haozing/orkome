import { WsClient } from '@ploykit/client'
import { useWorkspaceStore } from '@/stores/workspace'

function appWsUrl(): string {
  const proto =
    typeof location === 'undefined' ? 'ws' : location.protocol === 'https:' ? 'wss' : 'ws'
  const host = typeof location === 'undefined' ? 'localhost' : location.host
  const workspaceId = useWorkspaceStore.getState().currentId
  // The framework hub (ploykit platform/wsx) reads ?workspace_id= from the
  // handshake and auto-joins the workspace scope for cookie-authed sockets,
  // so the workspace ridealong lives in the URL, not a control frame.
  return workspaceId
    ? `${proto}://${host}/ws?workspace_id=${encodeURIComponent(workspaceId)}`
    : `${proto}://${host}/ws`
}

// ploykit v0.4.1 fixed the F16 control-frame split (hub accepts both the
// nested-payload and top-level shapes), so the stock WsClient works directly.
export const wsClient = new WsClient({ url: appWsUrl })
