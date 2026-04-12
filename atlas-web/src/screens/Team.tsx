import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, TeamEvent, TeamSnapshot, TeamTask } from '../api/client'
import { ErrorBanner } from '../components/ErrorBanner'
import { PageHeader } from '../components/PageHeader'
import { PageSpinner } from '../components/PageSpinner'
import { toast } from '../toast'

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
    case 'busy':
    case 'running':
    case 'in_progress':
      return 'badge badge-blue'
    case 'attention_needed':
    case 'pending_approval':
    case 'paused':
      return 'badge badge-yellow'
    case 'disabled':
    case 'error':
    case 'failed':
    case 'cancelled':
      return 'badge badge-red'
    default:
      return 'badge badge-gray'
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
  const [expandedAgentID, setExpandedAgentID] = useState<string | null>(null)
  const [expandedTaskID, setExpandedTaskID] = useState<string | null>(null)
  const [expandedTask, setExpandedTask] = useState<TeamTask | null>(null)
  const [expandedStepIDs, setExpandedStepIDs] = useState<Set<string>>(new Set())
  const [visibleEventCount, setVisibleEventCount] = useState(3)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [acting, setActing] = useState<Record<string, boolean>>({})

  const EVENT_PAGE_SIZE = 3
  const STEP_CLAMP_CHARS = 240

  const load = async ({ silent = false }: { silent?: boolean } = {}) => {
    if (!silent) setRefreshing(true)
    try {
      const [snapshotData, tasksData, eventsData] = await Promise.all([
        api.teamSnapshot(),
        api.teamTasks(),
        api.teamEvents(),
      ])
      setSnapshot(snapshotData)
      setTasks(tasksData)
      setEvents(eventsData)
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

  const runningTasks = useMemo(() => tasks.filter((t) => t.status === 'running').length, [tasks])
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

  if (loading) {
    return (
      <div class="screen">
        <PageHeader title="Team HQ" subtitle="Stations, delegated tasks, and live AGENTS activity." />
        <PageSpinner />
      </div>
    )
  }

  return (
    <div class="screen team-screen">
      <PageHeader
        title="Team HQ"
        subtitle="Operate Atlas and every configured AGENTS teammate from one station."
        actions={(
          <div class="team-header-actions">
            <button class="btn btn-sm" onClick={() => void load()} disabled={refreshing}>
              {refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
            <button
              class="btn btn-primary btn-sm"
              onClick={() => void runAction('sync', () => api.syncTeam(), 'AGENTS synced')}
              disabled={!!acting.sync}
            >
              {acting.sync ? 'Syncing…' : 'Sync AGENTS'}
            </button>
          </div>
        )}
      />

      <ErrorBanner error={error} onDismiss={() => setError(null)} />

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
              <span class={statusBadgeClass(snapshot.atlas.status)}>{snapshot.atlas.status}</span>
            )}
          </div>
          <p>{snapshot?.atlas.role ?? 'Coordinator'}</p>
        </div>
        <div class="team-kpi-row">
          <TeamKPI label="Agents" value={String(snapshot?.agents.length ?? 0)} />
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

      {/* ── Blocked Items — contextual strip, only when non-empty ─── */}
      {blockedItems.length > 0 && (
        <div class="team-blocked-strip">
          <div class="team-blocked-strip-label">
            <BlockedIcon />
            Blocked Items
          </div>
          <div class="team-blocked-items">
            {blockedItems.map((item) => (
              <div class="team-blocked-item" key={`${item.kind}:${item.id}`}>
                <div>
                  <div class="team-action-title">{item.title}</div>
                  <div class="team-action-meta">{item.kind} · {item.status}</div>
                </div>
                <span class={statusBadgeClass(item.status)}>{item.status}</span>
              </div>
            ))}
          </div>
        </div>
      )}

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
                    <div class={`team-agent-card${isExpanded ? ' expanded' : ''}`}>
                      <div class="team-agent-card-top">
                        <div>
                          <div class="team-agent-name">{agent.name}</div>
                          <div class="team-agent-role">{agent.role}</div>
                        </div>
                        <span class={statusBadgeClass(agent.runtime.status)}>{agent.runtime.status}</span>
                      </div>
                      <p class="team-agent-mission">{agent.mission}</p>
                      <div class="team-agent-meta-row">
                        <span>{agent.enabled ? 'Enabled' : 'Disabled'}</span>
                        <span>{agent.autonomy}</span>
                        <span>{formatRelative(agent.runtime.lastActiveAt ?? agent.runtime.updatedAt)}</span>
                      </div>
                      <div class="team-agent-card-footer">
                        <div class="team-agent-actions">
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
                        </div>
                        <button
                          class="team-agent-details-btn"
                          onClick={() => setExpandedAgentID(isExpanded ? null : agent.id)}
                          aria-expanded={isExpanded}
                        >
                          Details <ChevronIcon open={isExpanded} />
                        </button>
                      </div>
                    </div>

                    {isExpanded && (
                      <div class="team-agent-drawer">
                        <div class="team-detail-stack">
                          <div class="team-drawer-section">
                            <div class="team-drawer-section-label">Mission</div>
                            <p class="team-detail-copy">{agent.mission}</p>
                            {agent.style && <div class="team-detail-note">Style: {agent.style}</div>}
                            {agent.activation && <div class="team-detail-note">Activation: {agent.activation}</div>}
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
                                    <span class="team-mini-task-goal">{task.goal}</span>
                                    <span class={statusBadgeClass(task.status)}>{task.status}</span>
                                  </div>
                                ))}
                              </div>
                            ) : (
                              <div class="team-empty-inline">No tasks for this agent yet.</div>
                            )}
                          </div>
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
              body="Add your first teammate in AGENTS.md, sync the file, and their station will appear here."
            />
          )}
        </div>
      </div>

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
              {tasks.map((task) => {
                const isExpanded = expandedTaskID === task.taskID
                const detail = isExpanded ? expandedTask : null
                return (
                  <div key={task.taskID} class="team-task-item-wrap">
                    <button
                      class={`team-task-row${isExpanded ? ' expanded' : ''}`}
                      onClick={() => toggleTask(task.taskID)}
                    >
                      <div class="team-task-row-main">
                        <div class="team-task-title">{task.goal}</div>
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
                          {task.status === 'running' && (
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
                          {task.status === 'pending_approval' && (
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
        </div>
        <div class="team-panel-body">
          <div class="team-event-list">
            {events.length ? (
              <>
                {events.slice(0, visibleEventCount).map((event) => (
                  <div class={`team-event-item ${eventTone(event.eventType)}`} key={event.eventID}>
                    <div class="team-event-title-row">
                      <div class="team-event-title">{event.title}</div>
                      <div class="team-event-time">{formatRelative(event.createdAt)}</div>
                    </div>
                    <div class="team-event-meta">{event.eventType}</div>
                    {event.detail && <div class="team-event-detail">{event.detail}</div>}
                  </div>
                ))}
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
      <div class="team-empty-orb" />
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
