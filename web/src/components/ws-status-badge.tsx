import { Badge } from '@/components/ui/badge'
import { useWsStatus } from '@/hooks/ws'
import type { WsStatus } from '@/lib/ws'
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
  return (
    <Badge variant={meta.tone} data-ws-status={status}>
      <span className={'size-1.5 rounded-full bg-current'} aria-hidden="true" />
      {t(meta.labelKey)}
    </Badge>
  )
}
