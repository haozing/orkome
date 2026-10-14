import { createFileRoute } from '@tanstack/react-router'
import { PackInstallWizardView } from '@/components/pack-install-wizard'

// 能力包安装向导路由（平台化 P2）。视图主体在 components/pack-install-wizard.tsx：
// router 插件的 autoCodeSplitting 会拆走路由模块的非 Route 导出，页面级
// 组件测试须从组件文件引用（同 packs-page/pack-detail 的拆法）。
export const Route = createFileRoute('/_app/packs/install')({
  component: PackInstallWizardView,
})
