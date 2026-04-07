import XCTest
@testable import AtlasSkills

// MARK: - ForgeValidationGateTests
//
// Unit tests for ForgeValidationGate Gate 7: auth plan field completeness.
//
// Gate 7 calls `evaluatePlans(_:skillName:)` and checks each HTTPRequestPlan
// whose `authType` is explicitly set for the companion fields required at runtime.
//
// Tests verify:
//  1. apiKeyHeader missing authHeaderName → clarification
//  2. apiKeyHeader missing authSecretKey → clarification
//  3. apiKeyQuery missing authQueryParamName → clarification
//  4. apiKeyQuery missing authSecretKey → clarification
//  5. bearerTokenStatic missing authSecretKey → clarification
//  6. basicAuth missing authSecretKey → clarification
//  7. Complete apiKeyHeader plan → pass
//  8. Complete apiKeyQuery plan → pass
//  9. nil authType → skip (legacy path; always pass)
// 10. none authType → pass (no companion fields needed)
// 11. Multiple plans with missing fields → combined clarification
// 12. All fields present but empty string → clarification

final class ForgeValidationGateTests: XCTestCase {

    private let gate = ForgeValidationGate()
    private let skillName = "Test Skill"

    // MARK: - Helpers

    private func makePlan(
        actionID: String = "test.action",
        method: String = "GET",
        url: String = "https://api.test.com/v1/resource",
        authType: APIAuthType?,
        authSecretKey: String? = nil,
        authHeaderName: String? = nil,
        authQueryParamName: String? = nil
    ) -> ForgeActionPlan {
        let http = HTTPRequestPlan(
            method: method,
            url: url,
            authType: authType,
            authSecretKey: authSecretKey,
            authHeaderName: authHeaderName,
            authQueryParamName: authQueryParamName
        )
        return ForgeActionPlan(actionID: actionID, type: .http, httpRequest: http)
    }

    // MARK: - 1. apiKeyHeader: missing authHeaderName

    func testAPIKeyHeaderMissingHeaderNameReturnsClarification() {
        let plan = makePlan(
            authType: .apiKeyHeader,
            authSecretKey: "com.projectatlas.myapi",
            authHeaderName: nil   // ← missing
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Expected .needsClarification, got \(outcome)"); return
        }
        XCTAssertTrue(q.contains("authHeaderName"),
            "Clarification must mention the missing field. Got: \(q)")
    }

    // MARK: - 2. apiKeyHeader: missing authSecretKey

    func testAPIKeyHeaderMissingSecretKeyReturnsClarification() {
        let plan = makePlan(
            authType: .apiKeyHeader,
            authSecretKey: nil,   // ← missing
            authHeaderName: "X-API-Key"
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Expected .needsClarification, got \(outcome)"); return
        }
        XCTAssertTrue(q.contains("authSecretKey"),
            "Clarification must mention authSecretKey. Got: \(q)")
    }

    // MARK: - 3. apiKeyQuery: missing authQueryParamName

    func testAPIKeyQueryMissingParamNameReturnsClarification() {
        let plan = makePlan(
            authType: .apiKeyQuery,
            authSecretKey: "com.projectatlas.myapi",
            authQueryParamName: nil   // ← missing
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Expected .needsClarification, got \(outcome)"); return
        }
        XCTAssertTrue(q.contains("authQueryParamName"),
            "Clarification must mention authQueryParamName. Got: \(q)")
    }

    // MARK: - 4. apiKeyQuery: missing authSecretKey

    func testAPIKeyQueryMissingSecretKeyReturnsClarification() {
        let plan = makePlan(
            authType: .apiKeyQuery,
            authSecretKey: nil,   // ← missing
            authQueryParamName: "api_key"
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Expected .needsClarification, got \(outcome)"); return
        }
        XCTAssertTrue(q.contains("authSecretKey"),
            "Clarification must mention authSecretKey. Got: \(q)")
    }

    // MARK: - 5. bearerTokenStatic: missing authSecretKey

    func testBearerTokenStaticMissingSecretKeyReturnsClarification() {
        let plan = makePlan(
            authType: .bearerTokenStatic,
            authSecretKey: nil   // ← missing
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Expected .needsClarification, got \(outcome)"); return
        }
        XCTAssertTrue(q.contains("authSecretKey"),
            "Clarification must mention authSecretKey for bearerTokenStatic. Got: \(q)")
    }

    // MARK: - 6. basicAuth: missing authSecretKey

