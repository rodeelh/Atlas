import Foundation

public enum PermissionLevel: String, Codable, CaseIterable, Comparable, Hashable, Sendable {
    case read
    case draft
    case execute

    public static func < (lhs: PermissionLevel, rhs: PermissionLevel) -> Bool {
        lhs.sortOrder < rhs.sortOrder
    }

    private var sortOrder: Int {
        switch self {
        case .read:
            return 0
        case .draft:
            return 1
        case .execute:
            return 2
        }
    }
}

public enum ActionApprovalPolicy: String, Codable, CaseIterable, Hashable, Sendable {
    case autoApprove = "auto_approve"
    case askOnce = "ask_once"
    case alwaysAsk = "always_ask"

    /// The default approval policy for a given permission level.
    public static func defaultPolicy(for level: PermissionLevel) -> ActionApprovalPolicy {
        level == .read ? .autoApprove : .alwaysAsk
    }

    /// Whether this policy is considered a lower-friction choice for the given permission level.
    /// Used to trigger a disclaimer when the user selects `autoApprove` on a non-read action.
    public func requiresDisclaimerForDowngrade(from level: PermissionLevel) -> Bool {
        self == .autoApprove && level != .read
    }
}

public enum AtlasMessageRole: String, Codable, Hashable, Sendable {
    case system
    case user
    case assistant
    case tool
}

public enum AtlasToolCallStatus: String, Codable, Hashable, Sendable {
    case pending
    case approved
    case denied
    case running
    case completed
    case failed
}

public enum DeferredExecutionSourceType: String, Codable, CaseIterable, Hashable, Sendable {
    case skill
    case tool
}

public enum DeferredExecutionStatus: String, Codable, CaseIterable, Hashable, Sendable {
    case pendingApproval = "pending_approval"
    case approved
    case running
    case completed
    case failed
    case denied
    case cancelled

    public var isTerminal: Bool {
        switch self {
        case .completed, .failed, .denied, .cancelled:
            return true
        case .pendingApproval, .approved, .running:
            return false
        }
    }
}

public enum AtlasAgentResponseStatus: String, Codable, Hashable, Sendable {
    case completed
    case waitingForApproval = "waiting_for_approval"
    case failed
}

public enum AtlasRuntimeState: String, Codable, Hashable, Sendable {
    case starting
    case ready
    case degraded
    case stopped
}

public enum ApprovalStatus: String, Codable, Hashable, Sendable {
    case pending
    case approved
    case denied
}

public enum MemoryCategory: String, Codable, CaseIterable, Hashable, Sendable {
    case profile
    case preference
    case project
    case workflow
    case episodic
}

public enum MemorySource: String, Codable, Hashable, Sendable {
    case userExplicit = "user_explicit"
    case conversationInference = "conversation_inference"
    case assistantObservation = "assistant_observation"
    case systemDerived = "system_derived"
}

public struct MemoryItem: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let category: MemoryCategory
    public let title: String
    public let content: String
    public let source: MemorySource
    public let confidence: Double
    public let importance: Double
    public let createdAt: Date
    public let updatedAt: Date
    public let lastRetrievedAt: Date?
    public let isUserConfirmed: Bool
    public let isSensitive: Bool
    public let tags: [String]
    public let relatedConversationID: UUID?

    public init(
        id: UUID = UUID(),
        category: MemoryCategory,
        title: String,
        content: String,
        source: MemorySource,
        confidence: Double,
        importance: Double,
        createdAt: Date = .now,
        updatedAt: Date = .now,
        lastRetrievedAt: Date? = nil,
        isUserConfirmed: Bool = false,
        isSensitive: Bool = false,
        tags: [String] = [],
        relatedConversationID: UUID? = nil
    ) {
        self.id = id
        self.category = category
        self.title = title
        self.content = content
        self.source = source
        self.confidence = confidence
        self.importance = importance
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.lastRetrievedAt = lastRetrievedAt
        self.isUserConfirmed = isUserConfirmed
        self.isSensitive = isSensitive
        self.tags = tags
        self.relatedConversationID = relatedConversationID
    }

    public func updatingRetrievedAt(_ date: Date = .now) -> MemoryItem {
        MemoryItem(
            id: id,
            category: category,
            title: title,
            content: content,
            source: source,
            confidence: confidence,
            importance: importance,
            createdAt: createdAt,
            updatedAt: updatedAt,
            lastRetrievedAt: date,
            isUserConfirmed: isUserConfirmed,
            isSensitive: isSensitive,
            tags: tags,
            relatedConversationID: relatedConversationID
        )
    }

    public func updating(
        title: String? = nil,
        content: String? = nil,
        confidence: Double? = nil,
        importance: Double? = nil,
        updatedAt: Date = .now,
        lastRetrievedAt: Date? = nil,
        isUserConfirmed: Bool? = nil,
        isSensitive: Bool? = nil,
        tags: [String]? = nil
    ) -> MemoryItem {
        MemoryItem(
            id: id,
            category: category,
            title: title ?? self.title,
            content: content ?? self.content,
            source: source,
            confidence: confidence ?? self.confidence,
            importance: importance ?? self.importance,
            createdAt: createdAt,
            updatedAt: updatedAt,
            lastRetrievedAt: lastRetrievedAt ?? self.lastRetrievedAt,
            isUserConfirmed: isUserConfirmed ?? self.isUserConfirmed,
            isSensitive: isSensitive ?? self.isSensitive,
            tags: tags ?? self.tags,
            relatedConversationID: relatedConversationID
        )
    }
}

public struct AtlasMessage: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let role: AtlasMessageRole
    public let content: String
    public let timestamp: Date

    public init(
        id: UUID = UUID(),
        role: AtlasMessageRole,
        content: String,
        timestamp: Date = .now
    ) {
        self.id = id
        self.role = role
        self.content = content
        self.timestamp = timestamp
    }
}

public struct AtlasConversation: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let messages: [AtlasMessage]
    public let createdAt: Date
    public let updatedAt: Date
    /// Platform-specific persona context appended to the system prompt. Stored once at conversation creation.
    public let platformContext: String?

    public init(
        id: UUID = UUID(),
        messages: [AtlasMessage] = [],
        createdAt: Date = .now,
        updatedAt: Date = .now,
        platformContext: String? = nil
    ) {
        self.id = id
        self.messages = messages
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.platformContext = platformContext
    }

    public func appending(_ message: AtlasMessage) -> AtlasConversation {
        AtlasConversation(
            id: id,
            messages: messages + [message],
            createdAt: createdAt,
            updatedAt: message.timestamp,
            platformContext: platformContext
        )
    }
}

public struct AtlasToolInput: Codable, Hashable, Sendable {
    public let argumentsJSON: String

    public init(argumentsJSON: String = "{}") {
        self.argumentsJSON = argumentsJSON
    }

    public func decode<T: Decodable>(_ type: T.Type) throws -> T {
        let data = Data(argumentsJSON.utf8)
        return try AtlasJSON.decoder.decode(T.self, from: data)
    }

    public func dictionary() throws -> [String: String] {
        try decode([String: String].self)
    }
}

