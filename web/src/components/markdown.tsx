import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { cn } from '@/lib/utils'
import { useTranslation } from 'react-i18next'
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
