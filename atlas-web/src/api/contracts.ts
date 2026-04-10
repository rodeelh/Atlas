export interface RuntimeStatus {
  isRunning: boolean
  activeConversationCount: number
  lastMessageAt?: string
  lastError?: string
  state: string
  runtimePort: number
  startedAt?: string
  activeRequests: number
  pendingApprovalCount: number
  details: string
  tokensIn?: number
  tokensOut?: number
  telegram?: {
    enabled: boolean
    connected: boolean
    pollingActive: boolean
    lastError?: string
  }
  communications?: CommunicationsSnapshot
}

export interface APIKeyStatus {
  openAIKeySet: boolean
  telegramTokenSet: boolean
  discordTokenSet: boolean
  slackBotTokenSet: boolean
  slackAppTokenSet: boolean
  braveSearchKeySet: boolean
  anthropicKeySet: boolean
  geminiKeySet: boolean
  openRouterKeySet: boolean
  lmStudioKeySet: boolean
  ollamaKeySet: boolean
  finnhubKeySet: boolean
  customKeys: string[]
  customKeyLabels: Record<string, string>
}

export interface RuntimeConfig {
  runtimePort: number
  onboardingCompleted: boolean
  telegramEnabled: boolean
  discordEnabled: boolean
  discordClientID: string
  slackEnabled: boolean
  telegramPollingTimeoutSeconds: number
  telegramPollingRetryBaseSeconds: number
  telegramCommandPrefix: string
  telegramAllowedUserIDs: number[]
  telegramAllowedChatIDs: number[]
  defaultOpenAIModel: string
  baseSystemPrompt: string
  maxAgentIterations: number
  conversationWindowLimit: number
  memoryEnabled: boolean
  maxRetrievedMemoriesPerTurn: number
  memoryAutoSaveThreshold: number
  personaName: string
  userName?: string
  actionSafetyMode: string
  activeImageProvider: string
  activeAIProvider: string
  lmStudioBaseURL: string
  selectedAnthropicModel: string
  selectedGeminiModel: string
  selectedOpenRouterModel: string
  selectedOpenAIPrimaryModel: string
  selectedOpenAIFastModel: string
  selectedAnthropicFastModel: string
  selectedGeminiFastModel: string
  selectedOpenRouterFastModel: string
  selectedLMStudioModel: string
  selectedLMStudioModelFast: string
  lmStudioContextWindowLimit: number
  lmStudioMaxAgentIterations: number
  ollamaBaseURL: string
  selectedOllamaModel: string
  selectedOllamaModelFast: string
  ollamaContextWindowLimit: number
  ollamaMaxAgentIterations: number
  atlasEnginePort: number
  selectedAtlasEngineModel: string
  selectedAtlasEngineModelFast: string
  atlasEngineContextWindowLimit: number
  atlasEngineMaxAgentIterations: number
  atlasEngineCtxSize: number
  atlasEngineKVCacheQuant: string
  atlasEngineMlock: boolean
  atlasEngineRouterPort: number
  atlasEngineRouterModel: string
  atlasEngineRouterForAll: boolean
  atlasEngineDraftModel: string
  atlasMLXPort: number
  selectedAtlasMLXModel: string
  atlasMLXCtxSize: number
  atlasMLXRouterPort: number
  atlasMLXRouterModel: string
  atlasMLXRouterForAll: boolean
  selectedLocalEngine: string   // "atlas_engine" | "atlas_mlx"
  enableSmartToolSelection: boolean
  toolSelectionMode: string
  enableMultiAgentOrchestration: boolean
  maxParallelAgents: number
  workerMaxIterations: number
  remoteAccessEnabled: boolean
  tailscaleEnabled: boolean

