import Foundation
import AtlasLogging

// MARK: - CoreSkillService Errors

public enum CoreSkillServiceError: LocalizedError, Sendable {
    case missingCredential(String)
    case alreadyInstalled(String)
    case notAForgeSkill(String)

    public var errorDescription: String? {
        switch self {
        case .missingCredential(let msg):
            return "Cannot install Forge skill — missing credential: \(msg)"
        case .alreadyInstalled(let id):
            return "A skill with id '\(id)' is already registered."
        case .notAForgeSkill(let id):
            return "Cannot uninstall '\(id)' — only Forge skills can be uninstalled."
        }
    }
}

// MARK: - CoreSkillService

/// Internal skill registry operations for CoreSkills and Forge.
///
/// Provides the install → enable → disable lifecycle for dynamically loaded
/// skills. All operations delegate to the shared `SkillRegistry` actor so the
/// governed execution path is preserved.
///
/// **Key rules:**
/// - Install does NOT imply enable. A skill must be explicitly enabled before
///   the runtime presents it to the agent as a callable tool.
/// - Enabling a Forge skill triggers an immediate catalog resync so the agent
///   can use the skill in the same session without a daemon restart.
/// - `requiredSecrets` on a Forge skill's manifest are validated before install.
public struct CoreSkillService: Sendable {
    private let registry: SkillRegistry
    private let secrets: CoreSecretsService
    private let logger: AtlasLogger
    /// Called after a skill is enabled so the live tool catalog is refreshed.
    /// Injected from AgentRuntime at bootstrap via closure to avoid circular imports.
    private let resyncCallback: @Sendable () async -> Void

    public init(
        registry: SkillRegistry,
        secrets: CoreSecretsService,
        resyncCallback: @escaping @Sendable () async -> Void = {},
        logger: AtlasLogger = AtlasLogger(category: "core.skill")
    ) {
        self.registry = registry
        self.secrets = secrets
        self.resyncCallback = resyncCallback
        self.logger = logger
    }

    // MARK: - Forge Install Flow

    /// Install a Forge skill package into the registry.
    ///
    /// Pre-checks that all `requiredSecrets` in the manifest are configured.
    /// Creates a `ForgeSkill` adapter and registers it in `.installed` state.
    /// The skill will NOT be presented to the agent until `enable(skillID:)` is called.
    ///
    /// **Idempotent:** if a skill with the same ID is already registered, this method
    /// logs a warning and returns without error so repeated install calls are safe.
    public func installForgeSkill(
        package: ForgeSkillPackage,
        actionDefinitions: [SkillActionDefinition]
    ) async throws {
        let skillID = package.manifest.id

        // Pre-check: all required secrets must be present before install.
        for service in package.manifest.requiredSecrets {
            let check = await secrets.validate(service: service)
            if !check.isValid {
                throw CoreSkillServiceError.missingCredential(check.summary)
            }
        }

        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefinitions,
            secretsService: secrets
        )

        do {
            logger.info("CoreSkill installing Forge skill", metadata: ["skill_id": skillID])
            try await registry.register(skill)
        } catch SkillRegistryError.duplicateSkill {
            // Idempotent: skill already exists. This is not a failure for Forge.
            logger.warning("CoreSkill Forge skill already registered — skipping install", metadata: [
                "skill_id": skillID
            ])
        }
    }

    // MARK: - General Lifecycle

    /// Install any skill into the registry in the `.installed` state.
    /// The skill will not be presented to the agent until explicitly enabled.
    public func install(_ skill: any AtlasSkill) async throws {
        logger.info("CoreSkill installing skill", metadata: ["skill_id": skill.manifest.id])
        try await registry.register(skill)
    }

    /// Enable a previously installed skill so the agent can use it.
    /// Triggers a catalog resync so the skill is immediately visible to the agent.
    @discardableResult
    public func enable(skillID: String) async throws -> AtlasSkillRecord {
        logger.info("CoreSkill enabling skill", metadata: ["skill_id": skillID])
        let record = try await registry.enable(skillID: skillID)
        // Resync immediately so the agent sees the newly enabled skill without restart.
        await resyncCallback()
        return record
    }

    /// Disable a skill without removing it from the registry.
    /// Triggers a catalog resync to remove the skill from the agent's tool catalog.
    @discardableResult
    public func disable(skillID: String) async throws -> AtlasSkillRecord {
        logger.info("CoreSkill disabling skill", metadata: ["skill_id": skillID])
        let record = try await registry.disable(skillID: skillID)
        await resyncCallback()
        return record
    }

    /// Permanently remove a Forge skill from the registry.
    ///
    /// Safety rules:
    /// - Only skills with `manifest.source == "forge"` may be uninstalled.
    /// - If the skill is currently enabled it is disabled first, removing it from the
    ///   live tool catalog before deregistration.
    /// - The SkillRegistry clears the UserDefaults state entry so the skill does not
    ///   re-appear if the daemon restarts or `hydrateInstalledSkills()` is called again.
    /// - A catalog resync is triggered so the agent's tool list is updated immediately.
    public func uninstallForgeSkill(skillID: String) async throws {
        guard let record = await registry.skill(id: skillID) else {
            throw SkillRegistryError.unknownSkill(skillID)
        }
        guard record.manifest.source == "forge" else {
            throw CoreSkillServiceError.notAForgeSkill(skillID)
        }
        // Disable first if currently enabled so it's removed from the action catalog.
        if record.manifest.lifecycleState == .enabled {
            _ = try await registry.disable(skillID: skillID)
        }
        // Deregister: removes from in-memory entries and clears UserDefaults state.
        try await registry.deregister(skillID: skillID)
        // Resync: ensures the tool catalog reflects the removal immediately.
        await resyncCallback()
        logger.info("Uninstalled Forge skill", metadata: ["skill_id": skillID])
    }

    // MARK: - Query

    /// List all registered skills, optionally filtered by category.
    public func list(category: SkillCategory? = nil) async -> [AtlasSkillRecord] {
        let all = await registry.listAll()
        guard let category else { return all }
        return all.filter { $0.manifest.category == category }
    }

    /// List all user-visible skills (excludes internal/hidden skills).
    public func listVisible() async -> [AtlasSkillRecord] {
        await registry.listVisible()
    }

    /// List only enabled skills.
    public func listEnabled() async -> [AtlasSkillRecord] {
        await registry.listEnabled()
    }

    /// Look up a skill by its ID.
    public func skill(id: String) async -> AtlasSkillRecord? {
        await registry.skill(id: id)
    }
}
