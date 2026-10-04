import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createCredential, listCredentials } from '@/api/credentials'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api'
const SELECT_CLASS =
  'h-9 w-full rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
export function CredentialField({
  value,
  onChange,
}: {
  value: string
  onChange: (credentialId: string) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [formOpen, setFormOpen] = useState(false)
  const [provider, setProvider] = useState('')
  const [secret, setSecret] = useState('')
  const credentials = useQuery({ queryKey: ['credentials'], queryFn: listCredentials })
  const create = useMutation({
    mutationFn: () => createCredential(provider.trim(), secret),
    onSuccess: (c) => {
      void queryClient.invalidateQueries({ queryKey: ['credentials'] })
      onChange(c.id)
      setFormOpen(false)
      setProvider('')
      setSecret('')
    },
  })
  const submit = () => {
    if (!provider.trim() || !secret || create.isPending) return
    create.mutate()
  }
  return (
    <div>
      <div className="flex items-center gap-2">
        <select
          aria-label={t('cred.aria')}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={SELECT_CLASS}
        >
          <option value="">{t('cred.none')}</option>
          {(credentials.data ?? []).map((c) => (
            <option key={c.id} value={c.id}>
              {c.provider} ·{' '}
              {new Date(c.created_at).toLocaleDateString(
                document.documentElement.lang === 'zh' ? 'zh-CN' : 'en-US',
              )}
            </option>
          ))}
        </select>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          className="shrink-0"
          onClick={() => setFormOpen((v) => !v)}
        >
          {formOpen ? t('cred.collapse') : t('cred.add')}
        </Button>
      </div>
      {formOpen && (
        <div className="mt-2 space-y-2 rounded-md border border-zinc-200 p-2.5 dark:border-zinc-800">
          <Input
            aria-label={t('cred.providerAria')}
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
            placeholder={t('cred.providerPlaceholder')}
          />
          <Input
            aria-label={t('cred.secretAria')}
            type="password"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder={t('cred.secretPlaceholder')}
          />
          {create.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {create.error instanceof ApiError ? create.error.message : t('cred.createFailed')}
            </p>
          )}
          <div className="flex justify-end">
            <Button
              type="button"
              size="sm"
              disabled={!provider.trim() || !secret || create.isPending}
              onClick={submit}
            >
              {create.isPending ? t('skills.creating') : t('cred.saveAndUse')}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
