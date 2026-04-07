import Foundation
import AtlasGuard
import AtlasLogging
import AtlasMemory
import AtlasNetwork
import AtlasSkills
import AtlasShared
import AtlasTools

public struct AgentContext: Sendable {
    public let config: AtlasConfig
    public let logger: AtlasLogger
    public let modelSelector: ProviderAwareModelSelector
    /// The active AI client for all conversation turns and internal completions.
    /// Bound to the provider selected in `config.activeAIProvider`.
    public let aiClient: any AtlasAIClient
    public let telegramClient: TelegramClient
    public let memoryStore: MemoryStore
    public let conversationStore: ConversationStore
    public let eventLogStore: EventLogStore
    public let communicationSessionStore: CommunicationSessionStore
    public let telegramSessionStore: TelegramSessionStore
    public let permissionManager: PermissionManager
    public let approvalManager: ToolApprovalManager
    public let deferredExecutionManager: DeferredExecutionManager
    public let toolRegistry: ToolRegistry
    public let toolExecutor: ToolExecutor
    public let skillRegistry: SkillRegistry
    public let skillPolicyEngine: SkillPolicyEngine
    public let skillAuditStore: SkillAuditStore
    public let skillExecutionGateway: SkillExecutionGateway
    public let actionPolicyStore: ActionPolicyStore
    public let fileAccessScopeStore: FileAccessScopeStore
    public let personaEngine: any PersonaProviding
    public let personaPromptAssembler: any PersonaPromptAssembling
    public let skillRoutingPolicy: any SkillRoutingPolicying
    public let gremlinsFileStore: GremlinsFileStore
    public let gremlinManagingAdapter: GremlinManagingAdapter
    public let forgeProposalStore: ForgeProposalStore
    public let mindEngine: MindEngine
    public let skillsEngine: SkillsEngine