public struct AtlasToolCall: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let toolName: String
    public let argumentsJSON: String
    public let permissionLevel: PermissionLevel
    public let requiresApproval: Bool
    public let status: AtlasToolCallStatus
    public let openAICallID: String?
    public let timestamp: Date

    public init(
        id: UUID = UUID(),
        toolName: String,
        argumentsJSON: String,
        permissionLevel: PermissionLevel,
        requiresApproval: Bool,
        status: AtlasToolCallStatus = .pending,
        openAICallID: String? = nil,
        timestamp: Date = .now
    ) {
        self.id = id
        self.toolName = toolName
        self.argumentsJSON = argumentsJSON
        self.permissionLevel = permissionLevel
        self.requiresApproval = requiresApproval
        self.status = status
        self.openAICallID = openAICallID
        self.timestamp = timestamp
    }

    public var input: AtlasToolInput {
        AtlasToolInput(argumentsJSON: argumentsJSON)
    }

    public func updatingStatus(_ status: AtlasToolCallStatus) -> AtlasToolCall {
        AtlasToolCall(
            id: id,
            toolName: toolName,
            argumentsJSON: argumentsJSON,
            permissionLevel: permissionLevel,
            requiresApproval: requiresApproval,
            status: status,
            openAICallID: openAICallID,
            timestamp: timestamp
        )
    }
}

public struct AtlasToolResult: Codable, Hashable, Sendable {
    public let toolCallID: UUID
    public let output: String
    public let success: Bool
    public let errorMessage: String?
    public let timestamp: Date

    public init(
        toolCallID: UUID,
        output: String,
        success: Bool,
        errorMessage: String? = nil,
        timestamp: Date = .now
    ) {
        self.toolCallID = toolCallID
        self.output = output
        self.success = success
        self.errorMessage = errorMessage
        self.timestamp = timestamp
    }
}

public struct ApprovalRequest: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let toolCall: AtlasToolCall
    public let conversationID: UUID?
    public let deferredExecutionID: UUID?
    public let deferredExecutionStatus: DeferredExecutionStatus?
    public let lastError: String?
    public let createdAt: Date
    public let resolvedAt: Date?
    public let status: ApprovalStatus

    public init(
        id: UUID,
        toolCall: AtlasToolCall,
        conversationID: UUID? = nil,
        deferredExecutionID: UUID? = nil,
        deferredExecutionStatus: DeferredExecutionStatus? = nil,
        lastError: String? = nil,
        createdAt: Date = .now,
        resolvedAt: Date? = nil,
        status: ApprovalStatus = .pending
    ) {
        self.id = id
        self.toolCall = toolCall
        self.conversationID = conversationID
        self.deferredExecutionID = deferredExecutionID
        self.deferredExecutionStatus = deferredExecutionStatus
        self.lastError = lastError
        self.createdAt = createdAt
        self.resolvedAt = resolvedAt
        self.status = status
    }

    public var toolCallID: UUID {
        toolCall.id
    }

    public func updatingStatus(_ status: ApprovalStatus, resolvedAt: Date? = .now) -> ApprovalRequest {
        ApprovalRequest(
            id: id,
            toolCall: toolCall,
            conversationID: conversationID,
            deferredExecutionID: deferredExecutionID,
            deferredExecutionStatus: deferredExecutionStatus,
            lastError: lastError,
            createdAt: createdAt,
            resolvedAt: resolvedAt,
            status: status
        )
    }
}

public struct DeferredExecutionResult: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let output: String
    public let success: Bool
    public let summary: String
    public let errorMessage: String?
    public let metadata: [String: String]
    public let timestamp: Date

    public init(
        id: UUID = UUID(),
        output: String,
        success: Bool,
        summary: String,
        errorMessage: String? = nil,
        metadata: [String: String] = [:],
        timestamp: Date = .now
    ) {
        self.id = id
        self.output = output
        self.success = success
        self.summary = summary
        self.errorMessage = errorMessage
        self.metadata = metadata
        self.timestamp = timestamp
    }
}

public struct DeferredExecutionRequest: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let sourceType: DeferredExecutionSourceType
    public let skillID: String?
    public let toolID: String?
    public let actionID: String?
    public let toolCallID: UUID
    public let normalizedInputJSON: String
    public let conversationID: UUID?
    public let originatingMessageID: String?
    public let approvalID: UUID
    public let summary: String
    public let permissionLevel: PermissionLevel
    public let riskLevel: String
    public let status: DeferredExecutionStatus
    public let lastError: String?
    public let result: DeferredExecutionResult?
    public let createdAt: Date
    public let updatedAt: Date

    public init(
        id: UUID = UUID(),
        sourceType: DeferredExecutionSourceType,
        skillID: String? = nil,
        toolID: String? = nil,
        actionID: String? = nil,
        toolCallID: UUID,
        normalizedInputJSON: String,
        conversationID: UUID? = nil,
        originatingMessageID: String? = nil,
        approvalID: UUID,
        summary: String,
        permissionLevel: PermissionLevel,
        riskLevel: String,
        status: DeferredExecutionStatus = .pendingApproval,
        lastError: String? = nil,
        result: DeferredExecutionResult? = nil,
        createdAt: Date = .now,
        updatedAt: Date = .now
    ) {
        self.id = id
        self.sourceType = sourceType
        self.skillID = skillID
        self.toolID = toolID
        self.actionID = actionID
        self.toolCallID = toolCallID
        self.normalizedInputJSON = normalizedInputJSON
        self.conversationID = conversationID
        self.originatingMessageID = originatingMessageID
        self.approvalID = approvalID
        self.summary = summary
        self.permissionLevel = permissionLevel
        self.riskLevel = riskLevel
        self.status = status
        self.lastError = lastError
        self.result = result
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    public func updatingStatus(
        _ status: DeferredExecutionStatus,
        lastError: String? = nil,
        result: DeferredExecutionResult? = nil,
        updatedAt: Date = .now
    ) -> DeferredExecutionRequest {
        DeferredExecutionRequest(
            id: id,
            sourceType: sourceType,
            skillID: skillID,
            toolID: toolID,
            actionID: actionID,
            toolCallID: toolCallID,
            normalizedInputJSON: normalizedInputJSON,
            conversationID: conversationID,
            originatingMessageID: originatingMessageID,
            approvalID: approvalID,
            summary: summary,
            permissionLevel: permissionLevel,
            riskLevel: riskLevel,
            status: status,
            lastError: lastError,
            result: result,
            createdAt: createdAt,
            updatedAt: updatedAt
        )
    }
}

public protocol DeferredExecutionManaging: Sendable {
    func hydrate() async
    func allApprovalRequests() async -> [ApprovalRequest]
    func pendingApprovalCount() async -> Int
    func approvalStatus(for toolCallID: UUID) async -> ApprovalStatus?
    func approvalRequest(for toolCallID: UUID) async -> ApprovalRequest?
    func deferredExecution(for toolCallID: UUID) async -> DeferredExecutionRequest?
    func createDeferredSkillApproval(
        skillID: String,
        actionID: String,
        toolCallID: UUID,
        normalizedInputJSON: String,
        conversationID: UUID?,
        originatingMessageID: String?,
        summary: String,
        permissionLevel: PermissionLevel,
        riskLevel: String
    ) async throws -> ApprovalRequest
    func createDeferredToolApproval(
        toolCall: AtlasToolCall,
        conversationID: UUID?,
        summary: String,
        riskLevel: String
    ) async throws -> ApprovalRequest
    func approve(toolCallID: UUID) async throws -> ApprovalRequest
    func deny(toolCallID: UUID) async throws -> ApprovalRequest
    func markRunning(toolCallID: UUID) async throws
    func markCompleted(toolCallID: UUID, result: DeferredExecutionResult) async throws
    func markFailed(toolCallID: UUID, errorMessage: String) async throws
}

