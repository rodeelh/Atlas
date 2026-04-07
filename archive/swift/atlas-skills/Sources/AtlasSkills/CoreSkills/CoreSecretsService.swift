import Foundation
import AtlasLogging

// MARK: - Output Types

/// The result of a credential presence and validity check.
/// The actual secret value is never included — only structural facts about it.
public struct CoreCredentialValidation: Sendable {
    public let service: String
    public let isPresent: Bool
    public let isNonEmpty: Bool

    public var isValid: Bool { isPresent && isNonEmpty }

    public var summary: String {
        if isValid { return "Credential '\(service)' is present and non-empty." }
        if !isPresent { return "Credential '\(service)' is not configured." }
        return "Credential '\(service)' is set but empty."
    }
}

// MARK: - CoreSecretsService

/// Internal secrets/credential access for CoreSkills and Forge.
///
/// Uses closure injection so the concrete KeychainSecretStore does not need to be
/// imported by callers — the closure is provided by AgentRuntime at startup.
///
/// Security rules enforced here:
/// - Secret values are NEVER logged.
/// - Validation returns only structural facts (present/non-empty), not the value.
/// - The service is read-only — no write or delete operations.
public struct CoreSecretsService: Sendable {

    /// Async closure that maps a Keychain service key to its secret value.
    /// Returns nil if the credential is not set. Throws on actual Keychain errors.
    public typealias SecretsReader = @Sendable (String) async throws -> String?

    private let reader: SecretsReader
    private let logger: AtlasLogger

    public init(
        reader: @escaping SecretsReader,
        logger: AtlasLogger = AtlasLogger(category: "core.secrets")
    ) {
        self.reader = reader
        self.logger = logger
    }

    /// Retrieve a secret by Keychain service key. Returns nil if not set.
    /// The value is NEVER logged or exposed in error messages.
    public func get(service: String) async throws -> String? {
        logger.info("CoreSecrets reading credential", metadata: ["service": service])
        return try await reader(service)
    }

    /// Check whether a credential is configured and non-empty without exposing its value.
    public func validate(service: String) async -> CoreCredentialValidation {
        do {
            let value = try await reader(service)
            return CoreCredentialValidation(
                service: service,
                isPresent: value != nil,
                isNonEmpty: !(value?.isEmpty ?? true)
            )
        } catch {
            // Log that validation failed structurally, never the error value
            logger.warning("CoreSecrets credential validation error", metadata: ["service": service])
            return CoreCredentialValidation(service: service, isPresent: false, isNonEmpty: false)
        }
    }
}
