// Atlas HTTP API client
import type {
  APIKeyStatus,
  Approval,
  EngineDownloadStatus,
  EngineModelInfo,
  EngineStatus,
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
  LinkPreview,
  LogEntry,
  MemoryItem,
  MemoryParams,
  MessageAttachment,
  MessageResponse,
  ModelSelectorInfo,
  CloudModelHealth,
  OpenRouterModelHealth,
  OnboardingStatus,
  RuntimeConfig,
  RuntimeConfigUpdateResponse,
  RuntimeStatus,
  SkillRecord,
  TelegramChat,
  WorkflowDefinition,
  WorkflowSummary,
  WorkflowRun,
  TokenUsageSummary,
  TokenUsageEvent,
  VoiceStatus,
  VoiceModelInfo,
  VoiceTranscribeResult,
} from './contracts'

export type {
  AIModelRecord,
  APIKeyStatus,
  EngineModelInfo,
  EngineStatus,
  Approval,
  FsRoot,
  AutomationSummary,
  ApprovalToolCall,
  CommunicationChannel,
  CommunicationDestination,
  CommunicationsSnapshot,
  CommunicationPlatformStatus,
  CommunicationSetupValues,
  CommunicationValidationPayload,
  ConversationDetail,
  ConversationMessage,
  ConversationSummary,
  ForgeProposalRecord,
  ForgeProposalStatus,
  ForgeResearchingItem,
  GremlinItem,
  GremlinRun,
  LinkPreview,
  LogEntry,
  MemoryItem,
  MemoryParams,
  MessageAttachment,
  MessageResponse,
  ModelSelectorInfo,
  CloudModelHealth,
  OpenRouterModelHealth,
  OnboardingStatus,
  RuntimeConfig,
  RuntimeConfigUpdateResponse,
  RuntimeStatus,
  SkillRecord,
  TelegramChat,
  WorkflowApproval,
  WorkflowDefinition,
  WorkflowSummary,
  WorkflowRun,
  WorkflowStep,
  WorkflowStepRun,
  WorkflowTrustScope,
  VoiceStatus,
  VoiceModelInfo,
  VoiceTranscribeResult,
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
  })
  if (!res.ok) {
    // On remote device, a 401 means the session expired — redirect to root.
    // Root will route unauthenticated users to /auth/remote-gate.
    if (res.status === 401 && typeof window !== 'undefined' &&
        window.location.hostname !== 'localhost' && window.location.hostname !== '127.0.0.1') {
      window.location.href = `${window.location.origin}/`
      csrfTokenCache = null
      throw new Error('Session expired — redirecting to login')
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
  streamMessage: (conversationID: string) =>
    new EventSource(`${BASE()}/message/stream?conversationID=${encodeURIComponent(conversationID)}`),
  approvals: () => get<Approval[]>('/approvals'),
  // approve/deny take the toolCall.id (toolCallID), not the approval.id
  approve: (toolCallID: string) => post<Approval>(`/approvals/${toolCallID}/approve`, {}),
  deny: (toolCallID: string) => post<Approval>(`/approvals/${toolCallID}/deny`, {}),
  skills: () => get<SkillRecord[]>('/skills'),
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
  openRouterModelHealth: (model: string) =>
    get<OpenRouterModelHealth>('/providers/openrouter/model-health', { model, _t: Date.now() }),
  cloudModelHealth: (provider: string, model: string) =>
    get<CloudModelHealth>('/providers/cloud/model-health', { provider, model, _t: Date.now() }),

  // Telegram
  telegramChats: () => get<TelegramChat[]>('/telegram/chats'),
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
  automationsFile: () => get<{ content: string }>('/automations/advanced/file'),
  writeAutomationsFile: (content: string) => put<object>('/automations/advanced/import', { content }),
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

  // Preferences
  preferences: () => get<{ temperatureUnit: string; currency: string; unitSystem: string }>('/preferences'),
  setPreferences: (p: { temperatureUnit?: string; currency?: string; unitSystem?: string }) => put<{ temperatureUnit: string; currency: string; unitSystem: string }>('/preferences', p),

  // Remote access
  remoteAccessStatus: () => get<{ remoteAccessEnabled: boolean; port: number; lanIP: string | null; accessURL: string | null; tailscaleEnabled: boolean; tailscaleIP: string | null; tailscaleURL: string | null; tailscaleConnected: boolean }>('/auth/remote-status'),
  remoteAccessKey: () => get<{ key: string }>('/auth/remote-key'),
  revokeRemoteSessions: () => del<{ revoked: boolean }>('/auth/remote-sessions', {}),

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
  voiceTranscribe: async (blob: Blob, language?: string): Promise<VoiceTranscribeResult> => {
    const form = new FormData()
    const ext = blob.type.includes('wav') ? 'wav' : 'webm'
    form.append('audio', blob, `audio.${ext}`)
    const q = language ? `?language=${encodeURIComponent(language)}` : ''
    const res = await fetch(`${BASE()}/voice/transcribe${q}`, { method: 'POST', body: form })
    if (!res.ok) {
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
  ): { abort: () => void } => {
    const ctrl = new AbortController()
    void (async () => {
      try {
        const body: Record<string, unknown> = { text }
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
}
