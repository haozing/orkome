import { createFileRoute, redirect } from '@tanstack/react-router'

// /packs 列表已并入「集成」页（2026-10-15 裁决：pack 本体是归属标签，
// 与 MCP 服务器注册表同页呈现——安装视图 + 接线视图）。此处仅保留重定向，
// 兼容旧入口/书签。/packs/{id} 详情与 /packs/install 向导路径不变。
export const Route = createFileRoute('/_app/packs/')({
  beforeLoad: () => {
    throw redirect({ to: '/tools' })
  },
})
