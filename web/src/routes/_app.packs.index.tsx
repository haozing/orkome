import { createFileRoute } from '@tanstack/react-router'
import { PacksPageView } from '@/components/packs-page'

// 能力包列表页路由（Pack 一等公民化 P1，docs/pack-first-class-design-2026-10-13.md §2.2）。
// 视图主体在 components/packs-page.tsx：router 插件的 autoCodeSplitting 会拆走
// 路由模块的非 Route 导出，页面级组件测试须从组件文件引用（同 task-transcript-drawer）。
// 文件名 .index 而非 _app.packs.tsx：后者会成为 /packs 与 /packs/$packId 的
// 父布局（无 <Outlet/> 会让详情页渲染不出来），与 skills/agents 的拆法一致。
export const Route = createFileRoute('/_app/packs/')({
  component: PacksPageView,
})
