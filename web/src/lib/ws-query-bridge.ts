import type { QueryClient, QueryKey } from '@tanstack/react-query'
import type { WsClient, WsFrame } from '@/lib/ws'
export interface WsInvalidationRule {
  match: string | RegExp
  invalidate: readonly QueryKey[]
}
function ruleMatches(rule: WsInvalidationRule, type: string): boolean {
  return typeof rule.match === 'string' ? rule.match === type : rule.match.test(type)
}
export function bridgeWsToQuery(
  ws: WsClient,
  queryClient: QueryClient,
  rules: readonly WsInvalidationRule[],
): () => void {
  return ws.on('*', (frame: WsFrame) => {
    for (const rule of rules) {
      if (!ruleMatches(rule, frame.type)) continue
      for (const key of rule.invalidate) {
        queryClient.invalidateQueries({ queryKey: key })
      }
    }
  })
}
