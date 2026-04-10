import { describe, expect, it } from 'vitest'

import type { WorkflowDefinition } from '../api/client'
import { buildWorkflowPayload, promptPreviewForWorkflow, trustSummaryForWorkflow } from './workflowScreenModel'

describe('workflowScreenModel', () => {
  it('builds a normalized workflow payload from form fields', () => {
    const payload = buildWorkflowPayload({
      name: ' Project Handoff ',
      description: ' Deliver a concise handoff ',
      promptTemplate: ' Review {{path}} and summarize ',
      tags: 'files, handoff,  summary ',
      approvalMode: 'step_by_step',
      approvedRootPaths: '/tmp/one\n\n /tmp/two ',
      allowedApps: 'filesystem, browser ',
      allowsSensitiveRead: true,
      allowsLiveWrite: false,
    })

    expect(payload.id).toBe('project-handoff')
    expect(payload.name).toBe('Project Handoff')
    expect(payload.tags).toEqual(['files', 'handoff', 'summary'])
    expect(payload.trustScope.approvedRootPaths).toEqual(['/tmp/one', '/tmp/two'])
    expect(payload.trustScope.allowedApps).toEqual(['filesystem', 'browser'])
    expect(payload.approvalMode).toBe('step_by_step')
    expect(payload.isEnabled).toBe(true)
  })

  it('derives preview and trust summary text for workflow cards', () => {
    const workflow: WorkflowDefinition = {
      id: 'wf-1',
      name: 'Review',
      description: 'Review source changes',
      promptTemplate: 'Review source changes',
      tags: [],
      steps: [],
      trustScope: {
        approvedRootPaths: ['/tmp/atlas-approved'],
        allowedApps: ['filesystem', 'browser'],
        allowsSensitiveRead: false,
        allowsLiveWrite: true,
      },
      approvalMode: 'workflow_boundary',
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
      isEnabled: true,
    }

    expect(promptPreviewForWorkflow(workflow)).toBe('')
    workflow.description = 'Inspect the repo'
    expect(promptPreviewForWorkflow(workflow)).toBe('Review source changes')
    expect(trustSummaryForWorkflow(workflow)).toBe('2 apps · 1 paths · live writes')
  })
})
