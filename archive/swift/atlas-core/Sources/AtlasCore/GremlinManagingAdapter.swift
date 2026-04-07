import Foundation
import AtlasMemory
import AtlasShared
import AtlasSkills

/// Bridges the atlas-skills GremlinManaging protocol to GremlinsFileStore + GremlinScheduler.
public actor GremlinManagingAdapter: GremlinManaging {
    private let fileStore: GremlinsFileStore
    private var scheduler: GremlinScheduler?
    private let parser = GremlinFileParser()

    public init(fileStore: GremlinsFileStore, scheduler: GremlinScheduler? = nil) {
        self.fileStore = fileStore
        self.scheduler = scheduler
    }

    public func setScheduler(_ scheduler: GremlinScheduler) {
        self.scheduler = scheduler
    }

    public func loadGremlins() async throws -> [GremlinItem] {
        try await fileStore.loadGremlins()
    }

    public func addGremlin(_ item: GremlinItem) async throws {
        try await fileStore.addGremlin(item)
        if item.isEnabled, let scheduler {
            await scheduler.schedule(item)
        }
    }

    public func updateGremlin(_ item: GremlinItem) async throws {
        try await fileStore.updateGremlin(item)
        if let scheduler {
            await scheduler.cancel(id: item.id)
            if item.isEnabled {
                await scheduler.schedule(item)
            }
        }
    }

    public func deleteGremlin(id: String) async throws {
        try await fileStore.deleteGremlin(id: id)
        if let scheduler {
            await scheduler.cancel(id: id)
        }
    }

    public func runNow(id: String) async throws -> GremlinRun {
        let gremlins = try await fileStore.loadGremlins()
        guard let gremlin = gremlins.first(where: { $0.id == id }) else {
            throw GremlinManagingError.notFound(id)
        }
        if let scheduler {
            return await scheduler.runNow(gremlin)
        }
        return GremlinRun(
            gremlinID: id, startedAt: .now, finishedAt: .now, status: .failed,
            errorMessage: "Scheduler not available"
        )
    }

    public func runsForGremlin(gremlinID: String, limit: Int) async throws -> [GremlinRun] {
        try await fileStore.runsForGremlin(gremlinID: gremlinID, limit: limit)
    }

    public func validateSchedule(_ raw: String) async -> GremlinScheduleValidation {
        parser.validateSchedule(raw)
    }

    // MARK: - Chat convenience (non-protocol)

    public func listAutomationsForChat() async -> String {
        do {
            let gremlins = try await fileStore.loadGremlins()
            guard !gremlins.isEmpty else { return "No automations configured yet." }
            return gremlins.map { "• \($0.emoji) \($0.name) — \($0.isEnabled ? "On" : "Off")" }.joined(separator: "\n")
        } catch {
            return "Automations aren't available right now."
        }
    }

    public func triggerAutomationFromChat(_ nameOrID: String) async -> String {
        do {
            let gremlins = try await fileStore.loadGremlins()
            guard let gremlin = gremlins.first(where: {
                $0.id.caseInsensitiveCompare(nameOrID) == .orderedSame
                    || $0.name.caseInsensitiveCompare(nameOrID) == .orderedSame
            }) else {
                return "I couldn't find an automation named `\(nameOrID)`."
            }
            _ = try await runNow(id: gremlin.id)
            return "Started **\(gremlin.name)**."
        } catch {
            return "I couldn't run that automation right now."
        }
    }
}

public enum GremlinManagingError: LocalizedError {
    case notFound(String)

    public var errorDescription: String? {
        switch self {
        case .notFound(let id): return "Gremlin with id '\(id)' not found."
        }
    }
}
