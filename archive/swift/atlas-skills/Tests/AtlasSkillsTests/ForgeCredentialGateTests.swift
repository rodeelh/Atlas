import XCTest
@testable import AtlasSkills

// MARK: - ForgeCredentialGateTests
//
// Unit tests for ForgeCredentialGate (Gate 8): credential readiness.
//
// Gate 8 verifies that every Keychain credential declared in an action plan's
// `authSecretKey` actually exists and is non-empty before a proposal is created.
//
// Tests verify:
//  1. Missing credential → clarification
//  2. Empty credential → clarification
//  3. Present non-empty credential → pass
//  4. authType == none → skip Gate 8
//  5. nil authType → skip Gate 8
//  6. Plan with no authSecretKey → skip (Gate 7 already caught this)
//  7. Multiple plans — one missing → clarification names the action

final class ForgeCredentialGateTests: XCTestCase {

    private let gate = ForgeCredentialGate()
    private let skillName = "Test API Skill"

    // MARK: - Helpers

    private func makeSecrets(
        returning value: String? = nil,
        throwing error: Error? = nil
    ) -> CoreSecretsService {
        CoreSecretsService(reader: { _ in
            if let error { throw error }
            return value
        })
    }

    private func makePlan(
        actionID: String = "test.action",
        authType: APIAuthType,
        authSecretKey: String? = "com.projectatlas.myapi"
    ) -> ForgeActionPlan {
        let http = HTTPRequestPlan(
            method: "GET",
            url: "https://api.test.com/v1/resource",
            authType: authType,
            authSecretKey: authSecretKey
        )
        return ForgeActionPlan(actionID: actionID, type: .http, httpRequest: http)
    }

    // MARK: - 1. Missing credential → clarification

    func testMissingCredentialReturnsClarification() async {
        let plan = makePlan(authType: .apiKeyHeader, authSecretKey: "com.projectatlas.myapi")
        let secrets = makeSecrets(returning: nil) // credential not configured
        let outcome = await gate.evaluate(plans: [plan], skillName: skillName, secrets: secrets)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Missing credential must return .needsClarification. Got: \(outcome)"); return
        }
        XCTAssertTrue(q.contains("com.projectatlas.myapi"),
            "Clarification must name the missing credential. Got: \(q)")
    }

    // MARK: - 2. Empty credential → clarification

    func testEmptyCredentialReturnsClarification() async {
        let plan = makePlan(authType: .bearerTokenStatic, authSecretKey: "com.projectatlas.token")
        let secrets = makeSecrets(returning: "") // present but empty
        let outcome = await gate.evaluate(plans: [plan], skillName: skillName, secrets: secrets)
        guard case .needsClarification = outcome else {
            XCTFail("Empty credential must return .needsClarification. Got: \(outcome)"); return
        }
    }

    // MARK: - 3. Present non-empty credential → pass

    func testPresentCredentialPasses() async {
        let plan = makePlan(authType: .apiKeyHeader, authSecretKey: "com.projectatlas.myapi")
        let secrets = makeSecrets(returning: "super-secret-key-value")
        let outcome = await gate.evaluate(plans: [plan], skillName: skillName, secrets: secrets)
        if case .pass = outcome { return }
        XCTFail("Present credential must pass Gate 8. Got: \(outcome)")
    }

    // MARK: - 4. authType == .none → skip

    func testNoneAuthTypeSkipsGate8() async {
        let plan = makePlan(authType: .none, authSecretKey: nil)
        let secrets = makeSecrets(returning: nil) // would fail if checked
        let outcome = await gate.evaluate(plans: [plan], skillName: skillName, secrets: secrets)
        if case .pass = outcome { return }
        XCTFail("authType .none must skip Gate 8. Got: \(outcome)")
    }

    // MARK: - 5. nil authType → skip

    func testNilAuthTypeSkipsGate8() async {
        let http = HTTPRequestPlan(
            method: "GET",
            url: "https://api.test.com/v1/resource",
            secretHeader: "com.projectatlas.legacy"
            // authType nil — legacy path
        )
        let plan = ForgeActionPlan(actionID: "test.action", type: .http, httpRequest: http)
        let secrets = makeSecrets(returning: nil) // would fail if checked
        let outcome = await gate.evaluate(plans: [plan], skillName: skillName, secrets: secrets)
        if case .pass = outcome { return }
        XCTFail("nil authType must skip Gate 8. Got: \(outcome)")
    }

    // MARK: - 6. No authSecretKey → skip (Gate 7 already caught this)

    func testNoAuthSecretKeySkipsGate8() async {
        let plan = makePlan(authType: .apiKeyHeader, authSecretKey: nil)
        let secrets = makeSecrets(returning: nil)
        let outcome = await gate.evaluate(plans: [plan], skillName: skillName, secrets: secrets)
        // Gate 7 should have caught the missing authSecretKey. Gate 8 skips it.
        if case .pass = outcome { return }
        XCTFail("Plan with nil authSecretKey must be skipped by Gate 8. Got: \(outcome)")
    }

    // MARK: - 7. Multiple plans — one missing → clarification names the action

    func testMultiplePlansOneMissingNamesClarification() async {
        let plan1 = makePlan(
            actionID: "test.action1",
            authType: .apiKeyHeader,
            authSecretKey: "com.projectatlas.present"
        )
        let plan2 = makePlan(
            actionID: "test.action2",
            authType: .bearerTokenStatic,
            authSecretKey: "com.projectatlas.missing"
        )
        let secrets = CoreSecretsService(reader: { service in
            return service == "com.projectatlas.present" ? "value" : nil
        })
        let outcome = await gate.evaluate(plans: [plan1, plan2], skillName: skillName, secrets: secrets)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("One missing credential must return .needsClarification. Got: \(outcome)"); return
        }
        XCTAssertTrue(q.contains("test.action2"),
            "Clarification must name the failing action. Got: \(q)")
        XCTAssertFalse(q.contains("test.action1"),
            "Clarification must not mention the passing action. Got: \(q)")
    }

    // MARK: - 8. Keychain read error → treated as missing

    func testKeychainReadErrorTreatedAsMissing() async {
        struct KeychainFailure: Error {}
        let plan = makePlan(authType: .basicAuth, authSecretKey: "com.projectatlas.myapi")
        let secrets = makeSecrets(throwing: KeychainFailure())
        let outcome = await gate.evaluate(plans: [plan], skillName: skillName, secrets: secrets)
        // validate() catches errors and returns isPresent: false → Gate 8 fires
        guard case .needsClarification = outcome else {
            XCTFail("Keychain error must be treated as missing credential. Got: \(outcome)"); return
        }
    }

    // MARK: - 9. All credential-requiring types are checked

    func testAllCredentialTypesAreCheckedByGate8() async {
        let credentialTypes: [APIAuthType] = [.apiKeyHeader, .apiKeyQuery, .bearerTokenStatic, .basicAuth]
        for authType in credentialTypes {
            let plan = makePlan(authType: authType, authSecretKey: "com.projectatlas.missing")
            let secrets = makeSecrets(returning: nil)
            let outcome = await gate.evaluate(plans: [plan], skillName: skillName, secrets: secrets)
            guard case .needsClarification = outcome else {
                XCTFail("authType '\(authType.rawValue)' must trigger Gate 8 when credential is missing. Got: \(outcome)")
                return
            }
        }
    }
}
