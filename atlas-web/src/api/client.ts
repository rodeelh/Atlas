// Atlas HTTP API client
import type {
  APIKeyStatus,
  Approval,
  CapabilityRecord,
  DashboardDefinition,
  DashboardRefreshEvent,
  DashboardStatus,
  DashboardSummary,
  DashboardWidgetData,
  EngineDownloadStatus,
  EngineModelInfo,
  EngineStatus,
  MLXStatus,
  MLXModelInfo,
  MLXDownloadStatus,
  FsRoot,
  AutomationSummary,
  CommunicationChannel,
  CommunicationsSnapshot,
  CommunicationPlatformStatus,
  CommunicationSetupValues,
  CommunicationValidationPayload,
  ConversationDetail,
  ConversationSummary,
  ForgeProposalRecord,
  ForgeResearchingItem,
  GremlinItem,
  GremlinRun,
  ExecutableTarget,
  LinkPreview,
  LogEntry,
  MemoryItem,
  MemoryParams,
  ChatStreamEvent,
  MessageAttachment,
  MessageResponse,
  ModelSelectorInfo,
  CloudModelHealth,
  OnboardingStatus,
  ProviderStatusInfo,
  RuntimeConfig,
  RuntimeConfigUpdateResponse,
  RuntimeStatus,
  SkillRecord,
  TeamAgent,
  TeamAssignPayload,
  TeamEvent,
  TeamSnapshot,
  TeamTask,
  TriggerEvent,
  WorkflowDefinition,
  WorkflowSummary,
  WorkflowRun,
  TokenUsageSummary,
  TokenUsageEvent,
  VoiceStatus,
  VoiceModelInfo,
  VoiceTranscribeResult,
  VoiceOption,
  StorageStats,
} from './contracts'

export type {
  AIModelRecord,
  APIKeyStatus,
  DashboardDefinition,
  DashboardPreset,
  DashboardRefreshEvent,
  DashboardSize,
  DashboardStatus,
  DashboardSummary,
  DashboardWidget,
  DashboardWidgetCode,
  DashboardWidgetData,
  DashboardWidgetMode,
  EngineModelInfo,
  EngineStatus,
  MLXStatus,
  MLXModelInfo,
  MLXDownloadStatus,
  Approval,
  CapabilityRecord,
  FsRoot,
  AutomationSummary,
  CommunicationChannel,
  CommunicationsSnapshot,
  CommunicationPlatformStatus,
  CommunicationSetupValues,
  CommunicationValidationPayload,
  ConversationDetail,
  ConversationSummary,
  ForgeProposalRecord,
  ForgeResearchingItem,
  GremlinItem,
  GremlinRun,
  ExecutableTarget,
  LinkPreview,
  LogEntry,
  MemoryItem,
  MemoryParams,
  ChatStreamEvent,
  MessageAttachment,
  MessageResponse,
  ModelSelectorInfo,
  CloudModelHealth,
  OnboardingStatus,
  ProviderStatusInfo,
  RuntimeConfig,
  RuntimeConfigUpdateResponse,
  RuntimeStatus,
  SkillRecord,
  TeamAgent,
  TeamAssignPayload,
  TeamEvent,
  TeamSnapshot,
  TeamTask,
  TriggerEvent,
  WorkflowDefinition,
  WorkflowSummary,
  WorkflowRun,
  VoiceStatus,
  VoiceModelInfo,
  VoiceTranscribeResult,
  VoiceOption,
  StorageStats,
} from './contracts'

export function getPort(): string {
  try { return localStorage.getItem('atlasPort') ?? '1984' } catch { return '1984' }
}

// Derive the base API URL.
// When the page is served from a non-localhost host (LAN IP, Tailscale, etc.) we
// always target that same host — no localStorage needed, no timing race.
// Only fall back to localhost when running in the local menu-bar context.
const BASE = () => {
  if (typeof window !== 'undefined' &&
      window.location.hostname !== 'localhost' &&
      window.location.hostname !== '127.0.0.1') {
    return `${window.location.protocol}//${window.location.host}`
  }
  try {
    const stored = localStorage.getItem('atlasHost')
    if (stored) return `http://${stored}`
  } catch { /* localStorage blocked */ }
  return `http://localhost:${getPort()}`
}

