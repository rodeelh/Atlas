import Foundation
import AtlasLogging
import AtlasShared
import AtlasTools

// MARK: - ForgeSkill

/// A dynamic skill created by Forge and executed by the Atlas runtime.
///
/// `ForgeSkill` is the Dynamic Skill Runtime v0. It interprets a `ForgeSkillPackage`
/// at runtime, executing each action according to its `ForgeActionPlan` without
/// requiring pre-compiled Swift code per skill.
///
/// Forge v1 supports one execution type: HTTP (`ForgeActionType.http`).
/// Each action makes exactly one HTTP call using `CoreHTTPService`, maps the
/// agent-provided input to query parameters or request body, and returns the
/// raw response text as output.
///
/// Security rules enforced here:
/// - All HTTP calls go through `CoreHTTPService` — no raw URLSession usage.
/// - Secrets are read via `CoreSecretsService` and NEVER logged or included in output.
/// - Agent-provided input is treated as untrusted user data — URL-encoded for queries,
///   JSON-serialized for bodies. No server-side template injection risk.
/// - The skill goes through `SkillExecutionGateway` like all other skills — approval
///   and policy rules are not bypassed.
public struct ForgeSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let package: ForgeSkillPackage
    private let httpService: CoreHTTPService
    private let secretsService: CoreSecretsService
    private let oauth2Service: OAuth2ClientCredentialsService

    // MARK: - Init

    public init(
        package: ForgeSkillPackage,
        actionDefinitions: [SkillActionDefinition],
        httpService: CoreHTTPService = CoreHTTPService(),
        secretsService: CoreSecretsService
    ) {
        self.manifest = package.manifest
        self.actions = actionDefinitions
        self.package = package
        self.httpService = httpService
        self.secretsService = secretsService
        self.oauth2Service = OAuth2ClientCredentialsService(
            httpService: httpService,
            secretsService: secretsService
        )
    }

    // MARK: - Validation

    /// Validates that all `requiredSecrets` declared in the manifest are present
    /// in the Keychain before the skill can be enabled.
    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        var missing: [String] = []
        for service in manifest.requiredSecrets {
            let check = await secretsService.validate(service: service)
            if !check.isValid {
                missing.append(check.summary)
            }
        }

        if missing.isEmpty {
            return SkillValidationResult(
                skillID: manifest.id,
                status: .passed,
                summary: "Forge skill configuration is valid."
            )
        }

        return SkillValidationResult(
            skillID: manifest.id,
            status: .failed,
            summary: missing.joined(separator: " "),
            issues: missing
        )
    }

    // MARK: - Execution

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        guard let plan = package.plan(for: actionID) else {
            throw AtlasToolError.invalidInput(
                "Forge skill '\(manifest.id)' does not define an action '\(actionID)'."
            )
        }

        switch plan.type {
        case .http:
            guard let httpPlan = plan.httpRequest else {
                throw AtlasToolError.invalidInput(
                    "Action '\(actionID)' has type 'http' but no HTTPRequestPlan is defined."
                )
            }
            return try await executeHTTP(actionID: actionID, plan: httpPlan, input: input, context: context)
        }
    }

    // MARK: - HTTP Execution

    private func executeHTTP(
        actionID: String,
        plan: HTTPRequestPlan,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        // Decode agent-provided parameters as string dictionary.
        // ForgeSkill v1 requires all parameters to be string-valued.
        let params = (try? input.dictionary()) ?? [:]

        // Resolve URL: substitute {paramName} placeholders with input values.
        let resolvedURLString = substituteParams(in: plan.url, with: params)

        guard var urlComponents = URLComponents(string: resolvedURLString) else {
            throw AtlasToolError.invalidInput(
                "Forge action '\(actionID)' has an invalid URL after substitution: \(resolvedURLString)"
            )
        }

        // Params that were substituted into the URL path must not be re-injected as query items.
        // Example: url="/v1/quotes/{category}", input={"category":"motivation"}
        // → path becomes /v1/quotes/motivation; do NOT also append ?category=motivation.
        let pathSubstitutedKeys = Set(params.keys.filter { plan.url.contains("{\($0)}") })

        // Build query items: static plan.query first, then remaining input params for GET.
        let method = plan.method.uppercased()
        var queryItems = plan.query.map { dict in
            dict.map { URLQueryItem(name: $0.key, value: $0.value) }
        } ?? []

        if method == "GET" {
            for (key, value) in params where !pathSubstitutedKeys.contains(key) {
                queryItems.append(URLQueryItem(name: key, value: value))
            }
        }

        // ── Auth injection: apiKeyQuery (must happen before URL finalization) ────
        // Read the API key from Keychain and append it as a query parameter.
        // This must occur before urlComponents.url is resolved.
        if plan.authType == .apiKeyQuery,
           let secretKey = plan.authSecretKey,
           let paramName = plan.authQueryParamName {
            do {
                guard let value = try await secretsService.get(service: secretKey) else {
                    return SkillExecutionResult(
                        skillID: manifest.id, actionID: actionID,
                        output: "Credential '\(secretKey)' is not configured. " +
                                "Open Settings → Keychain and add the API key to use this skill.",
                        success: false,
                        summary: "Credential '\(secretKey)' not found in Keychain."
                    )
                }
                queryItems.append(URLQueryItem(name: paramName, value: value))
            } catch {
                throw AtlasToolError.executionFailed(
                    "Forge skill '\(manifest.id)' could not read API key '\(secretKey)': " +
                    error.localizedDescription
                )
            }
        }

        if !queryItems.isEmpty {
            urlComponents.queryItems = queryItems
        }

        guard let finalURL = urlComponents.url else {
            throw AtlasToolError.invalidInput(
                "Could not construct a valid URL for Forge action '\(actionID)'."
            )
        }

        // Build headers: static plan headers + Accept.
        var headers: [String: String] = plan.headers ?? [:]
        headers["Accept"] = "application/json"

        // ── Auth injection: header-based auth types ──────────────────────────────
        // Secret values are read from Keychain and are NEVER logged.
        // A nil return from `secretsService.get` means the secret is not yet configured;
        // execution proceeds without the auth header rather than failing hard, so the user
        // gets an informative API 401 rather than a cryptic runtime error.
        // A thrown error is a genuine Keychain failure and must surface as an execution error.
        if let authType = plan.authType {
            // New auth routing: authType drives the injection strategy.
            switch authType {
            case .none:
                break // No auth needed — proceed without any credential.

            case .apiKeyHeader:
                if let secretKey = plan.authSecretKey, let headerName = plan.authHeaderName {
                    do {
                        guard let value = try await secretsService.get(service: secretKey) else {
                            return SkillExecutionResult(
                                skillID: manifest.id, actionID: actionID,
                                output: "Credential '\(secretKey)' is not configured. " +
                                        "Open Settings → Keychain and add the API key to use this skill.",
                                success: false,
                                summary: "Credential '\(secretKey)' not found in Keychain."
                            )
                        }
                        headers[headerName] = value
                    } catch {
                        throw AtlasToolError.executionFailed(
                            "Forge skill '\(manifest.id)' could not read credential '\(secretKey)': " +
                            error.localizedDescription
                        )
                    }
                }

            case .bearerTokenStatic:
                // Prefer authSecretKey; fall back to legacy secretHeader for backward compat.
                let key = plan.authSecretKey ?? plan.secretHeader
                if let secretKey = key {
                    do {
                        guard let token = try await secretsService.get(service: secretKey) else {
                            return SkillExecutionResult(
                                skillID: manifest.id, actionID: actionID,
                                output: "Credential '\(secretKey)' is not configured. " +
                                        "Open Settings → Keychain and add the API key to use this skill.",
                                success: false,
                                summary: "Credential '\(secretKey)' not found in Keychain."
                            )
                        }
                        headers["Authorization"] = "Bearer \(token)"
                    } catch {
                        throw AtlasToolError.executionFailed(
                            "Forge skill '\(manifest.id)' could not read credential '\(secretKey)': " +
                            error.localizedDescription
                        )
                    }
                }

            case .basicAuth:
                if let secretKey = plan.authSecretKey {
                    do {
                        // The stored value must be the pre-encoded base64 "user:pass" string.
                        guard let encoded = try await secretsService.get(service: secretKey) else {
                            return SkillExecutionResult(
                                skillID: manifest.id, actionID: actionID,
                                output: "Credential '\(secretKey)' is not configured. " +
                                        "Open Settings → Keychain and add the base64 credentials to use this skill.",
                                success: false,
                                summary: "Credential '\(secretKey)' not found in Keychain."
                            )
                        }
                        headers["Authorization"] = "Basic \(encoded)"
                    } catch {
                        throw AtlasToolError.executionFailed(
                            "Forge skill '\(manifest.id)' could not read credential '\(secretKey)': " +
                            error.localizedDescription
                        )
                    }
                }

            case .apiKeyQuery:
                break // Already injected into queryItems before URL finalization — no-op here.

            case .oauth2ClientCredentials:
                do {
                    let token = try await oauth2Service.fetchToken(plan: plan)
                    headers["Authorization"] = "Bearer \(token)"
                } catch {
                    throw AtlasToolError.executionFailed(
                        "Forge skill '\(manifest.id)' could not obtain OAuth2 token: " +
                        error.localizedDescription
                    )
                }

            case .oauth2AuthorizationCode, .customUnsupported, .unknown:
                // These should have been blocked by ForgeValidationGate before the proposal
                // was created. If reached here, the skill was approved with invalid state.
                throw AtlasToolError.executionFailed(
                    "Forge skill '\(manifest.id)' has an unsupported auth type '\(authType.rawValue)'. " +
                    "This proposal should not have been approved — please uninstall and re-propose."
                )
            }
        } else if let secretService = plan.secretHeader {
            // ── Legacy path: secretHeader-only Bearer injection (pre-AuthCore v1) ──
            // Only runs when authType is nil — backward compatible with existing skills.
            do {
                guard let token = try await secretsService.get(service: secretService) else {
                    return SkillExecutionResult(
                        skillID: manifest.id, actionID: actionID,
                        output: "Credential '\(secretService)' is not configured. " +
                                "Open Settings → Keychain and add the API key to use this skill.",
                        success: false,
                        summary: "Credential '\(secretService)' not found in Keychain."
                    )
                }
                headers["Authorization"] = "Bearer \(token)"
            } catch {
                throw AtlasToolError.executionFailed(
                    "Forge skill '\(manifest.id)' could not read credential '\(secretService)': " +
                    error.localizedDescription
                )
            }
        }

        // Build body: for POST/PUT, construct JSON body from bodyFields/staticBodyFields
        // or fall back to the full params dict for backward compatibility.
        var body: Data?
        if method == "POST" || method == "PUT" {
            let bodyDict = buildBodyDict(params: params, plan: plan)
            if !bodyDict.isEmpty {
                headers["Content-Type"] = "application/json"
                body = try? JSONSerialization.data(withJSONObject: bodyDict, options: [])
            }
        }

        context.logger.info("ForgeSkill executing HTTP action", metadata: [
            "skill_id": manifest.id,
            "action_id": actionID,
            "method": method,
            "host": finalURL.host ?? "unknown"
            // URL path logged for diagnostics; query params may contain user data — omit
        ])

        let httpMethod = CoreHTTPRequest.Method(rawValue: method) ?? .get
        let request = CoreHTTPRequest(
            url: finalURL,
            method: httpMethod,
            headers: headers,
            body: body
        )

        var response = try await httpService.execute(request)

        // ── 401 retry for OAuth2 Client Credentials ──────────────────────────────
        // If the server returns 401 and this plan uses OAuth2 Client Credentials,
        // the cached token may have been revoked or expired early. Invalidate it,
        // fetch a fresh token, and retry once. A second 401 is returned as-is.
        if response.statusCode == 401, plan.authType == .oauth2ClientCredentials {
            let cacheKey = "\(plan.oauth2TokenURL ?? "")|\(plan.oauth2ClientIDKey ?? "")"
            await OAuth2TokenCache.shared.invalidate(for: cacheKey)

            do {
                let freshToken = try await oauth2Service.fetchToken(plan: plan)
                var retryHeaders = headers
                retryHeaders["Authorization"] = "Bearer \(freshToken)"
                let retryRequest = CoreHTTPRequest(
                    url: finalURL,
                    method: httpMethod,
                    headers: retryHeaders,
                    body: body
                )
                response = try await httpService.execute(retryRequest)
            } catch {
                // If the retry token fetch fails, return the original 401 response.
            }
        }

        let output  = response.bodyString ?? "(empty response body)"
        let success = response.isSuccess

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: actionID,
            output: output,
            success: success,
            summary: "HTTP \(response.statusCode) from \(finalURL.host ?? "unknown").",
            metadata: ["http_status": "\(response.statusCode)"]
        )
    }

    // MARK: - Private Helpers

    /// Builds the JSON body dictionary for POST/PUT requests.
    ///
    /// When `plan.bodyFields` is set, only the explicitly mapped params are included,
    /// renamed to their body keys. When nil, all input params are sent as-is (legacy mode).
    /// In both cases, `plan.staticBodyFields` entries are merged in last (they win on conflict).
    ///
    /// String values that look like integers, decimals, or booleans are coerced to native
    /// JSON types so APIs that require `{"limit": 10}` work even when the LLM provides `"10"`.
    private func buildBodyDict(params: [String: String], plan: HTTPRequestPlan) -> [String: Any] {
        var result: [String: Any] = [:]

        if let bodyFields = plan.bodyFields {
            // Explicit mapping: bodyKey → inputParamName
            for (bodyKey, inputParam) in bodyFields {
                if let value = params[inputParam] {
                    result[bodyKey] = coerceJSONValue(value)
                }
            }
        } else {
            // Legacy flat mode: send all input params with their original names.
            for (key, value) in params {
                result[key] = coerceJSONValue(value)
            }
        }

        // Merge static fields last — they override dynamic values on conflict.
        if let staticFields = plan.staticBodyFields {
            for (key, value) in staticFields {
                result[key] = coerceJSONValue(value)
            }
        }

        return result
    }

    /// Coerces a string value to the most appropriate native JSON type.
    /// Tries Int → Double → Bool → falls back to String.
    private func coerceJSONValue(_ value: String) -> Any {
        if let intVal = Int(value) { return intVal }
        if let doubleVal = Double(value) { return doubleVal }
        switch value.lowercased() {
        case "true": return true
        case "false": return false
        default: return value
        }
    }

    /// Replace all `{paramName}` occurrences in a URL template with percent-encoded values from params.
    ///
    /// Values are encoded with `.urlPathAllowed` so that spaces, slashes, and other
    /// URL-unsafe characters in path segments do not corrupt the URL structure.
    /// `URLComponents(string:)` requires a structurally valid URL string; without encoding,
    /// a value like "San Francisco" would produce an unresolvable URL.
    private func substituteParams(in template: String, with params: [String: String]) -> String {
        params.reduce(template) { result, pair in
            let encoded = pair.value
                .addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? pair.value
            return result.replacingOccurrences(of: "{\(pair.key)}", with: encoded)
        }
    }
}
