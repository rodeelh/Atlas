import Foundation
import AtlasShared

public actor DeferredExecutionStore {
    private let memoryStore: MemoryStore

    public init(memoryStore: MemoryStore) {
        self.memoryStore = memoryStore
    }

    @discardableResult
    public func upsert(_ request: DeferredExecutionRequest) async throws -> DeferredExecutionRequest {
        try await memoryStore.upsertDeferredExecution(request)
    }

    public func fetch(id: UUID) async throws -> DeferredExecutionRequest? {
        try await memoryStore.fetchDeferredExecution(id: id)
    }

    public func fetch(toolCallID: UUID) async throws -> DeferredExecutionRequest? {
        try await memoryStore.fetchDeferredExecution(toolCallID: toolCallID)
    }

    public func fetch(approvalID: UUID) async throws -> DeferredExecutionRequest? {
        try await memoryStore.fetchDeferredExecution(approvalID: approvalID)
    }

    public func list(limit: Int = 500) async throws -> [DeferredExecutionRequest] {
        try await memoryStore.listDeferredExecutions(limit: limit)
    }

    @discardableResult
    public func update(
        toolCallID: UUID,
        status: DeferredExecutionStatus,
        lastError: String? = nil,
        result: DeferredExecutionResult? = nil
    ) async throws -> DeferredExecutionRequest? {
        try await memoryStore.updateDeferredExecution(
            toolCallID: toolCallID,
            status: status,
            lastError: lastError,
            result: result
        )
    }
}
