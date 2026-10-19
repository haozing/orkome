import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConfirmProvider, Toaster, toast } from '@ploykit/ui'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Autopilot, Wakeup } from '@/api/automation'
import i18n from '@/i18n'
import { AutomationPage, datetimeLocalToCron } from '@/routes/_app.automation'
import { server } from '../msw'

// automation 页（wave3 P3 MVP + ui-sweep 整修）：列表三态（states 组合件）、
// 创建往返（含 cron 真源校验 + 下次触发预览）、手动触发（toast + 状态 Badge）、
// 删除统一走 kit useConfirm（409 分流不变）、wakeups 列表+新建+删除。
// setup.ts 固定 i18n=en，断言用英文文案。

const AGENT = {
  id: 'agent-1',
  workspace_id: 'ws-1',
  name: 'alpha',
  description: '',
  runtime_config: {},
  max_concurrent_tasks: 1,
  visibility: 'workspace',
  archived: false,
  created_at: '2026-10-11T08:00:00Z',
}
const WI = {
  id: 'wi-1',
  workspace_id: 'ws-1',
  number: 7,
  title: 'Sweep inventory',
  description: '',
  status: 'todo',
  status_category: 'backlog',
  priority: 0,
  assignee_type: 'none',
  creator_type: 'user',
  creator_id: 'u-1',
  revision: 1,
  created_at: '2026-10-11T08:00:00Z',
  updated_at: '2026-10-11T08:00:00Z',
  last_activity_at: '2026-10-11T08:00:00Z',
}
const AP = (over: Partial<Autopilot> = {}): Autopilot => ({
  id: 'ap-1',
  workspace_id: 'ws-1',
  name: 'Daily sweep',
  exec_mode: 'create_work_item',
  template: {},
  target_agent_id: 'agent-1',
  enabled: true,
  daily_limit: 10,
  ...over,
})
const WAKEUP = (over: Partial<Wakeup> = {}): Wakeup => ({
  id: 'wu-1',
  workspace_id: 'ws-1',
  work_item_id: 'wi-1',
  kind: 'time',
  instruction: 'check alerts',
  cron_expression: '30 9 1 1 *',
  timezone: 'Asia/Shanghai',
  next_fire_at: '2099-01-01T09:30:00+08:00',
  enabled: true,
  fire_count: 0,
  ...over,
})

let autopilotList: Autopilot[]
function defaultHandlers() {
  return [
    http.get('/api/agents', () => HttpResponse.json({ agents: [AGENT] })),
    http.get('/api/work-items', () => HttpResponse.json({ items: [WI], next_cursor: null })),
    http.get('/api/work-item-wakeups', () => HttpResponse.json([])),
    http.get('/api/autopilots', () => HttpResponse.json(autopilotList)),
  ]
}
function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      {/* ui-sweep 后页面依赖全站确认（useConfirm）与成功反馈（toast）宿主 */}
      <ConfirmProvider>
        <Toaster />
        <AutomationPage />
      </ConfirmProvider>
    </QueryClientProvider>,
  )
}
// sonner 的 toast 存全局 store，跨用例残留会污染后续 renderPage 的断言面
afterEach(() => {
  toast.dismiss()
})

describe('autopilot list (three states)', () => {
  it('empty: shows onboarding copy, no rows', async () => {
    autopilotList = []
    server.use(...defaultHandlers())
    renderPage()
    expect(await screen.findByText('No autopilots yet.')).toBeInTheDocument()
    expect(screen.queryByTestId('autopilot-row')).toBeNull()
  })
  it('data: renders name, exec mode, target agent and status badge', async () => {
    autopilotList = [AP(), AP({ id: 'ap-2', name: 'Paused one', enabled: false })]
    server.use(...defaultHandlers())
    renderPage()
    const rows = await screen.findAllByTestId('autopilot-row')
    expect(rows).toHaveLength(2)
    // 行内断言（两行同 exec_mode，全局文本会命中多次）
    expect(screen.getByText('Daily sweep')).toBeInTheDocument()
    expect(screen.getByText('Paused one')).toBeInTheDocument()
    expect(within(rows[0]!).getByText('Create work item')).toBeInTheDocument()
    expect(within(rows[0]!).getByText('→ alpha')).toBeInTheDocument()
    expect(within(rows[0]!).getByText('Enabled')).toBeInTheDocument()
    expect(within(rows[1]!).getByText('Disabled')).toBeInTheDocument()
  })
  it('failure: surfaces the server error message', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [] })),
      http.get('/api/work-items', () => HttpResponse.json({ items: [], next_cursor: null })),
      http.get('/api/work-item-wakeups', () => HttpResponse.json([])),
      http.get('/api/autopilots', () =>
        HttpResponse.json({ error: 'E_INTERNAL', message: 'db down' }, { status: 500 }),
      ),
    )
    renderPage()
    expect(await screen.findByText('db down')).toBeInTheDocument()
  })
})

