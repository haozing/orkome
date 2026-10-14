import { api } from '@ploykit/client'

// Webhook 订阅管理端点封装（pack 详情页「事件」区块的动作面）。
// 路由挂在 /api/workspaces/{id}/webhooks/*（workspace 在 URL path，同
// mcp-servers 的做法；apiFetch 的 workspaceIdProvider 只补 X-Workspace-Id 头，
// 不影响 path 寻址）。权限：ping/rotate/pause/redeliver 为 admin，member 调用
// 会 403——页面侧不做权限隐藏，错误提示即可。

/** 事件目录条目（GET /webhooks/events，member 可读）。 */
export interface WebhookEventDef {
  type: string
  description: string
}

/** GET /api/workspaces/{id}/webhooks/events → { items: [...] } */
export async function listWebhookEvents(workspaceId: string): Promise<WebhookEventDef[]> {
  const res = await api.get<{ items: WebhookEventDef[] | null }>(
    `/api/workspaces/${workspaceId}/webhooks/events`,
  )
  return res.items ?? []
}

/** 投递日志一行（GET /webhooks/deliveries）。 */
export interface WebhookDelivery {
  id: string
  subscription_id: string
  event_id: string
  event_type: string
  status: string
  attempts: number
  last_status_code?: number | null
  last_error?: string | null
  delivered_at?: string | null
  created_at: string
}

/** GET /api/workspaces/{id}/webhooks/deliveries?limit=50 → { items: [...] } */
export async function listWebhookDeliveries(workspaceId: string): Promise<WebhookDelivery[]> {
  const res = await api.get<{ items: WebhookDelivery[] | null }>(
    `/api/workspaces/${workspaceId}/webhooks/deliveries?limit=50`,
  )
  return res.items ?? []
}

/** POST /webhooks/subscriptions/{id}/ping → 204（连通测试）。 */
export function pingWebhookSubscription(workspaceId: string, subscriptionId: string) {
  return api.post<unknown>(
    `/api/workspaces/${workspaceId}/webhooks/subscriptions/${subscriptionId}/ping`,
    {},
  )
}

/** POST /webhooks/subscriptions/{id}/rotate-secret → { secret }（明文仅本次返回）。 */
export function rotateWebhookSecret(workspaceId: string, subscriptionId: string) {
  return api.post<{ secret: string }>(
    `/api/workspaces/${workspaceId}/webhooks/subscriptions/${subscriptionId}/rotate-secret`,
    {},
  )
}

/** PATCH /webhooks/subscriptions/{id} body { is_active }（暂停/恢复）。 */
export function setWebhookSubscriptionActive(
  workspaceId: string,
  subscriptionId: string,
  isActive: boolean,
) {
  return api.patch<unknown>(
    `/api/workspaces/${workspaceId}/webhooks/subscriptions/${subscriptionId}`,
    { is_active: isActive },
  )
}

/** POST /webhooks/deliveries/{id}/redeliver（重放一次投递）。 */
export function redeliverWebhookDelivery(workspaceId: string, deliveryId: string) {
  return api.post<unknown>(
    `/api/workspaces/${workspaceId}/webhooks/deliveries/${deliveryId}/redeliver`,
    {},
  )
}
