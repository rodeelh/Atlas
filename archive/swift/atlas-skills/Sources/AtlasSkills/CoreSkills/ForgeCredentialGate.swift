import Foundation

// MARK: - ForgeCredentialGate

/// Gate 8 — Credential Readiness.
///
/// Verifies that every Keychain credential declared in an action plan's `authSecretKey`
/// actually exists and is non-empty before a Forge proposal is created.
///
/// ## Which auth types trigger Gate 8
///
/// Only plans where `authType` is one of the credential-requiring types:
/// `apiKeyHeader`, `apiKeyQuery`, `bearerTokenStatic`, `basicAuth`, `oauth2ClientCredentials`.
/// `none` and plans with a `nil` authType (legacy `secretHeader` path) are always skipped.
///
/// For `oauth2ClientCredentials`, both `oauth2ClientIDKey` and `oauth2ClientSecretKey`
/// are validated (not `authSecretKey`, which is used by v1 auth types only).
///
/// ## Why this is a clarification, not a refusal
///
/// A missing credential is a user-action item, not a research failure. The fix is to
/// add the key in Settings → Keychain and retry — not to re-research the API. Therefore
/// this gate returns `.needsClarification`, not `.refuse`.
///
/// ## Production vs. test behaviour
///
/// `ForgeOrchestrationSkill` only runs Gate 8 when a `CoreSecretsService` is injected.
/// In tests that do not inject a secrets service, Gate 8 is silently skipped —
/// preserving backward compatibility.
struct ForgeCredentialGate: Sendable {

    // MARK: - Evaluation

    /// Check that all credential-requiring plans have their Keychain secrets configured.
    ///
    /// - Parameters:
    ///   - plans: The decoded `[ForgeActionPlan]` for the proposed skill.
    ///   - skillName: Human-readable name used in the returned message.
    ///   - secrets: Live `CoreSecretsService` backed by Keychain.
    /// - Returns: `.pass` when all required credentials exist;
    ///   `.needsClarification` listing which secrets are missing or empty.
    func evaluate(
        plans: [ForgeActionPlan],
        skillName: String,
        secrets: CoreSecretsService
    ) async -> ForgeValidationGate.GateOutcome {

        let credentialTypes: Set<APIAuthType> = [
            .apiKeyHeader, .apiKeyQuery, .bearerTokenStatic, .basicAuth, .oauth2ClientCredentials
        ]
        var missing: [String] = []

        for plan in plans {
            guard
                let http = plan.httpRequest,
                let authType = http.authType,
                credentialTypes.contains(authType)
            else {
                // none / nil authType → skip
                continue
            }

            if authType == .oauth2ClientCredentials {
                // OAuth2 Client Credentials: validate client_id and client_secret keys separately.
                // (Gate 7 already confirmed these fields are non-empty strings.)
                if let clientIDKey = http.oauth2ClientIDKey,
                   !clientIDKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    let result = await secrets.validate(service: clientIDKey)
                    if !result.isValid {
                        missing.append(
                            "Action '\(plan.actionID)' requires the OAuth client_id credential " +
                            "'\(clientIDKey)' for oauth2ClientCredentials auth — \(result.summary)"
                        )
                    }
                }
                if let clientSecretKey = http.oauth2ClientSecretKey,
                   !clientSecretKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    let result = await secrets.validate(service: clientSecretKey)
                    if !result.isValid {
                        missing.append(
                            "Action '\(plan.actionID)' requires the OAuth client_secret credential " +
                            "'\(clientSecretKey)' for oauth2ClientCredentials auth — \(result.summary)"
                        )
                    }
                }
            } else {
                // v1 auth types: validate authSecretKey.
                guard
                    let secretKey = http.authSecretKey,
                    !secretKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                else {
                    // absent authSecretKey was already caught by Gate 7
                    continue
                }

                let result = await secrets.validate(service: secretKey)
                if !result.isValid {
                    missing.append(
                        "Action '\(plan.actionID)' requires credential '\(secretKey)' " +
                        "for \(authType.rawValue) auth — \(result.summary)"
                    )
                }
            }
        }

        guard !missing.isEmpty else { return .pass }

        let items = missing
            .enumerated()
            .map { "  \($0.offset + 1). \($0.element)" }
            .joined(separator: "\n\n")

        return .needsClarification(question: """
            I need API credentials configured in Keychain before I can create the \
            "\(skillName)" skill:

            \(items)

            Please add the required credential(s) in Settings → Keychain, then try again.
            """)
    }
}
