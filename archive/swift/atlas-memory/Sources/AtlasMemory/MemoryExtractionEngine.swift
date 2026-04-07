import Foundation
import AtlasLogging
import AtlasShared

public struct MemoryTurnRecord: Sendable {
    public let conversationID: UUID
    public let userMessage: AtlasMessage
    public let assistantMessage: AtlasMessage
    public let toolCalls: [AtlasToolCall]
    public let toolResults: [AtlasToolResult]
    public let responseStatus: AtlasAgentResponseStatus

    public init(
        conversationID: UUID,
        userMessage: AtlasMessage,
        assistantMessage: AtlasMessage,
        toolCalls: [AtlasToolCall],
        toolResults: [AtlasToolResult],
        responseStatus: AtlasAgentResponseStatus
    ) {
        self.conversationID = conversationID
        self.userMessage = userMessage
        self.assistantMessage = assistantMessage
        self.toolCalls = toolCalls
        self.toolResults = toolResults
        self.responseStatus = responseStatus
    }
}

public struct MemoryCandidate: Sendable {
    public let item: MemoryItem
    public let score: Double
    public let shouldAutoSave: Bool
    public let rationale: String

    public init(
        item: MemoryItem,
        score: Double,
        shouldAutoSave: Bool,
        rationale: String
    ) {
        self.item = item
        self.score = score
        self.shouldAutoSave = shouldAutoSave
        self.rationale = rationale
    }
}

public protocol MemoryExtracting: Sendable {
    func extractCandidates(from turn: MemoryTurnRecord) -> [MemoryCandidate]
    func extractAndPersist(from turn: MemoryTurnRecord) async throws -> [MemoryItem]
    func recoverDurableMemories(from conversations: [AtlasConversation], limit: Int) async throws -> [MemoryItem]
    func normalizeStoredDurableMemories(limit: Int) async throws -> [MemoryItem]
}

public struct MemoryExtractionEngine: MemoryExtracting, Sendable {
    private let memoryStore: MemoryStore
    private let scorer: any MemoryScoring
    private let config: AtlasConfig
    private let logger: AtlasLogger

    public init(
        memoryStore: MemoryStore,
        scorer: any MemoryScoring = MemoryScorer(),
        config: AtlasConfig,
        logger: AtlasLogger = AtlasLogger(category: "memory.extraction")
    ) {
        self.memoryStore = memoryStore
        self.scorer = scorer
        self.config = config
        self.logger = logger
    }

    public func extractCandidates(from turn: MemoryTurnRecord) -> [MemoryCandidate] {
        guard config.memoryEnabled else {
            return []
        }

        let text = turn.userMessage.content.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else {
            return []
        }

        var candidates: [MemoryCandidate] = []
        var seenKeys: Set<String> = []

        func addCandidate(_ item: MemoryItem, rationale: String, minimumScore: Double = 0.55) {
            let key = "\(item.category.rawValue)|\(normalize(item.title))|\(normalize(item.content))"
            guard !seenKeys.contains(key) else {
                return
            }

            let score = scorer.candidateScore(for: item)
            guard score >= minimumScore else {
                return
            }

            seenKeys.insert(key)
            let shouldAutoSave = item.isUserConfirmed || score >= config.memoryAutoSaveThreshold
            candidates.append(
                MemoryCandidate(
                    item: item,
                    score: score,
                    shouldAutoSave: shouldAutoSave,
                    rationale: rationale
                )
            )
        }

        if let explicit = explicitMemoryCandidate(from: text, conversationID: turn.conversationID) {
            addCandidate(explicit, rationale: "explicit_memory_request", minimumScore: 0.4)
            return candidates
        }

        for item in profileCandidates(from: text, conversationID: turn.conversationID) {
            addCandidate(item, rationale: "profile_fact")
        }

        for item in preferenceCandidates(from: text, conversationID: turn.conversationID) {
            addCandidate(item, rationale: "preference_signal")
        }

        for item in projectCandidates(from: text, conversationID: turn.conversationID) {
            addCandidate(item, rationale: "project_context")
        }

        for item in workflowCandidates(from: text, conversationID: turn.conversationID) {
            addCandidate(item, rationale: "workflow_pattern")
        }

        if let episodic = episodicCandidate(from: turn) {
            addCandidate(episodic, rationale: "episodic_milestone", minimumScore: 0.58)
        }

        return candidates
    }

