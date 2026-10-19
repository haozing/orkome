import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import { ConfirmProvider, toast } from '@ploykit/ui'
import type { Skill, SkillFile } from '@/api/skills'
import { SkillsPage } from '@/routes/_app.skills.index'
import { SkillDetailPage } from '@/routes/_app.skills.$skillId'
import { server } from '../msw'

// ui-sweep skills 域整修钉子：
// ① 列表页两弹窗换本地 dialog 骨架 + kit Field/FieldLabel + 本地 Tabs（url/zip 导入）；
// ② 详情页删除×2 统一 useConfirm（deleteHint 只进 description）、refresh 走 toast、
//    Loader2 并入「重新拉取」按钮；
// ③ FileRow 删除钮 opacity-0 + group-hover/focus-within（键盘可达）；
// ④ 搜索无结果分支不再借 emptyDesc 的"手工新建或导入"文案（新键 skills.noMatchDesc）。
// setup.ts 固定 i18n=en；被 patch 触碰的文案一律用 i18n.t() 取当前值断言，
// 键存在性测试（最后一组）在 i18n patch 落地前为红——契约先行。
// 路由桩沿 work-item-detail-tabs 的做法：createFileRoute 返回带 useParams/useNavigate
// 的轻对象，Link 渲染成 <a>。

const st = vi.hoisted(() => ({
  params: { skillId: 'sk-1' },
  navigate: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute:
      () =>
      (options: Record<string, unknown>) => ({
        options,
        useParams: () => st.params,
        useNavigate: () => st.navigate,
      }),
    // 页面组件从模块顶层 import useNavigate（render 期调用、回调里触发导航），
    // 必须一并桩掉，否则拿到真实实现——RouterProvider 外导航是 no-op
    useNavigate: () => st.navigate,
    // 渲染成 <a>；params 是路由参数对象，剥掉避免 React 未知属性告警
    Link: ({ params, ...props }: { params?: unknown } & React.ComponentProps<'a'>) => {
      void params
      return <a {...props} />
    },
  }
})

// 重子组件轻桩：编辑器内核（tiptap）不在本测试射程
vi.mock('@/components/editor/rich-editor', () => ({
  RichEditor: ({ content, ariaLabel }: { content: string; ariaLabel?: string }) => (
    <div data-testid="stub-rich-editor" aria-label={ariaLabel}>
      {content}
    </div>
  ),
}))

const SKILL: Skill = {
  id: 'sk-1',
  workspace_id: 'ws-1',
  name: 'seo-style',
  description: 'SEO writing guide',
  origin: 'url',
  created_at: '2026-10-11T08:00:00Z',
}
const MAIN_MD: SkillFile = {
  id: 'f-1',
  path: 'SKILL.md',
  content: '---\nname: seo-style\ndescription: SEO writing guide\n---\n\n# Body',
  sha256: 'a'.repeat(64),
  size_bytes: 60,
}
const NOTES_TXT: SkillFile = {
  id: 'f-2',
  path: 'notes.txt',
  content: 'plain notes',
  sha256: 'b'.repeat(64),
  size_bytes: 11,
}

function renderPage(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ConfirmProvider>{ui}</ConfirmProvider>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  st.navigate.mockClear()
})

function indexHandlers(list: Skill[]) {
  return [
    http.get('/api/skills/search', () => HttpResponse.json({ skills: [] })),
    http.get('/api/skills', () => HttpResponse.json({ skills: list })),
  ]
}

