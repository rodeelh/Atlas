import Foundation
import AtlasLogging
import AtlasMemory
import AtlasShared
import AtlasSkills

// MARK: - ForgeProposalError

public enum ForgeProposalError: LocalizedError, Sendable {
    case proposalNotFound(UUID)
    case invalidStatus(ForgeProposalStatus)
    case runtimeNotReady

    public var errorDescription: String? {
        switch self {
        case .proposalNotFound(let id):
            return "Forge proposal \(id.uuidString) not found."
        case .invalidStatus(let s):
            return "Cannot approve a proposal in '\(s.rawValue)' status. Only 'pending' proposals can be approved."
        case .runtimeNotReady:
            return "Forge runtime is not yet initialized. Ensure CoreSkills bootstrap has completed."
        }
    }
}

// MARK: - ForgeProposalService

/// Service layer for Forge proposal lifecycle.
///
/// Responsibilities:
/// - Create and persist proposals from `ForgeSkillSpec` + `[ForgeActionPlan]`
/// - Track in-memory "researching" items for live UI feedback
/// - Approve (install ± enable) and reject proposals
/// - Expose installed forged skills for the Forge UI
///
/// Lives in `AtlasCore` to access both `ForgeProposalStore` (AtlasMemory)
/// and `CoreSkillsRuntime` (AtlasSkills) without circular imports.
public actor ForgeProposalService {
    private let store: ForgeProposalStore
    private let logger: AtlasLogger

    /// Injected after CoreSkills bootstrap via `configure(coreSkills:skillRegistry:)`.
    private var coreSkills: CoreSkillsRuntime?
    private var skillRegistry: SkillRegistry?

    /// In-memory researching items. Not persisted — ephemeral activity signals.
    private var researchingItems: [ForgeResearchingItem] = []

    public init(
        store: ForgeProposalStore,
        logger: AtlasLogger = AtlasLogger(category: "forge.service")
    ) {
        self.store = store
        self.logger = logger
    }

    /// Called from `AgentRuntime.bootstrapSkillsIfNeeded()` after CoreSkills are ready.
    public func configure(coreSkills: CoreSkillsRuntime, skillRegistry: SkillRegistry) {
        self.coreSkills = coreSkills
        self.skillRegistry = skillRegistry
    }

    // MARK: - Researching State

    /// Register an ephemeral researching item. Returns the item with its assigned ID.
    @discardableResult
    public func startResearching(title: String, message: String) -> ForgeResearchingItem {
        let item = ForgeResearchingItem(title: title, message: message)
        researchingItems.append(item)
        logger.info("Forge researching started", metadata: ["title": title])
        return item
    }

    /// Remove a researching item by ID once research is complete or abandoned.
    public func stopResearching(id: UUID) {
        researchingItems.removeAll { $0.id == id }
    }

    public func listResearching() -> [ForgeResearchingItem] {
        researchingItems
    }

    // MARK: - Proposal Creation

    /// Create and persist a new Forge proposal.
    ///
    /// - Parameters:
    ///   - spec:         The validated `ForgeSkillSpec` for this skill.
    ///   - plans:        The `ForgeActionPlan` array defining how each action executes.
    ///   - summary:      Human-readable explanation of what the skill does and why it was created.
    ///   - rationale:    Optional explanation of *why* Atlas is proposing this right now.
    ///   - contractJSON: JSON-encoded `APIResearchContract` that passed the validation gate.
    ///                   Present only for API skills; nil for composed/transform/workflow.
    @discardableResult
    public func createProposal(
        spec: ForgeSkillSpec,
        plans: [ForgeActionPlan],
        summary: String,
        rationale: String? = nil,
        contractJSON: String? = nil
    ) async throws -> ForgeProposalRecord {
        let encoder = AtlasJSON.encoder
        let specJSON  = String(data: try encoder.encode(spec), encoding: .utf8) ?? "{}"
        let plansJSON = String(data: try encoder.encode(plans), encoding: .utf8) ?? "[]"

        // Extract required secrets from HTTP plans.
        // Collect both new-style authSecretKey (AuthCore v1) and legacy secretHeader,
        // deduplicate, and expose them as the proposal's declared Keychain dependencies.
        let requiredSecrets: [String] = plans.flatMap { plan -> [String] in
            guard let http = plan.httpRequest else { return [] }
            return [http.authSecretKey, http.secretHeader].compactMap { $0 }
        }.unique()

        // Extract URL hosts that will be contacted at runtime
        let domains: [String] = plans
            .compactMap { plan -> String? in
                guard let urlString = plan.httpRequest?.url,
                      let host = URL(string: urlString)?.host else { return nil }
                return host
            }
            .unique()
            .sorted()

        let actionNames = spec.actions.map(\.name)

        let proposal = ForgeProposalRecord(
            skillID: spec.id,
            name: spec.name,
            description: spec.description,
            summary: summary,
            rationale: rationale,
            requiredSecrets: requiredSecrets,
            domains: domains,
            actionNames: actionNames,
            riskLevel: spec.riskLevel.rawValue,
            status: .pending,
            specJSON: specJSON,
            plansJSON: plansJSON,
            contractJSON: contractJSON
        )

        try await store.save(proposal)

        logger.info("Created Forge proposal", metadata: [
            "id": proposal.id.uuidString,
            "skillID": spec.id,
            "name": spec.name
        ])

        return proposal
    }

    // MARK: - Restart Hydration

    /// Re-registers all previously installed/enabled Forge skills after daemon restart.
    ///
    /// The `SkillRegistry` persists lifecycle state in UserDefaults but the actual
    /// `ForgeSkill` implementation objects are in-memory only. This method rebuilds
    /// those objects from the persisted `specJSON`/`plansJSON` in SQLite so the
    /// agent can use them without user re-approval.
    ///
    /// Must be called from `AgentRuntime.bootstrapSkillsIfNeeded()` after `configure()`.
    public func hydrateInstalledSkills() async {
        guard let core = coreSkills else {
            logger.warning("ForgeProposalService: skipping hydration — not yet configured")
            return
        }

        let all: [ForgeProposalRecord]
        do {
            all = try await store.list()
        } catch {
            logger.error("ForgeProposalService: hydration failed — could not load proposals", metadata: [
                "error": error.localizedDescription
            ])
            return
        }

        let toHydrate = all.filter { $0.status == .installed || $0.status == .enabled }
        guard !toHydrate.isEmpty else { return }

        logger.info("ForgeProposalService: hydrating Forge skills", metadata: [
            "count": "\(toHydrate.count)"
        ])

        let decoder = AtlasJSON.decoder
        for proposal in toHydrate {
            do {
                let spec  = try decoder.decode(ForgeSkillSpec.self, from: Data(proposal.specJSON.utf8))
                let plans = try decoder.decode([ForgeActionPlan].self, from: Data(proposal.plansJSON.utf8))
                let (package, actionDefs) = try core.forge.buildPackage(spec: spec, plans: plans)
                try await core.skills.installForgeSkill(package: package, actionDefinitions: actionDefs)
                if proposal.status == .enabled {
                    _ = try await core.skills.enable(skillID: spec.id)
                }
                logger.info("ForgeProposalService: hydrated Forge skill", metadata: [
                    "skillID": spec.id,
                    "status": proposal.status.rawValue
                ])
            } catch {
                logger.error("ForgeProposalService: failed to hydrate Forge skill", metadata: [
                    "proposalID": proposal.id.uuidString,
                    "skillID": proposal.skillID,
                    "error": error.localizedDescription
                ])
            }
        }
    }

    // MARK: - Proposal Queries

    public func listProposals() async throws -> [ForgeProposalRecord] {
        try await store.listActive()
    }

    public func fetchProposal(id: UUID) async throws -> ForgeProposalRecord? {
        try await store.fetch(id: id)
    }

    // MARK: - Approval

    /// Install (and optionally enable) a pending proposal.
    ///
    /// Steps:
    /// 1. Decode stored `specJSON` + `plansJSON` back to typed values
    /// 2. Build `ForgeSkillPackage` via `CoreForgeService.buildPackage`
    /// 3. Install via `CoreSkillService.installForgeSkill` (idempotent)
    /// 4. If `enable == true`, call `CoreSkillService.enable` → triggers catalog resync
    /// 5. Persist updated status
    @discardableResult
    public func approveProposal(id: UUID, enable: Bool) async throws -> ForgeProposalRecord {
        guard let proposal = try await store.fetch(id: id) else {
            throw ForgeProposalError.proposalNotFound(id)
        }

        guard proposal.status == .pending else {
            throw ForgeProposalError.invalidStatus(proposal.status)
        }

        guard let core = coreSkills else {
            throw ForgeProposalError.runtimeNotReady
        }

        let decoder = AtlasJSON.decoder
        let spec  = try decoder.decode(ForgeSkillSpec.self, from: Data(proposal.specJSON.utf8))
        let plans = try decoder.decode([ForgeActionPlan].self, from: Data(proposal.plansJSON.utf8))

        let (package, actionDefs) = try core.forge.buildPackage(spec: spec, plans: plans)
        try await core.skills.installForgeSkill(package: package, actionDefinitions: actionDefs)

        let newStatus: ForgeProposalStatus
        if enable {
            _ = try await core.skills.enable(skillID: spec.id)
            newStatus = .enabled
        } else {
            newStatus = .installed
        }

        try await store.updateStatus(id: id, status: newStatus)

        logger.info("Approved Forge proposal", metadata: [
            "id": id.uuidString,
            "skillID": spec.id,
            "enabled": enable ? "true" : "false"
        ])

        return (try await store.fetch(id: id)) ?? proposal
    }

    // MARK: - Rejection

    @discardableResult
    public func rejectProposal(id: UUID) async throws -> ForgeProposalRecord {
        guard let proposal = try await store.fetch(id: id) else {
            throw ForgeProposalError.proposalNotFound(id)
        }

        try await store.updateStatus(id: id, status: .rejected)

        logger.info("Rejected Forge proposal", metadata: [
            "id": id.uuidString,
            "skillID": proposal.skillID
        ])

        return (try await store.fetch(id: id)) ?? proposal
    }

    // MARK: - Uninstall

    /// Remove a previously installed Forge skill from the live registry and mark its
    /// proposal as `.uninstalled` so it is not re-hydrated on daemon restart.
    ///
    /// Steps:
    /// 1. Delegate runtime removal to `CoreSkillService.uninstallForgeSkill` — this
    ///    disables if needed, deregisters, clears UserDefaults state, and resyncs.
    /// 2. Find the persisted proposal for this skillID and update its status to
    ///    `.uninstalled` so `hydrateInstalledSkills()` skips it on next restart.
    ///
    /// Throws `ForgeProposalError.runtimeNotReady` if called before `configure()`.
    /// Throws `CoreSkillServiceError.notAForgeSkill` if `skillID` belongs to a built-in.
    /// Throws `SkillRegistryError.unknownSkill` if the skill is not currently registered.
    public func uninstallForgeSkill(skillID: String) async throws {
        guard let core = coreSkills else {
            throw ForgeProposalError.runtimeNotReady
        }

        // Remove from live registry (validates forge source, disables if needed, resyncs).
        try await core.skills.uninstallForgeSkill(skillID: skillID)

        // Mark ALL proposals for this skillID as uninstalled so restart hydration skips them.
        // Multiple proposals can exist for the same skillID after install → uninstall → reinstall cycles.
        // Using only `findBySkillID` (which returns the oldest) would miss the active one.
        if let all = try? await store.list() {
            for proposal in all where proposal.skillID == skillID &&
                (proposal.status == .installed || proposal.status == .enabled) {
                try await store.updateStatus(id: proposal.id, status: .uninstalled)
            }
        }

        logger.info("Uninstalled Forge skill", metadata: ["skillID": skillID])
    }

    // MARK: - Installed Forge Skills

    /// Returns all registered skills whose `source` is "forge".
    public func listInstalledForgedSkills() async -> [AtlasSkillRecord] {
        guard let registry = skillRegistry else { return [] }
        return await registry.listAll().filter { $0.manifest.source == "forge" }
    }
}

// MARK: - Array helper

private extension Array where Element: Hashable {
    func unique() -> [Element] {
        var seen = Set<Element>()
        return filter { seen.insert($0).inserted }
    }
}
