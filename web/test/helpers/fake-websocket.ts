import type { WsFrame, WsClientOptions } from '@ploykit/client'
import { HubCompatibleWsClient } from '@/realtime/client'
export class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static OPEN = 1
  readyState = 0
  sent: string[] = []
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  url = ''
  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }
  send(data: string): void {
    if (this.readyState !== FakeWebSocket.OPEN) throw new Error('not open')
    this.sent.push(data)
  }
  close(): void {
    if (this.readyState === 3) return
    this.readyState = 3
    this.onclose?.()
  }
  serverOpen(): void {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.()
  }
  serverSend(frame: WsFrame): void {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }
}
export function resetFakeSockets(): void {
  FakeWebSocket.instances = []
}
/**
 * Builds the app-wide client class (HubCompatibleWsClient, see
 * src/realtime/client.ts): control frames go out FLAT
 * ({"type":"subscribe","scope":…,"id":…}) to match the ploykit hub.
 */
export function makeFakeSocketClient(options: Partial<WsClientOptions> = {}) {
  const client = new HubCompatibleWsClient({
    socketFactory: (url) => new FakeWebSocket(url) as unknown as WebSocket,
    minRetryMs: 100,
    ...options,
  })
  const lastSocket = () => FakeWebSocket.instances.at(-1)!
  return { client, lastSocket }
}
