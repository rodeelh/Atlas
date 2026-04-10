import type { ComponentChildren } from 'preact'

interface EmptyStateProps {
  icon: ComponentChildren
  title: string
  body?: string
  action?: ComponentChildren
  class?: string
}

export function EmptyState({ icon, title, body, action, class: className }: EmptyStateProps) {
  return (
    <div class={`card empty-state${className ? ' ' + className : ''}`}>
      <div class="empty-icon">{icon}</div>
      <h3>{title}</h3>
      {body && <p>{body}</p>}
      {action}
    </div>
  )
}
