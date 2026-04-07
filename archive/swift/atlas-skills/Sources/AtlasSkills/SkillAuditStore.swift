import Foundation
import AtlasLogging

public actor SkillAuditStore {
    private let logger: AtlasLogger
    private var events: [SkillAuditEvent]

    public init(
        logger: AtlasLogger = AtlasLogger(category: "skills"),
        initialEvents: [SkillAuditEvent] = []
    ) {
        self.logger = logger
        self.events = initialEvents
    }

    public func record(_ event: SkillAuditEvent) {
        events.append(event)

        if events.count > 300 {
            events.removeFirst(events.count - 300)
        }

        var metadata: [String: String] = [
            "skill_id": event.skillID,
            "outcome": event.outcome.rawValue
        ]

        if let actionID = event.actionID {
            metadata["action_id"] = actionID
        }

        if let actionName = event.actionName {
            metadata["action_name"] = actionName
        }

        if let conversationID = event.conversationID {
            metadata["conversation_id"] = conversationID.uuidString
        }

        if event.approvalRequired {
            metadata["approval_required"] = "true"
        }

        if let approvalResult = event.approvalResult {
            metadata["approval_result"] = approvalResult.rawValue
        }

        if let errorMessage = event.errorMessage {
            metadata["error"] = errorMessage
        }

        logger.info("Skill audit event", metadata: metadata)
    }

    public func recentEvents(limit: Int = 100) -> [SkillAuditEvent] {
        Array(events.suffix(limit))
    }
}
