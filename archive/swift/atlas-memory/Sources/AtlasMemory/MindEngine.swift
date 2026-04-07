import Foundation
import AtlasShared
import AtlasLogging

/// Orchestrates MIND.md: loads/caches the file, fires non-blocking reflection tasks after turns.
public actor MindEngine {
    private let fileStore: MindFileStore
    private let reflectionService: MindReflectionService
    private var cachedContent: String = ""
    private let logger = AtlasLogger(category: "mind.engine")

    public init(fileStore: MindFileStore, reflectionService: MindReflectionService) {
        self.fileStore = fileStore
        self.reflectionService = reflectionService
    }

    // MARK: - Lifecycle

    /// Load MIND.md from disk into the in-memory cache. Call on startup.
    public func load() async throws {
        if await fileStore.exists() {
            do {
                cachedContent = try await fileStore.read()
                logger.info("MIND.md loaded", metadata: ["bytes": "\(cachedContent.utf8.count)"])
            } catch {
                logger.warning("MIND.md read failed — attempting recovery", metadata: ["error": error.localizedDescription])
                cachedContent = try await fileStore.recover()
            }
        } else {
            // First run — seed then load
            try await fileStore.seed()
            cachedContent = (try? await fileStore.read()) ?? ""
            logger.info("MIND.md seeded on first run")
        }
    }

    /// Returns cached MIND.md content synchronously — safe to call from the hot path.
    public func currentContent() -> String {
        cachedContent
    }

    /// Returns the full content as a formatted block for the system prompt.
    public func systemPromptBlock() -> String {
        guard !cachedContent.isEmpty else { return "" }
        return cachedContent
    }

    // MARK: - Reflection (non-blocking)

    /// Fires reflection as a detached Task — turns return to the user immediately.
    public func reflect(turn: MindTurnRecord) {
        let currentMind  = cachedContent
        let reflSvc      = reflectionService
        let fileStore    = self.fileStore
        let logger       = self.logger

        Task {
            do {
                // Tier 1 — always update Today's Read
                let withUpdatedRead = try await reflSvc.updateTodaysRead(currentMind: currentMind, turn: turn)
                try await fileStore.write(withUpdatedRead)
                self.updateCache(withUpdatedRead)

                // Tier 2 — gated deep reflection
                let isSignificant = try await reflSvc.assessSignificance(currentMind: withUpdatedRead, turn: turn)
                if isSignificant {
                    let deepReflected = try await reflSvc.deepReflect(currentMind: withUpdatedRead, turn: turn)
                    // Re-apply Tier 1 patch on top of deep reflection (in case Today's Read was overwritten)
                    let finalWithRead = try await reflSvc.updateTodaysRead(currentMind: deepReflected, turn: turn)
                    try await fileStore.write(finalWithRead)
                    self.updateCache(finalWithRead)
                    logger.info("MIND.md deep reflection completed")
                }
            } catch {
                // Reflection failure must never crash the turn — just log and continue
                logger.warning("MIND.md reflection failed", metadata: ["error": error.localizedDescription])
            }
        }
    }

    // MARK: - Direct update (from /mind PUT endpoint)

    public func updateContent(_ content: String) async throws {
        try await fileStore.write(content)
        cachedContent = content
    }

    // MARK: - Private

    private func updateCache(_ content: String) {
        cachedContent = content
    }
}
