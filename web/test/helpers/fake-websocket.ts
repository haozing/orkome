import { WsClient, type WsFrame } from '@/lib/ws'
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
export function makeFakeSocketClient(
  options: Partial<ConstructorParameters<typeof WsClient>[0]> = {},
) {
  const client = new WsClient({
    socketFactory: (url) => new FakeWebSocket(url) as unknown as WebSocket,
    minRetryMs: 100,
    ...options,
  })
  const lastSocket = () => FakeWebSocket.instances.at(-1)!
  return { client, lastSocket }
}
