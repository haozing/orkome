import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link2, Link2Off } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { listAgents } from '@/api/orchestration'
import { setAgentPack, setSkillPack } from '@/api/packs'
import { listSkills } from '@/api/skills'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// 资产挂载（能力包平台化 P2，详情页⑤资产区块）：
//  - PackAttachPickerDialog：勾选工作区全部智能体/技能里未挂载的部分，
//    确认后逐个 POST setXxxPack(id, packId)（admin 端点，member 403 时
//    行内报错不关闭——照 pack-sub-error 模式，不做权限隐藏）；
//  - PackDetachButton：已挂行内「卸下」小按钮，POST setXxxPack(id, '')。
// 两者成功后 invalidate ['pack', packId] 刷新详情聚合。
// 独立成文件以便组件级测试（同 packs-page 的拆法原因：autoCodeSplitting
// 拆走路由模块非 Route 导出）。

type AttachKind = 'agent' | 'skill'

/** picker 行只需要 id+name（listAgents 的 Agent / listSkills 的 Skill 的公共子集）。 */
interface AttachCandidate {
  id: string
  name: string
}

export function PackAttachPickerDialog({
  packId,
  kind,
  existingIds,
  onClose,
}: {
  packId: string
  kind: AttachKind
  /** 详情页已列出的资产 id（picker 过滤掉，避免重复挂载） */
  existingIds: string[]
  onClose: () => void
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  // 全量列表（listAgents/listSkills 均按 X-Workspace-Id 头寻址工作区）。
  // 显式标注返回公共子集 AttachCandidate——两个 API 的行形状不同，联合
  // 返回会让 useQuery 的 QueryFunction 泛型推不出来（build 期 TS 报错）。
  const candidates = useQuery<AttachCandidate[]>({
    queryKey: kind === 'agent' ? ['agents'] : ['skills'],
    queryFn: async () => {
      const rows = kind === 'agent' ? await listAgents() : await listSkills()
      return rows.map((r) => ({ id: r.id, name: r.name }))
    },
  })
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [submitErr, setSubmitErr] = useState(false)
  const toggle = (id: string, on: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (on) next.add(id)
      else next.delete(id)
      return next
    })

  const attach = useMutation({
    mutationFn: async () => {
      // 逐个提交（契约端点是单资产粒度）；any-失败即抛——行内报错、Dialog
      // 不关，已成功的让"全部已挂"自然收敛（重开 picker 会被过滤）。
      for (const id of selected) {
        if (kind === 'agent') await setAgentPack(id, packId)
        else await setSkillPack(id, packId)
      }
    },
    onSuccess: () => {
      setSubmitErr(false)
      void qc.invalidateQueries({ queryKey: ['pack', packId] })
      onClose()
    },
    onError: () => setSubmitErr(true),
  })

  const existing = new Set(existingIds)
  const rows = (candidates.data ?? []).filter((r) => !existing.has(r.id))
  return (
    <Dialog open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t(kind === 'agent' ? 'packsInstall.attachAgentTitle' : 'packsInstall.attachSkillTitle')}
          </DialogTitle>
          <DialogDescription>
            {t('packsInstall.attachEmpty', {
              kind: t(kind === 'agent' ? 'packsInstall.kindAgent' : 'packsInstall.kindSkill'),
            })}
          </DialogDescription>
        </DialogHeader>
        {candidates.isPending && (
          <p className="text-sm text-zinc-500" data-testid="pack-attach-loading">
            {t('packsInstall.attachLoading')}
          </p>
        )}
        {candidates.isError && (
          <p role="alert" className="text-sm text-red-600 dark:text-red-400">
            {t('packsInstall.attachLoadFailed')}
          </p>
        )}
        {candidates.isSuccess && (
          <>
            {rows.length === 0 ? (
              <p className="text-sm text-zinc-500" data-testid="pack-attach-none">
                {t('packsInstall.attachEmpty', {
                  kind: t(kind === 'agent' ? 'packsInstall.kindAgent' : 'packsInstall.kindSkill'),
                })}
              </p>
            ) : (
              <ul className="space-y-1" data-testid="pack-attach-list">
                {rows.map((r) => (
                  <li key={r.id}>
                    <label className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-zinc-100 dark:hover:bg-zinc-800">
                      <input
                        type="checkbox"
                        checked={selected.has(r.id)}
                        onChange={(e) => toggle(r.id, e.target.checked)}
                        className="size-3.5 accent-zinc-700"
                        data-testid="pack-attach-checkbox"
                      />
                      <span className="min-w-0 truncate">{r.name}</span>
                    </label>
                  </li>
                ))}
              </ul>
            )}
            {submitErr && (
              <p
                role="alert"
                className="text-xs text-red-600 dark:text-red-400"
                data-testid="pack-attach-error"
              >
                {t('packsInstall.attachFailed')}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={onClose}>
                {t('common.cancel')}
              </Button>
              <Button
                disabled={selected.size === 0 || attach.isPending}
                data-testid="pack-attach-confirm"
                onClick={() => attach.mutate()}
              >
                <Link2 className="size-3.5" aria-hidden="true" />
                {attach.isPending
                  ? t('packsInstall.attachSubmitting')
                  : t('packsInstall.attachConfirm')}
              </Button>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

/** 已挂资产行尾的「卸下」小按钮：POST setXxxPack(id, '')（空串=卸下）。 */
export function PackDetachButton({
  packId,
  kind,
  id,
}: {
  packId: string
  kind: AttachKind
  id: string
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [err, setErr] = useState(false)
  const detach = useMutation({
    mutationFn: () => (kind === 'agent' ? setAgentPack(id, '') : setSkillPack(id, '')),
    onSuccess: () => {
      setErr(false)
      void qc.invalidateQueries({ queryKey: ['pack', packId] })
    },
    onError: () => setErr(true),
  })
  return (
    <span className="inline-flex shrink-0 items-center gap-1.5">
      <Button
        size="xs"
        variant="ghost"
        disabled={detach.isPending}
        aria-label={t(kind === 'agent' ? 'packsInstall.detachAgent' : 'packsInstall.detachSkill')}
        data-testid={kind === 'agent' ? 'pack-agent-detach' : 'pack-skill-detach'}
        onClick={() => detach.mutate()}
      >
        <Link2Off className="size-3" aria-hidden="true" />
        {t(kind === 'agent' ? 'packsInstall.detachAgent' : 'packsInstall.detachSkill')}
      </Button>
      {err && (
        <span
          role="alert"
          className="text-xs text-red-600 dark:text-red-400"
          data-testid="pack-attach-error"
        >
          {t('packsInstall.detachFailed')}
        </span>
      )}
    </span>
  )
}
