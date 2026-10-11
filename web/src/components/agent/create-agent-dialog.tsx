import { Dialog } from '@base-ui/react/dialog'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { createAgent, type Agent } from '@/api/orchestration'
import { CredentialField } from '@/components/agent/credential-field'
import { Input } from '@/components/ui/input'
import { ApiError } from '@ploykit/client'
import { useTranslation } from 'react-i18next'
const LABEL_CLASS = 'mb-1 block text-xs text-zinc-500'
export function CreateAgentDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: (agent: Agent) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [provider, setProvider] = useState('openai')
  const [model, setModel] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [credentialId, setCredentialId] = useState('')
  const create = useMutation({
    mutationFn: () =>
      createAgent({
        name: name.trim(),
        description: description.trim(),
        runtime_config: {
          provider: provider.trim(),
          model: model.trim(),
          ...(baseUrl.trim() ? { base_url: baseUrl.trim() } : {}),
          ...(credentialId ? { credential_id: credentialId } : {}),
        },
      }),
    onSuccess: (a) => {
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      reset()
      onOpenChange(false)
      onCreated?.(a)
    },
  })
  const reset = () => {
    setName('')
    setDescription('')
    setModel('')
    setBaseUrl('')
    setCredentialId('')
    create.reset()
  }
  const submit = () => {
    if (!name.trim() || !provider.trim() || !model.trim() || create.isPending) return
    create.mutate()
  }
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-4 text-sm font-semibold">
            {t('agentsList.newAgent')}
          </Dialog.Title>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              submit()
            }}
          >
            <div>
              <label htmlFor="agent-name" className={LABEL_CLASS}>
                {t('agents.nameRequired')}
              </label>
              <Input
                id="agent-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={100}
                autoFocus
                placeholder={t('agents.nameExample')}
              />
            </div>
            <div>
              <label htmlFor="agent-desc" className={LABEL_CLASS}>
                {t('agents.descPromptLabel')}
              </label>
              <textarea
                id="agent-desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                rows={3}
                placeholder={t('agents.descPromptExample')}
                className="w-full resize-none rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
              />
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div>
                <label htmlFor="agent-provider" className={LABEL_CLASS}>
                  {t('agents.provider')}
                </label>
                <select
                  id="agent-provider"
                  value={provider}
                  onChange={(e) => setProvider(e.target.value)}
                  className="h-9 w-full rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
                >
                  <option value="openai">openai ({t('agents.compatible')})</option>
                </select>
              </div>
              <div>
                <label htmlFor="agent-model" className={LABEL_CLASS}>
                  {t('agents.modelRequired')}
                </label>
                <Input
                  id="agent-model"
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  placeholder="gpt-4o-mini"
                />
              </div>
            </div>
            <div>
              <label htmlFor="agent-baseurl" className={LABEL_CLASS}>
                {t('agents.baseUrlLabel')}
              </label>
              <Input
                id="agent-baseurl"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder="https://your-gateway.example/v1"
              />
            </div>

            <div>
              <span className={LABEL_CLASS}>{t('agents.credentialLabel')}</span>
              <CredentialField value={credentialId} onChange={setCredentialId} />
            </div>

            {create.error && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {create.error instanceof ApiError ? create.error.message : t('skills.createFailed')}
              </p>
            )}
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
                {t('common.cancel')}
              </Dialog.Close>
              <button
                type="submit"
                disabled={!name.trim() || !provider.trim() || !model.trim() || create.isPending}
                className="rounded-md bg-zinc-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900"
              >
                {create.isPending ? t('skills.creating') : t('common.create')}
              </button>
            </div>
          </form>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
