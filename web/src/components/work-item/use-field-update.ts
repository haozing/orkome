import { useMutation, useQueryClient } from '@tanstack/react-query'
import { updateWorkItem, type WorkItem, type WorkItemPatch } from '@/api/work'
import { ApiError } from '@/lib/api'
export function useFieldUpdate(item: WorkItem) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (patch: WorkItemPatch) => updateWorkItem(item.id, patch, item.revision),
    onSuccess: (updated) => {
      queryClient.setQueryData(['work-item', item.id], updated)
      void queryClient.invalidateQueries({ queryKey: ['work-items'] })
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === 'E_REVISION_CONFLICT') {
        void queryClient.invalidateQueries({ queryKey: ['work-item', item.id] })
      }
    },
  })
}
