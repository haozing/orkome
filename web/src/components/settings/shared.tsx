import type { QueryClient } from '@tanstack/react-query'
import { logout } from '@/api/identity'
import { router } from '@/router-singleton'
import { wsClient } from '@/realtime/client'
import { useWorkspaceStore } from '@/stores/workspace'
import { useTranslation } from 'react-i18next'

// settings 各区块共用的小工具。

// 按当前语言格式化时间（同 access 页惯例，i18n.language zh → zh-CN，其余 → en-US）。
export function useSettingsTimeFormatter() {
  const { i18n } = useTranslation()
  const locale = i18n.language === 'zh' ? 'zh-CN' : 'en-US'
  return (iso?: string): string => {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(locale, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })
  }
}

// 登出流程（同 _app.tsx 的 signOut）：清 workspace 选择、清 query 缓存、
// 关闭实时通道、跳 /login。用于吊销全部会话 / MFA 关闭后当前会话已死的场景。
// 经 router 单例做命令式导航（本函数不是组件，不能用 useNavigate）。
export async function signOutEverywhere(queryClient: QueryClient) {
  try {
    await logout()
  } catch {
    // cookie 可能已被服务端清掉（revoke-all 场景）——继续本地清理。
  }
  useWorkspaceStore.getState().setCurrent(null)
  queryClient.clear()
  wsClient.close()
  void router.navigate({ to: '/login' })
}
