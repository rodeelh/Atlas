import Foundation

// MARK: - ForgeValidationGate

/// Evaluates an `APIResearchContract` and `[ForgeActionPlan]` against the Forge v1.4
/// quality gates.
///
/// ## Contract gates (run against `APIResearchContract`)
///
/// 0. `validationStatus` != fail — dry-run or shape check did not explicitly fail.
/// 1. `docsQuality` >= medium — docs are good enough to map parameters reliably.
/// 2. `mappingConfidence` == high — the agent is certain about every field name,
///    location, and type. Low or medium confidence means the mapping is a guess.
/// 3. `endpoint` is defined and non-empty.
/// 3b. When `mappingConfidence == .high`, `exampleResponse` must be non-empty — the
///     agent must have seen an actual response before claiming high confidence.
/// 4. `method` is a recognised HTTP verb.
/// 5. Every entry in `requiredParams` has a known entry in `paramLocations`.
/// 6. `authType` (if set) is natively supported by `AuthCore` — unsupported types
///    (OAuth, custom, unknown) are hard refusals.
///
/// ## Plan gates (run against `[ForgeActionPlan]`)
///
/// 7. Auth field completeness — every `HTTPRequestPlan` with a typed `authType` has
///    all companion fields required for credential injection at runtime (via
///    `evaluatePlans(_:skillName:)`).
///    For `oauth2ClientCredentials`: `oauth2TokenURL`, `oauth2ClientIDKey`, `oauth2ClientSecretKey`.
///
/// Gates 0, 1, 2, 6 produce hard refusals (require re-research or are blocked by design).
/// Gates 3, 3b, 4, 5, 7 produce clarification requests (the LLM can fix these).
///
/// Non-API skills bypass these gates entirely — the caller is responsible for only
/// invoking `evaluate`/`evaluatePlans` for skills with `kind == .api`.
struct ForgeValidationGate {

    // MARK: - Outcome

    enum GateOutcome: Sendable {
        /// All gates passed — proposal creation may proceed.
        case pass
        /// A hard quality failure. The agent must re-research before trying again.
        case refuse(message: String)
        /// Non-fatal gaps exist. The user or agent can provide the missing details.
        case needsClarification(question: String)
    }

    // MARK: - Evaluation

    func evaluate(contract: APIResearchContract, skillName: String) -> GateOutcome {
        var hardFailures: [String] = []
        var clarifications: [String] = []

        // ── Gate 0: explicit validation failure ─────────────────────────────────
        // If the agent attempted a dry-run or shape check and it failed, refuse
        // unconditionally — a known-bad request shape must not become a proposal.
        if contract.validationStatus == .fail {
            hardFailures.append(
                "A validation or dry-run check was attempted and failed. " +
                "The request shape does not match what the API expects. " +
                "Fix the endpoint, parameters, or auth before proposing this skill."
            )
        }

        // ── Gate 1: documentation quality ──────────────────────────────────────
        if contract.docsQuality == .low {
            hardFailures.append(
                "The API documentation quality is too low to safely design this skill. " +
                "I need at least medium-quality documentation to reliably map parameters and response fields."
            )
        }

        // ── Gate 2: mapping confidence must be high ─────────────────────────────
        if contract.mappingConfidence != .high {
            hardFailures.append(
                "Mapping confidence is '\(contract.mappingConfidence.rawValue)'. " +
                "It must be 'high' before I can create a proposal — meaning I am certain " +
                "about every field name, location, type, and authentication requirement."
            )
        }

        // ── Gate 3: endpoint must be defined ────────────────────────────────────
        if (contract.endpoint ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            clarifications.append("What is the specific endpoint path for this API (e.g. /v1/weather/current)?")
        }

        // ── Gate 3b: exampleResponse required when mappingConfidence is high ────
        // Claiming high confidence without seeing an actual response means the mapping
        // is speculative. The agent must observe at least one real response example
        // from the documentation before the proposal can proceed.
        if contract.mappingConfidence == .high,
           (contract.exampleResponse ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            clarifications.append(
                "mappingConfidence is 'high' but no exampleResponse was provided. " +
                "Please include a real example response from the API documentation " +
                "(e.g. the JSON body of a successful response) in the contract_json."
            )
        }

        // ── Gate 4: HTTP method must be a known verb ────────────────────────────
        let knownMethods: Set<String> = ["GET", "POST", "PUT", "DELETE", "PATCH"]
        let method = (contract.method ?? "").uppercased().trimmingCharacters(in: .whitespacesAndNewlines)
        if !knownMethods.contains(method) {
            clarifications.append("Which HTTP method does this endpoint use (GET, POST, PUT, DELETE, or PATCH)?")
        }

        // ── Gate 5: required param locations must all be known ──────────────────
        if !contract.requiredParams.isEmpty {
            let unmapped = contract.requiredParams.filter { contract.paramLocations[$0] == nil }
            if !unmapped.isEmpty {
                clarifications.append(
                    "I need to know the location (path / query / body / header) for " +
                    "these required parameters: \(unmapped.joined(separator: ", "))."
                )
            }
        }

        // ── Gate 6: auth type must be natively supported by AuthCore ─────────────
        // If authType is nil (not classified), we allow it through with a warning baked
        // into the system prompt. If it IS classified and unsupported, hard-refuse now
        // rather than letting Forge waste tokens building plans for an auth type Atlas
        // cannot execute.
        if let authType = contract.authType {
            let support = AuthCore.supportLevel(for: authType)
            if !support.canForge {
                hardFailures.append(AuthCore.refusalMessage(for: authType, skillName: skillName))
            }
        }

        // ── Decide outcome ──────────────────────────────────────────────────────
        if !hardFailures.isEmpty {
            let body = hardFailures
                .enumerated()
                .map { "  \($0.offset + 1). \($0.element)" }
                .joined(separator: "\n\n")
            return .refuse(message: """
                I can't safely create a skill proposal for "\(skillName)".

                \(body)

                Please research the API documentation more thoroughly, then try again.
                """)
        }

        if !clarifications.isEmpty {
            let items = clarifications
                .enumerated()
                .map { "  \($0.offset + 1). \($0.element)" }
                .joined(separator: "\n")
            return .needsClarification(question: """
                Before I can build the "\(skillName)" skill, I need a few more details:

                \(items)

                Could you provide these so I can map the API correctly?
                """)
        }

        return .pass
    }

