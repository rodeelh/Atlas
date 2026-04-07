import Foundation
import AtlasLogging
import AtlasShared

public enum WorkflowStoreError: LocalizedError, Sendable {
    case definitionNotFound(String)
    case runNotFound(UUID)

    public var errorDescription: String? {
        switch self {
        case .definitionNotFound(let id):
            return "Workflow '\(id)' not found."
        case .runNotFound(let id):
            return "Workflow run '\(id.uuidString)' not found."
        }
    }
}

public actor WorkflowStore {
    private var definitions: [AtlasWorkflowDefinition]
    private var runs: [AtlasWorkflowRun]

    private let definitionsFileURL: URL
    private let runsFileURL: URL
    private let logger: AtlasLogger

    public init(
        directory: URL? = nil,
        logger: AtlasLogger = AtlasLogger(category: "workflow.store")
    ) {
        let dir = directory ?? Self.defaultDirectory()
        self.definitionsFileURL = dir.appendingPathComponent("workflow-definitions.json")
        self.runsFileURL = dir.appendingPathComponent("workflow-runs.json")
        self.logger = logger
        self.definitions = Self.load([AtlasWorkflowDefinition].self, from: self.definitionsFileURL)
        self.runs = Self.load([AtlasWorkflowRun].self, from: self.runsFileURL)
    }

    public func listDefinitions() -> [AtlasWorkflowDefinition] {
        definitions.sorted { lhs, rhs in
            if lhs.updatedAt == rhs.updatedAt {
                return lhs.name.localizedCaseInsensitiveCompare(rhs.name) == .orderedAscending
            }
            return lhs.updatedAt > rhs.updatedAt
        }
    }

    public func definition(id: String) -> AtlasWorkflowDefinition? {
        definitions.first(where: { $0.id == id })
    }

    @discardableResult
    public func upsertDefinition(_ definition: AtlasWorkflowDefinition) -> AtlasWorkflowDefinition {
        definitions.removeAll(where: { $0.id == definition.id })
        definitions.append(definition)
        persistDefinitions()
        logger.info("Workflow definition upserted", metadata: ["id": definition.id, "name": definition.name])
        return definition
    }

    @discardableResult
    public func deleteDefinition(id: String) throws -> AtlasWorkflowDefinition {
        guard let existing = definitions.first(where: { $0.id == id }) else {
            throw WorkflowStoreError.definitionNotFound(id)
        }
        definitions.removeAll(where: { $0.id == id })
        persistDefinitions()
        logger.info("Workflow definition deleted", metadata: ["id": id])
        return existing
    }

    public func listRuns(workflowID: String? = nil, limit: Int = 50) -> [AtlasWorkflowRun] {
        let filtered = workflowID.map { id in
            runs.filter { $0.workflowID == id }
        } ?? runs
        return Array(filtered.sorted(by: { $0.startedAt > $1.startedAt }).prefix(max(limit, 1)))
    }

    public func run(id: UUID) -> AtlasWorkflowRun? {
        runs.first(where: { $0.id == id })
    }

    @discardableResult
    public func upsertRun(_ run: AtlasWorkflowRun) -> AtlasWorkflowRun {
        runs.removeAll(where: { $0.id == run.id })
        runs.append(run)
        runs = Array(runs.sorted(by: { $0.startedAt > $1.startedAt }).prefix(200))
        persistRuns()
        logger.info("Workflow run upserted", metadata: [
            "run_id": run.id.uuidString,
            "workflow_id": run.workflowID,
            "status": run.status.rawValue
        ])
        return run
    }

    private func persistDefinitions() {
        persist(definitions, to: definitionsFileURL)
    }

    private func persistRuns() {
        persist(runs, to: runsFileURL)
    }

    private func persist<T: Encodable>(_ value: T, to url: URL) {
        do {
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            let data = try AtlasJSON.encoder.encode(value)
            try data.write(to: url, options: .atomic)
        } catch {
            logger.error("Failed to persist workflow data", metadata: [
                "file": url.lastPathComponent,
                "error": error.localizedDescription
            ])
        }
    }

    private static func load<T: Decodable>(_ type: T.Type, from url: URL) -> T where T: ExpressibleByArrayLiteral {
        guard let data = try? Data(contentsOf: url) else { return [] }
        return (try? AtlasJSON.decoder.decode(T.self, from: data)) ?? []
    }

    private static func defaultDirectory() -> URL {
        let appSupport = (try? FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? FileManager.default.temporaryDirectory
        return appSupport.appendingPathComponent("ProjectAtlas", isDirectory: true)
    }
}