    public func extractAndPersist(from turn: MemoryTurnRecord) async throws -> [MemoryItem] {
        let candidates = extractCandidates(from: turn)
        guard !candidates.isEmpty else {
            logger.debug("Memory extraction skipped", metadata: [
                "conversation_id": turn.conversationID.uuidString,
                "reason": "no_candidates"
            ])
            return []
        }

        var stored: [MemoryItem] = []

        for candidate in candidates {
            logger.info("Generated memory candidate", metadata: [
                "conversation_id": turn.conversationID.uuidString,
                "category": candidate.item.category.rawValue,
                "title": candidate.item.title,
                "score": String(format: "%.2f", candidate.score),
                "auto_save": candidate.shouldAutoSave ? "true" : "false",
                "reason": candidate.rationale
            ])

            guard candidate.shouldAutoSave else {
                logger.debug("Skipped memory candidate", metadata: [
                    "title": candidate.item.title,
                    "reason": "below_auto_save_threshold"
                ])
                continue
            }

            if let duplicate = try await memoryStore.findDuplicate(for: candidate.item) {
                let merged = merge(duplicate: duplicate, with: candidate.item)
                let saved = try await memoryStore.updateMemory(merged)
                stored.append(saved)
                logger.info("Duplicate memory avoided", metadata: [
                    "memory_id": saved.id.uuidString,
                    "title": saved.title
                ])
                continue
            }

            let saved = try await memoryStore.saveMemory(candidate.item)
            stored.append(saved)
            logger.info("Saved memory item", metadata: [
                "memory_id": saved.id.uuidString,
                "category": saved.category.rawValue,
                "title": saved.title
            ])
        }

        return stored
    }

    public func recoverDurableMemories(
        from conversations: [AtlasConversation],
        limit: Int = 200
    ) async throws -> [MemoryItem] {
        let orderedTurns = turnRecords(from: conversations)
        guard !orderedTurns.isEmpty else {
            return []
        }

        var recoveredByID: [UUID: MemoryItem] = [:]

        for turn in orderedTurns.suffix(limit) {
            let stored = try await extractAndPersist(from: turn)
            for item in stored {
                recoveredByID[item.id] = item
            }
        }

        let recovered = recoveredByID.values.sorted { lhs, rhs in
            if lhs.updatedAt == rhs.updatedAt {
                return lhs.createdAt < rhs.createdAt
            }

            return lhs.updatedAt < rhs.updatedAt
        }

        if !recovered.isEmpty {
            logger.info("Recovered durable memories from conversation history", metadata: [
                "conversation_count": "\(conversations.count)",
                "turn_count": "\(orderedTurns.count)",
                "recovered_count": "\(recovered.count)"
            ])
        }

        return recovered
    }

    public func normalizeStoredDurableMemories(limit: Int = 200) async throws -> [MemoryItem] {
        let storedMemories = try await memoryStore.listMemories(limit: limit)
        var normalizedMemories: [MemoryItem] = []

        for memory in storedMemories {
            guard let normalized = normalizedMemoryItem(from: memory), normalized != memory else {
                continue
            }

            let saved = try await memoryStore.updateMemory(normalized)
            normalizedMemories.append(saved)
        }

        if !normalizedMemories.isEmpty {
            logger.info("Normalized stored durable memories", metadata: [
                "memory_count": "\(normalizedMemories.count)"
            ])
        }

        return normalizedMemories
    }

