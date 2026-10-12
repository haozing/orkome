import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Menu } from '@base-ui/react/menu'
import { Check, ChevronDown } from 'lucide-react'
import { meQueryOptions } from '@/api/identity'
import { switchWorkspace } from '@/services/workspace-switch'
import { useWorkspaceStore } from '@/stores/workspace'
import { useTranslation } from 'react-i18next'
export function WorkspaceSwitcher() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const me = useQuery(meQueryOptions)
  const currentId = useWorkspaceStore((s) => s.currentId)
  const current = me.data?.workspaces.find((w) => w.id === currentId)
  return (
    <Menu.Root>
      <Menu.Trigger className="flex h-7 items-center gap-1.5 rounded-md px-2 text-sm font-medium outline-none hover:bg-zinc-100 data-[popup-open]:bg-zinc-100 dark:hover:bg-zinc-800 dark:data-[popup-open]:bg-zinc-800">
        <span className="max-w-40 truncate">{current?.name ?? t('ws.selectWorkspace')}</span>
        <ChevronDown className="size-3.5 text-zinc-400" aria-hidden="true" />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner sideOffset={6} align="start" className="z-50 outline-none">
          <Menu.Popup className="min-w-44 rounded-md border border-zinc-200 bg-white p-1 shadow-lg dark:border-zinc-700 dark:bg-zinc-900">
            {/* Base UI: Menu.GroupLabel throws without a Menu.Group context
                (audit FP-02 P0-1) — keep the label and items inside one group. */}
            <Menu.Group>
              <Menu.GroupLabel className="px-2 py-1 text-xs text-zinc-400">
                {t('ws.groupLabel')}
              </Menu.GroupLabel>
              {(me.data?.workspaces ?? []).map((ws) => (
                <Menu.Item
                  key={ws.id}
                  onClick={() => switchWorkspace(queryClient, ws.id)}
                  className="flex cursor-default items-center gap-2 rounded px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-zinc-100 dark:data-[highlighted]:bg-zinc-800"
                >
                  <Check
                    className={
                      'size-3.5 shrink-0 ' + (ws.id === currentId ? 'opacity-100' : 'opacity-0')
                    }
                    aria-hidden="true"
                  />
                  <span className="max-w-48 truncate">{ws.name}</span>
                </Menu.Item>
              ))}
            </Menu.Group>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  )
}