    func testBasicAuthMissingSecretKeyReturnsClarification() {
        let plan = makePlan(
            authType: .basicAuth,
            authSecretKey: nil   // ← missing
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Expected .needsClarification, got \(outcome)"); return
        }
        XCTAssertTrue(q.contains("authSecretKey"),
            "Clarification must mention authSecretKey for basicAuth. Got: \(q)")
    }

    // MARK: - 7. Complete apiKeyHeader plan → pass

    func testCompleteAPIKeyHeaderPlanPasses() {
        let plan = makePlan(
            authType: .apiKeyHeader,
            authSecretKey: "com.projectatlas.myapi",
            authHeaderName: "X-API-Key"
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        if case .pass = outcome { return }
        XCTFail("Complete apiKeyHeader plan must pass Gate 7. Got: \(outcome)")
    }

    // MARK: - 8. Complete apiKeyQuery plan → pass

    func testCompleteAPIKeyQueryPlanPasses() {
        let plan = makePlan(
            authType: .apiKeyQuery,
            authSecretKey: "com.projectatlas.myapi",
            authQueryParamName: "api_key"
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        if case .pass = outcome { return }
        XCTFail("Complete apiKeyQuery plan must pass Gate 7. Got: \(outcome)")
    }

    // MARK: - 9. nil authType → skip gate entirely

    func testNilAuthTypePlanSkipsGate7() {
        let http = HTTPRequestPlan(
            method: "GET",
            url: "https://api.test.com/v1/resource",
            secretHeader: "com.projectatlas.legacy"
            // authType is nil — legacy secretHeader path
        )
        let plan = ForgeActionPlan(actionID: "test.action", type: .http, httpRequest: http)
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        if case .pass = outcome { return }
        XCTFail("nil authType must skip Gate 7 and pass. Got: \(outcome)")
    }

    // MARK: - 10. none authType → no companion fields needed

    func testNoneAuthTypePlanPasses() {
        let plan = makePlan(authType: .none)
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        if case .pass = outcome { return }
        XCTFail("authType .none must pass Gate 7 with no companion fields. Got: \(outcome)")
    }

    // MARK: - 11. Multiple plans with missing fields → combined clarification

    func testMultiplePlansWithMissingFieldsReturnsCombinedClarification() {
        let plan1 = makePlan(
            actionID: "test.action1",
            authType: .apiKeyHeader,
            authSecretKey: "com.projectatlas.myapi",
            authHeaderName: nil   // ← missing
        )
        let plan2 = makePlan(
            actionID: "test.action2",
            authType: .apiKeyQuery,
            authSecretKey: nil,   // ← missing
            authQueryParamName: "api_key"
        )
        let outcome = gate.evaluatePlans([plan1, plan2], skillName: skillName)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Expected .needsClarification for two incomplete plans. Got: \(outcome)"); return
        }
        XCTAssertTrue(q.contains("test.action1"),
            "Clarification must mention action1. Got: \(q)")
        XCTAssertTrue(q.contains("test.action2"),
            "Clarification must mention action2. Got: \(q)")
    }

    // MARK: - 12. Empty string fields treated as missing

    func testEmptyAuthHeaderNameTreatedAsMissing() {
        let plan = makePlan(
            authType: .apiKeyHeader,
            authSecretKey: "com.projectatlas.myapi",
            authHeaderName: "   "   // whitespace only → treated as empty
        )
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        guard case .needsClarification(let q) = outcome else {
            XCTFail("Whitespace-only authHeaderName must be treated as missing. Got: \(outcome)"); return
        }
        XCTAssertTrue(q.contains("authHeaderName"),
            "Clarification must reference the blank field. Got: \(q)")
    }

    // MARK: - 13. Empty plans array → pass

    func testEmptyPlansArrayPasses() {
        let outcome = gate.evaluatePlans([], skillName: skillName)
        if case .pass = outcome { return }
        XCTFail("Empty plans array must pass Gate 7. Got: \(outcome)")
    }

    // MARK: - 14. Plan without httpRequest → skipped

    func testPlanWithNilHTTPRequestSkipped() {
        let plan = ForgeActionPlan(actionID: "test.action", type: .http, httpRequest: nil)
        let outcome = gate.evaluatePlans([plan], skillName: skillName)
        if case .pass = outcome { return }
        XCTFail("Plan with nil httpRequest must pass Gate 7. Got: \(outcome)")
    }
}
