import XCTest
import AtlasShared
@testable import AtlasApp

final class CredentialManagementTests: XCTestCase {
    func testAvailabilityReportsConfiguredMissingAndKeychainErrorSeparately() {
        let store = MockCredentialStore()
        let config = AtlasConfig(
            openAIServiceName: "com.projectatlas.tests.openai",
            openAIAccountName: "default"
        )
        let manager = AtlasCredentialManager(config: config, store: store)

        store.secrets["com.projectatlas.tests.openai|default"] = "sk-test"
        XCTAssertEqual(manager.availability(.openAI), .configured)

        store.secrets.removeAll()
        XCTAssertEqual(manager.availability(.openAI), .missing)

        store.readError = KeychainSecretStoreError.osStatus(errSecInteractionNotAllowed)
        XCTAssertEqual(
            manager.availability(.openAI),
            .keychainError(KeychainSecretStoreError.osStatus(errSecInteractionNotAllowed).localizedDescription)
        )
    }
}

private final class MockCredentialStore: CredentialStore, @unchecked Sendable {
    var secrets: [String: String] = [:]
    var readError: Error?

    func readSecret(service: String, account: String) throws -> String {
        if let readError {
            throw readError
        }
        let key = "\(service)|\(account)"
        guard let secret = secrets[key] else {
            throw KeychainSecretStoreError.secretNotFound(service: service, account: account)
        }
        return secret
    }

    func storeSecret(_ secret: String, service: String, account: String) throws {
        secrets["\(service)|\(account)"] = secret
    }

    func deleteSecret(service: String, account: String) throws {
        secrets.removeValue(forKey: "\(service)|\(account)")
    }

    func containsSecret(service: String, account: String) -> Bool {
        secrets["\(service)|\(account)"]?.isEmpty == false
    }
}
