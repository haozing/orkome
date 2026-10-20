import { createFileRoute, Link, Outlet, redirect, useNavigate } from '@tanstack/react-router'
import {
  Bell,
  BookOpen,
  Bot,
  DoorOpen,
  KeyRound,
  ListTodo,
  ScrollText,
  Server,
  Shield,
  User,
  UserCircle,
  Users,
  Wrench,
  Zap,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { meQueryOptions, type Me } from '@/api/identity'
import { LanguageToggle } from '@/components/language-toggle'
import { NotificationBell } from '@/components/notification-bell'
import { ThemeToggle } from '@/components/theme-toggle'
import { PackEntries } from '@/components/pack-entries'
import { ProjectNav } from '@/components/project-nav'
import { WorkspaceSwitcher } from '@/components/workspace-switcher'
import { WsStatusBadge } from '@/components/ws-status-badge'
import {
  AppShell as PloykitAppShell,
  KitLabelsProvider,
  type AppShellNavGroup,
  type KitLabels,
  type NavItem,
  type UserMenuExtraItem,
} from '@ploykit/ui'
import { ApiError } from '@ploykit/client'
import { cn } from '@/lib/utils'
import { wsClient } from '@/realtime/client'
import { useWorkspaceStore } from '@/stores/workspace'
export const Route = createFileRoute('/_app')({
  beforeLoad: async ({ context }) => {
    let me: Me
    try {
      me = await context.queryClient.ensureQueryData(meQueryOptions)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        throw redirect({ to: '/login' })
      }
      throw err
    }
    if (me.workspaces.length === 0) {
      throw redirect({ to: '/onboarding' })
    }
    const current = useWorkspaceStore.getState().currentId
    if (!current || !me.workspaces.some((w) => w.id === current)) {
      useWorkspaceStore.getState().setCurrent(me.workspaces[0]!.id)
    }
  },
  component: AppLayout,
})
const NAV_ITEMS = [
  { to: '/my-work', label: 'nav.myWork', icon: User },
  { to: '/notifications', label: 'nav.notifications', icon: Bell },
  { to: '/work-items', label: 'nav.allWork', icon: ListTodo },
] as const
const BOTTOM_NAV_ITEMS = [
  { to: '/access', label: 'nav.access', icon: DoorOpen },
  { to: '/automation', label: 'nav.automation', icon: Zap },
] as const
const AI_TEAM_NAV_ITEMS = [
  { to: '/agents', label: 'nav.agents', icon: Bot },
  { to: '/squads', label: 'nav.squads', icon: Users },
  { to: '/skills', label: 'nav.skills', icon: BookOpen },
  { to: '/tools', label: 'nav.integrations', icon: Wrench },
  { to: '/run-logs', label: 'nav.runLogs', icon: ScrollText },
] as const
const NAV_LINK_CLASS =
  'flex items-center gap-2 rounded-md px-3 py-1.5 text-sm text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800'
const NAV_ACTIVE_CLASS = cn(
  'font-medium text-zinc-900 dark:text-zinc-100',
  'bg-zinc-100 dark:bg-zinc-800',
)
// —— kit AuthContext 归属 kit 本体（上轮 AuthBridge 已删）——
// PloykitProvider（main.tsx）挂载后 kit AuthLayer 自供 user（/auth/me 401→null），
// v0.10.3 起 UserMenu/SidebarUserMenu/ImpersonationBanner 走 useAuthSafe，壳树
// 不再需要裸 AuthContext.Provider 桥。登出走 kit SidebarUserMenu 内置项：
// kit logout（POST /auth/logout + me/workspaces 缓存置空 + 注入的 orkome
// queryClient 全量 removeQueries）+ 组件自带 navigate('/login')；kit 语义缺的
// zustand 复位与 wsClient.close() 由 main.tsx 的 KitLogoutSideEffects 监听
// user 跃迁补齐。orkome 侧全量清理仍在 settings/shared.tsx 的
// signOutEverywhere（吊销全部会话 / MFA 关闭场景）。
// NavItem.icon 是 ReactNode 插槽；形状对齐 ploykit canonical（lucide + size-4）。
const navIcon = (Icon: LucideIcon) => <Icon className="size-4 shrink-0" aria-hidden="true" />
const toNavItems = (
  items: ReadonlyArray<{ to: string; label: string; icon: LucideIcon }>,
  t: (key: string) => string,
): NavItem[] =>
  items.map((item) => ({ path: item.to, label: t(item.label), icon: navIcon(item.icon) }))
