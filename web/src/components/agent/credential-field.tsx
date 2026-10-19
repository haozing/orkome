import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createCredential, listCredentials } from '@/api/credentials'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { FormField, PasswordField } from '@ploykit/ui'
import { ApiError } from '@ploykit/client'
export function CredentialField({
  id,
  value,
  onChange,
}: {
  /** 透传到内层 select，供 FormField htmlFor 关联（否则 label 不落焦点） */
  id?: string
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
        <Select
          id={id}
          aria-label={id ? undefined : t('cred.aria')}
          value={value}
          onChange={(e) => onChange(e.target.value)}
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
        </Select>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          className="shrink-0"
          onClick={() => setFormOpen((v) => !v)}
        >
          {formOpen ? (
            t('cred.collapse')
          ) : (
            <>
              <Plus className="size-3.5" aria-hidden="true" />
              {t('cred.add')}
            </>
          )}
        </Button>
      </div>
      {formOpen && (
        <div className="mt-2 space-y-2 rounded-md border border-zinc-200 p-2.5 dark:border-zinc-800">
          <FormField label={t('cred.providerAria')} htmlFor="credential-provider">
            <Input
              id="credential-provider"
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
              placeholder={t('cred.providerPlaceholder')}
            />
          </FormField>
          <FormField label={t('cred.secretAria')} htmlFor="credential-secret">
            <PasswordField
              id="credential-secret"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              placeholder={t('cred.secretPlaceholder')}
              autoComplete="off"
            />
          </FormField>
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
