import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@/i18n'
import { setWorkspaceIdProvider } from '@/lib/api'
import { createQueryClient } from '@/lib/query'
import { bridgeWsToQuery, type WsInvalidationRule } from '@/lib/ws-query-bridge'
import { createAppRouter } from '@/router'
import { wsClient } from '@/realtime/client'
import { useWorkspaceStore } from '@/stores/workspace'
setWorkspaceIdProvider(() => useWorkspaceStore.getState().currentId)
const queryClient = createQueryClient()
const wsInvalidationRules: readonly WsInvalidationRule[] = [
  { match: /^work_item:/, invalidate: [['work-items'], ['work-item']] },
  { match: /^task:/, invalidate: [['work-item'], ['task'], ['workspace-tasks']] },
  { match: /^comment:/, invalidate: [['work-item'], ['work-items']] },
]
bridgeWsToQuery(wsClient, queryClient, wsInvalidationRules)
const router = createAppRouter(queryClient)
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
