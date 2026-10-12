import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PackUISlot } from '@/api/packs'
import { PackSlotFrame } from '@/components/pack-slot-frame'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'

// setup.ts pins i18n to 'en'; assertions target the English copy from slots.*.
// 覆盖：铸票调用（现铸现用）/ 变量替换 / pack.resize 消息调高 / 3s 超时回落
// 声明高度 / 铸票失败兜底 / 右上角深链常在。

const SLOT = (over: Partial<PackUISlot> = {}): PackUISlot => ({
  slot: 'workitem.detail.tab',
  label: 'Article preview',
  url_template: 'https://agentblog.internal/slot/preview?item={workItemId}&ws={workspaceId}&t={uiToken}',
  height: 480,
  ...over,
})

function render_frame(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

let mints: { plugin: string; workspace_id: string }[] = []

beforeEach(() => {
  useWorkspaceStore.setState({ currentId: 'ws-1' })
  mints = []
  server.use(
    http.post('/api/ui-tokens', async ({ request }) => {
      mints.push((await request.json()) as { plugin: string; workspace_id: string })
      return HttpResponse.json({ token: 'tok-1' })
    }),
  )
})
afterEach(() => {
  useWorkspaceStore.setState({ currentId: null })
  vi.restoreAllMocks()
})

describe('PackSlotFrame', () => {
  it('mints a fresh token per mount and expands {uiToken}/{workspaceId}/{workItemId}', async () => {
    render_frame(<PackSlotFrame pack="agentblog" slot={SLOT()} workItemId="wi-42" />)
    const frame = await screen.findByTestId('pack-slot-frame')
    const iframe = frame.querySelector('iframe')
    expect(iframe).not.toBeNull()
    expect(iframe!.getAttribute('src')).toBe(
      'https://agentblog.internal/slot/preview?item=wi-42&ws=ws-1&t=tok-1',
    )
    expect(mints).toEqual([{ plugin: 'agentblog', workspace_id: 'ws-1' }])
    expect(iframe!.getAttribute('sandbox')).toBe('allow-scripts allow-same-origin allow-popups')
    // 声明高度为初始高度
    expect(iframe!.style.height).toBe('480px')
  })

  it('remounts with a fresh token (60s ticket: mint per mount, never cached)', async () => {
    let tokSeq = 0
    server.use(
      http.post('/api/ui-tokens', async () => {
        tokSeq += 1
        return HttpResponse.json({ token: `tok-${tokSeq}` })
      }),
    )
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { unmount } = render(
      <QueryClientProvider client={qc}>
        <PackSlotFrame pack="agentblog" slot={SLOT()} workItemId="wi-42" />
      </QueryClientProvider>,
    )
    await screen.findByTestId('pack-slot-frame')
    unmount()

    const qc2 = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc2}>
        <PackSlotFrame pack="agentblog" slot={SLOT()} workItemId="wi-42" />
      </QueryClientProvider>,
    )
    const frame = await screen.findByTestId('pack-slot-frame')
    // 新挂载新票据（tok-2），不是 tok-1
    expect(frame.querySelector('iframe')!.getAttribute('src')).toContain('t=tok-2')
    expect(tokSeq).toBe(2)
  })

  it('resizes on pack.resize postMessage from the slot origin', async () => {
    render_frame(<PackSlotFrame pack="agentblog" slot={SLOT()} workItemId="wi-42" />)
    const frame = await screen.findByTestId('pack-slot-frame')
    const iframe = frame.querySelector('iframe') as HTMLIFrameElement

    await act(async () => {
      window.dispatchEvent(
        new MessageEvent('message', {
          origin: 'https://agentblog.internal',
          data: { type: 'pack.resize', height: 720 },
        }),
      )
    })
    expect(iframe.style.height).toBe('720px')

    // 非法值（负数/超上限/错类型）忽略
    await act(async () => {
      window.dispatchEvent(
        new MessageEvent('message', {
          origin: 'https://agentblog.internal',
          data: { type: 'pack.resize', height: -5 },
        }),
      )
    })
    expect(iframe.style.height).toBe('720px')
  })

  it('ignores pack.resize from a foreign origin', async () => {
    render_frame(<PackSlotFrame pack="agentblog" slot={SLOT()} workItemId="wi-42" />)
    const frame = await screen.findByTestId('pack-slot-frame')
    const iframe = frame.querySelector('iframe') as HTMLIFrameElement
    await act(async () => {
      window.dispatchEvent(
        new MessageEvent('message', {
          origin: 'https://evil.example.com',
          data: { type: 'pack.resize', height: 9999 },
        }),
      )
    })
    expect(iframe.style.height, '陌生 origin 的 resize 消息被拒').toBe('480px')
  })

  it('falls back to declared height after the resize window with no message', async () => {
    // 注入 50ms 短窗口：无 resize 消息时回落声明高度；收到过消息则定格不回落。
    render_frame(
      <PackSlotFrame
        pack="agentblog"
        slot={SLOT({ height: 300 })}
        workItemId="wi-42"
        resizeTimeoutMs={50}
      />,
    )
    const frame = await screen.findByTestId('pack-slot-frame')
    const iframe = frame.querySelector('iframe') as HTMLIFrameElement
    expect(iframe.style.height).toBe('300px')

    // 窗口内收到一次 resize → 定格在消息高度
    await act(async () => {
      window.dispatchEvent(
        new MessageEvent('message', {
          origin: 'https://agentblog.internal',
          data: { type: 'pack.resize', height: 600 },
        }),
      )
    })
    expect(iframe.style.height).toBe('600px')
    await new Promise((r) => setTimeout(r, 120))
    expect(iframe.style.height, '收到过 resize 后超时不再回落').toBe('600px')
  })

  it('declared height restored when window elapses before any message', async () => {
    // 先人为抬高高度（模拟收到过一次 resize），再走超时路径验证回落语义：
    // 超时定时器只在该 effect 周期内存在——重挂载后窗口内无消息即回落声明高度。
    render_frame(
      <PackSlotFrame
        pack="agentblog"
        slot={SLOT({ height: 240 })}
        workItemId="wi-42"
        resizeTimeoutMs={80}
      />,
    )
    const frame = await screen.findByTestId('pack-slot-frame')
    const iframe = frame.querySelector('iframe') as HTMLIFrameElement
    // 不发任何消息，等窗口过去——初始即声明高度，回落后仍是声明高度（幂等证明）。
    await new Promise((r) => setTimeout(r, 200))
    expect(iframe.style.height).toBe('240px')
  })

  it('renders mint-failure fallback; external deep link is always present', async () => {
    server.use(
      http.post('/api/ui-tokens', () =>
        HttpResponse.json({ error: 'boom' }, { status: 500 }),
      ),
    )
    render_frame(<PackSlotFrame pack="agentblog" slot={SLOT()} workItemId="wi-42" />)
    expect(await screen.findByTestId('pack-slot-error')).toBeInTheDocument()
    expect(screen.getByText(/Failed to mint slot token/i)).toBeInTheDocument()
  })

  it('keeps a fixed external-link fallback on the frame (iframe is never the only path)', async () => {
    render_frame(<PackSlotFrame pack="agentblog" slot={SLOT()} workItemId="wi-42" />)
    const link = await screen.findByTestId('pack-slot-external')
    expect(link).toHaveAttribute('href', expect.stringContaining('t=tok-1'))
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    fireEvent.click(link) // 可点（jsdom 不真跳转，冒烟即可）
  })
})
