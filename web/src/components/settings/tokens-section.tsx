import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Checkbox, SecretModal } from '@ploykit/ui'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createToken,
  revokeToken,
  tokensQueryOptions,
  type PatSecret,
  type PatToken,
} from '@/api/identity'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import {
  INLINE_EMPTY_CLS,
  INLINE_ERROR_CLS,
  INLINE_LOADING_CLS,
  useSettingsTimeFormatter,
} from '@/components/settings/shared'
import { useWorkspaceStore } from '@/stores/workspace'

// PAT 管理面（design §3.3）：从 /access 页抽出的共享组件——settings 渲染完整面，
// access 页引用同组件保留内联创建语境。一次性 token 明文只存组件 useState。
// query key 沿用 ['tokens']（tokensQueryOptions）。
// 分页（2026-10-15 裁决"每页五条"）：纯前端切片——PAT 总量天然小，无需服务端
// 分页（框架 GET /api/tokens 无分页参数）；每页 5 条，页码越界时钳到末页
// （删除/撤销后当前页可能空）。

const PAGE_SIZE = 5

// PAT 收口（P1-1）：默认 30 天（安全默认，不再默认永不过期），「永不过期」
// 降为末位的显式选择。
const TTL_OPTIONS = [
  { value: 30 * 86400, key: 'mcp.ttl30' },
  { value: 90 * 86400, key: 'mcp.ttl90' },
  { value: 180 * 86400, key: 'mcp.ttl180' },
  { value: 0, key: 'mcp.ttlNever' },
] as const

function tokenStatus(t: PatToken): { key: string; tone: 'success' | 'warning' | 'neutral' } {
  if (t.revoked_at) return { key: 'mcp.tokRevoked', tone: 'neutral' }
  if (t.expires_at && new Date(t.expires_at) < new Date())
    return { key: 'mcp.tokExpired', tone: 'warning' }
  return { key: 'mcp.tokActive', tone: 'success' }
}

