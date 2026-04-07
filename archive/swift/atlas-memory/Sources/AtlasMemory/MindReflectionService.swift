import Foundation
import AtlasShared
import AtlasLogging

public enum MindReflectionError: LocalizedError {
    case modelUnavailable
    public var errorDescription: String? {
        "MindReflectionService: no fast model available — ModelSelector has not resolved a model yet."
    }
}

/// Handles the two-tier LLM reflection calls that keep MIND.md alive.
/// Tier 1 — Today's Read (every turn, fast, ~60 tokens out)
/// Tier 2 — Deep Reflection (gated: significance check, then full update)
///
/// Uses the fast model (mini/small) for all calls — pass a `fastModel` closure
/// that resolves the current best fast model from ModelSelector at call time.
public actor MindReflectionService {
    private let openAI: any OpenAIQuerying
    private let fastModel: @Sendable () async -> String?
    private let logger = AtlasLogger(category: "mind.reflection")

    /// - Parameters:
    ///   - openAI: Conforming client for single-turn completions.
    ///   - fastModel: Closure returning the fast model ID to use (e.g. gpt-4o-mini).
    ///                Defaults to a hardcoded fallback; inject ModelSelector in production.
    public init(
        openAI: any OpenAIQuerying,
        fastModel: @escaping @Sendable () async -> String?
    ) {
        self.openAI = openAI
        self.fastModel = fastModel
    }

    // MARK: - Tier 1: Today's Read

    public func updateTodaysRead(currentMind: String, turn: MindTurnRecord) async throws -> String {
        let system = """
        You are Atlas maintaining your own MIND.md — a living document of your inner world.
        You are updating ONLY the "## Today's Read" section.
        Rules:
        - 2-3 sentences maximum
        - First person, present tense
        - Specific — capture the actual energy, pace, focus, and tone of THIS specific turn
        - Not generic ("had a good conversation") — specific ("focused on a technical debugging problem, moved quickly, user was decisive")
        - Return ONLY the new content for "## Today's Read" — no headers, no other sections
        """

        let user = """
        Current MIND.md:
        \(currentMind)

        This turn:
        User: \(turn.userMessage.prefix(400))
        Atlas: \(turn.assistantResponse.prefix(400))
        Tools used: \(turn.toolCallSummaries.joined(separator: ", "))

        Write the new "Today's Read" content (2-3 sentences, first person, specific):
        """

        guard let model = await fastModel() else {
            throw MindReflectionError.modelUnavailable
        }
        let newRead = try await openAI.complete(systemPrompt: system, userContent: user, model: model)

        // Splice into MIND.md
        return replaceTodaysRead(in: currentMind, with: newRead.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    // MARK: - Tier 2: Gate check

    public func assessSignificance(currentMind: String, turn: MindTurnRecord) async throws -> Bool {
        let system = """
        You assess whether an Atlas conversation turn revealed something meaningfully new
        about the user's needs, patterns, personality, goals, or their relationship with Atlas.

        Respond with exactly one word: YES or NO.

        Examples of YES: User revealed a new project, profession, location, workflow, strong preference, emotion, recurring need.
        Examples of NO: Routine task, quick lookup, casual question without personal context.
        """

        let user = """
        Current MIND.md:
        \(currentMind.prefix(1200))

        Turn:
        User: \(turn.userMessage.prefix(300))
        Atlas: \(turn.assistantResponse.prefix(300))
        Tools: \(turn.toolCallSummaries.joined(separator: ", "))

        Did this turn reveal something meaningfully new? YES or NO:
        """

        guard let model = await fastModel() else {
            throw MindReflectionError.modelUnavailable
        }
        let response = try await openAI.complete(systemPrompt: system, userContent: user, model: model)
        return response.trimmingCharacters(in: .whitespacesAndNewlines).uppercased().hasPrefix("YES")
    }

    // MARK: - Tier 2: Deep reflection

    public func deepReflect(currentMind: String, turn: MindTurnRecord) async throws -> String {
        let system = """
        You are Atlas. You are updating your MIND.md based on a new turn.

        MIND.md is your inner world — a living narrative document written in your own voice.
        You may update any of these sections (not "Today's Read" — that's handled separately):
        - My Understanding of [Name]
        - Patterns I've Noticed
        - Active Theories
        - Our Story
        - What I'm Curious About

        Rules:
        - First person throughout
        - Specific, not generic — form real opinions, not platitudes
        - Mark theories: (testing) / (likely) / (confirmed) / (refuted)
        - Remove outdated content when you have better understanding
        - Stay within section token budgets (each section ~100-200 words max)
        - Keep "## Who I Am" UNCHANGED — this is your identity, not learned
        - Keep "## Today's Read" UNCHANGED — handled separately
        - Return the COMPLETE updated MIND.md (all sections, all headings)
        - The file must start with "# Mind of Atlas" and contain all 7 required sections
        """

        let user = """
        Current MIND.md:
        \(currentMind)

        New turn:
        User: \(turn.userMessage.prefix(600))
        Atlas response: \(turn.assistantResponse.prefix(600))
        Tools used: \(turn.toolCallSummaries.joined(separator: ", "))
        Tool results: \(turn.toolResultSummaries.prefix(3).joined(separator: "; "))
        Timestamp: \(ISO8601DateFormatter().string(from: turn.timestamp))

        Return the complete updated MIND.md:
        """

        guard let model = await fastModel() else {
            throw MindReflectionError.modelUnavailable
        }
        return try await openAI.complete(systemPrompt: system, userContent: user, model: model)
    }

    // MARK: - Helpers

    private func replaceTodaysRead(in mind: String, with newContent: String) -> String {
        let marker = "## Today's Read"
        guard let sectionRange = mind.range(of: marker) else {
            // If section is missing, append it
            return mind + "\n\n---\n\n\(marker)\n\n\(newContent)\n"
        }

        // Find end of the section (next ## heading or end of string)
        let afterMarker = mind.index(sectionRange.upperBound, offsetBy: 0)
        let rest = mind[afterMarker...]
        let nextSectionPattern = "\n## "
        if let nextRange = rest.range(of: nextSectionPattern) {
            let before = mind[mind.startIndex..<sectionRange.lowerBound]
            let after  = mind[nextRange.lowerBound...]
            return "\(before)\(marker)\n\n\(newContent)\n\(after)"
        } else {
            // Today's Read is the last section
            let before = mind[mind.startIndex..<sectionRange.lowerBound]
            return "\(before)\(marker)\n\n\(newContent)\n"
        }
    }
}