public struct TelegramSession: Codable, Identifiable, Hashable, Sendable {
    public let chatID: Int64
    public let userID: Int64?
    public let activeConversationID: UUID
    public let createdAt: Date
    public let updatedAt: Date
    public let lastTelegramMessageID: Int?

    public var id: Int64 {
        chatID
    }

    public init(
        chatID: Int64,
        userID: Int64?,
        activeConversationID: UUID,
        createdAt: Date = .now,
        updatedAt: Date = .now,
        lastTelegramMessageID: Int? = nil
    ) {
        self.chatID = chatID
        self.userID = userID
        self.activeConversationID = activeConversationID
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.lastTelegramMessageID = lastTelegramMessageID
    }
}

public struct AtlasTelegramStatus: Codable, Hashable, Sendable {
    public let enabled: Bool
    public let connected: Bool
    public let pollingActive: Bool
    public let botUsername: String?
    public let lastError: String?
    public let lastUpdateAt: Date?

    public init(
        enabled: Bool = false,
        connected: Bool = false,
        pollingActive: Bool = false,
        botUsername: String? = nil,
        lastError: String? = nil,
        lastUpdateAt: Date? = nil
    ) {
        self.enabled = enabled
        self.connected = connected
        self.pollingActive = pollingActive
        self.botUsername = botUsername
        self.lastError = lastError
        self.lastUpdateAt = lastUpdateAt
    }
}

public struct AtlasAgentResponse: Codable, Hashable, Sendable {
    public let assistantMessage: String
    public let toolCalls: [AtlasToolCall]
    public let status: AtlasAgentResponseStatus
    public let toolResults: [AtlasToolResult]
    public let pendingApprovals: [ApprovalRequest]
    public let errorMessage: String?

    public init(
        assistantMessage: String,
        toolCalls: [AtlasToolCall] = [],
        status: AtlasAgentResponseStatus,
        toolResults: [AtlasToolResult] = [],
        pendingApprovals: [ApprovalRequest] = [],
        errorMessage: String? = nil
    ) {
        self.assistantMessage = assistantMessage
        self.toolCalls = toolCalls
        self.status = status
        self.toolResults = toolResults
        self.pendingApprovals = pendingApprovals
        self.errorMessage = errorMessage
    }

    public var content: String {
        assistantMessage
    }
}

public struct APIKeyStatusResponse: Codable, Sendable {
    public let openAIKeySet: Bool
    public let telegramTokenSet: Bool
    public let discordTokenSet: Bool
    public let slackBotTokenSet: Bool
    public let slackAppTokenSet: Bool
    public let braveSearchKeySet: Bool
    public let anthropicKeySet: Bool
    public let geminiKeySet: Bool
    public let lmStudioKeySet: Bool
    /// User-defined custom API keys (names only — values never leave the Keychain).
    public let customKeys: [String]

    public init(
        openAIKeySet: Bool,
        telegramTokenSet: Bool,
        discordTokenSet: Bool = false,
        slackBotTokenSet: Bool = false,
        slackAppTokenSet: Bool = false,
        braveSearchKeySet: Bool,
        anthropicKeySet: Bool = false,
        geminiKeySet: Bool = false,
        lmStudioKeySet: Bool = false,
        customKeys: [String] = []
    ) {
        self.openAIKeySet = openAIKeySet
        self.telegramTokenSet = telegramTokenSet
        self.discordTokenSet = discordTokenSet
        self.slackBotTokenSet = slackBotTokenSet
        self.slackAppTokenSet = slackAppTokenSet
        self.braveSearchKeySet = braveSearchKeySet
        self.anthropicKeySet = anthropicKeySet
        self.geminiKeySet = geminiKeySet
        self.lmStudioKeySet = lmStudioKeySet
        self.customKeys = customKeys
    }
}

public struct AtlasRuntimeStatus: Codable, Hashable, Sendable {
    public let isRunning: Bool
    public let activeConversationCount: Int
    public let lastMessageAt: Date?
    public let lastError: String?
    public let state: AtlasRuntimeState
    public let runtimePort: Int
    public let startedAt: Date?
    public let activeRequests: Int
    public let pendingApprovalCount: Int
    public let details: String
    public let telegram: AtlasTelegramStatus
    public let communications: CommunicationsSnapshot

    public init(
        isRunning: Bool,
        activeConversationCount: Int,
        lastMessageAt: Date?,
        lastError: String?,
        state: AtlasRuntimeState,
        runtimePort: Int,
        startedAt: Date?,
        activeRequests: Int,
        pendingApprovalCount: Int,
        details: String,
        telegram: AtlasTelegramStatus = AtlasTelegramStatus(),
        communications: CommunicationsSnapshot = CommunicationsSnapshot()
    ) {
        self.isRunning = isRunning
        self.activeConversationCount = activeConversationCount
        self.lastMessageAt = lastMessageAt
        self.lastError = lastError
        self.state = state
        self.runtimePort = runtimePort
        self.startedAt = startedAt
        self.activeRequests = activeRequests
        self.pendingApprovalCount = pendingApprovalCount
        self.details = details
        self.telegram = telegram
        self.communications = communications
    }

    public var isConnected: Bool {
        isRunning
    }
}

public enum AtlasLogLevel: String, Codable, Hashable, Sendable {
    case debug
    case info
    case warning
    case error
}

public struct AtlasLogEntry: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let subsystem: String
    public let category: String
    public let level: AtlasLogLevel
    public let message: String
    public let metadata: [String: String]
    public let timestamp: Date

    public init(
        id: UUID = UUID(),
        subsystem: String,
        category: String,
        level: AtlasLogLevel,
        message: String,
        metadata: [String: String] = [:],
        timestamp: Date = .now
    ) {
        self.id = id
        self.subsystem = subsystem
        self.category = category
        self.level = level
        self.message = message
        self.metadata = metadata
        self.timestamp = timestamp
    }
}

/// A file attachment bundled with a chat message (base64-encoded payload).
public struct AtlasMessageAttachment: Codable, Hashable, Sendable {
    public let filename: String
    public let mimeType: String
    /// Base64-encoded file content (no data URI prefix).
    public let data: String

    public init(filename: String, mimeType: String, data: String) {
        self.filename = filename
        self.mimeType = mimeType
        self.data = data
    }

    /// Ready-to-use data URI for embedding in OpenAI requests.
    public var dataURI: String {
        "data:\(mimeType);base64,\(data)"
    }

    public var isImage: Bool {
        mimeType.hasPrefix("image/")
    }
}

public struct AtlasMessageRequest: Codable, Hashable, Sendable {
    public let conversationID: UUID?
    public let message: String
    public let attachments: [AtlasMessageAttachment]

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case message
        case attachments
    }

    public init(conversationID: UUID? = nil, message: String, attachments: [AtlasMessageAttachment] = []) {
        self.conversationID = conversationID
        self.message = message
        self.attachments = attachments
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        conversationID = try c.decodeIfPresent(UUID.self, forKey: .conversationID)
        message        = try c.decode(String.self, forKey: .message)
        attachments    = try c.decodeIfPresent([AtlasMessageAttachment].self, forKey: .attachments) ?? []
    }
}

public struct AtlasMemoryUpdateRequest: Codable, Hashable, Sendable {
    public let title: String
    public let content: String
    public let markAsConfirmed: Bool

