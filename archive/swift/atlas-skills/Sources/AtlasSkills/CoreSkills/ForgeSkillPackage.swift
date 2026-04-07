import Foundation

// MARK: - ForgeSkillPackage

/// A complete, installable Forge skill package.
///
/// A package contains:
/// - The `SkillManifest` describing the skill's identity, category, and risk.
/// - One `ForgeActionPlan` per action, describing how to execute it at runtime.
///
/// Produced by `CoreForgeService.buildPackage(spec:plans:)`.
/// Installed via `CoreSkillService.installForgeSkill(package:actionDefinitions:)`.
///
/// Design rule: the manifest and plans are kept separate from `SkillActionDefinition`
/// so that the agent-facing schema (what the LLM sees) can evolve independently from
/// the execution plan (what the runtime does).
public struct ForgeSkillPackage: Codable, Sendable {
    public let manifest: SkillManifest
    public let actions: [ForgeActionPlan]

    public init(manifest: SkillManifest, actions: [ForgeActionPlan]) {
        self.manifest = manifest
        self.actions = actions
    }

    /// Find the execution plan for a given action ID.
    public func plan(for actionID: String) -> ForgeActionPlan? {
        actions.first { $0.actionID == actionID }
    }
}

// MARK: - ForgeActionPlan

/// Describes how to execute a single Forge skill action at runtime.
///
/// For Forge v1 the only supported type is `.http`.
/// Future types (e.g. `.template`, `.chain`) are reserved and must not be added
/// until the Dynamic Skill Runtime supports them.
public struct ForgeActionPlan: Codable, Sendable {
    public let actionID: String
    public let type: ForgeActionType
    /// The HTTP request plan. Required when `type == .http`, nil otherwise.
    public let httpRequest: HTTPRequestPlan?

    public init(
        actionID: String,
        type: ForgeActionType,
        httpRequest: HTTPRequestPlan? = nil
    ) {
        self.actionID = actionID
        self.type = type
        self.httpRequest = httpRequest
    }
}

// MARK: - ForgeActionType

/// The execution strategy for a Forge action.
/// Forge v1 supports HTTP only.
public enum ForgeActionType: String, Codable, Sendable {
    /// Execute one HTTP request and return the response body.
    case http
}

// MARK: - HTTPRequestPlan

/// Describes the HTTP call for a Forge action.
///
/// ## Parameter routing at runtime
///
/// - `{paramName}` in `url` → substituted into the path (URL-encoded).
/// - GET: remaining input params → appended as URL query items.
/// - POST/PUT:
///   - If `bodyFields` is set: only the listed params are sent, renamed to the
///     specified body keys. Static entries from `staticBodyFields` are merged in.
///   - If `bodyFields` is nil: all input params become the JSON body (legacy flat mode),
///     merged with any `staticBodyFields` entries.
/// - `query`: static key/value pairs appended to **every** request regardless of method.
///
/// ## Auth injection (AuthCore v1)
///
/// Set `authType` + the corresponding auth fields to drive credential injection at runtime.
/// `ForgeSkill` reads the Keychain key named by `authSecretKey` and injects it according
/// to the `authType`. Secret values are NEVER logged.
///
/// | authType            | Required fields                              | Injection                       |
/// |---------------------|----------------------------------------------|---------------------------------|
/// | `none`                    | —                                                              | No auth                                                            |
/// | `apiKeyHeader`            | `authSecretKey`, `authHeaderName`                              | `<authHeaderName>: <value>`                                        |
/// | `apiKeyQuery`             | `authSecretKey`, `authQueryParamName`                          | `?<authQueryParamName>=<value>`                                     |
/// | `bearerTokenStatic`       | `authSecretKey`                                                | `Authorization: Bearer <value>`                                    |
/// | `basicAuth`               | `authSecretKey` (pre-encoded base64 value)                     | `Authorization: Basic <value>`                                     |
/// | `oauth2ClientCredentials` | `oauth2TokenURL`, `oauth2ClientIDKey`, `oauth2ClientSecretKey` | POST token endpoint → `Authorization: Bearer <access_token>`      |
///
/// ### Legacy field (backward compatible)
/// `secretHeader` remains supported for skills created before AuthCore v1.
/// If `authType` is nil, `ForgeSkill` falls back to `secretHeader → Bearer` injection.
/// New skills should use `authType + authSecretKey` instead.
public struct HTTPRequestPlan: Codable, Sendable {
    /// HTTP method: "GET", "POST", "PUT", or "DELETE".
    public let method: String
    /// Base URL. May contain `{paramName}` placeholders.
    public let url: String
    /// Static headers merged into every request.
    public let headers: [String: String]?
    /// Static query parameters appended to every request (any method).
    public let query: [String: String]?

