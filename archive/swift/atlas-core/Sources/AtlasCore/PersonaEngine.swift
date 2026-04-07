import Foundation
import AtlasShared

public protocol PersonaProviding: Sendable {
    func activeProfile() -> PersonaProfile
}

public struct PersonaEngine: PersonaProviding, Sendable {
    private let config: AtlasConfig

    public init(config: AtlasConfig) {
        self.config = config
    }

    public func activeProfile() -> PersonaProfile {
        return PersonaProfile(
            name: config.personaName,
            actionSafetyMode: config.actionSafetyMode,
            safetyRules: [
                .neverFakeMemory,
                .neverFakeSuccess,
                .stateUncertaintyClearly,
                .respectApprovalBoundaries,
                .doNotStoreTrivialJunk
            ]
        )
    }
}
