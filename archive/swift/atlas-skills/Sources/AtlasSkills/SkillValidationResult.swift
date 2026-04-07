import Foundation

public enum SkillValidationStatus: String, Codable, CaseIterable, Hashable, Sendable {
    case notValidated = "not_validated"
    case passed
    case warning
    case failed
}

public struct SkillValidationResult: Codable, Hashable, Sendable {
    public let skillID: String
    public let status: SkillValidationStatus
    public let summary: String
    public let issues: [String]
    public let validatedAt: Date

    public init(
        skillID: String,
        status: SkillValidationStatus,
        summary: String,
        issues: [String] = [],
        validatedAt: Date = .now
    ) {
        self.skillID = skillID
        self.status = status
        self.summary = summary
        self.issues = issues
        self.validatedAt = validatedAt
    }

    public var isValid: Bool {
        status == .passed || status == .warning
    }

    public static func notValidated(skillID: String) -> SkillValidationResult {
        SkillValidationResult(
            skillID: skillID,
            status: .notValidated,
            summary: "Not validated yet."
        )
    }
}
