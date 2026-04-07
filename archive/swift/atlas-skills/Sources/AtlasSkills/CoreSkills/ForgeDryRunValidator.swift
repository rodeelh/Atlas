import Foundation
import AtlasLogging

// MARK: - ForgeDryRunValidator

/// Auth-aware dry-run validator for Forge skill proposals.
///
/// Runs AFTER all validation gates pass and BEFORE `startResearching()` /
/// `createProposal()` are called. It confirms that the target API is real,
/// reachable, and responds as expected when credentials are injected.
///
/// ## Scope
///
/// Only `GET` action plans are executed — `POST`, `PUT`, and `DELETE` plans are
/// not executed to avoid unintended side-effects. If all plans are non-GET the
/// validator returns `.skipped`.
///
/// ## Auth injection
///
/// Credentials are injected from Keychain using the same strategy as
/// `ForgeSkill.executeHTTP`, ensuring the dry-run accurately represents what
/// will happen at runtime.
///
/// ## Response interpretation
///
/// | Status range          | Outcome                                         |
/// |-----------------------|-------------------------------------------------|
/// | 2xx                   | pass                                            |
/// | 3xx                   | pass (redirect — API is reachable)              |
/// | 4xx (not 401/403)     | pass (API responded; test placeholder wrong)    |
/// | 401 / 403 + cred      | pass (API reachable; auth recognised)           |
/// | 401 / 403 − cred      | fail (auth not configured; Gate 8 missed it)    |
/// | 5xx                   | fail (server error / wrong endpoint)            |
/// | DNS / network error   | fail (host unreachable / URL invalid)           |
///
/// ## Security
///
/// The full request URL (which may contain query-param credentials) and all
/// credential values are **never** logged. Only the host is recorded.
///
/// ## Testability
///
/// The HTTP executor is injectable via `init(secretsService:executor:)` so
/// unit tests can mock any response without live network calls.
public struct ForgeDryRunValidator: Sendable {

    // MARK: - Types

    /// Injectable HTTP executor — defaults to `CoreHTTPService.execute(_:)`.
    public typealias HTTPExecutor = @Sendable (CoreHTTPRequest) async throws -> CoreHTTPResponse

    /// The outcome of a dry-run validation pass.
    public enum Outcome: Sendable, Equatable {
        /// All validated GET plans received acceptable responses.
        case pass
        /// A plan produced a structural or network failure. Block proposal creation.
        case fail(reason: String)
        /// No GET plans were present (e.g. all POST). Treated as pass by callers.
        case skipped(reason: String)

        public static func == (lhs: Outcome, rhs: Outcome) -> Bool {
            switch (lhs, rhs) {
            case (.pass, .pass):                        return true
            case (.fail(let l), .fail(let r)):       return l == r
            case (.skipped(let l), .skipped(let r)):    return l == r
            default:                                    return false
            }
        }
    }

    // MARK: - Properties

    private let secretsService: CoreSecretsService
    private let executor: HTTPExecutor
    private let timeoutSeconds: Double
    private let logger: AtlasLogger

    // MARK: - Initialisers

    /// Production initialiser — uses a real `CoreHTTPService` for execution.
    public init(
        secretsService: CoreSecretsService,
        httpService: CoreHTTPService,
        timeoutSeconds: Double = 8.0
    ) {
        self.secretsService = secretsService
        self.timeoutSeconds = timeoutSeconds
        self.logger = AtlasLogger(category: "forge.dryrun")
        self.executor = { req in try await httpService.execute(req) }
    }

    /// Test / staging initialiser — injects a custom HTTP executor.
    ///
    /// Use this when you need to mock HTTP responses without making live network calls,
    /// e.g. in unit tests or integration tests that need deterministic API behaviour.
    public init(
        secretsService: CoreSecretsService,
        executor: @escaping HTTPExecutor,
        timeoutSeconds: Double = 8.0
    ) {
        self.secretsService = secretsService
        self.executor = executor
        self.timeoutSeconds = timeoutSeconds
        self.logger = AtlasLogger(category: "forge.dryrun")
    }

    // MARK: - Validation

    /// Validate all GET action plans by attempting a real HTTP request.
    ///
    /// Returns `.pass` when all GET plans succeed, `.fail` with an explanation
    /// when any plan fails, or `.skipped` when there are no GET plans to validate.
    public func validate(plans: [ForgeActionPlan], skillName: String) async -> Outcome {
        let getPlans = plans.filter { $0.httpRequest?.method.uppercased() == "GET" }

        if getPlans.isEmpty {
            return .skipped(reason:
                "No GET plans to dry-run — only GET requests are validated to avoid side-effects."
            )
        }

        for plan in getPlans {
            guard let http = plan.httpRequest else { continue }
            let outcome = await validateSinglePlan(plan: plan, http: http)
            if case .fail = outcome { return outcome }
        }

        return .pass
    }

    // MARK: - Private

