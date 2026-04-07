import Foundation
import SQLite
import AtlasShared
import AtlasLogging

public actor MemoryStore {
    private enum EventType: String {
        case toolCall = "tool_call"
        case toolResult = "tool_result"
        case approval = "approval"
        case runtimeError = "runtime_error"
    }

    private struct RuntimeErrorPayload: Codable {
        let message: String
        let metadata: [String: String]
    }

    private let logger = AtlasLogger(category: "memory")
    private let connection: Connection
    public let databasePath: String

    private let conversationsTable = Table("conversations")
    private let messagesTable = Table("messages")
    private let eventLogsTable = Table("event_logs")
    private let deferredExecutionsTable = Table("deferred_executions")
    private let memoriesTable = Table("memories")
    private let telegramSessionsTable = Table("telegram_sessions")
    private let communicationSessionsTable = Table("communication_sessions")

    private let conversationIDColumn = Expression<String>("conversation_id")
    private let createdAtColumn = Expression<Date>("created_at")
    private let updatedAtColumn = Expression<Date>("updated_at")
    private let conversationPlatformContextColumn = Expression<String?>("platform_context")

    private let messageIDColumn = Expression<String>("message_id")
    private let roleColumn = Expression<String>("role")
    private let contentColumn = Expression<String>("content")
    private let messageTimestampColumn = Expression<Date>("timestamp")

    private let eventIDColumn = Expression<String>("event_id")
    private let eventConversationIDColumn = Expression<String?>("conversation_id")
    private let eventTypeColumn = Expression<String>("event_type")
    private let referenceIDColumn = Expression<String>("reference_id")
    private let payloadColumn = Expression<String>("payload")
    private let eventTimestampColumn = Expression<Date>("timestamp")

    private let deferredIDColumn = Expression<String>("deferred_id")
    private let deferredSourceTypeColumn = Expression<String>("source_type")
    private let deferredSkillIDColumn = Expression<String?>("skill_id")
    private let deferredToolIDColumn = Expression<String?>("tool_id")
    private let deferredActionIDColumn = Expression<String?>("action_id")
    private let deferredToolCallIDColumn = Expression<String>("tool_call_id")
    private let deferredInputJSONColumn = Expression<String>("normalized_input_json")
    private let deferredConversationIDColumn = Expression<String?>("conversation_id")
    private let deferredOriginatingMessageIDColumn = Expression<String?>("originating_message_id")
    private let deferredApprovalIDColumn = Expression<String>("approval_id")
    private let deferredSummaryColumn = Expression<String>("summary")
    private let deferredPermissionLevelColumn = Expression<String>("permission_level")
    private let deferredRiskLevelColumn = Expression<String>("risk_level")
    private let deferredStatusColumn = Expression<String>("status")
    private let deferredLastErrorColumn = Expression<String?>("last_error")
    private let deferredResultJSONColumn = Expression<String?>("result_json")
    private let deferredCreatedAtColumn = Expression<Date>("created_at")
    private let deferredUpdatedAtColumn = Expression<Date>("updated_at")

    private let memoryIDColumn = Expression<String>("memory_id")
    private let memoryCategoryColumn = Expression<String>("category")
    private let memoryTitleColumn = Expression<String>("title")
    private let memoryContentColumn = Expression<String>("content")
    private let memorySourceColumn = Expression<String>("source")
    private let memoryConfidenceColumn = Expression<Double>("confidence")
    private let memoryImportanceColumn = Expression<Double>("importance")
    private let memoryCreatedAtColumn = Expression<Date>("created_at")
    private let memoryUpdatedAtColumn = Expression<Date>("updated_at")
    private let memoryLastRetrievedAtColumn = Expression<Date?>("last_retrieved_at")
    private let memoryIsUserConfirmedColumn = Expression<Bool>("is_user_confirmed")
    private let memoryIsSensitiveColumn = Expression<Bool>("is_sensitive")
    private let memoryTagsColumn = Expression<String>("tags_json")
    private let memoryRelatedConversationIDColumn = Expression<String?>("related_conversation_id")

    private let telegramChatIDColumn = Expression<Int64>("chat_id")
    private let telegramUserIDColumn = Expression<Int64?>("user_id")
    private let telegramConversationIDColumn = Expression<String>("active_conversation_id")
    private let telegramCreatedAtColumn = Expression<Date>("created_at")
    private let telegramUpdatedAtColumn = Expression<Date>("updated_at")
    private let telegramLastMessageIDColumn = Expression<Int?>("last_message_id")

    private let communicationPlatformColumn = Expression<String>("platform")
    private let communicationChannelIDColumn = Expression<String>("channel_id")
    private let communicationThreadIDColumn = Expression<String>("thread_id")
    private let communicationChannelNameColumn = Expression<String?>("channel_name")
    private let communicationUserIDColumn = Expression<String?>("user_id")
    private let communicationConversationIDColumn = Expression<String>("active_conversation_id")
    private let communicationCreatedAtColumn = Expression<Date>("created_at")
    private let communicationUpdatedAtColumn = Expression<Date>("updated_at")
    private let communicationLastMessageIDColumn = Expression<String?>("last_message_id")

    // web_sessions
    private let webSessionsTable           = Table("web_sessions")
    private let wsSessionIDColumn          = Expression<String>("session_id")
    private let wsCreatedAtColumn          = Expression<Double>("created_at")
    private let wsRefreshedAtColumn        = Expression<Double>("refreshed_at")
    private let wsExpiresAtColumn          = Expression<Double>("expires_at")
    private let wsIsRemoteColumn           = Expression<Int>("is_remote")

    private let gremlinRunIDColumn = Expression<String>("run_id")
    private let gremlinIDColumn = Expression<String>("gremlin_id")
    private let gremlinStartedAtColumn = Expression<Double>("started_at")
    private let gremlinFinishedAtColumn = Expression<Double?>("finished_at")
    private let gremlinStatusColumn = Expression<String>("status")
    private let gremlinOutputColumn = Expression<String?>("output")
    private let gremlinErrorColumn = Expression<String?>("error_message")
    private let gremlinConversationIDColumn = Expression<String?>("conversation_id")
    private let gremlinWorkflowRunIDColumn = Expression<String?>("workflow_run_id")
    private let gremlinRunsTable = Table("gremlin_runs")

    // forge_proposals
    private let forgeProposalsTable       = Table("forge_proposals")
    private let fpIDColumn                = Expression<String>("id")
    private let fpSkillIDColumn           = Expression<String>("skill_id")
    private let fpNameColumn              = Expression<String>("name")
    private let fpDescriptionColumn       = Expression<String>("description")
    private let fpSummaryColumn           = Expression<String>("summary")
    private let fpRationaleColumn         = Expression<String?>("rationale")
    private let fpRequiredSecretsColumn   = Expression<String>("required_secrets")
    private let fpDomainsColumn           = Expression<String>("domains")
    private let fpActionNamesColumn       = Expression<String>("action_names")
    private let fpRiskLevelColumn         = Expression<String>("risk_level")
    private let fpStatusColumn            = Expression<String>("status")
    private let fpSpecJSONColumn          = Expression<String>("spec_json")
    private let fpPlansJSONColumn         = Expression<String>("plans_json")
    private let fpContractJSONColumn      = Expression<String?>("contract_json")
    private let fpCreatedAtColumn         = Expression<Double>("fp_created_at")
    private let fpUpdatedAtColumn         = Expression<Double>("fp_updated_at")

    public init(databasePath: String? = nil) throws {
        let resolvedPath = try Self.resolveDatabasePath(override: databasePath)
        let fileExisted = FileManager.default.fileExists(atPath: resolvedPath)
        self.databasePath = resolvedPath
        self.connection = try Connection(resolvedPath)
        try Self.createSchema(on: connection)
        Self.migrateSchema(on: connection)
        let existingMemoryCount = (try? connection.scalar(memoriesTable.count)) ?? 0
        logger.info("Opened Atlas memory store", metadata: [
            "path": resolvedPath,
            "file_existed": fileExisted ? "true" : "false",
            "memory_count": "\(existingMemoryCount)"
        ])
    }

    public func createConversation(id: UUID = UUID(), createdAt: Date = .now, platformContext: String? = nil) throws -> AtlasConversation {
        try connection.run(
            conversationsTable.insert(or: .ignore,
                                      conversationIDColumn <- id.uuidString,
                                      createdAtColumn <- createdAt,
                                      updatedAtColumn <- createdAt,
                                      conversationPlatformContextColumn <- platformContext)
        )

        return AtlasConversation(
            id: id,
            messages: [],
            createdAt: createdAt,
            updatedAt: createdAt,
            platformContext: platformContext
        )
    }

    public func fetchConversation(id: UUID) throws -> AtlasConversation? {
        let query = conversationsTable.filter(conversationIDColumn == id.uuidString)
        guard let row = try connection.pluck(query) else {
            return nil
        }

        let messages = try fetchMessages(for: id)
        return AtlasConversation(
            id: id,
            messages: messages,
            createdAt: row[createdAtColumn],
            updatedAt: row[updatedAtColumn],
            platformContext: row[conversationPlatformContextColumn]
        )
    }

    public func listRecentConversations(limit: Int = 20) throws -> [AtlasConversation] {
        let query = conversationsTable
            .order(updatedAtColumn.desc)
            .limit(limit)

        return try connection.prepare(query).map { row in
            let id = UUID(uuidString: row[conversationIDColumn]) ?? UUID()
            let messages = try fetchMessages(for: id)
            return AtlasConversation(
                id: id,
                messages: messages,
                createdAt: row[createdAtColumn],
                updatedAt: row[updatedAtColumn],
                platformContext: row[conversationPlatformContextColumn]
            )
        }
    }

    @discardableResult
    public func appendMessage(_ message: AtlasMessage, to conversationID: UUID) throws -> AtlasConversation {
        if try fetchConversation(id: conversationID) == nil {
            _ = try createConversation(id: conversationID, createdAt: message.timestamp)
        }

        try connection.run(
            messagesTable.insert(or: .replace,
                                 messageIDColumn <- message.id.uuidString,
                                 self.conversationIDColumn <- conversationID.uuidString,
                                 roleColumn <- message.role.rawValue,
                                 contentColumn <- message.content,
                                 messageTimestampColumn <- message.timestamp)
        )

        let conversationRow = conversationsTable.filter(self.conversationIDColumn == conversationID.uuidString)
        try connection.run(
            conversationRow.update(updatedAtColumn <- message.timestamp)
        )

        return try fetchConversation(id: conversationID) ??
            AtlasConversation(id: conversationID, messages: [message], createdAt: message.timestamp, updatedAt: message.timestamp)
    }

    public func conversationCount() throws -> Int {
        try connection.scalar(conversationsTable.count)
    }

    /// Returns lightweight summaries of the most recent conversations, newest first.
    /// Loads only the first user message and last assistant message per conversation —
    /// no full message arrays are materialised.
    public func listConversationSummaries(limit: Int = 50, offset: Int = 0) throws -> [ConversationSummary] {
        let query = conversationsTable
            .order(updatedAtColumn.desc)
            .limit(limit, offset: offset)

        return try connection.prepare(query).compactMap { row in
            guard let id = UUID(uuidString: row[conversationIDColumn]) else { return nil }
            let preview = try conversationPreview(for: id)
            return ConversationSummary(
                id: id,
                messageCount: preview.count,
                firstUserMessage: preview.firstUser,
                lastAssistantMessage: preview.lastAssistant,
                createdAt: row[createdAtColumn],
                updatedAt: row[updatedAtColumn],
                platformContext: row[conversationPlatformContextColumn]
            )
        }
    }

    /// Full-text search over message content. Returns summaries of conversations that
    /// contain at least one message matching the query (case-insensitive LIKE scan).
    public func searchConversations(query: String, limit: Int = 50) throws -> [ConversationSummary] {
        let normalised = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !normalised.isEmpty else { return [] }

        // Find conversation IDs that have a matching message, deduplicated and ordered by recency.
        let matchingConvIDs: [String] = try {
            let sql = """
                SELECT DISTINCT m.conversation_id, c.updated_at
                FROM messages m
                JOIN conversations c ON c.conversation_id = m.conversation_id
                WHERE lower(m.content) LIKE ?
                ORDER BY c.updated_at DESC
                LIMIT \(limit)
                """
            var ids: [String] = []
            let stmt = try connection.prepare(sql, "%\(normalised)%")
            for row in stmt {
                if let cid = row[0] as? String { ids.append(cid) }
            }
            return ids
        }()

        return try matchingConvIDs.compactMap { cidString in
            guard let id = UUID(uuidString: cidString) else { return nil }
            let convQuery = conversationsTable.filter(conversationIDColumn == cidString)
            guard let convRow = try connection.pluck(convQuery) else { return nil }
            let preview = try conversationPreview(for: id)
            return ConversationSummary(
                id: id,
                messageCount: preview.count,
                firstUserMessage: preview.firstUser,
                lastAssistantMessage: preview.lastAssistant,
                createdAt: convRow[createdAtColumn],
                updatedAt: convRow[updatedAtColumn],
                platformContext: convRow[conversationPlatformContextColumn]
            )
        }
    }

    /// Returns the first user message, last assistant message, and total count for a conversation.
    private func conversationPreview(for id: UUID) throws -> (firstUser: String?, lastAssistant: String?, count: Int) {
        let allMessages = try connection.prepare(
            messagesTable
                .filter(conversationIDColumn == id.uuidString)
                .order(messageTimestampColumn.asc)
        )
        var firstUser: String?
        var lastAssistant: String?
        var count = 0
        for row in allMessages {
            count += 1
            let role = row[roleColumn]
            let content = row[contentColumn]
            if role == "user", firstUser == nil {
                firstUser = String(content.prefix(200))
            }
            if role == "assistant" {
                lastAssistant = String(content.prefix(200))
            }
        }
        return (firstUser, lastAssistant, count)
    }

    public func memoryCount() throws -> Int {
        try connection.scalar(memoriesTable.count)
    }

    @discardableResult
    public func saveMemory(_ memory: MemoryItem) throws -> MemoryItem {
        try connection.run(
            memoriesTable.insert(or: .replace,
                                 memoryIDColumn <- memory.id.uuidString,
                                 memoryCategoryColumn <- memory.category.rawValue,
                                 memoryTitleColumn <- memory.title,
                                 memoryContentColumn <- memory.content,
                                 memorySourceColumn <- memory.source.rawValue,
                                 memoryConfidenceColumn <- memory.confidence,
                                 memoryImportanceColumn <- memory.importance,
                                 memoryCreatedAtColumn <- memory.createdAt,
                                 memoryUpdatedAtColumn <- memory.updatedAt,
                                 memoryLastRetrievedAtColumn <- memory.lastRetrievedAt,
                                 memoryIsUserConfirmedColumn <- memory.isUserConfirmed,
                                 memoryIsSensitiveColumn <- memory.isSensitive,
                                 memoryTagsColumn <- encodeTags(memory.tags),
                                 memoryRelatedConversationIDColumn <- memory.relatedConversationID?.uuidString)
        )

        return try fetchMemory(id: memory.id) ?? memory
    }

    public func fetchMemory(id: UUID) throws -> MemoryItem? {
        let query = memoriesTable.filter(memoryIDColumn == id.uuidString)
        guard let row = try connection.pluck(query) else {
            return nil
        }

        return try memoryItem(from: row)
    }

    public func listMemories(limit: Int = 100, category: MemoryCategory? = nil) throws -> [MemoryItem] {
        var query = memoriesTable.order(memoryImportanceColumn.desc, memoryUpdatedAtColumn.desc).limit(limit)

        if let category {
            query = query.filter(memoryCategoryColumn == category.rawValue)
        }

        return try connection.prepare(query).map { row in
            try memoryItem(from: row)
        }
    }

    public func searchMemories(
        category: MemoryCategory? = nil,
        tag: String? = nil,
        query: String? = nil,
        limit: Int = 50
    ) throws -> [MemoryItem] {
        let normalizedQuery = query?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let normalizedTag = tag?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()

        return try listMemories(limit: 300, category: category)
            .filter { item in
                let matchesTag = normalizedTag.map { tag in
                    item.tags.contains { $0.lowercased() == tag }
                } ?? true

                let matchesQuery = normalizedQuery.map { query in
                    item.title.lowercased().contains(query) ||
                    item.content.lowercased().contains(query) ||
                    item.tags.contains { $0.lowercased().contains(query) }
                } ?? true

                return matchesTag && matchesQuery
            }
            .prefix(limit)
            .map { $0 }
    }

    @discardableResult
    public func updateMemory(_ memory: MemoryItem) throws -> MemoryItem {
        try saveMemory(memory)
    }

    public func deleteMemory(id: UUID) throws {
        let query = memoriesTable.filter(memoryIDColumn == id.uuidString)
        try connection.run(query.delete())
    }

    public func markMemoryConfirmed(id: UUID, confirmed: Bool = true) throws -> MemoryItem? {
        guard let memory = try fetchMemory(id: id) else {
            return nil
        }

        let updated = memory.updating(
            updatedAt: .now,
            isUserConfirmed: confirmed
        )
        return try saveMemory(updated)
    }

    public func touchMemories(ids: [UUID], retrievedAt: Date = .now) throws {
        for id in ids {
            guard let memory = try fetchMemory(id: id) else {
                continue
            }

            _ = try saveMemory(memory.updatingRetrievedAt(retrievedAt))
        }
    }

    public func findDuplicate(for candidate: MemoryItem) throws -> MemoryItem? {
        let normalizedTitle = normalize(candidate.title)
        let normalizedContent = normalize(candidate.content)

        for memory in try listMemories(limit: 500, category: candidate.category) {
            let sameConversation = candidate.relatedConversationID != nil &&
                candidate.relatedConversationID == memory.relatedConversationID
            let titleMatch = normalize(memory.title) == normalizedTitle
            let contentMatch = normalize(memory.content) == normalizedContent
            let overlapMatch = tokenOverlapScore(
                between: normalizedTitle + " " + normalizedContent,
                and: normalize(memory.title) + " " + normalize(memory.content)
            ) >= 0.82

            if titleMatch || contentMatch || (sameConversation && overlapMatch) || overlapMatch {
                return memory
            }
        }

        return nil
    }

    public func recordToolCall(_ toolCall: AtlasToolCall, conversationID: UUID) throws {
        try recordEvent(
            id: toolCall.id,
            conversationID: conversationID,
            type: .toolCall,
            referenceID: toolCall.id.uuidString,
            payload: toolCall,
            timestamp: toolCall.timestamp
        )
    }

    public func recordToolResult(_ result: AtlasToolResult, conversationID: UUID) throws {
        try recordEvent(
            id: UUID(),
            conversationID: conversationID,
            type: .toolResult,
            referenceID: result.toolCallID.uuidString,
            payload: result,
            timestamp: result.timestamp
        )
    }

    public func recordApprovalEvent(_ request: ApprovalRequest, conversationID: UUID?) throws {
        try recordEvent(
            id: UUID(),
            conversationID: conversationID,
            type: .approval,
            referenceID: request.id.uuidString,
            payload: request,
            timestamp: request.resolvedAt ?? request.createdAt
        )
    }

    public func recordRuntimeError(
        _ message: String,
        conversationID: UUID?,
        metadata: [String: String] = [:]
    ) throws {
        try recordEvent(
            id: UUID(),
            conversationID: conversationID,
            type: .runtimeError,
            referenceID: UUID().uuidString,
            payload: RuntimeErrorPayload(message: message, metadata: metadata),
            timestamp: .now
        )
    }

    @discardableResult
    public func upsertDeferredExecution(_ request: DeferredExecutionRequest) throws -> DeferredExecutionRequest {
        try connection.run(
            deferredExecutionsTable.insert(or: .replace,
                                           deferredIDColumn <- request.id.uuidString,
                                           deferredSourceTypeColumn <- request.sourceType.rawValue,
                                           deferredSkillIDColumn <- request.skillID,
                                           deferredToolIDColumn <- request.toolID,
                                           deferredActionIDColumn <- request.actionID,
                                           deferredToolCallIDColumn <- request.toolCallID.uuidString,
                                           deferredInputJSONColumn <- request.normalizedInputJSON,
                                           deferredConversationIDColumn <- request.conversationID?.uuidString,
                                           deferredOriginatingMessageIDColumn <- request.originatingMessageID,
                                           deferredApprovalIDColumn <- request.approvalID.uuidString,
                                           deferredSummaryColumn <- request.summary,
                                           deferredPermissionLevelColumn <- request.permissionLevel.rawValue,
                                           deferredRiskLevelColumn <- request.riskLevel,
                                           deferredStatusColumn <- request.status.rawValue,
                                           deferredLastErrorColumn <- request.lastError,
                                           deferredResultJSONColumn <- encodeDeferredResult(request.result),
                                           deferredCreatedAtColumn <- request.createdAt,
                                           deferredUpdatedAtColumn <- request.updatedAt)
        )

        return request
    }

    public func fetchDeferredExecution(id: UUID) throws -> DeferredExecutionRequest? {
        let query = deferredExecutionsTable.filter(deferredIDColumn == id.uuidString)
        return try fetchDeferredExecution(query: query)
    }

    public func fetchDeferredExecution(toolCallID: UUID) throws -> DeferredExecutionRequest? {
        let query = deferredExecutionsTable.filter(deferredToolCallIDColumn == toolCallID.uuidString)
        return try fetchDeferredExecution(query: query)
    }

    public func fetchDeferredExecution(approvalID: UUID) throws -> DeferredExecutionRequest? {
        let query = deferredExecutionsTable.filter(deferredApprovalIDColumn == approvalID.uuidString)
        return try fetchDeferredExecution(query: query)
    }

    public func listDeferredExecutions(limit: Int = 500) throws -> [DeferredExecutionRequest] {
        let query = deferredExecutionsTable
            .order(deferredCreatedAtColumn.desc)
            .limit(limit)

        return try connection.prepare(query).compactMap { row in
            makeDeferredExecution(from: row)
        }
    }

    public func updateDeferredExecution(
        toolCallID: UUID,
        status: DeferredExecutionStatus,
        lastError: String? = nil,
        result: DeferredExecutionResult? = nil,
        updatedAt: Date = .now
    ) throws -> DeferredExecutionRequest? {
        guard var existing = try fetchDeferredExecution(toolCallID: toolCallID) else {
            return nil
        }

        existing = existing.updatingStatus(status, lastError: lastError, result: result, updatedAt: updatedAt)
        _ = try upsertDeferredExecution(existing)
        return existing
    }

    public func fetchAllTelegramSessions() throws -> [TelegramSession] {
        try fetchCommunicationChannels(platform: .telegram).compactMap { channel in
            guard let chatID = Int64(channel.channelID) else { return nil }
            return TelegramSession(
                chatID: chatID,
                userID: channel.userID.flatMap(Int64.init),
                activeConversationID: channel.activeConversationID,
                createdAt: channel.createdAt,
                updatedAt: channel.updatedAt,
                lastTelegramMessageID: channel.lastMessageID.flatMap(Int.init)
            )
        }
    }

    public func fetchTelegramSession(chatID: Int64) throws -> TelegramSession? {
        guard let channel = try fetchCommunicationChannel(platform: .telegram, channelID: String(chatID)) else {
            return nil
        }

        return TelegramSession(
            chatID: chatID,
            userID: channel.userID.flatMap(Int64.init),
            activeConversationID: channel.activeConversationID,
            createdAt: channel.createdAt,
            updatedAt: channel.updatedAt,
            lastTelegramMessageID: channel.lastMessageID.flatMap(Int.init)
        )
    }

    public func upsertTelegramSession(
        chatID: Int64,
        userID: Int64?,
        conversationID: UUID,
        lastMessageID: Int?,
        timestamp: Date = .now
    ) throws -> TelegramSession {
        let channel = try upsertCommunicationChannel(
            platform: .telegram,
            channelID: String(chatID),
            channelName: nil,
            userID: userID.map(String.init),
            conversationID: conversationID,
            lastMessageID: lastMessageID.map(String.init),
            timestamp: timestamp
        )

        return TelegramSession(
            chatID: chatID,
            userID: userID,
            activeConversationID: channel.activeConversationID,
            createdAt: channel.createdAt,
            updatedAt: channel.updatedAt,
            lastTelegramMessageID: lastMessageID
        )
    }

    public func rotateTelegramSession(
        chatID: Int64,
        userID: Int64?,
        newConversationID: UUID = UUID(),
        lastMessageID: Int? = nil,
        platformContext: String? = nil,
        timestamp: Date = .now
    ) throws -> TelegramSession {
        _ = try createConversation(id: newConversationID, createdAt: timestamp, platformContext: platformContext)
        return try upsertTelegramSession(
            chatID: chatID,
            userID: userID,
            conversationID: newConversationID,
            lastMessageID: lastMessageID,
            timestamp: timestamp
        )
    }

    public func fetchCommunicationChannels(platform: ChatPlatform? = nil) throws -> [CommunicationChannel] {
        let queryBase = platform.map {
            communicationSessionsTable.filter(communicationPlatformColumn == $0.rawValue)
        } ?? communicationSessionsTable
        let query = queryBase.order(communicationUpdatedAtColumn.desc)

        return try connection.prepare(query).compactMap { row in
            guard let platform = ChatPlatform(rawValue: row[communicationPlatformColumn]) else { return nil }
            return CommunicationChannel(
                id: [
                    platform.rawValue,
                    row[communicationChannelIDColumn],
                    row[communicationThreadIDColumn]
                ].joined(separator: ":"),
                platform: platform,
                channelID: row[communicationChannelIDColumn],
                channelName: row[communicationChannelNameColumn],
                userID: row[communicationUserIDColumn],
                threadID: normalizedThreadID(row[communicationThreadIDColumn]),
                activeConversationID: UUID(uuidString: row[communicationConversationIDColumn]) ?? UUID(),
                createdAt: row[communicationCreatedAtColumn],
                updatedAt: row[communicationUpdatedAtColumn],
                lastMessageID: row[communicationLastMessageIDColumn]
            )
        }
    }

    public func fetchCommunicationChannel(
        platform: ChatPlatform,
        channelID: String,
        threadID: String? = nil
    ) throws -> CommunicationChannel? {
        let query = communicationSessionsTable
            .filter(
                communicationPlatformColumn == platform.rawValue &&
                communicationChannelIDColumn == channelID &&
                communicationThreadIDColumn == normalizedThreadStorageValue(threadID)
            )
        guard let row = try connection.pluck(query) else {
            return nil
        }

        return CommunicationChannel(
            id: [
                platform.rawValue,
                row[communicationChannelIDColumn],
                row[communicationThreadIDColumn]
            ].joined(separator: ":"),
            platform: platform,
            channelID: row[communicationChannelIDColumn],
            channelName: row[communicationChannelNameColumn],
            userID: row[communicationUserIDColumn],
            threadID: normalizedThreadID(row[communicationThreadIDColumn]),
            activeConversationID: UUID(uuidString: row[communicationConversationIDColumn]) ?? UUID(),
            createdAt: row[communicationCreatedAtColumn],
            updatedAt: row[communicationUpdatedAtColumn],
            lastMessageID: row[communicationLastMessageIDColumn]
        )
    }

    public func upsertCommunicationChannel(
        platform: ChatPlatform,
        channelID: String,
        threadID: String? = nil,
        channelName: String?,
        userID: String?,
        conversationID: UUID,
        lastMessageID: String?,
        timestamp: Date = .now
    ) throws -> CommunicationChannel {
        let normalizedThreadID = normalizedThreadStorageValue(threadID)
        let existing = try fetchCommunicationChannel(platform: platform, channelID: channelID, threadID: threadID)
        try connection.run(
            communicationSessionsTable.insert(or: .replace,
                communicationPlatformColumn <- platform.rawValue,
                communicationChannelIDColumn <- channelID,
                communicationThreadIDColumn <- normalizedThreadID,
                communicationChannelNameColumn <- (channelName ?? existing?.channelName),
                communicationUserIDColumn <- (userID ?? existing?.userID),
                communicationConversationIDColumn <- conversationID.uuidString,
                communicationCreatedAtColumn <- (existing?.createdAt ?? timestamp),
                communicationUpdatedAtColumn <- timestamp,
                communicationLastMessageIDColumn <- (lastMessageID ?? existing?.lastMessageID)
            )
        )

        return try fetchCommunicationChannel(platform: platform, channelID: channelID, threadID: threadID) ?? CommunicationChannel(
            platform: platform,
            channelID: channelID,
            channelName: channelName,
            userID: userID,
            threadID: threadID,
            activeConversationID: conversationID,
            createdAt: timestamp,
            updatedAt: timestamp,
            lastMessageID: lastMessageID
        )
    }

    public func rotateCommunicationChannel(
        platform: ChatPlatform,
        channelID: String,
        threadID: String? = nil,
        channelName: String?,
        userID: String?,
        newConversationID: UUID = UUID(),
        lastMessageID: String? = nil,
        platformContext: String? = nil,
        timestamp: Date = .now
    ) throws -> CommunicationChannel {
        _ = try createConversation(id: newConversationID, createdAt: timestamp, platformContext: platformContext)
        return try upsertCommunicationChannel(
            platform: platform,
            channelID: channelID,
            threadID: threadID,
            channelName: channelName,
            userID: userID,
            conversationID: newConversationID,
            lastMessageID: lastMessageID,
            timestamp: timestamp
        )
    }

    private func fetchMessages(for conversationID: UUID) throws -> [AtlasMessage] {
        let query = messagesTable
            .filter(self.conversationIDColumn == conversationID.uuidString)
            .order(messageTimestampColumn.asc)

        return try connection.prepare(query).map { row in
            AtlasMessage(
                id: UUID(uuidString: row[messageIDColumn]) ?? UUID(),
                role: AtlasMessageRole(rawValue: row[roleColumn]) ?? .assistant,
                content: row[contentColumn],
                timestamp: row[messageTimestampColumn]
            )
        }
    }

    private func recordEvent<T: Encodable>(
        id: UUID,
        conversationID: UUID?,
        type: EventType,
        referenceID: String,
        payload: T,
        timestamp: Date
    ) throws {
        let payloadData = try AtlasJSON.encoder.encode(payload)
        let payloadString = String(decoding: payloadData, as: UTF8.self)

        try connection.run(
            eventLogsTable.insert(or: .replace,
                                  eventIDColumn <- id.uuidString,
                                  eventConversationIDColumn <- conversationID?.uuidString,
                                  eventTypeColumn <- type.rawValue,
                                  referenceIDColumn <- referenceID,
                                  payloadColumn <- payloadString,
            eventTimestampColumn <- timestamp)
        )
    }

    private func fetchDeferredExecution(query: Table) throws -> DeferredExecutionRequest? {
        guard let row = try connection.pluck(query) else {
            return nil
        }

        return makeDeferredExecution(from: row)
    }

    private func makeDeferredExecution(from row: Row) -> DeferredExecutionRequest? {
        guard
            let id = UUID(uuidString: row[deferredIDColumn]),
            let sourceType = DeferredExecutionSourceType(rawValue: row[deferredSourceTypeColumn]),
            let toolCallID = UUID(uuidString: row[deferredToolCallIDColumn]),
            let approvalID = UUID(uuidString: row[deferredApprovalIDColumn]),
            let permissionLevel = PermissionLevel(rawValue: row[deferredPermissionLevelColumn]),
            let status = DeferredExecutionStatus(rawValue: row[deferredStatusColumn])
        else {
            return nil
        }

        return DeferredExecutionRequest(
            id: id,
            sourceType: sourceType,
            skillID: row[deferredSkillIDColumn],
            toolID: row[deferredToolIDColumn],
            actionID: row[deferredActionIDColumn],
            toolCallID: toolCallID,
            normalizedInputJSON: row[deferredInputJSONColumn],
            conversationID: row[deferredConversationIDColumn].flatMap(UUID.init(uuidString:)),
            originatingMessageID: row[deferredOriginatingMessageIDColumn],
            approvalID: approvalID,
            summary: row[deferredSummaryColumn],
            permissionLevel: permissionLevel,
            riskLevel: row[deferredRiskLevelColumn],
            status: status,
            lastError: row[deferredLastErrorColumn],
            result: decodeDeferredResult(row[deferredResultJSONColumn]),
            createdAt: row[deferredCreatedAtColumn],
            updatedAt: row[deferredUpdatedAtColumn]
        )
    }

    private func normalizedThreadStorageValue(_ threadID: String?) -> String {
        threadID?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }

    private func normalizedThreadID(_ stored: String) -> String? {
        let trimmed = stored.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    private func encodeDeferredResult(_ result: DeferredExecutionResult?) -> String? {
        guard let result, let data = try? AtlasJSON.encoder.encode(result) else {
            return nil
        }

        return String(decoding: data, as: UTF8.self)
    }

    private func decodeDeferredResult(_ value: String?) -> DeferredExecutionResult? {
        guard let value, let data = value.data(using: .utf8) else {
            return nil
        }

        return try? AtlasJSON.decoder.decode(DeferredExecutionResult.self, from: data)
    }

    private static func createSchema(on connection: Connection) throws {
        let conversationsTable = Table("conversations")
        let messagesTable = Table("messages")
        let eventLogsTable = Table("event_logs")
        let deferredExecutionsTable = Table("deferred_executions")
        let memoriesTable = Table("memories")
        let telegramSessionsTable = Table("telegram_sessions")
        let communicationSessionsTable = Table("communication_sessions")

        let conversationIDColumn = Expression<String>("conversation_id")
        let createdAtColumn = Expression<Date>("created_at")
        let updatedAtColumn = Expression<Date>("updated_at")

        let messageIDColumn = Expression<String>("message_id")
        let roleColumn = Expression<String>("role")
        let contentColumn = Expression<String>("content")
        let timestampColumn = Expression<Date>("timestamp")

        let eventIDColumn = Expression<String>("event_id")
        let eventConversationIDColumn = Expression<String?>("conversation_id")
        let eventTypeColumn = Expression<String>("event_type")
        let referenceIDColumn = Expression<String>("reference_id")
        let payloadColumn = Expression<String>("payload")
        let eventTimestampColumn = Expression<Date>("timestamp")

        let deferredIDColumn = Expression<String>("deferred_id")
        let deferredSourceTypeColumn = Expression<String>("source_type")
        let deferredSkillIDColumn = Expression<String?>("skill_id")
        let deferredToolIDColumn = Expression<String?>("tool_id")
        let deferredActionIDColumn = Expression<String?>("action_id")
        let deferredToolCallIDColumn = Expression<String>("tool_call_id")
        let deferredInputJSONColumn = Expression<String>("normalized_input_json")
        let deferredConversationIDColumn = Expression<String?>("conversation_id")
        let deferredOriginatingMessageIDColumn = Expression<String?>("originating_message_id")
        let deferredApprovalIDColumn = Expression<String>("approval_id")
        let deferredSummaryColumn = Expression<String>("summary")
        let deferredPermissionLevelColumn = Expression<String>("permission_level")
        let deferredRiskLevelColumn = Expression<String>("risk_level")
        let deferredStatusColumn = Expression<String>("status")
        let deferredLastErrorColumn = Expression<String?>("last_error")
        let deferredResultJSONColumn = Expression<String?>("result_json")
        let deferredCreatedAtColumn = Expression<Date>("created_at")
        let deferredUpdatedAtColumn = Expression<Date>("updated_at")

        let memoryIDColumn = Expression<String>("memory_id")
        let memoryCategoryColumn = Expression<String>("category")
        let memoryTitleColumn = Expression<String>("title")
        let memoryContentColumn = Expression<String>("content")
        let memorySourceColumn = Expression<String>("source")
        let memoryConfidenceColumn = Expression<Double>("confidence")
        let memoryImportanceColumn = Expression<Double>("importance")
        let memoryCreatedAtColumn = Expression<Date>("created_at")
        let memoryUpdatedAtColumn = Expression<Date>("updated_at")
        let memoryLastRetrievedAtColumn = Expression<Date?>("last_retrieved_at")
        let memoryIsUserConfirmedColumn = Expression<Bool>("is_user_confirmed")
        let memoryIsSensitiveColumn = Expression<Bool>("is_sensitive")
        let memoryTagsColumn = Expression<String>("tags_json")
        let memoryRelatedConversationIDColumn = Expression<String?>("related_conversation_id")

        let telegramChatIDColumn = Expression<Int64>("chat_id")
        let telegramUserIDColumn = Expression<Int64?>("user_id")
        let telegramConversationIDColumn = Expression<String>("active_conversation_id")
        let telegramCreatedAtColumn = Expression<Date>("created_at")
        let telegramUpdatedAtColumn = Expression<Date>("updated_at")
        let telegramLastMessageIDColumn = Expression<Int?>("last_message_id")

        let communicationPlatformColumn = Expression<String>("platform")
        let communicationChannelIDColumn = Expression<String>("channel_id")
        let communicationThreadIDColumn = Expression<String>("thread_id")
        let communicationChannelNameColumn = Expression<String?>("channel_name")
        let communicationUserIDColumn = Expression<String?>("user_id")
        let communicationConversationIDColumn = Expression<String>("active_conversation_id")
        let communicationCreatedAtColumn = Expression<Date>("created_at")
        let communicationUpdatedAtColumn = Expression<Date>("updated_at")
        let communicationLastMessageIDColumn = Expression<String?>("last_message_id")

        try connection.run(
            conversationsTable.create(ifNotExists: true) { table in
                table.column(conversationIDColumn, primaryKey: true)
                table.column(createdAtColumn)
                table.column(updatedAtColumn)
            }
        )

        try connection.run(
            messagesTable.create(ifNotExists: true) { table in
                table.column(messageIDColumn, primaryKey: true)
                table.column(conversationIDColumn)
                table.column(roleColumn)
                table.column(contentColumn)
                table.column(timestampColumn)
            }
        )

        try connection.run(
            eventLogsTable.create(ifNotExists: true) { table in
                table.column(eventIDColumn, primaryKey: true)
                table.column(eventConversationIDColumn)
                table.column(eventTypeColumn)
                table.column(referenceIDColumn)
                table.column(payloadColumn)
                table.column(eventTimestampColumn)
            }
        )

        try connection.run(
            deferredExecutionsTable.create(ifNotExists: true) { table in
                table.column(deferredIDColumn, primaryKey: true)
                table.column(deferredSourceTypeColumn)
                table.column(deferredSkillIDColumn)
                table.column(deferredToolIDColumn)
                table.column(deferredActionIDColumn)
                table.column(deferredToolCallIDColumn, unique: true)
                table.column(deferredInputJSONColumn)
                table.column(deferredConversationIDColumn)
                table.column(deferredOriginatingMessageIDColumn)
                table.column(deferredApprovalIDColumn, unique: true)
                table.column(deferredSummaryColumn)
                table.column(deferredPermissionLevelColumn)
                table.column(deferredRiskLevelColumn)
                table.column(deferredStatusColumn)
                table.column(deferredLastErrorColumn)
                table.column(deferredResultJSONColumn)
                table.column(deferredCreatedAtColumn)
                table.column(deferredUpdatedAtColumn)
            }
        )

        try connection.run(
            memoriesTable.create(ifNotExists: true) { table in
                table.column(memoryIDColumn, primaryKey: true)
                table.column(memoryCategoryColumn)
                table.column(memoryTitleColumn)
                table.column(memoryContentColumn)
                table.column(memorySourceColumn)
                table.column(memoryConfidenceColumn)
                table.column(memoryImportanceColumn)
                table.column(memoryCreatedAtColumn)
                table.column(memoryUpdatedAtColumn)
                table.column(memoryLastRetrievedAtColumn)
                table.column(memoryIsUserConfirmedColumn)
                table.column(memoryIsSensitiveColumn)
                table.column(memoryTagsColumn)
                table.column(memoryRelatedConversationIDColumn)
            }
        )

        try connection.run(
            telegramSessionsTable.create(ifNotExists: true) { table in
                table.column(telegramChatIDColumn, primaryKey: true)
                table.column(telegramUserIDColumn)
                table.column(telegramConversationIDColumn)
                table.column(telegramCreatedAtColumn)
                table.column(telegramUpdatedAtColumn)
                table.column(telegramLastMessageIDColumn)
            }
        )

        try connection.run(
            communicationSessionsTable.create(ifNotExists: true) { table in
                table.column(communicationPlatformColumn)
                table.column(communicationChannelIDColumn)
                table.column(communicationThreadIDColumn, defaultValue: "")
                table.column(communicationChannelNameColumn)
                table.column(communicationUserIDColumn)
                table.column(communicationConversationIDColumn)
                table.column(communicationCreatedAtColumn)
                table.column(communicationUpdatedAtColumn)
                table.column(communicationLastMessageIDColumn)
                table.primaryKey(communicationPlatformColumn, communicationChannelIDColumn, communicationThreadIDColumn)
            }
        )

        let webSessionsTable   = Table("web_sessions")
        let wsSessionIDCol     = Expression<String>("session_id")
        let wsCreatedAtCol     = Expression<Double>("created_at")
        let wsRefreshedAtCol   = Expression<Double>("refreshed_at")
        let wsExpiresAtCol     = Expression<Double>("expires_at")

        try connection.run(webSessionsTable.create(ifNotExists: true) { t in
            t.column(wsSessionIDCol, primaryKey: true)
            t.column(wsCreatedAtCol)
            t.column(wsRefreshedAtCol)
            t.column(wsExpiresAtCol)
        })
        try connection.run("CREATE INDEX IF NOT EXISTS idx_web_sessions_expires_at ON web_sessions (expires_at)")

        let gremlinRunIDCol = Expression<String>("run_id")
        let gremlinIDCol = Expression<String>("gremlin_id")
        let gremlinStartedAtCol = Expression<Double>("started_at")
        let gremlinFinishedAtCol = Expression<Double?>("finished_at")
        let gremlinStatusCol = Expression<String>("status")
        let gremlinOutputCol = Expression<String?>("output")
        let gremlinErrorCol = Expression<String?>("error_message")
        let gremlinConvIDCol = Expression<String?>("conversation_id")
        let gremlinWorkflowRunIDCol = Expression<String?>("workflow_run_id")
        let gremlinRunsTable = Table("gremlin_runs")

        try connection.run(gremlinRunsTable.create(ifNotExists: true) { t in
            t.column(gremlinRunIDCol, primaryKey: true)
            t.column(gremlinIDCol)
            t.column(gremlinStartedAtCol)
            t.column(gremlinFinishedAtCol)
            t.column(gremlinStatusCol)
            t.column(gremlinOutputCol)
            t.column(gremlinErrorCol)
            t.column(gremlinConvIDCol)
            t.column(gremlinWorkflowRunIDCol)
        })
        try connection.run("CREATE INDEX IF NOT EXISTS idx_gremlin_runs_gremlin_id ON gremlin_runs (gremlin_id)")

        // forge_proposals
        let forgeProposalsTable = Table("forge_proposals")
        let fpIDCol             = Expression<String>("id")
        let fpSkillIDCol        = Expression<String>("skill_id")
        let fpNameCol           = Expression<String>("name")
        let fpDescriptionCol    = Expression<String>("description")
        let fpSummaryCol        = Expression<String>("summary")
        let fpRationaleCol      = Expression<String?>("rationale")
        let fpRequiredSecretsCol = Expression<String>("required_secrets")
        let fpDomainsCol        = Expression<String>("domains")
        let fpActionNamesCol    = Expression<String>("action_names")
        let fpRiskLevelCol      = Expression<String>("risk_level")
        let fpStatusCol         = Expression<String>("status")
        let fpSpecJSONCol       = Expression<String>("spec_json")
        let fpPlansJSONCol      = Expression<String>("plans_json")
        let fpCreatedAtCol      = Expression<Double>("fp_created_at")
        let fpUpdatedAtCol      = Expression<Double>("fp_updated_at")

        try connection.run(forgeProposalsTable.create(ifNotExists: true) { t in
            t.column(fpIDCol, primaryKey: true)
            t.column(fpSkillIDCol)
            t.column(fpNameCol)
            t.column(fpDescriptionCol)
            t.column(fpSummaryCol)
            t.column(fpRationaleCol)
            t.column(fpRequiredSecretsCol)
            t.column(fpDomainsCol)
            t.column(fpActionNamesCol)
            t.column(fpRiskLevelCol)
            t.column(fpStatusCol)
            t.column(fpSpecJSONCol)
            t.column(fpPlansJSONCol)
            t.column(fpCreatedAtCol)
            t.column(fpUpdatedAtCol)
        })
        try connection.run("CREATE INDEX IF NOT EXISTS idx_forge_proposals_status ON forge_proposals (status)")
    }

    /// Adds any columns that may be missing from tables created by older schema versions.
    /// SQLite errors when a column already exists, so `_ = try?` makes each statement idempotent.
    private static func migrateSchema(on connection: Connection) {
        // deferred_executions — columns added after initial schema
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN skill_id TEXT")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN tool_id TEXT")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN action_id TEXT")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN conversation_id TEXT")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN originating_message_id TEXT")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN summary TEXT NOT NULL DEFAULT ''")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN permission_level TEXT NOT NULL DEFAULT 'execute'")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN risk_level TEXT NOT NULL DEFAULT 'execute'")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN last_error TEXT")
        _ = try? connection.run("ALTER TABLE deferred_executions ADD COLUMN result_json TEXT")

        // telegram_sessions — last_message_id added after initial schema
        _ = try? connection.run("ALTER TABLE telegram_sessions ADD COLUMN last_message_id INTEGER")

        // communication_sessions — platform-agnostic bridge session store
        _ = try? connection.run("""
            CREATE TABLE IF NOT EXISTS communication_sessions (
                platform TEXT NOT NULL,
                channel_id TEXT NOT NULL,
                thread_id TEXT NOT NULL DEFAULT '',
                channel_name TEXT,
                user_id TEXT,
                active_conversation_id TEXT NOT NULL,
                created_at DATETIME NOT NULL,
                updated_at DATETIME NOT NULL,
                last_message_id TEXT,
                PRIMARY KEY (platform, channel_id, thread_id)
            )
            """)
        if (try? connection.run("ALTER TABLE communication_sessions ADD COLUMN thread_id TEXT NOT NULL DEFAULT ''")) != nil {
            _ = try? connection.run("ALTER TABLE communication_sessions RENAME TO communication_sessions_legacy")
            _ = try? connection.run("""
                CREATE TABLE communication_sessions (
                    platform TEXT NOT NULL,
                    channel_id TEXT NOT NULL,
                    thread_id TEXT NOT NULL DEFAULT '',
                    channel_name TEXT,
                    user_id TEXT,
                    active_conversation_id TEXT NOT NULL,
                    created_at DATETIME NOT NULL,
                    updated_at DATETIME NOT NULL,
                    last_message_id TEXT,
                    PRIMARY KEY (platform, channel_id, thread_id)
                )
                """)
            _ = try? connection.run("""
                INSERT OR REPLACE INTO communication_sessions
                (platform, channel_id, thread_id, channel_name, user_id, active_conversation_id, created_at, updated_at, last_message_id)
                SELECT platform, channel_id, COALESCE(thread_id, ''), channel_name, user_id, active_conversation_id, created_at, updated_at, last_message_id
                FROM communication_sessions_legacy
                """)
            _ = try? connection.run("DROP TABLE communication_sessions_legacy")
        }
        _ = try? connection.run("""
            INSERT OR IGNORE INTO communication_sessions
            (platform, channel_id, thread_id, channel_name, user_id, active_conversation_id, created_at, updated_at, last_message_id)
            SELECT 'telegram', CAST(chat_id AS TEXT), '', NULL, CAST(user_id AS TEXT), active_conversation_id, created_at, updated_at, CAST(last_message_id AS TEXT)
            FROM telegram_sessions
            """)

        // gremlin_runs — conversation_id added after initial schema
        _ = try? connection.run("ALTER TABLE gremlin_runs ADD COLUMN conversation_id TEXT")
        _ = try? connection.run("ALTER TABLE gremlin_runs ADD COLUMN workflow_run_id TEXT")

        // conversations — platform_context added for chat bridge personas
        _ = try? connection.run("ALTER TABLE conversations ADD COLUMN platform_context TEXT")

        // forge_proposals — contract_json added in v1.1 for API research audit trail
        _ = try? connection.run("ALTER TABLE forge_proposals ADD COLUMN contract_json TEXT")

        // messages — index on conversation_id for fast conversation preview queries
        _ = try? connection.run("CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages (conversation_id)")

        // web_sessions — added for persistent browser session support
        _ = try? connection.run("""
            CREATE TABLE IF NOT EXISTS web_sessions (
                session_id   TEXT PRIMARY KEY,
                created_at   REAL NOT NULL,
                refreshed_at REAL NOT NULL,
                expires_at   REAL NOT NULL,
                is_remote    INTEGER NOT NULL DEFAULT 0
            )
            """)
        _ = try? connection.run("CREATE INDEX IF NOT EXISTS idx_web_sessions_expires_at ON web_sessions (expires_at)")
        // is_remote — migration for existing installations
        _ = try? connection.run("ALTER TABLE web_sessions ADD COLUMN is_remote INTEGER NOT NULL DEFAULT 0")
    }

    public func saveGremlinRun(_ run: GremlinRun) throws {
        try connection.run(gremlinRunsTable.insert(or: .replace,
            gremlinRunIDColumn <- run.id.uuidString,
            gremlinIDColumn <- run.gremlinID,
            gremlinStartedAtColumn <- run.startedAt.timeIntervalSince1970,
            gremlinFinishedAtColumn <- run.finishedAt?.timeIntervalSince1970,
            gremlinStatusColumn <- run.status.rawValue,
            gremlinOutputColumn <- run.output,
            gremlinErrorColumn <- run.errorMessage,
            gremlinConversationIDColumn <- run.conversationID?.uuidString,
            gremlinWorkflowRunIDColumn <- run.workflowRunID?.uuidString
        ))
    }

    public func fetchGremlinRuns(gremlinID: String, limit: Int = 20) throws -> [GremlinRun] {
        let query = gremlinRunsTable
            .filter(gremlinIDColumn == gremlinID)
            .order(gremlinStartedAtColumn.desc)
            .limit(limit)
        return try connection.prepare(query).map { row in
            GremlinRun(
                id: UUID(uuidString: row[gremlinRunIDColumn]) ?? UUID(),
                gremlinID: row[gremlinIDColumn],
                startedAt: Date(timeIntervalSince1970: row[gremlinStartedAtColumn]),
                finishedAt: row[gremlinFinishedAtColumn].map { Date(timeIntervalSince1970: $0) },
                status: GremlinRunStatus(rawValue: row[gremlinStatusColumn]) ?? .failed,
                output: row[gremlinOutputColumn],
                errorMessage: row[gremlinErrorColumn],
                conversationID: row[gremlinConversationIDColumn].flatMap { UUID(uuidString: $0) },
                workflowRunID: row[gremlinWorkflowRunIDColumn].flatMap { UUID(uuidString: $0) }
            )
        }
    }

    private func memoryItem(from row: Row) throws -> MemoryItem {
        MemoryItem(
            id: UUID(uuidString: row[memoryIDColumn]) ?? UUID(),
            category: MemoryCategory(rawValue: row[memoryCategoryColumn]) ?? .episodic,
            title: row[memoryTitleColumn],
            content: row[memoryContentColumn],
            source: MemorySource(rawValue: row[memorySourceColumn]) ?? .conversationInference,
            confidence: row[memoryConfidenceColumn],
            importance: row[memoryImportanceColumn],
            createdAt: row[memoryCreatedAtColumn],
            updatedAt: row[memoryUpdatedAtColumn],
            lastRetrievedAt: row[memoryLastRetrievedAtColumn],
            isUserConfirmed: row[memoryIsUserConfirmedColumn],
            isSensitive: row[memoryIsSensitiveColumn],
            tags: try decodeTags(row[memoryTagsColumn]),
            relatedConversationID: row[memoryRelatedConversationIDColumn].flatMap(UUID.init(uuidString:))
        )
    }

    private func encodeTags(_ tags: [String]) throws -> String {
        let data = try AtlasJSON.encoder.encode(Array(Set(tags.map { $0.lowercased() })).sorted())
        return String(decoding: data, as: UTF8.self)
    }

    private func decodeTags(_ payload: String) throws -> [String] {
        guard !payload.isEmpty else {
            return []
        }

        return try AtlasJSON.decoder.decode([String].self, from: Data(payload.utf8))
    }

    private func normalize(_ text: String) -> String {
        text
            .lowercased()
            .replacingOccurrences(of: "[^a-z0-9\\s]", with: " ", options: .regularExpression)
            .replacingOccurrences(of: "\\s+", with: " ", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func tokenOverlapScore(between lhs: String, and rhs: String) -> Double {
        let lhsTokens = Set(lhs.split(separator: " ").map(String.init))
        let rhsTokens = Set(rhs.split(separator: " ").map(String.init))
        guard !lhsTokens.isEmpty, !rhsTokens.isEmpty else {
            return 0
        }

        let intersection = lhsTokens.intersection(rhsTokens).count
        let denominator = max(lhsTokens.count, rhsTokens.count)
        return Double(intersection) / Double(denominator)
    }

    // MARK: - Web Sessions

    /// Persists a new browser session. Prunes any already-expired rows as a side effect.
    public func saveWebSession(id: String, createdAt: Date, expiresAt: Date, isRemote: Bool = false) throws {
        pruneExpiredWebSessions()
        try connection.run(
            webSessionsTable.insert(or: .replace,
                wsSessionIDColumn   <- id,
                wsCreatedAtColumn   <- createdAt.timeIntervalSince1970,
                wsRefreshedAtColumn <- createdAt.timeIntervalSince1970,
                wsExpiresAtColumn   <- expiresAt.timeIntervalSince1970,
                wsIsRemoteColumn    <- isRemote ? 1 : 0
            )
        )
    }

    /// Returns the session row if it exists and has not yet expired; nil otherwise.
    public func fetchWebSession(id: String) throws -> WebSessionRecord? {
        let now = Date().timeIntervalSince1970
        let query = webSessionsTable
            .filter(wsSessionIDColumn == id && wsExpiresAtColumn > now)
        guard let row = try connection.pluck(query) else { return nil }
        return WebSessionRecord(
            id: row[wsSessionIDColumn],
            createdAt: Date(timeIntervalSince1970: row[wsCreatedAtColumn]),
            refreshedAt: Date(timeIntervalSince1970: row[wsRefreshedAtColumn]),
            expiresAt: Date(timeIntervalSince1970: row[wsExpiresAtColumn]),
            isRemote: row[wsIsRemoteColumn] != 0
        )
    }

    /// Hard-deletes all remote sessions (e.g. when the API key is regenerated).
    public func deleteAllRemoteWebSessions() throws {
        let query = webSessionsTable.filter(wsIsRemoteColumn == 1)
        try connection.run(query.delete())
    }

    /// Slides the `refreshed_at` timestamp forward without changing the hard `expires_at` ceiling.
    public func refreshWebSession(id: String, refreshedAt: Date = .now) throws {
        let query = webSessionsTable.filter(wsSessionIDColumn == id)
        try connection.run(query.update(wsRefreshedAtColumn <- refreshedAt.timeIntervalSince1970))
    }

    /// Hard-deletes a session (explicit logout / invalidation).
    public func deleteWebSession(id: String) throws {
        let query = webSessionsTable.filter(wsSessionIDColumn == id)
        try connection.run(query.delete())
    }

    /// Removes all rows whose `expires_at` is in the past. Called automatically on every save.
    private func pruneExpiredWebSessions() {
        let now = Date().timeIntervalSince1970
        let expired = webSessionsTable.filter(wsExpiresAtColumn <= now)
        try? connection.run(expired.delete())
    }

    // MARK: - Forge Proposals

    public func saveForgeProposal(_ proposal: ForgeProposalRecord) throws {
        let secretsJSON  = (try? AtlasJSON.encoder.encode(proposal.requiredSecrets)).flatMap { String(data: $0, encoding: .utf8) } ?? "[]"
        let domainsJSON  = (try? AtlasJSON.encoder.encode(proposal.domains)).flatMap { String(data: $0, encoding: .utf8) } ?? "[]"
        let actionsJSON  = (try? AtlasJSON.encoder.encode(proposal.actionNames)).flatMap { String(data: $0, encoding: .utf8) } ?? "[]"

        try connection.run(forgeProposalsTable.insert(or: .replace,
            fpIDColumn            <- proposal.id.uuidString,
            fpSkillIDColumn       <- proposal.skillID,
            fpNameColumn          <- proposal.name,
            fpDescriptionColumn   <- proposal.description,
            fpSummaryColumn       <- proposal.summary,
            fpRationaleColumn     <- proposal.rationale,
            fpRequiredSecretsColumn <- secretsJSON,
            fpDomainsColumn       <- domainsJSON,
            fpActionNamesColumn   <- actionsJSON,
            fpRiskLevelColumn     <- proposal.riskLevel,
            fpStatusColumn        <- proposal.status.rawValue,
            fpSpecJSONColumn      <- proposal.specJSON,
            fpPlansJSONColumn     <- proposal.plansJSON,
            fpContractJSONColumn  <- proposal.contractJSON,
            fpCreatedAtColumn     <- proposal.createdAt.timeIntervalSince1970,
            fpUpdatedAtColumn     <- proposal.updatedAt.timeIntervalSince1970
        ))
    }

    public func updateForgeProposalStatus(id: UUID, status: ForgeProposalStatus) throws {
        let query = forgeProposalsTable.filter(fpIDColumn == id.uuidString)
        try connection.run(query.update(
            fpStatusColumn    <- status.rawValue,
            fpUpdatedAtColumn <- Date.now.timeIntervalSince1970
        ))
    }

    public func fetchForgeProposal(id: UUID) throws -> ForgeProposalRecord? {
        let query = forgeProposalsTable.filter(fpIDColumn == id.uuidString)
        guard let row = try connection.pluck(query) else { return nil }
        return try forgeProposalRecord(from: row)
    }

    public func listForgeProposals(status: ForgeProposalStatus? = nil) throws -> [ForgeProposalRecord] {
        var query = forgeProposalsTable.order(fpCreatedAtColumn.desc)
        if let status {
            query = query.filter(fpStatusColumn == status.rawValue)
        }
        return try connection.prepare(query).map { row in
            try forgeProposalRecord(from: row)
        }
    }

    private func forgeProposalRecord(from row: Row) throws -> ForgeProposalRecord {
        let secretsJSON = row[fpRequiredSecretsColumn]
        let domainsJSON = row[fpDomainsColumn]
        let actionsJSON = row[fpActionNamesColumn]

        let secrets  = (try? AtlasJSON.decoder.decode([String].self, from: Data(secretsJSON.utf8))) ?? []
        let domains  = (try? AtlasJSON.decoder.decode([String].self, from: Data(domainsJSON.utf8))) ?? []
        let actions  = (try? AtlasJSON.decoder.decode([String].self, from: Data(actionsJSON.utf8))) ?? []
        let status   = ForgeProposalStatus(rawValue: row[fpStatusColumn]) ?? .pending

        return ForgeProposalRecord(
            id: UUID(uuidString: row[fpIDColumn]) ?? UUID(),
            skillID: row[fpSkillIDColumn],
            name: row[fpNameColumn],
            description: row[fpDescriptionColumn],
            summary: row[fpSummaryColumn],
            rationale: row[fpRationaleColumn],
            requiredSecrets: secrets,
            domains: domains,
            actionNames: actions,
            riskLevel: row[fpRiskLevelColumn],
            status: status,
            specJSON: row[fpSpecJSONColumn],
            plansJSON: row[fpPlansJSONColumn],
            contractJSON: row[fpContractJSONColumn],
            createdAt: Date(timeIntervalSince1970: row[fpCreatedAtColumn]),
            updatedAt: Date(timeIntervalSince1970: row[fpUpdatedAtColumn])
        )
    }

    private static func resolveDatabasePath(override: String?) throws -> String {
        if let override, !override.isEmpty {
            return override
        }

        let appSupportRoot = try FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )

        let atlasDirectory = appSupportRoot.appendingPathComponent("ProjectAtlas", isDirectory: true)
        try FileManager.default.createDirectory(
            at: atlasDirectory,
            withIntermediateDirectories: true,
            attributes: nil
        )

        return atlasDirectory
            .appendingPathComponent("atlas.sqlite3", isDirectory: false)
            .path
    }
}
