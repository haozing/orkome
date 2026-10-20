import { Outlet, createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/settings')({
  component: () => <Outlet />,
})

// 设置域布局壳（纯路径前缀，无 UI）：/settings 与 /settings/workspace 两个
// 叶子页由此挂到 _app 分支下（同 _app.work-items 布局 + .index 叶子范式）。
// 原锚点长页已拆为：个人设置（.index：账户+令牌+服务端）与工作区设置
// （.workspace：切换/概览/成员/审计）。