  // Voice — Whisper STT + Kokoro TTS. All fields optional on the TS side so
  // older persisted configs don't fail to parse.
  voiceSTTEnabled?: boolean
  voiceTTSEnabled?: boolean
  voiceContinuousMode?: boolean
  voiceWhisperPort?: number
  voiceWhisperModel?: string
  voiceWhisperLanguage?: string
  voiceTTSAutoPlay?: boolean
  voiceSessionIdleSec?: number
  voiceKokoroPort?: number

  // Mind-thoughts — opt-in feature suite. thoughtsEnabled is the master
  // switch; when false the entire subsystem is dormant. napsEnabled is
  // the sub-flag that additionally turns on autonomous nap curation.
  // Both default to false on a fresh install.
  thoughtsEnabled?: boolean
  napsEnabled?: boolean
}

export interface RuntimeConfigUpdateResponse {
  config: RuntimeConfig
  restartRequired: boolean
}

export interface OnboardingStatus {
  completed: boolean
}

export interface MessageAttachment {
  filename: string
  mimeType: string
  data: string
}

export interface MessageResponse {
  conversation: {
    id: string
    messages: Array<{
      id: string
      role: 'user' | 'assistant'
      content: string
      timestamp: string
    }>
  }
  response: {
    assistantMessage?: string
    status: string
    errorMessage?: string
  }
}

export interface ChatStreamEvent {
  type:
    | 'assistant_started'
    | 'assistant_delta'
    | 'assistant_done'
    | 'tool_started'
    | 'tool_finished'
    | 'tool_failed'
    | 'approval_required'
    | 'done'
    | 'error'
    | 'token'
    | string
  content?: string
  role?: 'assistant' | 'user' | string
  conversationID?: string
  error?: string
  message?: string
  status?: string
  toolName?: string
  approvalID?: string
  toolCallID?: string
  arguments?: string
}

export interface ApprovalToolCall {
  id: string
  toolName: string
  argumentsJSON: string
  permissionLevel: 'read' | 'draft' | 'execute' | string
  requiresApproval: boolean
  status?: string
  timestamp?: string
}

export interface Approval {
  id: string
  status: 'pending' | 'approved' | 'denied' | string
  /**
   * Where this approval came from. Omitted for agent-initiated approvals
   * (the default). Set to "thought" when the mind-thoughts dispatcher
   * routed a thought-sourced action through the approvals flow.
   */
  source?: 'thought' | string
  conversationID?: string
  createdAt: string
  resolvedAt?: string
  deferredExecutionID?: string
  deferredExecutionStatus?: string
  lastError?: string
  previewDiff?: string
  toolCall: ApprovalToolCall
}

export interface FsRoot {
  id: string
  path: string
}

export interface SkillRecord {
  manifest: {
    id: string
    name: string
    version: string
    description: string
    lifecycleState: string
    riskLevel: string
    isUserVisible: boolean
    category?: string
    source?: string
    capabilities: string[]
    tags: string[]
  }
  actions: Array<{
    id: string
    name: string
    description: string
    permissionLevel: string
    approvalPolicy: string
    isEnabled: boolean
  }>
  validation?: {
    skillID: string
    status: string
    summary: string
    isValid: boolean
    issues: string[]
    validatedAt: string
  }
}

export interface MemoryItem {
  id: string
  category: string
  title: string
  content: string
  source?: string
  confidence: number
  importance: number
  isUserConfirmed: boolean
  isSensitive: boolean
  tags: string[]
  createdAt: string
  updatedAt: string
}

export interface MemoryParams {
  category?: string
  limit?: number
}

export interface LogEntry {
  id: string
  level: string
  message: string
  timestamp: string
  metadata?: Record<string, string>
}

export interface TelegramChat {
  chatID: number
  userID?: number
  activeConversationID: string
  createdAt: string
  updatedAt: string
  lastTelegramMessageID?: number
}

export interface CommunicationDestination {
  id: string
  platform: 'telegram' | 'discord' | 'slack' | 'whatsapp' | 'companion'
  channelID: string
  channelName?: string
  userID?: string
  threadID?: string
}