    private func validateSinglePlan(plan: ForgeActionPlan, http: HTTPRequestPlan) async -> Outcome {
        // Substitute all {paramName} placeholders with "test" so the URL is structurally
        // valid without real input values. We're testing reachability, not data correctness.
        let testURLString = http.url.replacingOccurrences(
            of: #"\{[^}]+\}"#,
            with: "test",
            options: .regularExpression
        )

        guard var urlComponents = URLComponents(string: testURLString) else {
            return .fail(reason:
                "Action '\(plan.actionID)' has an invalid URL '\(http.url)'. " +
                "Verify the base URL and endpoint path are correct."
            )
        }

        // Static query params from the plan definition only (no live input at proposal time)
        var queryItems: [URLQueryItem] = http.query.map { dict in
            dict.map { URLQueryItem(name: $0.key, value: $0.value) }
        } ?? []

        // Track whether a credential was injected — needed to interpret 401/403 correctly
        var credentialInjected = false

        // ── apiKeyQuery: inject before URL finalisation ────────────────────────
        if http.authType == .apiKeyQuery,
           let secretKey = nonEmpty(http.authSecretKey),
           let paramName = nonEmpty(http.authQueryParamName) {
            if let value = try? await secretsService.get(service: secretKey), !value.isEmpty {
                queryItems.append(URLQueryItem(name: paramName, value: value))
                credentialInjected = true
            }
        }

        if !queryItems.isEmpty { urlComponents.queryItems = queryItems }

        guard let finalURL = urlComponents.url else {
            return .fail(reason:
                "Action '\(plan.actionID)' produced an unresolvable URL after parameter substitution."
            )
        }

        // ── Build headers ──────────────────────────────────────────────────────
        var headers: [String: String] = http.headers ?? [:]
        headers["Accept"] = "application/json"

        if let authType = http.authType {
            switch authType {
            case .none:
                break

            case .apiKeyHeader:
                if let secretKey = nonEmpty(http.authSecretKey),
                   let headerName = nonEmpty(http.authHeaderName),
                   let value = try? await secretsService.get(service: secretKey),
                   !value.isEmpty {
                    headers[headerName] = value
                    credentialInjected = true
                }

            case .bearerTokenStatic:
                // Prefer authSecretKey; fall back to legacy secretHeader
                let key = nonEmpty(http.authSecretKey) ?? nonEmpty(http.secretHeader)
                if let secretKey = key,
                   let token = try? await secretsService.get(service: secretKey),
                   !token.isEmpty {
                    headers["Authorization"] = "Bearer \(token)"
                    credentialInjected = true
                }

            case .basicAuth:
                if let secretKey = nonEmpty(http.authSecretKey),
                   let encoded = try? await secretsService.get(service: secretKey),
                   !encoded.isEmpty {
                    headers["Authorization"] = "Basic \(encoded)"
                    credentialInjected = true
                }

            case .apiKeyQuery:
                break // already injected above

            case .oauth2AuthorizationCode, .oauth2ClientCredentials, .customUnsupported, .unknown:
                // These are blocked by Gate 6; reaching here means a residual unsupported plan.
                return .skipped(reason:
                    "Action '\(plan.actionID)' uses unsupported auth type '\(authType.rawValue)' — " +
                    "skipping dry-run (should have been blocked by Gate 6)."
                )
            }
        } else if let secretKey = nonEmpty(http.secretHeader) {
            // Legacy Bearer injection (pre-AuthCore v1 plans, nil authType)
            if let token = try? await secretsService.get(service: secretKey), !token.isEmpty {
                headers["Authorization"] = "Bearer \(token)"
                credentialInjected = true
            }
        }

        let request = CoreHTTPRequest(
            url: finalURL,
            method: .get,
            headers: headers,
            timeoutSeconds: timeoutSeconds
        )

        // Log host only — never log full URL (may contain query-param credentials)
        logger.info("ForgeDryRun: executing GET validation", metadata: [
            "action_id": plan.actionID,
            "host": finalURL.host ?? "unknown"
        ])

        do {
            let response = try await executor(request)
            return interpret(statusCode: response.statusCode, actionID: plan.actionID,
                             credentialInjected: credentialInjected)
        } catch let httpErr as CoreHTTPError {
            return mapError(httpErr, actionID: plan.actionID)
        } catch {
            return .fail(reason:
                "Action '\(plan.actionID)' — unexpected error during dry-run: " +
                error.localizedDescription
            )
        }
    }

    // MARK: - Response Interpretation

    private func interpret(statusCode: Int, actionID: String, credentialInjected: Bool) -> Outcome {
        switch statusCode {
        case 200..<300:
            return .pass

        case 300..<400:
            return .pass // Redirect — API is reachable

        case 401, 403:
            if credentialInjected {
                // Credential was injected and API recognised the request — reachable + auth OK
                return .pass
            }
            return .fail(reason:
                "Action '\(actionID)' returned HTTP \(statusCode) and no credential was available " +
                "to inject. Ensure the API key is configured in Keychain and 'authSecretKey' is " +
                "set correctly in plans_json."
            )

        case 400..<500:
            // Other 4xx: API responded; our placeholder test values were just invalid
            return .pass

        case 500..<600:
            return .fail(reason:
                "Action '\(actionID)' returned HTTP \(statusCode) (server error). " +
                "The endpoint may be incorrect or the API is temporarily unavailable. " +
                "Verify the base URL and endpoint path."
            )

        default:
            return .pass // Unexpected code — API responded; treat as reachable
        }
    }

    private func mapError(_ error: CoreHTTPError, actionID: String) -> Outcome {
        switch error {
        case .invalidURL(let url):
            return .fail(reason:
                "Action '\(actionID)' has an invalid URL: \(url). " +
                "Check the base URL and endpoint for typos."
            )
        case .networkError(let msg):
            return .fail(reason:
                "Action '\(actionID)' — network error: \(msg). " +
                "Verify the host is correct and reachable."
            )
        case .timeout:
            return .fail(reason:
                "Action '\(actionID)' timed out during dry-run. " +
                "The host may be unreachable or incorrectly specified."
            )
        case .httpError(let code, _):
            return interpret(statusCode: code, actionID: actionID, credentialInjected: false)
        case .responseError(let msg):
            return .fail(reason:
                "Action '\(actionID)' — response error: \(msg)."
            )
        }
    }

    // MARK: - Helpers

    /// Returns nil when `s` is nil or consists only of whitespace.
    private func nonEmpty(_ s: String?) -> String? {
        guard let s, !s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return s
    }
}