    // MARK: Auth Fields (AuthCore v1)

    /// The authentication type for this plan. Drives credential injection strategy.
    /// If set, takes precedence over the legacy `secretHeader` field.
    /// Nil means legacy behavior: check `secretHeader` for Bearer token injection.
    public let authType: APIAuthType?
    /// Keychain service key for the auth credential.
    /// - `apiKeyHeader`: value injected into the header named by `authHeaderName`.
    /// - `apiKeyQuery`: value appended as the query param named by `authQueryParamName`.
    /// - `bearerTokenStatic`: value injected as `Authorization: Bearer <value>`.
    /// - `basicAuth`: pre-encoded `base64(user:pass)` injected as `Authorization: Basic <value>`.
    /// Nil for `none` auth. Not needed if using the legacy `secretHeader` field.
    public let authSecretKey: String?
    /// For `apiKeyHeader` only: the HTTP header name to inject (e.g. "X-API-Key", "Api-Key").
    /// The value from Keychain (`authSecretKey`) is set as the value of this header.
    public let authHeaderName: String?
    /// For `apiKeyQuery` only: the URL query parameter name (e.g. "api_key", "key", "apiKey").
    /// The value from Keychain (`authSecretKey`) is appended as this query parameter.
    public let authQueryParamName: String?

    // MARK: Auth Fields — OAuth 2.0 Client Credentials

    /// For `oauth2ClientCredentials` only: the token endpoint URL.
    /// E.g. "https://accounts.spotify.com/api/token"
    public let oauth2TokenURL: String?

    /// For `oauth2ClientCredentials` only: Keychain key for the client_id.
    /// E.g. "com.projectatlas.spotify.clientid"
    public let oauth2ClientIDKey: String?

    /// For `oauth2ClientCredentials` only: Keychain key for the client_secret.
    /// E.g. "com.projectatlas.spotify.secret"
    public let oauth2ClientSecretKey: String?

    /// For `oauth2ClientCredentials` only: optional OAuth scope string.
    /// E.g. "read:metrics write:metrics"
    public let oauth2Scope: String?

    // MARK: Body Mapping (POST / PUT)

    /// Explicit mapping of JSON body key → input parameter name for POST and PUT requests.
    ///
    /// When set, only the listed params are included in the request body, keyed as
    /// specified. This handles APIs that use different key names than the skill's
    /// `inputSchema` properties (e.g. `"q"` → `"searchQuery"`).
    ///
    /// String values that look like integers, decimals, or booleans are coerced to
    /// native JSON types automatically.
    ///
    /// Example: `{"q": "searchQuery", "limit": "maxResults"}` sends:
    ///   `{"q": "<value of searchQuery input>", "limit": <int value of maxResults input>}`
    ///
    /// When nil, all input params are sent with their original names (backward compat).
    public let bodyFields: [String: String]?

    /// Static key/value pairs always included in the JSON body of POST and PUT requests.
    ///
    /// These are literal values hardcoded into the plan — not user-supplied inputs.
    /// Merged with the dynamic body (from `bodyFields` or the full input dict).
    /// Values are coerced to native types just like `bodyFields`.
    ///
    /// Example: `{"model": "gpt-4o", "stream": "false"}` always adds those to every request.
    public let staticBodyFields: [String: String]?

    // MARK: Legacy Auth Field

    /// **Deprecated** — use `authType + authSecretKey` for new skills.
    /// Keychain service key whose value is injected as `Authorization: Bearer <token>`.
    /// Only used when `authType` is nil (skills created before AuthCore v1).
    public let secretHeader: String?

    public init(
        method: String,
        url: String,
        headers: [String: String]? = nil,
        query: [String: String]? = nil,
        authType: APIAuthType? = nil,
        authSecretKey: String? = nil,
        authHeaderName: String? = nil,
        authQueryParamName: String? = nil,
        oauth2TokenURL: String? = nil,
        oauth2ClientIDKey: String? = nil,
        oauth2ClientSecretKey: String? = nil,
        oauth2Scope: String? = nil,
        bodyFields: [String: String]? = nil,
        staticBodyFields: [String: String]? = nil,
        secretHeader: String? = nil
    ) {
        self.method = method
        self.url = url
        self.headers = headers
        self.query = query
        self.authType = authType
        self.authSecretKey = authSecretKey
        self.authHeaderName = authHeaderName
        self.authQueryParamName = authQueryParamName
        self.oauth2TokenURL = oauth2TokenURL
        self.oauth2ClientIDKey = oauth2ClientIDKey
        self.oauth2ClientSecretKey = oauth2ClientSecretKey
        self.oauth2Scope = oauth2Scope
        self.bodyFields = bodyFields
        self.staticBodyFields = staticBodyFields
        self.secretHeader = secretHeader
    }
}
