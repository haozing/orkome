import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { setWorkspaceIdProvider } from '@ploykit/client'
import { bridgeWsToQuery, type WsInvalidationRule } from '@ploykit/hooks/ws-query-bridge'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { setAuditExportWorkspaceProvider } from '@/api/audit'
import '@/i18n'
import { createQueryClient } from '@/lib/query'
import { router } from '@/router-singleton'
import { wsClient } from '@/realtime/client'
import { useWorkspaceStore } from '@/stores/workspace'
setWorkspaceIdProvider(() => useWorkspaceStore.getState().currentId)
// 审计 CSV 导出走裸 fetch（浏览器下载导航带不上 apiFetch 的动态头），workspace 头在此注入
setAuditExportWorkspaceProvider(() => useWorkspaceStore.getState().currentId ?? '')
const queryClient = createQueryClient()
const wsInvalidationRules: readonly WsInvalidationRule[] = [
  { match: /^work_item:/, invalidate: [['work-items'], ['work-item']] },
  { match: /^task:/, invalidate: [['work-item'], ['task'], ['workspace-tasks']] },
  { match: /^comment:/, invalidate: [['work-item'], ['work-items']] },
  // user-scoped frames pushed by the framework hub (SendToUser): the bell,
  // the notifications page and the unread badge all live under the
  // ['notifications', …] tuple prefix.
  { match: /^notification:/, invalidate: [['notifications']] },
]
// FP-20 F4 / FP-05 P2-2：task:progress 逐落库消息一帧 + task:message 500ms
// 节流帧，桥逐帧同步 invalidate 会造成运行期 refetch 风暴（每次 GET 全量
// 拉 200 条消息正文）。给桥注入 invalidateQueries 的 ~300ms trailing
// debounce：同 key 突发合并为最后一次，四条规则的键语义不变（断线重连的
// 全量 resync 也走同一合并路径，仅延迟一拍）。桥只消费 invalidateQueries，
// 其余成员原样透传。
const INVALIDATE_DEBOUNCE_MS = 300
const pendingInvalidations = new Map<string, ReturnType<typeof setTimeout>>()
const bridgeQueryClient = {
  invalidateQueries: (opts?: { queryKey?: readonly unknown[] }): void => {
    const coalesceKey = JSON.stringify(opts?.queryKey ?? [])
    const prev = pendingInvalidations.get(coalesceKey)
    if (prev) clearTimeout(prev)
    pendingInvalidations.set(
      coalesceKey,
      setTimeout(() => {
        pendingInvalidations.delete(coalesceKey)
        void queryClient.invalidateQueries(opts)
      }, INVALIDATE_DEBOUNCE_MS),
    )
  },
} as unknown as QueryClient
bridgeWsToQuery(wsClient, bridgeQueryClient, wsInvalidationRules)
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
