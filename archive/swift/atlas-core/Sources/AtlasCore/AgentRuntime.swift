import Foundation
import NIOCore
import NIOHTTP1
import NIOPosix
import AtlasBridges
import AtlasLogging
import AtlasNetwork
import AtlasSkills
import AtlasShared
import AtlasTools

// MARK: - SSE Types

/// An SSE event emitted by the StreamBroadcaster.
public struct SSEEvent: Sendable {
    public enum EventType: String, Sendable {
        case token
        case toolCall = "tool_call"
        case approvalRequired = "approval_required"
        case done
        case error
        // Streaming events (v1)
        case assistantStarted = "assistant_started"
        case assistantDelta = "assistant_delta"
        case assistantDone = "assistant_done"
        case toolStarted = "tool_started"
        case toolFinished = "tool_finished"
        case toolFailed = "tool_failed"
    }

    public let type: EventType
    public let content: String?
    public let status: String?
    public let toolName: String?
    public let toolArgs: String?
    public let errorMessage: String?

    public static func token(_ content: String) -> SSEEvent {
        SSEEvent(type: .token, content: content, status: nil, toolName: nil, toolArgs: nil, errorMessage: nil)
    }

    public static func toolCall(name: String, args: String) -> SSEEvent {
        SSEEvent(type: .toolCall, content: nil, status: nil, toolName: name, toolArgs: args, errorMessage: nil)
    }

    public static func approvalRequired(toolName: String) -> SSEEvent {
        SSEEvent(type: .approvalRequired, content: nil, status: nil, toolName: toolName, toolArgs: nil, errorMessage: nil)
    }

    public static func done(status: String) -> SSEEvent {
        SSEEvent(type: .done, content: nil, status: status, toolName: nil, toolArgs: nil, errorMessage: nil)
    }

    public static func error(_ message: String) -> SSEEvent {
        SSEEvent(type: .error, content: nil, status: nil, toolName: nil, toolArgs: nil, errorMessage: message)
    }

    public static func assistantStarted() -> SSEEvent {
        SSEEvent(type: .assistantStarted, content: nil, status: nil, toolName: nil, toolArgs: nil, errorMessage: nil)
    }

    public static func assistantDelta(_ delta: String) -> SSEEvent {
        SSEEvent(type: .assistantDelta, content: delta, status: nil, toolName: nil, toolArgs: nil, errorMessage: nil)
    }

    public static func assistantDone() -> SSEEvent {
        SSEEvent(type: .assistantDone, content: nil, status: nil, toolName: nil, toolArgs: nil, errorMessage: nil)
    }

    public static func toolStarted(name: String) -> SSEEvent {
        SSEEvent(type: .toolStarted, content: nil, status: nil, toolName: name, toolArgs: nil, errorMessage: nil)
    }

    public static func toolFinished(name: String) -> SSEEvent {
        SSEEvent(type: .toolFinished, content: nil, status: nil, toolName: name, toolArgs: nil, errorMessage: nil)
    }

    public static func toolFailed(name: String) -> SSEEvent {
        SSEEvent(type: .toolFailed, content: nil, status: nil, toolName: name, toolArgs: nil, errorMessage: nil)
    }

    public func toSSEData() -> String {
        var dict: [String: String] = ["type": type.rawValue]
        if let c = content { dict["content"] = c }
        if let s = status { dict["status"] = s }
        if let n = toolName { dict["toolName"] = n }
        if let a = toolArgs { dict["args"] = a }
        if let e = errorMessage { dict["message"] = e }

        // SSE requires single-line data — use a compact encoder, not the pretty-printed AtlasJSON.encoder
        let compactEncoder = JSONEncoder()
        guard let data = try? compactEncoder.encode(dict),
              let json = String(data: data, encoding: .utf8) else {
            return "data: {\"type\":\"error\",\"message\":\"Encoding failure.\"}\n\n"
        }
        return "data: \(json)\n\n"
    }
}

// MARK: - StreamBroadcaster

/// Holds active SSE stream continuations keyed by conversation ID.
public actor StreamBroadcaster {
    private var continuations: [UUID: AsyncStream<SSEEvent>.Continuation] = [:]

    public init() {}

    /// Register a continuation for a conversation. Returns the AsyncStream to read from.
    public func register(conversationID: UUID) -> AsyncStream<SSEEvent> {
        var continuation: AsyncStream<SSEEvent>.Continuation!
        let stream = AsyncStream<SSEEvent> { cont in
            continuation = cont
        }
        continuations[conversationID] = continuation
        return stream
    }

    /// Remove the continuation for a conversation (called on client disconnect).
    public func remove(conversationID: UUID) {
        continuations[conversationID]?.finish()
        continuations.removeValue(forKey: conversationID)
    }

    /// Emit an event to all listeners for this conversation.
    public func emit(_ event: SSEEvent, conversationID: UUID) {
        continuations[conversationID]?.yield(event)
    }

    /// Finish (close) the stream for a conversation.
    public func finish(conversationID: UUID) {
        continuations[conversationID]?.finish()
        continuations.removeValue(forKey: conversationID)
    }
}

