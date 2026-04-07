import Foundation
import AtlasLogging
import AtlasShared

// MARK: - APIValidationStore

/// Persists `APIValidationAuditRecord` values as a JSON file in Application Support.
///
/// Mirrors the `DashboardStore` / `ActionPolicyStore` pattern of flat JSON file persistence.
/// Keeps a maximum of 100 records (drops oldest on overflow).
///
/// Store at: `~/Library/Application Support/ProjectAtlas/api-validation-history.json`
public actor APIValidationStore {

    private static let maxRecords = 100
    private static let fileName = "api-validation-history.json"

    private var records: [APIValidationAuditRecord] = []
    private let fileURL: URL
    private let logger: AtlasLogger

    public init(
        directory: URL? = nil,
        logger: AtlasLogger = AtlasLogger(category: "api.validation.store")
    ) {
        let dir = directory ?? Self.defaultDirectory()
        self.fileURL = dir.appendingPathComponent(Self.fileName)
        self.logger = logger
        self.records = Self.load(from: dir.appendingPathComponent(Self.fileName))
    }

    // MARK: - Public API

    /// Append a new audit record. Drops oldest records when max capacity is exceeded.
    public func append(_ record: APIValidationAuditRecord) {
        records.append(record)
        if records.count > Self.maxRecords {
            records = Array(records.dropFirst(records.count - Self.maxRecords))
        }
        persist()
    }

    /// List the most recent audit records, newest first.
    public func listRecent(limit: Int = 50) -> [APIValidationAuditRecord] {
        let sorted = records.sorted { $0.timestamp > $1.timestamp }
        return limit > 0 ? Array(sorted.prefix(limit)) : sorted
    }

    /// Remove all records. Intended for tests.
    public func clear() {
        records = []
        persist()
    }

    // MARK: - Persistence

    private func persist() {
        do {
            let directory = fileURL.deletingLastPathComponent()
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let data = try AtlasJSON.encoder.encode(records)
            try data.write(to: fileURL, options: .atomic)
        } catch {
            logger.error("APIValidationStore: failed to persist records", metadata: [
                "error": error.localizedDescription
            ])
        }
    }

    private static func load(from url: URL) -> [APIValidationAuditRecord] {
        guard let data = try? Data(contentsOf: url) else { return [] }
        return (try? AtlasJSON.decoder.decode([APIValidationAuditRecord].self, from: data)) ?? []
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
