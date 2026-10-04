import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useWsFrame, useWsScope, useWsStatus } from '@/hooks/ws'
import type { FrameHandler } from '@/lib/ws'
import { makeFakeSocketClient, resetFakeSockets } from '../helpers/fake-websocket'
beforeEach(() => {
  resetFakeSockets()
})
describe('useWsStatus', () => {
  it('follows connection lifecycle: connecting → open → reconnecting', () => {
    const { client, lastSocket } = makeFakeSocketClient()
    client.connect()
    const { result } = renderHook(() => useWsStatus(client))
    expect(result.current).toBe('connecting')
    act(() => lastSocket().serverOpen())
    expect(result.current).toBe('open')
    act(() => lastSocket().close())
    expect(result.current).toBe('reconnecting')
    client.close()
  })
})
describe('useWsFrame', () => {
  it('frame arrival callback; unsubscribes on unmount', () => {
    const { client, lastSocket } = makeFakeSocketClient()
    client.connect()
    lastSocket().serverOpen()
    const handler = vi.fn()
    const { unmount } = renderHook(() => useWsFrame('task:queued', handler, client))
    act(() => lastSocket().serverSend({ type: 'task:queued', payload: { id: 't1' } }))
    expect(handler).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'task:queued', payload: { id: 't1' } }),
    )
    unmount()
    act(() => lastSocket().serverSend({ type: 'task:queued', event_id: 'x' }))
    expect(handler).toHaveBeenCalledTimes(1)
    client.close()
  })
  it('handler uses latest closure (latest-ref); old closures no longer called', () => {
    const { client, lastSocket } = makeFakeSocketClient()
    client.connect()
    lastSocket().serverOpen()
    const first = vi.fn()
    const second = vi.fn()
    const { rerender } = renderHook(
      ({ h }: { h: FrameHandler }) => useWsFrame('chat:message', h, client),
      { initialProps: { h: first } },
    )
    act(() => lastSocket().serverSend({ type: 'chat:message', event_id: 'e1' }))
    expect(first).toHaveBeenCalledTimes(1)
    rerender({ h: second })
    act(() => lastSocket().serverSend({ type: 'chat:message', event_id: 'e2' }))
    expect(first).toHaveBeenCalledTimes(1)
    expect(second).toHaveBeenCalledTimes(1)
    client.close()
  })
})
describe('useWsScope', () => {
  it('id change unsubscribes old scope then subscribes new; unsubscribes on unmount', () => {
    const { client, lastSocket } = makeFakeSocketClient()
    client.connect()
    lastSocket().serverOpen()
    const { rerender, unmount } = renderHook(
      ({ id }: { id: string }) => useWsScope('task', id, client),
      { initialProps: { id: 't-1' } },
    )
    expect(lastSocket().sent).toContain(
      '{"type":"subscribe","payload":{"scope":"task","id":"t-1"}}',
    )
    rerender({ id: 't-2' })
    expect(lastSocket().sent).toContain(
      '{"type":"subscribe","payload":{"scope":"task","id":"t-2"}}',
    )
    expect(lastSocket().sent).toContain(
      '{"type":"unsubscribe","payload":{"scope":"task","id":"t-1"}}',
    )
    unmount()
    expect(lastSocket().sent).toContain(
      '{"type":"unsubscribe","payload":{"scope":"task","id":"t-2"}}',
    )
    client.close()
  })
  it('does not subscribe when id undefined', () => {
    const { client, lastSocket } = makeFakeSocketClient()
    client.connect()
    lastSocket().serverOpen()
    renderHook(() => useWsScope('task', undefined, client))
    expect(lastSocket().sent).toHaveLength(0)
    client.close()
  })
})