    private func explicitMemoryCandidate(from text: String, conversationID: UUID) -> MemoryItem? {
        let patterns = [
            #"(?i)\bremember that\s+(.+)$"#,
            #"(?i)\bremember\s*:\s*(.+)$"#,
            #"(?i)\bplease remember\s+(.+)$"#
        ]

        for pattern in patterns {
            if let captured = firstCapturedGroup(in: text, pattern: pattern) {
                if let structured = structuredExplicitMemoryItem(from: captured, conversationID: conversationID) {
                    return structured
                }

                let descriptor = explicitDescriptor(for: captured)
                return buildMemoryItem(
                    category: descriptor.category,
                    title: descriptor.title,
                    content: captured,
                    source: .userExplicit,
                    confidence: 0.99,
                    importance: 0.95,
                    isUserConfirmed: true,
                    tags: descriptor.tags,
                    conversationID: conversationID
                )
            }
        }

        return nil
    }

    private func profileCandidates(from text: String, conversationID: UUID) -> [MemoryItem] {
        var items: [MemoryItem] = []
        let lowercased = text.lowercased()

        if let name = extractPreferredDisplayName(from: text) {
            items.append(
                buildMemoryItem(
                    category: .profile,
                    title: "Preferred display name",
                    content: "User prefers to be called \(name).",
                    source: .userExplicit,
                    confidence: 0.98,
                    importance: 0.9,
                    isUserConfirmed: true,
                    tags: ["identity", "name"],
                    conversationID: conversationID
                )
            )
        }

        if let location = extractPreferredLocation(from: text) {
            items.append(
                buildMemoryItem(
                    category: .profile,
                    title: "Preferred location",
                    content: "User is based in \(location).",
                    source: .userExplicit,
                    confidence: 0.97,
                    importance: 0.9,
                    isUserConfirmed: true,
                    tags: ["location", "weather"],
                    conversationID: conversationID
                )
            )
        }

        let environmentContextSignals = [
            "working on",
            "building",
            "developing",
            "using xcode",
            "use xcode",
            "macos app",
            "macos project"
        ]
        if (lowercased.contains("macos") || lowercased.contains("xcode")) &&
            environmentContextSignals.contains(where: lowercased.contains) {
            items.append(
                buildMemoryItem(
                    category: .profile,
                    title: "Primary development environment",
                    content: "User primarily works in a macOS and Xcode development environment.",
                    source: .conversationInference,
                    confidence: 0.84,
                    importance: 0.72,
                    tags: ["macos", "xcode", "development"],
                    conversationID: conversationID
                )
            )
        }

        return items
    }