let csrfTokenCache: string | null = null

function isRemoteHostRuntime(): boolean {
  if (typeof window === 'undefined') return false
  return window.location.hostname !== 'localhost' && window.location.hostname !== '127.0.0.1'
}

function requiresCSRF(method: string): boolean {
  return method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS'
}

async function ensureCSRFToken(): Promise<string> {
  if (csrfTokenCache) return csrfTokenCache
  const res = await fetch(`${BASE()}/auth/csrf`)
  if (!res.ok) {
    throw new Error('Failed to fetch CSRF token')
  }
  const body = await res.json() as { token?: string }
  const token = (body?.token ?? '').trim()
  if (!token) {
    throw new Error('Missing CSRF token')
  }
  csrfTokenCache = token
  return token
}

// ---- HTTP helpers ----

async function request<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const url = `${BASE()}${path}`
  const method = (options.method ?? 'GET').toUpperCase()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...((options.headers ?? {}) as Record<string, string>),
  }
  if (isRemoteHostRuntime() && requiresCSRF(method)) {
    headers['X-CSRF-Token'] = await ensureCSRFToken()
  }
  const res = await fetch(url, {
    ...options,
    headers,
    credentials: 'include',
  })
  if (!res.ok) {
    // 401 handling varies by context:
    // - Local (localhost): reload the page so the LocalAuthGate re-evaluates state.
    // - Remote (LAN / Tailscale): redirect to root which routes to /auth/remote-gate.
    if (res.status === 401 && typeof window !== 'undefined') {
      const isLocal = window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1'
      if (isLocal) {
        window.location.reload()
        throw new Error('Session expired — reloading')
      } else {
        window.location.href = `${window.location.origin}/`
        csrfTokenCache = null
        throw new Error('Session expired — redirecting to login')
      }
    }
    const text = await res.text().catch(() => res.statusText)
    let message = text
    try { const j = JSON.parse(text); if (j?.error) message = j.error } catch { /* use raw text */ }
    throw new Error(message)
  }
  // Some endpoints return empty bodies (204)
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : ({} as T)
}

function get<T>(path: string, params?: Record<string, string | number | undefined>): Promise<T> {
  let p = path
  if (params) {
    const q = Object.entries(params)
      .filter(([, v]) => v !== undefined)
      .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
      .join('&')
    if (q) p = `${path}?${q}`
  }
  return request<T>(p, { method: 'GET' })
}

function post<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body: JSON.stringify(body) })
}

function put<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'PUT', body: JSON.stringify(body) })
}

function del<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'DELETE', body: JSON.stringify(body) })
}

// requestWithHeaders is like request but also returns response headers.
// Used for WebAuthn ceremony routes that pass the session ID in a header.
async function requestWithHeaders<T>(
  path: string,
  options: RequestInit & { extraHeaders?: Record<string, string> } = {}
): Promise<{ data: T; headers: Headers }> {
  const url = `${BASE()}${path}`
  const method = (options.method ?? 'POST').toUpperCase()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...((options.headers ?? {}) as Record<string, string>),
    ...(options.extraHeaders ?? {}),
  }
  const res = await fetch(url, { ...options, headers, credentials: 'include' })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    let message = text
    try { const j = JSON.parse(text); if (j?.error) message = j.error } catch { /* raw */ }
    throw new Error(message)
  }
  const text = await res.text()
  const data = text ? (JSON.parse(text) as T) : ({} as T)
  return { data, headers: res.headers }
}

// ---- API surface ----

