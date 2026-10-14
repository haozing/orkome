import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Check, Copy, Download, Save } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createSkill, putSkillFile } from '@/api/skills'
import { getPackUsageDocument } from '@/api/packs'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

// 使用文档 Dialog（能力包平台化 P2）：打开时拉 GET usage-document，
// <pre> 平铺 markdown 原文（不做 md 渲染，保持零依赖），底部三动作：
// 复制 / 下载 .md（Blob + a[download]）/ 存为技能（createSkill →
// putSkillFile(SKILL.md) 链式两调）。独立成文件以便组件级测试（同
// packs-page 的拆法原因：autoCodeSplitting 拆走路由模块非 Route 导出）。

export function PackUsageDialog({
  packId,
  packName,
  onClose,
}: {
  packId: string
  /** 存为技能的默认名与下载文件名取自包名 */
  packName: string
  onClose: () => void
}) {
  const { t } = useTranslation()
  const doc = useQuery({
    queryKey: ['pack-usage-doc', packId],
    queryFn: () => getPackUsageDocument(packId),
    // Dialog 存活期内容不回源——下载/复制/存技能都吃同一次快照
    gcTime: 0,
    retry: false,
  })
  const markdown = doc.data?.markdown ?? ''

  const [copied, setCopied] = useState(false)
  const doCopy = async () => {
    await navigator.clipboard.writeText(markdown)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const doDownload = () => {
    // Blob + 隐形 a[download]：无需后端参与，文件名取包名（非法字符替换为 -）
    const safe = packName.replace(/[^\w.-]+/g, '-')
    const url = URL.createObjectURL(new Blob([markdown], { type: 'text/markdown' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${safe}-usage.md`
    a.click()
    URL.revokeObjectURL(url)
  }

  // 存为技能：名称输入（默认 `{pack} 使用指南`）→ 确认后 createSkill →
  // putSkillFile('SKILL.md', ...) 链式两调；成功给行内提示 + 技能页链接。
  // SKILL.md 是服务端受护文件名：必须带 YAML frontmatter 的 name 才收
  //（MainFilenameGuard+ParseFrontmatter，裸 markdown 400），故包一层头。
  const [skillName, setSkillName] = useState(`${packName} 使用指南`)
  const [savedSkillId, setSavedSkillId] = useState<string | null>(null)
  const [saveErr, setSaveErr] = useState(false)
  const saveSkill = useMutation({
    mutationFn: async () => {
      const skill = await createSkill(skillName.trim(), `由能力包 ${packName} 使用文档生成`)
      const fm = `---\nname: ${skillName.trim()}\ndescription: 由能力包 ${packName} 使用文档生成\n---\n\n`
      await putSkillFile(skill.id, 'SKILL.md', fm + markdown)
      return skill
    },
    onSuccess: (skill) => {
      setSavedSkillId(skill.id)
      setSaveErr(false)
    },
    onError: () => setSaveErr(true),
  })

  return (
    <Dialog open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('packsInstall.usageTitle')}</DialogTitle>
        </DialogHeader>
        {doc.isPending && (
          <p className="text-sm text-zinc-500" data-testid="pack-usage-loading">
            {t('packsInstall.usageLoading')}
          </p>
        )}
        {doc.isError && (
          <p role="alert" className="text-sm text-red-600 dark:text-red-400">
            {t('packsInstall.usageFailed')}
          </p>
        )}
        {doc.isSuccess && (
          <>
            {/* markdown 原文平铺（零依赖，不做 md 渲染） */}
            <pre
              className="overflow-auto max-h-[50vh] rounded-md border border-zinc-200 bg-zinc-50 p-3 font-mono text-xs whitespace-pre-wrap dark:border-zinc-700 dark:bg-zinc-900"
              data-testid="pack-usage-content"
            >
              {markdown}
            </pre>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                data-testid="pack-usage-copy"
                onClick={() => void doCopy()}
              >
                {copied ? (
                  <>
                    <Check className="size-3.5" aria-hidden="true" />
                    {t('packsInstall.usageCopied')}
                  </>
                ) : (
                  <>
                    <Copy className="size-3.5" aria-hidden="true" />
                    {t('packsInstall.usageCopy')}
                  </>
                )}
              </Button>
              <Button
                size="sm"
                variant="outline"
                data-testid="pack-usage-download"
                onClick={doDownload}
              >
                <Download className="size-3.5" aria-hidden="true" />
                {t('packsInstall.usageDownload')}
              </Button>
            </div>
            <div className="space-y-1.5 rounded-md border border-zinc-200 p-3 dark:border-zinc-800">
              <label htmlFor="pack-usage-skill-name" className="mb-1 block text-xs text-zinc-500">
                {t('packsInstall.usageSkillNameLabel')}
              </label>
              <div className="flex flex-wrap items-center gap-2">
                <Input
                  id="pack-usage-skill-name"
                  value={skillName}
                  onChange={(e) => setSkillName(e.target.value)}
                  className="min-w-0 flex-1"
                  data-testid="pack-usage-skill-name"
                />
                <Button
                  size="sm"
                  disabled={!skillName.trim() || saveSkill.isPending || savedSkillId !== null}
                  data-testid="pack-usage-save-skill"
                  onClick={() => saveSkill.mutate()}
                >
                  <Save className="size-3.5" aria-hidden="true" />
                  {saveSkill.isPending
                    ? t('packsInstall.usageSavingSkill')
                    : t('packsInstall.usageSaveSkill')}
                </Button>
              </div>
              {savedSkillId && (
                <p
                  className="flex items-center gap-2 text-xs text-emerald-700 dark:text-emerald-400"
                  data-testid="pack-usage-skill-saved"
                >
                  {t('packsInstall.usageSkillSaved')}
                  <Link
                    to="/skills/$skillId"
                    params={{ skillId: savedSkillId }}
                    className="underline outline-none focus-visible:ring-2 focus-visible:ring-zinc-400"
                  >
                    {t('packsInstall.usageSkillLink')}
                  </Link>
                </p>
              )}
              {saveErr && (
                <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                  {t('packsInstall.usageSkillFailed')}
                </p>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
