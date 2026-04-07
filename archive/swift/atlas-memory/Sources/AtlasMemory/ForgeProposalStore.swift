import Foundation
import AtlasShared

/// SQLite-backed persistence for `ForgeProposalRecord` instances.
///
/// Thin actor wrapper over `MemoryStore` forge methods, following the same pattern
/// as `DeferredExecutionStore` and `ConversationStore`. Proposals survive daemon
/// restart and are queryable by status.
public actor ForgeProposalStore {
    private let memoryStore: MemoryStore

    public init(memoryStore: MemoryStore) {
        self.memoryStore = memoryStore
    }

    // MARK: - Write

    @discardableResult
    public func save(_ proposal: ForgeProposalRecord) async throws -> ForgeProposalRecord {
        try await memoryStore.saveForgeProposal(proposal)
        return proposal
    }

    public func updateStatus(id: UUID, status: ForgeProposalStatus) async throws {
        try await memoryStore.updateForgeProposalStatus(id: id, status: status)
    }

    // MARK: - Read

    public func fetch(id: UUID) async throws -> ForgeProposalRecord? {
        try await memoryStore.fetchForgeProposal(id: id)
    }

    /// Returns proposals ordered by `createdAt` descending.
    /// Pass `status: nil` to list all proposals regardless of status.
    public func list(status: ForgeProposalStatus? = nil) async throws -> [ForgeProposalRecord] {
        try await memoryStore.listForgeProposals(status: status)
    }

    /// Convenience: only active proposals (pending, installed, enabled).
    /// Excludes rejected and uninstalled entries.
    public func listActive() async throws -> [ForgeProposalRecord] {
        let all = try await memoryStore.listForgeProposals(status: nil)
        return all.filter { $0.status != .rejected && $0.status != .uninstalled }
    }

    /// Find the first proposal for a given skill ID in any status.
    public func findBySkillID(_ skillID: String) async throws -> ForgeProposalRecord? {
        let all = try await memoryStore.listForgeProposals(status: nil)
        return all.first { $0.skillID == skillID }
    }
}