describe('create autopilot round-trip', () => {
  function freshList() {
    autopilotList = []
    server.use(...defaultHandlers())
  }
  it('empty cron = manual-only: posts contract shape, closes dialog, list refetches', async () => {
    freshList()
    const user = userEvent.setup()
    const bodies: unknown[] = []
    server.use(
      http.post('/api/autopilots', async ({ request }) => {
        bodies.push(await request.json())
        // 在 handler 内换列表，失效重取必然拿到新行（不受时序影响）
        autopilotList = [AP({ id: 'ap-new', name: 'Made it' })]
        return HttpResponse.json(AP({ id: 'ap-new', name: 'Made it' }), { status: 201 })
      }),
    )
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'New autopilot' }))
    await user.type(screen.getByLabelText('Name (required)'), 'Made it')
    await user.selectOptions(screen.getByLabelText('Target agent'), 'agent-1')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    // ui-sweep：时区默认浏览器 IANA 名（原为空串=UTC）
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || ''
    expect(bodies[0]).toMatchObject({
      name: 'Made it',
      exec_mode: 'create_work_item',
      target_agent_id: 'agent-1',
      cron_expression: '',
      timezone: tz,
    })
    // 对话框关闭 + 列表失效重取
    expect(await screen.findByTestId('autopilot-row')).toBeInTheDocument()
    expect(await screen.findByText('Made it')).toBeInTheDocument()
  })
  it('malformed cron: submit stays disabled, nothing is posted', async () => {
    freshList()
    const user = userEvent.setup()
    const posts = vi.fn()
    server.use(http.post('/api/autopilots', () => (posts(), HttpResponse.json(AP()))))
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'New autopilot' }))
    await user.type(screen.getByLabelText('Name (required)'), 'X')
    await user.selectOptions(screen.getByLabelText('Target agent'), 'agent-1')
    await user.type(screen.getByLabelText('Cron expression (optional)'), 'not a cron')
    expect(screen.getByLabelText('Cron expression (optional)')).toBeInvalid()
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
    expect(posts).not.toHaveBeenCalled()
  })
  it('server-rejected cron: cron-preview 400 blocks create and shows the message', async () => {
    freshList()
    const user = userEvent.setup()
    let createPosts = 0
    server.use(
      http.post('/api/autopilots/cron-preview', () =>
        HttpResponse.json(
          { error: 'E_VALIDATION', message: 'bad cron "x": expected exactly 5 fields' },
          { status: 400 },
        ),
      ),
      http.post('/api/autopilots', () => {
        createPosts += 1
        return HttpResponse.json(AP())
      }),
    )
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'New autopilot' }))
    await user.type(screen.getByLabelText('Name (required)'), 'X')
    await user.selectOptions(screen.getByLabelText('Target agent'), 'agent-1')
    await user.type(screen.getByLabelText('Cron expression (optional)'), 'x x x x x')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(
      await screen.findByText(/bad cron "x": expected exactly 5 fields/),
    ).toBeInTheDocument()
    expect(createPosts).toBe(0)
  })
})

