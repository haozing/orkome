import { QueryClient } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import { createRootRouteWithContext, Outlet } from '@tanstack/react-router'
import '@/styles/global.css'
export interface RouterContext {
  queryClient: QueryClient
}
export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
})
function RootLayout() {
  return (
    <>
      <Outlet />
      {import.meta.env.DEV && (
        <ReactQueryDevtools buttonPosition="bottom-right" initialIsOpen={false} />
      )}
    </>
  )
}