    public init(
        title: String,
        content: String,
        markAsConfirmed: Bool = true
    ) {
        self.title = title
        self.content = content
        self.markAsConfirmed = markAsConfirmed
    }
}

public struct AtlasMemoryCreateRequest: Codable, Hashable, Sendable {
    public let category: MemoryCategory
    public let title: String
    public let content: String
    public let source: MemorySource
    public let confidence: Double
    public let importance: Double
    public let isUserConfirmed: Bool
    public let isSensitive: Bool
    public let tags: [String]

    public init(
        category: MemoryCategory,
        title: String,
        content: String,
        source: MemorySource,
        confidence: Double,
        importance: Double,
        isUserConfirmed: Bool,
        isSensitive: Bool = false,
        tags: [String] = []
    ) {
        self.category = category
        self.title = title
        self.content = content
        self.source = source
        self.confidence = confidence
        self.importance = importance
        self.isUserConfirmed = isUserConfirmed
        self.isSensitive = isSensitive
        self.tags = tags
    }
}

public struct AtlasMessageResponseEnvelope: Codable, Hashable, Sendable {
    public let conversation: AtlasConversation
    public let response: AtlasAgentResponse

    public init(conversation: AtlasConversation, response: AtlasAgentResponse) {
        self.conversation = conversation
        self.response = response
    }
}

public struct AtlasToolInputArrayItems: Codable, Hashable, Sendable {
    public let type: String
    public let description: String

    public init(type: String, description: String) {
        self.type = type
        self.description = description
    }
}

public struct AtlasToolInputProperty: Codable, Hashable, Sendable {
    public let type: String
    public let description: String
    public let items: AtlasToolInputArrayItems?

    public init(
        type: String,
        description: String,
        items: AtlasToolInputArrayItems? = nil
    ) {
        self.type = type
        self.description = description
        self.items = items
    }
}

public struct AtlasToolInputSchema: Codable, Hashable, Sendable {
    public let type: String
    public let properties: [String: AtlasToolInputProperty]
    public let required: [String]
    public let additionalProperties: Bool

    public init(
        type: String = "object",
        properties: [String: AtlasToolInputProperty],
        required: [String] = [],
        additionalProperties: Bool = false
    ) {
        self.type = type
        self.properties = properties
        self.required = required
        self.additionalProperties = additionalProperties
    }
}

public struct AtlasToolDefinition: Codable, Hashable, Sendable {
    public let name: String
    public let description: String
    public let inputSchema: AtlasToolInputSchema

    public init(name: String, description: String, inputSchema: AtlasToolInputSchema) {
        self.name = name
        self.description = description
        self.inputSchema = inputSchema
    }
}

public protocol AtlasToolExecuting: Sendable {
    func execute(toolCall: AtlasToolCall, conversationID: UUID) async throws -> AtlasToolResult
}

public protocol AtlasRuntimeHandling: Sendable {
    func handleMessage(_ request: AtlasMessageRequest) async -> AtlasMessageResponseEnvelope
    func status() async -> AtlasRuntimeStatus
    func approvals() async -> [ApprovalRequest]
    func approve(toolCallID: UUID) async throws -> AtlasMessageResponseEnvelope
    func deny(toolCallID: UUID) async throws -> ApprovalRequest
}

// MARK: - Workflows

public enum AtlasWorkflowStepKind: String, Codable, CaseIterable, Hashable, Sendable {
    case skillAction = "skill_action"
    case prompt = "prompt"
}

public enum AtlasWorkflowStepStatus: String, Codable, CaseIterable, Hashable, Sendable {
    case pending
    case running
    case completed
    case failed
    case waitingForApproval = "waiting_for_approval"
    case skipped
}

public enum AtlasWorkflowRunStatus: String, Codable, CaseIterable, Hashable, Sendable {
    case pending
    case running
    case waitingForApproval = "waiting_for_approval"
    case completed
    case failed
    case denied
}

public enum AtlasWorkflowApprovalStatus: String, Codable, CaseIterable, Hashable, Sendable {
    case pending
    case approved
    case denied
}

public enum AtlasWorkflowApprovalMode: String, Codable, CaseIterable, Hashable, Sendable {
    case workflowBoundary = "workflow_boundary"
    case stepByStep = "step_by_step"
}

public enum AtlasWorkflowOutcome: String, Codable, CaseIterable, Hashable, Sendable {
    case success
    case failed
    case waitingForApproval = "waiting_for_approval"
    case denied
}

public struct AtlasWorkflowTrustScope: Codable, Hashable, Sendable {
    public let approvedRootPaths: [String]
    public let allowedApps: [String]
    public let allowsSensitiveRead: Bool
    public let allowsLiveWrite: Bool

    public init(
        approvedRootPaths: [String] = [],
        allowedApps: [String] = [],
        allowsSensitiveRead: Bool = false,
        allowsLiveWrite: Bool = false
    ) {
        self.approvedRootPaths = approvedRootPaths
        self.allowedApps = allowedApps
        self.allowsSensitiveRead = allowsSensitiveRead
        self.allowsLiveWrite = allowsLiveWrite
    }
}

public struct AtlasWorkflowExecutionContext: Codable, Hashable, Sendable {
    public let workflowID: String
    public let workflowRunID: UUID
    public let trustScope: AtlasWorkflowTrustScope
    public let approvalMode: AtlasWorkflowApprovalMode
    public let approvalGrantedAt: Date?

    public init(
        workflowID: String,
        workflowRunID: UUID,
        trustScope: AtlasWorkflowTrustScope,
        approvalMode: AtlasWorkflowApprovalMode,
        approvalGrantedAt: Date? = nil
    ) {
        self.workflowID = workflowID
        self.workflowRunID = workflowRunID
        self.trustScope = trustScope
        self.approvalMode = approvalMode
        self.approvalGrantedAt = approvalGrantedAt
    }
}

public struct AtlasWorkflowStep: Codable, Hashable, Sendable, Identifiable {
    public let id: String
    public let title: String
    public let kind: AtlasWorkflowStepKind
    public let skillID: String?
    public let actionID: String?
    public let inputJSON: String?
    public let prompt: String?
    public let appName: String?
    public let targetPath: String?
    public let sideEffectLevel: String?

    public init(
        id: String,
        title: String,
        kind: AtlasWorkflowStepKind,
        skillID: String? = nil,
        actionID: String? = nil,
        inputJSON: String? = nil,
        prompt: String? = nil,
        appName: String? = nil,
        targetPath: String? = nil,
        sideEffectLevel: String? = nil
    ) {
        self.id = id
        self.title = title
        self.kind = kind
        self.skillID = skillID
        self.actionID = actionID
        self.inputJSON = inputJSON
        self.prompt = prompt
        self.appName = appName
        self.targetPath = targetPath
        self.sideEffectLevel = sideEffectLevel
    }
}

public struct AtlasWorkflowDefinition: Codable, Hashable, Sendable, Identifiable {
    public let id: String
    public let name: String
    public let description: String
    public let promptTemplate: String
    public let tags: [String]
    public let steps: [AtlasWorkflowStep]
    public let trustScope: AtlasWorkflowTrustScope
    public let approvalMode: AtlasWorkflowApprovalMode
    public let createdAt: Date
    public let updatedAt: Date
    public let sourceConversationID: UUID?
    public let isEnabled: Bool

