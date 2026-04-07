import Foundation
import AtlasShared

public struct MemoryRetrievalQuery: Sendable {
    public let userInput: String
    public let conversationID: UUID?
    public let now: Date

    public init(
        userInput: String,
        conversationID: UUID?,
        now: Date = .now
    ) {
        self.userInput = userInput
        self.conversationID = conversationID
        self.now = now
    }
}

public protocol MemoryScoring: Sendable {
    func retrievalScore(for memory: MemoryItem, query: MemoryRetrievalQuery) -> Double
    func candidateScore(for memory: MemoryItem) -> Double
}

public struct MemoryScorer: MemoryScoring, Sendable {
    public init() {}

    public func retrievalScore(for memory: MemoryItem, query: MemoryRetrievalQuery) -> Double {
        let lexical = lexicalOverlapScore(query.userInput, with: memory)
        let recency = recencyScore(for: memory, referenceDate: query.now)
        let conversationBoost = query.conversationID == memory.relatedConversationID ? 0.14 : 0
        let categoryBoost = categoryRetrievalBoost(memory.category)
        let sensitivityPenalty = memory.isSensitive ? 0.18 : 0

        let score =
            (lexical * 0.42) +
            (memory.importance * 0.24) +
            (memory.confidence * 0.18) +
            (recency * 0.08) +
            conversationBoost +
            categoryBoost -
            sensitivityPenalty

        return clamp(score)
    }

    public func candidateScore(for memory: MemoryItem) -> Double {
        let sourceBonus: Double
        switch memory.source {
        case .userExplicit:
            sourceBonus = 0.18
        case .conversationInference:
            sourceBonus = 0.08
        case .assistantObservation:
            sourceBonus = 0.05
        case .systemDerived:
            sourceBonus = 0.03
        }

        let confirmationBonus = memory.isUserConfirmed ? 0.12 : 0
        let categoryBoost: Double
        switch memory.category {
        case .profile, .preference, .project, .workflow:
            categoryBoost = 0.06
        case .episodic:
            categoryBoost = 0.01
        }

        return clamp(
            (memory.confidence * 0.42) +
            (memory.importance * 0.42) +
            sourceBonus +
            confirmationBonus +
            categoryBoost
        )
    }

    private func lexicalOverlapScore(_ userInput: String, with memory: MemoryItem) -> Double {
        let queryTokens = tokenize(userInput)
        guard !queryTokens.isEmpty else {
            return 0
        }

        let memoryTokens = tokenize([memory.title, memory.content, memory.tags.joined(separator: " ")].joined(separator: " "))
        guard !memoryTokens.isEmpty else {
            return 0
        }

        let intersection = queryTokens.intersection(memoryTokens).count
        let denominator = max(queryTokens.count, memoryTokens.count)
        return Double(intersection) / Double(denominator)
    }

    private func recencyScore(for memory: MemoryItem, referenceDate: Date) -> Double {
        let referencePoint = memory.lastRetrievedAt ?? memory.updatedAt
        let age = max(0, referenceDate.timeIntervalSince(referencePoint))
        let days = age / 86_400

        switch days {
        case ..<3:
            return 1.0
        case ..<14:
            return 0.72
        case ..<45:
            return 0.45
        default:
            return 0.2
        }
    }

    private func categoryRetrievalBoost(_ category: MemoryCategory) -> Double {
        switch category {
        case .preference:
            return 0.12
        case .workflow:
            return 0.1
        case .project:
            return 0.08
        case .profile:
            return 0.06
        case .episodic:
            return 0.02
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

    private func clamp(_ value: Double) -> Double {
        min(max(value, 0), 1)
    }
}
