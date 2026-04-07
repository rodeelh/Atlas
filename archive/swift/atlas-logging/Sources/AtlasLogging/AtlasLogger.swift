import Foundation
import OSLog
import AtlasShared

public struct AtlasLogger: Sendable {
    private static let entryStore = AtlasLogEntryStore()

    public let subsystem: String
    public let category: String

    private let logger: Logger

    public init(subsystem: String = "com.projectatlas", category: String) {
        self.subsystem = subsystem
        self.category = category
        self.logger = Logger(subsystem: subsystem, category: category)
    }

    public func debug(_ message: String, metadata: [String: String] = [:]) {
        log(.debug, message, metadata: metadata)
    }

    public func info(_ message: String, metadata: [String: String] = [:]) {
        log(.info, message, metadata: metadata)
    }

    public func warning(_ message: String, metadata: [String: String] = [:]) {
        log(.warning, message, metadata: metadata)
    }

    public func error(_ message: String, metadata: [String: String] = [:]) {
        log(.error, message, metadata: metadata)
    }

    public func log(_ level: AtlasLogLevel, _ message: String, metadata: [String: String] = [:]) {
        let metadataText = metadata.isEmpty
            ? ""
            : " " + metadata
                .sorted { $0.key < $1.key }
                .map { "\($0.key)=\($0.value)" }
                .joined(separator: " ")

        switch level {
        case .debug:
            logger.debug("\(message, privacy: .public)\(metadataText, privacy: .public)")
        case .info:
            logger.info("\(message, privacy: .public)\(metadataText, privacy: .public)")
        case .warning:
            logger.warning("\(message, privacy: .public)\(metadataText, privacy: .public)")
        case .error:
            logger.error("\(message, privacy: .public)\(metadataText, privacy: .public)")
        }

        let entry = AtlasLogEntry(
            subsystem: subsystem,
            category: category,
            level: level,
            message: message,
            metadata: metadata
        )

        Task {
            await Self.entryStore.append(entry)
        }
    }

    public static func recentEntries(limit: Int = 200) async -> [AtlasLogEntry] {
        await entryStore.recent(limit: limit)
    }
}

public extension AtlasLogger {
    static let app = AtlasLogger(category: "app")
    static let runtime = AtlasLogger(category: "runtime")
    static let network = AtlasLogger(category: "network")
    static let security = AtlasLogger(category: "security")
}

private actor AtlasLogEntryStore {
    private var entries: [AtlasLogEntry] = []

    func append(_ entry: AtlasLogEntry) {
        entries.append(entry)

        if entries.count > 500 {
            entries.removeFirst(entries.count - 500)
        }
    }

    func recent(limit: Int) -> [AtlasLogEntry] {
        Array(entries.suffix(limit))
    }
}
