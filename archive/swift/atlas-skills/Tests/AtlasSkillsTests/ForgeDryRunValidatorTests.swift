import XCTest
@testable import AtlasSkills

// MARK: - ForgeDryRunValidatorTests
//
// Unit tests for ForgeDryRunValidator: auth-aware pre-proposal HTTP validation.
//
// All tests use the injectable mock executor — no live network calls are made.
//
// Tests verify:
//  1. 200 response → pass
//  2. Network error (DNS failure) → fail
//  3. 5xx response → fail
//  4. 401 with credential injected → pass
//  5. 401 without credential → fail
//  6. POST plan → skipped (no HTTP call)
//  7. 4xx (non-auth) → pass (API is reachable; test values wrong)
//  8. Timeout → fail
//  9. Invalid URL → fail
// 10. No GET plans → skipped
// 11. Path substitution with placeholder "test"
// 12. 403 with credential → pass
// 13. 403 without credential → fail

final class ForgeDryRunValidatorTests: XCTestCase {

    // MARK: - Helpers

    private func makeValidator(
        response: CoreHTTPResponse? = nil,
        error: Error? = nil,
        secretsReader: @escaping CoreSecretsService.SecretsReader = { _ in nil }
    ) -> ForgeDryRunValidator {
        let secrets = CoreSecretsService(reader: secretsReader)
        return ForgeDryRunValidator(
            secretsService: secrets,
            executor: { _ in
                if let error { throw error }
                return response ?? CoreHTTPResponse(
                    statusCode: 200,
                    headers: [:],
                    body: Data(),
                    url: URL(string: "https://api.test.com")!
                )
            }
        )
    }

    private func makeResponse(statusCode: Int) -> CoreHTTPResponse {
        CoreHTTPResponse(
            statusCode: statusCode,
            headers: [:],
            body: Data(),
            url: URL(string: "https://api.test.com")!
        )
    }

    private func makeGetPlan(
        actionID: String = "test.get",
        url: String = "https://api.test.com/v1/resource",
        authType: APIAuthType? = nil,
        authSecretKey: String? = nil,
        authHeaderName: String? = nil,
        authQueryParamName: String? = nil
    ) -> ForgeActionPlan {
        let http = HTTPRequestPlan(
            method: "GET",
            url: url,
            authType: authType,
            authSecretKey: authSecretKey,
            authHeaderName: authHeaderName,
            authQueryParamName: authQueryParamName
        )
        return ForgeActionPlan(actionID: actionID, type: .http, httpRequest: http)
    }

    private func makePostPlan(
        actionID: String = "test.post",
        url: String = "https://api.test.com/v1/resource"
    ) -> ForgeActionPlan {
        let http = HTTPRequestPlan(method: "POST", url: url)
        return ForgeActionPlan(actionID: actionID, type: .http, httpRequest: http)
    }

    // MARK: - 1. 200 response → pass

    func test200ResponsePasses() async {
        let validator = makeValidator(response: makeResponse(statusCode: 200))
        let plan = makeGetPlan()
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        XCTAssertEqual(outcome, .pass, "HTTP 200 must pass the dry-run.")
    }

    // MARK: - 2. Network error → fail

    func testNetworkErrorFails() async {
        let validator = makeValidator(error: CoreHTTPError.networkError("connection refused"))
        let plan = makeGetPlan()
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        guard case .fail(let reason) = outcome else {
            XCTFail("Network error must fail. Got: \(outcome)"); return
        }
        XCTAssertTrue(reason.lowercased().contains("network") || reason.lowercased().contains("connection"),
            "Failure reason must mention network problem. Got: \(reason)")
    }

    // MARK: - 3. 5xx response → fail

    func test500ResponseFails() async {
        let validator = makeValidator(response: makeResponse(statusCode: 500))
        let plan = makeGetPlan()
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        guard case .fail(let reason) = outcome else {
            XCTFail("HTTP 500 must fail. Got: \(outcome)"); return
        }
        XCTAssertTrue(reason.contains("500"),
            "Failure reason must mention the status code. Got: \(reason)")
    }

