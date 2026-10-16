import { createFileRoute, redirect } from '@tanstack/react-router'

// /mcp 已拆分为 /tools（集成，消费方）与 /access（助理接入，提供方）；
// 旧地址301语义由客户端重定向兜底，默认去工具集成页（原页面主体所在）。
export const Route = createFileRoute('/_app/mcp')({
  beforeLoad: () => {
    throw redirect({ to: '/tools', replace: true })
  },
})
