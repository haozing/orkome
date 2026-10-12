import { useQuery } from '@tanstack/react-query'
import { ExternalLink, TriangleAlert } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@ploykit/client'
import type { PackUISlot } from '@/api/packs'
import { useWorkspaceStore } from '@/stores/workspace'

// PackSlotFrame —— pack slot 的宿主 iframe（前端设计 §1.3）：
//  1. 每次挂载现铸 ui-token（POST /api/ui-tokens；票 60 秒过期，绝不缓存/复用）；
//  2. 拼 URL：模板变量 {uiToken}/{workspaceId}/{workItemId} 由前端渲染时替换
//     （票据仍只有 workspace 级 claims，workItemId 是 URL 参数）；
//  3. sandbox="allow-scripts allow-same-origin allow-popups"；
//  4. 监听 postMessage({type:"pack.resize", height}) 调整高度，3 秒超时回落声明高度；
//  5. 加载失败/超时呈现"在控制台打开"深链兜底（iframe 永远不是唯一路径，
//     右上角固定 external-link 按钮）。
const RESIZE_TIMEOUT_MS = 3000

/** 铸一张新 ui-token（60s 短票，现铸现用） */
async function mintUiToken(plugin: string, workspaceId: string): Promise<string> {
  const res = await api.post<{ token: string }>('/api/ui-tokens', {
    plugin,
    workspace_id: workspaceId,
  })
  return res.token
}

function renderSlotUrl(
  template: string,
  uiToken: string,
  workspaceId: string,
  workItemId: string,
): string {
  return template
    .replaceAll('{uiToken}', encodeURIComponent(uiToken))
    .replaceAll('{workspaceId}', encodeURIComponent(workspaceId))
    .replaceAll('{workItemId}', encodeURIComponent(workItemId))
}

export interface PackSlotFrameProps {
  pack: string
  slot: PackUISlot
  workItemId: string
  /** resize 协议等待窗口（默认 3s，测试可注入短值） */
  resizeTimeoutMs?: number
}

export function PackSlotFrame({ pack, slot, workItemId, resizeTimeoutMs = RESIZE_TIMEOUT_MS }: PackSlotFrameProps) {
  const { t } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  const [height, setHeight] = useState(slot.height)

  // 铸票：queryKey 含 workItemId → 切 tab 重挂载必重铸；票据只在成功后进 URL。
  const token = useQuery({
    queryKey: ['pack-slot-token', pack, slot.slot, workspaceId, workItemId],
    queryFn: () => mintUiToken(pack, workspaceId ?? ''),
    enabled: !!workspaceId,
    staleTime: 0,
    gcTime: 0,
    retry: false,
  })

  const src = useMemo(
    () =>
      token.data && workspaceId
        ? renderSlotUrl(slot.url_template, token.data, workspaceId, workItemId)
        : null,
    [token.data, workspaceId, slot.url_template, workItemId],
  )

  // resize 协议：只认 src 同源消息 + pack.resize 类型；3s 未收到回落声明高度。
  useEffect(() => {
    if (!src) return
    let origin: string | null = null
    try {
      origin = new URL(src).origin
    } catch {
      origin = null
    }
    const timer = window.setTimeout(() => {
      setHeight(slot.height)
    }, resizeTimeoutMs)
    const onMessage = (ev: MessageEvent) => {
      if (origin && ev.origin !== origin) return
      const data = ev.data as { type?: string; height?: unknown } | null
      if (!data || data.type !== 'pack.resize') return
      const h = Number(data.height)
      if (Number.isFinite(h) && h > 0 && h <= 8192) {
        window.clearTimeout(timer)
        setHeight(Math.round(h))
      }
    }
    window.addEventListener('message', onMessage)
    return () => {
      window.clearTimeout(timer)
      window.removeEventListener('message', onMessage)
    }
  }, [src, slot.height, resizeTimeoutMs])

  const consoleUrl = token.data && workspaceId
    ? renderSlotUrl(slot.url_template, token.data, workspaceId, workItemId)
    : null

  if (!workspaceId) return null
  if (token.isError) {
    return (
      <div
        data-testid="pack-slot-error"
        className="flex items-center gap-2 rounded-md border border-zinc-200 p-4 text-sm text-zinc-500 dark:border-zinc-800"
      >
        <TriangleAlert className="size-4 shrink-0" aria-hidden="true" />
        <span>{t('slots.mintFailed')}</span>
      </div>
    )
  }
  if (!src) {
    return (
      <div data-testid="pack-slot-loading" style={{ height: slot.height }} aria-hidden="true" />
    )
  }
  return (
    <div className="relative" data-testid="pack-slot-frame">
      <iframe
        src={src}
        title={`${slot.label} (${pack})`}
        className="w-full rounded-md border border-zinc-200 dark:border-zinc-800"
        style={{ height }}
        sandbox="allow-scripts allow-same-origin allow-popups"
      />
      {/* 深链兜底：iframe 永远不是唯一路径 */}
      {consoleUrl && (
        <a
          href={consoleUrl}
          target="_blank"
          rel="noopener noreferrer"
          data-testid="pack-slot-external"
          aria-label={t('slots.openExternal')}
          className="absolute right-2 top-2 flex items-center gap-1 rounded-md bg-white/90 px-2 py-1 text-xs text-zinc-600 shadow-sm hover:text-zinc-900 dark:bg-zinc-900/90 dark:text-zinc-300 dark:hover:text-zinc-100"
        >
          <ExternalLink className="size-3" aria-hidden="true" />
          {t('slots.openExternal')}
        </a>
      )}
    </div>
  )
}
