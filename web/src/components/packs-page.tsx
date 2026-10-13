import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Boxes, PackageOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { listPacks, type PackEntry } from '@/api/packs'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'

// /packs 列表页的视图主体（Pack 一等公民化 P1，docs/pack-first-class-design-2026-10-13.md §2.2）：
// 已装 pack 一行一卡（名 / 版本 / 安装日期 / 三个资产计数），点击进详情五区块。
// 安装/卸载不在页面做——走 pack-provision CLI，空态给命令引导。
// 独立成组件文件的原因同 task-transcript-drawer：router 插件的
// autoCodeSplitting 会拆走路由模块的非 Route 导出，页面级测试须从这里引用。

/** /packs 列表页视图（数据拉取 + 三态 + 行渲染）。 */
export function PacksPageView() {
  const { t, i18n } = useTranslation()
  const packs = useQuery({ queryKey: ['packs'], queryFn: listPacks })
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })
  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-lg font-semibold">{t('packs2.pageTitle')}</h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{t('packs2.pageIntro')}</p>
      </div>

      {packs.isPending && <PageLoading label={t('packs2.loading')} />}

      {packs.isError && (
        <PageError
          title={t('common.error')}
          message={packs.error instanceof Error ? packs.error.message : undefined}
          onRetry={() => void packs.refetch()}
        />
      )}

      {packs.isSuccess && packs.data.length === 0 && (
        <PageEmpty
          title={t('packs2.emptyTitle')}
          description={t('packs2.emptyDesc')}
          action={
            <div className="max-w-full text-left">
              <p className="mb-1 text-xs text-zinc-400 dark:text-zinc-500">
                {t('packs2.installCmdLabel')}
              </p>
              <code
                className="block overflow-x-auto rounded-md border border-zinc-200 bg-zinc-50 px-3 py-2 font-mono text-xs whitespace-nowrap dark:border-zinc-700 dark:bg-zinc-900"
                data-testid="packs-install-cmd"
              >
                {t('packs2.installCmd')}
              </code>
            </div>
          }
        />
      )}

      {packs.data && packs.data.length > 0 && (
        <ul
          className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800"
          data-testid="packs2-list"
        >
          {packs.data.map((p) => (
            <li key={p.id}>
              <Link
                to="/packs/$packId"
                params={{ packId: p.id }}
                className="flex items-center gap-3 px-3 py-2.5 text-sm outline-none hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
                data-testid="packs2-row"
              >
                <Boxes className="size-4 shrink-0 text-zinc-400" aria-hidden="true" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono font-medium">{p.name}</span>
                  <span className="block truncate text-xs text-zinc-500 dark:text-zinc-400">
                    v{p.version || '–'} · {t('packs2.installedAtLabel')} {fmtDate(p.installed_at)}
                  </span>
                </span>
                <PackAssetCounts pack={p} />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** 行尾三个资产计数徽标（服务器 / 智能体 / 技能）。 */
export function PackAssetCounts({ pack }: { pack: PackEntry }) {
  const { t } = useTranslation()
  const items = [
    { icon: Boxes, count: pack.server_count, label: t('packs2.colServers') },
    { icon: PackageOpen, count: pack.agent_count, label: t('packs2.colAgents') },
  ]
  return (
    <span className="hidden shrink-0 items-center gap-2 text-xs text-zinc-500 sm:flex dark:text-zinc-400">
      {items.map(({ icon: Icon, count, label }) => (
        <span key={label} className="flex items-center gap-1" title={label}>
          <Icon className="size-3.5 text-zinc-400" aria-hidden="true" />
          {count}
        </span>
      ))}
      <span>
        {t('packs2.colSkills')} {pack.skill_count}
      </span>
    </span>
  )
}
