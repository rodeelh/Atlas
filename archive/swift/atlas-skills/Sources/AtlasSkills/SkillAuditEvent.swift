import Foundation
import AtlasShared

public enum SkillAuditOutcome: String, Codable, CaseIterable, Hashable, Sendable {
    case requested
    case executed
    case denied
    case failed
    case enabled
    case disabled
    case validated
}

public struct SkillAuditEvent: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let skillID: String
    public let actionID: String?
    public let actionName: String?
    public let conversationID: UUID?
    public let timestamp: Date
    public let approvalRequired: Bool
    public let approvalResult: ApprovalStatus?
    public let outcome: SkillAuditOutcome
    public let inputSummary: String?
    public let outputSummary: String?
    public let errorMessage: String?

    public init(
        id: UUID = UUID(),
        skillID: String,
        actionID: String? = nil,
        actionName: String? = nil,
        conversationID: UUID? = nil,
        timestamp: Date = .now,
        approvalRequired: Bool,
        approvalResult: ApprovalStatus? = nil,
        outcome: SkillAuditOutcome,
        inputSummary: String? = nil,
        outputSummary: String? = nil,
        errorMessage: String? = nil
    ) {
        self.id = id
        self.skillID = skillID
        self.actionID = actionID
        self.actionName = actionName
        self.conversationID = conversationID
        self.timestamp = timestamp
        self.approvalRequired = approvalRequired
        self.approvalResult = approvalResult
        self.outcome = outcome
        self.inputSummary = inputSummary
        self.outputSummary = outputSummary
        self.errorMessage = errorMessage
    }
}
