import Foundation
import AtlasLogging
import AtlasSkills
import AtlasShared

public struct PersonaSessionContext: Sendable {
    public let conversationID: UUID
    public let latestUserInput: String
    public let messageCount: Int
    public let platformContext: String?

    public init(
        conversationID: UUID,
        latestUserInput: String,
        messageCount: Int,
        platformContext: String? = nil
    ) {
        self.conversationID = conversationID
        self.latestUserInput = latestUserInput
        self.messageCount = messageCount
        self.platformContext = platformContext
    }
}

public protocol PersonaPromptAssembling: Sendable {
    func assemblePrompt(
        mindContent: String,
        sessionContext: PersonaSessionContext,
        routingDecision: SkillRoutingDecision?,
        enabledSkills: [SkillManifest],
        skillsBlock: String?
    ) -> String
}

public struct PersonaPromptAssembler: PersonaPromptAssembling, Sendable {
    private let logger: AtlasLogger

    public init(logger: AtlasLogger = AtlasLogger(category: "persona")) {
        self.logger = logger
    }

    public func assemblePrompt(
        mindContent: String,
        sessionContext: PersonaSessionContext,
        routingDecision: SkillRoutingDecision? = nil,
        enabledSkills: [SkillManifest] = [],
        skillsBlock: String? = nil
    ) -> String {
        let sections = [
            mindContent.trimmingCharacters(in: .whitespacesAndNewlines),
            sessionContext.platformContext ?? "",
            buildSkillsBlock(for: enabledSkills),
            buildRoutingBlock(for: routingDecision, skillsBlock: skillsBlock),
            buildSessionBlock(for: sessionContext)
        ]
            .compactMap { block -> String? in
                let trimmed = block.trimmingCharacters(in: .whitespacesAndNewlines)
                return trimmed.isEmpty ? nil : trimmed
            }

        logger.debug("Assembled persona prompt (MIND.md)", metadata: [
            "conversation_id": sessionContext.conversationID.uuidString,
            "message_count": "\(sessionContext.messageCount)",
            "routing_intent": routingDecision?.intent.rawValue ?? "none",
            "enabled_skills": "\(enabledSkills.count)",
            "has_skills_block": skillsBlock != nil ? "true" : "false"
        ])

        return sections.joined(separator: "\n\n")
    }

    private func buildSkillsBlock(for skills: [SkillManifest]) -> String {
        let visible = skills.filter { $0.isUserVisible && $0.lifecycleState == .enabled }
        guard !visible.isEmpty else { return "" }

        let lines = visible.map { "- \($0.name): \($0.description)" }
        return """
        Active skills (tools you can actively invoke):
        \(lines.joined(separator: "\n"))
        Use the appropriate skill tool whenever the user's request maps to one of the above. Do not describe what you are about to do — just call the tool.
        """
    }

    private func buildSessionBlock(for sessionContext: PersonaSessionContext) -> String {
        """
        Current session context:
        - Prior message count: \(sessionContext.messageCount)
        - Respond to the user's latest request directly.
        - Offer at most one or two optional next steps when they would materially help.
        """
    }

    private func buildRoutingBlock(for decision: SkillRoutingDecision?, skillsBlock: String?) -> String {
        var lines: [String] = []

        if let decision, decision.hasGuidance {
            lines.append("Routing guidance:")
            lines.append("- Intent: \(displayName(decision.intent)).")

            if let queryType = decision.queryType {
                lines.append("- Query type: \(displayName(queryType)).")
            }
            for hint in decision.routingHints.prefix(2) {
                lines.append("- \(hint.text)")
            }
        }

        if let block = skillsBlock, !block.isEmpty {
            if !lines.isEmpty { lines.append("") }
            lines.append(block)
        }

        return lines.joined(separator: "\n")
    }

    private func displayName<T: RawRepresentable>(_ value: T) -> String where T.RawValue == String {
        value.rawValue
            .replacingOccurrences(of: "_", with: " ")
    }
}
