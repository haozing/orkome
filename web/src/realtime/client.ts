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

/**
 * ploykit v0.4.0 protocol gap: @ploykit/client's WsClient serializes control
 * frames as `{type, payload:{scope,id}}`, but the framework hub
 * (platform/wsx handleControl) parses top-level `{type, token, scope, id}`.
 * Flatten subscribe/unsubscribe/auth frames so task/chat scope subscriptions
 * actually reach the hub until the framework aligns the two sides.
 */
class HubCompatibleWsClient extends WsClient {
  override send(type: string, payload?: unknown): void {
    if (
      (type === 'subscribe' || type === 'unsubscribe' || type === 'auth') &&
      payload !== null &&
      typeof payload === 'object'
    ) {
      const socket = (this as unknown as { socket: WebSocket | null }).socket
      if (socket?.readyState !== WebSocket.OPEN) return
      socket.send(JSON.stringify({ type, ...(payload as Record<string, unknown>) }))
      return
    }
    super.send(type, payload)
  }
}

export const wsClient = new HubCompatibleWsClient({ url: appWsUrl })
export { HubCompatibleWsClient }
