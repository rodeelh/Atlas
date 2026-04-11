import { useEffect, useState } from 'preact/hooks'
import { JSX } from 'preact'
import { api, CapabilityRecord, WorkflowDefinition, WorkflowRun, WorkflowSummary } from '../api/client'
import { PageHeader } from '../components/PageHeader'
import { Portal } from '../components/Portal'
import { buildWorkflowPayload, promptPreviewForWorkflow, trustSummaryForWorkflow } from './workflowScreenModel'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EmptyState } from '../components/EmptyState'
import { PageSpinner } from '../components/PageSpinner'

const MoreIcon = () => (
  <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor" aria-hidden="true">
    <circle cx="3" cy="7" r="1.1" />
    <circle cx="7" cy="7" r="1.1" />
    <circle cx="11" cy="7" r="1.1" />
  </svg>
)

const PlayIcon = () => (
  <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
    <polygon points="3,1.5 10.5,6 3,10.5" fill="currentColor" stroke="none" />
  </svg>
)

const FlowIcon = () => (
  <svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <circle cx="4" cy="4" r="2" />
    <circle cx="14" cy="9" r="2" />
    <circle cx="4" cy="14" r="2" />
    <path d="M6 4h4l2 3M6 14h4l2 -3" />
  </svg>
)

function CompactActionMenu({ children }: { children: JSX.Element | JSX.Element[] }) {
  return (
    <details class="automation-more-actions">
      <summary class="btn btn-sm btn-icon automation-action-btn automation-action-icon" title="More actions">
        <MoreIcon />
      </summary>
      <div class="automation-more-actions-panel">
        {children}
      </div>
    </details>
  )
}

function formatDate(value?: string) {
  if (!value) return '—'
  try { return new Date(value).toLocaleString() } catch { return value }
}

function statusBadge(status: string) {
  switch (status) {
    case 'healthy':
    case 'completed': return <span class="badge badge-green">{status}</span>
    case 'running': return <span class="badge badge-yellow">{status}</span>
    case 'failed':
    case 'denied': return <span class="badge badge-red">{status}</span>
    case 'waiting_for_approval': return <span class="badge badge-yellow">needs approval</span>
    case 'never_run': return <span class="badge badge-gray">never run</span>
    case 'disabled': return <span class="badge badge-gray">disabled</span>
    default: return <span class="badge badge-gray">{status}</span>
  }
}

function promptPreview(workflow: WorkflowDefinition) {
  return promptPreviewForWorkflow(workflow)
}

function trustSummary(workflow: WorkflowDefinition) {
  return trustSummaryForWorkflow(workflow)
}

interface WorkflowModalProps {
  workflow?: WorkflowDefinition
  onSave: (workflow: WorkflowDefinition) => Promise<void>
  onClose: () => void
}

