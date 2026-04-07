import XCTest
@testable import AtlasShared

final class KeychainSecretStoreTests: XCTestCase {
    override func setUp() {
        super.setUp()
        KeychainSecurityClient.resetForTesting()
    }

    override func tearDown() {
        KeychainSecurityClient.resetForTesting()
        super.tearDown()
    }

    func testStoreReadAndDeleteBundleThroughSharedAccessGroup() throws {
        let keychain = MockKeychain()
        installMockKeychain(keychain, accessGroups: ["TEAMID.com.projectatlas.shared"])

        try KeychainSecretStore.storeSecret("sk-test", service: "com.projectatlas.openai", account: "default")

        XCTAssertEqual(
            try KeychainSecretStore.readSecret(service: "com.projectatlas.openai", account: "default"),
            "sk-test"
        )
        XCTAssertNotNil(
            keychain.item(
                service: "com.projectatlas.credentials",
                account: "bundle",
                accessGroup: "TEAMID.com.projectatlas.shared"
            )
        )

        try KeychainSecretStore.deleteSecret(service: "com.projectatlas.openai", account: "default")

        XCTAssertThrowsError(
            try KeychainSecretStore.readSecret(service: "com.projectatlas.openai", account: "default")
        )
    }

    func testReadBundleMigratesLegacyBundleIntoSharedAccessGroup() throws {
        let keychain = MockKeychain()
        let legacyBundle = AtlasCredentialBundle(openAIAPIKey: "legacy-openai", braveSearchAPIKey: "legacy-brave")
        keychain.insertBundle(legacyBundle, service: "com.projectatlas.credentials", account: "bundle", accessGroup: nil)
        installMockKeychain(keychain, accessGroups: ["TEAMID.com.projectatlas.shared"])

        let loaded = try KeychainSecretStore.readBundle()

        XCTAssertEqual(loaded.openAIAPIKey, "legacy-openai")
        XCTAssertNil(keychain.item(service: "com.projectatlas.credentials", account: "bundle", accessGroup: nil))
        XCTAssertNotNil(
            keychain.item(
                service: "com.projectatlas.credentials",
                account: "bundle",
                accessGroup: "TEAMID.com.projectatlas.shared"
            )
        )
    }

    func testMigrateLegacyItemsIfNeededMovesIndividualSecretsIntoSharedBundle() throws {
        let keychain = MockKeychain()
        keychain.insertString("legacy-openai", service: "com.projectatlas.openai", account: "default", accessGroup: nil)
        keychain.insertString("legacy-finnhub", service: "com.projectatlas.finnhub", account: "default", accessGroup: nil)
        installMockKeychain(keychain, accessGroups: ["TEAMID.com.projectatlas.shared"])

        KeychainSecretStore.migrateFromLegacyItemsIfNeeded()

        let bundle = try XCTUnwrap(
            keychain.bundle(
                service: "com.projectatlas.credentials",
                account: "bundle",
                accessGroup: "TEAMID.com.projectatlas.shared"
            )
        )
        XCTAssertEqual(bundle.openAIAPIKey, "legacy-openai")
        XCTAssertEqual(bundle.finnhubAPIKey, "legacy-finnhub")
        XCTAssertNil(keychain.item(service: "com.projectatlas.openai", account: "default", accessGroup: nil))
        XCTAssertNil(keychain.item(service: "com.projectatlas.finnhub", account: "default", accessGroup: nil))
    }

    func testReadBundleReturnsEmptyWhenNoCredentialsExist() throws {
        let keychain = MockKeychain()
        installMockKeychain(keychain, accessGroups: ["TEAMID.com.projectatlas.shared"])

        let bundle = try KeychainSecretStore.readBundle()

        XCTAssertNil(bundle.openAIAPIKey)
        XCTAssertNil(bundle.customSecrets)
    }

    func testReadSecretPropagatesKeychainFailuresInsteadOfTreatingThemAsMissing() {
        KeychainSecurityClient.copyMatching = { _, _ in errSecInteractionNotAllowed }
        KeychainSecurityClient.resolveAccessGroups = { ["TEAMID.com.projectatlas.shared"] }

        XCTAssertThrowsError(
            try KeychainSecretStore.readSecret(service: "com.projectatlas.openai", account: "default")
        ) { error in
            guard case KeychainSecretStoreError.osStatus(let status) = error else {
                return XCTFail("Expected osStatus error, got \(error)")
            }
            XCTAssertEqual(status, errSecInteractionNotAllowed)
        }
    }

    func testCustomSecretPersistsInsideBundle() throws {
        let keychain = MockKeychain()
        installMockKeychain(keychain, accessGroups: ["TEAMID.com.projectatlas.shared"])

        try KeychainSecretStore.storeCustomSecret(name: "com.projectatlas.trackingmore", value: "abc123")

        XCTAssertEqual(
            try KeychainSecretStore.readCustomSecret(name: "com.projectatlas.trackingmore"),
            "abc123"
        )
        let bundle = try XCTUnwrap(
            keychain.bundle(
                service: "com.projectatlas.credentials",
                account: "bundle",
                accessGroup: "TEAMID.com.projectatlas.shared"
            )
        )
        XCTAssertEqual(bundle.customSecrets?["com.projectatlas.trackingmore"], "abc123")
    }

