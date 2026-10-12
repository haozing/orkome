import { useQuery } from '@tanstack/react-query'
import { listPackEntries, type PackUISlot } from '@/api/packs'
import { useWorkspaceStore } from '@/stores/workspace'

// usePackSlots —— work-item 详情页动态 slot tab 的数据源（C2/F3）。
// 拉GET /api/ui-entries（slots 随行返回）并展平成 (pack, slot) 对；
// 与侧栏 PackEntries 共用缓存键（相同端点与形状）。放 components 层而非
// hooks 层：它依赖 api + stores（layering 规则 hooks 层禁二者）。
// workItemId 传 null 时展平为空（详情页外无宿主）。
export interface PackSlotTab {
  pack: string
  slot: PackUISlot
  /** tab 键：pack:<pack>（zod search schema 放行 /^pack:/） */
  key: `pack:${string}`
}

export function usePackSlots(workItemId: string | null) {
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  return useQuery({
    queryKey: ['pack-ui-entries', workspaceId],
    queryFn: listPackEntries,
    enabled: !!workspaceId,
    staleTime: 60000,
    select: (entries: { pack: string; slots?: PackUISlot[] }[]): PackSlotTab[] =>
      workItemId === null
        ? []
        : entries.flatMap((e) =>
            (e.slots ?? []).map((s) => ({
              pack: e.pack,
              slot: s,
              key: `pack:${e.pack}` as const,
            })),
          ),
  })
}