function WorkflowModal({ workflow, onSave, onClose }: WorkflowModalProps) {
  const [name, setName] = useState(workflow?.name ?? '')
  const [description, setDescription] = useState(workflow?.description ?? '')
  const [promptTemplate, setPromptTemplate] = useState(workflow?.promptTemplate ?? '')
  const [tags, setTags] = useState((workflow?.tags ?? []).join(', '))
  const [approvalMode, setApprovalMode] = useState(workflow?.approvalMode ?? 'workflow_boundary')
  const [approvedRootPaths, setApprovedRootPaths] = useState((workflow?.trustScope.approvedRootPaths ?? []).join('\n'))
  const [allowedApps, setAllowedApps] = useState((workflow?.trustScope.allowedApps ?? []).join(', '))
  const [allowsSensitiveRead, setAllowsSensitiveRead] = useState(workflow?.trustScope.allowsSensitiveRead ?? false)
  const [allowsLiveWrite, setAllowsLiveWrite] = useState(workflow?.trustScope.allowsLiveWrite ?? false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSave() {
    if (!name.trim() || !promptTemplate.trim()) {
      setError('Name and prompt template are required.')
      return
    }

    setSaving(true)
    setError(null)
    try {
      await onSave(buildWorkflowPayload({
        name,
        description,
        promptTemplate,
        tags,
        approvalMode,
        approvedRootPaths,
        allowedApps,
        allowsSensitiveRead,
        allowsLiveWrite,
      }, workflow))
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save workflow.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Portal>
    <div class="modal-overlay" onClick={(e) => { if ((e.target as HTMLElement).classList.contains('modal-overlay')) onClose() }}>
      <div class="modal automation-modal" style={{ maxWidth: 820, width: '94vw' }}>

        <div class="modal-header">
          <div class="automation-modal-title-wrap">
            <div class="surface-eyebrow">Workflow</div>
            <h3 class="automation-modal-title">{workflow ? workflow.name : 'Create workflow'}</h3>
          </div>
          <button class="btn btn-ghost btn-sm" onClick={onClose}>✕</button>
        </div>
        <div class="modal-body automation-modal-body" style={{ maxHeight: 'calc(85vh - 130px)', overflowY: 'auto' }}>
          {error && <p class="error-banner">{error}</p>}

          <div class="workflow-form-grid">
            <div class="automation-field-group">
              <label class="field-label">Name</label>
              <input class="field-input" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} placeholder="Project handoff" />
            </div>
            <div class="automation-field-group">
              <label class="field-label">Description</label>
              <input class="field-input" value={description} onInput={(e) => setDescription((e.target as HTMLInputElement).value)} placeholder="What this workflow does" />
            </div>
          </div>

          <div class="automation-field-group">
            <label class="field-label">Prompt Template</label>
            <span class="workflow-field-hint">Use {'{{variable}}'} for dynamic values</span>
            <textarea
              class="field-input workflow-textarea"
              value={promptTemplate}
              onInput={(e) => setPromptTemplate((e.target as HTMLTextAreaElement).value)}
              placeholder="Describe the work Atlas should do…"
            />
          </div>

          {/* Tags + Approval Mode — flat row-sharing grid */}
          <div class="workflow-aligned-grid">
            <label class="field-label">Tags</label>
            <label class="field-label">Approval Mode</label>
            <span class="workflow-field-hint">Comma-separated</span>
            <span class="workflow-field-hint">When Atlas pauses for confirmation</span>
            <input class="field-input" value={tags} onInput={(e) => setTags((e.target as HTMLInputElement).value)} placeholder="files, apps, handoff" />
            <select class="field-input" value={approvalMode} onChange={(e) => setApprovalMode((e.target as HTMLSelectElement).value)}>
              <option value="workflow_boundary">Workflow boundary</option>
              <option value="step_by_step">Step by step</option>
            </select>
          </div>

          {/* Trust Scope — flat 4-row grid */}
          <div class="workflow-section">
            <div class="workflow-section-label">Trust Scope</div>
            <div class="workflow-trust-grid">
              <label class="field-label">Approved Root Paths</label>
              <label class="field-label">Allowed Apps</label>
              <span class="workflow-field-hint">One path per line</span>
              <span class="workflow-field-hint">Comma-separated app names</span>
              <textarea
                class="field-input workflow-textarea"
                value={approvedRootPaths}
                onInput={(e) => setApprovedRootPaths((e.target as HTMLTextAreaElement).value)}
                placeholder="/Users/you/Projects&#10;/Users/you/Documents"
              />
              <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', alignSelf: 'start' }}>
                <input
                  class="field-input"
                  value={allowedApps}
                  onInput={(e) => setAllowedApps((e.target as HTMLInputElement).value)}
                  placeholder="Finder, Safari, Calendar"
                />
                <div class="workflow-checkboxes">
                  <label class="workflow-checkbox-label">
                    <label class="toggle">
                      <input type="checkbox" checked={allowsSensitiveRead} onChange={(e) => setAllowsSensitiveRead((e.target as HTMLInputElement).checked)} />
                      <span class="toggle-track" />
                    </label>
                    Allow sensitive reads
                  </label>
                  <label class="workflow-checkbox-label">
                    <label class="toggle">
                      <input type="checkbox" checked={allowsLiveWrite} onChange={(e) => setAllowsLiveWrite((e.target as HTMLInputElement).checked)} />
                      <span class="toggle-track" />
                    </label>
                    Allow live writes
                  </label>
                </div>
              </div>
            </div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-ghost btn-sm" onClick={onClose} disabled={saving}>Cancel</button>
          <button class="btn btn-primary btn-sm" onClick={handleSave} disabled={saving}>
            {saving ? 'Saving…' : workflow ? 'Save Changes' : 'Create Workflow'}
          </button>
        </div>
      </div>
    </div>
    </Portal>
  )
}

function WorkflowRunsPanel({ workflow, onClose }: { workflow: WorkflowDefinition, onClose: () => void }) {
  const [runs, setRuns] = useState<WorkflowRun[]>([])

  useEffect(() => {
    api.workflowRuns(workflow.id).then(setRuns).catch(() => setRuns([]))
  }, [workflow.id])

  async function handleApprove(runID: string) {
    const updated = await api.approveWorkflowRun(runID)
    setRuns(current => current.map(run => run.id === updated.id ? updated : run))
  }

  async function handleDeny(runID: string) {
    const updated = await api.denyWorkflowRun(runID)
    setRuns(current => current.map(run => run.id === updated.id ? updated : run))
  }

  return (
    <Portal>
    <div class="modal-overlay" onClick={(e) => { if ((e.target as HTMLElement).classList.contains('modal-overlay')) onClose() }}>
      <div class="modal automation-modal" style={{ maxWidth: 760, width: '92vw' }}>

        <div class="modal-header">
          <div class="automation-modal-title-wrap">
            <div class="surface-eyebrow">Workflow Runs</div>
            <h3 class="automation-modal-title">{workflow.name}</h3>
          </div>
          <button class="btn btn-ghost btn-sm" onClick={onClose}>✕</button>
        </div>
        <div class="modal-body" style={{ maxHeight: 500, overflowY: 'auto' }}>
          {runs.length === 0 && <p class="empty-state">No workflow runs yet.</p>}
          {runs.map(run => (
            <div key={run.id} class="surface-card-soft" style={{ padding: '14px', marginBottom: '12px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: '10px', alignItems: 'center' }}>
                <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                  {statusBadge(run.status)}
                  <span class="surface-meta">{formatDate(run.startedAt)}</span>
                </div>
                {run.status === 'waiting_for_approval' && (
                  <div style={{ display: 'flex', gap: '8px' }}>
                    <button class="btn btn-primary btn-xs" onClick={() => handleApprove(run.id)}>Approve</button>
                    <button class="btn btn-ghost btn-xs" onClick={() => handleDeny(run.id)}>Deny</button>
                  </div>
                )}
              </div>
              {run.approval?.reason && <p class="automation-prompt" style={{ marginTop: '8px' }}>{run.approval.reason}</p>}
              {run.assistantSummary && <pre class="run-output">{run.assistantSummary}</pre>}
              {run.errorMessage && <p class="error-banner" style={{ marginTop: '10px' }}>{run.errorMessage}</p>}
              {run.stepRuns.length > 0 && (
                <div style={{ marginTop: '10px', display: 'flex', flexDirection: 'column', gap: '8px' }}>
                  {run.stepRuns.map(step => (
                    <div key={step.id} class="automation-run-card">
                      <div class="run-row-header">
                        <strong>{step.title}</strong>
                        {statusBadge(step.status)}
                      </div>
                      {(step.output || step.errorMessage) && <pre class="run-output">{step.output ?? step.errorMessage}</pre>}
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
    </Portal>
  )
}

export function Workflows() {
  const [workflows, setWorkflows] = useState<WorkflowDefinition[]>([])
  const [summaries, setSummaries] = useState<Record<string, WorkflowSummary>>({})
  const [capabilities, setCapabilities] = useState<Record<string, CapabilityRecord>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<WorkflowDefinition | 'new' | null>(null)
  const [runsTarget, setRunsTarget] = useState<WorkflowDefinition | null>(null)
  const [runningID, setRunningID]     = useState<string | null>(null)
  const [togglingID, setTogglingID]   = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<WorkflowDefinition | null>(null)

  async function load() {
    setLoading(true)
    setError(null)
    try {
      const [workflowData, summaryData, capabilityData] = await Promise.all([
        api.workflows(),
        api.workflowSummaries().catch(() => [] as WorkflowSummary[]),
        api.capabilities().catch(() => [] as CapabilityRecord[]),
      ])
      setWorkflows(workflowData)
      setSummaries(Object.fromEntries(summaryData.map(summary => [summary.id, summary])))
      setCapabilities(Object.fromEntries(capabilityData.map(capability => [capability.id, capability])))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load workflows.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  async function handleSave(workflow: WorkflowDefinition) {
    if (editing === 'new') {
      const created = await api.createWorkflow(workflow)
      setWorkflows(current => [created, ...current])
    } else {
      const updated = await api.updateWorkflow(workflow)
      setWorkflows(current => current.map(item => item.id === updated.id ? updated : item))
    }
  }

  function handleDelete(workflow: WorkflowDefinition) {
    setPendingDelete(workflow)
  }

  async function confirmDelete() {
    if (!pendingDelete) return
    const workflow = pendingDelete
    setPendingDelete(null)
    await api.deleteWorkflow(workflow.id)
    setWorkflows(current => current.filter(item => item.id !== workflow.id))
  }

  async function handleToggle(workflow: WorkflowDefinition) {
    setTogglingID(workflow.id)
    try {
      const updated = await api.updateWorkflow({ ...workflow, isEnabled: !workflow.isEnabled, updatedAt: new Date().toISOString() })
      setWorkflows(current => current.map(item => item.id === updated.id ? updated : item))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to toggle workflow.')
    } finally {
      setTogglingID(null)
    }
  }

  async function handleRun(workflow: WorkflowDefinition) {
    setRunningID(workflow.id)
    try {
      await api.runWorkflow(workflow.id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to run workflow.')
    } finally {
      setRunningID(null)
    }
  }

  return (
    <div class="screen">
      <PageHeader
        title="Workflows"
        subtitle="Reusable processes Atlas can run directly or through automations."
        actions={
          <>
            <button class="btn btn-primary btn-sm" onClick={() => setEditing('new')}>+ New</button>
          </>
        }
      />

      {error && <p class="error-banner">{error}</p>}
      {loading && <PageSpinner />}
      {!loading && workflows.length === 0 && (
        <EmptyState
          icon={<svg viewBox="0 0 36 36" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="18" cy="18" r="13" /><path d="M13 18h10M18 13l5 5-5 5" /></svg>}
          title="No workflows saved yet"
          body="Click + New to build a reusable operator flow, then attach it to an automation."
        />
      )}

      {!loading && workflows.length > 0 && (
        <div class="automation-list">
          {workflows.map(workflow => {
            const summary = summaries[workflow.id]
            const capability = capabilities[workflow.id]
            const preview = promptPreview(workflow)
            return (
            <div key={workflow.id} class={`card automation-card automation-console-card workflow-console-card${workflow.isEnabled ? '' : ' disabled'}`}>
              <div class="automation-card-header">
                <div class="automation-identity">
                  <span class="automation-emoji"><FlowIcon /></span>
                </div>
                <div class="automation-meta">
                  <div class="automation-title-row">
                    <span class="automation-name">{workflow.name}</span>
                    {statusBadge(summary?.health ?? (workflow.isEnabled ? 'never_run' : 'disabled'))}
                  </div>
                  <span class="automation-schedule">{workflow.description || 'Reusable workflow'}</span>
                </div>
                <div class="automation-actions">
                  <button
                    class={`btn btn-sm automation-action-btn automation-toggle-btn${workflow.isEnabled ? ' enabled' : ''}`}
                    onClick={() => handleToggle(workflow)}
                    disabled={togglingID === workflow.id}
                    title={workflow.isEnabled ? 'Disable' : 'Enable'}
                  >
                    {togglingID === workflow.id ? '…' : (workflow.isEnabled ? 'On' : 'Off')}
                  </button>
                  <button
                    class="btn btn-sm btn-icon automation-action-btn automation-action-icon"
                    onClick={() => handleRun(workflow)}
                    disabled={runningID === workflow.id}
                    title="Run now"
                  >
                    {runningID === workflow.id ? '…' : <PlayIcon />}
                  </button>
                  <CompactActionMenu>
                    <button class="btn btn-sm automation-action-btn" onClick={() => setRunsTarget(workflow)}>Runs</button>
                    <button class="btn btn-sm automation-action-btn" onClick={() => setEditing(workflow)}>Edit</button>
                    <button class="btn btn-sm automation-action-btn automation-action-danger" onClick={() => handleDelete(workflow)}>Delete</button>
                  </CompactActionMenu>
                </div>
              </div>

              {preview && (
                <div class="automation-prompt-section">
                  <span class="automation-console-label">Prompt</span>
                  <p class="automation-prompt">{preview}</p>
                </div>
              )}

              <div class="automation-console-grid workflow-console-grid">
                <div class="automation-console-cell">
                  <span class="automation-console-label">Process</span>
                  <strong>{workflow.steps.length || summary?.stepCount || 1} step{(workflow.steps.length || summary?.stepCount || 1) !== 1 ? 's' : ''}</strong>
                  <span>{workflow.steps.length > 0 ? workflow.steps.map(step => step.title).slice(0, 2).join(' · ') : 'Prompt template'}</span>
                </div>
                <div class="automation-console-cell">
                  <span class="automation-console-label">Trust Scope</span>
                  <strong>{workflow.approvalMode === 'step_by_step' ? 'Step by step' : 'Workflow boundary'}</strong>
                  <span>{trustSummary(workflow)}</span>
                </div>
                <div class="automation-console-cell">
                  <span class="automation-console-label">Artifacts</span>
                  <strong>{capability?.artifactTypes?.length ? capability.artifactTypes.join(', ') : 'workflow.run_result'}</strong>
                  <span>{capability?.requiredRoots?.length ? `Needs ${capability.requiredRoots.join(', ')}` : 'No extra file prerequisites declared'}</span>
                </div>
                <div class="automation-console-cell">
                  <span class="automation-console-label">Last Run</span>
                  <strong>{formatDate(summary?.lastRunAt)}</strong>
                  <span>{summary?.lastRunStatus ? statusBadge(summary.lastRunStatus) : 'No runs yet'}</span>
                </div>
              </div>
              {workflow.tags.length > 0 && (
                <div class="workflow-tag-strip">
                  {workflow.tags.map(tag => <span key={tag} class="badge badge-gray">{tag}</span>)}
                </div>
              )}
              {summary?.lastRunError && (
                <p class="automation-prompt automation-prompt-error">
                  {summary.lastRunError}
                </p>
              )}
            </div>
          )})}
        </div>
      )}

      {editing !== null && (
        <WorkflowModal
          workflow={editing === 'new' ? undefined : editing}
          onSave={handleSave}
          onClose={() => setEditing(null)}
        />
      )}

      {runsTarget && <WorkflowRunsPanel workflow={runsTarget} onClose={() => setRunsTarget(null)} />}
      {pendingDelete && (
        <ConfirmDialog
          title={`Delete "${pendingDelete.name}"?`}
          body="This workflow will be permanently removed."
          confirmLabel="Delete"
          danger
          onConfirm={confirmDelete}
          onCancel={() => setPendingDelete(null)}
        />
      )}
    </div>
  )
}
