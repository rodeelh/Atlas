import Foundation

// MARK: - Forge Skill Kind

/// Classifies what a Forge-generated skill does at runtime.
/// Used inside `ForgeOrchestrationSkill` to route proposals to the
/// correct validation pipeline before a proposal is created.
public enum ForgeSkillKind: String, Codable, Sendable {
    /// Connects to an external HTTP API. Requires a validated APIResearchContract.
    case api
    /// Composes two or more existing Atlas skills into a single callable action.
    case composed
    /// Transforms input data from one shape or format to another.
    case transform
    /// Sequences multiple steps, with outputs feeding into subsequent inputs.
    case workflow
}

// MARK: - Forge Proposal Status

/// The lifecycle state of a Forge proposal.
/// Stored as a raw string in SQLite so values survive daemon upgrades.
public enum ForgeProposalStatus: String, Codable, Sendable, CaseIterable {
    case pending     // Awaiting user decision
    case installed   // User approved; skill installed but not yet enabled
    case enabled     // Installed and enabled — agent can call it
    case rejected    // User rejected; kept for auditing
    case uninstalled // Was installed/enabled, then user removed it; not re-hydrated on restart
}

// MARK: - Forge Proposal Record

/// Persisted record for a Forge-generated skill proposal.
///
/// Human-readable summary fields are stored directly for the web UI to render without
/// decoding the full spec. The full `specJSON` and `plansJSON` blobs are decoded only
/// when installation is actually performed or the user requests technical details.
public struct ForgeProposalRecord: Codable, Sendable, Identifiable {
    public let id: UUID
    /// The `ForgeSkillSpec.id` — used to look up the installed skill in SkillRegistry.
    public let skillID: String
    public let name: String
    public let description: String
    /// Human-readable summary of what this skill does and why Atlas created it.
    public let summary: String
    /// Why Atlas is proposing this skill right now.
    public let rationale: String?
    /// Keychain service keys required for this skill to function.
    public let requiredSecrets: [String]
    /// URL hosts this skill will contact at runtime (e.g. "api.open-meteo.com").
    public let domains: [String]
    /// Display names of the actions the skill exposes to the agent.
    public let actionNames: [String]
    public let riskLevel: String   // "low" / "medium" / "high"
    public var status: ForgeProposalStatus
    /// JSON-encoded `ForgeSkillSpec`. Decoded in `ForgeProposalService.approveProposal`.
    public let specJSON: String
    /// JSON-encoded `[ForgeActionPlan]`. Decoded in `ForgeProposalService.approveProposal`.
    public let plansJSON: String
    /// JSON-encoded `APIResearchContract` that justified this proposal. Present only for API skills.
    /// Stored for auditability — not used at install time.
    public let contractJSON: String?
    public let createdAt: Date
    public var updatedAt: Date

    public init(
        id: UUID = UUID(),
        skillID: String,
        name: String,
        description: String,
        summary: String,
        rationale: String? = nil,
        requiredSecrets: [String] = [],
        domains: [String] = [],
        actionNames: [String] = [],
        riskLevel: String,
        status: ForgeProposalStatus = .pending,
        specJSON: String,
        plansJSON: String,
        contractJSON: String? = nil,
        createdAt: Date = .now,
        updatedAt: Date = .now
    ) {
        self.id = id
        self.skillID = skillID
        self.name = name
        self.description = description
        self.summary = summary
        self.rationale = rationale
        self.requiredSecrets = requiredSecrets
        self.domains = domains
        self.actionNames = actionNames
        self.riskLevel = riskLevel
        self.status = status
        self.specJSON = specJSON
        self.plansJSON = plansJSON
        self.contractJSON = contractJSON
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }
}

// MARK: - Forge Researching Item

/// Ephemeral in-memory item representing active Forge research activity.
///
/// Lives only in daemon memory — not persisted to SQLite. Surfaced via
/// `GET /forge/researching` to make Atlas feel actively engaged when it
/// is designing or evaluating a new skill.
public struct ForgeResearchingItem: Codable, Sendable, Identifiable {
    public let id: UUID
    /// Short working title (e.g. "Weather Skill").
    public let title: String
    /// One-line status message (e.g. "Researching Open-Meteo API").
    public let message: String
    public let startedAt: Date

    public init(
        id: UUID = UUID(),
        title: String,
        message: String,
        startedAt: Date = .now
    ) {
        self.id = id
        self.title = title
        self.message = message
        self.startedAt = startedAt
    }
}