    // MARK: - 4. 401 with credential injected → pass

    func test401WithCredentialPasses() async {
        let validator = ForgeDryRunValidator(
            secretsService: CoreSecretsService(reader: { _ in "my-api-key" }),
            executor: { _ in self.makeResponse(statusCode: 401) }
        )
        let plan = makeGetPlan(
            authType: .apiKeyHeader,
            authSecretKey: "com.projectatlas.myapi",
            authHeaderName: "X-API-Key"
        )
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        XCTAssertEqual(outcome, .pass,
            "HTTP 401 with injected credential must pass (API is reachable, auth configured).")
    }

    // MARK: - 5. 401 without credential → fail

    func test401WithoutCredentialFails() async {
        // No authType set, no secretHeader — no credential will be injected
        let validator = makeValidator(response: makeResponse(statusCode: 401))
        let plan = makeGetPlan(authType: .none) // none → no credential injection
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        guard case .fail(let reason) = outcome else {
            XCTFail("HTTP 401 without credential must fail. Got: \(outcome)"); return
        }
        XCTAssertTrue(reason.contains("401"),
            "Failure reason must reference the HTTP status. Got: \(reason)")
    }

    // MARK: - 6. POST plan → skipped (no HTTP call made)

    func testPostPlanIsSkipped() async {
        var httpCallMade = false
        let secrets = CoreSecretsService(reader: { _ in nil })
        let validator = ForgeDryRunValidator(
            secretsService: secrets,
            executor: { _ in
                httpCallMade = true
                return self.makeResponse(statusCode: 200)
            }
        )
        let plan = makePostPlan()
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        if case .skipped = outcome {
            XCTAssertFalse(httpCallMade, "No HTTP call must be made for POST plans.")
            return
        }
        XCTFail("POST plan must return .skipped. Got: \(outcome)")
    }

    // MARK: - 7. 4xx (non-auth) → pass

    func test404ResponsePasses() async {
        let validator = makeValidator(response: makeResponse(statusCode: 404))
        let plan = makeGetPlan()
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        XCTAssertEqual(outcome, .pass,
            "HTTP 404 must pass (API is reachable; placeholder test values were invalid).")
    }

    func test400ResponsePasses() async {
        let validator = makeValidator(response: makeResponse(statusCode: 400))
        let plan = makeGetPlan()
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        XCTAssertEqual(outcome, .pass, "HTTP 400 must pass (API responded; bad request from test values).")
    }

    func test422ResponsePasses() async {
        let validator = makeValidator(response: makeResponse(statusCode: 422))
        let plan = makeGetPlan()
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        XCTAssertEqual(outcome, .pass, "HTTP 422 must pass (API responded).")
    }

    // MARK: - 8. Timeout → fail

    func testTimeoutFails() async {
        let validator = makeValidator(error: CoreHTTPError.timeout)
        let plan = makeGetPlan()
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        guard case .fail(let reason) = outcome else {
            XCTFail("Timeout must fail. Got: \(outcome)"); return
        }
        XCTAssertTrue(reason.lowercased().contains("timed out") || reason.lowercased().contains("timeout"),
            "Failure reason must mention timeout. Got: \(reason)")
    }

    // MARK: - 9. Invalid URL → fail

    func testInvalidURLFails() async {
        let validator = makeValidator(error: CoreHTTPError.invalidURL("://bad-url"))
        let plan = makeGetPlan(url: "://this is not a url")
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        guard case .fail = outcome else {
            XCTFail("Invalid URL must fail. Got: \(outcome)"); return
        }
    }

    // MARK: - 10. No GET plans → skipped

    func testNoGetPlansSkipped() async {
        let validator = makeValidator(response: makeResponse(statusCode: 200))
        let plans = [makePostPlan(actionID: "test.create"), makePostPlan(actionID: "test.update")]
        let outcome = await validator.validate(plans: plans, skillName: "Test")
        if case .skipped = outcome { return }
        XCTFail("All-POST plans must return .skipped. Got: \(outcome)")
    }

