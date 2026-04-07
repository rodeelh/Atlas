import Foundation
import Security

public enum KeychainSecretStoreError: LocalizedError {
    case secretNotFound(service: String, account: String)
    case invalidSecretEncoding
    case osStatus(OSStatus)

    public var errorDescription: String? {
        switch self {
        case .secretNotFound(let service, let account):
            return "No keychain secret found for service '\(service)' and account '\(account)'."
        case .invalidSecretEncoding:
            return "The keychain secret could not be decoded as UTF-8 text."
        case .osStatus(let status):
            return SecCopyErrorMessageString(status, nil) as String? ?? "Keychain error \(status)."
        }
    }
}

enum KeychainSecurityClient {
    typealias CopyMatching = (CFDictionary, UnsafeMutablePointer<CFTypeRef?>?) -> OSStatus
    typealias Add = (CFDictionary, UnsafeMutablePointer<CFTypeRef?>?) -> OSStatus
    typealias Update = (CFDictionary, CFDictionary) -> OSStatus
    typealias Delete = (CFDictionary) -> OSStatus
    typealias AccessGroupsResolver = () -> [String]

    static var copyMatching: CopyMatching = SecItemCopyMatching
    static var add: Add = SecItemAdd
    static var update: Update = SecItemUpdate
    static var delete: Delete = SecItemDelete
    static var resolveAccessGroups: AccessGroupsResolver = defaultResolveAccessGroups

    private static func defaultResolveAccessGroups() -> [String] {
        guard
            let task = SecTaskCreateFromSelf(nil),
            let value = SecTaskCopyValueForEntitlement(
                task,
                "keychain-access-groups" as CFString,
                nil
            )
        else {
            return []
        }

        if let groups = value as? [String] {
            return groups
        }
        if let groups = value as? NSArray {
            return groups.compactMap { $0 as? String }
        }
        return []
    }

    static func resetForTesting() {
        copyMatching = SecItemCopyMatching
        add = SecItemAdd
        update = SecItemUpdate
        delete = SecItemDelete
        resolveAccessGroups = defaultResolveAccessGroups
        KeychainSecretStore.invalidateBundleCache()
    }
}

/// All Atlas API secrets are stored as a single JSON keychain entry shared by
/// AtlasApp and AtlasRuntimeService through a common keychain access group when
/// the binaries are properly team-signed. Unsigned dev builds fall back to the
/// legacy non-access-group item so Xcode workflows still function locally.
public enum KeychainSecretStore {

    // MARK: – Bundle constants

    private static let bundleService = "com.projectatlas.credentials"
    private static let bundleAccount = "bundle"
    private static let sharedAccessGroupSuffix = ".com.projectatlas.shared"

    // MARK: – Per-process bundle cache
    //
    // One SecItemCopyMatching per process lifetime; invalidated on every write.
    // Both AtlasApp and AtlasRuntimeService maintain independent caches that stay
    // in sync because every credential mutation goes through storeBundle/deleteBundle.

    private static let cacheLock = NSLock()
    private static var _cachedBundle: AtlasCredentialBundle?

    private static var cachedBundle: AtlasCredentialBundle? {
        get { cacheLock.withLock { _cachedBundle } }
        set { cacheLock.withLock { _cachedBundle = newValue } }
    }

    public static func invalidateBundleCache() {
        cachedBundle = nil
    }

    // MARK: – Service → KeyPath routing
    //
    // Adding a new integration = add one entry here + one field in AtlasCredentialBundle.

    private static let keyPathForService: [String: WritableKeyPath<AtlasCredentialBundle, String?>] = [
        "com.projectatlas.openai": \.openAIAPIKey,
        "com.projectatlas.telegram": \.telegramBotToken,
        "com.projectatlas.discord": \.discordBotToken,
        "com.projectatlas.slack.bot": \.slackBotToken,
        "com.projectatlas.slack.app": \.slackAppToken,
        "com.projectatlas.image.openai": \.openAIImageAPIKey,
        "com.projectatlas.image.google": \.googleImageAPIKey,
        "com.projectatlas.search.brave": \.braveSearchAPIKey,
        "com.projectatlas.finnhub": \.finnhubAPIKey,
        "com.projectatlas.alpha-vantage": \.alphaVantageAPIKey,
        "com.projectatlas.anthropic": \.anthropicAPIKey,
        "com.projectatlas.gemini": \.geminiAPIKey,
        "com.projectatlas.lmstudio": \.lmStudioAPIKey,
        "com.projectatlas.remote-access": \.remoteAccessAPIKey
    ]

    // MARK: – Public API (signatures unchanged; all callers work without modification)

