import Foundation
import AtlasShared
import AtlasLogging

/// Actor that manages GREMLINS.md (definitions) and gremlin_runs (SQLite run history).
public actor GremlinsFileStore {
    private let fileURL: URL
    private let memoryStore: MemoryStore
    private let parser = GremlinFileParser()
    private let logger = AtlasLogger(category: "gremlins.store")

    public init(fileURL: URL, memoryStore: MemoryStore) {
        self.fileURL = fileURL
        self.memoryStore = memoryStore
    }

    // MARK: - File operations

    public func loadGremlins() throws -> [GremlinItem] {
        guard FileManager.default.fileExists(atPath: fileURL.path) else {
            return []
        }
        let content = try String(contentsOf: fileURL, encoding: .utf8)
        return parser.parse(markdown: content)
    }

    public func saveGremlins(_ items: [GremlinItem]) throws {
        let content = parser.serialise(items)
        try content.write(to: fileURL, atomically: true, encoding: .utf8)
    }

    public func addGremlin(_ item: GremlinItem) throws {
        var items = try loadGremlins()
        // Remove any existing item with same id
        items.removeAll { $0.id == item.id }
        items.append(item)
        try saveGremlins(items)
        logger.info("Added gremlin", metadata: ["id": item.id, "schedule": item.scheduleRaw])
    }

    public func updateGremlin(_ item: GremlinItem) throws {
        var items = try loadGremlins()
        if let idx = items.firstIndex(where: { $0.id == item.id }) {
            items[idx] = item
        } else {
            items.append(item)
        }
        try saveGremlins(items)
    }

    public func deleteGremlin(id: String) throws {
        var items = try loadGremlins()
        items.removeAll { $0.id == id }
        try saveGremlins(items)
        logger.info("Deleted gremlin", metadata: ["id": id])
    }

    // MARK: - Run history (delegates to MemoryStore)

    public func saveRun(_ run: GremlinRun) async throws {
        try await memoryStore.saveGremlinRun(run)
    }

    public func runsForGremlin(gremlinID: String, limit: Int = 20) async throws -> [GremlinRun] {
        try await memoryStore.fetchGremlinRuns(gremlinID: gremlinID, limit: limit)
    }

    // MARK: - Raw file access

    public func rawMarkdown() throws -> String {
        guard FileManager.default.fileExists(atPath: fileURL.path) else {
            return ""
        }
        return try String(contentsOf: fileURL, encoding: .utf8)
    }

    public func writeRawMarkdown(_ content: String) throws {
        try content.write(to: fileURL, atomically: true, encoding: .utf8)
    }
}
