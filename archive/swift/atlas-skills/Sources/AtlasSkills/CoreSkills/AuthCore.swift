import Foundation

// MARK: - APIAuthType

/// First-class classification of API authentication mechanisms.
///
/// Used by `APIResearchContract` to capture what auth the target API requires,
/// and by `AuthCore` to determine whether Atlas can natively execute it at runtime
/// in a Forge skill.
///
/// When researching an API for Forge, the agent **must** classify the auth type
/// into one of these cases. `unknown` is treated as unsupported — the auth
/// mechanism must be identified before a Forge proposal can be created.
public enum APIAuthType: String, Sendable, CaseIterable {
    /// No authentication required — the API is publicly accessible.
    case none
    /// API key injected as a custom request header (e.g. `X-API-Key: <key>`).
    case apiKeyHeader
    /// API key appended as a URL query parameter (e.g. `?api_key=<key>`).
    case apiKeyQuery
    /// Static Bearer token in `Authorization: Bearer <token>`.
    case bearerTokenStatic
    /// HTTP Basic auth: `Authorization: Basic <base64(user:pass)>`.
    /// The pre-encoded Base64 value must be stored in Keychain by the user.
    case basicAuth
    /// OAuth 2.0 Authorization Code flow — requires browser redirect + token exchange.
    /// Not yet supported in Atlas. Deferred to AuthCore v2.
    case oauth2AuthorizationCode
    /// OAuth 2.0 Client Credentials flow — server-to-server token endpoint.
    /// Supported in Atlas via `OAuth2ClientCredentialsService` + `OAuth2TokenCache`.
    case oauth2ClientCredentials
    /// Non-standard or proprietary authentication scheme that cannot be expressed
    /// using the current credential injection model.
    case customUnsupported
    /// Auth mechanism not yet identified during research, or a legacy/unrecognised
    /// string value from a stored contract blob.
    /// Must be resolved to a concrete type before forging is permitted.
    case unknown
}

// MARK: - APIAuthType Codable
//
// Custom Codable implementation that maps unrecognised raw values to `.unknown`
// rather than throwing `.dataCorrupted`. This provides backward compatibility with
// stored `contractJSON` blobs that predate `APIAuthType` and may contain legacy
// strings like "apiKey", "bearer", or "oauth2".
//
// Encoding always uses the canonical raw value string.
extension APIAuthType: Codable {
    public init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = APIAuthType(rawValue: raw) ?? .unknown
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(rawValue)
    }
}

// MARK: - AuthSupportLevel

/// Whether Atlas can natively inject credentials for a given auth type in a Forge skill.
public enum AuthSupportLevel: String, Sendable {
    /// Atlas can inject this auth type via Keychain-backed credential injection at runtime.
    case supported
    /// Conceptually clear but requires an OAuth token exchange flow not yet implemented.
    /// Deferred to AuthCore v2.
    case requiresFutureOAuthSupport
    /// Atlas cannot support this auth type (custom/proprietary/unidentified).
    case unsupported

    /// True if Forge can proceed with proposal creation for this auth type.
    public var canForge: Bool { self == .supported }
}

// MARK: - AuthCore

/// Native auth classification and support gate for Atlas.
///
/// AuthCore v1 answers one question for Forge: "can Atlas natively execute this auth
/// type in a Forge skill right now?" If not, Forge must refuse the proposal before
/// spending tokens on spec and plan construction.
///
/// ## Supported (Keychain-backed injection via `ForgeSkill`)
///
/// | Auth type                 | Injection mechanism                                               |
/// |---------------------------|-------------------------------------------------------------------|
/// | `none`                    | No credential needed                                              |
/// | `apiKeyHeader`            | `authHeaderName: <value>` header from Keychain                    |
/// | `apiKeyQuery`             | `authQueryParamName=<value>` query param from Keychain            |
/// | `bearerTokenStatic`       | `Authorization: Bearer <value>` from Keychain                     |
/// | `basicAuth`               | `Authorization: Basic <pre-encoded-value>` from Keychain          |
/// | `oauth2ClientCredentials` | POST to `oauth2TokenURL` → cache → `Authorization: Bearer`        |
///
/// ## Deferred to AuthCore v2
/// - `oauth2AuthorizationCode` — browser redirect + token exchange
/// - `customUnsupported` — non-standard; requires case-by-case design
/// - `unknown` — research incomplete; auth type must be identified first
public struct AuthCore {

    // MARK: - Support Matrix

    /// Returns the native support level for a given auth type.
    public static func supportLevel(for authType: APIAuthType) -> AuthSupportLevel {
        switch authType {
        case .none, .apiKeyHeader, .apiKeyQuery, .bearerTokenStatic, .basicAuth,
             .oauth2ClientCredentials:
            return .supported
        case .oauth2AuthorizationCode:
            return .requiresFutureOAuthSupport
        case .customUnsupported, .unknown:
            return .unsupported
        }
    }

    /// Returns true if Atlas can natively execute this auth type in a Forge skill.
    public static func isSupported(_ authType: APIAuthType) -> Bool {
        supportLevel(for: authType).canForge
    }

    // MARK: - Refusal Messages

    /// Returns a clear, user-facing refusal message explaining why this auth type
    /// cannot be forged and what the user can do instead.
    ///
    /// Should only be called for unsupported auth types. The result for supported
    /// types is undefined and must not appear in conversation output.
    public static func refusalMessage(for authType: APIAuthType, skillName: String) -> String {
        switch authType {
        case .oauth2AuthorizationCode:
            return """
                I can't forge "\(skillName)" right now — this API uses OAuth 2.0 \
                Authorization Code, which requires a browser redirect and token exchange \
                flow that Atlas doesn't support yet. This will be possible once AuthCore \
                gains OAuth support in a future release.

                If the API also offers a static API key or personal access token as an \
                alternative, use that and set authType to "apiKeyHeader" or \
                "bearerTokenStatic" instead. If this API offers a service-account or \
                machine-to-machine option (Client Credentials), that is supported — set \
                authType to "oauth2ClientCredentials" instead.
                """
        case .customUnsupported:
            return """
                I can't forge "\(skillName)" — this API uses a custom or proprietary \
                authentication method that Atlas can't safely execute automatically.

                If the API also supports a standard auth method (API key, Bearer token), \
                reclassify the authType to "apiKeyHeader", "apiKeyQuery", or \
                "bearerTokenStatic" and try again.
                """
        case .unknown:
            return """
                I can't forge "\(skillName)" — the authentication method for this API \
                hasn't been identified yet. Please research the API documentation further, \
                determine the auth type, and try again.

                Supported auth types: none, apiKeyHeader, apiKeyQuery, bearerTokenStatic, basicAuth.
                """
        case .none, .apiKeyHeader, .apiKeyQuery, .bearerTokenStatic, .basicAuth,
             .oauth2ClientCredentials:
            // Supported — this function should not be called for these types.
            return "Auth type '\(authType.rawValue)' is supported — no refusal needed."
        }
    }
}
