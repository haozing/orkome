import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { ArrowLeft, FilePlus2, Loader2, RotateCw, Trash2 } from 'lucide-react'
import { Suspense, lazy, useState } from 'react'
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
import { FormField, Skeleton, toast, useConfirm } from '@ploykit/ui'
import { ApiError } from '@ploykit/client'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageError, PageLoading } from '@/components/ui/states'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'
const RichEditor = lazy(() =>
  import('@/components/editor/rich-editor').then((m) => ({ default: m.RichEditor })),
)
export const Route = createFileRoute('/_app/skills/$skillId')({
  component: SkillDetailPage,
})
const ORIGIN_KEYS: Record<string, { key: string; tone: 'neutral' | 'success' | 'warning' }> = {
  manual: { key: 'skills.originManual', tone: 'neutral' },
  import: { key: 'skills.originImport', tone: 'success' },
  url: { key: 'skills.originUrl', tone: 'warning' },
}
export function SkillDetailPage() {
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
  const confirm = useConfirm()
  const invalidateFiles = () => {
    void queryClient.invalidateQueries({ queryKey: ['skill-files', skillId] })
    void queryClient.invalidateQueries({ queryKey: ['skill', skillId] })
  }
  const refresh = useMutation({
    mutationFn: () => refreshSkill(skillId),
    onSuccess: (r) => {
      toast.success(
        r.refreshed > 0 ? t('skills.refreshedN', { n: r.refreshed }) : t('skills.noChange'),
      )
      invalidateFiles()
    },
    onError: (e) => {
      toast.error(e instanceof ApiError ? e.message : t('common.error'))
    },
  })
  const remove = useMutation({
    mutationFn: () => deleteSkill(skillId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] })
      void navigate({ to: '/skills' })
    },
  })
  const confirmRemoveSkill = async () => {
    const ok = await confirm({
      title: t('skills.deleteSkill'),
      description: t('skills.deleteHint'),
      confirmText: t('skills.confirmDelete'),
      cancelText: t('common.cancel'),
      danger: true,
    })
    if (ok && !remove.isPending) remove.mutate()
  }
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
              {refresh.isPending ? (
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              ) : (
                <RotateCw className="size-3.5" aria-hidden="true" />
              )}
              {t('skills.refetch')}
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            className="text-red-600 dark:text-red-400"
            disabled={remove.isPending}
            onClick={() => void confirmRemoveSkill()}
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
            {t('skills.deleteSkill')}
          </Button>
        </div>
      </div>
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
                isMainSkill={!list.some((f) => f.path === 'SKILL.md')}
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
                skillName={s.name}
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
  const confirm = useConfirm()
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
  const confirmRemoveFile = async () => {
    const ok = await confirm({
      title: t('skills.deleteFileAria', { path: file.path }),
      confirmText: t('skills.confirmDelete'),
      cancelText: t('common.cancel'),
      danger: true,
    })
    if (ok && !del.isPending) del.mutate()
  }
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
      {!isMain && (
        <button
          type="button"
          aria-label={t('skills.deleteFileAria', { path: file.path })}
          className="absolute top-2 right-2 text-zinc-400 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 hover:text-red-600 dark:hover:text-red-400"
          onClick={() => void confirmRemoveFile()}
        >
          <Trash2 className="size-3.5" aria-hidden="true" />
        </button>
      )}
      {error && <p className="px-3 pb-1.5 text-[11px] text-red-600 dark:text-red-400">{error}</p>}
    </li>
  )
}
function FileEditor({
  skillId,
  file,
  skillName,
  onSaved,
}: {
  skillId: string
  file: SkillFile
  skillName: string
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isMain = file.path === 'SKILL.md'
  const isMarkdown = file.path.endsWith('.md')

  // SKILL.md 分区编辑：frontmatter（--- 头）是元数据不是正文——富文本会把
  // `---` 渲染成分割线、`name: x` 渲染成标题。头部拆出来独立编辑（两个
  // 小输入框），正文进 RichEditor；保存时自动拼回，用户不需要懂 frontmatter。
  const parsed = isMain ? splitFrontmatter(file.content) : null
  const [fmName, setFmName] = useState(parsed?.name ?? '')
  const [fmDesc, setFmDesc] = useState(parsed?.description ?? '')
  const [body, setBody] = useState(parsed?.body ?? file.content)

  const save = useMutation({
    mutationFn: () => {
      const content = isMain
        ? `---\nname: ${fmName.trim() || skillName}\n${fmDesc.trim() ? `description: ${fmDesc.trim()}\n` : ''}---\n\n${body}`
        : body
      return putSkillFile(skillId, file.path, content)
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skill-files', skillId] })
      onSaved()
    },
  })
  const dirty = isMain
    ? body !== (parsed?.body ?? '') || fmName !== (parsed?.name ?? '') || fmDesc !== (parsed?.description ?? '')
    : body !== file.content
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="font-mono text-xs text-zinc-500">{file.path}</p>
        <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? t('common.saving') : t('common.save')}
        </Button>
      </div>

      {isMain && (
        <div className="grid gap-3 sm:grid-cols-2">
          <FormField label={t('skills.fmName')} htmlFor="skill-fm-name">
            <Input
              id="skill-fm-name"
              value={fmName}
              maxLength={100}
              onChange={(e) => setFmName(e.target.value)}
              className="font-mono text-xs"
            />
          </FormField>
          <FormField label={t('skills.fmDesc')} htmlFor="skill-fm-desc">
            <Input
              id="skill-fm-desc"
              value={fmDesc}
              maxLength={500}
              onChange={(e) => setFmDesc(e.target.value)}
            />
          </FormField>
        </div>
      )}

      {isMarkdown ? (
        <div className="min-h-[22rem] rounded-md border border-zinc-200 dark:border-zinc-800">
          <Suspense fallback={<Skeleton className="h-[22rem] w-full" />}>
            <RichEditor
              content={body}
              onChange={setBody}
              placeholder={t('skills.fileContentPlaceholder')}
              ariaLabel={t('skills.editFileAria', { path: file.path })}
              minRows={16}
              bubbleMenu={false}
            />
          </Suspense>
        </div>
      ) : (
        <Textarea
          aria-label={t('skills.editFileAria', { path: file.path })}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          spellCheck={false}
          className="h-[26rem] resize-y p-3 font-mono text-xs leading-relaxed"
        />
      )}
      {save.error && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {save.error instanceof ApiError ? save.error.message : t('common.saveFailed')}
        </p>
      )}
    </div>
  )
}

