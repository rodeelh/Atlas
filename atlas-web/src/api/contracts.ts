export interface RuntimeStatus {
  isRunning: boolean;
  activeConversationCount: number;
  lastMessageAt?: string;
  lastError?: string;
  state: string;
  runtimePort: number;
  startedAt?: string;
  activeRequests: number;
  pendingApprovalCount: number;
  details: string;
  tokensIn?: number;
  tokensOut?: number;
  telegram?: {
    enabled: boolean;
    connected: boolean;
    pollingActive: boolean;
    lastError?: string;
  };
  communications?: CommunicationsSnapshot;
}

export interface APIKeyStatus {
  openAIKeySet: boolean;
  telegramTokenSet: boolean;
  discordTokenSet: boolean;
  slackBotTokenSet: boolean;
  slackAppTokenSet: boolean;
  braveSearchKeySet: boolean;
  anthropicKeySet: boolean;
  geminiKeySet: boolean;
  openRouterKeySet: boolean;
  lmStudioKeySet: boolean;
  ollamaKeySet: boolean;
  finnhubKeySet: boolean;
  elevenLabsKeySet: boolean;
  customKeys: string[];
  customKeyLabels: Record<string, string>;
}

export interface RuntimeConfig {
  runtimePort: number;
  onboardingCompleted: boolean;
  telegramEnabled: boolean;
  discordEnabled: boolean;
  discordClientID: string;
  slackEnabled: boolean;
  telegramPollingTimeoutSeconds: number;
  telegramPollingRetryBaseSeconds: number;
  telegramCommandPrefix: string;
  telegramAllowedUserIDs: number[];
  telegramAllowedChatIDs: number[];
  defaultOpenAIModel: string;
  baseSystemPrompt: string;
  maxAgentIterations: number;
  conversationWindowLimit: number;
  memoryEnabled: boolean;
  maxRetrievedMemoriesPerTurn: number;
  memoryAutoSaveThreshold: number;
  personaName: string;
  userName?: string;
  actionSafetyMode: string;
  activeImageProvider: string;
  activeAIProvider: string;
  lmStudioBaseURL: string;
  selectedAnthropicModel: string;
  selectedGeminiModel: string;
  selectedOpenRouterModel: string;
  selectedOpenAIPrimaryModel: string;
  selectedOpenAIFastModel: string;
  selectedAnthropicFastModel: string;
  selectedGeminiFastModel: string;
  selectedOpenRouterFastModel: string;
  selectedOpenAIImageModel?: string;
  selectedGeminiImageModel?: string;
  selectedLMStudioModel: string;
  selectedLMStudioModelFast: string;
  lmStudioContextWindowLimit: number;
  lmStudioMaxAgentIterations: number;
  ollamaBaseURL: string;
  selectedOllamaModel: string;
  selectedOllamaModelFast: string;
  ollamaContextWindowLimit: number;
  ollamaMaxAgentIterations: number;
  atlasEnginePort: number;
  selectedAtlasEngineModel: string;
  selectedAtlasEngineModelFast: string;
  atlasEngineContextWindowLimit: number;
  atlasEngineMaxAgentIterations: number;
  atlasEngineCtxSize: number;
  atlasEngineKVCacheQuant: string;
  atlasEngineMlock: boolean;
  atlasEngineRouterPort: number;
  atlasEngineRouterModel: string;
  atlasEngineRouterForAll: boolean;
  atlasEngineDraftModel: string;
  atlasEmbedEnabled: boolean;
  atlasEmbedPort: number;
  atlasEmbedModel: string;
  atlasMLXPort: number;
  selectedAtlasMLXModel: string;
  atlasMLXCtxSize: number;
  atlasMLXRouterPort: number;
  atlasMLXRouterModel: string;
  atlasMLXRouterForAll: boolean;
  atlasMLXTemperature: number;
  atlasMLXTopP: number;
  atlasMLXMinP: number;
  atlasMLXRepetitionPenalty: number;
  atlasMLXThinkingEnabled: boolean;
  atlasMLXChatTemplateArgs: string;
  selectedLocalEngine: string; // "atlas_engine" | "atlas_mlx"
  enableSmartToolSelection: boolean;
  toolSelectionMode: string;
  enableMultiAgentOrchestration: boolean;
  maxParallelAgents: number;
  workerMaxIterations: number;
  remoteAccessEnabled: boolean;
  tailscaleEnabled: boolean;

