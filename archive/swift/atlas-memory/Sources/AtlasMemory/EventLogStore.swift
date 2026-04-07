import Foundation
import AtlasShared

public actor EventLogStore {
    private let memoryStore: MemoryStore

    public init(memoryStore: MemoryStore) {
        self.memoryStore = memoryStore
    }

    public func persist(toolCall: AtlasToolCall, conversationID: UUID) async throws {
        try await memoryStore.recordToolCall(toolCall, conversationID: conversationID)
    }

    public func persist(toolResult: AtlasToolResult, conversationID: UUID) async throws {
        try await memoryStore.recordToolResult(toolResult, conversationID: conversationID)
    }

    public func persist(approvalRequest: ApprovalRequest, conversationID: UUID?) async throws {
        try await memoryStore.recordApprovalEvent(approvalRequest, conversationID: conversationID)
    }

    public func persist(runtimeError message: String, conversationID: UUID?, metadata: [String: String] = [:]) async throws {
        try await memoryStore.recordRuntimeError(message, conversationID: conversationID, metadata: metadata)
    }
}
