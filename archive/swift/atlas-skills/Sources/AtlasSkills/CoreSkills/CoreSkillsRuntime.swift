import Foundation
import AtlasLogging
import AtlasShared

// MARK: - CoreSkillsRuntime

/// The CoreSkills runtime aggregates all internal capability services.
///
/// CoreSkills are NOT user-facing skills and are NOT registered in `SkillRegistry`.
/// They are internal infrastructure used by the daemon, built-in skills, and Forge.
///
/// **Skill layer model:**
/// ```
/// CoreSkills   — hidden internal services (this layer, never in registry)
/// Built-in     — user-visible skills registered in SkillRegistry at bootstrap
/// Forge/Forged — dynamically installed via CoreSkillService, registered in SkillRegistry
/// ```
///
/// Instantiated by `AgentRuntime` during bootstrap and held for the runtime's lifetime.
/// Exposed as `AgentRuntime.coreSkills` for use by Forge and internal operations.
public struct CoreSkillsRuntime: Sendable {

    // MARK: - Services

    /// Structured HTTP execution primitive. Use for any internal network calls.
    public let http: CoreHTTPService

    /// Time, locale, and runtime environment primitives.
    public let context: CoreContextService

    /// Keychain credential access. Values are never logged. Read-only.
    public let secrets: CoreSecretsService

    /// Policy evaluation and internal audit logging.
    public let policy: CorePolicyService

    /// Skill registry operations: install, enable, disable, list.
    /// Enables a Forge skill triggers an immediate catalog resync via the injected callback.
    public let skills: CoreSkillService

    /// Forge scaffolding, validation, and package assembly.
    /// Foundation for the Dynamic Skill Runtime.
    public let forge: CoreForgeService

    // MARK: - Init

    /// Create the CoreSkills runtime.
    ///
    /// - Parameters:
    ///   - registry: The shared `SkillRegistry` actor used by `skills` for install/enable/disable.
    ///   - secretsReader: Closure that reads a Keychain secret by service key.
    ///     Injected from `AgentRuntime` so `atlas-skills` does not import `KeychainSecretStore`.
    ///     Must return `nil` for "not set" and throw only for genuine Keychain errors.
    ///   - resyncCallback: Closure called after a skill is enabled/disabled to refresh
    ///     the live tool catalog. Injected from `AgentRuntime.resyncSkillCatalog()`.
    ///     Defaults to a no-op so unit tests and staging setups need not supply it.
    ///   - logger: Logger for runtime-level events. Services use their own category loggers.
    public init(
        registry: SkillRegistry,
        secretsReader: @escaping CoreSecretsService.SecretsReader,
        resyncCallback: @escaping @Sendable () async -> Void = {},
        logger: AtlasLogger = AtlasLogger(category: "core.runtime")
    ) {
        let secrets = CoreSecretsService(
            reader: secretsReader,
            logger: AtlasLogger(category: "core.secrets")
        )

        self.http     = CoreHTTPService(logger: AtlasLogger(category: "core.http"))
        self.context  = CoreContextService()
        self.secrets  = secrets
        self.policy   = CorePolicyService(logger: AtlasLogger(category: "core.policy"))
        self.skills   = CoreSkillService(
            registry: registry,
            secrets: secrets,
            resyncCallback: resyncCallback,
            logger: AtlasLogger(category: "core.skill")
        )
        self.forge    = CoreForgeService(logger: AtlasLogger(category: "core.forge"))

        logger.info("CoreSkills runtime initialized")
    }
}