  // Voice — Whisper STT + Kokoro TTS. All fields optional on the TS side so
  // older persisted configs don't fail to parse.
  voiceSTTEnabled?: boolean;
  voiceTTSEnabled?: boolean;
  voiceContinuousMode?: boolean;
  voiceWhisperPort?: number;
  voiceWhisperModel?: string;
  voiceWhisperLanguage?: string;
  voiceTTSAutoPlay?: boolean;
  voiceSessionIdleSec?: number;
  voiceKokoroPort?: number;
  voiceKokoroVoice?: string;

  // Audio provider — "local" | "openai" | "gemini"
  activeAudioProvider?: string;
  audioSTTModel?: string;
  audioSTTLanguage?: string;
  audioTTSModel?: string;
  audioTTSVoice?: string;
  audioTTSSpeed?: number;
  audioTTSStylePrompt?: string;

  // Mind-thoughts — opt-in feature suite. thoughtsEnabled is the master
  // switch; when false the entire subsystem is dormant. napsEnabled is
  // the sub-flag that additionally turns on autonomous nap curation.
  // Both default to false on a fresh install.
  thoughtsEnabled?: boolean;
  napsEnabled?: boolean;
}

export interface RuntimeConfigUpdateResponse {
  config: RuntimeConfig;
  restartRequired: boolean;
}

export interface OnboardingStatus {
  completed: boolean;
}

export interface MessageAttachment {
  filename: string;
  mimeType: string;
  data: string;
}

export interface MessageResponse {
  conversation: {
    id: string;
    messages: Array<{
      id: string;
      role: "user" | "assistant";
      content: string;
      timestamp: string;
      blocks?: Array<Record<string, unknown>>;
    }>;
  };
  response: {
    assistantMessage?: string;
    status: string;
    errorMessage?: string;
  };
}

export interface ChatStreamEvent {
  type:
    | "assistant_started"
    | "assistant_delta"
    | "assistant_done"
    | "tool_started"
    | "tool_finished"
    | "tool_failed"
    | "file_generated"
    | "approval_required"
    | "done"
    | "error"
    | "cancelled"
    | "token"
    | string;
  content?: string;
  role?: "assistant" | "user" | string;
  conversationID?: string;
  turnID?: string;
  error?: string;
  message?: string;
  status?: string;
  toolName?: string;
  approvalID?: string;
  toolCallID?: string;
  arguments?: string;
  /** file_generated fields */
  filename?: string;
  mimeType?: string;
  fileSize?: number;
  fileToken?: string;
  /** tool_finished — JSON-encoded tool artifacts for rich rendering */
  result?: string;
}

export interface ApprovalToolCall {
  id: string;
  toolName: string;
  argumentsJSON: string;
  permissionLevel: "read" | "draft" | "execute" | string;
  requiresApproval: boolean;
  status?: string;
  timestamp?: string;
}

export interface Approval {
  id: string;
  status: "pending" | "approved" | "denied" | string;
  /**
   * Where this approval came from. Omitted for agent-initiated approvals
   * (the default). Set to "thought" when the mind-thoughts dispatcher
   * routed a thought-sourced action through the approvals flow.
   */
  source?: "thought" | string;
  agentID?: string;
  conversationID?: string;
  createdAt: string;
  resolvedAt?: string;
  deferredExecutionID?: string;
  deferredExecutionStatus?: string;
  lastError?: string;
  previewDiff?: string;
  toolCall: ApprovalToolCall;
}

export interface FsRoot {
  id: string;
  path: string;
}