describe('skills list page', () => {
  it('h1 uses the nav.skills key, not a hardcoded literal source', async () => {
    server.use(...indexHandlers([]))
    renderPage(<SkillsPage />)
    expect(await screen.findByRole('heading', { level: 1, name: 'Skills' })).toBeInTheDocument()
  })

  it('true empty: onboarding title + emptyDesc (patched copy)', async () => {
    server.use(...indexHandlers([]))
    renderPage(<SkillsPage />)
    expect(await screen.findByText('No skills yet')).toBeInTheDocument()
    // 空态描述随 patch 改写（管理导向），按当前词条取值断言
    expect(screen.getByText(i18n.t('skills.emptyDesc'))).toBeInTheDocument()
  })

  it('no search match: keeps the match title but drops the misleading create/import copy', async () => {
    server.use(...indexHandlers([]))
    const user = userEvent.setup()
    renderPage(<SkillsPage />)
    await user.type(await screen.findByLabelText('Search skills'), 'zzz')
    // 400ms 防抖后打 search 端点
    await waitFor(
      () => {
        expect(screen.getByText('No matching skills')).toBeInTheDocument()
      },
      { timeout: 2000 },
    )
    // 失实分支修复：不再借用 emptyDesc 的"手工新建或导入"
    expect(screen.queryByText(i18n.t('skills.emptyDesc'))).not.toBeInTheDocument()
    expect(screen.getByText(i18n.t('skills.noMatchDesc'))).toBeInTheDocument()
  })

  it('create dialog round-trip: posts name/description, closes; no createHint line', async () => {
    server.use(...indexHandlers([]))
    const bodies: unknown[] = []
    server.use(http.post('/api/skills', async ({ request }) => {
      bodies.push(await request.json())
      return HttpResponse.json(SKILL, { status: 201 })
    }))
    const user = userEvent.setup()
    renderPage(<SkillsPage />)
    await user.click(await screen.findByRole('button', { name: 'New skill' }))
    const dlg = screen.getByRole('dialog')
    // 本地 dialog 骨架 + kit Field/FieldLabel（label htmlFor 关联输入）
    await user.type(within(dlg).getByPlaceholderText('e.g. Translation style guide'), 'seo-style')
    await user.type(
      within(dlg).getByPlaceholderText('What this skill teaches the agent to do and how'),
      'guide',
    )
    // createHint 键随 patch 删除，消费行已移除
    expect(within(dlg).queryByText(/After creating, edit the SKILL.md/)).toBeNull()
    await user.click(within(dlg).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).toMatchObject({ name: 'seo-style', description: 'guide' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(st.navigate).toHaveBeenCalledWith({
      to: '/skills/$skillId',
      params: { skillId: SKILL.id },
    })
  })

  it('import dialog: local Tabs switch url/zip; zip posts multipart with the file', async () => {
    server.use(...indexHandlers([]))
    const forms: unknown[] = []
    server.use(http.post('/api/skills/import', async ({ request }) => {
      forms.push(await request.formData())
      return HttpResponse.json(SKILL, { status: 201 })
    }))
    const user = userEvent.setup()
    renderPage(<SkillsPage />)
    await user.click(await screen.findByRole('button', { name: 'Import' }))
    const dlg = screen.getByRole('dialog')
    // 手写 role=tablist 换本地 tabs 组件（base-ui 语义）
    expect(within(dlg).getByRole('tablist', { name: 'Import mode' })).toBeInTheDocument()
    expect(within(dlg).getByRole('tab', { name: 'From URL' })).toBeInTheDocument()
    await user.click(within(dlg).getByRole('tab', { name: 'Upload zip' }))
    const fileInput = within(dlg).getByLabelText('Choose zip file') as HTMLInputElement
    await user.upload(fileInput, new File(['zip-bytes'], 'pkg.zip', { type: 'application/zip' }))
    expect(within(dlg).getByText('Selected: pkg.zip')).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: 'Import' }))
    await waitFor(() => expect(forms).toHaveLength(1))
    // msw(node) 用 undici 反序列化 multipart：跨 realm 认不出 jsdom File，文件名
    // 被重置为 'blob'（未命名 blob 回退）；类型与字节原样保留。文件名已由上面
    // 「Selected: pkg.zip」钉住，这里钉 multipart 里确实带上了所选文件本体。
    const uploaded = (forms[0] as FormData).get('file') as { type: string; size: number }
    expect(uploaded.type).toBe('application/zip')
    expect(uploaded.size).toBe('zip-bytes'.length)
    expect(st.navigate).toHaveBeenCalled()
  })
})

