import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PackUIEntry } from '@/api/packs'
import type { TaskMessage } from '@/api/orchestration'
import { TaskMessageList, TaskMessageRow } from '@/components/task-messages'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'

// setup.ts pins i18n to 'en'; assertions target the English copy from taskMsg.*.
// Covers the four deliverable states: ① job handle (+deep link) ② JobStatus
// failure (post_publish red / pre_publish neutral) ③ metrics grid ④ default
// gray-box fallbacks (unmatched / truncated / parse failure / other kinds)
// plus the contract-violation card and call_id → tool_use pairing.

const ENTRY: PackUIEntry = {
  id: 'pe-1',
  workspace_id: 'ws-1',
  pack: 'agentblog',
  label: 'Content Factory',
  url_template: 'https://agentblog.internal/console?ws={workspaceId}&t={uiToken}&job={jobId}',
  installed_at: '2026-10-11T08:00:00Z',
}

let seq = 0
const msg = (over: Partial<TaskMessage>): TaskMessage => ({
  id: `m-${++seq}`,
  task_id: 't-1',
  seq,
  kind: 'tool_result',
  content: '',
  truncated: false,
  created_at: '2026-10-11T08:00:00Z',
  ...over,
})

// eino-ext CallToolResult wrapper (A1 interface doc §2 shape):
// {"content":[{"type":"text","text":"<inner json>"}],"structuredContent":<inner>}
const wrap = (inner: unknown) =>
  JSON.stringify({
    content: [{ type: 'text', text: JSON.stringify(inner) }],
    structuredContent: inner,
  })
// 同款包装但不带 structuredContent（走 content[0].text 二次解析路径）
const wrapTextOnly = (innerText: string) =>
  JSON.stringify({ content: [{ type: 'text', text: innerText }] })

function render_ui(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

const entriesHandler = (items: PackUIEntry[]) =>
  http.get('/api/ui-entries', () => HttpResponse.json({ items }))

beforeEach(() => {
  useWorkspaceStore.setState({ currentId: null })
})
afterEach(() => {
  useWorkspaceStore.setState({ currentId: null })
})

describe('JobHandleCard (job handle result)', () => {
  it('shows job_id; deep link expands {jobId}/{uiToken}/{workspaceId} with a freshly minted token', async () => {
    useWorkspaceStore.setState({ currentId: 'ws-1' })
    const posts: unknown[] = []
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    server.use(
      entriesHandler([ENTRY]),
      http.post('/api/ui-tokens', async ({ request }) => {
        posts.push(await request.json())
        return HttpResponse.json({ token: 'tok-1' })
      }),
    )
    const messages = [
      msg({
        id: 'u1',
        kind: 'tool_use',
        call_id: 'call-1',
        content: 'agentblog_submit_generation {"seed":"x"}',
      }),
      msg({ id: 'r1', call_id: 'call-1', content: wrap({ job_id: 'job-42' }) }),
    ]
    render_ui(<TaskMessageList messages={messages} />)
    expect(await screen.findByTestId('task-job-handle')).toBeInTheDocument()
    expect(screen.getByText('job-42')).toBeInTheDocument()
    expect(screen.queryByTestId('task-job-console-link')).toBeNull() // entries 尚在加载
    fireEvent.click(await screen.findByRole('button', { name: 'Open in pack console' }))
    await waitFor(() =>
      expect(openSpy).toHaveBeenCalledWith(
        'https://agentblog.internal/console?ws=ws-1&t=tok-1&job=job-42',
        '_blank',
        'noopener',
      ),
    )
    expect(posts).toEqual([{ plugin: 'agentblog', workspace_id: 'ws-1' }])
    openSpy.mockRestore()
  })

  it('copy chip copies the job id and flips to Copied', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    })
    render_ui(<TaskMessageRow message={msg({ content: wrap({ job_id: 'job-42' }) })} />)
    expect(screen.getByTestId('task-job-handle')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('job-42'))
    expect(await screen.findByText('Copied')).toBeInTheDocument()
  })

  it('hides the console button when no pack entry matches the tool prefix; job_id still shown', async () => {
    useWorkspaceStore.setState({ currentId: 'ws-1' })
    server.use(entriesHandler([ENTRY]))
    const messages = [
      msg({ id: 'u1', kind: 'tool_use', call_id: 'call-2', content: 'orphan_submit_x {}' }),
      msg({ id: 'r1', call_id: 'call-2', content: wrapTextOnly('{"job_id":"j-9"}') }),
    ]
    render_ui(<TaskMessageList messages={messages} />)
    expect(await screen.findByTestId('task-job-handle')).toBeInTheDocument()
    expect(screen.getByText('j-9')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByTestId('task-job-console-link')).toBeNull())
    expect(screen.queryByRole('button', { name: 'Open in pack console' })).toBeNull()
  })

  it('hides the console button when the result has no paired tool_use (no tool name)', async () => {
    useWorkspaceStore.setState({ currentId: 'ws-1' })
    server.use(entriesHandler([ENTRY]))
    render_ui(
      <TaskMessageList
        messages={[msg({ call_id: 'call-9', content: wrap({ job_id: 'j-solo' }) })]}
      />,
    )
    expect(await screen.findByTestId('task-job-handle')).toBeInTheDocument()
    expect(screen.getByText('j-solo')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByTestId('task-job-console-link')).toBeNull())
  })

  it('renders the optional state badge when the handle carries one', () => {
    render_ui(
      <TaskMessageRow message={msg({ content: wrap({ job_id: 'job-7', state: 'queued' }) })} />,
    )
    expect(screen.getByTestId('task-job-handle')).toBeInTheDocument()
    expect(screen.getByTestId('task-job-state')).toHaveTextContent('Queued')
  })
})

