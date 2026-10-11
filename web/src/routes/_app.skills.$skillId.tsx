import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { ArrowLeft, FilePlus2, Loader2, RotateCw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  deleteSkill,
  deleteSkillFile,
  getSkill,
  getSkillFiles,
  listSkillAgents,
  putSkillFile,
  refreshSkill,
  type SkillFile,
} from '@/api/skills'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageError, PageLoading } from '@/components/ui/states'
import { ApiError } from '@ploykit/client'
import { cn } from '@/lib/utils'
export const Route = createFileRoute('/_app/skills/$skillId')({
  component: SkillDetailPage,
})
const ORIGIN_KEYS: Record<string, { key: string; tone: 'neutral' | 'success' | 'warning' }> = {
  manual: { key: 'skills.originManual', tone: 'neutral' },
  import: { key: 'skills.originImport', tone: 'success' },
  url: { key: 'skills.originUrl', tone: 'warning' },
}
function SkillDetailPage() {
  const { t, i18n } = useTranslation()
  const { skillId } = Route.useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const skill = useQuery({ queryKey: ['skill', skillId], queryFn: () => getSkill(skillId) })
  const files = useQuery({
    queryKey: ['skill-files', skillId],
    queryFn: () => getSkillFiles(skillId),
    staleTime: 60000,
  })
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const [refreshNote, setRefreshNote] = useState<string | null>(null)
  const invalidateFiles = () => {
    void queryClient.invalidateQueries({ queryKey: ['skill-files', skillId] })
    void queryClient.invalidateQueries({ queryKey: ['skill', skillId] })
  }
  const refresh = useMutation({
    mutationFn: () => refreshSkill(skillId),
    onSuccess: (r) => {
      setRefreshNote(
        r.refreshed > 0 ? t('skills.refreshedN', { n: r.refreshed }) : t('skills.noChange'),
      )
      invalidateFiles()
    },
  })
  const remove = useMutation({
    mutationFn: () => deleteSkill(skillId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] })
      void navigate({ to: '/skills' })
    },
  })
  if (skill.isPending) return <PageLoading label={t('skills.loading')} />
  if (skill.isError) {
    return (
      <PageError
        title={t('skills.notFound')}
        message={skill.error instanceof Error ? skill.error.message : undefined}
      />
    )
  }
  const s = skill.data
  const badge = ORIGIN_KEYS[s.origin] ?? { key: s.origin, tone: 'neutral' as const }
  const list = files.data?.files ?? []
  const active =
    list.find((f) => f.id === selectedId) ?? (list.length > 0 && !creating ? list[0] : undefined)
  const fromUrl = s.origin === 'url'
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Link
          to="/skills"
          className="flex size-7 items-center justify-center rounded-md text-zinc-400 outline-none hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          aria-label={t('skills.backToList')}
        >
          <ArrowLeft className="size-4" aria-hidden="true" />
        </Link>
        <h1 className="min-w-0 truncate text-lg font-semibold">{s.name}</h1>
        <Badge variant={badge.tone}>{t(badge.key)}</Badge>
        <span className="text-xs text-zinc-400">
          {t('skills.createdAt', {
            date: new Date(s.created_at).toLocaleDateString(
              i18n.language === 'zh' ? 'zh-CN' : 'en-US',
            ),
          })}
        </span>

        <div className="ml-auto flex items-center gap-2">
          {fromUrl && (
            <Button
              size="sm"
              variant="outline"
              disabled={refresh.isPending}
              onClick={() => refresh.mutate()}
            >
              <RotateCw className="size-3.5" aria-hidden="true" />
              {t('skills.refetch')}
            </Button>
          )}
          {confirmingDelete ? (
            <>
              <Button
                size="sm"
                variant="danger"
                disabled={remove.isPending}
                onClick={() => remove.mutate()}
              >
                {t('skills.confirmDelete')}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setConfirmingDelete(false)}>
                {t('common.cancel')}
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              variant="ghost"
              className="text-red-600 dark:text-red-400"
              onClick={() => setConfirmingDelete(true)}
            >
              <Trash2 className="size-3.5" aria-hidden="true" />
              {t('skills.deleteSkill')}
            </Button>
          )}
          {refresh.isPending && (
            <Loader2 className="size-3.5 animate-spin text-zinc-400" aria-hidden="true" />
          )}
        </div>
      </div>
      {confirmingDelete && <p className="text-xs text-zinc-500">{t('skills.deleteHint')}</p>}
      {refreshNote && (
        <p className="text-xs text-emerald-600 dark:text-emerald-400">{refreshNote}</p>
      )}
      {s.description && <p className="text-sm text-zinc-500 dark:text-zinc-400">{s.description}</p>}

      <BoundAgentsBar skillId={skillId} />

      {files.isPending && <PageLoading label={t('skills.loadingFiles')} />}
      {files.isError && (
        <PageError
          title={t('skills.filesFailed')}
          message={files.error instanceof Error ? files.error.message : undefined}
          onRetry={() => void files.refetch()}
        />
      )}

      {files.data && (
        <div className="flex gap-4">
          <div className="w-52 shrink-0 space-y-1">
            <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
              {list.map((f) => (
                <FileRow
                  key={f.id}
                  file={f}
                  active={!creating && active?.id === f.id}
                  onSelect={() => {
                    setSelectedId(f.id)
                    setCreating(false)
                  }}
                  onDeleted={invalidateFiles}
                  skillId={skillId}
                />
              ))}
              {list.length === 0 && (
                <li className="px-3 py-2.5 text-xs text-zinc-400">{t('skills.noFiles')}</li>
              )}
            </ul>
            <Button
              size="sm"
              variant="outline"
              className="w-full"
              onClick={() => {
                setCreating(true)
                setSelectedId(null)
              }}
            >
              <FilePlus2 className="size-3.5" aria-hidden="true" />
              {t('skills.newFile')}
            </Button>
          </div>

          <div className="min-w-0 flex-1">
            {creating ? (
              <NewFileForm
                skillId={skillId}
                onSaved={() => {
                  setCreating(false)
                  invalidateFiles()
                }}
                onCancel={() => setCreating(false)}
              />
            ) : active ? (
              <FileEditor
                key={active.id + ':' + (files.data.manifest_sha256 ?? '')}
                skillId={skillId}
                file={active}
                onSaved={invalidateFiles}
              />
            ) : (
              <p className="pt-8 text-center text-sm text-zinc-400">{t('skills.pickFile')}</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
function FileRow({
  file,
  active,
  skillId,
  onSelect,
  onDeleted,
}: {
  file: SkillFile
  active: boolean
  skillId: string
  onSelect: () => void
  onDeleted: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const isMain = file.path === 'SKILL.md'
  const del = useMutation({
    mutationFn: () => deleteSkillFile(skillId, file.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skill-files', skillId] })
      onDeleted()
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : t('skills.deleteFailed')),
  })
  return (
    <li className="group relative">
      <button
        type="button"
        onClick={onSelect}
        className={cn(
          'flex w-full cursor-default items-center gap-2 px-3 py-2 text-left text-sm outline-none hover:bg-zinc-50 dark:hover:bg-zinc-900/50',
          active && 'bg-zinc-100 font-medium dark:bg-zinc-800/70',
        )}
      >
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{file.path}</span>
        <span className="shrink-0 text-[11px] text-zinc-400">{formatSize(file.size_bytes)}</span>
      </button>
      {!isMain &&
        (confirming ? (
          <div className="flex items-center gap-1 px-3 pb-1.5">
            <button
              type="button"
              className="text-[11px] text-red-600 underline-offset-2 hover:underline dark:text-red-400"
              onClick={() => del.mutate()}
            >
              {t('agents.confirmDelete')}
            </button>
            <button
              type="button"
              className="text-[11px] text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-300"
              onClick={() => setConfirming(false)}
            >
              {t('common.cancel')}
            </button>
          </div>
        ) : (
          <button
            type="button"
            aria-label={t('skills.deleteFileAria', { path: file.path })}
            className="absolute top-2 right-2 hidden text-zinc-400 group-hover:block hover:text-red-600 dark:hover:text-red-400"
            onClick={() => setConfirming(true)}
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
          </button>
        ))}
      {error && <p className="px-3 pb-1.5 text-[11px] text-red-600 dark:text-red-400">{error}</p>}
    </li>
  )
}
function FileEditor({
  skillId,
  file,
  onSaved,
}: {
  skillId: string
  file: SkillFile
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [content, setContent] = useState(file.content)
  const isMain = file.path === 'SKILL.md'
  const save = useMutation({
    mutationFn: () => putSkillFile(skillId, file.path, content),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skill-files', skillId] })
      onSaved()
    },
  })
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <p className="font-mono text-xs text-zinc-500">{file.path}</p>
        <Button
          size="sm"
          disabled={content === file.content || save.isPending}
          onClick={() => save.mutate()}
        >
          {save.isPending ? t('common.saving') : t('common.save')}
        </Button>
      </div>
      {isMain && content && !hasFrontmatter(content) && (
        <p className="text-xs text-amber-600 dark:text-amber-400">{t('skills.frontmatterHint')}</p>
      )}
      <textarea
        aria-label={t('skills.editFileAria', { path: file.path })}
        value={content}
        onChange={(e) => setContent(e.target.value)}
        spellCheck={false}
        className="h-[26rem] w-full resize-y rounded-md border border-zinc-300 bg-white p-3 font-mono text-xs leading-relaxed outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
      />
      {save.error && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {save.error instanceof ApiError ? save.error.message : t('common.saveFailed')}
        </p>
      )}
    </div>
  )
}
function NewFileForm({
  skillId,
  onSaved,
  onCancel,
}: {
  skillId: string
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [path, setPath] = useState('')
  const [content, setContent] = useState('')
  const create = useMutation({
    mutationFn: () => putSkillFile(skillId, path.trim(), content),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skill-files', skillId] })
      onSaved()
    },
  })
  const pathValid = path.trim() !== '' && !path.includes('..') && !path.startsWith('/')
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Input
          aria-label={t('skills.newFilePath')}
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder={t('skills.filePathPlaceholder')}
          className="font-mono text-xs"
        />
        <div className="flex shrink-0 gap-2">
          <Button size="sm" variant="ghost" onClick={onCancel}>
            {t('common.cancel')}
          </Button>
          <Button
            size="sm"
            disabled={!pathValid || create.isPending}
            onClick={() => create.mutate()}
          >
            {create.isPending ? t('skills.creating') : t('common.create')}
          </Button>
        </div>
      </div>
      <textarea
        aria-label={t('skills.newFileContent')}
        value={content}
        onChange={(e) => setContent(e.target.value)}
        spellCheck={false}
        placeholder={t('skills.fileContentPlaceholder')}
        className="h-72 w-full resize-y rounded-md border border-zinc-300 bg-white p-3 font-mono text-xs leading-relaxed outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
      />
      {create.error && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {create.error instanceof ApiError ? create.error.message : t('skills.createFailed')}
        </p>
      )}
    </div>
  )
}
function hasFrontmatter(content: string): boolean {
  return content.startsWith('---') && content.slice(3).includes('name:')
}
function BoundAgentsBar({ skillId }: { skillId: string }) {
  const { t } = useTranslation()
  const agents = useQuery({
    queryKey: ['skill-agents', skillId],
    queryFn: () => listSkillAgents(skillId),
    staleTime: 60000,
  })
  if (!agents.data || agents.data.length === 0) return null
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400">
      <span>{t('skills.usedBy')}</span>
      {agents.data.map((a) => (
        <Badge key={a.agent_id} variant={a.enabled ? 'success' : 'neutral'}>
          {a.name}
          {a.enabled ? '' : t('skills.agentDisabledSuffix')}
        </Badge>
      ))}
    </p>
  )
}
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`
  return `${(bytes / 1024).toFixed(1)}KB`
}