    public static func readSecret(service: String, account: String) throws -> String {
        guard let keyPath = keyPathForService[service] else {
            throw KeychainSecretStoreError.secretNotFound(service: service, account: account)
        }
        let bundle = try readBundle()
        guard let value = bundle[keyPath: keyPath], !value.isEmpty else {
            throw KeychainSecretStoreError.secretNotFound(service: service, account: account)
        }
        return value
    }

    public static func storeSecret(_ secret: String, service: String, account: String) throws {
        guard let keyPath = keyPathForService[service] else { return }
        // SAFETY: readBundle() returns an empty bundle (not throw) when no entry exists yet
        // (first-time setup). It only throws on real Keychain errors. Propagate those errors
        // rather than falling back to an empty bundle — storing empty would wipe all credentials.
        var bundle = try readBundle()
        bundle[keyPath: keyPath] = secret
        try storeBundle(bundle)
    }

    public static func deleteSecret(service: String, account: String) throws {
        guard let keyPath = keyPathForService[service] else { return }
        // SAFETY: same as storeSecret — propagate read errors rather than overwriting with
        // an empty bundle, which would silently destroy all other stored credentials.
        var bundle = try readBundle()
        bundle[keyPath: keyPath] = nil
        try storeBundle(bundle)
    }

    // MARK: – Custom secrets (dynamic, user-defined)

    public static func readCustomSecret(name: String) throws -> String? {
        let bundle = try readBundle()
        return bundle.customSecrets?[name]
    }

    public static func storeCustomSecret(name: String, value: String) throws {
        var bundle = try readBundle()
        var secrets = bundle.customSecrets ?? [:]
        secrets[name] = value
        bundle.customSecrets = secrets
        try storeBundle(bundle)
    }

    public static func deleteCustomSecret(name: String) throws {
        var bundle = try readBundle()
        bundle.customSecrets?[name] = nil
        try storeBundle(bundle)
    }

    // MARK: – Bundle read / write

    /// Reads the single JSON credential bundle from the keychain.
    /// Returns an empty bundle (all nils) if no entry exists yet.
    /// Result is cached in-process; subsequent calls are free until the next write.
    public static func readBundle() throws -> AtlasCredentialBundle {
        if let cached = cachedBundle { return cached }

        let bundle: AtlasCredentialBundle

        if let accessGroup = sharedAccessGroup(),
           let shared = try readBundle(accessGroup: accessGroup) {
            bundle = shared
        } else if let legacyBundle = try readBundle(accessGroup: nil) {
            // Migrate legacy (no-access-group) item into the shared group.
            if let accessGroup = sharedAccessGroup() {
                do {
                    try storeBundle(legacyBundle, accessGroup: accessGroup)
                    try deleteBundle(accessGroup: nil)
                } catch {
                    // Preserve read compatibility for existing installs even if a
                    // signing or entitlement issue prevents the write migration.
                }
            }
            bundle = legacyBundle
        } else {
            bundle = AtlasCredentialBundle()
        }

        cachedBundle = bundle
        return bundle
    }

    public static func storeBundle(_ bundle: AtlasCredentialBundle) throws {
        cachedBundle = nil  // invalidate before write so any thrown error leaves cache clear
        if let accessGroup = sharedAccessGroup() {
            try storeBundle(bundle, accessGroup: accessGroup)
        } else {
            try storeBundle(bundle, accessGroup: nil)
        }
        cachedBundle = bundle  // repopulate with the just-written value
    }