    public init(
        id: String,
        name: String,
        description: String,
        promptTemplate: String,
        tags: [String] = [],
        steps: [AtlasWorkflowStep] = [],
        trustScope: AtlasWorkflowTrustScope = AtlasWorkflowTrustScope(),
        approvalMode: AtlasWorkflowApprovalMode = .workflowBoundary,
        createdAt: Date = .now,
        updatedAt: Date = .now,
        sourceConversationID: UUID? = nil,
        isEnabled: Bool = true
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.promptTemplate = promptTemplate
        self.tags = tags
        self.steps = steps
        self.trustScope = trustScope
        self.approvalMode = approvalMode
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.sourceConversationID = sourceConversationID
        self.isEnabled = isEnabled
    }
}

public struct AtlasWorkflowApproval: Codable, Hashable, Sendable, Identifiable {
    public let id: UUID
    public let workflowID: String
    public let workflowRunID: UUID
    public let status: AtlasWorkflowApprovalStatus
    public let reason: String
    public let requestedAt: Date
    public let resolvedAt: Date?
    public let trustScope: AtlasWorkflowTrustScope

    public init(
        id: UUID = UUID(),
        workflowID: String,
        workflowRunID: UUID,
        status: AtlasWorkflowApprovalStatus,
        reason: String,
        requestedAt: Date = .now,
        resolvedAt: Date? = nil,
        trustScope: AtlasWorkflowTrustScope
    ) {
        self.id = id
        self.workflowID = workflowID
        self.workflowRunID = workflowRunID
        self.status = status
        self.reason = reason
        self.requestedAt = requestedAt
        self.resolvedAt = resolvedAt
        self.trustScope = trustScope
    }
}

public struct AtlasWorkflowStepRun: Codable, Hashable, Sendable, Identifiable {
    public let id: UUID
    public let stepID: String
    public let title: String
    public let status: AtlasWorkflowStepStatus
    public let output: String?
    public let errorMessage: String?
    public let startedAt: Date?
    public let finishedAt: Date?

    public init(
        id: UUID = UUID(),
        stepID: String,
        title: String,
        status: AtlasWorkflowStepStatus,
        output: String? = nil,
        errorMessage: String? = nil,
        startedAt: Date? = nil,
        finishedAt: Date? = nil
    ) {
        self.id = id
        self.stepID = stepID
        self.title = title
        self.status = status
        self.output = output
        self.errorMessage = errorMessage
        self.startedAt = startedAt
        self.finishedAt = finishedAt
    }
}

public struct AtlasWorkflowRun: Codable, Hashable, Sendable, Identifiable {
    public let id: UUID
    public let workflowID: String
    public let workflowName: String
    public let status: AtlasWorkflowRunStatus
    public let outcome: AtlasWorkflowOutcome?
    public let inputValues: [String: String]
    public let stepRuns: [AtlasWorkflowStepRun]
    public let approval: AtlasWorkflowApproval?
    public let assistantSummary: String?
    public let errorMessage: String?
    public let startedAt: Date
    public let finishedAt: Date?
    public let conversationID: UUID?

    public init(
        id: UUID = UUID(),
        workflowID: String,
        workflowName: String,
        status: AtlasWorkflowRunStatus,
        outcome: AtlasWorkflowOutcome? = nil,
        inputValues: [String: String] = [:],
        stepRuns: [AtlasWorkflowStepRun] = [],
        approval: AtlasWorkflowApproval? = nil,
        assistantSummary: String? = nil,
        errorMessage: String? = nil,
        startedAt: Date = .now,
        finishedAt: Date? = nil,
        conversationID: UUID? = nil
    ) {
        self.id = id
        self.workflowID = workflowID
        self.workflowName = workflowName
        self.status = status
        self.outcome = outcome
        self.inputValues = inputValues
        self.stepRuns = stepRuns
        self.approval = approval
        self.assistantSummary = assistantSummary
        self.errorMessage = errorMessage
        self.startedAt = startedAt
        self.finishedAt = finishedAt
        self.conversationID = conversationID
    }
}

// MARK: - Gremlins

public enum GremlinRunStatus: String, Codable, CaseIterable, Hashable, Sendable {
    case success, failed, running, skipped
}

public struct GremlinItem: Codable, Identifiable, Hashable, Sendable {
    public let id: String           // slug, e.g. "morning-brief"
    public let name: String
    public let emoji: String
    public let prompt: String
    public let scheduleRaw: String  // e.g. "daily 08:00"
    public let isEnabled: Bool
    public let sourceType: String   // "chat" | "manual"
    public let createdAt: String    // yyyy-MM-dd string
    public let workflowID: String?
    public let workflowInputValues: [String: String]?
    public var nextRunAt: Date?
    public var lastRunAt: Date?
    public var lastRunStatus: GremlinRunStatus?
    /// When set, the output of each successful run is delivered to this communication destination.
    public let communicationDestination: CommunicationDestination?
    /// Legacy compatibility field preserved for older GREMLINS.md entries and older UIs.
    public let telegramChatID: Int64?
    // v2 fields
    public let gremlinDescription: String?   // human summary, separate from prompt
    public let tags: [String]                // grouping labels
    public let maxRetries: Int               // auto-retry on failure (default 0)
    public let timeoutSeconds: Int?          // kill hung runs after N seconds (nil = no limit)
    public let lastModifiedAt: String?       // yyyy-MM-dd string, updated on every save

    public init(id: String, name: String, emoji: String = "⚡", prompt: String,
                scheduleRaw: String, isEnabled: Bool = true, sourceType: String = "manual",
                createdAt: String, workflowID: String? = nil,
                workflowInputValues: [String: String]? = nil, nextRunAt: Date? = nil,
                lastRunAt: Date? = nil, lastRunStatus: GremlinRunStatus? = nil,
                communicationDestination: CommunicationDestination? = nil,
                telegramChatID: Int64? = nil,
                gremlinDescription: String? = nil,
                tags: [String] = [],
                maxRetries: Int = 0,
                timeoutSeconds: Int? = nil,
                lastModifiedAt: String? = nil) {
        self.id = id; self.name = name; self.emoji = emoji; self.prompt = prompt
        self.scheduleRaw = scheduleRaw; self.isEnabled = isEnabled
        self.sourceType = sourceType; self.createdAt = createdAt
        self.workflowID = workflowID; self.workflowInputValues = workflowInputValues
        self.nextRunAt = nextRunAt; self.lastRunAt = lastRunAt; self.lastRunStatus = lastRunStatus
        self.communicationDestination = communicationDestination ?? telegramChatID.map {
            CommunicationDestination(platform: .telegram, channelID: String($0))
        }
        self.telegramChatID = telegramChatID ?? {
            guard communicationDestination?.platform == .telegram else { return nil }
            return communicationDestination.flatMap { Int64($0.channelID) }
        }()
        self.gremlinDescription = gremlinDescription
        self.tags = tags
        self.maxRetries = max(0, maxRetries)
        self.timeoutSeconds = timeoutSeconds
        self.lastModifiedAt = lastModifiedAt
    }
}

public struct GremlinRun: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let gremlinID: String
    public let startedAt: Date
    public let finishedAt: Date?
    public let status: GremlinRunStatus
    public let output: String?
    public let errorMessage: String?
    public let conversationID: UUID?
    public let workflowRunID: UUID?

    public init(id: UUID = UUID(), gremlinID: String, startedAt: Date = .now,
                finishedAt: Date? = nil, status: GremlinRunStatus,
                output: String? = nil, errorMessage: String? = nil,
                conversationID: UUID? = nil, workflowRunID: UUID? = nil) {
        self.id = id; self.gremlinID = gremlinID; self.startedAt = startedAt
        self.finishedAt = finishedAt; self.status = status
        self.output = output; self.errorMessage = errorMessage
        self.conversationID = conversationID; self.workflowRunID = workflowRunID
    }
}

