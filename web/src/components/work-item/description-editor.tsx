import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AlertDialog } from '@base-ui/react/alert-dialog'
import { Pencil } from 'lucide-react'
import { Suspense, lazy, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getWorkItem, updateWorkItem, type WorkItem } from '@/api/work'
import { Markdown } from '@/components/markdown'
import { ApiError } from '@ploykit/client'
const RichEditor = lazy(() =>
  import('@/components/editor/rich-editor').then((m) => ({ default: m.RichEditor })),
)
const AUTOSAVE_DEBOUNCE_MS = 1500
type SaveState = 'idle' | 'dirty' | 'saving' | 'saved' | 'error'
export function DescriptionEditor({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const [saveState, setSaveState] = useState<SaveState>('idle')
  const [conflict, setConflict] = useState<{
    serverVersion: string
    localDraft: string
  } | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const latestRef = useRef(item)
  useEffect(() => {
    latestRef.current = item
  }, [item])
  const applySaved = (updated: WorkItem) => {
    queryClient.setQueryData(['work-item', item.id], updated)
    void queryClient.invalidateQueries({ queryKey: ['work-items'] })
  }
  const save = useMutation({
    mutationFn: async (content: string) => {
      const fresh = queryClient.getQueryData<WorkItem>(['work-item', item.id]) ?? latestRef.current
      return updateWorkItem(item.id, { description: content }, fresh.revision)
    },
    onSuccess: (updated) => {
      applySaved(updated)
      setSaveState('saved')
    },
    onError: async (err) => {
      if (err instanceof ApiError && err.code === 'E_REVISION_CONFLICT') {
        const server = await queryClient.fetchQuery({
          queryKey: ['work-item', item.id],
          queryFn: () => getWorkItem(item.id),
        })
        applySaved(server)
        setConflict({ serverVersion: server.description, localDraft: draft })
        setSaveState('idle')
        return
      }
      setSaveState('error')
    },
  })
  const scheduleSave = (content: string) => {
    setSaveState('dirty')
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => save.mutate(content), AUTOSAVE_DEBOUNCE_MS)
  }
  const flushSave = () => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current)
      debounceRef.current = null
    }
    if (saveState === 'dirty' || save.isPending) save.mutate(draft)
  }
  const startEditing = () => {
    setDraft(item.description)
    setSaveState('idle')
    setEditing(true)
  }
  const stopEditing = () => {
    flushSave()
    setEditing(false)
  }
  const keepMine = useMutation({
    mutationFn: async () => {
      const fresh = queryClient.getQueryData<WorkItem>(['work-item', item.id])
      return updateWorkItem(item.id, { description: conflict!.localDraft }, fresh!.revision)
    },
    onSuccess: (updated) => {
      applySaved(updated)
      setConflict(null)
      setSaveState('saved')
    },
  })
  const hasDescription = item.description.trim().length > 0
  return (
    <div className="mb-4">
      {editing ? (
        <div className="relative flex min-h-44 flex-col">
          <div data-testid="description-editor" className="shrink-0">
            <Suspense
              fallback={
                <div
                  aria-hidden="true"
                  className="min-h-32 rounded-md px-0 py-2 text-sm text-zinc-400"
                >
                  {t('work.editorLoading')}
                </div>
              }
            >
              <RichEditor
                content={draft}
                onChange={(md) => {
                  setDraft(md)
                  scheduleSave(md)
                }}
                onBlur={stopEditing}
                onKeyDown={(e) => {
                  if (e.key === 'Escape') {
                    if (debounceRef.current) clearTimeout(debounceRef.current)
                    setEditing(false)
                    setSaveState('idle')
                  }
                }}
                ariaLabel={t('desc.aria')}
                placeholder={t('desc.placeholder')}
                autoFocus
                minRows={4}
              />
            </Suspense>
          </div>
          <p className="absolute right-1 bottom-1 text-xs text-zinc-400" aria-live="polite">
            {save.isPending || saveState === 'saving'
              ? t('common.saving')
              : saveState === 'dirty'
                ? t('desc.autosavePending')
                : saveState === 'saved'
                  ? t('desc.saved')
                  : saveState === 'error'
                    ? t('desc.saveErrorRetry')
                    : ''}
          </p>
        </div>
      ) : hasDescription ? (
        <div className="group relative min-h-44">
          <div
            onClickCapture={(e) => {
              if ((e.target as HTMLElement).closest('a')) e.stopPropagation()
            }}
          >
            <Markdown>{item.description}</Markdown>
          </div>
          <button
            type="button"
            onClick={startEditing}
            className="absolute top-0 right-0 flex items-center gap-1 rounded-md border border-zinc-200 bg-white px-2 py-1 text-xs text-zinc-500 opacity-0 shadow-sm transition-opacity outline-none group-hover:opacity-100 hover:text-zinc-800 focus-visible:opacity-100 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200"
          >
            <Pencil className="size-3" aria-hidden="true" />
            {t('common.edit')}
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={startEditing}
          className="flex min-h-44 w-full cursor-default items-center justify-center gap-2 rounded-md border border-dashed border-zinc-300 bg-zinc-50/60 text-sm text-zinc-400 outline-none hover:border-zinc-400 hover:bg-zinc-50 hover:text-zinc-500 dark:border-zinc-700 dark:bg-zinc-900/40 dark:hover:border-zinc-600 dark:hover:bg-zinc-900 dark:hover:text-zinc-300"
        >
          <Pencil className="size-4" aria-hidden="true" />
          {t('desc.emptyEdit')}
        </button>
      )}

      <AlertDialog.Root
        open={conflict !== null}
        onOpenChange={(open) => {
          if (!open) setConflict(null)
        }}
      >
        <AlertDialog.Portal>
          <AlertDialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
          <AlertDialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-lg -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
            <AlertDialog.Title className="mb-1 text-sm font-semibold">
              {t('desc.conflictTitle')}
            </AlertDialog.Title>
            <AlertDialog.Description className="mb-4 text-sm text-zinc-500 dark:text-zinc-400">
              {t('desc.conflictDesc')}
            </AlertDialog.Description>
            <div className="mb-4 grid grid-cols-2 gap-3">
              <div className="rounded-md border border-zinc-200 p-2.5 dark:border-zinc-700">
                <p className="mb-1 text-xs font-medium text-zinc-400">{t('desc.serverVersion')}</p>
                <p className="max-h-40 overflow-auto text-sm whitespace-pre-wrap text-zinc-700 dark:text-zinc-300">
                  {conflict?.serverVersion || t('desc.emptyValue')}
                </p>
              </div>
              <div className="rounded-md border border-amber-300 p-2.5 dark:border-amber-800">
                <p className="mb-1 text-xs font-medium text-amber-600 dark:text-amber-400">
                  {t('desc.myVersion')}
                </p>
                <p className="max-h-40 overflow-auto text-sm whitespace-pre-wrap text-zinc-700 dark:text-zinc-300">
                  {conflict?.localDraft || t('desc.emptyValue')}
                </p>
              </div>
            </div>
            {keepMine.error && (
              <p role="alert" className="mb-3 text-xs text-red-600 dark:text-red-400">
                {t('desc.retryFailed')}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => {
                  setConflict(null)
                  setSaveState('saved')
                }}
                className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800"
              >
                {t('desc.useServer')}
              </button>
              <button
                type="button"
                disabled={keepMine.isPending}
                onClick={() => keepMine.mutate()}
                className="rounded-md bg-zinc-900 px-3 py-1.5 text-sm text-zinc-50 hover:bg-zinc-700 disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-300"
              >
                {keepMine.isPending ? t('common.saving') : t('desc.keepMine')}
              </button>
            </div>
          </AlertDialog.Popup>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </div>
  )
}