describe('skill detail page', () => {
  function detailHandlers(over: HttpHandler[] = [], files: SkillFile[] = [MAIN_MD, NOTES_TXT]) {
    return [
      http.get('/api/skills/sk-1', () => HttpResponse.json(SKILL)),
      http.get('/api/skills/sk-1/files', () =>
        HttpResponse.json({ files, manifest_sha256: 'm'.repeat(64) }),
      ),
      http.get('/api/skills/sk-1/agents', () => HttpResponse.json({ agents: [] })),
      ...over,
    ]
  }

  it('renders header, file list; main file has no delete affordance, others do', async () => {
    server.use(...detailHandlers())
    renderPage(<SkillDetailPage />)
    expect(await screen.findByRole('heading', { level: 1, name: 'seo-style' })).toBeInTheDocument()
    // 文件名同时出现在列表行与右侧编辑器头（默认选中 SKILL.md）——限定列表范围
    const fileList = within(screen.getByRole('list'))
    expect(fileList.getByText('SKILL.md')).toBeInTheDocument()
    expect(fileList.getByText('notes.txt')).toBeInTheDocument()
    // opacity-0 + group-hover/focus-within：键盘可达（按钮常驻 DOM，不再 display:none）
    expect(screen.getByRole('button', { name: 'Delete notes.txt' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete SKILL.md' })).toBeNull()
  })

  it('frontmatter fields are kit Field rows prefilled from the header', async () => {
    server.use(...detailHandlers())
    renderPage(<SkillDetailPage />)
    const nameInput = await screen.findByLabelText(i18n.t('skills.fmName'))
    expect(nameInput).toHaveValue('seo-style')
    expect(screen.getByLabelText(i18n.t('skills.fmDesc'))).toHaveValue('SEO writing guide')
  })

  it('non-markdown file edits in a plain textarea and PUTs on save', async () => {
    server.use(...detailHandlers())
    const bodies: unknown[] = []
    server.use(http.put('/api/skills/sk-1/files', async ({ request }) => {
      bodies.push(await request.json())
      return HttpResponse.json({ file: NOTES_TXT, manifest_sha256: 'm'.repeat(64) })
    }))
    const user = userEvent.setup()
    renderPage(<SkillDetailPage />)
    await user.click(await screen.findByText('notes.txt'))
    const editor = await screen.findByLabelText('Edit notes.txt')
    expect(editor).toBeInstanceOf(HTMLTextAreaElement)
    await user.type(editor, '!')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).toMatchObject({ path: 'notes.txt', content: 'plain notes!' })
  })

  it('refetch: spinner lives inside the button and result comes back as a toast', async () => {
    server.use(
      ...detailHandlers([
        http.post('/api/skills/sk-1/refresh', () => HttpResponse.json({ refreshed: 2 })),
      ]),
    )
    const successSpy = vi.spyOn(toast, 'success')
    const user = userEvent.setup()
    renderPage(<SkillDetailPage />)
    await user.click(await screen.findByRole('button', { name: 'Refetch' }))
    await waitFor(() => expect(successSpy).toHaveBeenCalledWith('Refetched 2 file(s)'))
    successSpy.mockRestore()
  })

  it('refetch failure surfaces via toast.error instead of vanishing', async () => {
    server.use(
      ...detailHandlers([
        http.post('/api/skills/sk-1/refresh', () =>
          HttpResponse.json({ error: 'E_UPSTREAM', message: 'remote 500' }, { status: 502 }),
        ),
      ]),
    )
    const errorSpy = vi.spyOn(toast, 'error')
    const user = userEvent.setup()
    renderPage(<SkillDetailPage />)
    await user.click(await screen.findByRole('button', { name: 'Refetch' }))
    await waitFor(() => expect(errorSpy).toHaveBeenCalledWith('remote 500'))
    errorSpy.mockRestore()
  })

  it('skill delete: useConfirm with the consequence copy as description; confirm navigates back', async () => {
    server.use(
      ...detailHandlers([
        http.delete('/api/skills/sk-1', () => HttpResponse.json({ result: 'ok' })),
      ]),
    )
    const user = userEvent.setup()
    renderPage(<SkillDetailPage />)
    await user.click(await screen.findByRole('button', { name: 'Delete skill' }))
    // deleteHint 只在确认框 description 说一次（页面常驻段已删）
    const confirmBox = await screen.findByRole('alertdialog')
    expect(within(confirmBox).getByText(i18n.t('skills.deleteHint'))).toBeInTheDocument()
    expect(within(confirmBox).getByText('Delete skill')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(st.navigate).toHaveBeenCalledWith({ to: '/skills' }))
  })

  it('skill delete: cancel keeps the page intact', async () => {
    const dels = vi.fn()
    server.use(...detailHandlers([http.delete('/api/skills/sk-1', () => (dels(), HttpResponse.json({ result: 'ok' })))]))
    const user = userEvent.setup()
    renderPage(<SkillDetailPage />)
    await user.click(await screen.findByRole('button', { name: 'Delete skill' }))
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(dels).not.toHaveBeenCalled()
    expect(screen.getByRole('heading', { level: 1, name: 'seo-style' })).toBeInTheDocument()
  })

  it('file delete: hover-revealed button goes through useConfirm and drops the row', async () => {
    let files: SkillFile[] = [MAIN_MD, NOTES_TXT]
    // 具体覆盖放前面：msw 同一批 handler 先注册者先匹配
    server.use(
      http.get('/api/skills/sk-1/files', () =>
        HttpResponse.json({ files, manifest_sha256: 'm'.repeat(64) }),
      ),
      http.delete('/api/skills/sk-1/files/f-2', () => {
        files = [MAIN_MD]
        return HttpResponse.json({ result: 'ok', manifest_sha256: 'n'.repeat(64) })
      }),
      ...detailHandlers(),
    )
    const user = userEvent.setup()
    renderPage(<SkillDetailPage />)
    await user.click(await screen.findByRole('button', { name: 'Delete notes.txt' }))
    const confirmBox = await screen.findByRole('alertdialog')
    expect(within(confirmBox).getByText('Delete notes.txt')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Delete notes.txt' })).toBeNull(),
    )
  })
})

describe('skills i18n keys exist in both locales', () => {
  // noMatchDesc 为 ui-sweep patch 新键：patch 落地前此组为红（契约先行）
  const KEYS = [
    'nav.skills',
    'skills.noMatchDesc',
    'skills.emptyTitle',
    'skills.emptyDesc',
    'skills.nameLabel',
    'skills.descLabel',
    'skills.urlLabel',
    'skills.fmName',
    'skills.fmDesc',
    'skills.filePathPlaceholder',
    'skills.pickFile',
    'skills.noFiles',
    'skills.confirmDelete',
    'skills.deleteHint',
    'skills.deleteFileAria',
  ] as const
  it.each(KEYS)('%s exists in both locales', (key) => {
    // 缺键时 i18n.t 回退返回键名本身——不等于键名即存在
    expect(i18n.t(key, { lng: 'en' })).not.toBe(key)
    expect(i18n.t(key, { lng: 'zh' })).not.toBe(key)
  })

  it('dead keys are gone: createHint / frontmatterHint / frontmatterFix / frontmatterTemplateDesc', () => {
    // i18n patch 要求删除的四个死键——t 回退返回键名即视为已删
    for (const key of [
      'skills.createHint',
      'skills.frontmatterHint',
      'skills.frontmatterFix',
      'skills.frontmatterTemplateDesc',
    ]) {
      expect(i18n.t(key, { lng: 'en' })).toBe(key)
      expect(i18n.t(key, { lng: 'zh' })).toBe(key)
    }
  })
})