// MARK: - Gremlin schedule validation

public struct GremlinScheduleValidation: Codable, Sendable {
    public let isValid: Bool
    public let nextFireDates: [Date]   // next 3 projected fire times (empty if invalid)
    public let errorMessage: String?

    public init(isValid: Bool, nextFireDates: [Date], errorMessage: String?) {
        self.isValid = isValid
        self.nextFireDates = nextFireDates
        self.errorMessage = errorMessage
    }
}

// MARK: - SKILLS.md

public struct LearnedRoutine: Codable, Hashable, Sendable {
    public let name: String
    public let triggers: [String]
    public let steps: [String]
    public let learnedAt: Date
    public let confirmedCount: Int

    public init(name: String, triggers: [String], steps: [String],
                learnedAt: Date = .now, confirmedCount: Int = 1) {
        self.name = name; self.triggers = triggers; self.steps = steps
        self.learnedAt = learnedAt; self.confirmedCount = confirmedCount
    }
}

// MARK: - Dashboards

/// Constrained set of widget types for schema-driven rendering.
public enum DashboardWidgetType: String, Codable, Sendable {
    case statCard = "stat_card"
    case summary  = "summary"
    case list     = "list"
    case table    = "table"
    case form     = "form"
    case search   = "search"
}

/// A field definition for form and search widgets.
public struct WidgetField: Codable, Sendable {
    public let key: String
    public let label: String
    /// "text" | "number" | "select" | "date"
    public let type: String
    public let required: Bool
    public let options: [String]?

    public init(key: String, label: String, type: String, required: Bool, options: [String]? = nil) {
        self.key = key
        self.label = label
        self.type = type
        self.required = required
        self.options = options
    }
}

public struct DashboardWidgetBinding: Codable, Sendable, Hashable {
    public let valuePath: String?
    public let itemsPath: String?
    public let rowsPath: String?
    public let primaryTextPath: String?
    public let secondaryTextPath: String?
    public let tertiaryTextPath: String?
    public let linkPath: String?
    public let imagePath: String?
    public let summaryPath: String?

    public init(
        valuePath: String? = nil,
        itemsPath: String? = nil,
        rowsPath: String? = nil,
        primaryTextPath: String? = nil,
        secondaryTextPath: String? = nil,
        tertiaryTextPath: String? = nil,
        linkPath: String? = nil,
        imagePath: String? = nil,
        summaryPath: String? = nil
    ) {
        self.valuePath = valuePath
        self.itemsPath = itemsPath
        self.rowsPath = rowsPath
        self.primaryTextPath = primaryTextPath
        self.secondaryTextPath = secondaryTextPath
        self.tertiaryTextPath = tertiaryTextPath
        self.linkPath = linkPath
        self.imagePath = imagePath
        self.summaryPath = summaryPath
    }
}

public struct DashboardDisplayItem: Codable, Sendable, Hashable {
    public let primaryText: String
    public let secondaryText: String?
    public let tertiaryText: String?
    public let linkURL: String?
    public let imageURL: String?

    public init(
        primaryText: String,
        secondaryText: String? = nil,
        tertiaryText: String? = nil,
        linkURL: String? = nil,
        imageURL: String? = nil
    ) {
        self.primaryText = primaryText
        self.secondaryText = secondaryText
        self.tertiaryText = tertiaryText
        self.linkURL = linkURL
        self.imageURL = imageURL
    }
}

public struct DashboardDisplayTableRow: Codable, Sendable, Hashable {
    public let values: [String]

    public init(values: [String]) {
        self.values = values
    }
}

public struct DashboardDisplayPayload: Codable, Sendable, Hashable {
    public let value: String?
    public let summary: String?
    public let items: [DashboardDisplayItem]?
    public let rows: [DashboardDisplayTableRow]?

    public init(
        value: String? = nil,
        summary: String? = nil,
        items: [DashboardDisplayItem]? = nil,
        rows: [DashboardDisplayTableRow]? = nil
    ) {
        self.value = value
        self.summary = summary
        self.items = items
        self.rows = rows
    }
}

/// A single widget in a dashboard spec.
public struct DashboardWidget: Codable, Sendable {
    public let id: String
    public let type: DashboardWidgetType
    public let title: String
    /// Must reference an installed skill.
    public let skillID: String
    /// The skill action name to invoke (e.g. "getWeather", "search"). Required for executable widgets.
    public let action: String?
    /// Dot-path into the skill result to extract the display value (e.g. "current.temperature").
    /// If nil, the full result string is used.
    public let dataKey: String?
    /// Default inputs passed to the skill action on auto-fetch. JSON-serializable key-value pairs.
    public var defaultInputs: [String: String]?
    /// v2 binding contract used to map structured skill output into a widget-ready display model.
    public let binding: DashboardWidgetBinding?
    /// For form / search widgets.
    public let fields: [WidgetField]?
    /// For table widgets.
    public let columns: [String]?
    public let emptyMessage: String?

    public init(
        id: String,
        type: DashboardWidgetType,
        title: String,
        skillID: String,
        action: String? = nil,
        dataKey: String? = nil,
        defaultInputs: [String: String]? = nil,
        binding: DashboardWidgetBinding? = nil,
        fields: [WidgetField]? = nil,
        columns: [String]? = nil,
        emptyMessage: String? = nil
    ) {
        self.id = id
        self.type = type
        self.title = title
        self.skillID = skillID
        self.action = action
        self.dataKey = dataKey
        self.defaultInputs = defaultInputs
        self.binding = binding
        self.fields = fields
        self.columns = columns
        self.emptyMessage = emptyMessage
    }

    // Custom decoder for backward compat: defaultInputs is nil if absent in stored JSON.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id            = try c.decode(String.self, forKey: .id)
        type          = try c.decode(DashboardWidgetType.self, forKey: .type)
        title         = try c.decode(String.self, forKey: .title)
        skillID       = try c.decode(String.self, forKey: .skillID)
        action        = try c.decodeIfPresent(String.self, forKey: .action)
        dataKey       = try c.decodeIfPresent(String.self, forKey: .dataKey)
        defaultInputs = try c.decodeIfPresent([String: String].self, forKey: .defaultInputs)
        binding       = try c.decodeIfPresent(DashboardWidgetBinding.self, forKey: .binding)
        fields        = try c.decodeIfPresent([WidgetField].self, forKey: .fields)
        columns       = try c.decodeIfPresent([String].self, forKey: .columns)
        emptyMessage  = try c.decodeIfPresent(String.self, forKey: .emptyMessage)
    }
}

// MARK: - WidgetExecutionResult

/// The result of executing a single dashboard widget's skill action.
public struct WidgetExecutionResult: Codable, Sendable {
    public let widgetID: String
    /// The full raw string output from the skill.
    public let rawOutput: String
    /// The value extracted via dot-path (widget.dataKey), or nil if no dataKey / path failed.
    public let extractedValue: String?
    /// v2 renderer-friendly normalized payload.
    public let displayPayload: DashboardDisplayPayload?
    public let success: Bool
    public let error: String?

