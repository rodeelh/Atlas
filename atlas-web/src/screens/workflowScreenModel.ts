import type { WorkflowDefinition } from '../api/client'

export interface WorkflowFormState {
  name: string
  description: string
  promptTemplate: string
  tags: string
  approvalMode: WorkflowDefinition['approvalMode']
  approvedRootPaths: string
  allowedApps: string
  allowsSensitiveRead: boolean
  allowsLiveWrite: boolean
}

export function buildWorkflowPayload(form: WorkflowFormState, workflow?: WorkflowDefinition): WorkflowDefinition {
  const now = new Date().toISOString()
  const id = workflow?.id ?? form.name.trim().toLowerCase().replace(/\s+/g, '-').replace(/[^a-z0-9-]/g, '')
  return {
    id,
    name: form.name.trim(),
    description: form.description.trim(),
    promptTemplate: form.promptTemplate.trim(),
    tags: form.tags.split(',').map(value => value.trim()).filter(Boolean),
    steps: workflow?.steps ?? [],
    trustScope: {
      approvedRootPaths: form.approvedRootPaths.split('\n').map(value => value.trim()).filter(Boolean),
      allowedApps: form.allowedApps.split(',').map(value => value.trim()).filter(Boolean),
      allowsSensitiveRead: form.allowsSensitiveRead,
      allowsLiveWrite: form.allowsLiveWrite,
    },
    approvalMode: form.approvalMode,
    createdAt: workflow?.createdAt ?? now,
    updatedAt: now,
    sourceConversationID: workflow?.sourceConversationID,
    isEnabled: workflow?.isEnabled ?? true,
  }
}

export function promptPreviewForWorkflow(workflow: WorkflowDefinition): string {
  const trimmed = workflow.promptTemplate.trim()
  if (!trimmed) return ''
  if (workflow.description && workflow.description.trim() === trimmed) return ''
  return trimmed
}

export function trustSummaryForWorkflow(workflow: WorkflowDefinition): string {
  const parts: string[] = []
  if (workflow.trustScope.allowedApps.length > 0) parts.push(`${workflow.trustScope.allowedApps.length} apps`)
  if (workflow.trustScope.approvedRootPaths.length > 0) parts.push(`${workflow.trustScope.approvedRootPaths.length} paths`)
  if (workflow.trustScope.allowsSensitiveRead) parts.push('sensitive reads')
  if (workflow.trustScope.allowsLiveWrite) parts.push('live writes')
  return parts.length ? parts.join(' · ') : 'Default trust scope'
}