public actor AgentRuntime: AtlasRuntimeHandling {
    private var context: AgentContext
    private var loop: AgentLoop

    /// Shared broadcaster for SSE streaming. Accessible to AgentLoop via AgentContext.
    public let streamBroadcaster = StreamBroadcaster()

    private var server: RuntimeHTTPServer?
    private let communicationManager: CommunicationManager
    private var state: AtlasRuntimeState = .stopped
    fileprivate var boundPort: Int = 0
    private var startedAt: Date?
    private var lastMessageAt: Date?
    private var activeRequests = 0
    private var lastError: String?
    private var skillsBootstrapped = false
    private var gremlinScheduler: GremlinScheduler?
    private var webAuthService: WebAuthService

    /// Internal CoreSkills runtime — available after bootstrap.
    /// Used by Forge and internal operations. Not part of the user-facing skill registry.
    public private(set) var coreSkills: CoreSkillsRuntime?

    /// Forge proposal service — available after bootstrap.
    private var forgeProposalService: ForgeProposalService?

    /// Dashboard store — persists proposals and installed dashboards.
    private let dashboardStore = DashboardStore()

    /// API validation store — persists audit records from API validation runs.
    private let apiValidationStore = APIValidationStore()
    private let workflowStore: WorkflowStore
    private var workflowRunner: WorkflowRunner

    public init(context: AgentContext? = nil) throws {
        let resolvedContext = try context ?? AgentContext()
        self.context = resolvedContext
        let workflowStore = WorkflowStore()
        self.workflowStore = workflowStore
        self.workflowRunner = WorkflowRunner(context: resolvedContext, store: workflowStore)
        self.loop = AgentLoop(context: resolvedContext)
        self.communicationManager = CommunicationManager(context: resolvedContext)
        self.webAuthService = WebAuthService(memoryStore: resolvedContext.memoryStore)
    }

    public func start() async throws {
        guard server == nil else { return }

        // Fetch best available models before anything uses OpenAI.
        // Failure is safe — ModelSelector keeps its hardcoded fallbacks.
        await context.modelSelector.refresh()

        await context.toolRegistry.registerDefaultTools()
        try await bootstrapSkillsIfNeeded()
        await context.deferredExecutionManager.hydrate()
        await loadMindIfNeeded()
        await loadSkillsMemoryIfNeeded()

        // Start Gremlin scheduler
        let scheduler = GremlinScheduler(
            fileStore: context.gremlinsFileStore,
            context: context,
            workflowRunner: workflowRunner,
            logger: context.logger
        )
        self.gremlinScheduler = scheduler
        await context.gremlinManagingAdapter.setScheduler(scheduler)
        let gremlins = (try? await context.gremlinsFileStore.loadGremlins()) ?? []
        await scheduler.start(gremlins: gremlins)

        state = .starting

        // Try the configured port first, then up to 5 alternates if it's taken.
        let preferredPort = context.config.runtimePort
        let bindHost = context.config.remoteAccessEnabled ? "0.0.0.0" : "127.0.0.1"
        var lastError: Error?
        var boundServer: RuntimeHTTPServer?
        var actualPort = preferredPort
        for candidate in preferredPort...(preferredPort + 5) {
            let candidate_server = RuntimeHTTPServer(
                host: bindHost,
                port: candidate,
                runtime: self
            )
            do {
                try candidate_server.start()
                boundServer = candidate_server
                actualPort = candidate
                break
            } catch {
                lastError = error
                context.logger.warning("AgentRuntime: port \(candidate) unavailable — \(error.localizedDescription)")
            }
        }

        guard let server = boundServer else {
            throw lastError!
        }

        self.server = server
        self.boundPort = actualPort
        self.startedAt = .now
        self.state = .ready
        self.lastError = nil

        if actualPort != preferredPort {
            context.logger.warning("AgentRuntime: preferred port \(preferredPort) was taken — bound to \(actualPort)")
        }
        context.logger.info("Atlas runtime started", metadata: [
            "host": "\(bindHost)",
            "port": "\(actualPort)",
            "remoteAccess": "\(context.config.remoteAccessEnabled)"
        ])

        await startCommunicationsIfNeeded()
    }

    public func stop() throws {
        Task {
            await communicationManager.stop()
        }
        Task {
            await gremlinScheduler?.cancelAll()
        }
        try server?.stop()
        server = nil
        gremlinScheduler = nil
        state = .stopped
        context.logger.info("Atlas runtime stopped")
    }

    public func handleMessage(_ request: AtlasMessageRequest) async -> AtlasMessageResponseEnvelope {
        let normalizedConversationID = request.conversationID ?? UUID()
        let normalizedRequest = AtlasMessageRequest(
            conversationID: normalizedConversationID,
            message: request.message,
            attachments: request.attachments
        )

        activeRequests += 1
        lastMessageAt = .now

        defer { activeRequests -= 1 }

        // Create an emitter that routes streaming events to the SSE broadcaster for this conversation.
        let broadcaster = streamBroadcaster
        let conversationID = normalizedConversationID
        let emitter: @Sendable (SSEEvent) async -> Void = { event in
            await broadcaster.emit(event, conversationID: conversationID)
        }

        do {
            let response = try await loop.process(normalizedRequest, emitter: emitter)
            lastError = response.response.errorMessage
            state = response.response.status == .failed ? .degraded : .ready
            context.logger.info("Atlas runtime produced response", metadata: [
                "conversation_id": response.conversation.id.uuidString,
                "status": response.response.status.rawValue
            ])

            // Emit the final SSE signals. Streaming already delivered text via assistant_delta events;
            // only the conversation-level done/approval signals need to be sent here.
            let responseStatus = response.response.status
            Task {
                if responseStatus == .waitingForApproval {
                    await broadcaster.emit(.approvalRequired(toolName: "pending"), conversationID: conversationID)
                    await broadcaster.emit(.done(status: "waitingForApproval"), conversationID: conversationID)
                    // Do NOT finish the stream — keep it alive so the resumed response
                    // can be pushed to the client after the user approves.
                } else {
                    await broadcaster.emit(.done(status: responseStatus.rawValue), conversationID: conversationID)
                    await broadcaster.finish(conversationID: conversationID)
                }
            }

            return response
        } catch {
            lastError = error.localizedDescription
            state = .degraded

            context.logger.error("Atlas runtime failed to process message", metadata: [
                "conversation_id": normalizedConversationID.uuidString,
                "error": error.localizedDescription
            ])

            do {
                try await context.eventLogStore.persist(
                    runtimeError: error.localizedDescription,
                    conversationID: normalizedConversationID
                )
            } catch {
                context.logger.error("Failed to persist runtime error", metadata: [
                    "conversation_id": normalizedConversationID.uuidString,
                    "error": error.localizedDescription
                ])
            }

            // Synthesize a human-readable message — never expose the raw Swift error.
            let humanMessage = Self.humanizeSystemError(error)
            let conversation = await appendFailureMessage(humanMessage, to: normalizedConversationID)
            let failureMessage = AtlasAgentResponse(
                assistantMessage: humanMessage,
                status: .failed,
                errorMessage: lastError
            )

            // Stream the fallback message so the frontend bubble is populated,
            // then close the turn. assistant_started is emitted first in case the
            // throw occurred before requestModelTurn reached that point.
            let conversationID = normalizedConversationID
            let broadcaster = streamBroadcaster
            Task {
                await broadcaster.emit(.assistantStarted(), conversationID: conversationID)
                await broadcaster.emit(.assistantDelta(humanMessage), conversationID: conversationID)
                await broadcaster.emit(.assistantDone(), conversationID: conversationID)
                await broadcaster.emit(.done(status: "failed"), conversationID: conversationID)
                await broadcaster.finish(conversationID: conversationID)
            }

            return AtlasMessageResponseEnvelope(conversation: conversation, response: failureMessage)
        }
    }

    public func status() async -> AtlasRuntimeStatus {
        let activeConversationCount = (try? await context.memoryStore.conversationCount()) ?? 0
        let pendingApprovalCount = await context.deferredExecutionManager.pendingApprovalCount()
        let telegramStatus = await communicationManager.legacyTelegramStatus()
        let communications = await communicationManager.statusSnapshot()

        return AtlasRuntimeStatus(
            isRunning: state == .ready || state == .degraded || state == .starting,
            activeConversationCount: activeConversationCount,
            lastMessageAt: lastMessageAt,
            lastError: lastError,
            state: state,
            runtimePort: boundPort > 0 ? boundPort : context.config.runtimePort,
            startedAt: startedAt,
            activeRequests: activeRequests,
            pendingApprovalCount: pendingApprovalCount,
            details: statusDescription,
            telegram: telegramStatus,
            communications: communications
        )
    }

    public func logs(limit: Int = 200) async -> [AtlasLogEntry] {
        await AtlasLogger.recentEntries(limit: limit)
    }

    public func approvals() async -> [ApprovalRequest] {
        await context.deferredExecutionManager.allApprovalRequests()
    }

    public func skills() async -> [AtlasSkillRecord] {
        await context.skillRegistry.listVisible()
    }

    // MARK: - Forge

    public func forgeResearching() async -> [ForgeResearchingItem] {
        await forgeProposalService?.listResearching() ?? []
    }

    @discardableResult
    public func forgeStartResearching(title: String, message: String) async -> UUID {
        guard let service = forgeProposalService else { return UUID() }
        let item = await service.startResearching(title: title, message: message)
        return item.id
    }

    public func forgeStopResearching(id: UUID) async {
        await forgeProposalService?.stopResearching(id: id)
    }

    public func forgeProposals() async throws -> [ForgeProposalRecord] {
        guard let service = forgeProposalService else { return [] }
        return try await service.listProposals()
    }

    public func forgeCreateProposal(
        spec: ForgeSkillSpec,
        plans: [ForgeActionPlan],
        summary: String,
        rationale: String?,
        contractJSON: String?
    ) async throws -> ForgeProposalRecord {
        guard let service = forgeProposalService else {
            throw RuntimeAPIError.invalidRequest("Forge service is not yet initialized.")
        }
        return try await service.createProposal(spec: spec, plans: plans, summary: summary, rationale: rationale, contractJSON: contractJSON)
    }

    public func forgeApproveProposal(id: UUID, enable: Bool) async throws -> ForgeProposalRecord {
        guard let service = forgeProposalService else {
            throw RuntimeAPIError.invalidRequest("Forge service is not yet initialized.")
        }
        return try await service.approveProposal(id: id, enable: enable)
    }

    public func forgeRejectProposal(id: UUID) async throws -> ForgeProposalRecord {
        guard let service = forgeProposalService else {
            throw RuntimeAPIError.invalidRequest("Forge service is not yet initialized.")
        }
        return try await service.rejectProposal(id: id)
    }

    public func forgeInstalledSkills() async -> [AtlasSkillRecord] {
        await forgeProposalService?.listInstalledForgedSkills() ?? []
    }

    public func forgeUninstallSkill(skillID: String) async throws {
        guard let service = forgeProposalService else {
            throw RuntimeAPIError.invalidRequest("Forge service is not yet initialized.")
        }
        try await service.uninstallForgeSkill(skillID: skillID)
    }

    // MARK: - Dashboards

    public func dashboardProposals() async -> [DashboardProposal] {
        await dashboardStore.listProposals()
    }

    public func createDashboardProposal(intent: String, skillIDs: [String]) async throws -> DashboardProposal {
        let actionCatalog = await context.skillRegistry.enabledActionCatalog()
        let model = await context.modelSelector.resolvedPrimaryModel()
        let planner = DashboardPlanner(
            openAI: context.aiClient,
            actionCatalog: actionCatalog,
            model: model
        )
        let proposal = try await planner.plan(intent: intent, skillIDs: skillIDs)
        try await dashboardStore.addProposal(proposal)
        return proposal
    }

    public func installDashboard(proposalID: String) async throws -> DashboardProposal {
        let proposals = await dashboardStore.listProposals()
        guard let proposal = proposals.first(where: { $0.proposalID == proposalID }) else {
            throw DashboardStoreError.proposalNotFound(proposalID)
        }
        let actionCatalog = await context.skillRegistry.enabledActionCatalog()
        let validation = DashboardValidator(actionCatalog: actionCatalog).validate(proposal.spec)
        guard validation.isValid else {
            throw DashboardPlannerError.validationFailed(validation.errors)
        }

        try await dashboardStore.install(proposalID: proposalID)
        let updatedProposals = await dashboardStore.listProposals()
        guard let updated = updatedProposals.first(where: { $0.proposalID == proposalID }) else {
            throw DashboardStoreError.proposalNotFound(proposalID)
        }
        return updated
    }

    public func rejectDashboard(proposalID: String) async throws -> DashboardProposal {
        try await dashboardStore.reject(proposalID: proposalID)
        let proposals = await dashboardStore.listProposals()
        guard let updated = proposals.first(where: { $0.proposalID == proposalID }) else {
            throw DashboardStoreError.proposalNotFound(proposalID)
        }
        return updated
    }

    public func installedDashboards() async -> [DashboardSpec] {
        await dashboardStore.listInstalled()
    }

    public func removeDashboard(dashboardID: String) async throws {
        try await dashboardStore.remove(dashboardID: dashboardID)
    }

    public func recordDashboardAccess(dashboardID: String) async throws {
        try await dashboardStore.recordAccess(dashboardID: dashboardID)
    }

    public func toggleDashboardPin(dashboardID: String) async throws -> DashboardSpec {
        try await dashboardStore.togglePin(dashboardID: dashboardID)
        let all = await dashboardStore.listInstalled()
        guard let updated = all.first(where: { $0.id == dashboardID }) else {
            throw DashboardStoreError.dashboardNotFound(dashboardID)
        }
        return updated
    }

    /// Execute a widget action for an installed dashboard and return the result.
    ///
    /// Security: the skillID is always sourced from the stored spec — never from user input.
    /// The caller only supplies dashboardID, widgetID, and optional override inputs.
    public func executeWidgetAction(
        dashboardID: String,
        widgetID: String,
        inputs: [String: String]
    ) async throws -> WidgetExecutionResult {
        let installed = await dashboardStore.listInstalled()
        guard let dashboard = installed.first(where: { $0.id == dashboardID }) else {
            throw DashboardStoreError.dashboardNotFound(dashboardID)
        }
        guard let widget = dashboard.widgets.first(where: { $0.id == widgetID }) else {
            throw DashboardExecutionError.widgetNotFound(widgetID)
        }

        let actionCatalog = await context.skillRegistry.enabledActionCatalog()
        let engine = DashboardExecutionEngine(
            gateway: context.skillExecutionGateway,
            actionCatalog: actionCatalog,
            openAI: context.aiClient,
            logger: context.logger
        )
        let skillContext = await skillExecutionContext(conversationID: nil)
        return await engine.execute(widget: widget, inputs: inputs, skillContext: skillContext)
    }

    // MARK: - API Validation History

    /// Returns recent API validation audit records, newest first.
    public func listAPIValidationHistory(limit: Int = 50) async -> [APIValidationAuditRecord] {
        await apiValidationStore.listRecent(limit: limit)
    }

    /// Appends an API validation audit record to the store.
    public func appendAPIValidationRecord(_ record: APIValidationAuditRecord) async {
        await apiValidationStore.append(record)
    }

    public func fileAccessRoots() async -> [ApprovedFileAccessRoot] {
        await context.fileAccessScopeStore.listRoots()
    }

    // MARK: - Conversation History

    public func conversationSummaries(limit: Int = 50, offset: Int = 0) async -> [ConversationSummary] {
        do {
            return try await context.memoryStore.listConversationSummaries(limit: limit, offset: offset)
        } catch {
            context.logger.error("Failed to list conversation summaries", metadata: ["error": error.localizedDescription])
            return []
        }
    }

    public func searchConversations(query: String, limit: Int = 50) async -> [ConversationSummary] {
        do {
            return try await context.memoryStore.searchConversations(query: query, limit: limit)
        } catch {
            context.logger.error("Failed to search conversations", metadata: ["error": error.localizedDescription])
            return []
        }
    }

    public func conversationDetail(id: UUID) async -> ConversationDetail? {
        do {
            guard let conv = try await context.memoryStore.fetchConversation(id: id) else { return nil }
            let firstUser      = conv.messages.first(where: { $0.role == .user })?.content.prefix(200).description
            let lastAssistant  = conv.messages.last(where: { $0.role == .assistant })?.content.prefix(200).description
            return ConversationDetail(
                id: conv.id,
                messageCount: conv.messages.count,
                firstUserMessage: firstUser,
                lastAssistantMessage: lastAssistant,
                createdAt: conv.createdAt,
                updatedAt: conv.updatedAt,
                platformContext: conv.platformContext,
                messages: conv.messages
            )
        } catch {
            context.logger.error("Failed to fetch conversation detail", metadata: ["id": id.uuidString, "error": error.localizedDescription])
            return nil
        }
    }

    public func memories(limit: Int = 500, category: MemoryCategory? = nil) async -> [MemoryItem] {
        do {
            return try await context.memoryStore.listMemories(limit: limit, category: category)
        } catch {
            context.logger.error("Failed to load Atlas memories", metadata: [
                "error": error.localizedDescription
            ])
            return []
        }
    }

    public func memory(id: UUID) async -> MemoryItem? {
        do {
            return try await context.memoryStore.fetchMemory(id: id)
        } catch {
            context.logger.error("Failed to load Atlas memory", metadata: [
                "memory_id": id.uuidString,
                "error": error.localizedDescription
            ])
            return nil
        }
    }

    public func searchMemories(
        query: String,
        category: MemoryCategory? = nil,
        limit: Int = 200
    ) async -> [MemoryItem] {
        do {
            return try await context.memoryStore.searchMemories(
                category: category,
                tag: nil,
                query: query,
                limit: limit
            )
        } catch {
            context.logger.error("Failed to search Atlas memories", metadata: [
                "category": category?.rawValue ?? "all",
                "error": error.localizedDescription
            ])
            return []
        }
    }

    public func createMemory(request: AtlasMemoryCreateRequest) async throws -> MemoryItem {
        let title = request.title.trimmingCharacters(in: .whitespacesAndNewlines)
        let content = request.content.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !title.isEmpty, !content.isEmpty else {
            throw RuntimeAPIError.invalidRequest("Memory title and content are required.")
        }

        let memory = MemoryItem(
            category: request.category,
            title: title,
            content: content,
            source: request.source,
            confidence: min(max(request.confidence, 0), 1),
            importance: min(max(request.importance, 0), 1),
            isUserConfirmed: request.isUserConfirmed,
            isSensitive: request.isSensitive,
            tags: request.tags
        )

        let saved = try await context.memoryStore.saveMemory(memory)
        context.logger.info("Created Atlas memory", metadata: [
            "memory_id": saved.id.uuidString,
            "category": saved.category.rawValue,
            "confirmed": saved.isUserConfirmed ? "true" : "false"
        ])

        return saved
    }

    public func updateMemory(id: UUID, request: AtlasMemoryUpdateRequest) async throws -> MemoryItem {
        guard let existing = await memory(id: id) else {
            throw RuntimeAPIError.notFound("Memory item not found.")
        }

        let title = request.title.trimmingCharacters(in: .whitespacesAndNewlines)
        let content = request.content.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !title.isEmpty, !content.isEmpty else {
            throw RuntimeAPIError.invalidRequest("Memory title and content are required.")
        }

        let updated = existing.updating(
            title: title,
            content: content,
            updatedAt: .now,
            isUserConfirmed: request.markAsConfirmed ? true : existing.isUserConfirmed
        )
        let saved = try await context.memoryStore.updateMemory(updated)

        context.logger.info("Updated Atlas memory", metadata: [
            "memory_id": saved.id.uuidString,
            "category": saved.category.rawValue,
            "confirmed": saved.isUserConfirmed ? "true" : "false"
        ])

        return saved
    }

    public func confirmMemory(id: UUID) async throws -> MemoryItem {
        guard let confirmed = try await context.memoryStore.markMemoryConfirmed(id: id, confirmed: true) else {
            throw RuntimeAPIError.notFound("Memory item not found.")
        }

        context.logger.info("Confirmed Atlas memory", metadata: [
            "memory_id": confirmed.id.uuidString,
            "category": confirmed.category.rawValue
        ])

        return confirmed
    }

    public func deleteMemory(id: UUID) async throws -> MemoryItem {
        guard let existing = await memory(id: id) else {
            throw RuntimeAPIError.notFound("Memory item not found.")
        }

        try await context.memoryStore.deleteMemory(id: id)

        context.logger.info("Deleted Atlas memory", metadata: [
            "memory_id": existing.id.uuidString,
            "category": existing.category.rawValue
        ])

        return existing
    }

    public func addFileAccessRoot(bookmarkData: Data) async throws -> ApprovedFileAccessRoot {
        let root = try await context.fileAccessScopeStore.addRoot(bookmarkData: bookmarkData)
        _ = try await validateSkill(id: "file-system")
        return root
    }

    public func removeFileAccessRoot(id: UUID) async throws -> ApprovedFileAccessRoot {
        let removed = try await context.fileAccessScopeStore.removeRoot(id: id)
        _ = try await validateSkill(id: "file-system")
        return removed
    }

    // MARK: - Automations (Gremlins) API

    public func automations() async -> [GremlinItem] {
        (try? await context.gremlinsFileStore.loadGremlins()) ?? []
    }

    public func automationsRawMarkdown() async -> String {
        (try? await context.gremlinsFileStore.rawMarkdown()) ?? ""
    }

    public func writeAutomationsMarkdown(_ content: String) async throws {
        try await context.gremlinsFileStore.writeRawMarkdown(content)
        // Reload scheduler to pick up any manual edits
        let gremlins = (try? await context.gremlinsFileStore.loadGremlins()) ?? []
        await gremlinScheduler?.reload(gremlins: gremlins)
    }

    public func automation(id: String) async -> GremlinItem? {
        let items = (try? await context.gremlinsFileStore.loadGremlins()) ?? []
        return items.first { $0.id == id }
    }

    public func createAutomation(_ item: GremlinItem) async throws -> GremlinItem {
        try await context.gremlinManagingAdapter.addGremlin(item)
        return item
    }

    public func updateAutomation(_ item: GremlinItem) async throws -> GremlinItem {
        try await context.gremlinManagingAdapter.updateGremlin(item)
        return item
    }

    public func deleteAutomation(id: String) async throws {
        try await context.gremlinManagingAdapter.deleteGremlin(id: id)
    }

    public func runAutomationNow(id: String) async throws -> GremlinRun {
        try await context.gremlinManagingAdapter.runNow(id: id)
    }

    public func automationRuns(gremlinID: String, limit: Int = 50) async -> [GremlinRun] {
        (try? await context.gremlinsFileStore.runsForGremlin(gremlinID: gremlinID, limit: limit)) ?? []
    }

    // MARK: - Workflows API

    public func workflows() async -> [AtlasWorkflowDefinition] {
        await workflowStore.listDefinitions()
    }

    public func workflow(id: String) async -> AtlasWorkflowDefinition? {
        await workflowStore.definition(id: id)
    }

    public func createWorkflow(_ definition: AtlasWorkflowDefinition) async throws -> AtlasWorkflowDefinition {
        let sanitized = try await sanitizedWorkflowDefinition(definition)
        return await workflowStore.upsertDefinition(sanitized)
    }

    public func updateWorkflow(_ definition: AtlasWorkflowDefinition) async throws -> AtlasWorkflowDefinition {
        let existing = await workflowStore.definition(id: definition.id)
        let sanitized = try await sanitizedWorkflowDefinition(
            definition,
            existingDefinition: existing
        )
        return await workflowStore.upsertDefinition(sanitized)
    }

    public func deleteWorkflow(id: String) async throws -> AtlasWorkflowDefinition {
        try await workflowStore.deleteDefinition(id: id)
    }

    public func runWorkflow(id: String, inputValues: [String: String] = [:]) async throws -> AtlasWorkflowRun {
        guard let definition = await workflowStore.definition(id: id) else {
            throw RuntimeAPIError.notFound("Workflow not found.")
        }
        return await workflowRunner.run(definition: definition, inputValues: inputValues)
    }

    public func workflowRuns(workflowID: String? = nil, limit: Int = 50) async -> [AtlasWorkflowRun] {
        await workflowStore.listRuns(workflowID: workflowID, limit: limit)
    }

    public func approveWorkflowRun(id: UUID) async throws -> AtlasWorkflowRun {
        try await workflowRunner.approve(runID: id)
    }

    public func denyWorkflowRun(id: UUID) async throws -> AtlasWorkflowRun {
        try await workflowRunner.deny(runID: id)
    }

    private func sanitizedWorkflowDefinition(
        _ definition: AtlasWorkflowDefinition,
        existingDefinition: AtlasWorkflowDefinition? = nil
    ) async throws -> AtlasWorkflowDefinition {
        let sanitizedSteps = try await sanitizedWorkflowSteps(definition.steps)
        let sanitizedTrustScope = WorkflowPlanner.derivedTrustScope(for: sanitizedSteps)

        return AtlasWorkflowDefinition(
            id: definition.id,
            name: definition.name,
            description: definition.description,
            promptTemplate: definition.promptTemplate,
            tags: definition.tags,
            steps: sanitizedSteps,
            trustScope: sanitizedTrustScope,
            approvalMode: definition.approvalMode,
            createdAt: existingDefinition?.createdAt ?? definition.createdAt,
            updatedAt: .now,
            sourceConversationID: definition.sourceConversationID,
            isEnabled: definition.isEnabled
        )
    }

    private func sanitizedWorkflowSteps(
        _ steps: [AtlasWorkflowStep]
    ) async throws -> [AtlasWorkflowStep] {
        var sanitizedSteps: [AtlasWorkflowStep] = []
        sanitizedSteps.reserveCapacity(steps.count)
        for step in steps {
            switch step.kind {
            case .prompt:
                sanitizedSteps.append(AtlasWorkflowStep(
                    id: step.id,
                    title: step.title,
                    kind: .prompt,
                    prompt: step.prompt
                ))
            case .skillAction:
                guard let skillID = step.skillID, let actionID = step.actionID else {
                    throw RuntimeAPIError.invalidRequest("Workflow step '\(step.title)' is missing skill metadata.")
                }
                guard let record = await context.skillRegistry.skill(id: skillID) else {
                    throw RuntimeAPIError.invalidRequest("Workflow step '\(step.title)' references an unknown skill.")
                }
                guard record.manifest.lifecycleState == .enabled else {
                    throw RuntimeAPIError.invalidRequest("Workflow step '\(step.title)' references a disabled skill.")
                }
                guard let action = record.actions.first(where: { $0.id == actionID }) else {
                    throw RuntimeAPIError.invalidRequest("Workflow step '\(step.title)' references an unknown action.")
                }
                guard action.isEnabled else {
                    throw RuntimeAPIError.invalidRequest("Workflow step '\(step.title)' references a disabled action.")
                }
                guard action.sideEffectLevel != .destructive else {
                    throw RuntimeAPIError.invalidRequest("Destructive workflow steps are not supported.")
                }

                let inputTargets = stepTargets(from: step.inputJSON)
                sanitizedSteps.append(AtlasWorkflowStep(
                    id: step.id,
                    title: step.title.isEmpty ? action.name : step.title,
                    kind: .skillAction,
                    skillID: skillID,
                    actionID: actionID,
                    inputJSON: step.inputJSON,
                    appName: concreteScopeValue(inputTargets.appName),
                    targetPath: concretePathScopeValue(inputTargets.targetPath),
                    sideEffectLevel: action.sideEffectLevel.rawValue
                ))
            }
        }
        return sanitizedSteps
    }

    private func stepTargets(from inputJSON: String?) -> (appName: String?, targetPath: String?) {
        guard let inputJSON, !inputJSON.isEmpty else {
            return (nil, nil)
        }
        guard let args = try? AtlasToolInput(argumentsJSON: inputJSON).dictionary() else {
            return (nil, nil)
        }
        return (
            args["appName"],
            args["path"] ??
                args["rootPath"] ??
                args["targetPath"] ??
                args["filePath"] ??
                args["directoryPath"] ??
                args["folderPath"]
        )
    }

    private func concreteScopeValue(_ value: String?) -> String? {
        guard let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines),
              !trimmed.isEmpty,
              !trimmed.contains("{{"),
              !trimmed.contains("}}") else {
            return nil
        }
        return trimmed
    }

    private func concretePathScopeValue(_ value: String?) -> String? {
        guard let concrete = concreteScopeValue(value) else {
            return nil
        }
        guard concrete.hasPrefix("/") else {
            return concrete
        }
        return URL(fileURLWithPath: concrete).standardizedFileURL.path
    }

    // MARK: - Config API

    public func actionConfig() async -> RuntimeConfigSnapshot {
        await AtlasConfigStore.shared.load()
    }

    public struct OnboardingStatusResponse: Encodable {
        public let completed: Bool
    }

    public func onboardingStatus() async -> OnboardingStatusResponse {
        let snapshot = await AtlasConfigStore.shared.load()
        return OnboardingStatusResponse(completed: snapshot.onboardingCompleted)
    }

    public func setOnboardingCompleted(_ completed: Bool) async throws -> OnboardingStatusResponse {
        var snapshot = await AtlasConfigStore.shared.load()
        snapshot.onboardingCompleted = completed
        try await AtlasConfigStore.shared.save(snapshot)
        AtlasConfig.seedSnapshot(snapshot)

        let updatedConfig = context.config.updatingOnboardingCompleted(completed)
        updatedConfig.persistOnboardingCompleted()

        context.logger.info("Onboarding status updated via web UI", metadata: [
            "completed": completed ? "true" : "false"
        ])

        return OnboardingStatusResponse(completed: snapshot.onboardingCompleted)
    }

    // MARK: - Model selector

    public struct ModelSelectorInfo: Encodable {
        public let primaryModel: String?
        public let fastModel: String?
        public let lastRefreshedAt: Date?
        public let availableModels: [AIModelRecord]
    }

    public func modelSelectorInfo() async -> ModelSelectorInfo {
        ModelSelectorInfo(
            primaryModel: await context.modelSelector.primaryModel,
            fastModel: await context.modelSelector.fastModel,
            lastRefreshedAt: await context.modelSelector.lastRefreshedAt,
            availableModels: await context.modelSelector.availableModels()
        )
    }

    public func refreshModels() async -> ModelSelectorInfo {
        await context.modelSelector.refresh()
        return await modelSelectorInfo()
    }

    /// Fetches the available model list for any provider on demand, without changing
    /// the daemon's active configuration. Used by the Settings UI when switching providers.
    public func availableModels(for provider: AIProvider) async -> ModelSelectorInfo {
        // Build a temporary config with just the provider swapped out.
        var snapshot = context.config.asSnapshot()
        snapshot.activeAIProvider = provider.rawValue
        let tempConfig = context.config.derived(snapshot: snapshot)
        let tempSelector = ProviderAwareModelSelector(config: tempConfig)
        await tempSelector.refresh()
        return ModelSelectorInfo(
            primaryModel: await tempSelector.primaryModel,
            fastModel: await tempSelector.fastModel,
            lastRefreshedAt: await tempSelector.lastRefreshedAt,
            availableModels: await tempSelector.availableModels()
        )
    }

    /// Returns all Telegram sessions that have ever connected to this bot.
    public func knownTelegramChats() async -> [TelegramSession] {
        (try? await context.telegramSessionStore.allSessions()) ?? []
    }

    public func communicationChannels() async -> [CommunicationChannel] {
        (try? await context.communicationSessionStore.allChannels()) ?? []
    }

    public func communicationsSnapshot() async -> CommunicationsSnapshot {
        await communicationManager.statusSnapshot()
    }

    public func updateCommunicationPlatform(platform: ChatPlatform, enabled: Bool) async throws -> CommunicationPlatformStatus {
        try await communicationManager.updatePlatform(platform, enabled: enabled)
    }

    public func validateCommunicationPlatform(_ platform: ChatPlatform) async throws -> CommunicationPlatformStatus {
        try await communicationManager.validate(platform: platform)
    }

    public func validateCommunicationPlatform(
        _ platform: ChatPlatform,
        credentialOverrides: [String: String],
        configOverrides: CommunicationValidationConfigOverrides
    ) async throws -> CommunicationPlatformStatus {
        let trimmedCredentials = credentialOverrides.reduce(into: [String: String]()) { partial, entry in
            let trimmed = entry.value.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty {
                partial[entry.key] = trimmed
            }
        }

        if trimmedCredentials.isEmpty && configOverrides.discordClientID == nil {
            return try await validateCommunicationPlatform(platform)
        }

        let persistedSnapshot = await AtlasConfigStore.shared.load()
        var validationSnapshot = persistedSnapshot

        if let discordClientID = configOverrides.discordClientID?.trimmingCharacters(in: .whitespacesAndNewlines),
           !discordClientID.isEmpty {
            validationSnapshot.discordClientID = discordClientID
        }

        switch platform {
        case .telegram:
            validationSnapshot.telegramEnabled = true
        case .discord:
            validationSnapshot.discordEnabled = true
        case .slack:
            validationSnapshot.slackEnabled = true
        case .whatsApp, .companion:
            break
        }

        let validationConfig = context.config.derived(
            snapshot: validationSnapshot,
            credentialBundleOverride: credentialBundle(from: trimmedCredentials)
        )
        return try await communicationManager.validate(platform: platform, config: validationConfig)
    }

    public func setAPIKey(provider: String, key: String, customName: String? = nil) async throws {
        let trimmed = key.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        switch provider {
        case "openai":      try context.config.storeOpenAIAPIKey(trimmed)
        case "telegram":    try context.config.storeTelegramBotToken(trimmed)
        case "discord":     try context.config.storeDiscordBotToken(trimmed)
        case "slackBot":    try context.config.storeSlackBotToken(trimmed)
        case "slackApp":    try context.config.storeSlackAppToken(trimmed)
        case "braveSearch": try context.config.storeBraveSearchAPIKey(trimmed)
        case "anthropic":   try context.config.storeAnthropicAPIKey(trimmed)
        case "gemini":      try context.config.storeGeminiAPIKey(trimmed)
        case "lm_studio":   try context.config.storeLMStudioAPIKey(trimmed)
        case "custom":
            if let name = customName?.trimmingCharacters(in: .whitespacesAndNewlines), !name.isEmpty {
                try context.config.storeCustomKey(displayName: name, value: trimmed)
            }
        default: break
        }
        context.logger.info("API key updated via web UI", metadata: ["provider": provider])
    }

    public func deleteAPIKey(name: String) async throws {
        try context.config.deleteCustomKey(displayName: name)
        context.logger.info("Custom API key deleted via web UI", metadata: ["name": name])
    }

    public func apiKeyStatus() async -> APIKeyStatusResponse {
        // Invalidate the in-process Keychain bundle cache before reading so that keys
        // stored by the settings app (a separate process) are immediately reflected here.
        context.config.invalidateSecretCache()
        let config = context.config
        return APIKeyStatusResponse(
            openAIKeySet: (try? !config.openAIAPIKey().isEmpty) ?? false,
            telegramTokenSet: config.hasTelegramBotToken(),
            discordTokenSet: config.hasDiscordBotToken(),
            slackBotTokenSet: config.hasSlackBotToken(),
            slackAppTokenSet: config.hasSlackAppToken(),
            braveSearchKeySet: config.hasBraveSearchAPIKey(),
            anthropicKeySet: config.hasAnthropicAPIKey(),
            geminiKeySet: config.hasGeminiAPIKey(),
            lmStudioKeySet: config.hasLMStudioAPIKey(),
            customKeys: config.customKeyDisplayNames()
        )
    }

    func invalidateSecretCache() {
        context.config.invalidateSecretCache()
    }

    public func communicationSetupValues(for platform: ChatPlatform) async -> [String: String] {
        let config = context.config
        var values: [String: String] = [:]

        switch platform {
        case .telegram:
            if let token = try? config.telegramBotToken(), !token.isEmpty {
                values["telegram"] = token
            }
        case .discord:
            if let token = try? config.discordBotToken(), !token.isEmpty {
                values["discord"] = token
            }
            if !config.discordClientID.isEmpty {
                values["discordClientID"] = config.discordClientID
            }
        case .slack:
            if let token = try? config.slackBotToken(), !token.isEmpty {
                values["slackBot"] = token
            }
            if let token = try? config.slackAppToken(), !token.isEmpty {
                values["slackApp"] = token
            }
        case .whatsApp, .companion:
            break
        }

        return values
    }

    public func updateConfig(_ snapshot: RuntimeConfigSnapshot) async throws -> (snapshot: RuntimeConfigSnapshot, restartRequired: Bool) {
        let previous = await AtlasConfigStore.shared.load()
        try await AtlasConfigStore.shared.save(snapshot)
        AtlasConfig.seedSnapshot(snapshot)

        // Port or remote access changes require a daemon restart to rebind the HTTP server.
        let restartRequired = snapshot.runtimePort != previous.runtimePort
            || snapshot.remoteAccessEnabled != previous.remoteAccessEnabled

        // AI-related changes are handled live by rebuilding the AgentContext.
        let aiChanged = snapshot.activeAIProvider != previous.activeAIProvider
            || snapshot.selectedAnthropicModel != previous.selectedAnthropicModel
            || snapshot.selectedGeminiModel != previous.selectedGeminiModel
            || snapshot.selectedOpenAIPrimaryModel != previous.selectedOpenAIPrimaryModel
            || snapshot.selectedOpenAIFastModel != previous.selectedOpenAIFastModel
            || snapshot.selectedAnthropicFastModel != previous.selectedAnthropicFastModel
            || snapshot.selectedGeminiFastModel != previous.selectedGeminiFastModel
            || snapshot.selectedLMStudioModel != previous.selectedLMStudioModel
            || snapshot.lmStudioBaseURL != previous.lmStudioBaseURL

        if aiChanged {
            await rebuildAIContext()
        }

        context.logger.info("Runtime config updated via PUT /config", metadata: [
            "restart_required": restartRequired ? "true" : "false",
            "ai_rebuilt": aiChanged ? "true" : "false"
        ])

        return (snapshot: snapshot, restartRequired: restartRequired)
    }

    /// Rebuilds `context`, `loop`, and `workflowRunner` after an AI provider or model change.
    /// All persistent services (memory, skills, tools, permissions) are preserved.
    private func rebuildAIContext() async {
        let newConfig = context.config.derived(snapshot: await AtlasConfigStore.shared.load())
        do {
            let newContext = try AgentContext(
                config: newConfig,
                logger: context.logger,
                telegramClient: context.telegramClient,
                memoryStore: context.memoryStore,
                permissionManager: context.permissionManager,
                approvalManager: context.approvalManager,
                toolRegistry: context.toolRegistry,
                toolExecutor: context.toolExecutor,
                skillRegistry: context.skillRegistry,
                skillPolicyEngine: context.skillPolicyEngine,
                skillAuditStore: context.skillAuditStore,
                skillExecutionGateway: context.skillExecutionGateway,
                actionPolicyStore: context.actionPolicyStore,
                fileAccessScopeStore: context.fileAccessScopeStore
            )
            self.context = newContext
            self.loop = AgentLoop(context: newContext)
            self.workflowRunner = WorkflowRunner(context: newContext, store: workflowStore)
            await newContext.modelSelector.refresh()
            newContext.logger.info("AgentRuntime: AI context rebuilt", metadata: [
                "provider": newConfig.activeAIProvider.rawValue
            ])
        } catch {
            context.logger.error("AgentRuntime: failed to rebuild AI context — \(error.localizedDescription)")
        }
    }

    // MARK: - Action Approval Policies

    public func actionPolicies() async -> [String: ActionApprovalPolicy] {
        await context.actionPolicyStore.allPolicies()
    }

    public func setActionPolicy(
        _ policy: ActionApprovalPolicy,
        for actionID: String
    ) async {
        await context.actionPolicyStore.setPolicy(policy, for: actionID)
    }

    public func resetActionPolicy(for actionID: String, permissionLevel: PermissionLevel) async {
        await context.actionPolicyStore.resetPolicy(for: actionID, permissionLevel: permissionLevel)
    }

    public func enableSkill(id: String) async throws -> AtlasSkillRecord {
        let record = try await context.skillRegistry.enable(skillID: id)
        await syncSkillTools()
        await context.skillAuditStore.record(
            SkillAuditEvent(
                skillID: record.id,
                timestamp: .now,
                approvalRequired: false,
                outcome: .enabled,
                outputSummary: "Skill enabled."
            )
        )
        return record
    }

    public func disableSkill(id: String) async throws -> AtlasSkillRecord {
        let record = try await context.skillRegistry.disable(skillID: id)
        await syncSkillTools()
        await context.skillAuditStore.record(
            SkillAuditEvent(
                skillID: record.id,
                timestamp: .now,
                approvalRequired: false,
                outcome: .disabled,
                outputSummary: "Skill disabled."
            )
        )
        return record
    }

    public func validateSkill(id: String) async throws -> AtlasSkillRecord {
        let record = try await context.skillRegistry.validate(
            skillID: id,
            context: SkillValidationContext(
                config: context.config,
                logger: context.logger
            )
        )

        if record.isEnabled {
            await syncSkillTools()
        }

        await context.skillAuditStore.record(
            SkillAuditEvent(
                skillID: record.id,
                timestamp: .now,
                approvalRequired: false,
                outcome: .validated,
                outputSummary: record.validation?.summary
            )
        )

        return record
    }

    public func approve(toolCallID: UUID) async throws -> AtlasMessageResponseEnvelope {
        let approved = try await context.deferredExecutionManager.approve(toolCallID: toolCallID)
        try? await context.eventLogStore.persist(approvalRequest: approved, conversationID: approved.conversationID)

        guard let deferred = await context.deferredExecutionManager.deferredExecution(for: toolCallID) else {
            return minimalEnvelope(message: "Approval recorded.", status: .completed)
        }

        context.logger.info("Resuming deferred execution after approval", metadata: [
            "tool_call_id": toolCallID.uuidString,
            "approval_id": approved.id.uuidString,
            "source_type": deferred.sourceType.rawValue
        ])

        do {
            switch deferred.sourceType {
            case .skill:
                guard let skillID = deferred.skillID, let actionID = deferred.actionID else {
                    throw RuntimeAPIError.invalidRequest("Deferred skill execution is missing skill metadata.")
                }

                let request = SkillExecutionRequest(
                    skillID: skillID,
                    actionID: actionID,
                    input: AtlasToolInput(argumentsJSON: deferred.normalizedInputJSON),
                    conversationID: deferred.conversationID,
                    toolCallID: deferred.toolCallID
                )
                let skillContext = await skillExecutionContext(conversationID: deferred.conversationID)
                _ = try await context.skillExecutionGateway.execute(request, context: skillContext)
            case .tool:
                guard let toolName = deferred.toolID else {
                    throw RuntimeAPIError.invalidRequest("Deferred tool execution is missing tool metadata.")
                }

                let toolCall = AtlasToolCall(
                    id: deferred.toolCallID,
                    toolName: toolName,
                    argumentsJSON: deferred.normalizedInputJSON,
                    permissionLevel: deferred.permissionLevel,
                    requiresApproval: true,
                    status: .approved
                )
                _ = try await context.toolExecutor.execute(
                    toolCall: toolCall,
                    conversationID: deferred.conversationID ?? UUID()
                )
            }
        } catch {
            try? await context.deferredExecutionManager.markFailed(
                toolCallID: deferred.toolCallID,
                errorMessage: error.localizedDescription
            )
            context.logger.error("Deferred execution resume failed", metadata: [
                "tool_call_id": deferred.toolCallID.uuidString,
                "error": error.localizedDescription
            ])
        }

        guard let conversationID = deferred.conversationID else {
            return minimalEnvelope(message: "Action executed.", status: .completed)
        }

        // Await the full conversation resumption so the caller receives the final response.
        return await resumeConversationAfterApproval(deferred: deferred, conversationID: conversationID)
    }

    /// After an approved skill/tool executes, feeds the result back to the model and
    /// continues the agent loop until the task completes or another approval is required.
    /// Returns a full response envelope — identical contract to a normal message send.
    private func resumeConversationAfterApproval(
        deferred: DeferredExecutionRequest,
        conversationID: UUID
    ) async -> AtlasMessageResponseEnvelope {
        guard let completed = await context.deferredExecutionManager.deferredExecution(for: deferred.toolCallID) else {
            context.logger.warning("Could not fetch completed deferred record for conversation resume", metadata: [
                "tool_call_id": deferred.toolCallID.uuidString
            ])
            return minimalEnvelope(message: "Atlas could not locate the completed action.", status: .failed)
        }

        let toolName: String
        switch completed.sourceType {
        case .skill:
            if let skillID = completed.skillID, let actionID = completed.actionID {
                toolName = SkillActionCatalogItem.toolName(skillID: skillID, actionID: actionID)
            } else {
                toolName = "skill__unknown"
            }
        case .tool:
            toolName = completed.toolID ?? "tool__unknown"
        }

        let toolCall = AtlasToolCall(
            id: completed.toolCallID,
            toolName: toolName,
            argumentsJSON: completed.normalizedInputJSON,
            permissionLevel: completed.permissionLevel,
            requiresApproval: true,
            status: .completed
        )

        let toolResult: AtlasToolResult
        if let execResult = completed.result {
            toolResult = AtlasToolResult(
                toolCallID: completed.toolCallID,
                output: execResult.output,
                success: execResult.success,
                errorMessage: execResult.errorMessage
            )
        } else {
            toolResult = AtlasToolResult(
                toolCallID: completed.toolCallID,
                output: completed.lastError ?? "The action did not produce a result.",
                success: false,
                errorMessage: completed.lastError
            )
        }

        // Create emitter for streaming model output back to the still-open SSE connection.
        let broadcaster = streamBroadcaster
        let cID = conversationID
        let emitter: @Sendable (SSEEvent) async -> Void = { event in
            await broadcaster.emit(event, conversationID: cID)
        }

        let orchestrator = AgentOrchestrator(context: context)
        var allToolCalls: [AtlasToolCall] = [toolCall]
        var allToolResults: [AtlasToolResult] = [toolResult]

        do {
            _ = try await orchestrator.appendToolResultMessages(
                [toolResult],
                toolCalls: [toolCall],
                to: conversationID
            )

            var currentTurn = try await orchestrator.requestModelTurn(
                forConversationID: conversationID,
                emitter: emitter
            )
            var iteration = 0

            while true {
                // No more tool calls — task is complete.
                if currentTurn.toolCalls.isEmpty {
                    let message = normalizedResumedMessage(currentTurn.assistantMessage)
                    return await finalizeResumedTurn(message, status: .completed,
                                                     toolCalls: allToolCalls, toolResults: allToolResults,
                                                     conversationID: conversationID)
                }

                let execution = await orchestrator.executeToolCalls(
                    currentTurn.toolCalls,
                    conversationID: conversationID,
                    emitter: emitter
                )
                allToolCalls.append(contentsOf: execution.toolCalls)
                allToolResults.append(contentsOf: execution.results)

                // Another approval gate hit — surface it to the caller.
                if !execution.pendingApprovals.isEmpty {
                    let count = execution.pendingApprovals.count
                    let message = "Atlas requires approval before it can continue. \(count == 1 ? "1 action is" : "\(count) actions are") pending."
                    await streamBroadcaster.emit(.done(status: "waitingForApproval"), conversationID: conversationID)
                    return await buildResumedEnvelope(
                        message: message, status: .waitingForApproval,
                        toolCalls: allToolCalls, toolResults: allToolResults,
                        pendingApprovals: execution.pendingApprovals,
                        conversationID: conversationID
                    )
                }

                _ = try await orchestrator.appendToolResultMessages(
                    execution.results,
                    toolCalls: execution.toolCalls,
                    to: conversationID
                )

                iteration += 1
                guard iteration < context.config.maxAgentIterations else {
                    let message = "Atlas stopped after reaching the maximum tool-loop depth."
                    return await finalizeResumedTurn(message, status: .failed,
                                                     toolCalls: allToolCalls, toolResults: allToolResults,
                                                     conversationID: conversationID, errorMessage: message)
                }

                currentTurn = try await orchestrator.continueModelTurn(
                    forConversationID: conversationID,
                    previousResponseID: currentTurn.responseID,
                    toolCalls: execution.toolCalls,
                    toolResults: execution.results,
                    emitter: emitter
                )
            }
        } catch {
            context.logger.error("Failed to resume conversation after approval", metadata: [
                "conversation_id": conversationID.uuidString,
                "tool_call_id": completed.toolCallID.uuidString,
                "error": error.localizedDescription
            ])
            let message = "I ran into an issue completing that action. Let me know if you'd like to try again."
            await streamBroadcaster.emit(.assistantDelta(message), conversationID: conversationID)
            await streamBroadcaster.emit(.assistantDone(), conversationID: conversationID)
            await streamBroadcaster.emit(.done(status: "failed"), conversationID: conversationID)
            await streamBroadcaster.finish(conversationID: conversationID)
            return minimalEnvelope(message: message, status: .failed, errorMessage: error.localizedDescription)
        }
    }

    private func normalizedResumedMessage(_ message: String) -> String {
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? "Atlas completed the action." : trimmed
    }

    private func finalizeResumedTurn(
        _ message: String,
        status: AtlasAgentResponseStatus,
        toolCalls: [AtlasToolCall],
        toolResults: [AtlasToolResult],
        conversationID: UUID,
        errorMessage: String? = nil
    ) async -> AtlasMessageResponseEnvelope {
        let assistantRecord = AtlasMessage(role: .assistant, content: message)
        let conversation = (try? await context.conversationStore.appendMessage(assistantRecord, to: conversationID))
            ?? AtlasConversation(id: conversationID)

        // Streaming delivered text via assistant_delta events; only the
        // conversation-level done signal needs to be sent here.
        await streamBroadcaster.emit(.done(status: status.rawValue), conversationID: conversationID)
        await streamBroadcaster.finish(conversationID: conversationID)

        context.logger.info("Conversation resumed after approval", metadata: [
            "conversation_id": conversationID.uuidString,
            "status": status.rawValue
        ])

        return AtlasMessageResponseEnvelope(
            conversation: conversation,
            response: AtlasAgentResponse(
                assistantMessage: message,
                toolCalls: toolCalls,
                status: status,
                toolResults: toolResults,
                pendingApprovals: [],
                errorMessage: errorMessage
            )
        )
    }

    private func buildResumedEnvelope(
        message: String,
        status: AtlasAgentResponseStatus,
        toolCalls: [AtlasToolCall],
        toolResults: [AtlasToolResult],
        pendingApprovals: [ApprovalRequest],
        conversationID: UUID
    ) async -> AtlasMessageResponseEnvelope {
        let conversation = (try? await context.conversationStore.fetchConversation(id: conversationID))
            ?? AtlasConversation(id: conversationID)
        return AtlasMessageResponseEnvelope(
            conversation: conversation,
            response: AtlasAgentResponse(
                assistantMessage: message,
                toolCalls: toolCalls,
                status: status,
                toolResults: toolResults,
                pendingApprovals: pendingApprovals
            )
        )
    }

    private func minimalEnvelope(
        message: String,
        status: AtlasAgentResponseStatus,
        errorMessage: String? = nil
    ) -> AtlasMessageResponseEnvelope {
        AtlasMessageResponseEnvelope(
            conversation: AtlasConversation(id: UUID()),
            response: AtlasAgentResponse(
                assistantMessage: message,
                toolCalls: [],
                status: status,
                toolResults: [],
                pendingApprovals: [],
                errorMessage: errorMessage
            )
        )
    }

    public func deny(toolCallID: UUID) async throws -> ApprovalRequest {
        let denied = try await context.deferredExecutionManager.deny(toolCallID: toolCallID)
        try? await context.eventLogStore.persist(approvalRequest: denied, conversationID: denied.conversationID)

        // Close any waiting SSE stream for this conversation.
        if let conversationID = denied.conversationID {
            await streamBroadcaster.emit(.error("The action was denied."), conversationID: conversationID)
            await streamBroadcaster.emit(.done(status: "denied"), conversationID: conversationID)
            await streamBroadcaster.finish(conversationID: conversationID)
        }

        return denied
    }

    private func fallbackConversation(id: UUID) async -> AtlasConversation {
        if let existing = try? await context.conversationStore.fetchConversation(id: id) {
            return existing
        }

        if let created = try? await context.conversationStore.createConversation(id: id) {
            return created
        }

        return AtlasConversation(id: id)
    }

    /// Converts a system-level error into a short, human-readable message.
    /// Never exposes raw Swift error descriptions, stack traces, or internal codes.
    static func humanizeSystemError(_ error: Error) -> String {
        let desc = error.localizedDescription.lowercased()
        if desc.contains("model") || desc.contains("openai") || desc.contains("api key")
            || desc.contains("no model") || desc.contains("modelunavailable") {
            return "I couldn't connect to my AI model. Check that your API key is valid and try again."
        }
        if desc.contains("network") || desc.contains("connection") || desc.contains("timeout")
            || desc.contains("timed out") || desc.contains("offline") {
            return "The connection failed. Check your network and try again."
        }
        if desc.contains("permission") || desc.contains("denied") || desc.contains("unauthorized") {
            return "I don't have the permissions needed for that."
        }
        return "Something went wrong on my end. Try sending your message again."
    }

    private func appendFailureMessage(_ message: String, to conversationID: UUID) async -> AtlasConversation {
        let assistantMessage = AtlasMessage(role: .assistant, content: message)

        if let conversation = try? await context.conversationStore.appendMessage(assistantMessage, to: conversationID) {
            return conversation
        }

        return await fallbackConversation(id: conversationID)
    }

    private func skillExecutionContext(conversationID: UUID?) async -> SkillExecutionContext {
        let context = self.context
        return SkillExecutionContext(
            conversationID: conversationID,
            logger: context.logger,
            config: context.config,
            permissionManager: context.permissionManager,
            runtimeStatusProvider: {
                await self.status()
            },
            enabledSkillsProvider: {
                await context.skillRegistry.listEnabled()
            },
            memoryItemsProvider: {
                do {
                    return try await context.memoryStore.listMemories(limit: 100)
                } catch {
                    context.logger.warning("Failed to load memory defaults for skill execution", metadata: [
                        "error": error.localizedDescription
                    ])
                    return []
                }
            }
        )
    }

    private var statusDescription: String {
        switch state {
        case .starting:
            return "Booting local Atlas runtime."
        case .ready:
            return "Local runtime is listening on localhost."
        case .degraded:
            return "Local runtime is reachable but reported an error on the last request."
        case .stopped:
            return "Local runtime is not running."
        }
    }

    private func bootstrapSkillsIfNeeded() async throws {
        guard skillsBootstrapped == false else { return }
        skillsBootstrapped = true

        // Bootstrap the CoreSkills runtime before registering built-in skills.
        // The secrets reader delegates through AtlasConfig so atlas-skills stays
        // unaware of the platform-specific secret backend.
        let secretsReader: CoreSecretsService.SecretsReader = { [config = self.context.config] service in
            try config.readSecretValue(for: service)
        }
        // Inject a resync closure so CoreSkillService can refresh the tool catalog
        // after a Forge skill is installed and enabled — no circular import needed.
        let resyncCallback: @Sendable () async -> Void = { [runtime = self] in
            await runtime.resyncSkillCatalog()
        }
        self.coreSkills = CoreSkillsRuntime(
            registry: context.skillRegistry,
            secretsReader: secretsReader,
            resyncCallback: resyncCallback,
            logger: context.logger
        )

        // Build Forge orchestration handler closures that delegate to self at call time.
        // These are created before ForgeProposalService exists, but since closures are
        // called lazily (at agent execution time), forgeProposalService will be ready.
        let runtime = self
        let forgeHandlers = ForgeOrchestrationHandlers(
            startResearching: { title, message in
                await runtime.forgeStartResearching(title: title, message: message)
            },
            stopResearching: { id in
                await runtime.forgeStopResearching(id: id)
            },
            createProposal: { spec, plans, summary, rationale, contractJSON in
                try await runtime.forgeCreateProposal(spec: spec, plans: plans, summary: summary, rationale: rationale, contractJSON: contractJSON)
            }
        )

        try await context.skillRegistry.register(
            BuiltInSkillsProvider(
                fileAccessScopeStore: context.fileAccessScopeStore,
                gremlinManaging: context.gremlinManagingAdapter,
                forgeOrchestrationHandlers: forgeHandlers,
                forgeCoreSkills: coreSkills,
                notificationSink: RelayNotificationSink()
            ).makeSkills()
        )

        let visibleSkills = await context.skillRegistry.listVisible()
        for skill in visibleSkills {
            do {
                _ = try await context.skillRegistry.validate(
                    skillID: skill.id,
                    context: SkillValidationContext(
                        config: context.config,
                        logger: context.logger
                    )
                )
            } catch {
                context.logger.error("Skill validation failed during runtime bootstrap", metadata: [
                    "skill_id": skill.id,
                    "error": error.localizedDescription
                ])
            }
        }

        await syncSkillTools()
        await validateSkillRouting()

        // Wire ForgeProposalService with the now-ready CoreSkills runtime,
        // then re-hydrate any previously installed/enabled Forge skills so they
        // survive daemon restart without requiring user re-approval.
        let service = ForgeProposalService(
            store: context.forgeProposalStore,
            logger: context.logger
        )
        await service.configure(coreSkills: coreSkills!, skillRegistry: context.skillRegistry)
        await service.hydrateInstalledSkills()
        self.forgeProposalService = service
    }

    private func validateSkillRouting() async {
        let enabled = await context.skillRegistry.listEnabled()
        let untriggered = enabled.filter { $0.manifest.triggers.isEmpty && $0.manifest.isUserVisible }
        guard !untriggered.isEmpty else { return }
        context.logger.warning("Skills registered with no routing triggers — they will never be proactively selected by the classifier", metadata: [
            "skills": untriggered.map(\.id).joined(separator: ", ")
        ])
    }

    /// Rebuild the agent's live skill tool catalog from the current registry state.
    ///
    /// Call this after installing and enabling a Forge skill so the agent can
    /// use it in the same session without a daemon restart.
    /// Also called automatically by `enableSkill()` and `disableSkill()`.
    public func resyncSkillCatalog() async {
        await syncSkillTools()
    }

    private func syncSkillTools() async {
        await context.toolRegistry.removeTools(withPrefix: "skill__")

        let catalog = await context.skillRegistry.enabledActionCatalog()
        let adapters = catalog.map { item in
            SkillActionToolAdapter(
                catalogItem: item,
                gateway: context.skillExecutionGateway,
                executionContextBuilder: { [context, runtime = self] toolContext in
                    SkillExecutionContext(
                        conversationID: toolContext.conversationID,
                        logger: context.logger,
                        config: context.config,
                        permissionManager: context.permissionManager,
                        runtimeStatusProvider: {
                            await runtime.status()
                        },
                        enabledSkillsProvider: {
                            await context.skillRegistry.listEnabled()
                        },
                        memoryItemsProvider: {
                            do {
                                return try await context.memoryStore.listMemories(limit: 100)
                            } catch {
                                context.logger.warning("Failed to load memory defaults for skill execution", metadata: [
                                    "error": error.localizedDescription
                                ])
                                return []
                            }
                        }
                    )
                }
            )
        }

        await context.toolRegistry.register(adapters)

        context.logger.info("Synced skill action catalog", metadata: [
            "enabled_skills": "\(await context.skillRegistry.listEnabled().count)",
            "actions": "\(catalog.count)"
        ])
    }

    private func startCommunicationsIfNeeded() async {
        // Build automation closures for Telegram command integration
        let fileStore = context.gremlinsFileStore
        let scheduler = gremlinScheduler

        let listAutomations: @Sendable () async -> String = {
            let gremlins = (try? await fileStore.loadGremlins()) ?? []
            guard !gremlins.isEmpty else { return "No automations configured yet." }
            let lines = gremlins.map { g in
                let status = g.isEnabled ? "✅" : "⏸"
                let destination = g.communicationDestination != nil ? " 📨" : ""
                return "\(status) \(g.emoji) *\(g.name)*\(destination) — `\(g.scheduleRaw)`"
            }
            return "Automations:\n" + lines.joined(separator: "\n") + "\n\nUse /run <name> to trigger one."
        }

        let triggerAutomation: @Sendable (_ nameOrID: String) async -> String = { nameOrID in
            let gremlins = (try? await fileStore.loadGremlins()) ?? []
            guard let gremlin = gremlins.first(where: {
                $0.id == nameOrID || $0.name.lowercased() == nameOrID.lowercased()
            }) else {
                return "No automation named '\(nameOrID)'. Use /automations to see the list."
            }
            guard let scheduler else {
                return "Scheduler is not running."
            }
            let run = await scheduler.runNow(gremlin)
            switch run.status {
            case .success:
                return "\(gremlin.emoji) *\(gremlin.name)* finished successfully."
            case .failed:
                return "\(gremlin.emoji) *\(gremlin.name)* failed: \(run.errorMessage ?? "unknown error")"
            case .skipped:
                return "\(gremlin.emoji) *\(gremlin.name)* was skipped (already running)."
            case .running:
                return "\(gremlin.emoji) *\(gremlin.name)* is running."
            }
        }

        let commandRouter = TelegramCommandRouter(
            config: context.config,
            listAutomations: listAutomations,
            triggerAutomation: triggerAutomation
        )
        await communicationManager.start(
            telegramCommandRouter: commandRouter,
            telegramRuntime: self
        )

        // Wire the Telegram notifier into the scheduler so automation results are delivered
        if let scheduler {
            await scheduler.setCommunicationNotifier { [weak self] destination, emoji, name, output in
                await self?.communicationManager.deliverAutomationResult(
                    destination: destination,
                    emoji: emoji,
                    name: name,
                    output: output
                )
            }
            await scheduler.setApprovalNotifier { [weak self] session, approval in
                await self?.communicationManager.routeApprovalNotification(session: session, approval: approval)
            }
        }
    }

    // MARK: - Proactive approval routing

    private func routeApprovalNotification(session: ChatSession, approval: ApprovalRequest) async {
        await communicationManager.routeApprovalNotification(session: session, approval: approval)
    }

    // MARK: - MIND.md

    private func loadMindIfNeeded() async {
        do {
            try await context.mindEngine.load()
            context.logger.info("MIND.md loaded successfully")
        } catch {
            context.logger.error("MIND.md load failed", metadata: ["error": error.localizedDescription])
        }
    }

    public func mindContent() async -> String {
        await context.mindEngine.currentContent()
    }

    public func updateMindContent(_ content: String) async throws {
        try await context.mindEngine.updateContent(content)
    }

    // MARK: - SKILLS.md

    public func skillsMemoryContent() async -> String {
        await context.skillsEngine.currentContent()
    }

    public func updateSkillsMemory(_ content: String) async throws {
        try await context.skillsEngine.updateContent(content)
    }

    public func loadSkillsMemoryIfNeeded() async {
        do {
            _ = try await context.skillsEngine.load()
            context.logger.info("SKILLS.md loaded successfully")
        } catch {
            context.logger.error("SKILLS.md load failed", metadata: ["error": error.localizedDescription])
        }
    }

    public func regenerateMind() async throws -> String {
        // Delete current content and re-seed
        try await context.mindEngine.updateContent("")
        try await context.mindEngine.load()
        return await context.mindEngine.currentContent()
    }

    // MARK: - WebAuth

    /// Issue a short-lived HMAC-signed launch token for the menu bar → browser handoff.
    func issueWebLaunchToken() async -> String {
        await webAuthService.issueLaunchToken()
    }

    /// Verify `token`, create a new session, and return `(sessionID, Set-Cookie header value)`.
    /// Throws `RuntimeAPIError.invalidRequest` if the token is invalid, expired, or replayed.
    func bootstrapWebSession(token: String) async throws -> (sessionID: String, cookieHeader: String) {
        do {
            try await webAuthService.verifyLaunchToken(token)
        } catch WebAuthService.WebAuthError.expiredToken {
            throw RuntimeAPIError.invalidRequest("Launch token has expired. Please try opening Atlas from the menu bar again.")
        } catch WebAuthService.WebAuthError.alreadyUsed {
            throw RuntimeAPIError.invalidRequest("Launch token has already been used. Please try opening Atlas from the menu bar again.")
        } catch {
            throw RuntimeAPIError.invalidRequest("Invalid launch token.")
        }
        let session     = await webAuthService.createSession()
        let cookieHeader = webAuthService.sessionSetCookieValue(for: session)
        return (session.id, cookieHeader)
    }

    /// Returns `true` if `id` refers to an active, non-expired session.
    func validateWebSession(id: String?) async -> Bool {
        await webAuthService.validateSession(id: id)
    }

    /// Returns the full session struct (including `isRemote`) or nil if not found/expired.
    func webSessionDetail(id: String?) async -> WebAuthService.Session? {
        await webAuthService.sessionDetail(id: id)
    }

    /// Validate a remote API key and, on success, create a remote session.
    /// Returns the Set-Cookie header value, or nil if the key is invalid or remote access is disabled.
    func authenticateRemoteAPIKey(_ key: String) async -> String? {
        guard context.config.remoteAccessEnabled else { return nil }
        guard await webAuthService.validateAPIKey(key, config: context.config) else { return nil }
        let session = await webAuthService.createRemoteSession()
        return webAuthService.sessionSetCookieValue(for: session)
    }

    /// Returns whether LAN remote access is currently enabled.
    /// Reads from the live AtlasConfigStore on every call — NOT from context.config.
    /// context.config is an immutable struct frozen at daemon startup; it does not
    /// update when settings change via PUT /config. AtlasConfigStore.shared always
    /// reflects the latest value saved by updateConfig(), so this is the authoritative
    /// source for the middleware gate that runs on every remote request.
    func remoteAccessEnabled() async -> Bool {
        await AtlasConfigStore.shared.load().remoteAccessEnabled
    }

    /// Returns the current remote access token, or an empty string if none has been set.
    /// Read-only — key generation only happens from the macOS app side.
    func remoteAccessKey() -> String {
        context.config.invalidateSecretCache()
        return (try? context.config.remoteAccessAPIKey()) ?? ""
    }

    /// Invalidate all active remote sessions.
    /// After this call all remote clients must re-authenticate using the current token.
    /// The token itself is NOT rotated here — Keychain bundle writes from the daemon
    /// process are unsafe under some signing/entitlement conditions and can silently
    /// corrupt the entire credential bundle. Token rotation must be initiated from the
    /// macOS app (AtlasApp), which always has the correct Keychain access group.
    func revokeAndRotateRemoteAccess() async {
        await webAuthService.invalidateAllRemoteSessions()
    }

    /// Remote access status payload for `GET /auth/remote-status`.
    struct RemoteAccessStatus: Encodable, Sendable {
        let remoteAccessEnabled: Bool
        let port: Int
        let lanIP: String?
        let accessURL: String?
    }

    func remoteAccessStatus() async -> RemoteAccessStatus {
        let enabled = context.config.remoteAccessEnabled
        let port    = boundPort > 0 ? boundPort : context.config.runtimePort
        let lanIP   = lanIPAddress()
        let accessURL: String? = (enabled && lanIP != nil) ? "http://\(lanIP!):\(port)" : nil
        return RemoteAccessStatus(
            remoteAccessEnabled: enabled,
            port: port,
            lanIP: lanIP,
            accessURL: accessURL
        )
    }

    /// Returns the Mac's primary LAN IP address (first non-loopback IPv4).
    func lanIPAddress() -> String? {
        var ifaddr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddr) == 0 else { return nil }
        defer { freeifaddrs(ifaddr) }
        var current = ifaddr
        while let ptr = current {
            let flags = Int32(ptr.pointee.ifa_flags)
            let isUp = (flags & IFF_UP) != 0
            let isLoopback = (flags & IFF_LOOPBACK) != 0
            if isUp && !isLoopback,
               ptr.pointee.ifa_addr.pointee.sa_family == UInt8(AF_INET) {
                var hostname = [CChar](repeating: 0, count: Int(NI_MAXHOST))
                if getnameinfo(ptr.pointee.ifa_addr, socklen_t(ptr.pointee.ifa_addr.pointee.sa_len),
                               &hostname, socklen_t(hostname.count),
                               nil, 0, NI_NUMERICHOST) == 0 {
                    let ip = String(cString: hostname)
                    if ip.hasPrefix("192.") || ip.hasPrefix("10.") || ip.hasPrefix("172.") || ip.hasPrefix("100.") {
                        return ip
                    }
                }
            }
            current = ptr.pointee.ifa_next
        }
        return nil
    }
}