describe('manual trigger', () => {
  it('posts trigger, toasts the dispatched feedback with a status badge', async () => {
    autopilotList = [AP()]
    server.use(
      ...defaultHandlers(),
      http.post('/api/autopilots/ap-1/trigger', () =>
        HttpResponse.json({
          id: 'run-1',
          autopilot_id: 'ap-1',
          source: 'manual',
          status: 'running',
          work_item_id: 'wi-9',
          task_id: 'task-12345678',
          planned_at: '2026-10-12T07:05:08Z',
        }),
      ),
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Trigger now' }))
    // ui-sweep：灰条直出枚举/原因码 → toast + 状态 Badge（taskStatus.running）
    expect(
      await screen.findByText('"Daily sweep" triggered; task dispatched.'),
    ).toBeInTheDocument()
    expect(await screen.findByText('Running')).toBeInTheDocument()
  })
})

describe('delete 409 branches', () => {
  it('triggers-blocked: shows the remove-triggers-first hint, row stays', async () => {
    autopilotList = [AP()]
    server.use(
      ...defaultHandlers(),
      http.delete('/api/autopilots/ap-1', () =>
        HttpResponse.json(
          {
            error: 'E_REVISION_CONFLICT',
            message: 'autopilot still has enabled triggers; disable or delete them first',
          },
          { status: 409 },
        ),
      ),
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await user.click(await screen.findByRole('button', { name: 'Confirm delete' }))
    expect(
      await screen.findByText(
        'Delete blocked: this autopilot still has enabled triggers; remove or disable them first.',
      ),
    ).toBeInTheDocument()
    expect(screen.getByTestId('autopilot-row')).toBeInTheDocument()
  })
  it('runs-blocked: shows the retry-later hint', async () => {
    autopilotList = [AP()]
    server.use(
      ...defaultHandlers(),
      http.delete('/api/autopilots/ap-1', () =>
        HttpResponse.json(
          { error: 'E_REVISION_CONFLICT', message: 'autopilot has pending/running runs' },
          { status: 409 },
        ),
      ),
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await user.click(await screen.findByRole('button', { name: 'Confirm delete' }))
    expect(
      await screen.findByText(
        'Delete blocked: this autopilot has pending/running runs; retry later.',
      ),
    ).toBeInTheDocument()
  })
})

describe('wakeups section', () => {
  it('renders rows; system rules show no delete affordance', async () => {
    autopilotList = []
    // 具体覆盖放前面：msw 同一批 handler 先注册者先匹配
    server.use(
      http.get('/api/work-item-wakeups', () =>
        HttpResponse.json([
          WAKEUP(),
          WAKEUP({
            id: 'wu-sys',
            kind: 'system',
            system_rule: 'children_done',
            enabled: false,
            instruction: 'children-done system instruction',
          }),
        ]),
      ),
      ...defaultHandlers(),
    )
    renderPage()
    const rows = await screen.findAllByTestId('wakeup-row')
    expect(rows).toHaveLength(2)
    expect(screen.getByText('check alerts')).toBeInTheDocument()
    // 行内元信息是多段文本节点，用子串匹配；两行同一 work item 都解析出标题
    expect(screen.getAllByText(/#7 Sweep inventory/)).toHaveLength(2)
    expect(screen.getByText('System rule, cannot be deleted')).toBeInTheDocument()
    // 仅时间型行有删除按钮；系统规则行没有
    expect(screen.getAllByRole('button', { name: 'Delete' })).toHaveLength(1)
  })
  it('create round-trip: datetime-local becomes a one-shot 5-field cron with max_fires=1', async () => {
    autopilotList = []
    let wakeups: Wakeup[] = []
    const bodies: unknown[] = []
    server.use(
      http.get('/api/work-item-wakeups', () => HttpResponse.json(wakeups)),
      http.post('/api/work-items/wi-1/wakeups', async ({ request }) => {
        bodies.push(await request.json())
        const created = WAKEUP({ id: 'wu-new', instruction: 'wake up' })
        wakeups = [created]
        return HttpResponse.json(created, { status: 201 })
      }),
      ...defaultHandlers(),
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'New wakeup' }))
    await user.selectOptions(screen.getByLabelText('Target work item (required)'), 'wi-1')
    await user.type(screen.getByLabelText('Instruction (required, ≤4000 chars)'), 'wake up')
    fireEvent.change(screen.getByLabelText('Fire at (one-shot)'), {
      target: { value: '2099-01-01T09:30' },
    })
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || ''
    expect(bodies[0]).toEqual({
      kind: 'time',
      instruction: 'wake up',
      cron_expression: '30 9 1 1 *',
      timezone: tz,
      max_fires: 1,
    })
    expect(await screen.findByText('wake up')).toBeInTheDocument()
  })
  it('past fire time: submit stays disabled', async () => {
    autopilotList = []
    server.use(...defaultHandlers())
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'New wakeup' }))
    await user.selectOptions(screen.getByLabelText('Target work item (required)'), 'wi-1')
    await user.type(screen.getByLabelText('Instruction (required, ≤4000 chars)'), 'wake up')
    fireEvent.change(screen.getByLabelText('Fire at (one-shot)'), {
      target: { value: '2020-01-01T09:30' },
    })
    expect(screen.getByText('Fire time must be in the future')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()
  })
  it('delete: calls the work-item-scoped DELETE and drops the row', async () => {
    autopilotList = []
    let wakeups: Wakeup[] = [WAKEUP()]
    const dels: string[] = []
    server.use(
      http.get('/api/work-item-wakeups', () => HttpResponse.json(wakeups)),
      http.delete('/api/work-items/wi-1/wakeups/wu-1', () => {
        dels.push('wu-1')
        wakeups = []
        return new HttpResponse(null, { status: 204 })
      }),
      ...defaultHandlers(),
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await user.click(await screen.findByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(dels).toEqual(['wu-1']))
    await waitFor(() => expect(screen.queryByTestId('wakeup-row')).toBeNull())
  })
})

describe('datetimeLocalToCron', () => {
  it('converts without leading zeros and rejects garbage', () => {
    expect(datetimeLocalToCron('2099-01-01T09:30')).toEqual({
      cron: '30 9 1 1 *',
      at: new Date(2099, 0, 1, 9, 30),
    })
    expect(datetimeLocalToCron('2099-12-31T23:05')?.cron).toBe('5 23 31 12 *')
    expect(datetimeLocalToCron('nope')).toBeNull()
    expect(datetimeLocalToCron('')).toBeNull()
  })
})

describe('automation i18n keys exist in both locales', () => {
  const KEYS = [
    'automation.pageTitle',
    'automation.newBtn',
    'automation.newTitle',
    'automation.nameLabel',
    'automation.namePlaceholder',
    'automation.execModeLabel',
    'automation.targetAgentLabel',
    'automation.targetAgentPick',
    'automation.agentsLoading',
    'automation.promptLabel',
    'automation.promptPlaceholder',
    'automation.workItemLabel',
    'automation.workItemPick',
    'automation.cronLabel',
    'automation.cronHint',
    'automation.cronInvalid',
    'automation.cronNext',
    'automation.timezoneLabel',
    'automation.tzUtc',
    'automation.creating',
    'automation.delBlockedTriggers',
    'automation.delBlockedRuns',
    'automation.deleteApTitle',
    'automation.deleteConfirmDesc',
    'automation.wakeDeleteTitle',
    'automation.wakeDeleteDesc',
    'automation.wakeTitle',
    'automation.wakeEmpty',
    'automation.wakeNewBtn',
    'automation.wakeNewTitle',
    'automation.wakeKindTime',
    'automation.wakeKindEvent',
    'automation.wakeKindCondition',
    'automation.wakeKindSystem',
    'automation.wakeEvery',
    'automation.wakeNext',
    'automation.wakeWorkItemLabel',
    'automation.wakeWorkItemShort',
    'automation.wakeInstructionLabel',
    'automation.wakeInstructionPlaceholder',
    'automation.wakeTimeLabel',
    'automation.wakePastTime',
    'automation.wakeSystemNote',
  ] as const
  it.each(KEYS)('%s exists in both locales', (key) => {
    // 缺键时 i18n.t 回退返回键名本身——不等于键名即存在
    expect(i18n.t(key, { lng: 'en' })).not.toBe(key)
    expect(i18n.t(key, { lng: 'zh' })).not.toBe(key)
  })
})
