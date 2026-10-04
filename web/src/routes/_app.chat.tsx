import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { PagePlaceholder } from '@/components/page-placeholder'
export const Route = createFileRoute('/_app/chat')({
  component: ChatPage,
})
function ChatPage() {
  const { t } = useTranslation()
  return <PagePlaceholder title={t('chat.title')} description={t('chat.desc')} />
}
