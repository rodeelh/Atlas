import Foundation
import AtlasShared

public actor ConversationStore {
    private let memoryStore: MemoryStore

    public init(memoryStore: MemoryStore) {
        self.memoryStore = memoryStore
    }

    public func createConversation(id: UUID = UUID()) async throws -> AtlasConversation {
        try await memoryStore.createConversation(id: id)
    }

    public func appendMessage(_ message: AtlasMessage, to conversationID: UUID) async throws -> AtlasConversation {
        try await memoryStore.appendMessage(message, to: conversationID)
    }

    public func fetchConversation(id: UUID) async throws -> AtlasConversation? {
        try await memoryStore.fetchConversation(id: id)
    }

    public func listRecentConversations(limit: Int = 20) async throws -> [AtlasConversation] {
        try await memoryStore.listRecentConversations(limit: limit)
    }
}