export interface CommunicationChannel {
  id: string
  platform: 'telegram' | 'discord' | 'slack' | 'whatsapp' | 'companion'
  channelID: string
  channelName?: string
  userID?: string
  threadID?: string
  activeConversationID: string
  createdAt: string
  updatedAt: string
  lastMessageID?: string
  canReceiveNotifications: boolean
}

export interface CommunicationPlatformStatus {
  id: string
  platform: 'telegram' | 'discord' | 'slack' | 'whatsapp' | 'companion'
  enabled: boolean
  connected: boolean
  available: boolean
  setupState: 'not_started' | 'missing_credentials' | 'partial_setup' | 'validation_failed' | 'ready'
  statusLabel: string
  connectedAccountName?: string
  credentialConfigured: boolean
  blockingReason?: string
  requiredCredentials: string[]
  lastError?: string
  lastUpdatedAt?: string
  metadata: Record<string, string>
}

export interface CommunicationsSnapshot {
  platforms: CommunicationPlatformStatus[]
  channels: CommunicationChannel[]
}

export interface CommunicationValidationPayload {
  credentials?: Record<string, string>
  config?: {
    discordClientID?: string
  }
}

export interface CommunicationSetupValues {
  values: Record<string, string>
}

export interface GremlinItem {
  id: string
  name: string
  emoji: string
  prompt: string
  scheduleRaw: string
  isEnabled: boolean
  sourceType: string
  createdAt: string
  workflowID?: string
  workflowInputValues?: Record<string, string>
  nextRunAt?: string
  lastRunAt?: string
  lastRunStatus?: string
  telegramChatID?: number
  communicationDestination?: CommunicationDestination
}

export interface AutomationSummary {
  id: string
  name: string
  emoji: string
  prompt: string
  scheduleRaw: string
  isEnabled: boolean
  sourceType: string
  createdAt: string
  communicationDestination?: CommunicationDestination
  lastRunAt?: string
  lastRunStatus?: string
  lastRunError?: string
  nextRunAt?: string
  health: string
  deliveryHealth: string
  destinationLabel?: string
}

export type ForgeProposalStatus = 'pending' | 'installed' | 'enabled' | 'rejected' | 'uninstalled'

export interface ForgeProposalRecord {
  id: string
  skillID: string
  name: string
  description: string
  summary: string
  rationale?: string
  requiredSecrets: string[]
  domains: string[]
  actionNames: string[]
  riskLevel: string
  status: ForgeProposalStatus
  specJSON: string
  plansJSON: string
  contractJSON?: string
  createdAt: string
  updatedAt: string
}

export interface ForgeResearchingItem {
  id: string
  title: string
  message: string
  startedAt: string
}

// ── Engine LM (llama.cpp) ────────────────────────────────────────────────

export interface EngineStatus {
  running: boolean
  loading?: boolean
  loadedModel: string
  port: number
  binaryReady: boolean
  buildVersion?: string
  lastError?: string
  lastTPS?: number
  promptTPS?: number
  genTimeSec?: number
  activeRequests?: number
  contextTokens?: number
}

export interface EngineModelInfo {
  name: string
  sizeBytes: number
  sizeHuman: string
}

export interface EngineDownloadProgress {
  downloaded: number
  total: number
  percent: number
}

export interface EngineDownloadStatus {
  active: boolean
  filename: string
  url?: string
  downloaded: number
  total: number
  percent: number
}

// ── MLX-LM ───────────────────────────────────────────────────────────────────

export interface MLXInferenceStats {
  decodeTPS: number
  promptTokens: number
  completionTokens: number
  generationSec: number
}

export interface MLXStatus {
  running: boolean
  loading?: boolean
  loadedModel: string
  port: number
  venvReady: boolean
  packageVersion?: string        // installed mlx-lm version
  latestVersion?: string         // latest version on PyPI (source: https://pypi.org/project/mlx-lm/)
  lastError?: string
  isAppleSilicon: boolean
  lastInference?: MLXInferenceStats  // stats from last completed turn
}

