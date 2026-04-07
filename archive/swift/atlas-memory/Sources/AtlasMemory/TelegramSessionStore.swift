import Foundation
import AtlasShared

public actor TelegramSessionStore: ChatSessionStoring {
    private let memoryStore: MemoryStore

    public init(memoryStore: MemoryStore) {
        self.memoryStore = memoryStore
    }

    // MARK: - Telegram-native API (Int64 chat IDs)

    public func allSessions() async throws -> [TelegramSession] {
        try await memoryStore.fetchAllTelegramSessions()
    }

    public func session(forChatID chatID: Int64) async throws -> TelegramSession? {
        try await memoryStore.fetchTelegramSession(chatID: chatID)
    }

    public func resolveSession(
        chatID: Int64,
        userID: Int64?,
        lastMessageID: Int? = nil,
        platformContext: String? = nil
    ) async throws -> TelegramSession {
        if let existing = try await memoryStore.fetchTelegramSession(chatID: chatID) {
            return try await memoryStore.upsertTelegramSession(
                chatID: chatID,
                userID: userID ?? existing.userID,
                conversationID: existing.activeConversationID,
                lastMessageID: lastMessageID ?? existing.lastTelegramMessageID
            )
        }

        let conversationID = UUID()
        _ = try await memoryStore.createConversation(id: conversationID, platformContext: platformContext)
        return try await memoryStore.upsertTelegramSession(
            chatID: chatID,
            userID: userID,
            conversationID: conversationID,
            lastMessageID: lastMessageID
        )
    }

    public func rotateConversation(
        chatID: Int64,
        userID: Int64?,
        lastMessageID: Int? = nil,
        platformContext: String? = nil
    ) async throws -> TelegramSession {
        try await memoryStore.rotateTelegramSession(
            chatID: chatID,
            userID: userID,
            lastMessageID: lastMessageID,
            platformContext: platformContext
        )
    }

    // MARK: - ChatSessionStoring conformance (String-typed IDs)

    public func resolveSession(
        chatID: String,
        userID: String?,
        platformContext: String?
    ) async throws -> ChatSession {
        guard let chatIDInt = Int64(chatID) else {
            throw TelegramSessionStoreError.invalidChatID(chatID)
        }
        let userIDInt = userID.flatMap(Int64.init)
        let session = try await resolveSession(
            chatID: chatIDInt,
            userID: userIDInt,
            platformContext: platformContext
        )
        return session.toChatSession()
    }

    public func rotateConversation(
        chatID: String,
        userID: String?,
        platformContext: String?
    ) async throws -> ChatSession {
        guard let chatIDInt = Int64(chatID) else {
            throw TelegramSessionStoreError.invalidChatID(chatID)
        }
        let userIDInt = userID.flatMap(Int64.init)
        let session = try await rotateConversation(
            chatID: chatIDInt,
            userID: userIDInt,
            platformContext: platformContext
        )
        return session.toChatSession()
    }

    public func session(chatID: String) async throws -> ChatSession? {
        guard let chatIDInt = Int64(chatID) else {
            throw TelegramSessionStoreError.invalidChatID(chatID)
        }
        return try await session(forChatID: chatIDInt)?.toChatSession()
    }
}

public enum TelegramSessionStoreError: Error {
    case invalidChatID(String)
}

private extension TelegramSession {
    func toChatSession() -> ChatSession {
        ChatSession(
            id: UUID(),
            platform: .telegram,
            platformChatID: String(chatID),
            platformUserID: userID.map(String.init),
            activeConversationID: activeConversationID,
            createdAt: createdAt,
            updatedAt: updatedAt
        )
    }
}
