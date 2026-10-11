import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Plus, Search, Upload } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createSkill,
  importSkillFromUrl,
  importSkillZip,
  listSkills,
  type Skill,
} from '@/api/skills'
import { Dialog } from '@base-ui/react/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { ApiError } from '@ploykit/client'
import { cn } from '@/lib/utils'
export const Route = createFileRoute('/_app/skills/')({
  component: SkillsPage,
})
const SEARCH_DEBOUNCE_MS = 400
const LABEL_CLASS = 'mb-1 block text-xs text-zinc-500'
const ORIGIN_KEYS: Record<string, { key: string; tone: 'neutral' | 'success' | 'warning' }> = {
  manual: { key: 'skills.originManual', tone: 'neutral' },
  import: { key: 'skills.originImport', tone: 'success' },
  url: { key: 'skills.originUrl', tone: 'warning' },
}
function SkillsPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [query])
  const skills = useQuery({ queryKey: ['skills', debounced], queryFn: () => listSkills(debounced) })
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-lg font-semibold">Skills</h1>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setImportOpen(true)}>
            <Upload className="size-3.5" aria-hidden="true" />
            {t('skills.import')}
          </Button>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="size-3.5" aria-hidden="true" />
            {t('skills.newSkill')}
          </Button>
        </div>
      </div>

      <div className="relative">
        <Search
          className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-zinc-400"
          aria-hidden="true"
        />
        <Input
          aria-label={t('agents.searchSkill')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('skills.searchPlaceholder')}
          className="pl-8"
        />
      </div>

      {skills.isPending && <PageLoading label={t('skills.loading')} />}

      {skills.isError && (
        <PageError
          title={t('common.error')}
          message={skills.error instanceof Error ? skills.error.message : undefined}
          onRetry={() => void skills.refetch()}
        />
      )}

      {skills.isSuccess && skills.data.length === 0 && (
        <PageEmpty
          title={debounced ? t('agents.noSkillMatch') : t('skills.emptyTitle')}
          description={t('skills.emptyDesc')}
        />
      )}

      {skills.data && skills.data.length > 0 && (
        <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {skills.data.map((s) => (
            <SkillRow key={s.id} skill={s} />
          ))}
        </ul>
      )}

      <CreateSkillDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(s) => void navigate({ to: '/skills/$skillId', params: { skillId: s.id } })}
      />
      <ImportSkillDialog
        open={importOpen}
        onOpenChange={setImportOpen}
        onImported={(s) => void navigate({ to: '/skills/$skillId', params: { skillId: s.id } })}
      />
    </div>
  )
}
function SkillRow({ skill }: { skill: Skill }) {
  const { t, i18n } = useTranslation()
  const badge = ORIGIN_KEYS[skill.origin] ?? { key: skill.origin, tone: 'neutral' as const }
  return (
    <li>
      <Link
        to="/skills/$skillId"
        params={{ skillId: skill.id }}
        className="flex items-center gap-3 px-3 py-2.5 text-sm outline-none hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
      >
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium">{skill.name}</p>
          {skill.description && (
            <p className="truncate text-xs text-zinc-500 dark:text-zinc-400">{skill.description}</p>
          )}
        </div>
        <Badge variant={badge.tone}>{t(badge.key)}</Badge>
        <span className="hidden shrink-0 text-xs text-zinc-400 sm:inline">
          {new Date(skill.created_at).toLocaleDateString(
            i18n.language === 'zh' ? 'zh-CN' : 'en-US',
          )}
        </span>
      </Link>
    </li>
  )
}
function CreateSkillDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (skill: Skill) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const create = useMutation({
    mutationFn: () => createSkill(name.trim(), description.trim()),
    onSuccess: (s) => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] })
      setName('')
      setDescription('')
      onOpenChange(false)
      onCreated(s)
    },
  })
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-sm -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-4 text-sm font-semibold">{t('skills.newSkill')}</Dialog.Title>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (name.trim() && !create.isPending) create.mutate()
            }}
          >
            <div>
              <label htmlFor="skill-name" className={LABEL_CLASS}>
                {t('skills.nameLabel')}
              </label>
              <Input
                id="skill-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={100}
                autoFocus
                placeholder={t('skills.namePlaceholder')}
              />
            </div>
            <div>
              <label htmlFor="skill-desc" className={LABEL_CLASS}>
                {t('skills.descLabel')}
              </label>
              <textarea
                id="skill-desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                rows={3}
                placeholder={t('skills.descPlaceholder')}
                className="w-full resize-none rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
              />
            </div>
            {create.error && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {create.error instanceof ApiError ? create.error.message : t('skills.createFailed')}
              </p>
            )}
            <p className="text-xs text-zinc-400">{t('skills.createHint')}</p>
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
                {t('common.cancel')}
              </Dialog.Close>
              <button
                type="submit"
                disabled={!name.trim() || create.isPending}
                className="rounded-md bg-zinc-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900"
              >
                {create.isPending ? t('skills.creating') : t('common.create')}
              </button>
            </div>
          </form>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
