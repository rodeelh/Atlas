import Foundation
import AtlasLogging
import AtlasShared

// MARK: - APIValidationRequest

/// Runtime input for `APIValidationService.validate()`.
///
/// Defined here (not in AtlasTypes) because it is internal to the execution engine
/// and does not need to cross package boundaries.
public struct APIValidationRequest: Sendable {
    public let providerName: String
    public let baseURL: String
    public let endpoint: String
    public let method: String              // only GET is executed in v1
    public let authType: String            // matches APIAuthType raw values
    public let authSecretKey: String?      // keychain secret key name (no value)
    public let authHeaderName: String?
    public let authQueryParamName: String?
    public let requiredParams: [String]
    public let paramLocations: [String: String]  // param → "path"|"query"|"body"
    public let exampleInputs: [ExampleInput]     // provided by caller, may be empty
    public let expectedFields: [String]          // fields the response should contain
    public let useCaseSummary: String

    public init(
        providerName: String,
        baseURL: String,
        endpoint: String,
        method: String = "GET",
        authType: String = "none",
        authSecretKey: String? = nil,
        authHeaderName: String? = nil,
        authQueryParamName: String? = nil,
        requiredParams: [String] = [],
        paramLocations: [String: String] = [:],
        exampleInputs: [ExampleInput] = [],
        expectedFields: [String] = [],
        useCaseSummary: String = ""
    ) {
        self.providerName = providerName
        self.baseURL = baseURL
        self.endpoint = endpoint
        self.method = method
        self.authType = authType
        self.authSecretKey = authSecretKey
        self.authHeaderName = authHeaderName
        self.authQueryParamName = authQueryParamName
        self.requiredParams = requiredParams
        self.paramLocations = paramLocations
        self.exampleInputs = exampleInputs
        self.expectedFields = expectedFields
        self.useCaseSummary = useCaseSummary
    }
}

// MARK: - APIValidationService