/** 拆 SKILL.md 的 frontmatter 头（---\nname: x\ndescription: y\n---）。
 *  无头/头不完整时 name/description 为空串、body 返回原文（服务端
 *  ParseFrontmatter 同款宽松解析）。 */
function splitFrontmatter(content: string): { name: string; description: string; body: string } {
  if (!content.startsWith('---')) return { name: '', description: '', body: content }
  const rest = content.slice(3)
  const end = rest.indexOf('\n---')
  if (end < 0) return { name: '', description: '', body: content }
  let name = ''
  let description = ''
  for (const line of rest.slice(0, end).split('\n')) {
    const i = line.indexOf(':')
    if (i <= 0) continue
    const k = line.slice(0, i).trim()
    const v = line.slice(i + 1).trim()
    if (k === 'name') name = v
    if (k === 'description') description = v
  }
  const body = rest.slice(end + 4).replace(/^\n+/, '')
  return { name, description, body }
}
function NewFileForm({
  skillId,
  isMainSkill,
  onSaved,
  onCancel,
}: {
  skillId: string
  isMainSkill: boolean
  onSaved: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [path, setPath] = useState(isMainSkill ? 'SKILL.md' : '')
  const [content, setContent] = useState(
    isMainSkill ? `---\nname: \ndescription: \n---\n\n` : '',
  )
  const [isMarkdown, setIsMarkdown] = useState(isMainSkill)
  const create = useMutation({
    mutationFn: () => putSkillFile(skillId, path.trim(), content),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skill-files', skillId] })
      onSaved()
    },
  })
  const pathValid = path.trim() !== '' && !path.includes('..') && !path.startsWith('/')
  // 路径变化时同步编辑器形态（md → 富文本，其余 → 代码 textarea）
  const pathIsMd = path.trim().endsWith('.md')
  if (pathIsMd !== isMarkdown) setIsMarkdown(pathIsMd)
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
      {isMarkdown ? (
        <div className="min-h-72 rounded-md border border-zinc-200 dark:border-zinc-800">
          <Suspense fallback={<Skeleton className="h-72 w-full" />}>
            <RichEditor
              content={content}
              onChange={setContent}
              placeholder={t('skills.fileContentPlaceholder')}
              ariaLabel={t('skills.newFileContent')}
              minRows={14}
              bubbleMenu={false}
            />
          </Suspense>
        </div>
      ) : (
        <Textarea
          aria-label={t('skills.newFileContent')}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          spellCheck={false}
          placeholder={t('skills.fileContentPlaceholder')}
          className="h-72 resize-y p-3 font-mono text-xs leading-relaxed"
        />
      )}
      {create.error && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {create.error instanceof ApiError ? create.error.message : t('skills.createFailed')}
        </p>
      )}
    </div>
  )
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
