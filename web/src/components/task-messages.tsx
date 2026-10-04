import type { TaskMessage } from '@/api/orchestration'
import { Markdown } from '@/components/markdown'
import { useTranslation } from 'react-i18next'
export function TaskMessageRow({ message }: { message: TaskMessage }) {
  const { t } = useTranslation()
  if (message.kind === 'text') {
    return (
      <li className="list-none">
        <Markdown>{message.content}</Markdown>
      </li>
    )
  }
  const label =
    message.kind === 'tool_use'
      ? t('taskMsg.toolUse')
      : message.kind === 'tool_result'
        ? t('taskMsg.toolResult')
        : message.kind === 'approval'
          ? t('taskMsg.approval')
          : message.kind === 'error'
            ? t('taskMsg.error')
            : message.kind
  const tone =
    message.kind === 'error'
      ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/50 dark:bg-red-950/40 dark:text-red-300'
      : message.kind === 'approval'
        ? 'border-amber-200 bg-amber-50 dark:border-amber-900/50 dark:bg-amber-950/40'
        : 'border-zinc-200 bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900/50'
  return (
    <li className={'rounded-md border px-2.5 py-1.5 ' + tone}>
      <p className="mb-0.5 text-xs font-medium opacity-70">
        {label}
        {message.call_id ? ` · ${message.call_id.slice(0, 8)}…` : ''}
      </p>
      <pre className="max-h-64 overflow-auto font-mono text-xs break-all whitespace-pre-wrap">
        {message.content}
      </pre>
      {message.truncated && <p className="text-xs opacity-60">{t('taskMsg.truncated')}</p>}
    </li>
  )
}
export function TaskMessageList({ messages }: { messages: TaskMessage[] }) {
  return (
    <ol className="space-y-2">
      {messages.map((m) => (
        <TaskMessageRow key={m.id} message={m} />
      ))}
    </ol>
  )
}
