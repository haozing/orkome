import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Settings } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import { DropdownMenuItem, WorkspaceSwitcher as KitWorkspaceSwitcher } from '@ploykit/ui'
import { createWorkspace, meQueryOptions, type Workspace } from '@/api/identity'
import { switchWorkspace } from '@/services/workspace-switch'
import { useWorkspaceStore } from '@/stores/workspace'

// 工作区切换器：kit WorkspaceSwitcher（canonical size-lg 卡片 + 内联新建表单）
// 的 orkome 适配层。数据经 kit 的 `data` 注入缝喂入（字段级 merge over
// useWorkspaceSwitch）——**单一数据源纪律：zustand currentId 仍是唯一真相**
// （X-Workspace-Id 头经 setWorkspaceIdProvider 读它，见 main.tsx 的
// WorkspaceHeaderAuthorityBridge）。kit WorkspaceLayer 的内部 current 因字段
// merge 对本组件不生效，且 main.tsx 桥保证它也抢不走 API 头；list 取自
// orkome meQueryOptions 缓存（kit 侧 ['workspace','list'] 与本缓存互不相干）。
// kit 组件文案经 _app.tsx 的 KitLabelsProvider 注入（i18n 随语言切换）。

// slug 约定照搬 kit 内置 slugify（满足后端/引导页规则：小写字母数字短横线、
// 4-40 位、不以短横线开头结尾；过短补随机后缀）。注 入 create 是单参契约，
// slug 决策归消费方——即此处。
const slugSuffix = (n = 4): string => {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let out = ''
  for (let i = 0; i < n; i++) out += alphabet[Math.floor(Math.random() * alphabet.length)]
  return out
}
const slugify = (s: string): string => {
  let slug = s
    .toLowerCase()
    .replace(/[^a-z0-9-]/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-+|-+$/g, '')
  if (slug.length > 40) slug = slug.slice(0, 40).replace(/-+$/g, '')
  if (slug.length < 4) slug = `${slug || 'ws'}-${slugSuffix()}`
  return slug
}

const toSwitcherItem = (ws: Workspace) => ({
  id: ws.id,
  name: ws.name,
  slug: ws.slug,
  plan_code: ws.plan_code,
})

export function WorkspaceSwitcher() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const me = useQuery(meQueryOptions)
  const currentId = useWorkspaceStore((s) => s.currentId)
  const workspaces = me.data?.workspaces ?? []
  const current = workspaces.find((ws) => ws.id === currentId) ?? null

  // 创建成功后：失效 orkome me 缓存（切换器列表刷新）并走 orkome 切换语义
  // （zustand 置新 + 清缓存 + ws 重连）；kit 侧的 ['workspace','list'] 不失效——
  // WorkspaceLayer 的内部 current 已被桥隔离，不影响任何行为。
  const create = useMutation({
    mutationFn: (name: string) => createWorkspace(name, slugify(name)),
    onSuccess: (ws) => {
      void queryClient.invalidateQueries({ queryKey: meQueryOptions.queryKey })
      switchWorkspace(queryClient, ws.id)
    },
  })
  // 注入 error 契约为 string | null（消费方本地化）；ApiError 带服务端消息，
  // 直接展示（同 project-nav 惯例）。注意：字符串契约下 kit 的配额指引分支
  // （isApiError 判定）不会触发——orkome 无计费页，故也不传 billingHref。
  const createError = create.error
    ? create.error instanceof ApiError
      ? create.error.message
      : t('kitLabels.createErrorFallback')
    : null

  return (
    <KitWorkspaceSwitcher
      data={{
        current: current ? toSwitcherItem(current) : null,
        list: workspaces.map(toSwitcherItem),
        switchTo: (id) => switchWorkspace(queryClient, id),
        create: async (name) => {
          await create.mutateAsync(name)
        },
        creating: create.isPending,
        error: createError,
      }}
      menuFooter={
        // 工作区设置页入口（IA 裁决：工作区内容自 /settings#workspace 迁出为
        // 独立页 /settings/workspace；个人设置留 /settings，由用户面板进入）。
        // kit 的 billingHref 不传：orkome 无计费页（配额分支见上注，实际不可达）。
        <DropdownMenuItem
          className="gap-2 p-2"
          onClick={() => {
            void navigate({ to: '/settings/workspace' })
          }}
        >
          <Settings className="size-4" aria-hidden="true" />
          <span className="whitespace-nowrap font-medium text-muted-foreground">
            {t('kitLabels.workspaceSettings')}
          </span>
        </DropdownMenuItem>
      }
    />
  )
}
