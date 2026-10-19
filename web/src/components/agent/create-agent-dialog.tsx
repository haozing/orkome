import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { createAgent, type Agent } from '@/api/orchestration'
import { CredentialField } from '@/components/agent/credential-field'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { FormField } from '@ploykit/ui'
import { ApiError } from '@ploykit/client'
import { useTranslation } from 'react-i18next'
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
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="max-w-md">
        <DialogTitle className="text-sm font-semibold">{t('agentsList.newAgent')}</DialogTitle>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <FormField label={t('agents.nameRequired')} htmlFor="agent-name">
            <Input
              id="agent-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={100}
              autoFocus
              placeholder={t('agents.nameExample')}
            />
          </FormField>
          <FormField label={t('agents.descPromptLabel')} htmlFor="agent-desc">
            <Textarea
              id="agent-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={3}
              placeholder={t('agents.descPromptExample')}
              className="resize-none"
            />
          </FormField>

          <div className="grid grid-cols-2 gap-3">
            <FormField label={t('agents.provider')} htmlFor="agent-provider">
              <Select
                id="agent-provider"
                value={provider}
                onChange={(e) => setProvider(e.target.value)}
              >
                {/* 供应商名是品牌名，不做 i18n */}
                <option value="openai">OpenAI</option>
              </Select>
            </FormField>
            <FormField label={t('agents.modelRequired')} htmlFor="agent-model">
              <Input
                id="agent-model"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder="gpt-4o-mini"
              />
            </FormField>
          </div>
          <FormField label={t('agents.baseUrlLabel')} htmlFor="agent-baseurl">
            <Input
              id="agent-baseurl"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="https://your-gateway.example/v1"
            />
          </FormField>

          <FormField label={t('agents.credentialLabel')} htmlFor="agent-credential">
            <CredentialField id="agent-credential" value={credentialId} onChange={setCredentialId} />
          </FormField>

          {create.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {create.error instanceof ApiError ? create.error.message : t('skills.createFailed')}
            </p>
          )}
          <DialogFooter>
            <DialogClose render={<Button variant="outline" type="button" />}>
              {t('common.cancel')}
            </DialogClose>
            <Button
              type="submit"
              disabled={!name.trim() || !provider.trim() || !model.trim() || create.isPending}
            >
              {create.isPending ? t('skills.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
