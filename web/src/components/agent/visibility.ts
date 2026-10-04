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
  return VISIBILITY_META[visibility] ?? { labelKey: visibility, tone: 'neutral' }
}
export type VisibilityTone = NonNullable<Parameters<typeof Badge>[0]['variant']>