describe('JobStatusCard (JobStatus schema result)', () => {
  const failedStatus = wrap({
    job_id: 'job-9',
    state: 'failed',
    stage: 'cdn_refresh',
    result: { url: 'https://blog.example.com/posts/1', summary: 'generated but CDN flush failed' },
    metrics: { quality_score: 0.82 },
    error: { code: 'cdn_flush_failed', retryable: false, stage_failed: 'post_publish' },
  })

  it('failed + post_publish: red warning, error.code, stage_failed tag, clickable url', () => {
    render_ui(<TaskMessageRow message={msg({ content: failedStatus })} />)
    expect(screen.getByTestId('task-job-status')).toBeInTheDocument()
    expect(screen.getByTestId('task-job-state')).toHaveTextContent('Failed')
    expect(screen.getByTestId('task-job-status')).toHaveTextContent('cdn_refresh')
    const errBox = screen.getByTestId('task-job-error')
    expect(errBox).toHaveTextContent('cdn_flush_failed')
    expect(errBox).toHaveTextContent('post_publish')
    expect(errBox.className).toContain('border-red-200') // 红色警示
    expect(screen.getByText(/Already live externally/)).toBeInTheDocument()
    const url = screen.getByTestId('task-job-result-url') as HTMLAnchorElement
    expect(url.getAttribute('href')).toBe('https://blog.example.com/posts/1')
    expect(screen.getByTestId('task-metrics-grid')).toHaveTextContent('quality_score')
  })

  it('failed + pre_publish: neutral tone and retry note', () => {
    const content = wrap({
      job_id: 'job-10',
      state: 'failed',
      stage: 'draft',
      error: { code: 'quality_failed', retryable: true, stage_failed: 'pre_publish' },
    })
    render_ui(<TaskMessageRow message={msg({ content })} />)
    const errBox = screen.getByTestId('task-job-error')
    expect(errBox).toHaveTextContent('quality_failed')
    expect(errBox).toHaveTextContent('pre_publish')
    expect(errBox.className).toContain('border-zinc-200') // 中性
    expect(screen.getByText(/Nothing published yet/)).toBeInTheDocument()
    expect(screen.queryByText(/Already live externally/)).toBeNull()
  })

  it('running state shows progress and stage without an error block', () => {
    const content = wrap({ job_id: 'job-11', state: 'running', stage: 'drafting' })
    render_ui(<TaskMessageRow message={msg({ content })} />)
    expect(screen.getByTestId('task-job-state')).toHaveTextContent('Running')
    expect(screen.getByTestId('task-job-status')).toHaveTextContent('drafting')
    expect(screen.queryByTestId('task-job-error')).toBeNull()
  })
})

