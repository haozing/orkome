import { QueryClient } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { bridgeWsToQuery, type WsInvalidationRule } from '@/lib/ws-query-bridge'
import { makeFakeSocketClient, resetFakeSockets } from '../helpers/fake-websocket'
describe('WS → Query invalidation bridge', () => {
  let queryClient: QueryClient
  let invalidateSpy: ReturnType<typeof vi.spyOn>
  const rules: readonly WsInvalidationRule[] = [
    { match: /^task:/, invalidate: [['tasks']] },
    { match: 'inbox:created', invalidate: [['inbox'], ['inbox', 'unread-count']] },
  ]
  beforeEach(() => {
    resetFakeSockets()
    queryClient = new QueryClient()
    invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
  })
  it('regex rules match prefix frames, exact rules match same-name frames only', () => {
    const { client, lastSocket } = makeFakeSocketClient()
    const off = bridgeWsToQuery(client, queryClient, rules)
    client.connect()
    lastSocket().serverOpen()
    lastSocket().serverSend({ type: 'task:queued', event_id: 'e1' })
    expect(invalidateSpy).toHaveBeenCalledTimes(1)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['tasks'] })
    invalidateSpy.mockClear()
    lastSocket().serverSend({ type: 'chat:message', event_id: 'e2' })
    expect(invalidateSpy).not.toHaveBeenCalled()
    invalidateSpy.mockClear()
    lastSocket().serverSend({ type: 'inbox:created', event_id: 'e3' })
    expect(invalidateSpy).toHaveBeenCalledTimes(2)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['inbox'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['inbox', 'unread-count'] })
    off()
  })
  it('event_id dedup: duplicate frames do not invalidate twice', () => {
    const { client, lastSocket } = makeFakeSocketClient()
    bridgeWsToQuery(client, queryClient, rules)
    client.connect()
    lastSocket().serverOpen()
    const frame = { type: 'task:queued', event_id: 'dup-1' }
    lastSocket().serverSend(frame)
    lastSocket().serverSend(frame)
    expect(invalidateSpy).toHaveBeenCalledTimes(1)
  })
  it('no invalidation after bridge teardown', () => {
    const { client, lastSocket } = makeFakeSocketClient()
    const off = bridgeWsToQuery(client, queryClient, rules)
    client.connect()
    lastSocket().serverOpen()
    off()
    lastSocket().serverSend({ type: 'task:queued', event_id: 'e4' })
    expect(invalidateSpy).not.toHaveBeenCalled()
  })
})
