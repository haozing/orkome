import { useTranslation } from 'react-i18next'
interface PagePlaceholderProps {
  title: string
  description: string
}
export function PagePlaceholder({ title, description }: PagePlaceholderProps) {
  const { t } = useTranslation()
  return (
    <div className="mx-auto max-w-md pt-16 text-center">
      <h1 className="mb-2 text-lg font-semibold">{title}</h1>
      <p className="text-sm text-zinc-500 dark:text-zinc-400">{description}</p>
      <p className="mt-6 text-xs text-zinc-400 dark:text-zinc-500">{t('common.skeletonNote')}</p>
    </div>
  )
}