describe('MetricsCard (metrics object result)', () => {
  it('renders a key/value grid, formatting nested values as JSON', () => {
    const content = wrap({
      metrics: { quality_score: 0.87, word_count: 1234, passed: true, top_words: ['a', 'b'] },
    })
    render_ui(<TaskMessageRow message={msg({ content })} />)
    const grid = screen.getByTestId('task-metrics-grid')
    expect(grid).toHaveTextContent('quality_score')
    expect(grid).toHaveTextContent('0.87')
    expect(grid).toHaveTextContent('word_count')
    expect(grid).toHaveTextContent('1234')
    expect(grid).toHaveTextContent('passed')
    expect(grid).toHaveTextContent('true')
    expect(grid).toHaveTextContent('top_words')
    expect(grid).toHaveTextContent('["a","b"]')
  })
})

describe('default gray box fallback (zero style regression)', () => {
  it('unmatched shape renders the current pre box with the raw content', () => {
    render_ui(<TaskMessageRow message={msg({ content: '{"hello":"world"}' })} />)
    expect(screen.queryByTestId('task-job-handle')).toBeNull()
    expect(screen.queryByTestId('task-job-status')).toBeNull()
    expect(screen.queryByTestId('task-metrics')).toBeNull()
    expect(screen.getByText('Tool result')).toBeInTheDocument()
    const pre = document.querySelector('li pre')
    expect(pre).toBeInTheDocument()
    expect(pre!.textContent).toBe('{"hello":"world"}')
    expect(document.querySelector('li')!.className).toContain('rounded-md')
  })

  it('truncated content always falls back, even if it looks like a job handle', () => {
    render_ui(
      <TaskMessageRow message={msg({ content: wrap({ job_id: 'cut-off' }), truncated: true })} />,
    )
    expect(screen.queryByTestId('task-job-handle')).toBeNull()
    expect(screen.getByText('…content truncated')).toBeInTheDocument()
    expect(document.querySelector('li pre')).toBeInTheDocument()
  })

  it('non-JSON content falls back to the pre box', () => {
    render_ui(<TaskMessageRow message={msg({ content: 'pong (not json)' })} />)
    expect(screen.queryByTestId('task-job-handle')).toBeNull()
    expect(document.querySelector('li pre')!.textContent).toBe('pong (not json)')
  })

  it('wrapper whose inner text is not JSON falls back (structuredProbe semantics)', () => {
    render_ui(<TaskMessageRow message={msg({ content: wrapTextOnly('pong') })} />)
    expect(screen.queryByTestId('task-job-handle')).toBeNull()
    expect(document.querySelector('li pre')).toBeInTheDocument()
  })

  it('other kinds (tool_use / approval) keep the current rendering', () => {
    render_ui(
      <TaskMessageList
        messages={[
          msg({ id: 'u1', kind: 'tool_use', content: 'get_work_item {"id":"wi-1"}' }),
          msg({ id: 'a1', kind: 'approval', content: 'approve me' }),
        ]}
      />,
    )
    expect(screen.getByText('Tool use')).toBeInTheDocument()
    expect(screen.getByText('Approval request')).toBeInTheDocument()
    expect(screen.getByText('approve me')).toBeInTheDocument()
  })

  it('handle with extra keys is not a job handle (contract strictness → default box)', () => {
    render_ui(<TaskMessageRow message={msg({ content: wrap({ job_id: 'j-x', note: 'hi' }) })} />)
    expect(screen.queryByTestId('task-job-handle')).toBeNull()
    expect(document.querySelector('li pre')).toBeInTheDocument()
  })
})

describe('contract violation state (A1 frozen error format)', () => {
  it('renders the lightweight violation card with reason / tool / excerpt', () => {
    const content =
      'tool output contract violation: malformed json\n' +
      '{"code":"TOOL_OUTPUT_CONTRACT_VIOLATION","tool":"demo_ping","reason":"malformed json","output_excerpt":"pong (not json)"}'
    render_ui(<TaskMessageRow message={msg({ content })} />)
    expect(screen.getByTestId('task-violation')).toBeInTheDocument()
    expect(screen.getByText(/Tool output contract violation/)).toBeInTheDocument()
    expect(screen.getByText(/demo_ping/)).toBeInTheDocument()
    expect(screen.getAllByText(/malformed json/).length).toBeGreaterThan(0)
    expect(screen.getByText('pong (not json)')).toBeInTheDocument()
    // 违规态不落默认灰框：原始两行错误文本不被原样整段渲染
    expect(screen.queryByText(/tool output contract violation: malformed json/)).toBeNull()
  })
})
