import type { QueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState, type ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'
import { logout } from '@/api/identity'
import { router } from '@/router-singleton'
import { wsClient } from '@/realtime/client'
import { useWorkspaceStore } from '@/stores/workspace'
import { Button } from '@/components/ui/button'

// settings 各区块共用的小工具。

// 复制按钮（ui-sweep 裁决③收口）：tokens/access snippet/mfa CodeField 三处
// 手写实现合一。默认 outline sm（明文块旁的主形态），CodeField 等次级位置传
// ghost xs + 灰字 className。
export function CopyButton({
  text,
  variant = 'outline',
  size = 'sm',
  className,
}: {
  text: string
  variant?: ComponentProps<typeof Button>['variant']
  size?: ComponentProps<typeof Button>['size']
  className?: string
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant={variant}
      size={size}
      className={className}
      aria-label={copied ? t('common.copied') : t('common.copy')}
      onClick={() => {
        void navigator.clipboard.writeText(text).then(
          () => {
            setCopied(true)
            setTimeout(() => setCopied(false), 1500)
          },
          () => {},
        )
      }}
    >
      {copied ? (
        <Check className="size-3.5" aria-hidden="true" />
      ) : (
        <Copy className="size-3.5" aria-hidden="true" />
      )}
      {copied ? t('common.copied') : t('common.copy')}
    </Button>
  )
}

// states 组合件的行内规格（ui-sweep 裁决⑦）：PageLoading/PageEmpty/PageError
// 默认是页面级（pt-24 居中），区块内三态用这组 className 压成行内形态。
export const INLINE_LOADING_CLS = 'flex-row items-center gap-2 py-2 text-left'
export const INLINE_ERROR_CLS = 'flex-row items-center gap-2 py-2 text-left'
export const INLINE_EMPTY_CLS = 'items-start gap-1 py-2 text-left'

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
