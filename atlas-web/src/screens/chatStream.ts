import type { ChatStreamEvent } from '../api/client'

export function extractStreamError(event: ChatStreamEvent): string {
  const direct = event.error?.trim()
  if (direct) return direct
  const legacy = event.message?.trim()
  if (legacy) return legacy
  return 'An error occurred.'
}

export function buildDocumentTitle(baseTitle: string, counts: {
  pendingGreetings: number
  unreadReplies: number
  pendingApprovals: number
}): string {
  const total = Math.max(0, counts.pendingGreetings) + Math.max(0, counts.unreadReplies) + Math.max(0, counts.pendingApprovals)
  return total > 0 ? `(${total}) ${baseTitle}` : baseTitle
}