export interface SkillRecord {
  manifest: {
    id: string;
    name: string;
    version: string;
    description: string;
    lifecycleState: string;
    riskLevel: string;
    isUserVisible: boolean;
    category?: string;
    source?: string;
    capabilities: string[];
    tags: string[];
    routing?: {
      capability_group?: string;
      description?: string;
      phrases?: string[];
      words?: string[];
      pairs?: string[][];
      threshold?: number;
    };
  };
  actions: Array<{
    id: string;
    publicID?: string;
    name: string;
    description: string;
    permissionLevel: string;
    approvalPolicy: string;
    isEnabled: boolean;
  }>;
  validation?: {
    skillID: string;
    status: string;
    summary: string;
    isValid: boolean;
    issues: string[];
    validatedAt: string;
  };
}

export interface ExecutableTarget {
  type: "skill" | "workflow" | "command" | string;
  ref: string;
}

export interface CapabilityRecord {
  id: string;
  kind: "skill" | "workflow" | "automation" | string;
  name: string;
  description: string;
  source: string;
  category: string;
  target: ExecutableTarget;
  isEnabled: boolean;
  tags: string[];
  inputSchema: Record<string, unknown>;
  outputSchema: Record<string, unknown>;
  artifactTypes: string[];
  requiredCapabilities: string[];
  requiredSecrets: string[];
  requiredRoots: string[];
  metadata: Record<string, unknown>;
}

export interface MemoryItem {
  id: string;
  category: string;
  title: string;
  content: string;
  source?: string;
  confidence: number;
  importance: number;
  isUserConfirmed: boolean;
  isSensitive: boolean;
  tags: string[];
  createdAt: string;
  updatedAt: string;
}

export interface MemoryParams {
  category?: string;
  limit?: number;
}

export interface LogEntry {
  id: string;
  level: string;
  message: string;
  timestamp: string;
  metadata?: Record<string, string>;
}

export interface TelegramChat {
  chatID: number;
  userID?: number;
  activeConversationID: string;
  createdAt: string;
  updatedAt: string;
  lastTelegramMessageID?: number;
}

export interface CommunicationDestination {
  id: string;
  platform:
    | "telegram"
    | "discord"
    | "slack"
    | "whatsapp"
    | "companion"
    | "webchat";
  channelID: string;
  channelName?: string;
  userID?: string;
  threadID?: string;
}

export interface CommunicationChannel {
  id: string;
  platform: "telegram" | "discord" | "slack" | "whatsapp" | "companion";
  channelID: string;
  channelName?: string;
  userID?: string;
  threadID?: string;
  activeConversationID: string;
  createdAt: string;
  updatedAt: string;
  lastMessageID?: string;
  canReceiveNotifications: boolean;
}

export interface CommunicationPlatformStatus {
  id: string;
  platform: "telegram" | "discord" | "slack" | "whatsapp" | "companion";
  enabled: boolean;
  connected: boolean;
  available: boolean;
  setupState:
    | "not_started"
    | "missing_credentials"
    | "partial_setup"
    | "validation_failed"
    | "ready";
  statusLabel: string;
  connectedAccountName?: string;
  credentialConfigured: boolean;
  blockingReason?: string;
  requiredCredentials: string[];
  lastError?: string;
  lastUpdatedAt?: string;
  metadata: Record<string, string>;
}

export interface CommunicationsSnapshot {
  platforms: CommunicationPlatformStatus[];
  channels: CommunicationChannel[];
}

export interface CommunicationValidationPayload {
  credentials?: Record<string, string>;
  config?: {
    discordClientID?: string;
  };
}

export interface CommunicationSetupValues {
  values: Record<string, string>;
}

export interface GremlinItem {
  id: string;
  name: string;
  emoji: string;
  prompt: string;
  scheduleRaw: string;
  isEnabled: boolean;
  sourceType: string;
  createdAt: string;
  target?: ExecutableTarget;
  workflowID?: string;
  workflowInputValues?: Record<string, string>;
  nextRunAt?: string;
  lastRunAt?: string;
  lastRunStatus?: string;
  telegramChatID?: number;
  communicationDestination?: CommunicationDestination;
  gremlinDescription?: string;
  tags?: string[];
  lastModifiedAt?: string;
}