    public init(
        widgetID: String,
        rawOutput: String,
        extractedValue: String? = nil,
        displayPayload: DashboardDisplayPayload? = nil,
        success: Bool,
        error: String? = nil
    ) {
        self.widgetID = widgetID
        self.rawOutput = rawOutput
        self.extractedValue = extractedValue
        self.displayPayload = displayPayload
        self.success = success
        self.error = error
    }
}

// MARK: - DashboardExecutionError

public enum DashboardExecutionError: LocalizedError, Sendable {
    case widgetNotFound(String)
    case widgetNotExecutable(String)   // missing action
    case skillExecutionFailed(String)
    case extractionFailed(String)      // dataKey path not found — non-fatal log only

    public var errorDescription: String? {
        switch self {
        case .widgetNotFound(let id):
            return "Widget '\(id)' not found in the dashboard."
        case .widgetNotExecutable(let id):
            return "Widget '\(id)' has no action configured and cannot be executed."
        case .skillExecutionFailed(let msg):
            return "Skill execution failed: \(msg)"
        case .extractionFailed(let path):
            return "Data extraction failed for path '\(path)'."
        }
    }
}

/// The full dashboard definition.
public struct DashboardSpec: Codable, Sendable {
    public let id: String
    public let title: String
    /// SF Symbol name used as the icon.
    public let icon: String
    public let description: String
    public let sourceSkillIDs: [String]
    public let widgets: [DashboardWidget]
    public let emptyState: String?
    /// Whether the user has pinned this dashboard to the top of the sidebar.
    public var isPinned: Bool
    /// Timestamp of the last time the user opened this dashboard.
    public var lastAccessedAt: Date?

    public init(
        id: String,
        title: String,
        icon: String,
        description: String,
        sourceSkillIDs: [String],
        widgets: [DashboardWidget],
        emptyState: String? = nil,
        isPinned: Bool = false,
        lastAccessedAt: Date? = nil
    ) {
        self.id = id
        self.title = title
        self.icon = icon
        self.description = description
        self.sourceSkillIDs = sourceSkillIDs
        self.widgets = widgets
        self.emptyState = emptyState
        self.isPinned = isPinned
        self.lastAccessedAt = lastAccessedAt
    }

    // Custom decoder so existing JSON without isPinned/lastAccessedAt still loads cleanly.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id             = try c.decode(String.self, forKey: .id)
        title          = try c.decode(String.self, forKey: .title)
        icon           = try c.decode(String.self, forKey: .icon)
        description    = try c.decode(String.self, forKey: .description)
        sourceSkillIDs = try c.decode([String].self, forKey: .sourceSkillIDs)
        widgets        = try c.decode([DashboardWidget].self, forKey: .widgets)
        emptyState     = try c.decodeIfPresent(String.self, forKey: .emptyState)
        isPinned       = (try? c.decode(Bool.self, forKey: .isPinned)) ?? false
        lastAccessedAt = try? c.decode(Date.self, forKey: .lastAccessedAt)
    }
}

/// Lifecycle status of a dashboard proposal.
public enum DashboardProposalStatus: String, Codable, Sendable {
    case pending   = "pending"
    case installed = "installed"
    case rejected  = "rejected"
}

/// A proposal wrapping a DashboardSpec — mirrors the ForgeProposal pattern.
public struct DashboardProposal: Codable, Sendable, Identifiable {
    public let proposalID: String
    public let spec: DashboardSpec
    public let summary: String
    public let rationale: String
    public let linkedSkillID: String?
    public let linkedProposalID: String?
    public var status: DashboardProposalStatus
    public let createdAt: Date

    public var id: String { proposalID }

    public init(
        proposalID: String = UUID().uuidString,
        spec: DashboardSpec,
        summary: String,
        rationale: String,
        linkedSkillID: String? = nil,
        linkedProposalID: String? = nil,
        status: DashboardProposalStatus = .pending,
        createdAt: Date = .now
    ) {
        self.proposalID = proposalID
        self.spec = spec
        self.summary = summary
        self.rationale = rationale
        self.linkedSkillID = linkedSkillID
        self.linkedProposalID = linkedProposalID
        self.status = status
        self.createdAt = createdAt
    }
}

// MARK: - API Validation Core

/// Where an example input for API validation came from.
public enum ExampleInputSource: String, Codable, Sendable {
    case provided  // user/planner explicitly supplied
    case catalog   // internal safe-defaults catalog
    case generated // constrained model-generated fallback
}

/// A single candidate example call used during API validation.
public struct ExampleInput: Codable, Sendable {
    public let name: String
    public let inputs: [String: String]
    public let source: ExampleInputSource

    public init(name: String, inputs: [String: String], source: ExampleInputSource) {
        self.name = name
        self.inputs = inputs
        self.source = source
    }
}

/// What the validation service recommends doing with this API.
public enum APIValidationRecommendation: String, Codable, Sendable {
    case usable          // response is good, proceed to Forge
    case needsRevision   = "needs_revision"  // might work with adjusted params
    case reject          // do not proceed
    case skipped         // validation not applicable (e.g. non-GET plan) — treated as pass-through
}

/// Why validation failed, if it did.
public enum APIValidationFailureCategory: String, Codable, Sendable {
    case invalidRequestShape   = "invalid_request_shape"
    case missingCredentials    = "missing_credentials"
    case unsupportedAuth       = "unsupported_auth"
    case networkFailure        = "network_failure"
    case httpError             = "http_error"
    case emptyResponse         = "empty_response"
    case missingExpectedFields = "missing_expected_fields"
    case unusableResponse      = "unusable_response"
    case unknown               = "unknown"
}

/// The result produced by APIValidationService.
public struct APIValidationResult: Codable, Sendable {
    public let success: Bool
    public let confidence: Double            // 0.0–1.0
    public let exampleUsed: ExampleInput?
    public let requestSummary: String        // e.g. "GET https://api.example.com/v1/data"
    public let responsePreview: String?      // trimmed, safe preview (no secrets)
    public let extractedFields: [String]     // top-level field names found in response
    public let failureCategory: APIValidationFailureCategory?
    public let failureReason: String?
    public let recommendation: APIValidationRecommendation
    /// How many candidate execution attempts were used (1 or 2). 1 = immediate result.
    public let attemptsCount: Int

    public init(
        success: Bool,
        confidence: Double,
        exampleUsed: ExampleInput?,
        requestSummary: String,
        responsePreview: String?,
        extractedFields: [String],
        failureCategory: APIValidationFailureCategory?,
        failureReason: String?,
        recommendation: APIValidationRecommendation,
        attemptsCount: Int = 1
    ) {
        self.success = success
        self.confidence = confidence
        self.exampleUsed = exampleUsed
        self.requestSummary = requestSummary
        self.responsePreview = responsePreview
        self.extractedFields = extractedFields
        self.failureCategory = failureCategory
        self.failureReason = failureReason
        self.recommendation = recommendation
        self.attemptsCount = attemptsCount
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        success          = try c.decode(Bool.self, forKey: .success)
        confidence       = try c.decode(Double.self, forKey: .confidence)
        exampleUsed      = try c.decodeIfPresent(ExampleInput.self, forKey: .exampleUsed)
        requestSummary   = try c.decode(String.self, forKey: .requestSummary)
        responsePreview  = try c.decodeIfPresent(String.self, forKey: .responsePreview)
        extractedFields  = (try? c.decodeIfPresent([String].self, forKey: .extractedFields)) ?? []
        failureCategory  = try c.decodeIfPresent(APIValidationFailureCategory.self, forKey: .failureCategory)
        failureReason    = try c.decodeIfPresent(String.self, forKey: .failureReason)
        recommendation   = try c.decode(APIValidationRecommendation.self, forKey: .recommendation)
        attemptsCount    = (try? c.decodeIfPresent(Int.self, forKey: .attemptsCount)) ?? 1
    }
}

