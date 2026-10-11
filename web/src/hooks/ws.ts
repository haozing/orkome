import { useEffect, useRef, useSyncExternalStore } from 'react'
import type { FrameHandler, ScopePrefix, WsClient, WsFrame, WsStatus } from '@ploykit/client'
import { wsClient } from '@/realtime/client'

export type { FrameHandler, ScopePrefix, WsClient, WsFrame, WsStatus }

// The underlying WsClient is @ploykit/client's; these are orkome's thin
// bindings defaulting the app-wide singleton (the framework's own hooks in
// @ploykit/hooks/ws require the client as an explicit argument and cannot be
// aliased from the module cache because they value-import 'react' from a
// directory without node_modules).
export function useWsStatus(client: WsClient = wsClient): WsStatus {
  return useSyncExternalStore(
    (cb) => client.onStatus(cb),
    () => client.status,
    () => 'idle',
  )
}

export function useWsScope(
  prefix: ScopePrefix,
  id: string | undefined,
  client: WsClient = wsClient,
): void {
  useEffect(() => {
    if (!id) return
    return client.subscribe(prefix, id)
  }, [prefix, id, client])
}

export function useWsFrame(type: string, handler: FrameHandler, client: WsClient = wsClient): void {
  const ref = useRef(handler)
  useEffect(() => {
    ref.current = handler
  })
  useEffect(() => {
    const dispatch = (frame: WsFrame) => ref.current(frame)
    return client.on(type, dispatch)
  }, [type, client])
}
