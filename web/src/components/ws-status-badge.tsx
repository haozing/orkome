import { Badge } from '@/components/ui/badge'
import { useWsStatus } from '@/hooks/ws'
import type { WsStatus } from '@ploykit/client'
import { Check } from 'lucide-react'
import { useTranslation } from 'react-i18next'
const STATUS_META: Record<
  WsStatus,
  {
    labelKey: string
    tone: 'neutral' | 'success' | 'warning' | 'danger'
  }
> = {
  idle: { labelKey: 'ws.idle', tone: 'neutral' },
  connecting: { labelKey: 'ws.connecting', tone: 'warning' },
  open: { labelKey: 'ws.open', tone: 'success' },
  reconnecting: { labelKey: 'ws.reconnecting', tone: 'warning' },
  stopped: { labelKey: 'ws.stopped', tone: 'danger' },
}
export function WsStatusBadge() {
  const { t } = useTranslation()
  const status = useWsStatus()
  const meta = STATUS_META[status]
  // 已连接收敛为一个对号（用户裁决：状态常态只占一个角标，不占一行文字）；
  // 其余状态保留文字（连接中/重连/断开需要可读的原因）。屏幕阅读器仍读全文。
  if (status === 'open') {
    return (
      <Badge variant="success" data-ws-status={status}>
        <Check className="size-3" aria-hidden="true" />
        <span className="sr-only">{t(meta.labelKey)}</span>
      </Badge>
    )
  }
  return (
    <Badge variant={meta.tone} data-ws-status={status}>
      <span className={'size-1.5 rounded-full bg-current'} aria-hidden="true" />
      {t(meta.labelKey)}
    </Badge>
  )
}
