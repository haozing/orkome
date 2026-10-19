import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, Outlet, redirect, useNavigate } from '@tanstack/react-router'
import {
  Bell,
  BookOpen,
  Bot,
  DoorOpen,
  ListTodo,
  Settings,
  ScrollText,
  User,
  Users,
  Wrench,
  Zap,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { useCallback, useEffect, useMemo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { logout, meQueryOptions, type Me } from '@/api/identity'
import { LanguageToggle } from '@/components/language-toggle'
import { NotificationBell } from '@/components/notification-bell'
import { ThemeToggle } from '@/components/theme-toggle'
import { ProjectNav } from '@/components/project-nav'
import { PackEntries } from '@/components/pack-entries'
import { WorkspaceSwitcher } from '@/components/workspace-switcher'
import { WsStatusBadge } from '@/components/ws-status-badge'
import { AuthContext, type AuthContextValue } from '@ploykit/hooks'
import {
  AppShell as PloykitAppShell,
  type AppShellNavGroup,
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
  { to: '/settings', label: 'nav.settings', icon: Settings },
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
// —— 最小 AuthContext 桥（本次波次拍板的取舍，见代码评审记录）——
// @ploykit/ui 的 AppShell 无条件渲染 ImpersonationBanner 与用户菜单
// （UserMenu/SidebarUserMenu 二选一），三者组件体直接调 useAuth()：
// @ploykit/hooks 0.8.0 在无 AuthContext 时同步抛 'must be used within
// PloykitProvider'，整壳首帧崩溃。AppShell 本体对 useWorkspace/useAuth 的
// try/catch 防御不覆盖这三个子件（ploykit 自家测试是 mock useAuth 才跑通的）。
// 本桥只满足其"读展示值"的最小契约：不引入 PloykitProvider、不挂
// WorkspaceContext（AppShell 的 perm/permKey 过滤因此走"rolePerms 未知 →
// 默认可见"降级分支），orkome 的 zustand workspace store 与 api 层零改动。
function AuthBridge({ children }: { children: ReactNode }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  // beforeLoad 已 ensureQueryData，这里纯读缓存（过期仅后台刷新，无首帧请求）。
  const me = useQuery(meQueryOptions).data
  const signOut = useCallback(async () => {
    await logout()
    useWorkspaceStore.getState().setCurrent(null)
    queryClient.clear()
    wsClient.close()
    void navigate({ to: '/login' })
  }, [navigate, queryClient])
  const value = useMemo<AuthContextValue>(
    () => ({
      // PKUser 比 orkome 的 User 多必填位（status/is_platform_admin/
      // email_verified/created_at），按 orkome 语义补默认：无平台管理员概念
      // （置 false，避免用户菜单渲染指向不存在的 /admin 的入口）、会话内
      // 用户视为 active/verified。
      user: me
        ? {
            ...me.user,
            status: 'active',
            is_platform_admin: false,
            email_verified: true,
            created_at: '',
          }
        : null,
      // AppShell 树内零消费工作区列表（切换器走 orkome 自有 WorkspaceSwitcher
      // + zustand），不伪造 wire 数据，置空即可。
      workspaces: [],
      loading: false,
      error: null,
      refresh: () => queryClient.invalidateQueries({ queryKey: meQueryOptions.queryKey }),
      // 登出转发 orkome signOut（清 store + 查询缓存 + 断 ws + 跳登录页），
      // 供 UserMenu/SidebarUserMenu 的内置"退出登录"项调用。
      logout: signOut,
    }),
    [me, queryClient, signOut],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
// NavItem.icon 是 ReactNode 插槽；形状对齐 ploykit canonical（lucide + size-4）。
const navIcon = (Icon: LucideIcon) => <Icon className="size-4 shrink-0" aria-hidden="true" />
const toNavItems = (
  items: ReadonlyArray<{ to: string; label: string; icon: LucideIcon }>,
  t: (key: string) => string,
): NavItem[] =>
  items.map((item) => ({ path: item.to, label: t(item.label), icon: navIcon(item.icon) }))
function AppLayout() {
  const { t } = useTranslation()
  useEffect(() => wsClient.connect(), [])
  const mainNav: NavItem[] = toNavItems(NAV_ITEMS, t)
  const aiTeamNav: AppShellNavGroup = {
    // 分组标签复用既有 nav.aiTeam 键（旧壳同款文案位；本波次禁止新增 i18n 键）。
    group: t('nav.aiTeam'),
    items: toNavItems(AI_TEAM_NAV_ITEMS, t),
  }
  const userMenuItems: UserMenuExtraItem[] = [
    // 账户入口指向 orkome 设置页。登出不另塞条目：SidebarUserMenu 内置
    // "退出登录"项的 logout() 已由 AuthBridge 转发 orkome signOut，重复条目
    // 只会让菜单出现两个登出。
    { label: t('nav.settings'), to: '/settings', icon: navIcon(Settings) },
  ]
  return (
    <AuthBridge>
      <PloykitAppShell
        brand="Orkome"
        nav={mainNav}
        navGroups={[aiTeamNav]}
        userMenu="sidebar-footer"
        userMenuItems={userMenuItems}
        sidebarHeader={
          /* sidebarHeader 槽整体替换 AppShell 默认 logo+brand 块（其文档语义），
             故品牌字标搬进槽内与 WorkspaceSwitcher 同排（旧壳头部同款布局）；
             brand prop 仍传，留作声明。web/public 无静态 logo 资源，不传 logo。 */
          <div className="flex items-center gap-2 px-2 pb-1 pt-1 group-data-[collapsible=icon]:justify-center">
            <span className="text-lg font-bold text-foreground group-data-[collapsible=icon]:hidden">
              Orkome
            </span>
            <WorkspaceSwitcher />
          </div>
        }
        sidebarFooter={
          /* ProjectNav/PackEntries/底部导航（助理接入/自动化/设置）在 AppShell
             没有中部内容槽可放，保守收进 sidebarFooter；footer 里的自定义内容
             折叠态（collapsible=icon）不会被 AppShell 自动隐藏（它只在 header
             用户菜单模式隐藏整个 footer），这里自带折叠隐藏兜底，避免窄轨溢出
             （用户卡折叠态由 SidebarUserMenu 自理）。旧壳"项目列表在 AI 团队组
             之上"的相对顺序在 footer 形态下不可保留，接受重排：主导航 → AI
             团队组 → 项目/Pack/底部导航 → 用户卡。 */
          <div className="group-data-[collapsible=icon]:hidden">
            <ProjectNav />
            <PackEntries workspaceId={useWorkspaceStore((s) => s.currentId)} />
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
             登出按钮移入侧栏底部用户卡（内置退出项，经 AuthBridge 走 orkome
             signOut）。 */
          <>
            <WsStatusBadge />
            <NotificationBell />
            <ThemeToggle />
            <LanguageToggle />
          </>
        }
      >
        <Outlet />
      </PloykitAppShell>
    </AuthBridge>
  )
}