public struct CommunicationValidationConfigOverrides: Sendable {
    public let discordClientID: String?

    public init(discordClientID: String? = nil) {
        self.discordClientID = discordClientID
    }
}

private func credentialBundle(from values: [String: String]) -> AtlasCredentialBundle {
    AtlasCredentialBundle(
        telegramBotToken: values["telegram"],
        discordBotToken: values["discord"],
        slackBotToken: values["slackBot"],
        slackAppToken: values["slackApp"]
    )
}

private final class RuntimeHTTPServer {
    private let host: String
    private let port: Int
    private let runtime: AgentRuntime
    private let group = MultiThreadedEventLoopGroup(numberOfThreads: 1)

    private var channel: Channel?

    init(host: String, port: Int, runtime: AgentRuntime) {
        self.host = host
        self.port = port
        self.runtime = runtime
    }

    func start() throws {
        let bootstrap = ServerBootstrap(group: group)
            .serverChannelOption(ChannelOptions.backlog, value: 256)
            .serverChannelOption(ChannelOptions.socketOption(.so_reuseaddr), value: 1)
            .childChannelOption(ChannelOptions.socketOption(.so_reuseaddr), value: 1)
            .childChannelInitializer { channel in
                channel.pipeline.configureHTTPServerPipeline().flatMap {
                    channel.pipeline.addHandler(RuntimeHTTPHandler(runtime: self.runtime))
                }
            }

        channel = try bootstrap.bind(host: host, port: port).wait()
    }

