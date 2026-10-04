import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Check, Copy } from 'lucide-react'
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
import { cn } from '@/lib/utils'
export const Route = createFileRoute('/_app/mcp')({
  component: McpPage,
})
const MCP_ENDPOINT = `${window.location.origin}/mcp`
const MCP_TOOLS = [
  { name: 'list_work_items', desc: 'mcp.toolListWorkItems' },
  { name: 'get_work_item', desc: 'mcp.toolGetWorkItem' },
  { name: 'create_work_item', desc: 'mcp.toolCreateWorkItem' },
  { name: 'add_comment', desc: 'mcp.toolAddComment' },
  { name: 'my_work', desc: 'mcp.toolMyWork' },
] as const
const CARD_CLASS = 'rounded-lg border border-zinc-200 p-4 dark:border-zinc-800'
const CARD_TITLE_CLASS = 'mb-1 text-sm font-semibold'
const CARD_DESC_CLASS = 'mb-3 text-xs text-zinc-500 dark:text-zinc-400'
function CopyButton({ text }: { text: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={() => {
        void navigator.clipboard.writeText(text).then(
          () => {
            setCopied(true)
            setTimeout(() => setCopied(false), 1500)
          },
          () => {},
        )
      }}
    >
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      {copied ? t('common.copied') : t('common.copy')}
    </Button>
  )
}
function Snippet({ title, file, code }: { title: string; file?: string; code: string }) {
  return (
    <div className={CARD_CLASS}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <p className="text-sm font-medium">
          {title}
          {file && <span className="ml-2 font-mono text-xs text-zinc-400">{file}</span>}
        </p>
        <CopyButton text={code} />
      </div>
      <pre className="overflow-x-auto rounded-md bg-zinc-950 p-3 font-mono text-xs leading-relaxed text-zinc-100">
        {code}
      </pre>
    </div>
  )
}
function tokenStatusKey(t: PatToken): { key: string; tone: 'success' | 'warning' | 'neutral' } {
  if (t.revoked_at) return { key: 'mcp.tokRevoked', tone: 'neutral' }
  if (t.expires_at && new Date(t.expires_at) < new Date())
    return { key: 'mcp.tokExpired', tone: 'warning' }
  return { key: 'mcp.tokActive', tone: 'success' }
}
const TTL_OPTIONS = [
  { value: 0, key: 'mcp.ttlNever' },
  { value: 30 * 86400, key: 'mcp.ttl30' },
  { value: 90 * 86400, key: 'mcp.ttl90' },
  { value: 180 * 86400, key: 'mcp.ttl180' },
] as const
function McpPage() {
  const { t, i18n } = useTranslation()
  const fmtTime = (iso?: string): string => {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })
  }
  const tokenPlaceholder = t('mcp.tokenPlaceholder')
  const queryClient = useQueryClient()
  const tokens = useQuery(tokensQueryOptions)
  const [name, setName] = useState('')
  const [ttl, setTtl] = useState<number>(0)
  const [secret, setSecret] = useState<PatSecret | null>(null)
  const [confirmingId, setConfirmingId] = useState<string | null>(null)
  const invalidateTokens = () =>
    queryClient.invalidateQueries({ queryKey: tokensQueryOptions.queryKey })
  const create = useMutation({
    mutationFn: () => createToken(name.trim(), ttl),
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
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-lg font-semibold">MCP</h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{t('mcp.intro')}</p>
      </div>

      <section className={CARD_CLASS}>
        <div className="flex items-center justify-between gap-2">
          <h2 className={CARD_TITLE_CLASS}>{t('mcp.endpointTitle')}</h2>
          <Badge variant="warning">{t('mcp.comingSoon')}</Badge>
        </div>
        <p className={cn(CARD_DESC_CLASS, 'mb-2')}>{t('mcp.endpointDesc')}</p>
        <div className="flex items-center gap-2">
          <code className="min-w-0 flex-1 overflow-x-auto rounded-md bg-zinc-100 px-3 py-2 font-mono text-xs dark:bg-zinc-900">
            {MCP_ENDPOINT}
          </code>
          <CopyButton text={MCP_ENDPOINT} />
        </div>
      </section>

      <section className="space-y-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold">{t('mcp.connectTitle')}</h2>
          <Badge variant="warning">{t('mcp.availableAfterLaunch')}</Badge>
        </div>
        <Snippet
          title="Codex"
          file="~/.codex/config.toml"
          code={[
            '[mcp_servers.orkome]',
            `url = "${MCP_ENDPOINT}"`,
            'bearer_token_env_var = "ORKOME_TOKEN"',
            '',
            `# ${t('mcp.codexEnvHint')}`,
            `# export ORKOME_TOKEN="${tokenPlaceholder}"`,
          ].join('\n')}
        />
        <Snippet
          title="Claude Code"
          file=".mcp.json"
          code={JSON.stringify(
            {
              mcpServers: {
                orkome: {
                  type: 'http',
                  url: MCP_ENDPOINT,
                  headers: { Authorization: `Bearer ${tokenPlaceholder}` },
                },
              },
            },
            null,
            2,
          )}
        />
        <Snippet
          title={t('mcp.genericClient')}
          code={[
            `POST ${MCP_ENDPOINT}`,
            `Authorization: Bearer ${tokenPlaceholder}`,
            'Content-Type: application/json',
            'Accept: application/json, text/event-stream',
          ].join('\n')}
        />
        <p className={CARD_DESC_CLASS}>{t('mcp.replaceTokenHint')}</p>
      </section>

      <section className={CARD_CLASS}>
        <div className="flex items-center justify-between gap-2">
          <h2 className={CARD_TITLE_CLASS}>{t('mcp.toolsTitle')}</h2>
          <Badge variant="warning">{t('mcp.comingSoon')}</Badge>
        </div>
        <p className={CARD_DESC_CLASS}>{t('mcp.toolsDesc')}</p>
        <ul className="space-y-2">
          {MCP_TOOLS.map((tool) => (
            <li key={tool.name} className="flex flex-col gap-0.5">
              <code className="font-mono text-xs font-medium">{tool.name}</code>
              <span className="text-xs text-zinc-500 dark:text-zinc-400">{t(tool.desc)}</span>
            </li>
          ))}
        </ul>
      </section>

      <section className={CARD_CLASS}>
        <h2 className={CARD_TITLE_CLASS}>{t('mcp.credentialsTitle')}</h2>
        <p className={CARD_DESC_CLASS}>{t('mcp.credentialsDesc')}</p>

        {secret && (
          <div
            role="status"
            className="mb-4 rounded-md border border-emerald-300 bg-emerald-50 p-3 dark:border-emerald-800 dark:bg-emerald-950"
          >
            <p className="text-sm font-medium text-emerald-800 dark:text-emerald-200">
              {t('mcp.createdOnce', { name: secret.name })}
            </p>
            <div className="mt-2 flex items-center gap-2">
              <code className="min-w-0 flex-1 overflow-x-auto rounded-md bg-white px-3 py-2 font-mono text-xs dark:bg-zinc-900">
                {secret.token}
              </code>
              <CopyButton text={secret.token} />
              <Button variant="ghost" size="sm" onClick={() => setSecret(null)}>
                {t('common.ok')}
              </Button>
            </div>
          </div>
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
            className="min-w-0 flex-1"
          />
          <select
            value={ttl}
            onChange={(e) => setTtl(Number(e.target.value))}
            aria-label={t('mcp.ttlLabel')}
            className="h-9 rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
          >
            {TTL_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {t(opt.key)}
              </option>
            ))}
          </select>
          <Button onClick={submitCreate} disabled={!name.trim() || create.isPending}>
            {create.isPending ? t('mcp.creating') : t('mcp.createToken')}
          </Button>
        </div>
        {create.error && (
          <p className="mt-2 text-xs text-red-600 dark:text-red-400">{create.error.message}</p>
        )}

        <div className="mt-4 border-t border-zinc-200 dark:border-zinc-800">
          {tokens.isPending && <p className="pt-3 text-sm text-zinc-500">{t('common.loading')}</p>}
          {tokens.error && (
            <p className="pt-3 text-sm text-red-600 dark:text-red-400">{tokens.error.message}</p>
          )}
          {tokens.data && tokens.data.length === 0 && (
            <p className="pt-3 text-sm text-zinc-500">{t('mcp.noCredentials')}</p>
          )}
          {tokens.data && tokens.data.length > 0 && (
            <div className="divide-y divide-zinc-200 dark:divide-zinc-800">
              {tokens.data.map((tok) => {
                const status = tokenStatusKey(tok)
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
                      {tok.expires_at &&
                        ` · ${t('mcp.expiresAt', { time: fmtTime(tok.expires_at) })}`}
                    </p>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </section>
    </div>
  )
}