    // MARK: - Gate 7: Auth Plan Field Completeness

    /// Evaluates each `HTTPRequestPlan` in the action plan array for auth field completeness.
    ///
    /// For every plan whose `authType` is explicitly set, verifies that the companion
    /// fields required for runtime credential injection are non-empty:
    ///
    /// | authType                  | Required companion fields                                      |
    /// |---------------------------|----------------------------------------------------------------|
    /// | `apiKeyHeader`            | `authSecretKey`, `authHeaderName`                              |
    /// | `apiKeyQuery`             | `authSecretKey`, `authQueryParamName`                          |
    /// | `bearerTokenStatic`       | `authSecretKey`                                                |
    /// | `basicAuth`               | `authSecretKey`                                                |
    /// | `oauth2ClientCredentials` | `oauth2TokenURL`, `oauth2ClientIDKey`, `oauth2ClientSecretKey` |
    /// | `none`                    | (none)                                                         |
    /// | `nil`                     | Skipped — legacy `secretHeader` path                           |
    ///
    /// Returns `.needsClarification` listing all missing fields. Returns `.pass`
    /// when every plan is complete or has no typed `authType`.
    func evaluatePlans(_ plans: [ForgeActionPlan], skillName: String) -> GateOutcome {
        var clarifications: [String] = []

        for plan in plans {
            guard let http = plan.httpRequest, let authType = http.authType else {
                continue // nil authType → legacy secretHeader path; skip gate 7
            }

            func missingField(_ fieldName: String, _ hint: String) {
                clarifications.append(
                    "Action '\(plan.actionID)': authType is '\(authType.rawValue)' " +
                    "but '\(fieldName)' is missing. \(hint)"
                )
            }

            switch authType {
            case .none:
                break // no companion fields required

            case .apiKeyHeader:
                if isEmpty(http.authSecretKey) {
                    missingField("authSecretKey",
                        "Provide the Keychain service name for the API key " +
                        "(e.g. \"com.projectatlas.myapi\").")
                }
                if isEmpty(http.authHeaderName) {
                    missingField("authHeaderName",
                        "Specify the HTTP header name the key is sent in " +
                        "(e.g. \"X-API-Key\", \"Api-Key\").")
                }

            case .apiKeyQuery:
                if isEmpty(http.authSecretKey) {
                    missingField("authSecretKey",
                        "Provide the Keychain service name for the API key.")
                }
                if isEmpty(http.authQueryParamName) {
                    missingField("authQueryParamName",
                        "Specify the URL query parameter name for the key " +
                        "(e.g. \"api_key\", \"key\", \"apiKey\").")
                }

            case .bearerTokenStatic:
                if isEmpty(http.authSecretKey) {
                    missingField("authSecretKey",
                        "Provide the Keychain service name for the Bearer token.")
                }

            case .basicAuth:
                if isEmpty(http.authSecretKey) {
                    missingField("authSecretKey",
                        "Provide the Keychain service name for the base64-encoded " +
                        "\"user:pass\" credential.")
                }

            case .oauth2ClientCredentials:
                if isEmpty(http.oauth2TokenURL) {
                    missingField("oauth2TokenURL",
                        "Provide the token endpoint URL for this API " +
                        "(e.g. \"https://accounts.spotify.com/api/token\").")
                }
                if isEmpty(http.oauth2ClientIDKey) {
                    missingField("oauth2ClientIDKey",
                        "Provide the Keychain service name for the client_id " +
                        "(e.g. \"com.projectatlas.spotify.clientid\").")
                }
                if isEmpty(http.oauth2ClientSecretKey) {
                    missingField("oauth2ClientSecretKey",
                        "Provide the Keychain service name for the client_secret " +
                        "(e.g. \"com.projectatlas.spotify.secret\").")
                }

            case .oauth2AuthorizationCode, .customUnsupported, .unknown:
                break // should have been blocked by Gate 6; no field check needed
            }
        }

        guard !clarifications.isEmpty else { return .pass }

        let items = clarifications
            .enumerated()
            .map { "  \($0.offset + 1). \($0.element)" }
            .joined(separator: "\n\n")

        return .needsClarification(question: """
            The authentication configuration for "\(skillName)" is incomplete:

            \(items)

            Please update plans_json with the missing auth fields and try again.
            """)
    }

    // MARK: - Private Helpers

    private func isEmpty(_ s: String?) -> Bool {
        s?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ?? true
    }
}