    func stop() throws {
        if let channel {
            try channel.close().wait()
        }

        try group.syncShutdownGracefully()
    }
}

private final class RuntimeHTTPHandler: ChannelInboundHandler, @unchecked Sendable {
    typealias InboundIn = HTTPServerRequestPart
    typealias OutboundOut = HTTPServerResponsePart

    private let runtime: AgentRuntime
    private let domainHandlers: [any RuntimeDomainHandler]
    private var requestHead: HTTPRequestHead?
    private var requestBody: ByteBuffer?
    /// When in SSE mode, holds the conversation ID for cleanup on disconnect.
    private var sseConversationID: UUID?

    init(runtime: AgentRuntime) {
        self.runtime = runtime
        self.domainHandlers = [
            AuthDomainHandler(runtime: runtime),
            ControlDomainHandler(runtime: runtime),
            ConversationsDomainHandler(runtime: runtime),
            ApprovalsDomainHandler(runtime: runtime),
            CommunicationsDomainHandler(runtime: runtime),
            FeaturesDomainHandler(runtime: runtime)
        ]
    }

    func channelRead(context: ChannelHandlerContext, data: NIOAny) {
        let part = unwrapInboundIn(data)

        switch part {
        case .head(let head):
            requestHead = head
            requestBody = context.channel.allocator.buffer(capacity: 0)
        case .body(var bodyPart):
            requestBody?.writeBuffer(&bodyPart)
        case .end:
            handleRequest(context: context)
            requestHead = nil
            requestBody = nil
        }
    }