    private func preferenceCandidates(from text: String, conversationID: UUID) -> [MemoryItem] {
        var items: [MemoryItem] = []
        let lowercased = text.lowercased()

        if lowercased.contains("prefer concise") || (lowercased.contains("concise") && lowercased.contains("answer")) {
            items.append(
                buildMemoryItem(
                    category: .preference,
                    title: "Response style",
                    content: "User prefers concise responses for straightforward questions.",
                    source: .conversationInference,
                    confidence: 0.9,
                    importance: 0.88,
                    tags: ["concise", "responses"],
                    conversationID: conversationID
                )
            )
        }

        if lowercased.contains("approval") && (lowercased.contains("clear") || lowercased.contains("surface")) {
            items.append(
                buildMemoryItem(
                    category: .preference,
                    title: "Approval visibility",
                    content: "User wants approval-requiring actions surfaced clearly before Atlas continues.",
                    source: .conversationInference,
                    confidence: 0.88,
                    importance: 0.9,
                    tags: ["approvals", "safety"],
                    conversationID: conversationID
                )
            )
        }

        if lowercased.contains("telegram") && (lowercased.contains("prefer") || lowercased.contains("remote")) {
            items.append(
                buildMemoryItem(
                    category: .preference,
                    title: "Preferred remote interface",
                    content: "User prefers Telegram as a remote interface for Atlas when it is available.",
                    source: .conversationInference,
                    confidence: 0.84,
                    importance: 0.78,
                    tags: ["telegram", "remote"],
                    conversationID: conversationID
                )
            )
        }

        if let atlasName = extractPreferredAtlasName(from: text) {
            items.append(
                buildMemoryItem(
                    category: .preference,
                    title: "Preferred Atlas name",
                    content: "User prefers Atlas to go by \(atlasName).",
                    source: .userExplicit,
                    confidence: 0.97,
                    importance: 0.84,
                    isUserConfirmed: true,
                    tags: ["assistant", "atlas", "name", atlasName.lowercased()],
                    conversationID: conversationID
                )
            )
        }

        if let temperatureUnit = preferredTemperatureUnit(in: lowercased) {
            items.append(
                buildMemoryItem(
                    category: .preference,
                    title: "Preferred temperature unit",
                    content: "User prefers \(temperatureUnit) for weather-related temperatures.",
                    source: .userExplicit,
                    confidence: 0.96,
                    importance: 0.89,
                    isUserConfirmed: true,
                    tags: ["weather", "temperature", "unit", temperatureUnit],
                    conversationID: conversationID
                )
            )
        }

        if lowercased.contains("prefer detailed") || lowercased.contains("more detailed") || lowercased.contains("more detail") {
            items.append(
                buildMemoryItem(
                    category: .preference,
                    title: "Response style",
                    content: "User prefers more detailed responses when nuance matters.",
                    source: .conversationInference,
                    confidence: 0.9,
                    importance: 0.84,
                    tags: ["communication", "detailed", "responses"],
                    conversationID: conversationID
                )
            )
        }

        return items
    }

    private func projectCandidates(from text: String, conversationID: UUID) -> [MemoryItem] {
        var items: [MemoryItem] = []
        let lowercased = text.lowercased()
        let workSignals = [
            "working on",
            "building",
            "implement",
            "implementing",
            "refactor",
            "refining",
            "improve",
            "stabil",
            "fix",
            "add "
        ]

        if lowercased.contains("atlas") && (lowercased.contains("project") || lowercased.contains("build") || lowercased.contains("working on")) {
            items.append(
                buildMemoryItem(
                    category: .project,
                    title: "Atlas project context",
                    content: "Atlas is an active macOS-first AI operator project under ongoing development.",
                    source: .conversationInference,
                    confidence: 0.9,
                    importance: 0.92,
                    tags: ["atlas", "project", "macos"],
                    conversationID: conversationID
                )
            )
        }

        if lowercased.contains("atlas") &&
            workSignals.contains(where: lowercased.contains) &&
            (lowercased.contains("ui") || lowercased.contains("memory") || lowercased.contains("telegram")) {
            let focusAreas = [
                lowercased.contains("ui") ? "UI refinement" : nil,
                lowercased.contains("memory") ? "memory features" : nil,
                lowercased.contains("telegram") ? "Telegram operations" : nil
            ]
                .compactMap { $0 }
                .joined(separator: ", ")

            if !focusAreas.isEmpty {
                items.append(
                    buildMemoryItem(
                        category: .project,
                        title: "Current Atlas focus",
                        content: "Current Atlas work includes \(focusAreas).",
                        source: .conversationInference,
                        confidence: 0.78,
                        importance: 0.76,
                        tags: ["atlas", "focus"],
                        conversationID: conversationID
                    )
                )
            }
        }

        return items
    }

