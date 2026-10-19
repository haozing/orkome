import { cn } from '@/lib/utils'

export interface SegmentedOption<V extends string> {
  value: V
  label: string
}

interface SegmentedControlProps<V extends string> {
  options: readonly SegmentedOption<V>[]
  value: V
  onChange: (value: V) => void
  ariaLabel: string
  className?: string
}

// 筛选分段控件（ui-sweep #2 收编：my-work 视角 pills 与 notifications 视图
// pills 原为两份逐字重复的手写实现）。语义是筛选器而非页签——切换即改变
// 下方列表内容而非切换面板——因此用 role=group + aria-pressed 的按钮组，
// 不用 role=tablist/aria-selected（agents 列表同病已判同例）。
export function SegmentedControl<V extends string>({
  options,
  value,
  onChange,
  ariaLabel,
  className,
}: SegmentedControlProps<V>) {
  return (
    <div role="group" aria-label={ariaLabel} className={cn('flex flex-wrap gap-1', className)}>
      {options.map((opt) => {
        const active = opt.value === value
        return (
          <button
            key={opt.value}
            type="button"
            aria-pressed={active}
            className={cn(
              'cursor-default rounded-md px-3 py-1.5 text-sm outline-none',
              active
                ? 'bg-zinc-100 font-medium text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100'
                : 'text-zinc-500 hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200',
            )}
            onClick={() => onChange(opt.value)}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