    private static func storeBundle(
        _ bundle: AtlasCredentialBundle,
        accessGroup: String?
    ) throws {
        let data = try JSONEncoder().encode(bundle)
        let query = bundleQuery(accessGroup: accessGroup)
        // Include kSecAttrAccessible on every update so existing items are normalised
        // to AfterFirstUnlock without requiring a delete-then-add cycle.
        let attributes: [String: Any] = [
            kSecAttrLabel as String: "Atlas API Credentials",
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlock,
            kSecValueData as String: data
        ]

        let updateStatus = KeychainSecurityClient.update(
            query as CFDictionary,
            attributes as CFDictionary
        )

        switch updateStatus {
        case errSecSuccess:
            return
        case errSecItemNotFound:
            var addQuery = query
            addQuery[kSecAttrLabel as String] = "Atlas API Credentials"
            addQuery[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
            addQuery[kSecValueData as String] = data

            let addStatus = KeychainSecurityClient.add(addQuery as CFDictionary, nil)
            guard addStatus == errSecSuccess else {
                throw KeychainSecretStoreError.osStatus(addStatus)
            }
        default:
            throw KeychainSecretStoreError.osStatus(updateStatus)
        }
    }

    // MARK: – One-time migration from legacy individual items

    /// Call once at app launch (e.g. from AtlasAppState.init).
    /// Reads each pre-bundle keychain item, merges them into the JSON bundle, then
    /// deletes the individual entries. Subsequent launches are no-ops.
    public static func migrateFromLegacyItemsIfNeeded() {
        let seededBundle = (try? readBundle()) ?? AtlasCredentialBundle()
        if bundleContainsAnySecrets(seededBundle) {
            // Bundle already exists and has data — nothing to migrate.
            // Accessibility normalisation (kSecAttrAccessibleAfterFirstUnlock) is now
            // applied by storeBundle(_:accessGroup:) on every write, so there is no need
            // for the old delete-then-add pattern here.  That pattern was the root cause
            // of data loss: a failed add after a successful delete left the bundle absent.
            return
        }

        let legacyPairs: [(String, WritableKeyPath<AtlasCredentialBundle, String?>)] = [
            ("com.projectatlas.openai", \.openAIAPIKey),
            ("com.projectatlas.telegram", \.telegramBotToken),
            ("com.projectatlas.discord", \.discordBotToken),
            ("com.projectatlas.slack.bot", \.slackBotToken),
            ("com.projectatlas.slack.app", \.slackAppToken),
            ("com.projectatlas.image.openai", \.openAIImageAPIKey),
            ("com.projectatlas.image.google", \.googleImageAPIKey),
            ("com.projectatlas.search.brave", \.braveSearchAPIKey),
            ("com.projectatlas.finnhub", \.finnhubAPIKey),
            ("com.projectatlas.alpha-vantage", \.alphaVantageAPIKey)
        ]

        var bundle = AtlasCredentialBundle()
        var foundAny = false

        for (service, kp) in legacyPairs {
            if let value = readLegacyItem(service: service, account: "default") {
                bundle[keyPath: kp] = value
                foundAny = true
            }
        }

        guard foundAny else { return }

        do {
            try storeBundle(bundle)
            for (service, _) in legacyPairs {
                deleteLegacyItem(service: service, account: "default")
            }
        } catch {
            // Non-fatal: legacy items remain usable until next launch.
        }
    }

    // MARK: – Private helpers

    private static func readLegacyItem(service: String, account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecMatchLimit as String: kSecMatchLimitOne,
            kSecReturnData as String: kCFBooleanTrue as Any
        ]
        var result: CFTypeRef?
        guard KeychainSecurityClient.copyMatching(query as CFDictionary, &result) == errSecSuccess,
              let data = result as? Data,
              let str = String(data: data, encoding: .utf8),
              !str.isEmpty
        else { return nil }
        return str
    }

    private static func deleteLegacyItem(service: String, account: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
        _ = KeychainSecurityClient.delete(query as CFDictionary)
    }

    private static func bundleQuery(accessGroup: String?) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: bundleService,
            kSecAttrAccount as String: bundleAccount
        ]
        if let accessGroup {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        return query
    }

    private static func readBundle(accessGroup: String?) throws -> AtlasCredentialBundle? {
        var query = bundleQuery(accessGroup: accessGroup)
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        query[kSecReturnData as String] = kCFBooleanTrue as Any

        var result: CFTypeRef?
        let status = KeychainSecurityClient.copyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess, let data = result as? Data else {
            throw KeychainSecretStoreError.osStatus(status)
        }
        return try JSONDecoder().decode(AtlasCredentialBundle.self, from: data)
    }

    private static func deleteBundle(accessGroup: String?) throws {
        let status = KeychainSecurityClient.delete(bundleQuery(accessGroup: accessGroup) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainSecretStoreError.osStatus(status)
        }
    }

    private static func bundleContainsAnySecrets(_ bundle: AtlasCredentialBundle) -> Bool {
        let keyValues: [String?] = [
            bundle.openAIAPIKey,
            bundle.telegramBotToken,
            bundle.discordBotToken,
            bundle.slackBotToken,
            bundle.slackAppToken,
            bundle.openAIImageAPIKey,
            bundle.googleImageAPIKey,
            bundle.braveSearchAPIKey,
            bundle.finnhubAPIKey,
            bundle.alphaVantageAPIKey,
            bundle.anthropicAPIKey,
            bundle.geminiAPIKey,
            bundle.lmStudioAPIKey,
            bundle.remoteAccessAPIKey
        ]
        // Note: third-party integrations (e.g. TrackingMore) live in customSecrets, not as
        // first-class fields. Only the providers above are ever first-class bundle fields.
        return keyValues.contains { $0?.isEmpty == false } ||
            (bundle.customSecrets?.isEmpty == false)
    }

    private static func sharedAccessGroup() -> String? {
        let groups = KeychainSecurityClient.resolveAccessGroups()
        // Only return the explicitly shared group — never fall back to groups.first.
        // Falling back to an arbitrary access group causes the bundle to land in an
        // inconsistent keychain slot across builds and processes, which makes readBundle()
        // miss the item and return an empty bundle, causing storeSecret to overwrite all
        // credentials with a single-key bundle.
        return groups.first(where: { $0.hasSuffix(sharedAccessGroupSuffix) })
    }
}
