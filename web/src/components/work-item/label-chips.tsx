import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Tag } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  attachLabel,
  createLabel,
  detachLabel,
  listLabels,
  listWorkItemLabels,
  type Label,
} from '@/api/work'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
function colorOf(label: Label): string {
  return /^#[0-9a-fA-F]{6}$/.test(label.color ?? '') ? label.color! : '#a1a1aa'
}
const PRESET_COLORS = ['#8b5cf6', '#0ea5e9', '#10b981', '#f59e0b', '#ef4444', '#64748b']
function ColorDot({ label }: { label: Label }) {
  return (
    <span
      className="size-2 shrink-0 rounded-full"
      style={{ backgroundColor: colorOf(label) }}
      aria-hidden="true"
    />
  )
}
export function LabelsBlock({ workItemId }: { workItemId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const attached = useQuery({
    queryKey: ['work-item', workItemId, 'labels'],
    queryFn: () => listWorkItemLabels(workItemId),
  })
  const catalog = useQuery({
    queryKey: ['labels'],
    queryFn: listLabels,
    staleTime: 5 * 60000,
  })
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const colorIdx = useRef(0)
  const fail = () => setError(t('labels.opFailed'))
  const applyList = (labels: Label[]) => {
    setError(null)
    queryClient.setQueryData(['work-item', workItemId, 'labels'], labels)
  }
  const attach = useMutation({
    mutationFn: (labelId: string) => attachLabel(workItemId, labelId),
    onSuccess: applyList,
    onError: fail,
  })
  const detach = useMutation({
    mutationFn: (labelId: string) => detachLabel(workItemId, labelId),
    onSuccess: applyList,
    onError: fail,
  })
  const create = useMutation({
    mutationFn: (name: string) => {
      const color = PRESET_COLORS[colorIdx.current % PRESET_COLORS.length]
      colorIdx.current += 1
      return createLabel(name, color)
    },
    onSuccess: async (label) => {
      setError(null)
      setNewName('')
      void queryClient.invalidateQueries({ queryKey: ['labels'] })
      applyList(await attachLabel(workItemId, label.id))
    },
    onError: fail,
  })
  const submitNew = () => {
    const name = newName.trim()
    if (!name || create.isPending) return
    create.mutate(name)
  }
  const attachedList = attached.data ?? []
  const attachedIds = new Set(attachedList.map((l) => l.id))
  const unattached = (catalog.data ?? []).filter((l) => !attachedIds.has(l.id))
  const menuError =
    error ?? (attach.error || detach.error || create.error ? t('labels.opFailed') : null)
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {attachedList.map((label) => (
        <span
          key={label.id}
          className="inline-flex items-center gap-1 rounded-full border border-zinc-200 px-2 py-0.5 text-xs dark:border-zinc-700"
        >
          <ColorDot label={label} />
          {label.name}
          <button
            type="button"
            aria-label={t('labels.removeAria', { name: label.name })}
            onClick={() => detach.mutate(label.id)}
            className="cursor-default text-zinc-400 outline-none hover:text-red-500"
          >
            ×
          </button>
        </span>
      ))}

      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label={t('labels.addAria')}
          className="flex cursor-default items-center gap-1 rounded-full border border-dashed border-zinc-300 px-2 py-0.5 text-xs text-zinc-400 outline-none hover:border-zinc-400 hover:text-zinc-600 data-[popup-open]:border-zinc-400 dark:border-zinc-700 dark:hover:border-zinc-600 dark:hover:text-zinc-300"
        >
          <Tag className="size-3" aria-hidden="true" />
          {t('labels.title')}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-48">
          {menuError && (
            <p role="alert" className="px-2 py-1 text-xs text-red-600 dark:text-red-400">
              {menuError}
            </p>
          )}
          {unattached.length > 0 && (
            <DropdownMenuGroup>
              <DropdownMenuLabel>{t('labels.clickToAttach')}</DropdownMenuLabel>
              {unattached.map((label) => (
                <DropdownMenuItem key={label.id} onClick={() => attach.mutate(label.id)}>
                  <ColorDot label={label} />
                  {label.name}
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
          )}
          <DropdownMenuGroup>
            {/* 「（点击移除）」后缀删（ui-sweep #10：点击行为自明，见条目即知） */}
            <DropdownMenuLabel>{t('labels.attached')}</DropdownMenuLabel>
            {attachedList.length === 0 && (
              <p className="text-muted-foreground px-2 py-1 text-xs">{t('labels.noneAttached')}</p>
            )}
            {attachedList.map((label) => (
              <DropdownMenuItem key={label.id} onClick={() => detach.mutate(label.id)}>
                <ColorDot label={label} />
                {label.name}
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <form
            className="flex items-center gap-1 px-2 py-1"
            onSubmit={(e) => {
              e.preventDefault()
              submitNew()
            }}
          >
            <input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => e.stopPropagation()}
              placeholder={t('labels.newPlaceholder')}
              aria-label={t('labels.newAria')}
              className="w-full rounded bg-transparent py-1 text-sm outline-none"
            />
            <button
              type="submit"
              disabled={!newName.trim() || create.isPending}
              aria-label={t('labels.createAndAttachAria')}
              className="shrink-0 rounded px-1.5 py-0.5 text-xs text-zinc-500 outline-none hover:bg-zinc-100 disabled:opacity-40 dark:hover:bg-zinc-800"
            >
              <Plus className="size-3.5" aria-hidden="true" />
            </button>
          </form>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
