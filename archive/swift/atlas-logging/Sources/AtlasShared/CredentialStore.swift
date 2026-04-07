import Foundation

public struct UnsupportedSecretBackend: SecretStore, SecretCacheInvalidating, Sendable {
    private let backendName: String

    public init(backendName: String = "unsupported") {
        self.backendName = backendName
    }

    public func getSecret(name: String) throws -> String? {
        throw UnsupportedSecretStoreError.unavailableBackend(
            description: "Secret backend '\(backendName)' is not implemented yet."
        )
    }

    public func setSecret(name: String, value: String) throws {
        throw UnsupportedSecretStoreError.unavailableBackend(
            description: "Secret backend '\(backendName)' is not implemented yet."
        )
    }

    public func deleteSecret(name: String) throws {
        throw UnsupportedSecretStoreError.unavailableBackend(
            description: "Secret backend '\(backendName)' is not implemented yet."
        )
    }

    public func hasSecret(name: String) -> Bool {
        false
    }

    public func listSecretNames() throws -> [String] {
        []
    }

    public func invalidateSecretCache() {}
}

public struct UnsupportedCredentialStore: CredentialStore, Sendable {
    private let backendName: String

    public init(backendName: String = "unsupported") {
        self.backendName = backendName
    }

    public func readSecret(service: String, account: String) throws -> String {
        throw UnsupportedSecretStoreError.unavailableBackend(
            description: "Credential backend '\(backendName)' is not implemented yet."
        )
    }

    public func storeSecret(_ secret: String, service: String, account: String) throws {
        throw UnsupportedSecretStoreError.unavailableBackend(
            description: "Credential backend '\(backendName)' is not implemented yet."
        )
    }

    public func deleteSecret(service: String, account: String) throws {
        throw UnsupportedSecretStoreError.unavailableBackend(
            description: "Credential backend '\(backendName)' is not implemented yet."
        )
    }

    public func containsSecret(service: String, account: String) -> Bool {
        false
    }
}

/// The canonical creation point for all secret and credential storage backends.
///
/// **Rule:** Always use this factory rather than instantiating `KeychainCredentialStore`
/// or any concrete backend directly. This ensures that when a non-macOS backend is
/// introduced in a later migration phase, callers automatically pick it up without
/// change. Direct instantiation of concrete backends bypasses the platform boundary
/// and is considered a migration violation.
public enum SecretBackendFactory {
    public static func defaultCredentialStore() -> any CredentialStore {
        #if os(macOS)
        return KeychainCredentialStore()
        #else
        return UnsupportedCredentialStore(backendName: "native-secret-backend")
        #endif
    }

    public static func defaultSecretStore() -> any SecretStore {
        #if os(macOS)
        return KeychainCredentialStore()
        #else
        return UnsupportedSecretBackend(backendName: "native-secret-backend")
        #endif
    }
}

public struct KeychainSecretBackend: SecretStore, SecretCacheInvalidating, Sendable {
    public init() {}

    public func getSecret(name: String) throws -> String? {
        try? KeychainSecretStore.readCustomSecret(name: name)
    }

    public func setSecret(name: String, value: String) throws {
        try KeychainSecretStore.storeCustomSecret(name: name, value: value)
    }

    public func deleteSecret(name: String) throws {
        try KeychainSecretStore.deleteCustomSecret(name: name)
    }

    public func hasSecret(name: String) -> Bool {
        (try? !(getSecret(name: name) ?? "").isEmpty) ?? false
    }

    public func listSecretNames() throws -> [String] {
        let bundle = try KeychainSecretStore.readBundle()
        return Array(bundle.customSecrets?.keys ?? [String: String]().keys).sorted()
    }

    public func invalidateSecretCache() {
        KeychainSecretStore.invalidateBundleCache()
    }
}

public protocol CredentialStore: Sendable {
    func readSecret(service: String, account: String) throws -> String
    func storeSecret(_ secret: String, service: String, account: String) throws
    func deleteSecret(service: String, account: String) throws
    func containsSecret(service: String, account: String) -> Bool
}

public struct KeychainCredentialStore: CredentialStore, SecretStore, SecretCacheInvalidating, Sendable {
    public init() {}

    public func readSecret(service: String, account: String) throws -> String {
        try KeychainSecretStore.readSecret(service: service, account: account)
    }

    public func storeSecret(_ secret: String, service: String, account: String) throws {
        try KeychainSecretStore.storeSecret(secret, service: service, account: account)
    }

    public func deleteSecret(service: String, account: String) throws {
        try KeychainSecretStore.deleteSecret(service: service, account: account)
    }

    public func containsSecret(service: String, account: String) -> Bool {
        (try? !readSecret(service: service, account: account).isEmpty) ?? false
    }

    public func getSecret(name: String) throws -> String? {
        try KeychainSecretStore.readCustomSecret(name: name)
    }

    public func setSecret(name: String, value: String) throws {
        try KeychainSecretStore.storeCustomSecret(name: name, value: value)
    }

    public func deleteSecret(name: String) throws {
        try KeychainSecretStore.deleteCustomSecret(name: name)
    }

    public func hasSecret(name: String) -> Bool {
        (try? !(getSecret(name: name) ?? "").isEmpty) ?? false
    }

    public func listSecretNames() throws -> [String] {
        let bundle = try KeychainSecretStore.readBundle()
        return Array(bundle.customSecrets?.keys ?? [String: String]().keys).sorted()
    }

    public func invalidateSecretCache() {
        KeychainSecretStore.invalidateBundleCache()
    }
}
