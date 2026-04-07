import Foundation
import AtlasLogging
import AtlasShared

/// Persists per-action approval policies as a JSON dictionary keyed by action ID.
///
/// Every registered action gets an explicit policy written at initialization time based on
/// its `permissionLevel`. User overrides are preserved across restarts and skill updates.
public actor ActionPolicyStore {
    private var policies: [String: ActionApprovalPolicy]
    private let fileURL: URL
    private let logger: AtlasLogger

    public init(
        directory: URL? = nil,
        logger: AtlasLogger = AtlasLogger(category: "action-policy")
    ) {
        let resolvedDirectory = directory ?? Self.defaultDirectory()
        self.fileURL = resolvedDirectory.appendingPathComponent("action-policies.json")
        self.logger = logger
        self.policies = Self.load(from: resolvedDirectory.appendingPathComponent("action-policies.json"))
    }

    // MARK: - Public API

    /// Returns the stored policy for the given action ID.
    public func policy(for actionID: String) -> ActionApprovalPolicy? {
        policies[actionID]
    }

    /// Returns the effective policy: the stored policy if present, or the permission-level default.
    public func effectivePolicy(for actionID: String, permissionLevel: PermissionLevel) -> ActionApprovalPolicy {
        policies[actionID] ?? ActionApprovalPolicy.defaultPolicy(for: permissionLevel)
    }

    /// Sets a user-chosen policy for the given action.
    public func setPolicy(_ policy: ActionApprovalPolicy, for actionID: String) {
        policies[actionID] = policy
        persist()

        logger.info("Updated action approval policy", metadata: [
            "action_id": actionID,
            "policy": policy.rawValue
        ])
    }

    /// Called by the gateway after a successful `askOnce` approval — promotes to `autoApprove`.
    public func promoteToAutoApprove(actionID: String) {
        guard policies[actionID] == .askOnce else { return }

        policies[actionID] = .autoApprove
        persist()

        logger.info("Promoted action policy after askOnce approval", metadata: [
            "action_id": actionID,
            "new_policy": ActionApprovalPolicy.autoApprove.rawValue
        ])
    }

    /// Resets an action to its permission-level default.
    public func resetPolicy(for actionID: String, permissionLevel: PermissionLevel) {
        let defaultPolicy = ActionApprovalPolicy.defaultPolicy(for: permissionLevel)
        policies[actionID] = defaultPolicy
        persist()

        logger.info("Reset action approval policy to default", metadata: [
            "action_id": actionID,
            "policy": defaultPolicy.rawValue
        ])
    }

    /// Idempotent: writes a default policy for each action only if no entry already exists.
    /// Called at skill registration time so every action always has an explicit policy.
    public func initializeActions(_ actions: [SkillActionDefinition]) {
        var changed = false

        for action in actions {
            guard policies[action.id] == nil else { continue }

            policies[action.id] = ActionApprovalPolicy.defaultPolicy(for: action.permissionLevel)
            changed = true
        }

        if changed {
            persist()
        }
    }

    /// Returns a snapshot of all stored policies. Used by the API layer.
    public func allPolicies() -> [String: ActionApprovalPolicy] {
        policies
    }

    // MARK: - Persistence

    private func persist() {
        do {
            let directory = fileURL.deletingLastPathComponent()
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)

            let data = try AtlasJSON.encoder.encode(policies)
            try data.write(to: fileURL, options: .atomic)
        } catch {
            logger.error("Failed to persist action policies", metadata: [
                "error": error.localizedDescription
            ])
        }
    }

    private static func load(from url: URL) -> [String: ActionApprovalPolicy] {
        guard let data = try? Data(contentsOf: url) else {
            return [:]
        }

        return (try? AtlasJSON.decoder.decode([String: ActionApprovalPolicy].self, from: data)) ?? [:]
    }

    private static func defaultDirectory() -> URL {
        let appSupport = (try? FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? FileManager.default.temporaryDirectory

        return appSupport.appendingPathComponent("ProjectAtlas", isDirectory: true)
    }
}