    // MARK: - 11. Path placeholder substitution

    func testPathPlaceholderSubstituted() async {
        var capturedURL: URL?
        let secrets = CoreSecretsService(reader: { _ in nil })
        let validator = ForgeDryRunValidator(
            secretsService: secrets,
            executor: { req in
                capturedURL = req.url
                return self.makeResponse(statusCode: 200)
            }
        )
        let plan = makeGetPlan(url: "https://api.test.com/v1/users/{userID}/profile")
        _ = await validator.validate(plans: [plan], skillName: "Test")
        XCTAssertFalse(capturedURL?.absoluteString.contains("{userID}") ?? true,
            "Path placeholders must be substituted before the request is made.")
        XCTAssertTrue(capturedURL?.absoluteString.contains("test") ?? false,
            "Placeholder must be replaced with 'test'. Got: \(capturedURL?.absoluteString ?? "nil")")
    }

    // MARK: - 12. 403 with credential → pass

    func test403WithCredentialPasses() async {
        let validator = ForgeDryRunValidator(
            secretsService: CoreSecretsService(reader: { _ in "my-bearer-token" }),
            executor: { _ in self.makeResponse(statusCode: 403) }
        )
        let plan = makeGetPlan(
            authType: .bearerTokenStatic,
            authSecretKey: "com.projectatlas.token"
        )
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        XCTAssertEqual(outcome, .pass,
            "HTTP 403 with injected Bearer token must pass (API is reachable).")
    }

    // MARK: - 13. 403 without credential → fail

    func test403WithoutCredentialFails() async {
        let validator = makeValidator(response: makeResponse(statusCode: 403))
        let plan = makeGetPlan(authType: .none)
        let outcome = await validator.validate(plans: [plan], skillName: "Test")
        guard case .fail(let reason) = outcome else {
            XCTFail("HTTP 403 without credential must fail. Got: \(outcome)"); return
        }
        XCTAssertTrue(reason.contains("403"),
            "Failure must reference status 403. Got: \(reason)")
    }

    // MARK: - 14. 2xx codes all pass

    func testAll2xxCodePass() async {
        for statusCode in [200, 201, 204] {
            let validator = makeValidator(response: makeResponse(statusCode: statusCode))
            let plan = makeGetPlan()
            let outcome = await validator.validate(plans: [plan], skillName: "Test")
            XCTAssertEqual(outcome, .pass, "HTTP \(statusCode) must pass the dry-run.")
        }
    }

    // MARK: - 15. 5xx codes all fail

    func testAll5xxCodesFail() async {
        for statusCode in [500, 502, 503] {
            let validator = makeValidator(response: makeResponse(statusCode: statusCode))
            let plan = makeGetPlan()
            let outcome = await validator.validate(plans: [plan], skillName: "Test")
            guard case .fail = outcome else {
                XCTFail("HTTP \(statusCode) must fail the dry-run. Got: \(outcome)"); return
            }
        }
    }

    // MARK: - 16. apiKeyQuery credential injected into query params

    func testAPIKeyQueryCredentialInjected() async {
        var capturedURL: URL?
        let secrets = CoreSecretsService(reader: { _ in "my-query-key" })
        let validator = ForgeDryRunValidator(
            secretsService: secrets,
            executor: { req in
                capturedURL = req.url
                return self.makeResponse(statusCode: 200)
            }
        )
        let plan = makeGetPlan(
            authType: .apiKeyQuery,
            authSecretKey: "com.projectatlas.myapi",
            authQueryParamName: "api_key"
        )
        _ = await validator.validate(plans: [plan], skillName: "Test")
        let urlStr = capturedURL?.absoluteString ?? ""
        XCTAssertTrue(urlStr.contains("api_key="),
            "apiKeyQuery credential must be injected as a query parameter. Got URL: \(urlStr)")
        // Value must NOT appear in logs (tested implicitly — we just ensure the param name is present)
    }
}
