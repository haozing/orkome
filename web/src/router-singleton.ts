import { createAppRouter } from '@/router'
import { createQueryClient } from '@/lib/query'

// 应用级 router 单例：命令式导航（登出流程等非组件上下文）从这里取。
// main.tsx 与本模块共享同一实例（router 实例全局只建一次）。
export const router = createAppRouter(createQueryClient())