    private func workflowCandidates(from text: String, conversationID: UUID) -> [MemoryItem] {
        var items: [MemoryItem] = []
        let lowercased = text.lowercased()

        if (lowercased.contains("codex") && lowercased.contains("xcode")) &&
            ["working on", "build", "develop", "use", "using"].contains(where: lowercased.contains) {
            items.append(
                buildMemoryItem(
                    category: .workflow,
                    title: "Development workflow",
                    content: "User uses Codex alongside Xcode for Atlas development work.",
                    source: .conversationInference,
                    confidence: 0.92,
                    importance: 0.85,
                    tags: ["codex", "xcode", "workflow"],
                    conversationID: conversationID
                )
            )
        }

        if lowercased.contains("stabil") && lowercased.contains("expansion") {
            items.append(
                buildMemoryItem(
                    category: .workflow,
                    title: "Feature sequencing preference",
                    content: "User prefers feature stabilization before expanding scope.",
                    source: .conversationInference,
                    confidence: 0.88,
                    importance: 0.86,
                    tags: ["workflow", "stability", "scope"],
                    conversationID: conversationID
                )
            )
        }

        return items
    }

    private func episodicCandidate(from turn: MemoryTurnRecord) -> MemoryItem? {
        let lowercased = turn.userMessage.content.lowercased()
        let successSignals = [
            "working perfectly",
            "works perfectly",
            "validated successfully",
            "operating as expected",
            "bridge works",
            "received on my phone",
            "working perfectly"
        ]

        guard successSignals.contains(where: { lowercased.contains($0) }) else {
            return nil
        }

        let content: String
        let tags: [String]
        if lowercased.contains("telegram") || lowercased.contains("phone") || lowercased.contains("chat") {
            content = "Telegram bridge behavior was validated successfully in a live user test."
            tags = ["telegram", "validation", "milestone"]
        } else {
            content = "A recent Atlas workflow was validated successfully during live testing."
            tags = ["validation", "milestone"]
        }

        return buildMemoryItem(
            category: .episodic,
            title: "Recent validation milestone",
            content: content,
            source: .assistantObservation,
            confidence: 0.82,
            importance: 0.62,
            tags: tags,
            conversationID: turn.conversationID
        )
    }

    private func inferCategory(for text: String) -> MemoryCategory {
        let lowercased = text.lowercased()

        if lowercased.contains("prefer") || lowercased.contains("default") ||
            lowercased.contains("fahrenheit") || lowercased.contains("celsius") ||
            lowercased.contains("concise") || lowercased.contains("detailed") {
            return .preference
        }

        if lowercased.contains("workflow") || lowercased.contains("usually") || lowercased.contains("xcode") || lowercased.contains("codex") {
            return .workflow
        }

        if lowercased.contains("project") || lowercased.contains("atlas") || lowercased.contains("building") {
            return .project
        }

        if lowercased.contains("name") || lowercased.contains("call me") ||
            lowercased.contains("live in") || lowercased.contains("based in") ||
            lowercased.contains("location") {
            return .profile
        }

        return .episodic
    }

    private func explicitDescriptor(for content: String) -> (category: MemoryCategory, title: String, tags: [String]) {
        let lowercased = content.lowercased()

        if extractPreferredDisplayName(from: content) != nil {
            return (.profile, "Preferred display name", ["identity", "name"])
        }

        if extractPreferredLocation(from: content) != nil {
            return (.profile, "Preferred location", ["location", "weather"])
        }

        if extractPreferredAtlasName(from: content) != nil {
            return (.preference, "Preferred Atlas name", ["assistant", "atlas", "name"])
        }

        if let temperatureUnit = preferredTemperatureUnit(in: lowercased) {
            return (.preference, "Preferred temperature unit", ["weather", "temperature", "unit", temperatureUnit])
        }

        if lowercased.contains("concise") {
            return (.preference, "Response style", ["communication", "concise", "responses"])
        }

        if lowercased.contains("detailed") || lowercased.contains("more detail") {
            return (.preference, "Response style", ["communication", "detailed", "responses"])
        }

        return (inferCategory(for: content), title(for: content, fallback: "User-requested memory"), tags(for: content))
    }