export interface MLXModelInfo {
  name: string       // directory name, e.g. "Llama-3.2-3B-Instruct-4bit"
  sizeBytes: number
  sizeHuman: string
}

export interface MLXDownloadStatus {
  active: boolean
  repo: string       // HuggingFace repo ID — NOT a direct URL
  modelName: string  // derived directory name (last segment of repo)
  downloaded: number
  total: number
  percent: number
  error?: string
}

// ── Model Selector ────────────────────────────────────────────────────────────

export interface AIModelRecord {
  id: string
  displayName: string
  isFast: boolean
}

export interface ModelSelectorInfo {
  primaryModel?: string
  fastModel?: string
  lastRefreshedAt?: string
  availableModels?: AIModelRecord[]
  totalAvailable?: number
  hasMore?: boolean
  providerStatus?: ProviderStatusInfo
}

export interface ProviderStatusInfo {
  state: string
  label: string
  tone: 'green' | 'yellow' | 'red' | 'neutral' | string
  message: string
  checkedAt: string
}

export interface OpenRouterModelHealth {
  status: 'ok' | 'rate_limited' | 'warning' | 'missing_key' | 'unavailable' | 'unknown' | string
  message: string
  checkedAt: string
}

export interface CloudModelHealth {
  status: 'ok' | 'rate_limited' | 'warning' | 'missing_key' | 'unavailable' | 'unknown' | string
  message: string
  checkedAt: string
}

export interface GremlinRun {
  id: string
  gremlinID: string
  startedAt: string
  finishedAt?: string
  status: 'success' | 'failed' | 'running' | 'skipped' | string
  output?: string
  errorMessage?: string
  conversationID?: string
  workflowRunID?: string
}

export interface WorkflowTrustScope {
  approvedRootPaths: string[]
  allowedApps: string[]
  allowsSensitiveRead: boolean
  allowsLiveWrite: boolean
}

export interface WorkflowStep {
  id: string
  title: string
  kind: 'skill_action' | 'prompt' | string
  skillID?: string
  actionID?: string
  inputJSON?: string
  prompt?: string
  appName?: string
  targetPath?: string
  sideEffectLevel?: string
}

export interface WorkflowDefinition {
  id: string
  name: string
  description: string
  promptTemplate: string
  tags: string[]
  steps: WorkflowStep[]
  trustScope: WorkflowTrustScope
  approvalMode: 'workflow_boundary' | 'step_by_step' | string
  createdAt: string
  updatedAt: string
  sourceConversationID?: string
  isEnabled: boolean
}

export interface WorkflowSummary {
  id: string
  name: string
  description: string
  isEnabled: boolean
  stepCount: number
  health: string
  lastRunAt?: string
  lastRunStatus?: string
  lastRunError?: string
}

export interface WorkflowApproval {
  id: string
  workflowID: string
  workflowRunID: string
  status: 'pending' | 'approved' | 'denied' | string
  reason: string
  requestedAt: string
  resolvedAt?: string
  trustScope: WorkflowTrustScope
}

export interface WorkflowStepRun {
  id: string
  stepID: string
  title: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'waiting_for_approval' | 'skipped' | string
  output?: string
  errorMessage?: string
  startedAt?: string
  finishedAt?: string
}

export interface WorkflowRun {
  id: string
  workflowID: string
  workflowName: string
  status: 'pending' | 'running' | 'waiting_for_approval' | 'completed' | 'failed' | 'denied' | string
  outcome?: 'success' | 'failed' | 'waiting_for_approval' | 'denied' | string
  inputValues: Record<string, string>
  stepRuns: WorkflowStepRun[]
  approval?: WorkflowApproval
  assistantSummary?: string
  errorMessage?: string
  startedAt: string
  finishedAt?: string
  conversationID?: string
}