// kit 组件文案 → orkome 字典（解析序：组件 prop → 本 Provider → kit 内置中文）。
// 既有键直接映射；kit 特有文案进 kitLabels.* 段（主线 i18n patch 清单见交付报告）。
// useTranslation 可用树内挂载 → 语言切换即重算（useMemo 依赖 t）。
function useKitLabelsFromI18n(): KitLabels {
  const { t } = useTranslation()
  return useMemo<KitLabels>(
    () => ({
      // ---- WorkspaceSwitcher ----
      workspaceSwitcherAria: t('kitLabels.workspaceSwitcherAria'),
      workspacePickerPlaceholder: t('ws.selectWorkspace'),
      workspaceGroupLabel: t('ws.groupLabel'),
      createWorkspace: t('kitLabels.createWorkspace'),
      workspaceNamePlaceholder: t('kitLabels.workspaceNamePlaceholder'),
      newWorkspaceNameAria: t('kitLabels.newWorkspaceNameAria'),
      creating: t('kitLabels.creating'),
      create: t('common.create'),
      cancel: t('common.cancel'),
      createErrorFallback: t('kitLabels.createErrorFallback'),
      workspaceCreatedToast: (name: string) => t('kitLabels.workspaceCreatedToast', { name }),
      // 配额指引三段（quotaBillingHref 不映射：orkome 无计费页，切换器不传
      // billingHref，且 data 注入缝的字符串 error 契约下该分支本就不可达）。
      quotaGuidancePrefix: t('kitLabels.quotaGuidancePrefix'),
      quotaGuidanceLinkText: t('kitLabels.quotaGuidanceLinkText'),
      quotaGuidanceSuffix: t('kitLabels.quotaGuidanceSuffix'),
      // ---- DataTable（orkome 暂未消费，前瞻映射）----
      paginationTotal: (total: number, page: number, pages: number) =>
        t('mcp.pageInfo', { page, pages, total }),
      paginationLoading: t('common.loading'),
      previousPage: t('mcp.pagePrev'),
      nextPage: t('mcp.pageNext'),
      emptyState: t('kitLabels.emptyState'),
      // ---- SecretModal ----
      confirmSaved: t('kitLabels.confirmSaved'),
      confirmPending: t('kitLabels.confirmPending'),
      // ---- UserMenu / SidebarUserMenu ----
      // accountSettings 不映射（undefined → 落回 kit 内置「账户设置」）。kit
      // 无隐藏机制（UserMenuProps.labels 仅覆盖文案，条目恒渲染）；但 orkome
      // 用 userMenu="sidebar-footer"（SidebarUserMenu），该模式本就不渲染内置
      // 「账户设置」项，只有 header UserMenu 才有——当前形态下此键实际不消费。
      accountSettings: undefined,
      logout: t('common.logout'),
      userMenuAria: t('kitLabels.userMenuAria'),
      logoutFailedToast: t('kitLabels.logoutFailedToast'),
    }),
    [t],
  )
}
function AppLayout() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  useEffect(() => wsClient.connect(), [])
  const mainNav: NavItem[] = toNavItems(NAV_ITEMS, t)
  const aiTeamNav: AppShellNavGroup = {
    // 分组标签复用既有 nav.aiTeam 键（旧壳同款文案位；本波次禁止新增非 kitLabels i18n 键）。
    group: t('nav.aiTeam'),
    items: toNavItems(AI_TEAM_NAV_ITEMS, t),
  }
  // 用户面板（SidebarUserMenu 弹出）= 个人设置入口组，形态对齐 ploykit
  // example 的 userPanelItems 四件套（个人资料/安全/访问令牌/通知偏好）。
  // orkome 个人设置仍是单页 /settings（锚点分区），故各项落为「/settings +
  // hash」；侧栏「设置」按钮已随 IA 裁决移除，工作区设置由切换器 menuFooter
  // 进入（workspace-switcher.tsx，非本文件职责）。
  // 注意必须走 onClick + navigate({ to, hash })：kit SidebarUserMenu 对 to 是
  // navigate(item.to) 字符串直达，TanStack 只对 href 拆 hash、对 to 不拆
  // （to 带'#'会被整段当路径匹配→not-found），与 _app.access.tsx 的
  // <Link to hash> 同一约束。锚点 id 与 _app.settings.index.tsx 分区一一对应。
  const userMenuItems: UserMenuExtraItem[] = (
    [
      { labelKey: 'userPanel.profile', icon: UserCircle, to: '/settings', hash: 'account' },
      { labelKey: 'userPanel.security', icon: Shield, to: '/settings', hash: 'account' },
      { labelKey: 'userPanel.tokens', icon: KeyRound, to: '/settings', hash: 'tokens' },
      { labelKey: 'userPanel.notifications', icon: Bell, to: '/settings', hash: 'account' },
      { labelKey: 'userPanel.server', icon: Server, to: '/settings', hash: 'server' },
    ] as const
  ).map(({ labelKey, icon, to, hash }) => ({
    label: t(labelKey),
    icon: navIcon(icon),
    onClick: () => void navigate({ to, hash }),
  }))
  const kitLabels = useKitLabelsFromI18n()
  return (
    <KitLabelsProvider labels={kitLabels}>
      <PloykitAppShell
        brand="Orkome"
        nav={mainNav}
        navGroups={[aiTeamNav]}
        userMenu="sidebar-footer"
        userMenuItems={userMenuItems}
        sidebarHeader={
          /* sidebarHeader 槽整体替换 AppShell 默认 logo+brand 块（其文档语义）。
             品牌形态裁决（用户两轮迭代）：词标并排（互抢重心×）→ 字母块 mark
             （与切换器头像双黑块并排更丑×）→ **B 形态=品牌完全退位**，侧栏头
             只留 kit canonical 切换器大卡（Notion/Linear/Slack 同款）。品牌由
             页面 title/favicon/登录页承载；真 logo mark（几何异形非字母块）
             设计出来后再回 Vercel 式。 */
          <WorkspaceSwitcher />
        }
        sidebarMiddle={
          /* 项目导航回导航区（上轮 navGroups 纯链接语义装不下 ProjectNav 的
             管理动作：新建/悬浮编辑流程/星标；用户反馈"项目跑到下面来了"）。
             sidebarMiddle 渲染在平铺 nav 之后、navGroups 之前，恢复旧壳
             "项目在 AI 团队之上"的顺序。折叠态兜底同 footer：ProjectNav 根
             元素自带 group-data-[collapsible=icon]:hidden。 */
          <ProjectNav />
        }
        sidebarFooter={
          /* Pack 深链入口 + 底部导航（助理接入/自动化；「设置」已删——个人
             设置入口收进用户面板，工作区设置走切换器 menuFooter）。
             折叠态兜底同前。 */
          <div className="group-data-[collapsible=icon]:hidden">
            <PackEntries workspaceId={workspaceId} />
            <nav className="mt-2 flex flex-col gap-1 border-t border-zinc-200 pt-2 dark:border-zinc-800">
              {BOTTOM_NAV_ITEMS.map((item) => (
                <Link
                  key={item.to}
                  to={item.to}
                  className={NAV_LINK_CLASS}
                  activeProps={{ className: NAV_ACTIVE_CLASS }}
                >
                  <item.icon className="size-4 shrink-0" aria-hidden="true" />
                  {t(item.label)}
                </Link>
              ))}
            </nav>
          </div>
        }
        headerRight={
          /* 旧壳头部右侧五件中的四件，顺序保持：状态徽标 → 铃铛 → 主题 → 语言。
             登出按钮移入侧栏底部用户卡（内置退出项走 kit AuthLayer logout）。 */
          <>
            <WsStatusBadge />
            <NotificationBell />
            <ThemeToggle />
            <LanguageToggle />
          </>
        }
      >
        {/* 全站内容宽度约定（ui-sweep 裁决）：唯一在此定宽，页面禁止自建
            mx-auto/max-w——超宽屏通栏对列表可读性是伤害；h-full 保住
            work-items 详情页 flex 双栏的高度链。 */}
        <div className="mx-auto h-full w-full max-w-6xl">
          <Outlet />
        </div>
      </PloykitAppShell>
    </KitLabelsProvider>
  )
}
