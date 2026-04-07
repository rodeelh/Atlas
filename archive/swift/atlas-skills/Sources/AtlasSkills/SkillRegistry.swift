import Foundation
import AtlasLogging
import AtlasShared

public enum SkillRegistryError: LocalizedError {
    case duplicateSkill(String)
    case unknownSkill(String)
    case validationFailed(String)

    public var errorDescription: String? {
        switch self {
        case .duplicateSkill(let id):
            return "A skill with id '\(id)' is already registered."
        case .unknownSkill(let id):
            return "The skill '\(id)' is not registered."
        case .validationFailed(let message):
            return message
        }
    }
}

public actor SkillRegistry {
    private struct SkillStateSnapshot: Codable, Sendable {
        let lifecycleState: SkillLifecycleState
        let validation: SkillValidationResult?
    }

    private final class SkillStateStore: @unchecked Sendable {
        private let defaults: UserDefaults
        private let prefix = "AtlasSkillState."

        init(defaults: UserDefaults = .standard) {
            self.defaults = defaults
        }

        func loadState(for skillID: String) -> SkillStateSnapshot? {
            guard let data = defaults.data(forKey: prefix + skillID) else {
                return nil
            }

            return try? AtlasJSON.decoder.decode(SkillStateSnapshot.self, from: data)
        }

        func saveState(_ snapshot: SkillStateSnapshot, for skillID: String) {
            guard let data = try? AtlasJSON.encoder.encode(snapshot) else {
                return
            }

            defaults.set(data, forKey: prefix + skillID)
        }

        func clearState(for skillID: String) {
            defaults.removeObject(forKey: prefix + skillID)
        }
    }

    private struct Entry {
        let skill: any AtlasSkill
        var manifest: SkillManifest
        var validation: SkillValidationResult?
    }

    private let logger: AtlasLogger
    private let stateStore: SkillStateStore
    private let policyStore: ActionPolicyStore
    private var entries: [String: Entry]

    public init(
        policyStore: ActionPolicyStore = ActionPolicyStore(),
        logger: AtlasLogger = AtlasLogger(category: "skills-registry"),
        defaults: UserDefaults = .standard
    ) {
        self.logger = logger
        self.stateStore = SkillStateStore(defaults: defaults)
        self.policyStore = policyStore
        self.entries = [:]
    }

    public func register(_ skill: any AtlasSkill) async throws {
        guard entries[skill.manifest.id] == nil else {
            throw SkillRegistryError.duplicateSkill(skill.manifest.id)
        }

        let persisted = stateStore.loadState(for: skill.manifest.id)
        let lifecycleState = persisted?.lifecycleState ?? defaultLifecycleState(for: skill.manifest)
        let validation = persisted?.validation

        entries[skill.manifest.id] = Entry(
            skill: skill,
            manifest: skill.manifest.updatingLifecycleState(lifecycleState),
            validation: validation
        )

        // Initialize default policies for all actions in this skill
        await policyStore.initializeActions(skill.actions)

        logger.info("Registered Atlas skill", metadata: [
            "skill_id": skill.manifest.id,
            "state": lifecycleState.rawValue
        ])
    }

    public func register(_ skills: [any AtlasSkill]) async throws {
        for skill in skills {
            try await register(skill)
        }
    }

    public func listAll() async -> [AtlasSkillRecord] {
        var records: [AtlasSkillRecord] = []
        for entry in entries.values {
            records.append(await record(from: entry))
        }
        return records.sorted { $0.manifest.name.localizedCaseInsensitiveCompare($1.manifest.name) == .orderedAscending }
    }

    public func listVisible() async -> [AtlasSkillRecord] {
        let all = await listAll()
        return all.filter { $0.manifest.isUserVisible }
    }

    public func listEnabled() async -> [AtlasSkillRecord] {
        let all = await listAll()
        return all.filter(\.isEnabled)
    }

    public func skill(id: String) async -> AtlasSkillRecord? {
        guard let entry = entries[id] else { return nil }
        return await record(from: entry)
    }

    func skillImplementation(id: String) -> (any AtlasSkill)? {
        entries[id]?.skill
    }

    public func enabledActionCatalog() async -> [SkillActionCatalogItem] {
        let enabled = await listEnabled()
        return enabled.flatMap { record in
            record.actions
                .filter(\.isEnabled)
                .map { action in
                    SkillActionCatalogItem(
                        skillID: record.manifest.id,
                        skillName: record.manifest.name,
                        skillDescription: record.manifest.description,
                        skillCategory: record.manifest.category,
                        trustProfile: record.manifest.trustProfile,
                        freshnessType: record.manifest.freshnessType,
                        action: action,
                        riskLevel: record.manifest.riskLevel,
                        preferredQueryTypes: Array(Set(record.manifest.preferredQueryTypes + action.preferredQueryTypes)),
                        routingPriority: record.manifest.routingPriority + action.routingPriority,
                        canAnswerStructuredLiveData: record.manifest.canAnswerStructuredLiveData,
                        canHandleLocalData: record.manifest.canHandleLocalData,
                        canHandleExploratoryQueries: record.manifest.canHandleExploratoryQueries,
                        alwaysInclude: record.manifest.alwaysInclude
                    )
                }
        }
    }

    @discardableResult
    public func enable(skillID: String) async throws -> AtlasSkillRecord {
        guard var entry = entries[skillID] else {
            throw SkillRegistryError.unknownSkill(skillID)
        }

        if entry.validation?.status == .failed {
            throw SkillRegistryError.validationFailed(
                "The skill '\(entry.manifest.name)' failed validation and cannot be enabled."
            )
        }

        entry.manifest = entry.manifest.updatingLifecycleState(.enabled)
        entries[skillID] = entry
        persist(entry)

        logger.info("Enabled Atlas skill", metadata: ["skill_id": skillID])
        return await record(from: entry)
    }

    @discardableResult
    public func disable(skillID: String) async throws -> AtlasSkillRecord {
        guard var entry = entries[skillID] else {
            throw SkillRegistryError.unknownSkill(skillID)
        }

        entry.manifest = entry.manifest.updatingLifecycleState(.disabled)
        entries[skillID] = entry
        persist(entry)

        logger.info("Disabled Atlas skill", metadata: ["skill_id": skillID])
        return await record(from: entry)
    }

    /// Remove a skill from the registry entirely and clear its persisted state.
    ///
    /// Used exclusively for Forge skill uninstallation. The cleared UserDefaults entry
    /// ensures the skill is not re-registered if `register()` is called again (e.g. a
    /// fresh install after the user decides to reinstall). Built-in skills must never be
    /// deregistered — enforce this at the call site (`CoreSkillService.uninstallForgeSkill`).
    public func deregister(skillID: String) throws {
        guard entries[skillID] != nil else {
            throw SkillRegistryError.unknownSkill(skillID)
        }
        entries.removeValue(forKey: skillID)
        stateStore.clearState(for: skillID)
        logger.info("Deregistered skill", metadata: ["skill_id": skillID])
    }

    @discardableResult
    public func validate(skillID: String, context: SkillValidationContext) async throws -> AtlasSkillRecord {
        guard var entry = entries[skillID] else {
            throw SkillRegistryError.unknownSkill(skillID)
        }

        let result = await entry.skill.validateConfiguration(context: context)
        entry.validation = result

        if result.status == .failed {
            entry.manifest = entry.manifest.updatingLifecycleState(.failedValidation)
        } else if entry.manifest.lifecycleState == .failedValidation {
            let restoredState: SkillLifecycleState = entry.manifest.isEnabledByDefault ? .enabled : .configured
            entry.manifest = entry.manifest.updatingLifecycleState(restoredState)
        } else if entry.manifest.lifecycleState == .known || entry.manifest.lifecycleState == .proposed {
            entry.manifest = entry.manifest.updatingLifecycleState(.configured)
        }

        entries[skillID] = entry
        persist(entry)

        logger.info("Validated Atlas skill", metadata: [
            "skill_id": skillID,
            "status": result.status.rawValue
        ])

        return await record(from: entry)
    }

    private func record(from entry: Entry) async -> AtlasSkillRecord {
        let policies = await policyStore.allPolicies()
        let enrichedActions = entry.skill.actions.map { action in
            let policy = policies[action.id] ?? ActionApprovalPolicy.defaultPolicy(for: action.permissionLevel)
            return action.updatingApprovalPolicy(policy)
        }

        return AtlasSkillRecord(
            manifest: entry.manifest,
            actions: enrichedActions,
            validation: entry.validation
        )
    }

    private func defaultLifecycleState(for manifest: SkillManifest) -> SkillLifecycleState {
        if manifest.isEnabledByDefault {
            return .enabled
        }

        switch manifest.lifecycleState {
        case .enabled, .configured, .installed, .known, .proposed, .disabled, .failedValidation:
            return manifest.lifecycleState
        }
    }

    private func persist(_ entry: Entry) {
        stateStore.saveState(
            SkillStateSnapshot(
                lifecycleState: entry.manifest.lifecycleState,
                validation: entry.validation
            ),
            for: entry.manifest.id
        )
    }
}
