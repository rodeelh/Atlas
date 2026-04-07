import Foundation
import AtlasShared

public enum PersonaSafetyRule: String, Codable, CaseIterable, Hashable, Sendable {
    case neverFakeMemory = "never_fake_memory"
    case neverFakeSuccess = "never_fake_success"
    case stateUncertaintyClearly = "state_uncertainty_clearly"
    case respectApprovalBoundaries = "respect_approval_boundaries"
    case doNotStoreTrivialJunk = "do_not_store_trivial_junk"
}

public struct PersonaProfile: Codable, Hashable, Sendable {
    public let name: String
    public let actionSafetyMode: AtlasActionSafetyMode
    public let safetyRules: [PersonaSafetyRule]

    public init(
        name: String,
        actionSafetyMode: AtlasActionSafetyMode,
        safetyRules: [PersonaSafetyRule]
    ) {
        self.name = name
        self.actionSafetyMode = actionSafetyMode
        self.safetyRules = safetyRules
    }
}