export interface AutomationSummary {
  id: string;
  name: string;
  emoji: string;
  prompt: string;
  scheduleRaw: string;
  isEnabled: boolean;
  sourceType: string;
  createdAt: string;
  communicationDestination?: CommunicationDestination;
  lastRunAt?: string;
  lastRunStatus?: string;
  lastRunError?: string;
  nextRunAt?: string;
  health: string;
  deliveryHealth: string;
  destinationLabel?: string;
  target?: ExecutableTarget;
}

export type ForgeProposalStatus =
  | "pending"
  | "installed"
  | "enabled"
  | "rejected"
  | "uninstalled";

export interface ForgeProposalRecord {
  id: string;
  skillID: string;
  name: string;
  description: string;
  summary: string;
  rationale?: string;
  requiredSecrets: string[];
  domains: string[];
  actionNames: string[];
  riskLevel: string;
  status: ForgeProposalStatus;
  specJSON: string;
  plansJSON: string;
  contractJSON?: string;
  createdAt: string;
  updatedAt: string;
}

export interface ForgeResearchingItem {
  id: string;
  title: string;
  message: string;
  startedAt: string;
}

// ── Engine LM (llama.cpp) ────────────────────────────────────────────────

export interface EngineStatus {
  running: boolean;
  loading?: boolean;
  loadedModel: string;
  port: number;
  binaryReady: boolean;
  buildVersion?: string;
  lastError?: string;
  lastTPS?: number;
  promptTPS?: number;
  genTimeSec?: number;
  activeRequests?: number;
  contextTokens?: number;
}

export interface EngineModelInfo {
  name: string;
  sizeBytes: number;
  sizeHuman: string;
}

export interface EngineDownloadStatus {
  active: boolean;
  filename: string;
  url?: string;
  downloaded: number;
  total: number;
  percent: number;
}

// ── MLX-LM ───────────────────────────────────────────────────────────────────

export interface MLXInferenceStats {
  decodeTPS: number;
  promptTokens: number;
  cachedPromptTokens?: number;
  cachedPromptRatio?: number;
  completionTokens: number;
  generationSec: number;
  firstTokenSec?: number;
  streamChunks?: number;
  streamChars?: number;
  avgChunkChars?: number;
}

export interface MLXSchedulerStats {
  queueDepth: number;
  activeRequests: number;
  maxConcurrency: number;
  batchWindowMs: number;
  lastBatchSize?: number;
  totalRequests?: number;
  totalBatches?: number;
  avgQueueWaitSec?: number;
}

export interface MLXStatus {
  running: boolean;
  loading?: boolean;
  loadedModel: string;
  port: number;
  venvReady: boolean;
  packageVersion?: string; // installed mlx-lm version
  latestVersion?: string; // latest version on PyPI (source: https://pypi.org/project/mlx-lm/)
  lastError?: string;
  isAppleSilicon: boolean;
  lastInference?: MLXInferenceStats; // stats from last completed turn
  scheduler: MLXSchedulerStats;
}

export interface MLXModelCapabilities {
  hasChatTemplate: boolean;
  hasToolCalling: boolean;
  hasThinking: boolean;
  toolParserType?: string;
  chatTemplateType?: string;
}

export interface MLXModelInfo {
  name: string; // directory name, e.g. "Llama-3.2-3B-Instruct-4bit"
  sizeBytes: number;
  sizeHuman: string;
  capabilities?: MLXModelCapabilities;
}

export interface MLXDownloadStatus {
  active: boolean;
  repo: string; // HuggingFace repo ID — NOT a direct URL
  modelName: string; // derived directory name (last segment of repo)
  downloaded: number;
  total: number;
  percent: number;
  error?: string;
}

// ── Model Selector ────────────────────────────────────────────────────────────

export interface AIModelRecord {
  id: string;
  displayName: string;
  isFast: boolean;
}

