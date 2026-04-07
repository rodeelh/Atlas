import Foundation
import AtlasGuard
import AtlasLogging
import AtlasShared

public struct SkillValidationContext: Sendable {
    public let config: AtlasConfig
    public let logger: AtlasLogger

    public init(config: AtlasConfig, logger: AtlasLogger) {
        self.config = config
        self.logger = logger
    }
}

public struct SkillExecutionContext: Sendable {
    public let conversationID: UUID?
    public let logger: AtlasLogger
    public let config: AtlasConfig
    public let permissionManager: PermissionManager
    public let workflowExecution: AtlasWorkflowExecutionContext?
    public let runtimeStatusProvider: @Sendable () async -> AtlasRuntimeStatus?
    public let enabledSkillsProvider: @Sendable () async -> [AtlasSkillRecord]
    public let memoryItemsProvider: @Sendable () async -> [MemoryItem]

    public init(
        conversationID: UUID?,
        logger: AtlasLogger,
        config: AtlasConfig,
        permissionManager: PermissionManager,
        workflowExecution: AtlasWorkflowExecutionContext? = nil,
        runtimeStatusProvider: @escaping @Sendable () async -> AtlasRuntimeStatus?,
        enabledSkillsProvider: @escaping @Sendable () async -> [AtlasSkillRecord],
        memoryItemsProvider: @escaping @Sendable () async -> [MemoryItem] = { [] }
    ) {
        self.conversationID = conversationID
        self.logger = logger
        self.config = config
        self.permissionManager = permissionManager
        self.workflowExecution = workflowExecution
        self.runtimeStatusProvider = runtimeStatusProvider
        self.enabledSkillsProvider = enabledSkillsProvider
        self.memoryItemsProvider = memoryItemsProvider
    }
}

public struct SkillExecutionRequest: Sendable {
    public let skillID: String
    public let actionID: String
    public let input: AtlasToolInput
    public let conversationID: UUID?
    public let toolCallID: UUID

    public init(
        skillID: String,
        actionID: String,
        input: AtlasToolInput,
        conversationID: UUID?,
        toolCallID: UUID
    ) {
        self.skillID = skillID
        self.actionID = actionID
        self.input = input
        self.conversationID = conversationID
        self.toolCallID = toolCallID
    }
}

public struct SkillExecutionResult: Codable, Hashable, Sendable {
    public let skillID: String
    public let actionID: String
    public let output: String
    public let success: Bool
    public let summary: String
    public let metadata: [String: String]

    public init(
        skillID: String,
        actionID: String,
        output: String,
        success: Bool = true,
        summary: String,
        metadata: [String: String] = [:]
    ) {
        self.skillID = skillID
        self.actionID = actionID
        self.output = output
        self.success = success
        self.summary = summary
        self.metadata = metadata
    }
}

public struct AtlasSkillRecord: Codable, Identifiable, Hashable, Sendable {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]
    public let validation: SkillValidationResult?

    public init(
        manifest: SkillManifest,
        actions: [SkillActionDefinition],
        validation: SkillValidationResult?
    ) {
        self.manifest = manifest
        self.actions = actions
        self.validation = validation
    }

    public var id: String {
        manifest.id
    }

    public var isEnabled: Bool {
        manifest.lifecycleState == .enabled
    }
}

public protocol AtlasSkill: Sendable {
    var manifest: SkillManifest { get }
    var actions: [SkillActionDefinition] { get }

    func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult
    func execute(actionID: String, input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult
}

public extension AtlasSkill {
    func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        SkillValidationResult(
            skillID: manifest.id,
            status: .passed,
            summary: "No additional configuration is required."
        )
    }
}
