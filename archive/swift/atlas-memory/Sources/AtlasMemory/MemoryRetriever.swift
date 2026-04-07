import Foundation
import AtlasLogging
import AtlasShared

public protocol MemoryRetrieving: Sendable {
    func retrieveRelevantMemories(
        for userInput: String,
        conversationID: UUID?
    ) async throws -> [MemoryItem]
}

public struct MemoryRetriever: MemoryRetrieving, Sendable {
    private let memoryStore: MemoryStore
    private let scorer: any MemoryScoring
    private let config: AtlasConfig
    private let logger: AtlasLogger

    public init(
        memoryStore: MemoryStore,
        scorer: any MemoryScoring = MemoryScorer(),
        config: AtlasConfig,
        logger: AtlasLogger = AtlasLogger(category: "memory.retrieval")
    ) {
        self.memoryStore = memoryStore
        self.scorer = scorer
        self.config = config
        self.logger = logger
    }

    public func retrieveRelevantMemories(
        for userInput: String,
        conversationID: UUID?
    ) async throws -> [MemoryItem] {
        guard config.memoryEnabled else {
            logger.debug("Memory retrieval skipped", metadata: ["reason": "disabled"])
            return []
        }

        let trimmedInput = userInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedInput.isEmpty else {
            logger.debug("Memory retrieval skipped", metadata: ["reason": "empty_input"])
            return []
        }

        let query = MemoryRetrievalQuery(
            userInput: trimmedInput,
            conversationID: conversationID
        )

        let memories = try await memoryStore.listMemories(limit: 300)
        let scored = memories
            .map { memory in
                (memory: memory, score: scorer.retrievalScore(for: memory, query: query))
            }
            .filter { candidate in
                candidate.score >= 0.32 &&
                isRelevant(
                    candidate.memory,
                    for: query,
                    using: tokenize(trimmedInput)
                )
            }
            .sorted { lhs, rhs in
                if lhs.score == rhs.score {
                    return lhs.memory.updatedAt > rhs.memory.updatedAt
                }

                return lhs.score > rhs.score
            }

        var selected = Array(scored.prefix(config.maxRetrievedMemoriesPerTurn))

        if selected.isEmpty {
            let fallback = memories
                .filter { memory in
                    [.preference, .workflow, .profile].contains(memory.category) &&
                    memory.importance >= 0.85 &&
                    memory.confidence >= 0.82
                }
                .sorted { lhs, rhs in
                    if lhs.isUserConfirmed != rhs.isUserConfirmed {
                        return lhs.isUserConfirmed && !rhs.isUserConfirmed
                    }
                    if lhs.importance == rhs.importance {
                        return lhs.updatedAt > rhs.updatedAt
                    }

                    return lhs.importance > rhs.importance
                }
                .prefix(min(2, config.maxRetrievedMemoriesPerTurn))
                .map { (memory: $0, score: 0.5) }

            selected.append(contentsOf: fallback)
        }

        let selectedMemories = selected.map(\.memory)

        if !selectedMemories.isEmpty {
            try await memoryStore.touchMemories(
                ids: selectedMemories.map(\.id),
                retrievedAt: query.now
            )
        }

        logger.info("Retrieved memories", metadata: [
            "conversation_id": conversationID?.uuidString ?? "none",
            "retrieved_count": "\(selectedMemories.count)",
            "candidate_count": "\(memories.count)",
            "categories": selectedMemories.map(\.category.rawValue).joined(separator: ",")
        ])

        return selectedMemories
    }

    private func isRelevant(
        _ memory: MemoryItem,
        for query: MemoryRetrievalQuery,
        using queryTokens: Set<String>
    ) -> Bool {
        let memoryTokens = tokenize(
            [memory.title, memory.content, memory.tags.joined(separator: " ")].joined(separator: " ")
        )
        let lexicalOverlap = overlapScore(lhs: queryTokens, rhs: memoryTokens)
        let tagOverlap = overlapScore(lhs: queryTokens, rhs: Set(memory.tags.map { $0.lowercased() }))
        let categoryHint = overlapScore(lhs: queryTokens, rhs: categoryKeywords(for: memory.category))
        let sameConversation = query.conversationID != nil && query.conversationID == memory.relatedConversationID

        if lexicalOverlap >= 0.14 || tagOverlap > 0 || categoryHint >= 0.25 {
            return true
        }

        if sameConversation && lexicalOverlap >= 0.06 {
            return true
        }

        if sameConversation && categoryHint > 0 && memory.category == .episodic {
            return true
        }

        return false
    }

    private func categoryKeywords(for category: MemoryCategory) -> Set<String> {
        switch category {
        case .profile:
            return ["name", "call", "setup", "platform", "macos", "environment", "location", "weather", "city", "town"]
        case .preference:
            return ["prefer", "preference", "style", "concise", "verbose", "approval", "telegram", "remote", "assistant", "name", "weather", "temperature", "forecast", "fahrenheit", "celsius", "unit"]
        case .project:
            return ["atlas", "project", "build", "feature", "runtime", "ui", "memory", "telegram"]
        case .workflow:
            return ["workflow", "process", "usually", "codex", "xcode", "stabilize", "automation"]
        case .episodic:
            return ["recent", "last", "validated", "happened", "working", "milestone"]
        }
    }

    private func tokenize(_ text: String) -> Set<String> {
        Set(
            text
                .lowercased()
                .replacingOccurrences(of: "[^a-z0-9\\s]", with: " ", options: .regularExpression)
                .split(separator: " ")
                .map(String.init)
                .filter { $0.count > 2 }
        )
    }

    private func overlapScore(lhs: Set<String>, rhs: Set<String>) -> Double {
        guard !lhs.isEmpty, !rhs.isEmpty else {
            return 0
        }

        let intersection = lhs.intersection(rhs).count
        return Double(intersection) / Double(min(lhs.count, rhs.count))
    }
}