export interface ModelSelectorInfo {
  primaryModel?: string;
  fastModel?: string;
  lastRefreshedAt?: string;
  availableModels?: AIModelRecord[];
  totalAvailable?: number;
  hasMore?: boolean;
  providerStatus?: ProviderStatusInfo;
}

export interface ProviderStatusInfo {
  state: string;
  label: string;
  tone: "green" | "yellow" | "red" | "neutral" | string;
  message: string;
  checkedAt: string;
}

export interface OpenRouterModelHealth {
  status:
    | "ok"
    | "rate_limited"
    | "warning"
    | "missing_key"
    | "unavailable"
    | "unknown"
    | string;
  message: string;
  checkedAt: string;
}

export interface CloudModelHealth {
  status:
    | "ok"
    | "rate_limited"
    | "warning"
    | "missing_key"
    | "unavailable"
    | "unknown"
    | string;
  message: string;
  checkedAt: string;
}

export interface GremlinRun {
  id: string;
  gremlinID: string;
  startedAt: string;
  finishedAt?: string;
  status: "success" | "failed" | "running" | "skipped" | string;
  output?: string;
  errorMessage?: string;
  conversationID?: string;
  workflowRunID?: string;
  triggerSource?: string;
  executionStatus?: string;
  deliveryStatus?: string;
  deliveryError?: string;
  durationMs?: number;
  retryCount?: number;
  artifactsJSON?: string;
}

export interface WorkflowTrustScope {
  approvedRootPaths: string[];
  allowedApps: string[];
  allowsSensitiveRead: boolean;
  allowsLiveWrite: boolean;
}

export interface WorkflowStep {
  id: string;
  title: string;
  kind: "skill_action" | "prompt" | string;
  skillID?: string;
  actionID?: string;
  inputJSON?: string;
  prompt?: string;
  appName?: string;
  targetPath?: string;
  sideEffectLevel?: string;
}

export interface WorkflowDefinition {
  id: string;
  name: string;
  description: string;
  promptTemplate: string;
  tags: string[];
  steps: WorkflowStep[];
  trustScope: WorkflowTrustScope;
  approvalMode: "workflow_boundary" | "step_by_step" | string;
  createdAt: string;
  updatedAt: string;
  sourceConversationID?: string;
  isEnabled: boolean;
}

export interface WorkflowSummary {
  id: string;
  name: string;
  description: string;
  isEnabled: boolean;
  stepCount: number;
  health: string;
  lastRunAt?: string;
  lastRunStatus?: string;
  lastRunError?: string;
}

export interface WorkflowApproval {
  id: string;
  workflowID: string;
  workflowRunID: string;
  status: "pending" | "approved" | "denied" | string;
  reason: string;
  requestedAt: string;
  resolvedAt?: string;
  trustScope: WorkflowTrustScope;
}