/// A lightweight audit record for a past validation run.
public struct APIValidationAuditRecord: Codable, Sendable {
    public let id: String
    public let providerName: String
    public let endpoint: String
    public let exampleUsed: ExampleInput?
    public let confidence: Double
    public let recommendation: APIValidationRecommendation
    public let failureCategory: APIValidationFailureCategory?
    public let responsePreviewTrimmed: String?   // max 200 chars, no secrets
    public let timestamp: Date

    public init(
        id: String,
        providerName: String,
        endpoint: String,
        exampleUsed: ExampleInput?,
        confidence: Double,
        recommendation: APIValidationRecommendation,
        failureCategory: APIValidationFailureCategory?,
        responsePreviewTrimmed: String?,
        timestamp: Date
    ) {
        self.id = id
        self.providerName = providerName
        self.endpoint = endpoint
        self.exampleUsed = exampleUsed
        self.confidence = confidence
        self.recommendation = recommendation
        self.failureCategory = failureCategory
        self.responsePreviewTrimmed = responsePreviewTrimmed
        self.timestamp = timestamp
    }
}

// MARK: - MIND.md

public struct MindTurnRecord: Sendable {
    public let conversationID: UUID
    public let userMessage: String
    public let assistantResponse: String
    public let toolCallSummaries: [String]
    public let toolResultSummaries: [String]
    public let timestamp: Date

    public init(conversationID: UUID, userMessage: String, assistantResponse: String,
                toolCallSummaries: [String] = [], toolResultSummaries: [String] = [],
                timestamp: Date = .now) {
        self.conversationID = conversationID; self.userMessage = userMessage
        self.assistantResponse = assistantResponse
        self.toolCallSummaries = toolCallSummaries
        self.toolResultSummaries = toolResultSummaries
        self.timestamp = timestamp
    }
}

// MARK: - Multi-Agent Orchestration

/// A single decomposed sub-task produced by the supervisor's planning step.
public struct AgentTask: Codable, Identifiable, Sendable {
    public let id: UUID
    /// Short human-readable label used in SSE progress events.
    public let title: String
    /// The full prompt the worker agent receives as its user message.
    public let prompt: String
    /// Skill IDs the worker is allowed to invoke. nil = all enabled skills.
    public let allowedSkillIDs: [String]?

    public init(id: UUID = UUID(), title: String, prompt: String, allowedSkillIDs: [String]? = nil) {
        self.id = id
        self.title = title
        self.prompt = prompt
        self.allowedSkillIDs = allowedSkillIDs
    }
}

/// The output produced by a single worker agent.
public struct AgentTaskResult: Codable, Identifiable, Sendable {
    public let id: UUID
    public let taskID: UUID
    public let taskTitle: String
    public let output: String
    public let conversationID: UUID
    public let success: Bool
    public let errorMessage: String?

    public init(
        id: UUID = UUID(),
        taskID: UUID,
        taskTitle: String,
        output: String,
        conversationID: UUID,
        success: Bool,
        errorMessage: String? = nil
    ) {
        self.id = id
        self.taskID = taskID
        self.taskTitle = taskTitle
        self.output = output
        self.conversationID = conversationID
        self.success = success
        self.errorMessage = errorMessage
    }
}

/// The supervisor's decomposition plan: a list of parallel tasks and a synthesis
/// prompt template used to combine their results into a unified response.
public struct MultiAgentPlan: Codable, Sendable {
    public let tasks: [AgentTask]
    /// Injected into the synthesis LLM call alongside the collected worker outputs.
    public let synthesisContext: String

    public init(tasks: [AgentTask], synthesisContext: String = "") {
        self.tasks = tasks
        self.synthesisContext = synthesisContext
    }

    /// A trivial single-task fallback plan wrapping the original user request.
    public static func singleTask(prompt: String) -> MultiAgentPlan {
        MultiAgentPlan(
            tasks: [AgentTask(title: "Main task", prompt: prompt)],
            synthesisContext: ""
        )
    }
}

// MARK: - Conversation History

/// Lightweight summary of a conversation used in list and search views.
/// Does not include the full message array — use `ConversationDetail` for that.
public struct ConversationSummary: Codable, Identifiable, Sendable {
    public let id: UUID
    public let messageCount: Int
    /// Text of the first user message in the conversation, truncated to 200 chars.
    public let firstUserMessage: String?
    /// Text of the last assistant message in the conversation, truncated to 200 chars.
    public let lastAssistantMessage: String?
    public let createdAt: Date
    public let updatedAt: Date
    /// Platform context string (e.g. "telegram", "discord") if the conversation originated from a bridge.
    public let platformContext: String?

    public init(
        id: UUID,
        messageCount: Int,
        firstUserMessage: String?,
        lastAssistantMessage: String?,
        createdAt: Date,
        updatedAt: Date,
        platformContext: String? = nil
    ) {
        self.id = id
        self.messageCount = messageCount
        self.firstUserMessage = firstUserMessage
        self.lastAssistantMessage = lastAssistantMessage
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.platformContext = platformContext
    }
}

/// Full conversation detail including all messages. Used when opening a specific conversation.
public struct ConversationDetail: Codable, Identifiable, Sendable {
    public let id: UUID
    public let messageCount: Int
    public let firstUserMessage: String?
    public let lastAssistantMessage: String?
    public let createdAt: Date
    public let updatedAt: Date
    public let platformContext: String?
    public let messages: [AtlasMessage]

    public init(
        id: UUID,
        messageCount: Int,
        firstUserMessage: String?,
        lastAssistantMessage: String?,
        createdAt: Date,
        updatedAt: Date,
        platformContext: String? = nil,
        messages: [AtlasMessage]
    ) {
        self.id = id
        self.messageCount = messageCount
        self.firstUserMessage = firstUserMessage
        self.lastAssistantMessage = lastAssistantMessage
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.platformContext = platformContext
        self.messages = messages
    }
}

// MARK: - Web Session

/// A persisted browser session record, stored in the `web_sessions` SQLite table.
public struct WebSessionRecord: Sendable {
    public let id: String
    public let createdAt: Date
    public let refreshedAt: Date
    public let expiresAt: Date
    /// `true` for sessions created by remote devices via API key authentication.
    public let isRemote: Bool

    public var isValid: Bool { Date() < expiresAt }

    public init(id: String, createdAt: Date, refreshedAt: Date, expiresAt: Date, isRemote: Bool = false) {
        self.id = id
        self.createdAt = createdAt
        self.refreshedAt = refreshedAt
        self.expiresAt = expiresAt
        self.isRemote = isRemote
    }
}

// MARK: - Integration Registry

/// Describes a named third-party integration (API key-gated capability).
/// Used by AtlasConfig.integrations() and injected into the system prompt.
public struct AtlasIntegration: Sendable {
    public let id: String
    public let name: String
    public let category: String
    public let description: String
    public let isConfigured: Bool
    public let setupHint: String

    public init(
        id: String,
        name: String,
        category: String,
        description: String,
        isConfigured: Bool,
        setupHint: String
    ) {
        self.id = id
        self.name = name
        self.category = category
        self.description = description
        self.isConfigured = isConfigured
        self.setupHint = setupHint
    }
}