    func channelInactive(context: ChannelHandlerContext) {
        // When client disconnects from an SSE connection, clean up the broadcaster continuation.
        if let id = sseConversationID {
            let broadcaster = runtime.streamBroadcaster
            Task { await broadcaster.remove(conversationID: id) }
        }
        context.fireChannelInactive()
    }

    func errorCaught(context: ChannelHandlerContext, error: Error) {
        context.close(promise: nil)
    }

    private func handleRequest(context: ChannelHandlerContext) {
        guard let head = requestHead else {
            Self.writeResponse(
                context: context,
                status: .badRequest,
                data: Self.encodeErrorPayload("Missing HTTP request head.")
            )
            return
        }

        // Special case: SSE streaming — handle inline, keep channel open
        let path = head.uri.split(separator: "?", maxSplits: 1).first.map(String.init) ?? head.uri
        if head.method == .GET && path == "/message/stream" {
            let queryItems = Self.queryItems(from: head.uri)
            let contextBox = ChannelHandlerContextBox(context)
            let broadcaster = runtime.streamBroadcaster

            guard let idString = queryItems["conversationID"], let conversationID = UUID(uuidString: idString) else {
                Self.writeResponse(
                    context: context,
                    status: .badRequest,
                    data: Self.encodeErrorPayload("conversationID query parameter is required.")
                )
                return
            }

            sseConversationID = conversationID

            // Capture request headers for async auth check inside the Task.
            let requestHeaders = head.headers

            Task {
                // ── SSE auth check ──────────────────────────────────────────────
                // Apply the same Origin + session policy as standard routes.
                // This guard must run before any response headers are written.
                if let origin = requestHeaders["Origin"].first {
                    let cookieHeader = requestHeaders["Cookie"].first
                    let sessionID    = WebAuthService.sessionID(fromCookieHeader: cookieHeader)
                    if Self.isLocalhostOrigin(origin) {
                        guard await runtime.validateWebSession(id: sessionID) else {
                            contextBox.context.eventLoop.execute {
                                Self.writeResponse(
                                    context: contextBox.context,
                                    status: .unauthorized,
                                    data: Self.encodeErrorPayload("Not authenticated. Click the Atlas icon in your menu bar to open the web UI.")
                                )
                            }
                            return
                        }
                    } else {
                        guard let session = await runtime.webSessionDetail(id: sessionID),
                              session.isRemote else {
                            contextBox.context.eventLoop.execute {
                                Self.writeResponse(
                                    context: contextBox.context,
                                    status: .unauthorized,
                                    data: Self.encodeErrorPayload("Remote access requires authentication.")
                                )
                            }
                            return
                        }
                    }
                }
                // No Origin header → native URLSession — pass through.
                // ───────────────────────────────────────────────────────────────

                // Write SSE response headers (HTTP 200, text/event-stream, no Content-Length)
                contextBox.context.eventLoop.execute {
                    var headers = HTTPHeaders()
                    headers.add(name: "Content-Type", value: "text/event-stream")
                    headers.add(name: "Cache-Control", value: "no-cache")
                    headers.add(name: "Connection", value: "keep-alive")
                    let head = HTTPResponseHead(version: .http1_1, status: .ok, headers: headers)
                    contextBox.context.write(NIOAny(HTTPServerResponsePart.head(head)), promise: nil)
                    contextBox.context.flush()
                }

                // Register a stream and consume events
                let stream = await broadcaster.register(conversationID: conversationID)

                for await event in stream {
                    let sseText = event.toSSEData()
                    contextBox.context.eventLoop.execute {
                        var buffer = contextBox.context.channel.allocator.buffer(capacity: sseText.utf8.count)
                        buffer.writeString(sseText)
                        contextBox.context.writeAndFlush(NIOAny(HTTPServerResponsePart.body(.byteBuffer(buffer))), promise: nil)
                    }
                }

                // Stream finished — close the connection
                contextBox.context.eventLoop.execute {
                    contextBox.context.writeAndFlush(NIOAny(HTTPServerResponsePart.end(nil))).whenComplete { _ in
                        contextBox.context.close(promise: nil)
                    }
                }
            }
            return
        }

        // Standard request/response
        let bodyString = requestBody?.getString(at: 0, length: requestBody?.readableBytes ?? 0) ?? ""
        let contextBox = ChannelHandlerContextBox(context)

        // Compute CORS headers once for this request so they can be appended to
        // both successful responses and error responses.
        let requestOrigin = head.headers["Origin"].first
        let corsResponseHeaders: [(String, String)] = {
            guard let origin = requestOrigin, !Self.isLocalhostOrigin(origin) else { return [] }
            return [
                ("Access-Control-Allow-Origin", origin),
                ("Access-Control-Allow-Credentials", "true"),
                ("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS"),
                ("Access-Control-Allow-Headers", "Content-Type, Cookie")
            ]
        }()

        Task {
            do {
                var response = try await route(method: head.method, uri: head.uri, body: bodyString, headers: head.headers)
                response.additionalHeaders += corsResponseHeaders
                contextBox.context.eventLoop.execute {
                    if let location = response.redirectLocation {
                        Self.writeRedirect(
                            context: contextBox.context,
                            to: location,
                            additionalHeaders: response.additionalHeaders
                        )
                    } else {
                        Self.writeResponse(
                            context: contextBox.context,
                            status: response.status,
                            data: response.payload,
                            contentType: response.contentType,
                            additionalHeaders: response.additionalHeaders
                        )
                    }
                }
            } catch {
                contextBox.context.eventLoop.execute {
                    let status: HTTPResponseStatus
                    switch error {
                    case RuntimeAPIError.invalidRequest:
                        status = .badRequest
                    case RuntimeAPIError.notFound:
                        status = .notFound
                    case RuntimeAPIError.forbidden:
                        status = .forbidden
                    case RuntimeAPIError.unauthorized:
                        status = .unauthorized
                    default:
                        status = .internalServerError
                    }

                    Self.writeResponse(
                        context: contextBox.context,
                        status: status,
                        data: Self.encodeErrorPayload(Self.sanitizeErrorForClient(error)),
                        additionalHeaders: corsResponseHeaders
                    )
                }
            }
        }
    }

