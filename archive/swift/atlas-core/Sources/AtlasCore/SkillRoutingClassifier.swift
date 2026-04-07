import Foundation
import AtlasSkills

// MARK: - Classification result

public struct SkillClassification: Sendable {
    public let intent: SkillIntent
    public let queryType: SkillQueryType?
    public let confidence: Double
    public let explanation: String
    /// The skill ID whose trigger phrase matched, if any.
    public let matchedSkillID: String?
}

// MARK: - Protocol

public protocol SkillRoutingClassifying: Sendable {
    /// Classify a user message against the registered skill manifests.
    /// The classifier picks the *longest* trigger phrase that matches, breaking
    /// ties by descending `routingPriority`, so more-specific phrases always win.
    func classify(message: String, skills: [AtlasSkillRecord]) -> SkillClassification
    /// Returns true when the message is likely a compound request that would benefit
    /// from parallel multi-agent decomposition. Conservative by design — false negatives
    /// are fine; false positives waste tokens.
    func isCompoundRequest(message: String) -> Bool
}

// MARK: - Implementation

public struct SkillRoutingClassifier: SkillRoutingClassifying, Sendable {
    public init() {}

    public func classify(message: String, skills: [AtlasSkillRecord]) -> SkillClassification {
        let normalized = message
            .lowercased()
            .replacingOccurrences(of: "\n", with: " ")
            .replacingOccurrences(of: "\t", with: " ")
            .trimmingCharacters(in: .whitespaces)

        guard !normalized.isEmpty else {
            return SkillClassification(
                intent: .unknown,
                queryType: nil,
                confidence: 0,
                explanation: "Empty message.",
                matchedSkillID: nil
            )
        }

        // Tier 1: Detect pure conversational messages (greetings / acks) before trigger
        // matching. Only exact or very short phrases qualify — keeps false positives
        // near-zero. Anything ambiguous falls through to trigger matching (Tier 2/3).
        if isConversational(normalized) {
            return SkillClassification(
                intent: .conversational,
                queryType: nil,
                confidence: 0.95,
                explanation: "Classified as conversational — no tools required.",
                matchedSkillID: nil
            )
        }

        // Collect every trigger that fires across all enabled skills.
        struct Match {
            let skillID: String
            let intent: SkillIntent
            let queryType: SkillQueryType?
            let routingPriority: Int
            let phraseLength: Int
        }

        var matches: [Match] = []

        for record in skills {
            let manifest = record.manifest
            for trigger in manifest.triggers {
                if normalized.contains(trigger.phrase) {
                    matches.append(Match(
                        skillID: manifest.id,
                        intent: manifest.intent,
                        queryType: trigger.queryType ?? manifest.preferredQueryTypes.first,
                        routingPriority: manifest.routingPriority,
                        phraseLength: trigger.phrase.count
                    ))
                }
            }
        }

        // Pick the best match: longest phrase wins, then highest routingPriority.
        let best = matches.max {
            if $0.phraseLength != $1.phraseLength { return $0.phraseLength < $1.phraseLength }
            return $0.routingPriority < $1.routingPriority
        }

        if let best {
            // If multiple distinct skills matched, the request is ambiguous — lower
            // confidence so the caller falls back to the full tool list (Tier 3).
            let matchedSkillIDs = Set(matches.map { $0.skillID })
            let isAmbiguous = matchedSkillIDs.count > 1

            return SkillClassification(
                intent: best.intent,
                queryType: best.queryType,
                confidence: isAmbiguous ? 0.6 : 0.9,
                explanation: isAmbiguous
                    ? "Multiple skills matched (\(matchedSkillIDs.count)) — routing to full tool list."
                    : "Matched skill '\(best.skillID)' via trigger phrase (\(best.phraseLength) chars).",
                matchedSkillID: isAmbiguous ? nil : best.skillID
            )
        }

        // No trigger matched — fall back to general reasoning.
        return SkillClassification(
            intent: .generalReasoning,
            queryType: nil,
            confidence: 0.35,
            explanation: "No trigger phrase matched any enabled skill.",
            matchedSkillID: nil
        )
    }

    // MARK: - Compound request detection

    public func isCompoundRequest(message: String) -> Bool {
        let normalized = message
            .lowercased()
            .replacingOccurrences(of: "\n", with: " ")
            .trimmingCharacters(in: .whitespaces)

        // Never treat short messages as compound — they’re almost never multi-task
        guard normalized.count > 60 else { return false }

        // Explicit multi-task conjunctions
        let conjunctionPhrases = [
            "and also", "as well as", "additionally", "at the same time",
            "simultaneously", "in addition", "on top of that", "plus also",
            "while also", "and separately", "and independently"
        ]
        for phrase in conjunctionPhrases where normalized.contains(phrase) {
            return true
        }

        // Multiple distinct question marks (two separate questions)
        let questionMarkCount = normalized.filter { $0 == "?" }.count
        if questionMarkCount >= 2 { return true }

        // Long message with multiple imperative verbs at sentence starts
        // — only for substantial requests to avoid false positives
        if normalized.count > 300 {
            let imperatives = ["search", "find", "get", "fetch", "check", "look up",
                               "create", "make", "generate", "write", "build",
                               "analyze", "summarize", "compare", "calculate"]
            let wordBoundaryPhrases = imperatives.filter { imp in
                // Must appear at least twice, or appear after a period/comma
                let pattern = normalized.ranges(of: imp)
                return pattern.count >= 2
            }
            if wordBoundaryPhrases.count >= 2 { return true }
        }

        return false
    }

    // MARK: - Conversational detection

    /// Returns true only for unambiguous greetings and acknowledgements — messages
    /// where we are highly confident no tool calls are needed. The bar is intentionally
    /// high to prevent false positives.
    private func isConversational(_ normalized: String) -> Bool {
        // Strip trailing punctuation for matching
        let stripped = normalized.trimmingCharacters(in: CharacterSet(charactersIn: "!.,?"))

        let exactGreetings: Set<String> = [
            "hi", "hello", "hey", "howdy", "greetings",
            "good morning", "good afternoon", "good evening", "good night"
        ]
        let exactAcks: Set<String> = [
            "thanks", "thank you", "ok", "okay", "k",
            "sounds good", "got it", "perfect", "great", "awesome",
            "cool", "understood", "noted", "sure", "absolutely",
            "of course", "no problem", "np", "nice", "good"
        ]

        // Exact matches (with or without punctuation)
        if exactGreetings.contains(stripped) || exactAcks.contains(stripped) { return true }
        if exactGreetings.contains(normalized) || exactAcks.contains(normalized) { return true }

        // Short greeting with an appended name/word: "hi atlas", "hey there", "hello there"
        let words = stripped.split(separator: " ")
        if words.count <= 3, let firstWord = words.first {
            let first = String(firstWord)
            if exactGreetings.contains(first) { return true }
        }

        return false
    }
}
