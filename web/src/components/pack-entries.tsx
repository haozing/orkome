import { useMutation, useQuery } from '@tanstack/react-query'
import { ExternalLink } from 'lucide-react'
import { api } from '@ploykit/client'
interface PackUIEntry {
  id: string
  pack: string
  label: string
  url_template: string
}
function renderURL(template: string, uiToken: string, ws: string): string {
  return template
    .replaceAll('{uiToken}', encodeURIComponent(uiToken))
    .replaceAll('{workspaceId}', encodeURIComponent(ws))
}
export function PackEntries({ workspaceId }: { workspaceId: string | null }) {
  const entries = useQuery({
    queryKey: ['pack-ui-entries', workspaceId],
    queryFn: async (): Promise<PackUIEntry[]> => {
      const res = await api.get<{
        items: PackUIEntry[] | null
      }>('/api/ui-entries')
      return res.items ?? []
    },
    enabled: !!workspaceId,
    staleTime: 60000,
  })
  const jump = useMutation({
    mutationFn: async (entry: PackUIEntry) => {
      const res = await api.post<{
        token: string
      }>('/api/ui-tokens', {
        plugin: entry.pack,
        workspace_id: workspaceId,
      })
      return renderURL(entry.url_template, res.token, workspaceId ?? '')
    },
    onSuccess: (url) => window.open(url, '_blank', 'noopener'),
  })
  if (!workspaceId || !entries.data?.length) return null
  return (
    <div className="px-2 pb-2">
      {entries.data.map((e) => (
        <button
          key={e.id}
          type="button"
          onClick={() => jump.mutate(e)}
          disabled={jump.isPending}
          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm text-zinc-600 transition-colors hover:bg-zinc-100 hover:text-zinc-900 disabled:opacity-50 dark:text-zinc-300 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
        >
          <ExternalLink className="size-3.5 shrink-0" aria-hidden="true" />
          <span className="truncate">{e.label}</span>
        </button>
      ))}
    </div>
  )
}
