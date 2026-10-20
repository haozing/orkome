import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { setWorkspaceIdProvider } from '@ploykit/client'
import { bridgeWsToQuery, type WsInvalidationRule } from '@ploykit/hooks/ws-query-bridge'
import { PloykitProvider, useAuthCtx } from '@ploykit/hooks'
import { ConfirmProvider, Toaster } from '@ploykit/ui'
import { StrictMode, useLayoutEffect, useRef } from 'react'
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

// —— kit 运行时桥（PloykitProvider 落地波次）——两个桥都必须挂在
// PloykitProvider 树内（消费 kit context），故与 Provider 同文件驻留 main.tsx。

/**
 * [历史] 工作区头桥已随 ploykit v0.11.1 退役：PloykitProvider 新增
 * workspaceHeaderSource='consumer'（上游正式解决——provider 不再注册全局
 * X-Workspace-Id，且 'kit' 模式的注册也改为 id-diff 门控+卸载撤销）。
 * orkome 以 'consumer' 挂载，zustand 注册（main.tsx 顶层）即唯一真相。
 */


/**
 * kit 登出副作用桥：kit AuthLayer.logout 的语义 = POST /auth/logout + 清
 * pk.ws.{uid} localStorage + me/workspaces 缓存置空 + qc.removeQueries()（注
 * 入的 orkome queryClient → orkome 全部缓存一并清掉），并由 kit
 * SidebarUserMenu 自带 navigate('/login')。相对 orkome signOut（settings/
 * shared.tsx signOutEverywhere）缺两步：zustand setCurrent(null) 与
 * wsClient.close()——本桥监听 kit user 的 非空→空 跃迁补齐这两步。挂在路由
 * 树外（此处）：登出后 _app 立即卸载，效应放在 _app 内会随卸载丢失。重新登
 * 录后的恢复路径不变：AppLayout 挂载时 wsClient.connect()，_app beforeLoad
 * 校验/重置 zustand currentId。
 */
function KitLogoutSideEffects() {
  const { user } = useAuthCtx()
  const prevUser = useRef(user)
  useLayoutEffect(() => {
    if (prevUser.current && !user) {
      useWorkspaceStore.getState().setCurrent(null)
      wsClient.close()
    }
    prevUser.current = user
  }, [user])
  return null
}

// PloykitProvider 自带 QueryClientProvider + AuthLayer + WorkspaceLayer；传
// orkome 的 queryClient 实例 → kit 查询（['auth','me']/['workspace','list'] 等
// queryKeys）进 orkome 缓存，ws-bridge 的失效联动对 kit 查询白得。挂在 orkome
// QueryClientProvider 内层（任务裁决）。workspaceHeaderSource='consumer'
// （v0.11.1）：provider 不注册全局 X-Workspace-Id——orkome 顶层 zustand 注册
// 即唯一真相（原 WorkspaceHeaderAuthorityBridge 已随此能力退役）。
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <PloykitProvider queryClient={queryClient} workspaceHeaderSource="consumer">
        <KitLogoutSideEffects />
        {/* 全站危险确认（useConfirm）与成功反馈（toast）的统一宿主——ui-sweep
            裁决：确认/反馈三轨归 kit */}
        <ConfirmProvider>
          <Toaster />
          <RouterProvider router={router} />
        </ConfirmProvider>
      </PloykitProvider>
    </QueryClientProvider>
  </StrictMode>,
)