    private func route(method: HTTPMethod, uri: String, body: String, headers: HTTPHeaders) async throws -> EncodedResponse {
        let path       = uri.split(separator: "?", maxSplits: 1).first.map(String.init) ?? uri
        let queryItems = Self.queryItems(from: uri)

        // ── LAN access gate ─────────────────────────────────────────────────────
        // If LAN access is disabled, ALL non-localhost connections are rejected
        // immediately — regardless of path, session validity, or auth status.
        // This takes effect without a daemon restart: toggling the setting in the
        // app closes access on the very next request, even though the socket remains
        // bound to 0.0.0.0 until the daemon is restarted.
        let requestOrigin = headers["Origin"].first
        let requestHost   = headers["Host"].first
        let isNonLocalhost: Bool = {
            if let o = requestOrigin { return !Self.isLocalhostOrigin(o) }
            if let h = requestHost { return !Self.isLocalhostHost(h) }
            return false
        }()
        if isNonLocalhost {
            let lanEnabled = await runtime.remoteAccessEnabled()
            if !lanEnabled {
                throw RuntimeAPIError.unauthorized("LAN access is disabled on this Atlas instance.")
            }
        }

        // ── Auth middleware ─────────────────────────────────────────────────────
        // OPTIONS, /auth/*, and /web/* routes are exempt:
        //   - OPTIONS: CORS preflight, no payload
        //   - /auth/*: bootstrap flow itself
        //   - /web/*: static HTML/JS/CSS — no sensitive data; security is enforced at
        //             the API layer. The Preact app redirects to /auth/remote-gate when
        //             any API call returns 401 (unauthenticated remote access).
        let isAuthExempt = method == .OPTIONS || path.hasPrefix("/auth/")
            || path == "/web" || path.hasPrefix("/web/")
        if !isAuthExempt {
            if let origin = headers["Origin"].first {
                let cookieHeader = headers["Cookie"].first
                let sessionID    = WebAuthService.sessionID(fromCookieHeader: cookieHeader)
                if Self.isLocalhostOrigin(origin) {
                    // Localhost browser — require a valid session cookie.
                    guard await runtime.validateWebSession(id: sessionID) else {
                        throw RuntimeAPIError.unauthorized(
                            "Not authenticated. Click the Atlas icon in your menu bar to open the web UI."
                        )
                    }
                } else {
                    // Remote browser — must have a remote-flagged session.
                    guard let session = await runtime.webSessionDetail(id: sessionID),
                          session.isRemote else {
                        throw RuntimeAPIError.unauthorized(
                            "Remote access requires authentication. Navigate to http://<atlas-ip>:<port>/auth/remote-gate"
                        )
                    }
                }
            } else if let host = headers["Host"].first, !Self.isLocalhostHost(host) {
                // No Origin header but non-localhost Host = remote browser top-level navigation
                // (browsers don't send Origin on direct address-bar navigations).
                // Check for a valid remote session and redirect to the auth gate if missing.
                let cookieHeader = headers["Cookie"].first
                let sessionID    = WebAuthService.sessionID(fromCookieHeader: cookieHeader)
                let hasRemoteSession = await {
                    guard let s = await runtime.webSessionDetail(id: sessionID) else { return false }
                    return s.isRemote
                }()
                if !hasRemoteSession {
                    return EncodedResponse(
                        status: .found,
                        payload: Data(),
                        contentType: "text/plain",
                        redirectLocation: "/auth/remote-gate"
                    )
                }
            }
            // No Origin and localhost Host → native URLSession call — pass through without session check.
        }

        // ── Domain handler dispatch ──────────────────────────────────────────────
        for handler in domainHandlers {
            if let response = try await handler.handle(
                method: method, path: path, queryItems: queryItems, body: body, headers: headers
            ) {
                return response
            }
        }

        return EncodedResponse(
            status: .notFound,
            payload: Self.encodeErrorPayload("Route not found.")
        )
    }

