import { useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, Outlet, redirect, useNavigate } from '@tanstack/react-router'
import {
  Bell,
  BookOpen,
  Bot,
  DoorOpen,
  ListTodo,
  LogOut,
  Package,
  ScrollText,
  Settings,
  User,
  Users,
  Wrench,
  Zap,
} from 'lucide-react'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { logout, meQueryOptions, type Me } from '@/api/identity'
import { LanguageToggle } from '@/components/language-toggle'
import { NotificationBell } from '@/components/notification-bell'
import { Button } from '@/components/ui/button'
import { ThemeToggle } from '@/components/theme-toggle'
import { ProjectNav } from '@/components/project-nav'
import { PackEntries } from '@/components/pack-entries'
import { WorkspaceSwitcher } from '@/components/workspace-switcher'
import { WsStatusBadge } from '@/components/ws-status-badge'
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
  component: AppShell,
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
  { to: '/packs', label: 'nav.packs', icon: Package },
  { to: '/tools', label: 'nav.tools', icon: Wrench },
  { to: '/run-logs', label: 'nav.runLogs', icon: ScrollText },
] as const
const NAV_LINK_CLASS =
  'flex items-center gap-2 rounded-md px-3 py-1.5 text-sm text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800'
const NAV_ACTIVE_CLASS = cn(
  'font-medium text-zinc-900 dark:text-zinc-100',
  'bg-zinc-100 dark:bg-zinc-800',
)
function AppShell() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  useEffect(() => wsClient.connect(), [])
  const signOut = async () => {
    await logout()
    useWorkspaceStore.getState().setCurrent(null)
    queryClient.clear()
    wsClient.close()
    void navigate({ to: '/login' })
  }
  return (
    <div className="flex h-dvh flex-col">
      <header className="flex h-12 shrink-0 items-center justify-between border-b border-zinc-200 px-4 dark:border-zinc-800">
        <div className="flex items-center gap-3">
          <span className="text-sm font-semibold tracking-wide">ORKOME</span>
          <WorkspaceSwitcher />
        </div>
        <div className="flex items-center gap-2">
          <WsStatusBadge />
          <NotificationBell />
          <ThemeToggle />
          <LanguageToggle />
          <Button
            variant="ghost"
            size="sm"
            aria-label={t('common.logout')}
            onClick={() => void signOut()}
          >
            <LogOut className="size-4" />
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <nav className="flex w-44 shrink-0 flex-col border-r border-zinc-200 p-2 dark:border-zinc-800">
          <div className="flex flex-col gap-1">
            {NAV_ITEMS.map((item) => (
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
          </div>
          <ProjectNav />

          <PackEntries workspaceId={useWorkspaceStore((s) => s.currentId)} />
          <div className="mt-2 flex flex-col gap-1 border-t border-zinc-200 pt-2 dark:border-zinc-800">
            <span className="px-3 text-xs text-zinc-400">{t('nav.aiTeam')}</span>
            {AI_TEAM_NAV_ITEMS.map((item) => (
              <Link
                key={item.to}
                to={item.to}
                className={cn(NAV_LINK_CLASS, 'flex items-center gap-2')}
                activeProps={{ className: NAV_ACTIVE_CLASS }}
              >
                <item.icon className="size-4 shrink-0" aria-hidden="true" />
                {t(item.label)}
              </Link>
            ))}
          </div>
          <div className="mt-auto flex flex-col gap-1 border-t border-zinc-200 pt-2 dark:border-zinc-800">
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
          </div>
        </nav>

        <main className="min-w-0 flex-1 overflow-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
