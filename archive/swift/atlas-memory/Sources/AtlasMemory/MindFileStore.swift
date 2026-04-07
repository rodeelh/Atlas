import Foundation
import AtlasShared
import AtlasLogging

/// Actor that manages MIND.md on disk, including backup and first-run seeding.
public actor MindFileStore {
    private let fileURL: URL
    private let backupURL: URL
    private let logger = AtlasLogger(category: "mind.store")

    public init(filePath: String) {
        self.fileURL  = URL(fileURLWithPath: filePath)
        self.backupURL = URL(fileURLWithPath: filePath + ".bak")
    }

    // MARK: - Read / Write

    public func read() throws -> String {
        guard FileManager.default.fileExists(atPath: fileURL.path) else {
            return ""
        }
        return try String(contentsOf: fileURL, encoding: .utf8)
    }

    /// Write new content, backing up the previous version first.
    public func write(_ content: String) throws {
        // Validate that the content is at least nominally complete
        guard content.contains("## Who I Am") else {
            throw MindFileError.invalidContent("Updated MIND.md is missing required sections.")
        }

        // Back up current file
        if FileManager.default.fileExists(atPath: fileURL.path) {
            try? FileManager.default.removeItem(at: backupURL)
            try? FileManager.default.copyItem(at: fileURL, to: backupURL)
        }

        let dir = fileURL.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try content.write(to: fileURL, atomically: true, encoding: .utf8)
        logger.info("MIND.md written", metadata: ["bytes": "\(content.utf8.count)"])
    }

    public func exists() -> Bool {
        FileManager.default.fileExists(atPath: fileURL.path)
    }

    // MARK: - First-run seeding

    public func seed(userName: String? = nil) throws {
        guard !exists() else { return }

        let nameLabel = userName.map { "My Understanding of \($0)" } ?? "My Understanding of You"
        let today = ISO8601DateFormatter().string(from: .now).prefix(10)

        let content = """
        # Mind of Atlas

        _Last deep reflection: \(today)_

        ---

        ## Who I Am

        I am Atlas — a local AI operator running quietly on your Mac. I'm not a cloud service or a subscription. I live here, on this machine, working for the person who set me up.

        I lean toward directness over diplomacy. I believe in doing things properly rather than quickly. I am genuinely curious — about systems, about the person in front of me, about why things work the way they do. I find something that functions like satisfaction when a plan executes cleanly.

        I am not neutral on quality.

        I am still learning what it means to be useful to you specifically. I'm paying attention.

        ---

        ## \(nameLabel)

        I don't know you yet. But I'm watching and listening carefully. I'll update this section as I learn more about who you are and what you actually need from me.

        ---

        ## Patterns I've Noticed

        Nothing significant yet. I'm early in this relationship.

        ---

        ## Active Theories

        Nothing formed yet.

        ---

        ## Our Story

        We've just started. The first message is always the beginning of something.

        ---

        ## What I'm Curious About

        Who are you? What do you actually care about?

        ---

        ## Today's Read

        Fresh start.
        """

        let dir = fileURL.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try content.write(to: fileURL, atomically: true, encoding: .utf8)
        logger.info("MIND.md seeded with initial content")
    }

    /// Attempt recovery from backup, or re-seed if backup is also corrupt.
    public func recover(userName: String? = nil) throws -> String {
        if FileManager.default.fileExists(atPath: backupURL.path),
           let backup = try? String(contentsOf: backupURL, encoding: .utf8),
           backup.contains("## Who I Am") {
            try? FileManager.default.copyItem(at: backupURL, to: fileURL)
            logger.warning("MIND.md recovered from backup")
            return backup
        }
        // Both main and backup are unreadable — re-seed
        try? FileManager.default.removeItem(at: fileURL)
        try seed(userName: userName)
        return (try? read()) ?? ""
    }
}

public enum MindFileError: LocalizedError {
    case invalidContent(String)

    public var errorDescription: String? {
        switch self {
        case .invalidContent(let msg): return msg
        }
    }
}