    private func preferredTemperatureUnit(in lowercased: String) -> String? {
        if lowercased.contains("fahrenheit") || matchesPattern(in: lowercased, pattern: #"\b(?:always\s+use|use|show|display|weather\s+in|temperature\s+in|temps?\s+in|degrees?\s+in|only\s+want\s+weather\s+in)\s*f\b"#) {
            return "fahrenheit"
        }

        if lowercased.contains("celsius") || matchesPattern(in: lowercased, pattern: #"\b(?:always\s+use|use|show|display|weather\s+in|temperature\s+in|temps?\s+in|degrees?\s+in|only\s+want\s+weather\s+in)\s*c\b"#) {
            return "celsius"
        }

        return nil
    }

    private func extractPreferredDisplayName(from text: String) -> String? {
        let patterns = [
            #"(?i)\b(?:my name is|you can call me|call me|i go by)\s+([A-Za-z][A-Za-z0-9'_-]*(?:\s+[A-Za-z][A-Za-z0-9'_-]*){0,3})"#
        ]

        for pattern in patterns {
            if let captured = firstCapturedGroup(in: text, pattern: pattern) {
                let name = sanitizeDisplayName(captured)
                if !name.isEmpty {
                    return name
                }
            }
        }

        return nil
    }

    private func extractPreferredAtlasName(from text: String) -> String? {
        let patterns = [
            #"(?i)\b(?:i want you to go by|please go by|you should go by|go by)\s+([A-Za-z][A-Za-z0-9'_-]*(?:\s+[A-Za-z][A-Za-z0-9'_-]*){0,3})"#
        ]

        for pattern in patterns {
            if let captured = firstCapturedGroup(in: text, pattern: pattern) {
                let name = sanitizeDisplayName(
                    captured.replacingOccurrences(of: #"(?i)\s+from now on$"#, with: "", options: .regularExpression)
                )
                if !name.isEmpty {
                    return name
                }
            }
        }

        return nil
    }

    private func extractPreferredLocation(from text: String) -> String? {
        let patterns = [
            #"(?i)\b(?:i\s+(?:also\s+)?live in|i(?:'m| am|m)\s+in|i(?:'m| am|m)\s+located in|i(?:'m| am|m)\s+based in|my location is)\s+([A-Za-z0-9 ,._'-]{2,80})"#
        ]

        for pattern in patterns {
            if let captured = firstCapturedGroup(in: text, pattern: pattern) {
                let location = sanitizeLocation(captured)
                if !location.isEmpty {
                    return location
                }
            }
        }

        return nil
    }

    private func structuredExplicitMemoryItem(from content: String, conversationID: UUID) -> MemoryItem? {
        if let name = extractPreferredDisplayName(from: content) {
            return buildMemoryItem(
                category: .profile,
                title: "Preferred display name",
                content: "User prefers to be called \(name).",
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.95,
                isUserConfirmed: true,
                tags: ["identity", "name"],
                conversationID: conversationID
            )
        }

        if let location = extractPreferredLocation(from: content) {
            return buildMemoryItem(
                category: .profile,
                title: "Preferred location",
                content: "User is based in \(location).",
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.95,
                isUserConfirmed: true,
                tags: ["location", "weather"],
                conversationID: conversationID
            )
        }

        if let atlasName = extractPreferredAtlasName(from: content) {
            return buildMemoryItem(
                category: .preference,
                title: "Preferred Atlas name",
                content: "User prefers Atlas to go by \(atlasName).",
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.92,
                isUserConfirmed: true,
                tags: ["assistant", "atlas", "name", atlasName.lowercased()],
                conversationID: conversationID
            )
        }

        if let temperatureUnit = preferredTemperatureUnit(in: content.lowercased()) {
            return buildMemoryItem(
                category: .preference,
                title: "Preferred temperature unit",
                content: "User prefers \(temperatureUnit) for weather-related temperatures.",
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.95,
                isUserConfirmed: true,
                tags: ["weather", "temperature", "unit", temperatureUnit],
                conversationID: conversationID
            )
        }

        return nil
    }

    private func sanitizeDisplayName(_ raw: String) -> String {
        let fillerWords = Set(["btw", "please", "thanks", "thank", "lol", "haha"])
        let cleaned = raw
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: ".,!?"))

