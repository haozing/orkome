import * as React from 'react'
import { cn } from '@/lib/utils'
// 原生 <select> 的 shadcn 皮（仓库尚无 listbox 型 Select 原语；原生下拉在
// 表单密集编辑器里更稳——键盘/滚动/移动端行为免费）。chevron 用 CSS 背景内联
// SVG，appearance:none 去掉系统箭头。
function Select({ className, ...props }: React.ComponentProps<'select'>) {
  return (
    <select
      data-slot="select"
      className={cn(
        'border-input focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:bg-input/30 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40 flex h-8 w-full appearance-none items-center justify-between gap-2 rounded-lg border bg-transparent py-1 pr-8 pl-2.5 text-sm whitespace-nowrap transition-colors outline-none select-none focus-visible:ring-3 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:ring-3 data-[size=default]:h-8',
        'bg-[url("data:image/svg+xml;charset=utf-8,%3Csvg%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%20width%3D%2216%22%20height%3D%2216%22%20viewBox%3D%220%200%2024%2024%22%20fill%3D%22none%22%20stroke%3D%22%2371717a%22%20stroke-width%3D%222%22%20stroke-linecap%3D%22round%22%20stroke-linejoin%3D%22round%22%3E%3Cpath%20d%3D%22m6%209%206%206%206-6%22%2F%3E%3C%2Fsvg%3E")] bg-[length:1rem] bg-[right_0.5rem_center] bg-no-repeat',
        className,
      )}
      {...props}
    />
  )
}
export { Select }