export interface WorkflowStepRun {
  id: string;
  stepID: string;
  title: string;
  status:
    | "pending"
    | "running"
    | "completed"
    | "failed"
    | "waiting_for_approval"
    | "skipped"
    | string;
  output?: string;
  errorMessage?: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface WorkflowRun {
  id: string;
  workflowID: string;
  workflowName: string;
  status:
    | "pending"
    | "running"
    | "waiting_for_approval"
    | "completed"
    | "failed"
    | "denied"
    | string;
  outcome?: "success" | "failed" | "waiting_for_approval" | "denied" | string;
  inputValues: Record<string, string>;
  stepRuns: WorkflowStepRun[];
  approval?: WorkflowApproval;
  assistantSummary?: string;
  errorMessage?: string;
  startedAt: string;
  finishedAt?: string;
  conversationID?: string;
}

export interface ConversationSummary {
  id: string;
  messageCount: number;
  firstUserMessage?: string;
  lastAssistantMessage?: string;
  createdAt: string;
  updatedAt: string;
  platform: string;
  platformContext?: string;
}

export interface ConversationMessage {
  id: string;
  role: "user" | "assistant" | "system" | "tool";
  content: string;
  timestamp: string;
  blocks?: Array<Record<string, unknown>>;
}

export interface ConversationDetail extends ConversationSummary {
  messages: ConversationMessage[];
}

export interface LinkPreview {
  url: string;
  title?: string;
  description?: string;
  imageURL?: string;
  domain?: string;
}

export interface ImageModelBreakdown {
  provider: string;
  model: string;
  imageCount: number;
  totalCostUSD: number;
}

export interface TokenUsageSummary {
  totalInputTokens: number;
  totalCachedInputTokens: number;
  totalOutputTokens: number;
  totalTokens: number;
  totalCostUSD: number;
  turnCount: number;
  byModel: ModelUsageBreakdown[];
  dailySeries: DailyUsageSeries[];
  imageTotalCount: number;
  imageTotalCostUSD: number;
  imageByModel: ImageModelBreakdown[];
}

export interface ModelUsageBreakdown {
  provider: string;
  model: string;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  totalTokens: number;
  totalCostUSD: number;
  turnCount: number;
}

export interface DailyUsageSeries {
  date: string;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  totalTokens: number;
  costUSD: number;
  turnCount: number;
}

export interface TokenUsageEvent {
  id: string;
  conversationId: string;
  provider: string;
  model: string;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  inputCostUSD: number;
  outputCostUSD: number;
  totalCostUSD: number;
  recordedAt: string;
}

// ── Voice ─────────────────────────────────────────────────────────────────────

export interface VoiceStatus {
  sessionActive: boolean;
  sessionID?: string;
  sessionStartedUnix?: number;
  whisperRunning: boolean;
  whisperReady: boolean;
  whisperPort: number;
  whisperModel?: string;
  whisperBuildTag?: string;
  kokoroRunning: boolean;
  kokoroReady: boolean;
  kokoroPort: number;
  kokoroVersion?: string;
  lastError?: string;
}

export interface VoiceOption {
  id: string;
  label: string;
  description: string;
  featured: boolean;
  modelGate?: string; // only shown when this TTS model is selected
}

export interface VoiceModelInfo {
  name: string;
  component: "whisper" | "kokoro";
  sizeBytes: number;
  sizeHuman: string;
}

export interface VoiceTranscribeResult {
  text: string;
  language?: string;
  duration?: number;
  sessionID?: string;
}

// ── Dashboards v2 ────────────────────────────────────────────────────────────
// Mirrors internal/modules/dashboards/types.go. SchemaVersion = 2.

export type DashboardStatus = "draft" | "live";

export const DASHBOARD_SIZES = [
  "quarter",
  "third",
  "half",
  "tall",
  "full",
] as const;
export type DashboardSize = (typeof DASHBOARD_SIZES)[number];

export type DashboardSourceKind =
  | "runtime"
  | "skill"
  | "sql"
  | "chat_analytics"
  | "gremlin"
  | "live_compute";

export type DashboardRefreshMode = "manual" | "interval" | "push";

export type DashboardWidgetMode = "preset" | "code";

export type DashboardPreset =
  | "metric"
  | "table"
  | "line_chart"
  | "bar_chart"
  | "list"
  | "markdown";

export interface DashboardRefreshPolicy {
  mode: DashboardRefreshMode;
  intervalSeconds?: number;
  idleSeconds?: number;
}

export interface DashboardDataSource {
  name: string;
  kind: DashboardSourceKind;
  config: Record<string, unknown>;
  refresh: DashboardRefreshPolicy;
}

export interface DashboardDataSourceBinding {
  source: string;
  path?: string;
  options?: Record<string, unknown>;
}

export interface DashboardWidgetCode {
  mode: DashboardWidgetMode;
  preset?: DashboardPreset;
  options?: Record<string, unknown>;
  tsx?: string;
  compiled?: string;
  hash?: string;
}

export interface DashboardWidget {
  id: string;
  title?: string;
  description?: string;
  size: DashboardSize;
  group?: string;
  bindings?: DashboardDataSourceBinding[];
  code: DashboardWidgetCode;
  gridX: number;
  gridY: number;
  gridW: number;
  gridH: number;
}

export interface DashboardLayoutHints {
  columns: number;
}

export interface DashboardDefinition {
  schemaVersion: number;
  id: string;
  name: string;
  description?: string;
  status: DashboardStatus;
  sources: DashboardDataSource[];
  widgets: DashboardWidget[];
  layout: DashboardLayoutHints;
  createdAt: string;
  updatedAt: string;
  committedAt?: string;
}

export interface DashboardSummary {
  id: string;
  name: string;
  description?: string;
  status: DashboardStatus;
  widgetCount: number;
  sourceCount: number;
  createdAt: string;
  updatedAt: string;
  committedAt?: string;
}

export interface DashboardWidgetData {
  widgetId: string;
  sourceKind?: string;
  source?: string;
  success: boolean;
  error?: string;
  data?: unknown;
  resolvedAt: string;
  durationMs: number;
}

export interface DashboardRefreshEvent {
  dashboardId: string;
  source: string;
  data?: unknown;
  error?: string;
  at: string;
}

export interface StorageStats {
  dir: string;
  fileCount: number;
  totalSize: number; // bytes
}

// ── Team HQ / AGENTS ───────────────────────────────────────────────────────

export interface TeamAtlasStation {
  id: string;
  name: string;
  role: string;
  status: string;
}

export interface TeamAgentRuntime {
  status: string;
  currentTaskID?: string;
  lastActiveAt?: string;
  lastError?: string;
  updatedAt: string;
}

export interface TeamAgentMetrics {
  tasksCompleted: number;
  tasksFailed: number;
  totalToolCalls: number;
  lastActiveAt?: string;
  successRate?: number;
}

export interface TeamKPIs {
  totalTasksCompleted: number;
  totalTasksFailed: number;
  totalToolCalls: number;
}

export interface TriggerEvent {
  triggerID: string;
  triggerType: string;
  agentID?: string;
  instruction: string;
  status: "fired" | "suppressed" | "pending" | string;
  firedAt?: string;
  createdAt: string;
}

export interface TeamAgent {
  name: string;
  id: string;
  role: string;
  templateRole?: string;
  mission: string;
  style?: string;
  allowedSkills: string[];
  allowedToolClasses?: string[];
  autonomy: string;
  activation?: string;
  providerType?: string;
  model?: string;
  enabled: boolean;
  runtime: TeamAgentRuntime;
  metrics?: TeamAgentMetrics;
}

export interface TeamAssignPayload {
  agentID: string;
  task: string;
  goal?: string;
}

export interface TeamEvent {
  eventID: string;
  eventType: string;
  agentID?: string;
  taskID?: string;
  title: string;
  detail?: string;
  payload?: Record<string, unknown>;
  createdAt: string;
}

export interface TeamBlockedItem {
  kind: string;
  id: string;
  agentID?: string;
  title: string;
  status: string;
  blockingKind?: string;
  blockingDetail?: string;
}

export interface TeamSuggestedAction {
  kind: string;
  id: string;
  agentID?: string;
  title: string;
}

export interface TeamTaskStep {
  stepID: string;
  sequenceNumber: number;
  role: string;
  stepType: string;
  content: string;
  toolName?: string;
  toolCallID?: string;
  createdAt: string;
}

export interface TeamTask {
  taskID: string;
  agentID: string;
  status: string;
  goal: string;
  title?: string;
  objective?: string;
  mode?: string;
  pattern?: string;
  requestedBy: string;
  resultSummary?: string;
  errorMessage?: string;
  conversationID?: string;
  blockingKind?: string;
  blockingDetail?: string;
  startedAt: string;
  finishedAt?: string;
  createdAt: string;
  updatedAt: string;
  steps?: TeamTaskStep[];
}

export interface TeamSnapshot {
  atlas: TeamAtlasStation;
  agents: TeamAgent[];
  activity: TeamEvent[];
  blockedItems: TeamBlockedItem[];
  suggestedActions: TeamSuggestedAction[];
  kpis: TeamKPIs;
}