export function TokensSection() {
  const { t } = useTranslation()
  const fmtTime = useSettingsTimeFormatter()
  const queryClient = useQueryClient()
  const tokens = useQuery(tokensQueryOptions)
  const currentId = useWorkspaceStore((s) => s.currentId)
  const [name, setName] = useState('')
  const [ttl, setTtl] = useState<number>(30 * 86400)
  // 「限定工作区」默认开：提交 scope.workspace_ids=[当前工作区]，令牌只能
  // 操作该区。无当前工作区（账户级语境）时开关禁用且不携带 scope 字段。
  const [limitToWs, setLimitToWs] = useState(true)
  const [page, setPage] = useState(1)
  const [secret, setSecret] = useState<PatSecret | null>(null)
  const [confirmingId, setConfirmingId] = useState<string | null>(null)
  const invalidateTokens = () =>
    queryClient.invalidateQueries({ queryKey: tokensQueryOptions.queryKey })
  const create = useMutation({
    mutationFn: () =>
      createToken(name.trim(), ttl, limitToWs && currentId ? { workspace_ids: [currentId] } : undefined),
    onSuccess: (s) => {
      setSecret(s)
      setName('')
      void invalidateTokens()
    },
  })
  const revoke = useMutation({
    mutationFn: revokeToken,
    onSuccess: () => {
      setConfirmingId(null)
      void invalidateTokens()
    },
  })
  const submitCreate = () => {
    const trimmed = name.trim()
    if (!trimmed || trimmed.length > 100) return
    create.mutate()
  }

  // 分页切片：safePage 钳位防越界（撤销/列表变化后当前页可能变空）。
  const total = tokens.data?.length ?? 0
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const safePage = Math.min(page, pageCount)
  const pageTokens = (tokens.data ?? []).slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE)

  return (
    <div>
      {/* 一次性 token 明文（ui-sweep 裁决③）：kit SecretModal 收口——勾选
          「已保存」前关不掉，不可再取的警示由组件内置说一次。 */}
      {secret && (
        <SecretModal
          open
          onOpenChange={(o) => {
            if (!o) setSecret(null)
          }}
          secretName={secret.name}
          secretValue={secret.token}
          title={t('mcp.createdOnce', { name: secret.name })}
        />
      )}

      <div className="flex flex-wrap gap-2">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') submitCreate()
          }}
          placeholder={t('mcp.tokenNamePlaceholder')}
          maxLength={100}
          aria-label={t('mcp.tokenNamePlaceholder')}
          className="min-w-0 flex-1"
          data-testid="token-name-input"
        />
        <Select
          value={ttl}
          onChange={(e) => setTtl(Number(e.target.value))}
          aria-label={t('mcp.ttlLabel')}
          className="w-28"
        >
          {TTL_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {t(opt.key)}
            </option>
          ))}
        </Select>
        <Button onClick={submitCreate} disabled={!name.trim() || create.isPending} data-testid="token-create">
          {create.isPending ? t('mcp.creating') : t('mcp.createToken')}
        </Button>
      </div>
      <label
        className="mt-2 flex w-fit cursor-pointer items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400"
        data-testid="token-scope-limit"
      >
        <Checkbox
          checked={limitToWs && !!currentId}
          onCheckedChange={(c) => setLimitToWs(c === true)}
          disabled={!currentId}
        />
        <span>{t('mcp.tokensScopeLimit')}</span>
        <span className="text-zinc-400 dark:text-zinc-500">{t('mcp.tokensScopeLimitDesc')}</span>
      </label>
      {create.error && (
        <p className="mt-2 text-xs text-red-600 dark:text-red-400">{create.error.message}</p>
      )}

      <div className="mt-4 border-t border-zinc-200 dark:border-zinc-800">
        {tokens.isPending && (
          <PageLoading label={t('common.loading')} className={INLINE_LOADING_CLS} />
        )}
        {tokens.error && (
          <PageError
            title={t('common.error')}
            message={tokens.error.message}
            onRetry={() => void tokens.refetch()}
            className={INLINE_ERROR_CLS}
          />
        )}
        {tokens.data && tokens.data.length === 0 && (
          <PageEmpty title={t('mcp.noCredentials')} className={INLINE_EMPTY_CLS} />
        )}
        {tokens.data && tokens.data.length > 0 && (
          <>
          <div className="divide-y divide-zinc-200 dark:divide-zinc-800">
            {pageTokens.map((tok) => {
              const status = tokenStatus(tok)
              const confirming = confirmingId === tok.id
              return (
                <div key={tok.id} className="py-2.5">
                  <div className="flex items-center gap-3">
                    <div className="min-w-0 flex-1">
                      <span className="text-sm font-medium">{tok.name}</span>
                      <span className="ml-2 font-mono text-xs text-zinc-400">{tok.prefix}…</span>
                    </div>
                    <Badge variant={status.tone}>{t(status.key)}</Badge>
                    {!tok.revoked_at &&
                      (confirming ? (
                        <div className="flex items-center gap-1">
                          <Button
                            variant="danger"
                            size="sm"
                            disabled={revoke.isPending}
                            onClick={() => revoke.mutate(tok.id)}
                          >
                            {t('mcp.confirmRevoke')}
                          </Button>
                          <Button variant="ghost" size="sm" onClick={() => setConfirmingId(null)}>
                            {t('common.cancel')}
                          </Button>
                        </div>
                      ) : (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-red-600 dark:text-red-400"
                          onClick={() => setConfirmingId(tok.id)}
                          data-testid={`token-revoke-${tok.id}`}
                        >
                          {t('mcp.revoke')}
                        </Button>
                      ))}
                  </div>
                  <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
                    {t('mcp.tokenMeta', {
                      created: fmtTime(tok.created_at),
                      used: fmtTime(tok.last_used_at),
                    })}
                    {tok.expires_at && ` · ${t('mcp.expiresAt', { time: fmtTime(tok.expires_at) })}`}
                  </p>
                </div>
              )
            })}
          </div>
          {pageCount > 1 && (
            <div className="flex items-center justify-between pt-3 text-xs text-zinc-500 dark:text-zinc-400">
              <span data-testid="token-page-info">
                {t('mcp.pageInfo', { page: safePage, pages: pageCount, total: tokens.data.length })}
              </span>
              <span className="flex gap-2">
                <Button
                  size="xs"
                  variant="outline"
                  disabled={safePage <= 1}
                  data-testid="token-page-prev"
                  onClick={() => setPage(safePage - 1)}
                >
                  {t('mcp.pagePrev')}
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={safePage >= pageCount}
                  data-testid="token-page-next"
                  onClick={() => setPage(safePage + 1)}
                >
                  {t('mcp.pageNext')}
                </Button>
              </span>
            </div>
          )}
          </>
        )}
      </div>
    </div>
  )
}