/// Validates a real HTTP API endpoint before a Forge proposal is persisted.
///
/// ## Validation sequence
///
/// ### Phase 1 — Pre-flight (fast, no network)
/// 1. **Method check** — non-GET returns `.skipped`; only GET is safely executable in v1.
/// 2. **Shape check** — baseURL and endpoint must be non-empty.
/// 3. **Auth type check** — unsupported auth types (oauth2, custom, unknown) reject early via `AuthCore`.
/// 4. **Credential readiness** — if secretsService injected, verifies the required Keychain secret exists.
///
/// ### Phase 2 — Candidate execution loop (max 2 attempts)
/// Runs up to 2 live GET attempts. Each attempt uses a distinct example input:
/// - Attempt 1: best available (provided → catalog → generated).
/// - Attempt 2: alternate example (only if attempt 1 returned `.needsRevision`).
///   Hard failures (`.reject`: auth error, 5xx, network) abort immediately — no retry.
///
/// The loop stops as soon as a `.usable` result is produced, or after at most 2 attempts.
///
/// ### Phase 3 — Audit
/// Appends an `APIValidationAuditRecord` to the in-memory history regardless of outcome.
///
/// ## Auth injection
/// Credentials are read from `CoreSecretsService` at call time — the same pattern as
/// `ForgeDryRunValidator`. `AuthCore` remains the single source of truth for auth classification.
///
/// ## Security
/// Full URLs (which may contain query-param credentials) and credential values are never logged.
/// Only the host name is recorded.
///
/// ## Testability
/// The HTTP executor is injectable via `init(executor:secretsService:)` so unit tests
/// can mock any response without live network calls.
public actor APIValidationService {

    // MARK: - Types

    /// Injectable HTTP executor. Defaults to `CoreHTTPService.execute(_:)`.
    /// Override in tests via the `executor:` initialiser to avoid live network calls.
    public typealias HTTPExecutor = @Sendable (CoreHTTPRequest) async throws -> CoreHTTPResponse

    // MARK: - Properties

    private let executor: HTTPExecutor
    private let secretsService: CoreSecretsService?
    private let exampleCatalog: ExampleInputCatalog
    private let logger: AtlasLogger
    private var history: [APIValidationAuditRecord] = []

    /// Maximum candidate attempts per validation call. Hard limit — never exceeded.
    private static let maxAttempts = 2

    // MARK: - Init

    /// Production initialiser — wraps a `CoreHTTPService` for execution.
    public init(
        httpService: CoreHTTPService,
        secretsService: CoreSecretsService?,
        catalog: ExampleInputCatalog = .default,
        logger: AtlasLogger = AtlasLogger(category: "api.validation")
    ) {
        self.executor = { req in try await httpService.execute(req) }
        self.secretsService = secretsService
        self.exampleCatalog = catalog
        self.logger = logger
    }

    /// Test initialiser — injects a custom HTTP executor so unit tests can mock
    /// any response without making live network calls.
    public init(
        executor: @escaping HTTPExecutor,
        secretsService: CoreSecretsService?,
        catalog: ExampleInputCatalog = .default,
        logger: AtlasLogger = AtlasLogger(category: "api.validation")
    ) {
        self.executor = executor
        self.secretsService = secretsService
        self.exampleCatalog = catalog
        self.logger = logger
    }

    // MARK: - Validate

    public func validate(_ request: APIValidationRequest) async -> APIValidationResult {

        // ── Phase 1: Pre-flight checks (no network) ──────────────────────────────

        // 1. Method check — non-GET skipped, not rejected
        let normalizedMethod = request.method.uppercased().trimmingCharacters(in: .whitespacesAndNewlines)
        guard normalizedMethod == "GET" else {
            let result = skippedResult(
                request: request,
                reason: "API Validation v1 only executes GET requests (method: '\(request.method)'). " +
                        "Non-GET plans are not validated — proceeding to Forge gates.",
                summary: "\(request.method) \(request.baseURL)\(request.endpoint)"
            )
            appendAudit(result, request: request, exampleUsed: nil)
            return result
        }

        let trimmedBase = request.baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedEndpoint = request.endpoint.trimmingCharacters(in: .whitespacesAndNewlines)

        // 2. Shape check
        guard !trimmedBase.isEmpty && !trimmedEndpoint.isEmpty else {
            let result = failure(
                request: request,
                category: .invalidRequestShape,
                reason: "baseURL and endpoint must both be non-empty.",
                recommendation: .reject,
                summary: "GET \(trimmedBase)\(trimmedEndpoint)"
            )
            appendAudit(result, request: request, exampleUsed: nil)
            return result
        }

        // 3. Auth type support check (AuthCore is the single source of truth)
        let parsedAuthType = APIAuthType(rawValue: request.authType) ?? .unknown
        let supportLevel = AuthCore.supportLevel(for: parsedAuthType)
        guard supportLevel.canForge else {
            let result = failure(
                request: request,
                category: .unsupportedAuth,
                reason: "Auth type '\(request.authType)' is not supported in API Validation v1. " +
                        "Supported types: none, apiKeyHeader, apiKeyQuery, bearerTokenStatic, basicAuth.",
                recommendation: .reject,
                summary: "GET \(trimmedBase)\(trimmedEndpoint)"
            )
            appendAudit(result, request: request, exampleUsed: nil)
            return result
        }

        // 4. Credential readiness check
        let credentialTypes: Set<APIAuthType> = [.apiKeyHeader, .apiKeyQuery, .bearerTokenStatic, .basicAuth]
        if let secrets = secretsService, credentialTypes.contains(parsedAuthType) {
            if let secretKey = nonEmpty(request.authSecretKey) {
                let validation = await secrets.validate(service: secretKey)
                if !validation.isValid {
                    let result = failure(
                        request: request,
                        category: .missingCredentials,
                        reason: "Required credential '\(secretKey)' for \(request.authType) auth is not configured. " +
                                "Add it in Settings → Keychain before creating this skill.",
                        recommendation: .reject,
                        summary: "GET \(trimmedBase)\(trimmedEndpoint)"
                    )
                    appendAudit(result, request: request, exampleUsed: nil)
                    return result
                }
            }
        }

        // ── Phase 2: Candidate execution loop (max 2 attempts) ───────────────────

        let candidates = buildCandidates(for: request)
        var finalResult: APIValidationResult?
        var attemptsUsed = 0

        for candidate in candidates.prefix(Self.maxAttempts) {
            attemptsUsed += 1
            logger.info("APIValidation: attempt \(attemptsUsed)/\(Self.maxAttempts)", metadata: [
                "provider": request.providerName,
                "example": candidate.name,
                "source": candidate.source.rawValue
            ])

            let attemptResult = await executeAttempt(
                request: request,
                example: candidate,
                parsedAuthType: parsedAuthType,
                trimmedBase: trimmedBase,
                trimmedEndpoint: trimmedEndpoint
            )

            // Attach attempts count (will be overwritten by the final value)
            finalResult = APIValidationResult(
                success: attemptResult.success,
                confidence: attemptResult.confidence,
                exampleUsed: attemptResult.exampleUsed,
                requestSummary: attemptResult.requestSummary,
                responsePreview: attemptResult.responsePreview,
                extractedFields: attemptResult.extractedFields,
                failureCategory: attemptResult.failureCategory,
                failureReason: attemptResult.failureReason,
                recommendation: attemptResult.recommendation,
                attemptsCount: attemptsUsed
            )

            // Stop conditions
            if attemptResult.recommendation == .usable {
                // Success — no need for a second attempt
                break
            }
            if attemptResult.recommendation == .reject {
                // Hard failure (auth error, 5xx, network failure, unsupported auth) — retry won't help
                break
            }
            // .needsRevision — try next candidate if available
        }

        let result = finalResult!

        // ── Phase 3: Audit ────────────────────────────────────────────────────────
        appendAudit(result, request: request, exampleUsed: result.exampleUsed)

        logger.info("APIValidation: completed", metadata: [
            "provider": request.providerName,
            "recommendation": result.recommendation.rawValue,
            "confidence": String(format: "%.2f", result.confidence),
            "attempts": "\(attemptsUsed)"
        ])

        return result
    }

    // MARK: - Audit History

    public func auditHistory() -> [APIValidationAuditRecord] {
        history
    }

    // MARK: - Private: Candidate Building

    /// Builds up to 2 candidate examples for the retry loop.
    /// Attempt 1: best available (provided → catalog → generated).
    /// Attempt 2: alternate example when a different input might produce a better response.
    private func buildCandidates(for request: APIValidationRequest) -> [ExampleInput] {
        let primary = exampleCatalog.resolve(for: request)
        var candidates = [primary]
        if let alternate = exampleCatalog.resolveAlternate(for: request, first: primary) {
            candidates.append(alternate)
        }
        return candidates
    }

    // MARK: - Private: Single Attempt Execution

    /// Executes one candidate attempt: builds the URL, injects auth, runs the HTTP call,
    /// and inspects the response. Returns an `APIValidationResult` (without `attemptsCount`;
    /// the caller sets that on the final result).
    private func executeAttempt(
        request: APIValidationRequest,
        example: ExampleInput,
        parsedAuthType: APIAuthType,
        trimmedBase: String,
        trimmedEndpoint: String
    ) async -> APIValidationResult {

        // Build URL with example inputs
        var resolvedEndpoint = trimmedEndpoint
        for (param, value) in example.inputs {
            if resolvedEndpoint.contains("{\(param)}") {
                let encoded = value.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? value
                resolvedEndpoint = resolvedEndpoint.replacingOccurrences(of: "{\(param)}", with: encoded)
            }
        }
        // Fill remaining {placeholder} tokens from requiredParams defaults
        for param in request.requiredParams {
            if resolvedEndpoint.contains("{\(param)}") {
                let fallback = ExampleInputCatalog().defaultValue(for: param)
                resolvedEndpoint = resolvedEndpoint.replacingOccurrences(of: "{\(param)}", with: fallback)
            }
        }

        let fullURLString = trimmedBase + resolvedEndpoint
        guard var urlComponents = URLComponents(string: fullURLString) else {
            return failure(
                request: request,
                category: .invalidRequestShape,
                reason: "Could not parse URL: \(fullURLString)",
                recommendation: .reject,
                summary: "GET \(fullURLString)"
            )
        }

        // Append query params
        var queryItems: [URLQueryItem] = urlComponents.queryItems ?? []
        let pathSubstitutedKeys = Set(example.inputs.keys.filter { trimmedEndpoint.contains("{\($0)}") })
        for param in request.requiredParams where !pathSubstitutedKeys.contains(param) {
            if let loc = request.paramLocations[param], loc == "query" {
                let value = example.inputs[param] ?? ExampleInputCatalog().defaultValue(for: param)
                queryItems.append(URLQueryItem(name: param, value: value))
            }
        }
        for (key, value) in example.inputs where !pathSubstitutedKeys.contains(key) {
            if let loc = request.paramLocations[key], loc == "query",
               !queryItems.contains(where: { $0.name == key }) {
                queryItems.append(URLQueryItem(name: key, value: value))
            }
        }

        // Auth injection — query param first (must precede URL finalization)
        var credentialInjected = false
        if parsedAuthType == .apiKeyQuery,
           let secretKey = nonEmpty(request.authSecretKey),
           let paramName = nonEmpty(request.authQueryParamName),
           let secrets = secretsService,
           let value = try? await secrets.get(service: secretKey), !value.isEmpty {
            queryItems.append(URLQueryItem(name: paramName, value: value))
            credentialInjected = true
        }

        if !queryItems.isEmpty { urlComponents.queryItems = queryItems }

        guard let finalURL = urlComponents.url else {
            return failure(
                request: request,
                category: .invalidRequestShape,
                reason: "Could not construct final URL from components.",
                recommendation: .reject,
                summary: "GET \(fullURLString)"
            )
        }

        // Header-based auth injection
        var headers: [String: String] = ["Accept": "application/json"]
        switch parsedAuthType {
        case .none:
            break
        case .apiKeyHeader:
            if let secrets = secretsService,
               let secretKey = nonEmpty(request.authSecretKey),
               let headerName = nonEmpty(request.authHeaderName),
               let value = try? await secrets.get(service: secretKey), !value.isEmpty {
                headers[headerName] = value
                credentialInjected = true
            }
        case .bearerTokenStatic:
            if let secrets = secretsService,
               let secretKey = nonEmpty(request.authSecretKey),
               let token = try? await secrets.get(service: secretKey), !token.isEmpty {
                headers["Authorization"] = "Bearer \(token)"
                credentialInjected = true
            }
        case .basicAuth:
            if let secrets = secretsService,
               let secretKey = nonEmpty(request.authSecretKey),
               let encoded = try? await secrets.get(service: secretKey), !encoded.isEmpty {
                headers["Authorization"] = "Basic \(encoded)"
                credentialInjected = true
            }
        case .apiKeyQuery:
            break // already injected above
        case .oauth2AuthorizationCode, .oauth2ClientCredentials, .customUnsupported, .unknown:
            break // blocked in pre-flight; unreachable here
        }

        _ = credentialInjected  // suppress warning — pattern mirrors ForgeDryRunValidator

        let requestSummary = "GET \(finalURL.host ?? trimmedBase)\(finalURL.path)"

        // Never log full URL — may contain query-param credentials
        logger.info("APIValidation: executing GET", metadata: [
            "host": finalURL.host ?? "unknown"
        ])

        // Execute
        let rawResponse: CoreHTTPResponse
        do {
            rawResponse = try await executor(CoreHTTPRequest(
                url: finalURL,
                method: .get,
                headers: headers,
                timeoutSeconds: 10.0
            ))
        } catch let httpErr as CoreHTTPError {
            let (category, reason) = mapHTTPError(httpErr)
            return failure(
                request: request,
                category: category,
                reason: reason,
                recommendation: .reject,
                summary: requestSummary
            )
        } catch {
            return failure(
                request: request,
                category: .networkFailure,
                reason: "Unexpected error: \(error.localizedDescription)",
                recommendation: .reject,
                summary: requestSummary
            )
        }

        // Inspect response
        let httpURLResponse = HTTPURLResponse(
            url: finalURL,
            statusCode: rawResponse.statusCode,
            httpVersion: nil,
            headerFields: rawResponse.headers
        )!
        let inspection = APIResponseInspector.inspect(
            data: rawResponse.body,
            response: httpURLResponse,
            expectedFields: request.expectedFields
        )

        return APIValidationResult(
            success: inspection.success,
            confidence: inspection.confidence,
            exampleUsed: example,
            requestSummary: requestSummary,
            responsePreview: inspection.responsePreview,
            extractedFields: inspection.extractedFields,
            failureCategory: inspection.failureCategory,
            failureReason: inspection.failureReason,
            recommendation: inspection.recommendation,
            attemptsCount: 1  // placeholder; caller sets the real attemptsCount
        )
    }

    // MARK: - Private: Audit

    private func appendAudit(
        _ result: APIValidationResult,
        request: APIValidationRequest,
        exampleUsed: ExampleInput?
    ) {
        let preview = result.responsePreview.map { String($0.prefix(200)) }
        history.append(APIValidationAuditRecord(
            id: UUID().uuidString,
            providerName: request.providerName,
            endpoint: request.endpoint,
            exampleUsed: exampleUsed,
            confidence: result.confidence,
            recommendation: result.recommendation,
            failureCategory: result.failureCategory,
            responsePreviewTrimmed: preview,
            timestamp: Date()
        ))
    }

    // MARK: - Private: Result Constructors

    private func failure(
        request: APIValidationRequest,
        category: APIValidationFailureCategory,
        reason: String,
        recommendation: APIValidationRecommendation,
        summary: String
    ) -> APIValidationResult {
        APIValidationResult(
            success: false,
            confidence: 0.0,
            exampleUsed: nil,
            requestSummary: summary,
            responsePreview: nil,
            extractedFields: [],
            failureCategory: category,
            failureReason: reason,
            recommendation: recommendation,
            attemptsCount: 1
        )
    }

    private func skippedResult(
        request: APIValidationRequest,
        reason: String,
        summary: String
    ) -> APIValidationResult {
        APIValidationResult(
            success: true,
            confidence: 1.0,
            exampleUsed: nil,
            requestSummary: summary,
            responsePreview: nil,
            extractedFields: [],
            failureCategory: nil,
            failureReason: reason,
            recommendation: .skipped,
            attemptsCount: 0
        )
    }

    // MARK: - Private: Helpers

    private func mapHTTPError(_ error: CoreHTTPError) -> (APIValidationFailureCategory, String) {
        switch error {
        case .invalidURL(let u):
            return (.invalidRequestShape, "Invalid URL: \(u)")
        case .networkError(let msg):
            return (.networkFailure, "Network error: \(msg)")
        case .timeout:
            return (.networkFailure, "Request timed out (10 second limit)")
        case .httpError(let code, _):
            if code == 401 || code == 403 { return (.httpError, "Authentication failed (HTTP \(code))") }
            if (500..<600).contains(code) { return (.httpError, "Server error HTTP \(code)") }
            return (.httpError, "HTTP \(code)")
        case .responseError(let msg):
            return (.networkFailure, "Response error: \(msg)")
        }
    }

    private func nonEmpty(_ s: String?) -> String? {
        guard let s, !s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return s
    }
}
