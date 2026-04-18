import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { api, TeamAgent, TeamAssignPayload, TeamEvent, TeamSnapshot, TeamTask, TriggerEvent } from '../api/client'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { ErrorBanner } from '../components/ErrorBanner'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { toast } from '../toast'

interface AgentTemplate {
  name: string
  role: string
  mission: string
  style: string
  allowedSkills: string[]
  autonomy: string
}

const AGENT_TEMPLATES: AgentTemplate[] = [
  {
    name: 'Scout',
    role: 'Research Specialist',
    mission: 'Search the web, retrieve data, and synthesize research findings to inform decisions.',
    style: 'Concise. Lead with key findings. Flag uncertainties.',
    allowedSkills: ['websearch', 'web', 'fs'],
    autonomy: 'assistive',
  },
  {
    name: 'Builder',
    role: 'Drafting and Implementation Specialist',
    mission: 'Write, edit, and test code artifacts as directed. Verify correctness before reporting done.',
    style: 'Precise. Show your work. Summarize changes made.',
    allowedSkills: ['fs', 'terminal', 'websearch'],
    autonomy: 'on_demand',
  },
  {
    name: 'Reviewer',
    role: 'Quality Specialist',
    mission: 'Audit code, content, or plans for errors, edge cases, and improvement opportunities.',
    style: 'Critical but constructive. Enumerate issues clearly.',
    allowedSkills: ['fs', 'websearch', 'web'],
    autonomy: 'assistive',
  },
  {
    name: 'Operator',
    role: 'Execution Specialist',
    mission: 'Execute repeatable workflows, run automations, and operate tools on behalf of the team.',
    style: 'Methodical. Log each step. Report outcomes clearly.',
    allowedSkills: ['terminal', 'applescript', 'fs', 'system'],
    autonomy: 'on_demand',
  },
  {
    name: 'Monitor',
    role: 'Watcher',
    mission: 'Watch for conditions, check system health, and surface anomalies to the team.',
    style: 'Brief. Signal-to-noise ratio matters. Only report what changed.',
    allowedSkills: ['websearch', 'web', 'system', 'fs'],
    autonomy: 'bounded_autonomous',
  },
]

function formatAutonomy(value: string): string {
  const map: Record<string, string> = {
    assistive: 'Assistive',
    on_demand: 'On Demand',
    bounded_autonomous: 'Autonomous',
    autonomous: 'Autonomous',
  }
  return map[value] ?? value.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

function formatLabel(value: string): string {
  return value.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

function formatRelative(iso?: string): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(diff) || diff < 0) return 'just now'
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  return `${Math.floor(diff / 86_400_000)}d ago`
}

function formatStamp(iso?: string): string {
  if (!iso) return '—'
  try {
    return new Date(iso).toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    })
  } catch {
    return iso
  }
}

function statusBadgeClass(status: string): string {
  switch (status.toLowerCase()) {
    case 'online':
    case 'idle':
    case 'completed':
    case 'enabled':
      return 'badge badge-green'
    case 'working':
    case 'busy':
    case 'running':
    case 'in_progress':
      return 'badge badge-blue'
    case 'needs_review':
    case 'attention_needed':
    case 'pending_approval':
    case 'paused':
    case 'waiting':
    case 'blocked':
      return 'badge badge-yellow'
    case 'disabled':
    case 'error':
    case 'failed':
    case 'cancelled':
    case 'canceled':
      return 'badge badge-red'
    default:
      return 'badge badge-gray'
  }
}

function humanEventType(t: string): string {
  const map: Record<string, string> = {
    'team.task.completed': 'Task completed',
    'team.task.failed': 'Task failed',
    'team.task.started': 'Task started',
    'team.task.cancelled': 'Task cancelled',
    'team.task.pending_approval': 'Awaiting approval',
    'team.task.approved': 'Task approved',
    'team.task.rejected': 'Task rejected',
    'team.synced': 'Agents synced',
    'team.synced.v1': 'Agents synced',
    'team.agent.created': 'Agent created',
    'team.agent.deleted': 'Agent deleted',
    'team.tool.started': 'Tool started',
    'team.tool.finished': 'Tool finished',
    'team.tool.failed': 'Tool failed',
    'team.tool.approval_required': 'Tool needs approval',
    'agent.triggered': 'Auto-triggered',
    'agent.task.completed': 'Agent task completed',
    'agent.task.failed': 'Agent task failed',
    'agent.task.step': 'Task step recorded',
  }
  return map[t] ?? t.replace(/[._]/g, ' ')
}

function triggerStatusBadge(status: string): string {
  switch (status) {
    case 'fired': return 'badge badge-green'
    case 'suppressed': return 'badge badge-gray'
    default: return 'badge badge-yellow'
  }
}

function eventTone(eventType: string): string {
  if (eventType.includes('failed') || eventType.includes('error')) return 'team-event-danger'
  if (eventType.includes('pending') || eventType.includes('approval')) return 'team-event-warning'
  if (eventType.includes('completed') || eventType.includes('resumed') || eventType.includes('enabled')) return 'team-event-good'
  return ''
}