    func testStoreReadAndDeleteSlackSecretsThroughSharedBundle() throws {
        let keychain = MockKeychain()
        installMockKeychain(keychain, accessGroups: ["TEAMID.com.projectatlas.shared"])

        try KeychainSecretStore.storeSecret("xoxb-test", service: "com.projectatlas.slack.bot", account: "default")
        try KeychainSecretStore.storeSecret("xapp-test", service: "com.projectatlas.slack.app", account: "default")

        XCTAssertEqual(
            try KeychainSecretStore.readSecret(service: "com.projectatlas.slack.bot", account: "default"),
            "xoxb-test"
        )
        XCTAssertEqual(
            try KeychainSecretStore.readSecret(service: "com.projectatlas.slack.app", account: "default"),
            "xapp-test"
        )

        let bundle = try XCTUnwrap(
            keychain.bundle(
                service: "com.projectatlas.credentials",
                account: "bundle",
                accessGroup: "TEAMID.com.projectatlas.shared"
            )
        )
        XCTAssertEqual(bundle.slackBotToken, "xoxb-test")
        XCTAssertEqual(bundle.slackAppToken, "xapp-test")

        try KeychainSecretStore.deleteSecret(service: "com.projectatlas.slack.bot", account: "default")
        try KeychainSecretStore.deleteSecret(service: "com.projectatlas.slack.app", account: "default")

        XCTAssertThrowsError(
            try KeychainSecretStore.readSecret(service: "com.projectatlas.slack.bot", account: "default")
        )
        XCTAssertThrowsError(
            try KeychainSecretStore.readSecret(service: "com.projectatlas.slack.app", account: "default")
        )
    }

    func testMigrateLegacyItemsIfNeededMovesSlackSecretsIntoSharedBundle() throws {
        let keychain = MockKeychain()
        keychain.insertString("legacy-bot", service: "com.projectatlas.slack.bot", account: "default", accessGroup: nil)
        keychain.insertString("legacy-app", service: "com.projectatlas.slack.app", account: "default", accessGroup: nil)
        installMockKeychain(keychain, accessGroups: ["TEAMID.com.projectatlas.shared"])

        KeychainSecretStore.migrateFromLegacyItemsIfNeeded()

        let bundle = try XCTUnwrap(
            keychain.bundle(
                service: "com.projectatlas.credentials",
                account: "bundle",
                accessGroup: "TEAMID.com.projectatlas.shared"
            )
        )
        XCTAssertEqual(bundle.slackBotToken, "legacy-bot")
        XCTAssertEqual(bundle.slackAppToken, "legacy-app")
        XCTAssertNil(keychain.item(service: "com.projectatlas.slack.bot", account: "default", accessGroup: nil))
        XCTAssertNil(keychain.item(service: "com.projectatlas.slack.app", account: "default", accessGroup: nil))
    }

    private func installMockKeychain(_ keychain: MockKeychain, accessGroups: [String]) {
        KeychainSecurityClient.copyMatching = keychain.copyMatching
        KeychainSecurityClient.add = keychain.add
        KeychainSecurityClient.update = keychain.update
        KeychainSecurityClient.delete = keychain.delete
        KeychainSecurityClient.resolveAccessGroups = { accessGroups }
    }
}

private final class MockKeychain {
    struct ItemKey: Hashable {
        let service: String
        let account: String
        let accessGroup: String?
    }

    private var items: [ItemKey: Data] = [:]

    func insertString(_ value: String, service: String, account: String, accessGroup: String?) {
        items[ItemKey(service: service, account: account, accessGroup: accessGroup)] = Data(value.utf8)
    }

    func insertBundle(_ bundle: AtlasCredentialBundle, service: String, account: String, accessGroup: String?) {
        items[ItemKey(service: service, account: account, accessGroup: accessGroup)] = try? JSONEncoder().encode(bundle)
    }

    func item(service: String, account: String, accessGroup: String?) -> Data? {
        items[ItemKey(service: service, account: account, accessGroup: accessGroup)]
    }

    func bundle(service: String, account: String, accessGroup: String?) -> AtlasCredentialBundle? {
        guard let data = item(service: service, account: account, accessGroup: accessGroup) else {
            return nil
        }
        return try? JSONDecoder().decode(AtlasCredentialBundle.self, from: data)
    }

    func copyMatching(_ query: CFDictionary, _ result: UnsafeMutablePointer<CFTypeRef?>?) -> OSStatus {
        let key = keyFromQuery(query)
        guard let data = items[key] else {
            return errSecItemNotFound
        }
        result?.pointee = data as CFData
        return errSecSuccess
    }

    func add(_ attributes: CFDictionary, _ result: UnsafeMutablePointer<CFTypeRef?>?) -> OSStatus {
        let key = keyFromQuery(attributes)
        guard items[key] == nil else {
            return errSecDuplicateItem
        }
        guard let data = dataFromDictionary(attributes) else {
            return errSecParam
        }
        items[key] = data
        result?.pointee = nil
        return errSecSuccess
    }

    func update(_ query: CFDictionary, _ attributes: CFDictionary) -> OSStatus {
        let key = keyFromQuery(query)
        guard items[key] != nil else {
            return errSecItemNotFound
        }
        guard let data = dataFromDictionary(attributes) else {
            return errSecParam
        }
        items[key] = data
        return errSecSuccess
    }

    func delete(_ query: CFDictionary) -> OSStatus {
        let key = keyFromQuery(query)
        guard items.removeValue(forKey: key) != nil else {
            return errSecItemNotFound
        }
        return errSecSuccess
    }

    private func keyFromQuery(_ query: CFDictionary) -> ItemKey {
        let dictionary = query as NSDictionary
        return ItemKey(
            service: dictionary[kSecAttrService as String] as? String ?? "",
            account: dictionary[kSecAttrAccount as String] as? String ?? "",
            accessGroup: dictionary[kSecAttrAccessGroup as String] as? String
        )
    }

    private func dataFromDictionary(_ query: CFDictionary) -> Data? {
        let dictionary = query as NSDictionary
        return dictionary[kSecValueData as String] as? Data
    }
}
