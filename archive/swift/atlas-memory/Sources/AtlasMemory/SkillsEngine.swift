import Foundation
import AtlasShared
import AtlasLogging

/// Manages SKILLS.md — a living markdown file that teaches Atlas how to use skills
/// for this specific user. Provides selective injection (~150 tokens max per turn)
/// and learns new routines after repeated usage patterns.
public actor SkillsEngine {
    private let skillsFilePath: String
    private let openAI: any OpenAIQuerying
    private var cachedContent: String = ""
    private var cachedRoutines: [LearnedRoutine] = []
    // In-memory occurrence counter for learning (resets on restart — conservative by design)
    private var sequenceCounts: [String: Int] = [:]
    private let logger = AtlasLogger(category: "skills.engine")

    public init(skillsFilePath: String, openAI: any OpenAIQuerying) {
        self.skillsFilePath = skillsFilePath
        self.openAI = openAI
    }

    // MARK: - Load / Save

    public func load() async throws -> String {
        let url = URL(fileURLWithPath: skillsFilePath)
        if FileManager.default.fileExists(atPath: skillsFilePath) {
            cachedContent = try String(contentsOf: url, encoding: .utf8)
        } else {
            // Create skeleton on first run
            cachedContent = seedContent()
            try save(cachedContent)
        }
        cachedRoutines = parseRoutines(from: cachedContent)
        logger.info("SKILLS.md loaded", metadata: ["routines": "\(cachedRoutines.count)"])
        return cachedContent
    }

    public func save(_ content: String) throws {
        let url = URL(fileURLWithPath: skillsFilePath)
        let dir = url.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try content.write(to: url, atomically: true, encoding: .utf8)
    }

    public func currentContent() -> String { cachedContent }

    public func updateContent(_ content: String) throws {
        cachedContent = content
        cachedRoutines = parseRoutines(from: content)
        try save(content)
    }

    // MARK: - Selective injection

    /// Returns a short routine block if the user message matches a learned routine, else nil.
    /// Maximum ~150 tokens. No API call — pure string matching.
    public func selectiveBlock(for userMessage: String) -> String? {
        guard let routine = matchRoutine(for: userMessage) else { return nil }

        let stepsText = routine.steps.prefix(4).enumerated().map { i, step in
            "\(i + 1). \(step)"
        }.joined(separator: "\n")

        let block = """
        Learned routine for this request:
        ### \(routine.name)
        **Triggers:** \(routine.triggers.joined(separator: ", "))
        **Steps:**
        \(stepsText)
        Follow these steps in order.
        """

        // Rough token cap: ~150 tokens ≈ 600 chars
        let capped = String(block.prefix(600))
        return capped.isEmpty ? nil : capped
    }

    // MARK: - Automatic learning

    /// Called from AgentLoop.finalizeTurn() — detects learnable multi-skill sequences.
    public func reflectAfterTurn(_ turn: MindTurnRecord) async {
        let skillIDs = extractSkillIDs(from: turn.toolCallSummaries)
        guard skillIDs.count >= 2 else { return }

        // Explicit instruction detection
        let lower = turn.userMessage.lowercased()
        let explicitPhrases = ["next time i ask", "whenever i say", "always do", "when i ask"]
        if explicitPhrases.contains(where: { lower.contains($0) }) {
            await learnFromInstruction(turn: turn, skillIDs: skillIDs)
            return
        }

        // Repeated-sequence learning
        let sequenceKey = skillIDs.joined(separator: "→")
        let count = (sequenceCounts[sequenceKey] ?? 0) + 1
        sequenceCounts[sequenceKey] = count

        if count >= 3 {
            sequenceCounts[sequenceKey] = 0 // reset counter
            await writeRoutineForSequence(turn: turn, skillIDs: skillIDs)
        }
    }

    // MARK: - Private: Learning

    private func learnFromInstruction(turn: MindTurnRecord, skillIDs: [String]) async {
        do {
            let newContent = try await callOpenAIToWriteRoutine(
                currentSkills: cachedContent,
                userMessage: turn.userMessage,
                skillSequence: skillIDs,
                reason: "explicit user instruction"
            )
            try updateContent(newContent)
            logger.info("SKILLS.md updated from explicit instruction")
        } catch {
            logger.warning("SKILLS.md explicit instruction learning failed", metadata: ["error": error.localizedDescription])
        }
    }

    private func writeRoutineForSequence(turn: MindTurnRecord, skillIDs: [String]) async {
        do {
            let newContent = try await callOpenAIToWriteRoutine(
                currentSkills: cachedContent,
                userMessage: turn.userMessage,
                skillSequence: skillIDs,
                reason: "repeated sequence (3+ times)"
            )
            try updateContent(newContent)
            logger.info("SKILLS.md updated from repeated sequence", metadata: ["sequence": skillIDs.joined(separator: "→")])
        } catch {
            logger.warning("SKILLS.md sequence learning failed", metadata: ["error": error.localizedDescription])
        }
    }

    private func callOpenAIToWriteRoutine(
        currentSkills: String,
        userMessage: String,
        skillSequence: [String],
        reason: String
    ) async throws -> String {
        let system = """
        You are Atlas updating your SKILLS.md — a living document of learned skill orchestration patterns.

        You are adding or updating a "Learned Routine" entry based on a pattern you've observed.

        Rules:
        - Add the new routine to the "## Learned Routines" section
        - Format exactly:
          ### [Routine Name]
          **Triggers:** [comma-separated trigger phrases]
          **Steps:**
          1. [SkillName] → [action] ([parameter])
          2. ...
          **Learned:** [today's date] — [brief note]
        - Do not duplicate an existing routine
        - Keep routines concise (max 5 steps)
        - Return the COMPLETE updated SKILLS.md
        """

        let user = """
        Current SKILLS.md:
        \(currentSkills)

        Reason to add routine: \(reason)
        User message that triggered this: \(userMessage.prefix(300))
        Skill sequence used: \(skillSequence.joined(separator: " → "))
        Today's date: \(ISO8601DateFormatter().string(from: .now).prefix(10))

        Return the updated SKILLS.md with the new routine added:
        """

        return try await openAI.complete(systemPrompt: system, userContent: user)
    }

    // MARK: - Private: Parsing

    private func parseRoutines(from content: String) -> [LearnedRoutine] {
        var routines: [LearnedRoutine] = []
        let sections = content.components(separatedBy: "\n### ")
        for section in sections.dropFirst() {
            let lines = section.components(separatedBy: "\n")
            guard let nameLine = lines.first else { continue }
            let name = nameLine.trimmingCharacters(in: .whitespacesAndNewlines)

            var triggers: [String] = []
            var steps: [String] = []

            for line in lines {
                if line.hasPrefix("**Triggers:**") {
                    let raw = line.replacingOccurrences(of: "**Triggers:**", with: "").trimmingCharacters(in: .whitespaces)
                    triggers = raw.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }
                }
                if let match = line.range(of: #"^\d+\. "#, options: .regularExpression) {
                    steps.append(String(line[match.upperBound...]).trimmingCharacters(in: .whitespaces))
                }
            }

            if !triggers.isEmpty || !steps.isEmpty {
                routines.append(LearnedRoutine(
                    name: name,
                    triggers: triggers,
                    steps: steps,
                    learnedAt: .now,
                    confirmedCount: 1
                ))
            }
        }
        return routines
    }

    private func matchRoutine(for message: String) -> LearnedRoutine? {
        let lower = message.lowercased()
        return cachedRoutines.first { routine in
            routine.triggers.contains { trigger in
                lower.contains(trigger.lowercased())
            }
        }
    }

    private func extractSkillIDs(from toolCallSummaries: [String]) -> [String] {
        var seen: [String] = []
        var seenSet = Set<String>()
        for summary in toolCallSummaries {
            // Tool summaries are like "WeatherSkill.get_weather"
            let skillID = summary.split(separator: ".").first.map(String.init) ?? summary
            if seenSet.insert(skillID).inserted {
                seen.append(skillID)
            }
        }
        return seen
    }

    // MARK: - First-run seed

    private func seedContent() -> String {
        let today = ISO8601DateFormatter().string(from: .now).prefix(10)
        return """
        # Skill Memory

        _Last updated: \(today)_

        ---

        ## Orchestration Principles

        Always complete the user's request using the most relevant skill first, then synthesise the result.

        ---

        ## Learned Routines

        _(None yet — Atlas learns routines from repeated multi-skill workflows.)_

        ---

        ## Things That Don't Work

        _(None recorded yet.)_
        """
    }
}
