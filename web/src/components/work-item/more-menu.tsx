import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Ellipsis, ExternalLink, Link2, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { deleteWorkItem, type WorkItem } from '@/api/work'
import { ApiError } from '@/lib/api'
import { useTranslation } from 'react-i18next'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
export function MoreMenu({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const remove = useMutation({
    mutationFn: () => deleteWorkItem(item.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['work-items'] })
      void navigate({ to: '/work-items' })
    },
  })
  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(window.location.href)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
      // eslint-disable-next-line no-empty
    } catch {}
  }
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label={t('work.moreActions')}
          className="hover:bg-accent data-[popup-open]:bg-accent flex size-8 cursor-default items-center justify-center rounded-md text-zinc-500 outline-none hover:text-zinc-700 dark:hover:text-zinc-300"
        >
          <Ellipsis className="size-4" aria-hidden="true" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-44">
          <DropdownMenuItem onClick={() => window.open(window.location.href, '_blank')}>
            <ExternalLink className="size-3.5" aria-hidden="true" />
            {t('work.openInNewTab')}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => void copyLink()}>
            <Link2 className="size-3.5" aria-hidden="true" />
            {copied ? t('common.copied') : t('work.copyLink')}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onClick={() => setConfirmOpen(true)}>
            <Trash2 className="size-3.5" aria-hidden="true" />
            {t('work.deleteTask')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogTitle>{t('work.deleteTask')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('work.deleteConfirm', { title: item.title })}
          </AlertDialogDescription>
          {remove.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {remove.error instanceof ApiError ? remove.error.message : t('work.deleteFailed')}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-red-600 text-white hover:bg-red-500 focus-visible:ring-red-600/40"
              disabled={remove.isPending}
              onClick={() => remove.mutate()}
            >
              {remove.isPending ? t('work.deleting') : t('common.delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