export const api = {
  status: () => get<RuntimeStatus>('/status'),
  apiKeys: () => get<APIKeyStatus>('/api-keys'),
  setAPIKey: (provider: string, key: string, name?: string, label?: string) => post<APIKeyStatus>('/api-keys', { provider, key, name, label }),
  deleteAPIKey: (name: string) => del<APIKeyStatus>('/api-keys', { name }),
  config: () => get<RuntimeConfig>('/config'),
  updateConfig: (c: Partial<RuntimeConfig>) => put<RuntimeConfigUpdateResponse>('/config', c),
  onboardingStatus: () => get<OnboardingStatus>('/onboarding'),
  updateOnboardingStatus: (completed: boolean) => put<OnboardingStatus>('/onboarding', { completed }),
  sendMessage: (conversationID: string, message: string, attachments?: MessageAttachment[]) =>
    post<MessageResponse>('/message', {
      conversationId: conversationID,
      message,
      ...(attachments && attachments.length > 0 ? { attachments } : {}),
    }),
  cancelTurn: (conversationID: string) =>
    post<void>('/message/cancel', { conversationId: conversationID }),
  streamMessage: (conversationID: string) =>
    new EventSource(`${BASE()}/message/stream?conversationID=${encodeURIComponent(conversationID)}`),
  approvals: () => get<Approval[]>('/approvals'),
  // approve/deny take the toolCall.id (toolCallID), not the approval.id
  approve: (toolCallID: string) => post<Approval>(`/approvals/${toolCallID}/approve`, {}),
  deny: (toolCallID: string) => post<Approval>(`/approvals/${toolCallID}/deny`, {}),
  teamSnapshot: () => get<TeamSnapshot>('/agents/hq'),
  teamAgents: () => get<TeamAgent[]>('/agents'),
  teamAgent: (id: string) => get<TeamAgent>(`/agents/${encodeURIComponent(id)}`),
  teamEvents: () => get<TeamEvent[]>('/agents/events'),
  teamTasks: () => get<TeamTask[]>('/agents/tasks'),
  teamTask: (id: string) => get<TeamTask>(`/agents/tasks/${encodeURIComponent(id)}`),
  syncTeam: () => post<{ count: number; source: string; updated: string; agents: TeamAgent[] }>('/agents/sync', {}),
  enableTeamAgent: (id: string) => post<TeamAgent>(`/agents/${encodeURIComponent(id)}/enable`, {}),
  disableTeamAgent: (id: string) => post<TeamAgent>(`/agents/${encodeURIComponent(id)}/disable`, {}),
  pauseTeamAgent: (id: string) => post<TeamAgent>(`/agents/${encodeURIComponent(id)}/pause`, {}),
  resumeTeamAgent: (id: string) => post<TeamAgent>(`/agents/${encodeURIComponent(id)}/resume`, {}),
  cancelTeamTask: (id: string) => post<TeamTask>(`/agents/tasks/${encodeURIComponent(id)}/cancel`, {}),
  approveTeamTask: (id: string) => post<TeamTask>(`/agents/tasks/${encodeURIComponent(id)}/approve`, {}),
  rejectTeamTask: (id: string) => post<TeamTask>(`/agents/tasks/${encodeURIComponent(id)}/reject`, {}),
  assignTeamTask: (payload: TeamAssignPayload) => post<{ taskID: string; agentID: string; status: string }>('/agents/tasks', payload),
  createTeamAgent: (payload: Partial<TeamAgent>) => post<TeamAgent>('/agents', payload),
  updateTeamAgent: (id: string, payload: Partial<TeamAgent>) => put<TeamAgent>(`/agents/${encodeURIComponent(id)}`, payload),
  deleteTeamAgent: (id: string) => del<{ id: string; deleted: boolean }>(`/agents/${encodeURIComponent(id)}`, {}),
  teamTriggers: () => get<TriggerEvent[]>('/agents/triggers'),
  evaluateTrigger: (triggerType: string, instruction?: string) =>
    post<{ status: string; triggerType: string }>('/agents/triggers/evaluate', { triggerType, instruction }),

  // Mind-thoughts greeting flow (phase 5/6). pendingGreetings returns the
  // count for the sidebar dot; triggerGreeting drains the queue into a
  // live assistant message on the current conversation.
  pendingGreetings: () => get<{ count: number }>('/chat/pending-greetings'),
  triggerGreeting: (conversationID?: string) =>
    post<{ delivered: boolean; conversationID?: string; messageID?: string; content?: string; thoughtIDs?: string[]; skipped?: string }>(
      '/chat/greeting',
      conversationID ? { conversationID } : {},
    ),
  mindThoughts: () =>
    get<{ count: number; thoughts: unknown[] | null }>('/mind/thoughts'),
  skills: () => get<SkillRecord[]>('/skills'),
  capabilities: () => get<CapabilityRecord[]>('/capabilities'),
  enableSkill: (id: string) => post<SkillRecord>(`/skills/${encodeURIComponent(id)}/enable`, {}),
  disableSkill: (id: string) => post<SkillRecord>(`/skills/${encodeURIComponent(id)}/disable`, {}),
  validateSkill: (id: string) => post<SkillRecord>(`/skills/${encodeURIComponent(id)}/validate`, {}),
  customSkills: () => get<unknown[]>('/skills/custom'),
  installCustomSkill: (path: string) => post<{ id: string; path: string; message: string }>('/skills/install', { path }),
  removeCustomSkill: (id: string) => request<{ id: string; removed: boolean }>(`/skills/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  fsRoots: () => get<FsRoot[]>('/skills/file-system/roots'),
  addFsRoot: (path: string) => post<FsRoot>('/skills/file-system/roots', { path }),
  removeFsRoot: (id: string) => post<FsRoot[]>(`/skills/file-system/roots/${encodeURIComponent(id)}/remove`, {}),
  pickFsFolder: () => post<{ path: string }>('/skills/file-system/pick-folder', {}),
  actionPolicies: () => get<Record<string, string>>('/action-policies'),
  setActionPolicy: (actionID: string, policy: string) =>
    put<Record<string, string>>(`/action-policies/${encodeURIComponent(actionID)}`, { policy }),
  memories: (params?: MemoryParams) =>
    get<MemoryItem[]>('/memories', params as Record<string, string | number | undefined>),
  searchMemories: (query: string) => get<MemoryItem[]>('/memories/search', { query }),
  deleteMemory: (id: string) => post<MemoryItem>(`/memories/${id}/delete`, {}),
  logs: (limit = 100) => get<LogEntry[]>(`/logs?limit=${limit}`),

  // MIND.md
  mind: () => get<{ content: string }>('/mind'),
  updateMind: (content: string) => put<object>('/mind', { content }),
  regenerateMind: () => post<{ content: string }>('/mind/regenerate', {}),
  forceDream: () => post<{ status: string }>('/mind/dream', {}),
  dreamState: () => get<{ running: boolean }>('/mind/dream'),

  // Diary
  diary: () => get<{ content: string }>('/diary'),

  // Model selector
  models: () => get<ModelSelectorInfo>('/models'),
  modelsForProvider: (provider: string, refresh = false) =>
    get<ModelSelectorInfo>('/models/available', {
      provider,
      ...(refresh ? { refresh: 1, _t: Date.now() } : {}),
    }),
  refreshModels: () => post<ModelSelectorInfo>('/models/refresh', {}),
  openRouterModels: (refresh = false, limit = 25) => get<ModelSelectorInfo>(
    '/providers/openrouter/models',
    { limit, ...(refresh ? { refresh: 1, _t: Date.now() } : {}) },
  ),
  cloudModelHealth: (provider: string, model: string) =>
    get<CloudModelHealth>('/providers/cloud/model-health', { provider, model, _t: Date.now() }),

  // Storage
  getStorageStats: () => get<StorageStats>('/storage/stats'),
  clearStorageFiles: () => del<void>('/storage/files', undefined),
  openStorageFolder: () => post<void>('/storage/open-folder', {}),

  // Communications
  communications: () => get<CommunicationsSnapshot>('/communications'),
  communicationSetupValues: (platform: string) => get<CommunicationSetupValues>(`/communications/platforms/${encodeURIComponent(platform)}/setup`),
  communicationChannels: () => get<CommunicationChannel[]>('/communications/channels'),
  updateCommunicationPlatform: (platform: string, enabled: boolean) =>
    put<CommunicationPlatformStatus>(`/communications/platforms/${encodeURIComponent(platform)}`, { enabled }),
  validateCommunicationPlatform: (platform: string, payload?: CommunicationValidationPayload) =>
    post<CommunicationPlatformStatus>(`/communications/platforms/${encodeURIComponent(platform)}/validate`, payload ?? {}),

  // Automations (Gremlins)
  automations: () => get<GremlinItem[]>('/automations'),
  automationSummaries: () => get<AutomationSummary[]>('/automations/summaries'),
  automationsAdvancedFile: () => get<{ content: string }>('/automations/advanced/file'),
  importAutomationsAdvancedFile: (content: string) => put<object>('/automations/advanced/import', { content }),
  createAutomation: (item: Omit<GremlinItem, 'id'> & { id?: string }) =>
    post<GremlinItem>('/automations', item),
  updateAutomation: (item: GremlinItem) =>
    put<GremlinItem>(`/automations/${item.id}`, item),
  deleteAutomation: (id: string) => request<object>(`/automations/${id}`, { method: 'DELETE' }),
  enableAutomation: (id: string) => post<GremlinItem>(`/automations/${id}/enable`, {}),
  disableAutomation: (id: string) => post<GremlinItem>(`/automations/${id}/disable`, {}),
  runAutomationNow: (id: string) => post<GremlinRun>(`/automations/${id}/run`, {}),
  automationRuns: (id: string) => get<GremlinRun[]>(`/automations/${id}/runs`),
  workflows: () => get<WorkflowDefinition[]>('/workflows'),
  workflowSummaries: () => get<WorkflowSummary[]>('/workflows/summaries'),
  workflow: (id: string) => get<WorkflowDefinition>(`/workflows/${encodeURIComponent(id)}`),
  createWorkflow: (definition: WorkflowDefinition) => post<WorkflowDefinition>('/workflows', definition),
  updateWorkflow: (definition: WorkflowDefinition) => put<WorkflowDefinition>(`/workflows/${encodeURIComponent(definition.id)}`, definition),
  deleteWorkflow: (id: string) => request<WorkflowDefinition>(`/workflows/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  runWorkflow: (id: string, inputValues?: Record<string, string>) =>
    post<WorkflowRun>(`/workflows/${encodeURIComponent(id)}/run`, { inputValues }),
  workflowRuns: (id?: string) => get<WorkflowRun[]>(id ? `/workflows/${encodeURIComponent(id)}/runs` : '/workflows/runs'),
  approveWorkflowRun: (runID: string) => post<WorkflowRun>(`/workflows/runs/${encodeURIComponent(runID)}/approve`, {}),
  denyWorkflowRun: (runID: string) => post<WorkflowRun>(`/workflows/runs/${encodeURIComponent(runID)}/deny`, {}),

  // Dashboards (v2) — agents are the primary author; UI is viewer-only.
  dashboards: (status?: DashboardStatus) =>
    get<DashboardSummary[]>(status ? `/dashboards?status=${encodeURIComponent(status)}` : '/dashboards'),
  dashboard: (id: string) =>
    get<DashboardDefinition>(`/dashboards/${encodeURIComponent(id)}`),
  deleteDashboard: (id: string) =>
    request<void>(`/dashboards/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  resolveDashboardWidget: (dashboardID: string, widgetID: string) =>
    post<DashboardWidgetData>(`/dashboards/${encodeURIComponent(dashboardID)}/resolve`, { widgetId: widgetID }),
  refreshDashboard: (id: string) =>
    post<DashboardRefreshEvent[]>(`/dashboards/${encodeURIComponent(id)}/refresh`, {}),
  streamDashboardEvents: (id: string) =>
    new EventSource(`${BASE()}/dashboards/${encodeURIComponent(id)}/events`),

  // Conversation History
  conversations: (limit = 50, offset = 0) =>
    get<ConversationSummary[]>('/conversations', { limit, offset }),
  searchConversations: (query: string, limit = 50) =>
    get<ConversationSummary[]>('/conversations/search', { query, limit }),
  conversationDetail: (id: string) =>
    get<ConversationDetail>(`/conversations/${encodeURIComponent(id)}`),
  clearAllConversations: () => request<void>('/conversations', { method: 'DELETE' }),

  // Link Preview
  fetchLinkPreview: (url: string) => get<LinkPreview>('/link-preview', { url }),

  // Forge
  forgeResearching: () => get<ForgeResearchingItem[]>('/forge/researching'),
  forgeProposals: () => get<ForgeProposalRecord[]>('/forge/proposals'),
  forgeInstalled: () => get<SkillRecord[]>('/forge/installed'),
  forgeInstall: (id: string) => post<ForgeProposalRecord>(`/forge/proposals/${id}/install`, {}),
  forgeInstallEnable: (id: string) => post<ForgeProposalRecord>(`/forge/proposals/${id}/install-enable`, {}),
  forgeReject: (id: string) => post<ForgeProposalRecord>(`/forge/proposals/${id}/reject`, {}),
  forgeUninstall: (skillID: string) => post<{ skillID: string; uninstalled: boolean }>(`/forge/installed/${encodeURIComponent(skillID)}/uninstall`, {}),

  // Location
  location: () => get<{ city: string; country: string; timezone: string; latitude: number; longitude: number; source: string; updatedAt: string }>('/location'),
  setLocation: (city: string, country: string) => put<{ city: string; country: string; timezone: string; source: string; updatedAt: string }>('/location', { city, country }),
  detectLocation: () => post<{ city: string; country: string; timezone: string; source: string; updatedAt: string }>('/location/detect', {}),
  setLocationFromCoords: (latitude: number, longitude: number) => post<{ city: string; country: string; timezone: string; source: string; updatedAt: string }>('/location/coords', { latitude, longitude }),

  // Preferences
  preferences: () => get<{ temperatureUnit: string; currency: string; unitSystem: string }>('/preferences'),
  setPreferences: (p: { temperatureUnit?: string; currency?: string; unitSystem?: string }) => put<{ temperatureUnit: string; currency: string; unitSystem: string }>('/preferences', p),

  // Remote access
  remoteAccessStatus: () => get<{ remoteAccessEnabled: boolean; port: number; lanIP: string | null; httpsReady: boolean; accessURL: string | null; tailscaleEnabled: boolean; tailscaleIP: string | null; tailscaleURL: string | null; tailscaleConnected: boolean }>('/auth/remote-status'),
  remoteAccessKey: () => get<{ key: string }>('/auth/remote-key'),
  revokeRemoteSessions: () => del<{ revoked: boolean }>('/auth/remote-sessions', {}),
  restartAtlas: () => post<{ accepted: boolean; message?: string }>('/control/restart', {}),

  // Engine LM
  engineStatus: () => get<EngineStatus>('/engine/status'),
  engineModels: () => get<EngineModelInfo[]>('/engine/models'),
  engineStart: (model: string, port?: number, ctxSize?: number) => post<EngineStatus>('/engine/start', { model, port, ctxSize }),
  engineStop: () => post<EngineStatus>('/engine/stop', {}),
  engineDeleteModel: (name: string) => request<EngineModelInfo[]>(`/engine/models/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  engineDownloadStatus: () => get<EngineDownloadStatus>('/engine/models/download/status'),
  engineDismissDownload: () => request<void>('/engine/models/download', { method: 'DELETE' }),
  engineDownloadModel: (url: string, filename: string): EventSource =>
    // POST body can't be sent via EventSource — use a query-string shim via a GET
    // that the server won't support. Instead we open an SSE POST via fetch in the
    // component using a raw fetch+ReadableStream. This entry point is a factory
    // helper for the base URL so components don't need to import BASE directly.
    new EventSource(`${BASE()}/engine/models/download`),
  engineDownloadBaseURL: () => BASE(),
  engineUpdateBaseURL: () => BASE(),

  // Tool Router (Phase 3)
  engineRouterStatus: () => get<EngineStatus>('/engine/router/status'),
  engineRouterStart: (model?: string) => post<EngineStatus>('/engine/router/start', { model }),
  engineRouterStop: () => post<EngineStatus>('/engine/router/stop', {}),

  // MLX-LM (Apple Silicon only)
  mlxStatus: () => get<MLXStatus>('/engine/mlx/status'),
  mlxModels: () => get<MLXModelInfo[]>('/engine/mlx/models'),
  mlxStart: (model: string, port?: number, ctxSize?: number) =>
    post<MLXStatus>('/engine/mlx/start', { model, port, ctxSize }),
  mlxStop: () => post<MLXStatus>('/engine/mlx/stop', {}),
  mlxDeleteModel: (name: string) =>
    request<MLXModelInfo[]>(`/engine/mlx/models/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  mlxDownloadStatus: () => get<MLXDownloadStatus>('/engine/mlx/models/download/status'),
  mlxDismissDownload: () => request<void>('/engine/mlx/models/download', { method: 'DELETE' }),
  // SSE POST — components use fetch() directly; these return the base URL for construction.
  mlxDownloadBaseURL: () => BASE(),
  mlxInstallBaseURL: () => BASE(),
  mlxRouterStatus: () => get<MLXStatus>('/engine/mlx/router/status'),
  mlxRouterStart: (model?: string) => post<MLXStatus>('/engine/mlx/router/start', { model }),
  mlxRouterStop: () => post<MLXStatus>('/engine/mlx/router/stop', {}),

  // Usage & cost tracking
  usageSummary: (params?: { since?: string; until?: string; days?: number }) => {
    const q = new URLSearchParams()
    if (params?.since) q.set('since', params.since)
    if (params?.until) q.set('until', params.until)
    if (params?.days !== undefined) q.set('days', String(params.days))
    return get<TokenUsageSummary>(`/usage/summary?${q}`)
  },
  usageEvents: (params?: { since?: string; until?: string; provider?: string; model?: string; limit?: number }) => {
    const q = new URLSearchParams()
    if (params?.since) q.set('since', params.since)
    if (params?.until) q.set('until', params.until)
    if (params?.provider) q.set('provider', params.provider)
    if (params?.model) q.set('model', params.model)
    if (params?.limit !== undefined) q.set('limit', String(params.limit))
    return get<{ events: TokenUsageEvent[] }>(`/usage/events?${q}`)
  },
  deleteUsage: (before: string) => request<{ deleted: number }>(`/usage?before=${encodeURIComponent(before)}`, { method: 'DELETE' }),

  // Voice (Phase 1: Whisper STT)
  voiceStatus: () => get<VoiceStatus>('/voice/status'),
  voiceStartSession: (whisperModel?: string, whisperPort?: number) =>
    post<{ sessionID: string; status: VoiceStatus }>('/voice/session/start', { whisperModel, whisperPort }),
  voiceEndSession: () => post<VoiceStatus>('/voice/session/end', {}),
  voiceModels: (component: 'whisper' | 'kokoro') =>
    get<VoiceModelInfo[]>(`/voice/models/${component}`),
  voiceDeleteModel: (component: 'whisper' | 'kokoro', name: string) =>
    request<VoiceModelInfo[]>(`/voice/models/${component}/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  /**
   * Pre-warm the Kokoro subprocess so the next /voice/synthesize call doesn't
   * pay the ~600 ms cold-start cost. Idempotent.
   */
  voiceKokoroWarmup: () => post<{ ok: boolean; port: number }>('/voice/kokoro/warmup', {}),
  voiceWhisperUpdateBaseURL: () => BASE(),
  voiceKokoroUpdateBaseURL: () => BASE(),
  voiceVoices: (provider?: string) =>
    get<VoiceOption[]>('/voice/voices' + (provider ? `?provider=${encodeURIComponent(provider)}` : '')),
  voiceTranscribe: async (blob: Blob, language?: string): Promise<VoiceTranscribeResult> => {
    const form = new FormData()
    const ext = blob.type.includes('wav') ? 'wav' : 'webm'
    form.append('audio', blob, `audio.${ext}`)
    const q = language ? `?language=${encodeURIComponent(language)}` : ''
    const res = await fetch(`${BASE()}/voice/transcribe${q}`, { method: 'POST', body: form, credentials: 'include' })
    if (!res.ok) {
      if (res.status === 401 && typeof window !== 'undefined') {
        window.location.reload()
        throw new Error('Session expired — reloading')
      }
      const text = await res.text().catch(() => res.statusText)
      let message = text
      try { const j = JSON.parse(text); if (j?.error) message = j.error } catch { /* use raw */ }
      throw new Error(message || 'voice transcription failed')
    }
    return (await res.json()) as VoiceTranscribeResult
  },
  /**
   * Synthesize speech via /voice/synthesize and stream SSE audio chunks back.
   * Returns an object with an `abort()` method; the caller supplies handlers
   * for each chunk and the end-of-stream event.
   */
  voiceSynthesize: (
    text: string,
    handlers: {
      onChunk: (b64: string, index: number, sampleRate: number, final: boolean) => void
      onEnd: () => void
      onError?: (message: string) => void
    },
    voice?: string,
  ): { abort: () => void } => {
    const ctrl = new AbortController()
    void (async () => {
      try {
        const body: Record<string, unknown> = { text, ...(voice ? { voice } : {}) }
        const res = await fetch(`${BASE()}/voice/synthesize`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
          body: JSON.stringify(body),
          signal: ctrl.signal,
        })
        if (!res.ok || !res.body) {
          const msg = await res.text().catch(() => res.statusText)
          handlers.onError?.(msg || `voice synthesize HTTP ${res.status}`)
          return
        }
        const reader = res.body.getReader()
        const decoder = new TextDecoder('utf-8')
        let buf = ''
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          buf += decoder.decode(value, { stream: true })
          // SSE messages separated by blank line
          let idx: number
          while ((idx = buf.indexOf('\n\n')) >= 0) {
            const frame = buf.slice(0, idx)
            buf = buf.slice(idx + 2)
            let event = 'message'
            let data = ''
            for (const line of frame.split('\n')) {
              if (line.startsWith('event: ')) event = line.slice(7).trim()
              else if (line.startsWith('data: ')) data += line.slice(6)
            }
            if (!data) continue
            try {
              const parsed = JSON.parse(data)
              if (event === 'voice_audio' && typeof parsed.pcm === 'string') {
                handlers.onChunk(parsed.pcm, parsed.index ?? 0, parsed.sampleRate ?? 22050, !!parsed.final)
              } else if (event === 'voice_audio_end') {
                handlers.onEnd()
                return
              } else if (event === 'error') {
                handlers.onError?.(parsed.message || 'voice synthesize failed')
                return
              }
            } catch {
              // ignore malformed frame
            }
          }
        }
        // Stream ended without an explicit end event — still fire onEnd.
        handlers.onEnd()
      } catch (err) {
        if ((err as Error).name === 'AbortError') return
        handlers.onError?.((err as Error).message || 'voice synthesize failed')
      }
    })()
    return {
      abort() { ctrl.abort() },
    }
  },

  // ── Local auth ────────────────────────────────────────────────────────────

  localAuthStatus: () =>
    get<{ configured: boolean; authenticated: boolean; hasWebAuthn: boolean; hasPIN: boolean }>(
      '/auth/local/status'
    ),

  localAuthWebAuthnRegisterBegin: async (name: string) => {
    const { data, headers } = await requestWithHeaders<Record<string, unknown>>(
      '/auth/local/webauthn/register/begin',
      { method: 'POST', body: JSON.stringify({ name }) }
    )
    return { options: data, sessionId: headers.get('X-WebAuthn-Session') ?? '' }
  },

  localAuthWebAuthnRegisterFinish: async (
    sessionId: string,
    credName: string,
    credential: Record<string, unknown>
  ) => {
    await requestWithHeaders('/auth/local/webauthn/register/finish', {
      method: 'POST',
      body: JSON.stringify(credential),
      extraHeaders: { 'X-WebAuthn-Session': sessionId, 'X-Credential-Name': credName },
    })
  },

  localAuthWebAuthnAuthBegin: async () => {
    const { data, headers } = await requestWithHeaders<Record<string, unknown>>(
      '/auth/local/webauthn/authenticate/begin',
      { method: 'POST', body: '{}' }
    )
    return { options: data, sessionId: headers.get('X-WebAuthn-Session') ?? '' }
  },

  localAuthWebAuthnAuthFinish: async (sessionId: string, assertion: Record<string, unknown>) => {
    await requestWithHeaders('/auth/local/webauthn/authenticate/finish', {
      method: 'POST',
      body: JSON.stringify(assertion),
      extraHeaders: { 'X-WebAuthn-Session': sessionId },
    })
  },

  localAuthPINSetup: (pin: string) =>
    post<{ status: string }>('/auth/local/pin/setup', { pin }),

  localAuthPINVerify: (pin: string) =>
    post<{ status: string }>('/auth/local/pin/verify', { pin }),

  localAuthCredentials: () =>
    get<Array<{ id: string; type: string; name: string; createdAt: string; lastUsedAt: string }>>(
      '/auth/local/credentials'
    ),

  localAuthDeleteCredential: (id: string) =>
    del<void>(`/auth/local/credentials/${id}`, undefined),

  localAuthLogout: () =>
    post<{ status: string }>('/auth/local/logout', {}),
}
