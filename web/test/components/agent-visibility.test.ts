import { describe, expect, it } from 'vitest'
import { visibilityMeta } from '@/components/agent/visibility'

// ui-sweep 枚举兜底修复的钉子：visibilityMeta 的旧兜底 `{ labelKey: visibility }`
// 会把未知枚举原始值当 i18n 键直出（徽标显示 'future_kind' 这类裸枚举）。
// 修复后一律退到中性 agents.visUnknown。
describe('visibilityMeta', () => {
  it('maps known enums to their i18n keys and tones', () => {
    expect(visibilityMeta('public')).toEqual({
      labelKey: 'agents.visPublic',
      tone: 'neutral',
    })
    expect(visibilityMeta('public_to')).toEqual({
      labelKey: 'agents.visLimited',
      tone: 'warning',
    })
    expect(visibilityMeta('private')).toEqual({
      labelKey: 'agents.visPrivate',
      tone: 'outline',
    })
  })

  it('falls back to the neutral visUnknown key for unknown enums (never the raw value)', () => {
    const meta = visibilityMeta('future_kind')
    expect(meta).toEqual({ labelKey: 'agents.visUnknown', tone: 'neutral' })
    expect(meta.labelKey).not.toBe('future_kind')
  })
})