export interface ConversationSummary {
  id: string
  messageCount: number
  firstUserMessage?: string
  lastAssistantMessage?: string
  createdAt: string
  updatedAt: string
  platform: string
  platformContext?: string
}

export interface ConversationMessage {
  id: string
  role: 'user' | 'assistant' | 'system' | 'tool'
  content: string
  timestamp: string
}

export interface ConversationDetail extends ConversationSummary {
  messages: ConversationMessage[]
}

export interface LinkPreview {
  url: string
  title?: string
  description?: string
  imageURL?: string
  domain?: string
}

export interface TokenUsageSummary {
  totalInputTokens: number
  totalOutputTokens: number
  totalTokens: number
  totalCostUSD: number
  turnCount: number
  byModel: ModelUsageBreakdown[]
  dailySeries: DailyUsageSeries[]
}

export interface ModelUsageBreakdown {
  provider: string
  model: string
  inputTokens: number
  outputTokens: number
  totalTokens: number
  totalCostUSD: number
  turnCount: number
}

export interface DailyUsageSeries {
  date: string
  inputTokens: number
  outputTokens: number
  totalTokens: number
  costUSD: number
  turnCount: number
}

export interface TokenUsageEvent {
  id: string
  conversationId: string
  provider: string
  model: string
  inputTokens: number
  outputTokens: number
  inputCostUSD: number
  outputCostUSD: number
  totalCostUSD: number
  recordedAt: string
}

// ── Voice (Phase 1: Whisper STT; Phase 2 reserved for Piper TTS) ─────────────

export interface VoiceStatus {
  sessionActive: boolean
  sessionID?: string
  sessionStartedUnix?: number
  whisperRunning: boolean
  whisperReady: boolean
  whisperPort: number
  whisperModel?: string
  whisperBuildTag?: string
  kokoroRunning: boolean
  kokoroReady: boolean
  kokoroPort: number
  lastError?: string
}

export interface VoiceModelInfo {
  name: string
  component: 'whisper' | 'kokoro'
  sizeBytes: number
  sizeHuman: string
}

export interface VoiceTranscribeResult {
  text: string
  language?: string
  duration?: number
  sessionID?: string
}

export interface VoiceSynthesizeChunkEvent {
  index: number
  text: string
  final: boolean
  chunk: string // base64 WAV
}

// ── Dashboards ──────────────────────────────────────────────────────────────

export type DashboardSourceKind = 'runtime' | 'skill' | 'web' | 'sql'

export type DashboardWidgetKind =
  | 'metric'
  | 'table'
  | 'line_chart'
  | 'bar_chart'
  | 'markdown'
  | 'list'
  | 'news'
  | 'custom_html'

export interface DashboardDataSource {
  kind: DashboardSourceKind
  endpoint?: string
  query?: Record<string, string>
  action?: string
  args?: Record<string, unknown>
  url?: string
  sql?: string
}

export interface DashboardWidget {
  id: string
  kind: DashboardWidgetKind
  title?: string
  description?: string
  gridX: number
  gridY: number
  gridW: number
  gridH: number
  source?: DashboardDataSource
  options?: Record<string, unknown>
  html?: string
  css?: string
  js?: string
  refreshIntervalSeconds?: number
}

export interface DashboardDefinition {
  id: string
  name: string
  description?: string
  template?: string
  widgets: DashboardWidget[]
  createdAt: string
  updatedAt: string
}

export interface DashboardSummary {
  id: string
  name: string
  description?: string
  template?: string
  widgetCount: number
  createdAt: string
  updatedAt: string
}

export interface DashboardTemplate {
  id: string
  name: string
  description: string
  definition: DashboardDefinition
}

export interface DashboardWidgetData {
  widgetId: string
  success: boolean
  data?: unknown
  error?: string
  resolvedAt: string
  sourceKind: string
  durationMs: number
}