const ChevronIcon = ({ open }: { open: boolean }) => (
  <svg
    width="14" height="14" viewBox="0 0 14 14"
    fill="none" stroke="currentColor" stroke-width="1.8"
    stroke-linecap="round" stroke-linejoin="round"
    style={{ transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 200ms ease', flexShrink: 0, opacity: 0.5 }}
  >
    <path d="M2.5 5l4.5 4 4.5-4" />
  </svg>
)

const BlockedIcon = () => (
  <svg width="13" height="13" viewBox="0 0 13 13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
    <circle cx="6.5" cy="6.5" r="5" />
    <path d="M6.5 3.5v3M6.5 9v.5" />
  </svg>
)

export function Team() {
  const [snapshot, setSnapshot] = useState<TeamSnapshot | null>(null)
  const [tasks, setTasks] = useState<TeamTask[]>([])
  const [events, setEvents] = useState<TeamEvent[]>([])
  const [triggers, setTriggers] = useState<TriggerEvent[]>([])
  const [expandedAgentID, setExpandedAgentID] = useState<string | null>(null)
  const [expandedTaskID, setExpandedTaskID] = useState<string | null>(null)
  const [expandedTask, setExpandedTask] = useState<TeamTask | null>(null)
  const [expandedStepIDs, setExpandedStepIDs] = useState<Set<string>>(new Set())
  const [visibleEventCount, setVisibleEventCount] = useState(3)
  const [visibleTaskCount, setVisibleTaskCount] = useState(3)
  const [visibleBlockedCount, setVisibleBlockedCount] = useState(3)
  const [expandedEventDetails, setExpandedEventDetails] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [acting, setActing] = useState<Record<string, boolean>>({})
  const [showTemplateModal, setShowTemplateModal] = useState(false)
  const [selectedTemplateName, setSelectedTemplateName] = useState<string>(AGENT_TEMPLATES[0]?.name ?? '')
  const [agentPendingDelete, setAgentPendingDelete] = useState<TeamAgent | null>(null)
  const [assignAgentID, setAssignAgentID] = useState<string | null>(null)
  const [assignTask, setAssignTaskText] = useState('')
  const [assignGoal, setAssignGoal] = useState('')
  const [submittingAssign, setSubmittingAssign] = useState(false)

  const EVENT_PAGE_SIZE = 3
  const TASK_PAGE_SIZE = 3
  const BLOCKED_PAGE_SIZE = 3
  const STEP_CLAMP_CHARS = 240
  const selectedTemplate = AGENT_TEMPLATES.find((tpl) => tpl.name === selectedTemplateName) ?? AGENT_TEMPLATES[0]

  const load = async ({ silent = false }: { silent?: boolean } = {}) => {
    if (!silent) setRefreshing(true)
    try {
      const [snapshotData, tasksData, eventsData, triggersData] = await Promise.all([
        api.teamSnapshot(),
        api.teamTasks(),
        api.teamEvents(),
        api.teamTriggers().catch(() => [] as TriggerEvent[]),
      ])
      setSnapshot(snapshotData)
      setTasks(tasksData)
      setEvents(eventsData)
      setTriggers(triggersData)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load team workspace.')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => {
    void load()
    const interval = window.setInterval(() => void load({ silent: true }), 8000)
    return () => window.clearInterval(interval)
  }, [])

  useEffect(() => {
    if (!expandedTaskID) {
      setExpandedTask(null)
      return
    }
    let cancelled = false
    void api.teamTask(expandedTaskID)
      .then((task) => { if (!cancelled) setExpandedTask(task) })
      .catch((err) => { if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load task detail.') })
    return () => { cancelled = true }
  }, [expandedTaskID])

  const runningTasks = useMemo(() => tasks.filter((t) => t.status === 'working' || t.status === 'running').length, [tasks])
  const blockedItems = snapshot?.blockedItems ?? []
  const suggestedActions = snapshot?.suggestedActions ?? []

  const runAction = async (key: string, action: () => Promise<unknown>, successMessage: string) => {
    setActing((prev) => ({ ...prev, [key]: true }))
    try {
      await action()
      toast.success(successMessage)
      await load({ silent: true })
      if (expandedTaskID) {
        const detail = await api.teamTask(expandedTaskID)
        setExpandedTask(detail)
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Action failed')
    } finally {
      setActing((prev) => ({ ...prev, [key]: false }))
    }
  }

  const toggleTask = (taskID: string) => {
    if (expandedTaskID === taskID) {
      setExpandedTaskID(null)
      setExpandedTask(null)
    } else {
      setExpandedTask(null)
      setExpandedTaskID(taskID)
      setExpandedStepIDs(new Set())
    }
  }

  const toggleStep = (stepID: string) => {
    setExpandedStepIDs((prev) => {
      const next = new Set(prev)
      if (next.has(stepID)) next.delete(stepID)
      else next.add(stepID)
      return next
    })
  }

  const applyTemplate = async (template: AgentTemplate) => {
    const payload: Partial<TeamAgent> = {
      name: template.name,
      role: template.role,
      mission: template.mission,
      style: template.style,
      allowedSkills: template.allowedSkills,
      autonomy: template.autonomy,
      enabled: true,
    }
    try {
      await api.createTeamAgent(payload)
      toast.success(`${template.name} agent created`)
      setShowTemplateModal(false)
      await load({ silent: true })
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to create agent')
    }
  }

  const submitAssignTask = async () => {
    if (!assignAgentID || !assignTask.trim() || submittingAssign) return
    setSubmittingAssign(true)
    try {
      const payload: TeamAssignPayload = {
        agentID: assignAgentID,
        task: assignTask.trim(),
        goal: assignGoal.trim() || undefined,
      }
      await api.assignTeamTask(payload)
      toast.success('Task assigned')
      setAssignAgentID(null)
      setAssignTaskText('')
      setAssignGoal('')
      await load({ silent: true })
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to assign task')
    } finally {
      setSubmittingAssign(false)
    }
  }

  const confirmDeleteAgent = async () => {
    if (!agentPendingDelete) return
    const agent = agentPendingDelete
    const deleteKey = `delete:${agent.id}`
    setActing((prev) => ({ ...prev, [deleteKey]: true }))
    try {
      await api.deleteTeamAgent(agent.id)
      toast.success(`${agent.name} deleted`)
      setAgentPendingDelete(null)
      if (expandedAgentID === agent.id) setExpandedAgentID(null)
      if (assignAgentID === agent.id) {
        setAssignAgentID(null)
        setAssignTaskText('')
        setAssignGoal('')
      }
      await load({ silent: true })
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete agent')
    } finally {
      setActing((prev) => ({ ...prev, [deleteKey]: false }))
    }
  }



  if (loading) {
    return (
      <div class="screen">
        <PageHeader title="Team HQ" subtitle="Manage agents, delegate tasks, and monitor team activity." />
        <PageSpinner />
      </div>
    )
  }

  return (
    <div class={`screen team-screen${showTemplateModal ? ' team-screen-modal-open' : ''}`}>
      <PageHeader
        title="Team HQ"
        subtitle="Manage agents, delegate tasks, and monitor team activity."
        actions={(
          <div class="team-header-actions">
            <button class="btn btn-primary btn-sm" onClick={() => setShowTemplateModal(true)}>
              Add Agent
            </button>
          </div>
        )}
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

      {/* ── Template Picker Modal ──────────────────────────────────── */}
      {showTemplateModal && (
        <div class="team-modal-overlay" onClick={() => setShowTemplateModal(false)}>
          <div class="team-modal" onClick={(e) => e.stopPropagation()}>
            <div class="team-modal-header">
              <div class="team-modal-title-wrap">
                <div class="team-section-label">Template Library</div>
                <h3>Add Agent from Template</h3>
                <p class="team-modal-subtitle">Pick a starting point that feels native to Team HQ. You can refine the agent in AGENTS.md after creation.</p>
              </div>
              <button class="team-modal-close" onClick={() => setShowTemplateModal(false)} aria-label="Close add agent dialog">✕</button>
            </div>
            <div class="team-modal-body">
              <aside class="team-template-picker">
                <div class="team-section-label">Agents</div>
                <div class="team-template-picker-list">
                  {AGENT_TEMPLATES.map((tpl) => {
                    const active = tpl.name === selectedTemplate?.name
                    return (
                      <button
                        key={tpl.name}
                        class={`team-template-picker-item${active ? ' active' : ''}`}
                        onClick={() => setSelectedTemplateName(tpl.name)}
                      >
                        <div class="team-template-picker-name">{tpl.name}</div>
                        <div class="team-template-picker-role">{tpl.role}</div>
                      </button>
                    )
                  })}
                </div>
              </aside>
              <div class="team-template-detail">
                {selectedTemplate && (
                  <>
                    <div class="team-template-card">
                      <div class="team-template-card-top">
                        <div>
                          <div class="team-template-name">{selectedTemplate.name}</div>
                          <div class="team-template-role">{selectedTemplate.role}</div>
                        </div>
                        <span class="team-template-cta">Selected</span>
                      </div>
                      <p class="team-template-mission">{selectedTemplate.mission}</p>
                      <div class="team-template-style">{selectedTemplate.style}</div>
                      <div class="team-template-footer">
                        <span class="team-token team-token-muted">{formatAutonomy(selectedTemplate.autonomy)}</span>
                        {selectedTemplate.allowedSkills.map((s) => (
                          <span class="team-token" key={s}>{s}</span>
                        ))}
                      </div>
                    </div>
                    <div class="team-modal-sidebar-card">
                      <div class="team-section-label">Station Notes</div>
                      <h4>What creation does</h4>
                      <p>Atlas creates the team member and makes it immediately available in Team HQ.</p>
                    </div>
                  </>
                )}
              </div>
              {selectedTemplate && (
                <div class="team-template-action-row">
                  <button class="btn" onClick={() => setShowTemplateModal(false)}>Cancel</button>
                  <button class="btn btn-primary" onClick={() => void applyTemplate(selectedTemplate)}>
                    Create {selectedTemplate.name}
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* ── Atlas Station ──────────────────────────────────────────── */}
      <div class="card team-atlas-card">

        <div class="team-atlas-meta">
          <div class="team-section-label">{snapshot?.atlas.name ?? 'Atlas'} Station</div>
          <div class="team-atlas-name-row">
            <div class="team-atlas-name-with-dot">
              <h2>{snapshot?.atlas.name ?? 'Atlas'}</h2>
              <span class="team-live-dot" title="Live — auto-refreshing" />
            </div>
            {snapshot?.atlas.status && !['online', 'idle'].includes(snapshot.atlas.status) && (
              <span class={statusBadgeClass(snapshot.atlas.status)}>{formatLabel(snapshot.atlas.status)}</span>
            )}
          </div>
          <p>{snapshot?.atlas.role ?? 'Coordinator'}</p>
        </div>
        <div class="team-kpi-row">
          <TeamKPI label="Agents"  value={String(snapshot?.agents.length ?? 0)} />
          <TeamKPI label="Running" value={String(runningTasks)} tone={runningTasks > 0 ? 'blue' : undefined} />
          <TeamKPI label="Blocked" value={String(blockedItems.length)} tone={blockedItems.length > 0 ? 'amber' : undefined} />
          <TeamKPI label="Activity" value={String(events.length)} />
        </div>

        {/* Suggested Actions — only rendered when non-empty */}
        {suggestedActions.length > 0 && (
          <div class="team-suggestions-section">
            <div class="team-suggestions-label">Suggested Actions</div>
            <div class="team-suggestions-list">
              {suggestedActions.map((item) => (
                <div class="team-suggestion-item" key={`${item.kind}:${item.id}`}>
                  <div class="team-suggestion-text">{item.title}</div>
                  <div class="team-suggestion-meta">{item.kind}{item.agentID ? ` · ${item.agentID}` : ''}</div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>


      {/* ── Agent Stations ─────────────────────────────────────────── */}
      <div class="card">
        <div class="card-header">
          <span class="card-title">Agent Stations</span>
        </div>
        <div class="team-panel-body">
          {snapshot?.agents.length ? (
            <div class="team-agent-grid">
              {snapshot.agents.map((agent) => {
                const isExpanded = expandedAgentID === agent.id
                const busyKey = `agent:${agent.id}`
                const agentTasks = tasks.filter((t) => t.agentID === agent.id).slice(0, 4)
                return (
                  <div key={agent.id} class="team-agent-card-wrap">
                    <div class={`team-agent-card${isExpanded ? ' expanded' : ''}${assignAgentID === agent.id ? ' assigning' : ''}`}>
                      <button
                        class="team-agent-delete-btn"
                        disabled={!!acting[`delete:${agent.id}`] || agent.runtime.status === 'working' || agent.runtime.status === 'busy'}
                        onClick={() => setAgentPendingDelete(agent)}
                        aria-label={`Delete ${agent.name}`}
                      >✕</button>
                      <div class="team-agent-card-top">
                        <div>
                          <div class="team-agent-name">{agent.name}</div>
                          <div class="team-agent-role">{formatLabel(agent.role)}</div>
                        </div>
                        <span class={statusBadgeClass(agent.runtime.status)}>{formatLabel(agent.runtime.status)}</span>
                      </div>
                      <p class="team-agent-mission">{agent.mission}</p>
                      <div class="team-agent-meta-row">
                        <span>{agent.enabled ? 'Enabled' : 'Disabled'}</span>
                        <span>{formatAutonomy(agent.autonomy)}</span>
                        <span>{formatRelative(agent.runtime.lastActiveAt ?? agent.runtime.updatedAt)}</span>
                      </div>
                      <div class="team-agent-card-footer">
                        <div class="team-agent-actions-left">
                          <button
                            class="btn btn-sm btn-primary"
                            disabled={!agent.enabled || agent.runtime.status === 'working' || agent.runtime.status === 'busy'}
                            onClick={() => {
                              const opening = assignAgentID !== agent.id
                              setAssignAgentID(opening ? agent.id : null)
                              if (opening) setExpandedAgentID(null)
                            }}
                          >
                            Assign
                          </button>
                          <button
                            class="btn btn-sm"
                            disabled={!!acting[busyKey] || !agent.enabled}
                            onClick={() => void runAction(
                              busyKey,
                              () => agent.runtime.status === 'paused' ? api.resumeTeamAgent(agent.id) : api.pauseTeamAgent(agent.id),
                              agent.runtime.status === 'paused' ? `${agent.name} resumed` : `${agent.name} paused`,
                            )}
                          >
                            {agent.runtime.status === 'paused' ? 'Resume' : 'Pause'}
                          </button>
                          <button
                            class="btn btn-sm"
                            disabled={!!acting[busyKey]}
                            onClick={() => void runAction(
                              busyKey,
                              () => agent.enabled ? api.disableTeamAgent(agent.id) : api.enableTeamAgent(agent.id),
                              agent.enabled ? `${agent.name} disabled` : `${agent.name} enabled`,
                            )}
                          >
                            {agent.enabled ? 'Disable' : 'Enable'}
                          </button>
                        </div>
                        <button
                          class="btn btn-sm"
                          onClick={() => {
                            const opening = !isExpanded
                            setExpandedAgentID(opening ? agent.id : null)
                            if (opening) { setAssignAgentID(null); setAssignTaskText(''); setAssignGoal('') }
                          }}
                          aria-expanded={isExpanded}
                        >
                          Details <ChevronIcon open={isExpanded} />
                        </button>
                      </div>
                    </div>

                    {assignAgentID === agent.id && (
                      <div class="team-assign-form">
                        <div class="team-assign-form-title">Assign task to {agent.name}</div>
                        <textarea
                          class="team-assign-textarea"
                          placeholder="Describe the task…"
                          value={assignTask}
                          onInput={(e) => setAssignTaskText((e.target as HTMLTextAreaElement).value)}
                          rows={3}
                        />
                        <input
                          class="team-assign-input"
                          type="text"
                          placeholder="Goal label (optional)"
                          value={assignGoal}
                          onInput={(e) => setAssignGoal((e.target as HTMLInputElement).value)}
                        />
                        <div class="team-assign-form-actions">
                          <button
                            class="btn btn-primary btn-sm"
                            disabled={submittingAssign || !assignTask.trim()}
                            onClick={() => void submitAssignTask()}
                          >
                            {submittingAssign ? 'Assigning…' : 'Assign'}
                          </button>
                          <button
                            class="btn btn-sm"
                            onClick={() => { setAssignAgentID(null); setAssignTaskText(''); setAssignGoal('') }}
                          >
                            Cancel
                          </button>
                        </div>
                      </div>
                    )}

                    {isExpanded && (
                      <div class="team-agent-drawer">
                        <div class="team-detail-stack">
                          <div class="team-drawer-section">
                            <div class="team-drawer-section-label">Mission</div>
                            <p class="team-detail-copy">{agent.mission}</p>
                            {agent.style && <div class="team-detail-note">Style: {agent.style}</div>}
                            {agent.activation && <div class="team-detail-note">Activation: {agent.activation}</div>}
                            {agent.providerType && (
                              <div class="team-detail-note team-provider-badge">
                                Provider: {agent.providerType}{agent.model ? ` · ${agent.model}` : ''}
                              </div>
                            )}
                          </div>

                          <div class="team-drawer-section">
                            <div class="team-drawer-section-label">Allowed Skills</div>
                            <div class="team-token-group">
                              {agent.allowedSkills.map((skill) => (
                                <span class="team-token" key={skill}>{skill}</span>
                              ))}
                              {!agent.allowedSkills.length && <span class="team-empty-inline">No skills assigned.</span>}
                            </div>
                          </div>

                          {!!agent.allowedToolClasses?.length && (
                            <div class="team-drawer-section">
                              <div class="team-drawer-section-label">Tool Classes</div>
                              <div class="team-token-group">
                                {agent.allowedToolClasses.map((tc) => (
                                  <span class="team-token team-token-muted" key={tc}>{tc}</span>
                                ))}
                              </div>
                            </div>
                          )}

                          <div class="team-drawer-section">
                            <div class="team-drawer-section-label">Recent Delegated Work</div>
                            {agentTasks.length ? (
                              <div class="team-mini-list">
                                {agentTasks.map((task) => (
                                  <div class="team-mini-task-row" key={task.taskID}>
                                    <span class="team-mini-task-goal">{task.title || task.goal}</span>
                                    <span class={statusBadgeClass(task.status)}>{task.status}</span>
                                  </div>
                                ))}
                              </div>
                            ) : (
                              <div class="team-empty-inline">No tasks for this agent yet.</div>
                            )}
                          </div>

                          {agent.metrics && (
                            <div class="team-drawer-section">
                              <div class="team-drawer-section-label">Performance</div>
                              <div class="team-metrics-row">
                                <div class="team-metric-chip">
                                  <div class="team-metric-value">{agent.metrics.tasksCompleted}</div>
                                  <div class="team-metric-label">Completed</div>
                                </div>
                                <div class="team-metric-chip team-metric-chip-muted">
                                  <div class="team-metric-value">{agent.metrics.tasksFailed}</div>
                                  <div class="team-metric-label">Failed</div>
                                </div>
                                <div class="team-metric-chip">
                                  <div class="team-metric-value">{agent.metrics.totalToolCalls}</div>
                                  <div class="team-metric-label">Tool Calls</div>
                                </div>
                                {agent.metrics.lastActiveAt && (
                                  <div class="team-metric-chip">
                                    <div class="team-metric-value">{formatRelative(agent.metrics.lastActiveAt)}</div>
                                    <div class="team-metric-label">Last Active</div>
                                  </div>
                                )}
                              </div>
                              {agent.metrics.successRate != null && (agent.metrics.tasksCompleted + agent.metrics.tasksFailed) >= 3 && (
                                <div class="team-success-rate">
                                  <span class="team-success-rate-label">Success rate</span>
                                  <div class="team-success-rate-bar">
                                    <div
                                      class="team-success-rate-fill"
                                      style={{ width: `${(agent.metrics.successRate * 100).toFixed(0)}%` }}
                                    />
                                  </div>
                                  <span class="team-success-rate-pct">{(agent.metrics.successRate * 100).toFixed(0)}%</span>
                                </div>
                              )}
                            </div>
                          )}
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          ) : (
            <TeamEmptyState
              title="No agents configured"
              body="Click Add Agent above to create your first teammate from a template."
            />
          )}
        </div>
      </div>

      {/* ── Blocked Items ──────────────────────────────────────────── */}
      {blockedItems.length > 0 && (
        <div class="card">
          <div class="card-header">
            <span class="card-title">Blocked Items</span>
            <span class="team-event-count-badge">{blockedItems.length} item{blockedItems.length !== 1 ? 's' : ''}</span>
          </div>
          <div class="team-panel-body">
            <div class="team-task-list">
              {blockedItems.slice(0, visibleBlockedCount).map((item) => (
                <div class="team-task-item-wrap" key={`${item.kind}:${item.id}`}>
                  <div class="team-task-row" style={{ cursor: 'default' }}>
                    <div class="team-task-row-main">
                      <div class="team-task-title">{item.title}</div>
                      <div class="team-task-meta">
                        {item.kind}{item.blockingKind ? ` · ${item.blockingKind}` : ''}{item.blockingDetail ? ` · ${item.blockingDetail}` : ''}
                      </div>
                    </div>
                    <div class="team-task-row-right">
                      <span class={statusBadgeClass(item.status)}>{item.status}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
            {(visibleBlockedCount < blockedItems.length || visibleBlockedCount > BLOCKED_PAGE_SIZE) && (
              <div class="team-event-pagination">
                {visibleBlockedCount < blockedItems.length ? (
                  <button class="team-show-more-btn" onClick={() => setVisibleBlockedCount((n) => n + BLOCKED_PAGE_SIZE)}>
                    Show {Math.min(BLOCKED_PAGE_SIZE, blockedItems.length - visibleBlockedCount)} more
                  </button>
                ) : <span />}
                {visibleBlockedCount > BLOCKED_PAGE_SIZE && (
                  <button class="team-show-more-btn" onClick={() => setVisibleBlockedCount(BLOCKED_PAGE_SIZE)}>
                    Show less
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {/* ── Delegated Tasks ─────────────────────────────────────────── */}
      <div class="card">
        <div class="card-header">
          <span class="card-title">Delegated Tasks</span>
          {tasks.length > 0 && (
            <span class="team-event-count-badge">{tasks.length} task{tasks.length !== 1 ? 's' : ''}</span>
          )}
        </div>
        <div class="team-panel-body">
          {tasks.length ? (
            <div class="team-task-list">
              {tasks.slice(0, visibleTaskCount).map((task) => {
                const isExpanded = expandedTaskID === task.taskID
                const detail = isExpanded ? expandedTask : null
                return (
                  <div key={task.taskID} class="team-task-item-wrap">
                    <button
                      class={`team-task-row${isExpanded ? ' expanded' : ''}`}
                      onClick={() => toggleTask(task.taskID)}
                    >
                      <div class="team-task-row-main">
                        <div class="team-task-title">{task.title || task.goal}</div>
                        <div class="team-task-meta">
                          {task.agentID} · {task.requestedBy} · {formatRelative(task.updatedAt)}
                        </div>
                      </div>
                      <div class="team-task-row-right">
                        <span class={statusBadgeClass(task.status)}>{task.status}</span>
                        <ChevronIcon open={isExpanded} />
                      </div>
                    </button>

                    {isExpanded && (
                      <div class="team-task-drawer">
                        <div class="team-task-drawer-header">
                          <div>
                            <div class="team-detail-note">Started {formatStamp(task.startedAt)}</div>
                            {detail?.resultSummary && (
                              <p class="team-detail-copy" style={{ marginTop: '6px' }}>{detail.resultSummary}</p>
                            )}
                            {detail?.errorMessage && (
                              <div class="team-task-error" style={{ marginTop: '8px' }}>{detail.errorMessage}</div>
                            )}
                          </div>
                          {(task.status === 'working' || task.status === 'running') && (
                            <button
                              class="btn btn-sm btn-danger"
                              onClick={() => void runAction(
                                `task:${task.taskID}`,
                                () => api.cancelTeamTask(task.taskID),
                                'Task cancelled',
                              )}
                              disabled={!!acting[`task:${task.taskID}`]}
                            >
                              Cancel
                            </button>
                          )}
                          {(task.status === 'needs_review' || task.status === 'pending_approval') && (
                            <div class="team-approval-block">
                              <div class="team-approval-context">
                                <span class="team-approval-agent-badge">{task.agentID}</span>
                                <span class="team-approval-label">is requesting approval to proceed</span>
                              </div>
                              <div style={{ display: 'flex', gap: '8px' }}>
                                <button
                                  class="btn btn-sm btn-primary"
                                  onClick={() => void runAction(
                                    `task:${task.taskID}`,
                                    () => api.approveTeamTask(task.taskID),
                                    'Task approved',
                                  )}
                                  disabled={!!acting[`task:${task.taskID}`]}
                                >
                                  Approve
                                </button>
                                <button
                                  class="btn btn-sm btn-danger"
                                  onClick={() => void runAction(
                                    `task:${task.taskID}`,
                                    () => api.rejectTeamTask(task.taskID),
                                    'Task rejected',
                                  )}
                                  disabled={!!acting[`task:${task.taskID}`]}
                                >
                                  Reject
                                </button>
                              </div>
                            </div>
                          )}
                        </div>

                        <div class="team-step-list">
                          {detail?.steps?.length ? detail.steps.map((step) => {
                            const isStepExpanded = expandedStepIDs.has(step.stepID)
                            const isLong = step.content.length > STEP_CLAMP_CHARS
                            return (
                              <div class="team-step-item" key={step.stepID}>
                                <div class="team-step-meta">
                                  <span>{step.sequenceNumber}. {step.stepType}</span>
                                  <span>{formatStamp(step.createdAt)}</span>
                                </div>
                                {step.toolName && <div class="team-step-tool">{step.toolName}</div>}
                                <div class={`team-step-content${isLong && !isStepExpanded ? ' team-step-content-clamped' : ''}`}>
                                  {step.content}
                                </div>
                                {isLong && (
                                  <button class="team-step-expand-btn" onClick={() => toggleStep(step.stepID)}>
                                    {isStepExpanded ? 'Show less' : 'Show more'}
                                  </button>
                                )}
                              </div>
                            )
                          }) : detail ? (
                            <div class="team-empty-inline">No step timeline recorded yet.</div>
                          ) : (
                            <div class="team-empty-inline">Loading…</div>
                          )}
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
              {(visibleTaskCount < tasks.length || visibleTaskCount > TASK_PAGE_SIZE) && (
                <div class="team-event-pagination">
                  {visibleTaskCount < tasks.length ? (
                    <button
                      class="team-show-more-btn"
                      onClick={() => setVisibleTaskCount((n) => n + TASK_PAGE_SIZE)}
                    >
                      Show {Math.min(TASK_PAGE_SIZE, tasks.length - visibleTaskCount)} more
                    </button>
                  ) : <span />}
                  {visibleTaskCount > TASK_PAGE_SIZE && (
                    <button
                      class="team-show-more-btn"
                      onClick={() => setVisibleTaskCount(TASK_PAGE_SIZE)}
                    >
                      Show less
                    </button>
                  )}
                </div>
              )}
            </div>
          ) : (
            <TeamEmptyInline
              title="No delegated work yet"
              body="Once Atlas hands a task to a teammate, it will show up here with live status and step history."
            />
          )}
        </div>
      </div>

      {/* ── Recent Activity ─────────────────────────────────────────── */}
      <div class="card">
        <div class="card-header">
          <span class="card-title">Recent Activity</span>
          {events.length > 0 && (
            <button
              class="btn btn-sm"
              onClick={() => void runAction('clear-events', () => api.clearTeamEvents(), 'Activity cleared')}
            >
              Clear
            </button>
          )}
        </div>
        <div class="team-panel-body">
          <div class="team-event-list">
            {events.length ? (
              <>
                {events.slice(0, visibleEventCount).map((event) => {
                  const detailExpanded = expandedEventDetails.has(event.eventID)
                  const detailLong = !!event.detail && event.detail.length > 200
                  return (
                    <div class={`team-event-item ${eventTone(event.eventType)}`} key={event.eventID}>
                      <div class="team-event-title-row">
                        <div class="team-event-title">{event.title}</div>
                        <div class="team-event-time">{formatRelative(event.createdAt)}</div>
                      </div>
                      <div class="team-event-meta">{humanEventType(event.eventType)}</div>
                      {event.detail && (
                        <>
                          <div class={`team-event-detail${detailLong && !detailExpanded ? ' team-event-detail-clamped' : ''}`}>
                            {event.detail}
                          </div>
                          {detailLong && (
                            <button
                              class="team-step-expand-btn"
                              onClick={() => setExpandedEventDetails(prev => {
                                const s = new Set(prev); s.has(event.eventID) ? s.delete(event.eventID) : s.add(event.eventID); return s
                              })}
                            >
                              {detailExpanded ? 'Show less' : 'Show more'}
                            </button>
                          )}
                        </>
                      )}
                    </div>
                  )
                })}
                {(visibleEventCount < events.length || visibleEventCount > EVENT_PAGE_SIZE) && (
                  <div class="team-event-pagination">
                    {visibleEventCount < events.length ? (
                      <button
                        class="team-show-more-btn"
                        onClick={() => setVisibleEventCount((n) => n + EVENT_PAGE_SIZE)}
                      >
                        Show {Math.min(EVENT_PAGE_SIZE, events.length - visibleEventCount)} more
                      </button>
                    ) : <span />}
                    {visibleEventCount > EVENT_PAGE_SIZE && (
                      <button
                        class="team-show-more-btn"
                        onClick={() => setVisibleEventCount(EVENT_PAGE_SIZE)}
                      >
                        Show less
                      </button>
                    )}
                  </div>
                )}
              </>
            ) : (
              <TeamEmptyInline
                title="No team events yet"
                body="Once syncs, delegations, or state changes happen, the activity rail will populate here."
              />
            )}
          </div>
        </div>
      </div>

      {/* ── Autonomy Triggers ──────────────────────────────────────────── */}
      {triggers.length > 0 && (
        <div class="card">
          <div class="card-header">
            <span class="card-title">Autonomy Triggers</span>
            <span class="team-event-count-badge">{triggers.length} event{triggers.length !== 1 ? 's' : ''}</span>
          </div>
          <div class="team-panel-body">
            <div class="team-trigger-list">
              {triggers.slice(0, 10).map((t) => (
                <div class="team-trigger-item" key={t.triggerID}>
                  <div class="team-trigger-row">
                    <div class="team-trigger-info">
                      <span class="team-trigger-type">{t.triggerType.replace(/\./g, ' › ')}</span>
                      {t.agentID && <span class="team-trigger-agent">{t.agentID}</span>}
                    </div>
                    <div class="team-trigger-right">
                      <span class={triggerStatusBadge(t.status)}>{t.status}</span>
                      <span class="team-trigger-time">{formatRelative(t.createdAt)}</span>
                    </div>
                  </div>
                  <div class="team-trigger-instruction">{t.instruction}</div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {agentPendingDelete && (
        <ConfirmDialog
          title={`Delete ${agentPendingDelete.name}?`}
          body="This permanently removes the teammate from Team HQ. Use disable if you only want to stop using an agent without removing its definition."
          confirmLabel="Delete Agent"
          cancelLabel="Keep Agent"
          danger
          onConfirm={() => void confirmDeleteAgent()}
          onCancel={() => setAgentPendingDelete(null)}
        />
      )}
    </div>
  )
}

function TeamKPI({ label, value, tone }: { label: string; value: string; tone?: 'blue' | 'amber' }) {
  return (
    <div class={`team-kpi${tone ? ` team-kpi-${tone}` : ''}`}>
      <div class="team-kpi-value">{value}</div>
      <div class="team-kpi-label">{label}</div>
    </div>
  )
}

function TeamEmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div class="team-empty-state">
      {/*
        Circle ring — faint orbit track + 5 explicit arc paths, one per adjacent edge.
        Each path is a CW arc (sweep-flag=1) from one node to the next.
        Nodes drawn last so they sit on top of the arc endpoints.
      */}
      <svg class="team-empty-icon" width="64" height="64" viewBox="0 0 64 64" fill="none" aria-hidden="true">
        <circle class="team-orbit-track" cx="32" cy="32" r="22" />
        {/* Arc N1→N2 */}
        <path class="team-edge-spark team-spark-1" d="M 32 10 A 22 22 0 0 1 53 25" />
        {/* Arc N2→N3 */}
        <path class="team-edge-spark team-spark-2" d="M 53 25 A 22 22 0 0 1 45 50" />
        {/* Arc N3→N4 */}
        <path class="team-edge-spark team-spark-3" d="M 45 50 A 22 22 0 0 1 19 50" />
        {/* Arc N4→N5 */}
        <path class="team-edge-spark team-spark-4" d="M 19 50 A 22 22 0 0 1 11 25" />
        {/* Arc N5→N1 */}
        <path class="team-edge-spark team-spark-5" d="M 11 25 A 22 22 0 0 1 32 10" />
        <circle class="team-node-dot team-node-1" cx="32" cy="10" r="3.5" fill="currentColor" />
        <circle class="team-node-dot team-node-2" cx="53" cy="25" r="3.5" fill="currentColor" />
        <circle class="team-node-dot team-node-3" cx="45" cy="50" r="3.5" fill="currentColor" />
        <circle class="team-node-dot team-node-4" cx="19" cy="50" r="3.5" fill="currentColor" />
        <circle class="team-node-dot team-node-5" cx="11" cy="25" r="3.5" fill="currentColor" />
      </svg>
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  )
}

function TeamEmptyInline({ title, body }: { title: string; body: string }) {
  return (
    <div class="team-empty-inline-card">
      <div class="team-empty-inline-title">{title}</div>
      <div class="team-empty-inline">{body}</div>
    </div>
  )
}