    public init(
        config: AtlasConfig = AtlasConfig(),
        logger: AtlasLogger = .runtime,
        modelSelector: ProviderAwareModelSelector? = nil,
        aiClient: (any AtlasAIClient)? = nil,
        telegramClient: TelegramClient? = nil,
        memoryStore: MemoryStore? = nil,
        permissionManager: PermissionManager? = nil,
        approvalManager: ToolApprovalManager? = nil,
        toolRegistry: ToolRegistry? = nil,
        toolExecutor: ToolExecutor? = nil,
        skillRegistry: SkillRegistry? = nil,
        skillPolicyEngine: SkillPolicyEngine? = nil,
        skillAuditStore: SkillAuditStore? = nil,
        skillExecutionGateway: SkillExecutionGateway? = nil,
        actionPolicyStore: ActionPolicyStore? = nil,
        fileAccessScopeStore: FileAccessScopeStore? = nil,
        personaEngine: (any PersonaProviding)? = nil,
        personaPromptAssembler: (any PersonaPromptAssembling)? = nil,
        skillRoutingPolicy: (any SkillRoutingPolicying)? = nil,
        mindEngine: MindEngine? = nil,
        skillsEngine: SkillsEngine? = nil
    ) throws {
        self.config = config
        self.logger = logger

        // Build the provider-aware model selector
        let resolvedModelSelector = modelSelector ?? ProviderAwareModelSelector(config: config)
        self.modelSelector = resolvedModelSelector

        // Build the correct AI client for the active provider
        let resolvedAIClient: any AtlasAIClient
        if let injected = aiClient {
            resolvedAIClient = injected
        } else {
            switch config.activeAIProvider {
            case .openAI:
                resolvedAIClient = OpenAIClient(config: config)
            case .anthropic:
                resolvedAIClient = AnthropicClient(config: config)
            case .gemini:
                resolvedAIClient = GeminiClient(config: config)
            case .lmStudio:
                resolvedAIClient = LMStudioClient(config: config)
            }
        }
        self.aiClient = resolvedAIClient

        let resolvedMemoryStore = try memoryStore ?? MemoryStore(databasePath: config.memoryDatabasePath)
        let resolvedPermissionManager = permissionManager ?? PermissionManager(
            grantedPermissions: [.read, .draft, .execute],
            autoApproveDraftTools: config.autoApproveDraftTools
        )
        let resolvedApprovalManager = approvalManager ?? ToolApprovalManager()
        let resolvedDeferredExecutionManager = DeferredExecutionManager(
            memoryStore: resolvedMemoryStore,
            approvalManager: resolvedApprovalManager,
            logger: logger
        )
        let resolvedToolRegistry = toolRegistry ?? ToolRegistry()
        let resolvedActionPolicyStore = actionPolicyStore ?? ActionPolicyStore()
        let resolvedSkillRegistry = skillRegistry ?? SkillRegistry(policyStore: resolvedActionPolicyStore)
        let resolvedSkillPolicyEngine = skillPolicyEngine ?? SkillPolicyEngine()
        let resolvedSkillAuditStore = skillAuditStore ?? SkillAuditStore()
        let resolvedFileAccessScopeStore = fileAccessScopeStore ?? FileAccessScopeStore()
        let resolvedSandbox = try config.resolvedToolSandboxDirectory()

        self.telegramClient = telegramClient ?? TelegramClient(config: config)
        self.memoryStore = resolvedMemoryStore
        self.conversationStore = ConversationStore(memoryStore: resolvedMemoryStore)
        self.eventLogStore = EventLogStore(memoryStore: resolvedMemoryStore)
        self.communicationSessionStore = CommunicationSessionStore(memoryStore: resolvedMemoryStore)
        self.telegramSessionStore = TelegramSessionStore(memoryStore: resolvedMemoryStore)
        self.permissionManager = resolvedPermissionManager
        self.approvalManager = resolvedApprovalManager
        self.deferredExecutionManager = resolvedDeferredExecutionManager
        self.toolRegistry = resolvedToolRegistry
        self.toolExecutor = toolExecutor ?? ToolExecutor(
            registry: resolvedToolRegistry,
            permissionManager: resolvedPermissionManager,
            approvalManager: resolvedApprovalManager,
            deferredExecutionManager: resolvedDeferredExecutionManager,
            logger: logger,
            fileAccessScope: resolvedSandbox
        )
        self.skillRegistry = resolvedSkillRegistry
        self.skillPolicyEngine = resolvedSkillPolicyEngine
        self.skillAuditStore = resolvedSkillAuditStore
        self.actionPolicyStore = resolvedActionPolicyStore
        self.skillExecutionGateway = skillExecutionGateway ?? SkillExecutionGateway(
            registry: resolvedSkillRegistry,
            policyEngine: resolvedSkillPolicyEngine,
            policyStore: resolvedActionPolicyStore,
            approvalManager: resolvedApprovalManager,
            deferredExecutionManager: resolvedDeferredExecutionManager,
            auditStore: resolvedSkillAuditStore,
            logger: logger
        )
        self.fileAccessScopeStore = resolvedFileAccessScopeStore
        self.personaEngine = personaEngine ?? PersonaEngine(config: config)
        self.personaPromptAssembler = personaPromptAssembler ?? PersonaPromptAssembler()
        self.skillRoutingPolicy = skillRoutingPolicy ?? SkillRoutingPolicy()

        let resolvedGremlinsFileStore = GremlinsFileStore(
            fileURL: URL(fileURLWithPath: config.gremlinsFilePath),
            memoryStore: resolvedMemoryStore
        )
        self.gremlinsFileStore = resolvedGremlinsFileStore
        self.gremlinManagingAdapter = GremlinManagingAdapter(fileStore: resolvedGremlinsFileStore)
        self.forgeProposalStore = ForgeProposalStore(memoryStore: resolvedMemoryStore)

        // MIND.md engine — uses fast model via the active provider
        if let mindEngine {
            self.mindEngine = mindEngine
        } else {
            let fileStore = MindFileStore(filePath: config.mindFilePath)
            let reflectionService = MindReflectionService(
                openAI: resolvedAIClient,
                fastModel: { await resolvedModelSelector.resolvedFastModel() }
            )
            self.mindEngine = MindEngine(fileStore: fileStore, reflectionService: reflectionService)
        }

        // SKILLS.md engine — uses the active AI client
        self.skillsEngine = skillsEngine ?? SkillsEngine(
            skillsFilePath: config.skillsFilePath,
            openAI: resolvedAIClient
        )
    }
}
