import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { cn } from '@/lib/utils'
import { useTranslation } from 'react-i18next'

// looksMarkdown 启发式：body 是否按 Markdown 渲染。通知/产出等自由文本
// 来源混杂（纯文本回显 vs agent 产出的 md 报告），无法靠类型区分，按
// 结构标记探测：标题/列表/引用/代码栅栏/表格/链接/行内强调。误判代价
// 低（纯文本里偶现 * 或 # 也只是行内强调），漏判代价高（md 报告糊成一团）。
export function looksMarkdown(s: string): boolean {
  if (!s) return false
  return (
    /^#{1,6}\s/m.test(s) || // atx 标题
    /^\s{0,3}[-*+]\s/m.test(s) || // 无序列表
    /^\s{0,3}\d+\.\s/m.test(s) || // 有序列表
    /^>\s/m.test(s) || // 引用
    /```|~~~/m.test(s) || // 代码栅栏
    /^\|.*\|/m.test(s) || // 表格行
    /\[[^\]]+\]\([^)]+\)/.test(s) || // 链接
    /\*\*[^*\s][^*]*\*\*/.test(s) || // 粗体
    /`[^`]+`/.test(s) // 行内代码
  )
}

export function Markdown({ children, className }: { children: string; className?: string }) {
  const { t } = useTranslation()
  return (
    <div className={cn('max-w-none space-y-2 text-sm leading-relaxed', className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: (props) => <a {...props} target="_blank" rel="noreferrer" />,
          img: () => <span className="text-xs text-zinc-400">{t('md.image')}</span>,
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  )
}
