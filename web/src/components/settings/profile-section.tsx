import { useMutation, useQueryClient } from '@tanstack/react-query'
import { FormField } from '@ploykit/ui'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import { meQueryOptions, updateProfile, type Me, type User } from '@/api/identity'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// 资料编辑（design §3.1）：PATCH /auth/me 部分化语义（ploykit 0.7.0）——
// 只提交用户改动过的字段，未触碰的字段 undefined=保留现值（不再有「只改昵称
// 静默清空头像」的覆盖风险）；avatar_url 显式空串=清空。成功后写回 me 缓存。
export function ProfileSection({ user }: { user: User }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // 草稿态 + 来源戳：me 缓存被别处刷新（如邀请接受、保存成功写回）带来新的
  // user 对象时，以新值重置草稿（React 官方「props 变化时调整状态」模式——
  // 渲染期 setState 触发重渲染而非级联 effect）。dirty 集记录用户碰过的字段。
  const [draft, setDraft] = useState({
    source: user,
    displayName: user.display_name,
    avatarUrl: user.avatar_url ?? '',
    dirty: new Set<string>(),
  })
  if (draft.source !== user) {
    setDraft({
      source: user,
      displayName: user.display_name,
      avatarUrl: user.avatar_url ?? '',
      dirty: new Set<string>(),
    })
  }
  const { displayName, avatarUrl, dirty } = draft
  const setDisplayName = (v: string) =>
    setDraft((d) => ({ ...d, displayName: v, dirty: new Set(d.dirty).add('display_name') }))
  const setAvatarUrl = (v: string) =>
    setDraft((d) => ({ ...d, avatarUrl: v, dirty: new Set(d.dirty).add('avatar_url') }))
  const [status, setStatus] = useState<{ kind: 'saved' | 'error'; message?: string } | null>(null)

  const save = useMutation({
    mutationFn: () => {
      const patch: { display_name?: string; avatar_url?: string } = {}
      if (dirty.has('display_name')) patch.display_name = displayName.trim()
      if (dirty.has('avatar_url')) patch.avatar_url = avatarUrl.trim()
      return updateProfile(patch)
    },
    onSuccess: (updated) => {
      queryClient.setQueryData<Me>(meQueryOptions.queryKey, (prev) =>
        prev ? { ...prev, user: updated } : prev,
      )
      setStatus({ kind: 'saved' })
    },
    onError: (err) => {
      setStatus({ kind: 'error', message: err instanceof ApiError ? err.message : String(err) })
    },
  })

  const canSave =
    dirty.size > 0 &&
    displayName.trim().length >= 1 &&
    displayName.trim().length <= 100 &&
    !save.isPending

  return (
    <div className="space-y-4">
      <div className="max-w-md space-y-3">
        <FormField label={t('settings.profile.displayName')} htmlFor="settings-profile-name">
          <Input
            id="settings-profile-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            maxLength={100}
            data-testid="profile-name-input"
          />
        </FormField>
        <FormField
          label={t('settings.profile.avatar')}
          htmlFor="settings-profile-avatar"
          description={t('settings.profile.avatarHint')}
        >
          <Input
            id="settings-profile-avatar"
            value={avatarUrl}
            onChange={(e) => setAvatarUrl(e.target.value)}
            maxLength={2048}
            placeholder="https://…"
            data-testid="profile-avatar-input"
          />
        </FormField>
      </div>
      <div className="flex items-center gap-3">
        <Button onClick={() => save.mutate()} disabled={!canSave} data-testid="profile-save">
          {save.isPending ? t('common.saving') : t('common.save')}
        </Button>
        <p
          aria-live="polite"
          data-testid="profile-status"
          className={
            status?.kind === 'error'
              ? 'text-xs text-red-600 dark:text-red-400'
              : 'text-xs text-zinc-500 dark:text-zinc-400'
          }
        >
          {status?.kind === 'saved' && t('settings.profile.saved')}
          {status?.kind === 'error' && (
            <>
              {t('settings.profile.saveFailed')}
              {status.message && <span className="ml-1 break-all">{status.message}</span>}
            </>
          )}
        </p>
      </div>
    </div>
  )
}
