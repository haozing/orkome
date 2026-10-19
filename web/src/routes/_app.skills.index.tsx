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
import { FormField } from '@ploykit/ui'
import { ApiError } from '@ploykit/client'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
export const Route = createFileRoute('/_app/skills/')({
  component: SkillsPage,
})
const SEARCH_DEBOUNCE_MS = 400
const ORIGIN_KEYS: Record<string, { key: string; tone: 'neutral' | 'success' | 'warning' }> = {
  manual: { key: 'skills.originManual', tone: 'neutral' },
  import: { key: 'skills.originImport', tone: 'success' },
  url: { key: 'skills.originUrl', tone: 'warning' },
}
export function SkillsPage() {
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
        <h1 className="text-lg font-semibold">{t('nav.skills')}</h1>
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
          description={debounced ? t('skills.noMatchDesc') : t('skills.emptyDesc')}
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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('skills.newSkill')}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (name.trim() && !create.isPending) create.mutate()
          }}
        >
          <FormField label={t('skills.nameLabel')} htmlFor="skill-name">
            <Input
              id="skill-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={100}
              autoFocus
              placeholder={t('skills.namePlaceholder')}
            />
          </FormField>
          <FormField label={t('skills.descLabel')} htmlFor="skill-desc">
            <Textarea
              id="skill-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={3}
              placeholder={t('skills.descPlaceholder')}
              className="resize-none"
            />
          </FormField>
          {create.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {create.error instanceof ApiError ? create.error.message : t('skills.createFailed')}
            </p>
          )}
          <DialogFooter>
            <DialogClose render={<Button type="button" variant="outline" />}>
              {t('common.cancel')}
            </DialogClose>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              {create.isPending ? t('skills.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
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
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('skills.importSkill')}</DialogTitle>
        </DialogHeader>

        <Tabs value={mode} onValueChange={(value) => setMode(value as 'url' | 'zip')}>
          <TabsList aria-label={t('skills.importMode')}>
            <TabsTrigger value="url">{t('skills.fromUrl')}</TabsTrigger>
            <TabsTrigger value="zip">{t('skills.uploadZip')}</TabsTrigger>
          </TabsList>
        </Tabs>

        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            const ready = mode === 'url' ? url.trim() !== '' : file !== null
            if (ready && !import_.isPending) import_.mutate()
          }}
        >
          {mode === 'url' ? (
            <FormField label={t('skills.urlLabel')} htmlFor="import-url">
              <Input
                id="import-url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                autoFocus
                placeholder="https://example.com/skill.zip"
              />
            </FormField>
          ) : (
            <FormField label={t('skills.zipLabel')} htmlFor="import-zip">
              <input
                id="import-zip"
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
            </FormField>
          )}

          {import_.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {import_.error instanceof ApiError
                ? import_.error.message
                : t('skills.importFailed')}
            </p>
          )}
          <DialogFooter>
            <DialogClose render={<Button type="button" variant="outline" />}>
              {t('common.cancel')}
            </DialogClose>
            <Button
              type="submit"
              disabled={import_.isPending || (mode === 'url' ? !url.trim() : !file)}
            >
              {import_.isPending ? t('skills.importing') : t('skills.import')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
