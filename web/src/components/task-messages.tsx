import { useMemo } from 'react'
import type { TaskMessage } from '@/api/orchestration'
import { Markdown } from '@/components/markdown'
import { ToolResultRenderer } from '@/components/task-tool-renderers'
import { useTranslation } from 'react-i18next'

// TaskMessage 无工具名字段：工具名只出现在配对 tool_use 消息 content 的首 token
// （后端 emit `name + " " + args`）。tool_result 靠 call_id 回溯配对；这里的
// map 由 TaskMessageList 一次性构建后按 call_id 下发给各 Row。
function toolNameByCallId(messages: TaskMessage[]): Map<string, string> {
  const map = new Map<string, string>()
  for (const m of messages) {
    if (m.kind !== 'tool_use' || !m.call_id) continue
    const name = m.content.trim().split(/\s+/, 1)[0]
    if (name) map.set(m.call_id, name)
  }
  return map
}

export function TaskMessageRow({
  message,
  toolName,
}: {
  message: TaskMessage
  /** 配对 tool_use 的工具名（含 pack 前缀）；仅深链按钮用，形状卡不依赖它 */
  toolName?: string
}) {
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
      <ToolResultRenderer message={message} toolName={toolName} />
    </li>
  )
}
export function TaskMessageList({ messages }: { messages: TaskMessage[] }) {
  const toolNames = useMemo(() => toolNameByCallId(messages), [messages])
  return (
    <ol className="space-y-2">
      {messages.map((m) => (
        <TaskMessageRow
          key={m.id}
          message={m}
          toolName={m.call_id ? toolNames.get(m.call_id) : undefined}
        />
      ))}
    </ol>
  )
}