    private static func writeResponse(
        context: ChannelHandlerContext,
        status: HTTPResponseStatus,
        data: Data,
        contentType: String = "application/json; charset=utf-8",
        additionalHeaders: [(String, String)] = []
    ) {
        var headers = HTTPHeaders()
        headers.add(name: "Content-Type", value: contentType)
        headers.add(name: "Content-Length", value: "\(data.count)")
        headers.add(name: "Connection", value: "close")
        for (name, value) in additionalHeaders {
            headers.add(name: name, value: value)
        }

        let head = HTTPResponseHead(version: .http1_1, status: status, headers: headers)
        context.write(NIOAny(HTTPServerResponsePart.head(head)), promise: nil)

        var buffer = context.channel.allocator.buffer(capacity: data.count)
        buffer.writeBytes(data)
        context.write(NIOAny(HTTPServerResponsePart.body(.byteBuffer(buffer))), promise: nil)
        context.writeAndFlush(NIOAny(HTTPServerResponsePart.end(nil))).whenComplete { _ in
            context.close(promise: nil)
        }
    }

    private static func writeRedirect(
        context: ChannelHandlerContext,
        to location: String,
        additionalHeaders: [(String, String)] = []
    ) {
        var headers = HTTPHeaders()
        headers.add(name: "Location", value: location)
        headers.add(name: "Content-Length", value: "0")
        for (name, value) in additionalHeaders {
            headers.add(name: name, value: value)
        }
        let head = HTTPResponseHead(version: .http1_1, status: .found, headers: headers)
        context.write(NIOAny(HTTPServerResponsePart.head(head)), promise: nil)
        context.writeAndFlush(NIOAny(HTTPServerResponsePart.end(nil))).whenComplete { _ in
            context.close(promise: nil)
        }
    }

