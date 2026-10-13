import { createFileRoute } from '@tanstack/react-router'
import { PackDetailView } from '@/components/pack-detail'

// Pack 详情页路由（五区块 §2.3）。视图主体在 components/pack-detail.tsx：
// router 插件的 autoCodeSplitting 会拆走路由模块的非 Route 导出，页面级
// 组件测试须从组件文件引用（同 task-transcript-drawer）。
export const Route = createFileRoute('/_app/packs/$packId')({
  component: PackDetailRouteComponent,
})
function PackDetailRouteComponent() {
  const { packId } = Route.useParams()
  return <PackDetailView packId={packId} />
}
