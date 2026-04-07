import Foundation

public enum AtlasActionSafetyMode: String, Codable, CaseIterable, Hashable, Sendable, CustomStringConvertible {
    case alwaysAskBeforeActions = "always_ask_before_actions"
    case askOnlyForRiskyActions = "ask_only_for_risky_actions"
    case moreAutonomous = "more_autonomous"

    public var title: String {
        switch self {
        case .alwaysAskBeforeActions:
            return "Always ask before actions"
        case .askOnlyForRiskyActions:
            return "Ask only for risky actions"
        case .moreAutonomous:
            return "More autonomous"
        }
    }

    public var description: String { title }

    public var segmentedTitle: String {
        switch self {
        case .alwaysAskBeforeActions:
            return "Always Ask"
        case .askOnlyForRiskyActions:
            return "Risky Only"
        case .moreAutonomous:
            return "Autonomous"
        }
    }
}