        let tokens = cleaned.split(separator: " ").map(String.init)
        var sanitizedTokens = Array(tokens.drop(while: { $0.isEmpty }))
        while let last = sanitizedTokens.last, fillerWords.contains(last.lowercased()) {
            sanitizedTokens.removeLast()
        }

        return sanitizedTokens.joined(separator: " ")
    }

    private func sanitizeLocation(_ raw: String) -> String {
        let cleaned = raw
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .replacingOccurrences(of: #"(?i)^(?:also\s+)"#, with: "", options: .regularExpression)
            .trimmingCharacters(in: CharacterSet(charactersIn: ".,!?"))

        let tokens = cleaned.split(separator: " ").map(String.init)
        let normalizedTokens = tokens.map { token in
            let letters = token.filter(\.isLetter)
            guard !letters.isEmpty else {
                return token
            }

            if letters.count <= 3, letters == letters.lowercased() {
                return token.uppercased()
            }

            if token == token.lowercased() {
                return token.localizedCapitalized
            }

            return token
        }

        return normalizedTokens.joined(separator: " ")
    }

    private func buildMemoryItem(
        category: MemoryCategory,
        title: String,
        content: String,
        source: MemorySource,
        confidence: Double,
        importance: Double,
        isUserConfirmed: Bool = false,
        isSensitive: Bool = false,
        tags: [String] = [],
        conversationID: UUID
    ) -> MemoryItem {
        MemoryItem(
            category: category,
            title: title,
            content: content,
            source: source,
            confidence: confidence,
            importance: importance,
            isUserConfirmed: isUserConfirmed,
            isSensitive: isSensitive,
            tags: tags,
            relatedConversationID: conversationID
        )
    }

    private func merge(duplicate: MemoryItem, with candidate: MemoryItem) -> MemoryItem {
        let preferredContent: String
        if candidate.isUserConfirmed && candidate.confidence >= duplicate.confidence {
            preferredContent = candidate.content
        } else {
            preferredContent = duplicate.content.count >= candidate.content.count ? duplicate.content : candidate.content
        }

        return duplicate.updating(
            title: duplicate.title.count >= candidate.title.count ? duplicate.title : candidate.title,
            content: preferredContent,
            confidence: max(duplicate.confidence, candidate.confidence),
            importance: max(duplicate.importance, candidate.importance),
            updatedAt: .now,
            isUserConfirmed: duplicate.isUserConfirmed || candidate.isUserConfirmed,
            isSensitive: duplicate.isSensitive || candidate.isSensitive,
            tags: Array(Set(duplicate.tags + candidate.tags)).sorted()
        )
    }

    private func title(for content: String, fallback: String) -> String {
        let trimmed = content.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return fallback
        }

        let words = trimmed.split(separator: " ").prefix(6).map(String.init)
        let title = words.joined(separator: " ")
        return title.count > 48 ? String(title.prefix(48)) : title
    }

    private func tags(for content: String) -> [String] {
        let keywords = [
            "atlas", "telegram", "openai", "xcode", "codex", "runtime", "approvals",
            "memory", "ui", "tools", "location", "weather", "forecast", "temperature",
            "fahrenheit", "celsius", "communication", "concise", "detailed", "name"
        ]
        let lowercased = content.lowercased()
        return keywords.filter(lowercased.contains)
    }

    private func firstCapturedGroup(in text: String, pattern: String) -> String? {
        guard let regex = try? NSRegularExpression(pattern: pattern) else {
            return nil
        }

        let range = NSRange(text.startIndex..<text.endIndex, in: text)
        guard
            let match = regex.firstMatch(in: text, options: [], range: range),
            match.numberOfRanges > 1,
            let captureRange = Range(match.range(at: 1), in: text)
        else {
            return nil
        }

        return String(text[captureRange]).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func matchesPattern(in text: String, pattern: String) -> Bool {
        guard let regex = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive]) else {
            return false
        }

        let range = NSRange(text.startIndex..<text.endIndex, in: text)
        return regex.firstMatch(in: text, options: [], range: range) != nil
    }

    private func turnRecords(from conversations: [AtlasConversation]) -> [MemoryTurnRecord] {
        var turns: [MemoryTurnRecord] = []

        for conversation in conversations.sorted(by: { $0.updatedAt < $1.updatedAt }) {
            let messages = conversation.messages.sorted(by: { $0.timestamp < $1.timestamp })

            for (index, message) in messages.enumerated() where message.role == .user {
                guard let assistantMessage = firstAssistantReply(in: messages, afterIndex: index) else {
                    continue
                }

                turns.append(
                    MemoryTurnRecord(
                        conversationID: conversation.id,
                        userMessage: message,
                        assistantMessage: assistantMessage,
                        toolCalls: [],
                        toolResults: [],
                        responseStatus: .completed
                    )
                )
            }
        }

        return turns
    }

    private func firstAssistantReply(in messages: [AtlasMessage], afterIndex index: Int) -> AtlasMessage? {
        guard index >= messages.startIndex, index + 1 < messages.endIndex else {
            return nil
        }

        for candidate in messages[(index + 1)...] {
            if candidate.role == .assistant {
                return candidate
            }

            if candidate.role == .user {
                return nil
            }
        }

        return nil
    }

    private func normalize(_ text: String) -> String {
        text
            .lowercased()
            .replacingOccurrences(of: "[^a-z0-9\\s]", with: " ", options: .regularExpression)
            .replacingOccurrences(of: "\\s+", with: " ", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func normalizedMemoryItem(from memory: MemoryItem) -> MemoryItem? {
        switch memory.title {
        case "Preferred display name":
            guard
                let rawName = firstCapturedGroup(
                    in: memory.content,
                    pattern: #"(?i)\b(?:prefers to be called|call(?:ed)?|name is)\s+([A-Za-z][A-Za-z0-9'_-]*(?:\s+[A-Za-z][A-Za-z0-9'_-]*){0,3})"#
                )
            else {
                return nil
            }

            let name = sanitizeDisplayName(rawName)
            let normalizedContent = "User prefers to be called \(name)."
            guard normalizedContent != memory.content else {
                return nil
            }

            return memory.updating(content: normalizedContent, updatedAt: .now)

        case "Preferred Atlas name":
            guard
                let rawName = firstCapturedGroup(
                    in: memory.content,
                    pattern: #"(?i)\b(?:atlas to go by|go by)\s+([A-Za-z][A-Za-z0-9'_-]*(?:\s+[A-Za-z][A-Za-z0-9'_-]*){0,3})"#
                )
            else {
                return nil
            }

            let name = sanitizeDisplayName(rawName)
            let normalizedContent = "User prefers Atlas to go by \(name)."
            guard normalizedContent != memory.content else {
                return nil
            }

            return memory.updating(content: normalizedContent, updatedAt: .now)

        case "Preferred location":
            guard
                let rawLocation = firstCapturedGroup(
                    in: memory.content,
                    pattern: #"(?i)\b(?:based in|live in|located in)\s+([A-Za-z0-9 ,._'-]{2,80})"#
                )
            else {
                return nil
            }

            let location = sanitizeLocation(rawLocation)
            let normalizedContent = "User is based in \(location)."
            guard normalizedContent != memory.content else {
                return nil
            }

            return memory.updating(content: normalizedContent, updatedAt: .now)

        case "Preferred temperature unit":
            guard let temperatureUnit = preferredTemperatureUnit(in: memory.content.lowercased()) else {
                return nil
            }

            let normalizedContent = "User prefers \(temperatureUnit) for weather-related temperatures."
            guard normalizedContent != memory.content else {
                return nil
            }

            return memory.updating(content: normalizedContent, updatedAt: .now)

        default:
            return nil
        }
    }
}
