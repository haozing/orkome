import type { QueryClient } from '@tanstack/react-query'
import { wsClient } from '@/realtime/client'
import { useWorkspaceStore } from '@/stores/workspace'
export function switchWorkspace(queryClient: QueryClient, workspaceId: string): void {
  const current = useWorkspaceStore.getState().currentId
  if (current === workspaceId) return
  useWorkspaceStore.getState().setCurrent(workspaceId)
  queryClient.removeQueries({
    predicate: (query) => {
      const head = query.queryKey[0]
      return head !== 'me' && head !== 'config'
    },
  })
  wsClient.reconnect()
}
