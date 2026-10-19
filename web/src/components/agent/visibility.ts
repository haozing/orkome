import type { Badge } from '@/components/ui/badge'
export const VISIBILITY_META: Record<
  string,
  {
    labelKey: string
    tone: 'neutral' | 'warning' | 'outline'
  }
> = {
  public: { labelKey: 'agents.visPublic', tone: 'neutral' },
  public_to: { labelKey: 'agents.visLimited', tone: 'warning' },
  private: { labelKey: 'agents.visPrivate', tone: 'outline' },
}
export function visibilityMeta(visibility: string): {
  labelKey: string
  tone: 'neutral' | 'warning' | 'outline'
} {
  // 未知枚举不能把原始值当 i18n 键直出（旧兜底 `{ labelKey: visibility }` 会把
  // 'whatever' 渲染成徽标文案）——退到中性「受限」，按最小披露处理。
  return VISIBILITY_META[visibility] ?? { labelKey: 'agents.visUnknown', tone: 'neutral' }
}
export type VisibilityTone = NonNullable<Parameters<typeof Badge>[0]['variant']>
