import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsFrame } from '@ploykit/client'
import { FakeWebSocket, makeFakeSocketClient, resetFakeSockets } from '../helpers/fake-websocket'
const makeClient = makeFakeSocketClient
beforeEach(() => {
  resetFakeSockets()
})
afterEach(() => {
  vi.useRealTimers()
})
describe('WsClient connect and reconnect', () => {
  it('state open after connect; server close triggers backoff reconnect and resends active subscriptions', async () => {
    vi.useFakeTimers()
    const { client, lastSocket } = makeClient()
    const statuses: string[] = []
    client.onStatus((s) => statuses.push(s))
    client.connect()
    lastSocket().serverOpen()
    const release = client.subscribe('task', 't-1')
    // hub-compatible flat control frame (see src/realtime/client.ts adapter)
    expect(lastSocket().sent).toContain('{"type":"subscribe","scope":"task","id":"t-1"}')
    lastSocket().close()
    expect(client.status).toBe('reconnecting')
    await vi.advanceTimersByTimeAsync(400)
    lastSocket().serverOpen()
    expect(client.status).toBe('open')
    expect(lastSocket().sent).toContain('{"type":"subscribe","scope":"task","id":"t-1"}')
    release()
    client.close()
    expect(client.status).toBe('stopped')
  })
  it('no reconnect after manual close', async () => {
    vi.useFakeTimers()
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    client.close()
    const count = FakeWebSocket.instances.length
    await vi.advanceTimersByTimeAsync(5000)
    expect(FakeWebSocket.instances.length).toBe(count)
  })
  it('send silently dropped when not connected (no throw)', () => {
    const { client } = makeClient()
    expect(() => client.send('subscribe', { scope: 'task', id: 'x' })).not.toThrow()
  })
})
describe('WsClient frame dispatch', () => {
  it('on(type) exact match; unknown frame types silently ignored (forward-compat contract)', () => {
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    const received: string[] = []
    client.on('task:queued', (f) => received.push(f.type))
    expect(() => lastSocket().serverSend({ type: 'some:future:frame' })).not.toThrow()
    lastSocket().serverSend({ type: 'task:queued', payload: { id: 't1' } })
    expect(received).toEqual(['task:queued'])
  })
  it("'*' wildcard receives all frames (incl. control acks)", () => {
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    const types: string[] = []
    client.on('*', (f) => types.push(f.type))
    lastSocket().serverSend({ type: 'connected', payload: { capabilities: ['task'] } })
    lastSocket().serverSend({ type: 'confirmed:subscribe', payload: { scope: 'task' } })
    lastSocket().serverSend({ type: 'inbox:created' })
    expect(types).toEqual(['connected', 'confirmed:subscribe', 'inbox:created'])
  })
  it('event_id idempotent dedup: duplicate delivery dispatches once', () => {
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    let count = 0
    client.on('work_item:created', () => {
      count += 1
    })
    const frame: WsFrame = { type: 'work_item:created', event_id: 'evt-1', payload: { id: 'w1' } }
    lastSocket().serverSend(frame)
    lastSocket().serverSend(frame)
    expect(count).toBe(1)
  })
  it('frames without event_id are not deduped (control frames repeatable)', () => {
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    let count = 0
    client.on('confirmed:subscribe', () => {
      count += 1
    })
    lastSocket().serverSend({ type: 'confirmed:subscribe' })
    lastSocket().serverSend({ type: 'confirmed:subscribe' })
    expect(count).toBe(2)
  })
  it('dedup ring cap: evicted old event_id dispatches again', () => {
    const { client, lastSocket } = makeClient({ dedupeLimit: 2 })
    client.connect()
    lastSocket().serverOpen()
    const seen: string[] = []
    client.on('task:progress', (f) => seen.push(f.event_id!))
    lastSocket().serverSend({ type: 'task:progress', event_id: 'e1' })
    lastSocket().serverSend({ type: 'task:progress', event_id: 'e2' })
    lastSocket().serverSend({ type: 'task:progress', event_id: 'e3' })
    lastSocket().serverSend({ type: 'task:progress', event_id: 'e1' })
    expect(seen).toEqual(['e1', 'e2', 'e3', 'e1'])
  })
  it('handler throw does not affect other handlers', () => {
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    const consoleMock = vi.spyOn(console, 'error').mockImplementation(() => {})
    const good: string[] = []
    client.on('task:queued', () => {
      throw new Error('boom')
    })
    client.on('task:queued', (f) => good.push(f.type))
    lastSocket().serverSend({ type: 'task:queued' })
    expect(good).toEqual(['task:queued'])
    consoleMock.mockRestore()
  })
  it('non-JSON messages ignored', () => {
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    expect(() => lastSocket().onmessage?.({ data: 'not-json' })).not.toThrow()
  })
})
describe('WsClient scoped subscriptions', () => {
  it('refcount: two subscriptions send one frame; unsubscribe only after all released', () => {
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    const r1 = client.subscribe('chat', 's-1')
    const r2 = client.subscribe('chat', 's-1')
    const sent = () => lastSocket().sent.filter((s) => s.includes('"subscribe"')).length
    expect(sent()).toBe(1)
    r1()
    expect(lastSocket().sent).not.toContainEqual(expect.stringContaining('unsubscribe'))
    r2()
    expect(lastSocket().sent).toContain('{"type":"unsubscribe","scope":"chat","id":"s-1"}')
  })
})
describe('WsClient url function and reconnect', () => {
  it('url function evaluated per connect (workspace switch picks latest)', () => {
    let dynamic = 'ws://a/ws?workspace_id=ws-1'
    const { client } = makeClient({ url: () => dynamic })
    client.connect()
    expect(FakeWebSocket.instances.at(-1)!.url).toBe('ws://a/ws?workspace_id=ws-1')
    dynamic = 'ws://a/ws?workspace_id=ws-2'
    client.reconnect()
    expect(FakeWebSocket.instances.at(-1)!.url).toBe('ws://a/ws?workspace_id=ws-2')
    client.close()
  })
  it('reconnect reopens immediately (no backoff timer) and resends active subscriptions', () => {
    const { client } = makeClient()
    client.connect()
    const first = FakeWebSocket.instances.at(-1)!
    first.serverOpen()
    client.subscribe('task', 't-1')
    client.reconnect()
    expect(client.status).toBe('connecting')
    const second = FakeWebSocket.instances.at(-1)!
    expect(second).not.toBe(first)
    second.serverOpen()
    expect(client.status).toBe('open')
    expect(second.sent).toContain('{"type":"subscribe","scope":"task","id":"t-1"}')
    const count = FakeWebSocket.instances.length
    first.onclose?.()
    expect(FakeWebSocket.instances.length).toBe(count)
    client.close()
  })
  it('reconnect is a no-op when stopped (no revival after logout)', () => {
    const { client } = makeClient()
    client.close()
    client.reconnect()
    expect(client.status).toBe('stopped')
    expect(FakeWebSocket.instances).toHaveLength(0)
  })
})
describe('hub-compatible control frames (ploykit wsx parses top-level fields)', () => {
  it('subscribe/unsubscribe frames are flat: {"type","scope","id"} at top level', () => {
    const { client, lastSocket } = makeClient()
    client.connect()
    lastSocket().serverOpen()
    const release = client.subscribe('task', 'group:sub:leaf')
    expect(lastSocket().sent).toContain('{"type":"subscribe","scope":"task","id":"group:sub:leaf"}')
    release()
    expect(lastSocket().sent).toContain(
      '{"type":"unsubscribe","scope":"task","id":"group:sub:leaf"}',
    )
    client.close()
  })
})
