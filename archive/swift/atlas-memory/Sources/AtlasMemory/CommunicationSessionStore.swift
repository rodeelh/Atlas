import Foundation
import AtlasShared

public actor CommunicationSessionStore: ChatSessionStoring {
    private let memoryStore: MemoryStore

    public init(memoryStore: MemoryStore) {
        self.memoryStore = memoryStore
    }

    public func allChannels(platform: ChatPlatform? = nil) async throws -> [CommunicationChannel] {
        try await memoryStore.fetchCommunicationChannels(platform: platform)
    }

    public func channel(
        platform: ChatPlatform,
        channelID: String,
        threadID: String? = nil
    ) async throws -> CommunicationChannel? {
        try await memoryStore.fetchCommunicationChannel(platform: platform, channelID: channelID, threadID: threadID)
    }

    public func resolveSession(
        platform: ChatPlatform,
        chatID: String,
        userID: String?,
        threadID: String? = nil,
        channelName: String? = nil,
        lastMessageID: String? = nil,
        platformContext: String? = nil
    ) async throws -> ChatSession {
        if let existing = try await memoryStore.fetchCommunicationChannel(platform: platform, channelID: chatID, threadID: threadID) {
            let updated = try await memoryStore.upsertCommunicationChannel(
                platform: platform,
                channelID: chatID,
                threadID: threadID,
                channelName: channelName,
                userID: userID ?? existing.userID,
                conversationID: existing.activeConversationID,
                lastMessageID: lastMessageID ?? existing.lastMessageID
            )
            return updated.session
        }

        let conversationID = UUID()
        _ = try await memoryStore.createConversation(id: conversationID, platformContext: platformContext)
        let channel = try await memoryStore.upsertCommunicationChannel(
            platform: platform,
            channelID: chatID,
            threadID: threadID,
            channelName: channelName,
            userID: userID,
            conversationID: conversationID,
            lastMessageID: lastMessageID
        )
        return channel.session
    }

    public func rotateConversation(
        platform: ChatPlatform,
        chatID: String,
        userID: String?,
        threadID: String? = nil,
        channelName: String? = nil,
        lastMessageID: String? = nil,
        platformContext: String? = nil
    ) async throws -> ChatSession {
        let channel = try await memoryStore.rotateCommunicationChannel(
            platform: platform,
            channelID: chatID,
            threadID: threadID,
            channelName: channelName,
            userID: userID,
            lastMessageID: lastMessageID,
            platformContext: platformContext
        )
        return channel.session
    }

    public func resolveSession(
        chatID: String,
        userID: String?,
        platformContext: String?
    ) async throws -> ChatSession {
        try await resolveSession(
            platform: .telegram,
            chatID: chatID,
            userID: userID,
            threadID: nil,
            platformContext: platformContext
        )
    }

    public func rotateConversation(
        chatID: String,
        userID: String?,
        platformContext: String?
    ) async throws -> ChatSession {
        try await rotateConversation(
            platform: .telegram,
            chatID: chatID,
            userID: userID,
            threadID: nil,
            platformContext: platformContext
        )
    }

    public func session(chatID: String) async throws -> ChatSession? {
        try await channel(platform: .telegram, channelID: chatID, threadID: nil)?.session
    }
}