function ImportSkillDialog({
  open,
  onOpenChange,
  onImported,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: (skill: Skill) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [mode, setMode] = useState<'url' | 'zip'>('url')
  const [url, setUrl] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const reset = () => {
    setUrl('')
    setFile(null)
    import_.reset()
  }
  const import_ = useMutation({
    mutationFn: () => (mode === 'url' ? importSkillFromUrl(url.trim()) : importSkillZip(file!)),
    onSuccess: (s) => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] })
      reset()
      onOpenChange(false)
      onImported(s)
    },
  })
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-sm -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-3 text-sm font-semibold">
            {t('skills.importSkill')}
          </Dialog.Title>

          <div className="mb-3 flex gap-1" role="tablist" aria-label={t('skills.importMode')}>
            {(
              [
                ['url', 'skills.fromUrl'],
                ['zip', 'skills.uploadZip'],
              ] as const
            ).map(([key, label]) => (
              <button
                key={key}
                type="button"
                role="tab"
                aria-selected={mode === key}
                className={cn(
                  'cursor-default rounded-md px-3 py-1.5 text-sm outline-none',
                  mode === key
                    ? 'bg-zinc-100 font-medium text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100'
                    : 'text-zinc-500 hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800',
                )}
                onClick={() => setMode(key)}
              >
                {t(label)}
              </button>
            ))}
          </div>

          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              const ready = mode === 'url' ? url.trim() !== '' : file !== null
              if (ready && !import_.isPending) import_.mutate()
            }}
          >
            {mode === 'url' ? (
              <div>
                <label htmlFor="import-url" className={LABEL_CLASS}>
                  {t('skills.urlLabel')}
                </label>
                <Input
                  id="import-url"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  autoFocus
                  placeholder="https://example.com/skill.zip"
                />
              </div>
            ) : (
              <div>
                <span className={LABEL_CLASS}>{t('skills.zipLabel')}</span>
                <input
                  type="file"
                  accept=".zip"
                  aria-label={t('skills.pickZip')}
                  onChange={(e) => setFile(e.target.files?.[0] ?? null)}
                  className="w-full cursor-pointer rounded-md border border-zinc-300 bg-white p-2 text-sm outline-none dark:border-zinc-700 dark:bg-zinc-900"
                />
                {file && (
                  <p className="mt-1 text-xs text-zinc-400">
                    {t('skills.fileChosen', { name: file.name })}
                  </p>
                )}
              </div>
            )}

            {import_.error && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {import_.error instanceof ApiError
                  ? import_.error.message
                  : t('skills.importFailed')}
              </p>
            )}
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
                {t('common.cancel')}
              </Dialog.Close>
              <button
                type="submit"
                disabled={import_.isPending || (mode === 'url' ? !url.trim() : !file)}
                className="rounded-md bg-zinc-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900"
              >
                {import_.isPending ? t('skills.importing') : t('skills.import')}
              </button>
            </div>
          </form>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