    private static func encodeErrorPayload(_ message: String) -> Data {
        (try? AtlasJSON.encoder.encode(["error": message])) ?? Data(#"{"error":"Encoding failure."}"#.utf8)
    }

    /// Maps an error to a safe string for HTTP clients.
    ///
    /// `AtlasToolError` and `RuntimeAPIError` carry user-facing messages by design and are passed
    /// through verbatim. All other error types (database, Keychain, file-system internals, decoding
    /// errors from third-party code) are replaced with a generic message to avoid leaking internal
    /// paths, schema details, or system information to any process that can reach the local HTTP API.
    private static func sanitizeErrorForClient(_ error: Error) -> String {
        switch error {
        case let toolError as AtlasToolError:
            return toolError.localizedDescription
        case let apiError as RuntimeAPIError:
            return apiError.localizedDescription
        case let forgeError as ForgeProposalError:
            // ForgeProposalError descriptions are user-safe: "not found", "wrong status", "not ready"
            return forgeError.localizedDescription
        case let dashboardError as DashboardStoreError:
            return dashboardError.localizedDescription
        case let dashboardExecError as DashboardExecutionError:
            return dashboardExecError.localizedDescription
        case let dashboardPlannerError as DashboardPlannerError:
            return dashboardPlannerError.localizedDescription
        case let skillError as CoreSkillServiceError:
            // CoreSkillServiceError.missingCredential is the expected user-facing case (API key not set)
            return skillError.localizedDescription
        case is DecodingError:
            return "Invalid request format."
        default:
            return "An internal error occurred. Check the activity log for details."
        }
    }

    /// Returns `true` when `origin` refers to a localhost address (any port).
    private static func isLocalhostOrigin(_ origin: String) -> Bool {
        origin.hasPrefix("http://localhost") || origin.hasPrefix("http://127.0.0.1")
    }

    /// Returns `true` when the HTTP `Host` header refers to a localhost address.
    private static func isLocalhostHost(_ host: String) -> Bool {
        host.hasPrefix("localhost") || host.hasPrefix("127.0.0.1")
    }

    private static func queryItems(from uri: String) -> [String: String] {
        guard let components = URLComponents(string: "http://localhost\(uri)") else {
            return [:]
        }

        return components.queryItems?.reduce(into: [String: String]()) { partialResult, item in
            partialResult[item.name] = item.value
        } ?? [:]
    }
}

private final class ChannelHandlerContextBox: @unchecked Sendable {
    let context: ChannelHandlerContext

    init(_ context: ChannelHandlerContext) {
        self.context = context
    }
}
