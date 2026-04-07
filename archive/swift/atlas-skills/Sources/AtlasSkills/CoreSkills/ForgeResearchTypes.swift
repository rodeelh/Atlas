import Foundation

// MARK: - ForgeDataQuality

/// Confidence or quality level used in Forge research assessments.
public enum ForgeDataQuality: String, Codable, Sendable {
    case low
    case medium
    case high

    /// Returns true if this quality level meets the minimum bar (medium or high).
    var meetsMinimum: Bool { self == .medium || self == .high }
}

// MARK: - ForgeValidationStatus

/// Whether a dry-run or shape-validation check was attempted for a Forge action plan.
public enum ForgeValidationStatus: String, Codable, Sendable {
    /// No validation was attempted.
    case unknown
    /// Validation was performed and passed.
    case pass
    /// Validation was performed and failed.
    case fail
}

// MARK: - APIResearchContract

/// Structured research contract the agent must populate before proposing an API skill.
///
/// The contract captures what the agent learned while researching the target API.
/// `ForgeValidationGate` evaluates it to decide whether a proposal may be created.
///
/// All fields are optional except `providerName`, `docsQuality`, and `mappingConfidence`
/// — those three are always set by the agent; the gate uses them alongside the others.
///
/// `authType` should always be set for API skills. If the auth mechanism could not be
/// identified, use `.unknown` — it will trigger a gate refusal prompting further research.
///
/// This struct is never persisted to SQLite. It lives only in memory during a
/// `forge.orchestration.propose` call.
public struct APIResearchContract: Codable, Sendable {
    /// Human-readable provider name (e.g. "OpenWeatherMap", "GitHub").
    public let providerName: String
    /// URL of the API documentation page used for research.
    public let docsURL: String?
    /// Quality assessment of the documentation found.
    public let docsQuality: ForgeDataQuality
    /// Base URL of the API (e.g. "https://api.openweathermap.org").
    public let baseURL: String?
    /// Specific endpoint path (e.g. "/data/2.5/weather").
    public let endpoint: String?
    /// HTTP method ("GET", "POST", "PUT", "DELETE", "PATCH").
    public let method: String?
    /// The authentication type this API requires.
    /// Must be one of the `APIAuthType` enum cases. `AuthCore` checks this against
    /// the native support matrix — unsupported types block proposal creation.
    /// Always classify explicitly; do not leave nil if auth exists.
    public let authType: APIAuthType?
    /// Parameter names that the API requires.
    public let requiredParams: [String]
    /// Parameter names that are optional.
    public let optionalParams: [String]
    /// Maps each parameter name to its location: "path", "query", "body", or "header".
    public let paramLocations: [String: String]
    /// Example request body, URL, or curl snippet (documentation purposes only).
    public let exampleRequest: String?
    /// Example response excerpt used to verify field names match reality.
    public let exampleResponse: String?
    /// How confident the agent is that all field names, locations, and types are correct.
    public let mappingConfidence: ForgeDataQuality
    /// Whether a dry-run or shape check was attempted and what the outcome was.
    public let validationStatus: ForgeValidationStatus
    /// Free-form notes about uncertainties, rate limits, or known limitations.
    public let notes: String?
    /// Top-level JSON field names expected in a successful response body.
    /// Used by `APIValidationService` to score response quality via field matching.
    /// Optional — if absent or empty, the inspector grades on structure only.
    public let expectedResponseFields: [String]

    public init(
        providerName: String,
        docsURL: String? = nil,
        docsQuality: ForgeDataQuality,
        baseURL: String? = nil,
        endpoint: String? = nil,
        method: String? = nil,
        authType: APIAuthType? = nil,
        requiredParams: [String] = [],
        optionalParams: [String] = [],
        paramLocations: [String: String] = [:],
        exampleRequest: String? = nil,
        exampleResponse: String? = nil,
        mappingConfidence: ForgeDataQuality,
        validationStatus: ForgeValidationStatus = .unknown,
        notes: String? = nil,
        expectedResponseFields: [String] = []
    ) {
        self.providerName = providerName
        self.docsURL = docsURL
        self.docsQuality = docsQuality
        self.baseURL = baseURL
        self.endpoint = endpoint
        self.method = method
        self.authType = authType
        self.requiredParams = requiredParams
        self.optionalParams = optionalParams
        self.paramLocations = paramLocations
        self.exampleRequest = exampleRequest
        self.exampleResponse = exampleResponse
        self.mappingConfidence = mappingConfidence
        self.validationStatus = validationStatus
        self.notes = notes
        self.expectedResponseFields = expectedResponseFields
    }

    // MARK: - Lenient Codable

    // Custom decoder that tolerates missing or malformed optional collection fields
    // without throwing. `authType` is handled leniently by `APIAuthType.init(from:)`
    // itself — unrecognised raw values map to `.unknown` rather than throwing.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        providerName      = try c.decode(String.self, forKey: .providerName)
        docsURL           = try c.decodeIfPresent(String.self, forKey: .docsURL)
        docsQuality       = try c.decode(ForgeDataQuality.self, forKey: .docsQuality)
        baseURL           = try c.decodeIfPresent(String.self, forKey: .baseURL)
        endpoint          = try c.decodeIfPresent(String.self, forKey: .endpoint)
        method            = try c.decodeIfPresent(String.self, forKey: .method)
        authType          = try c.decodeIfPresent(APIAuthType.self, forKey: .authType)
        requiredParams    = (try? c.decodeIfPresent([String].self, forKey: .requiredParams))   ?? []
        optionalParams    = (try? c.decodeIfPresent([String].self, forKey: .optionalParams))   ?? []
        paramLocations    = (try? c.decodeIfPresent([String: String].self, forKey: .paramLocations))   ?? [:]
        exampleRequest    = try c.decodeIfPresent(String.self, forKey: .exampleRequest)
        exampleResponse   = try c.decodeIfPresent(String.self, forKey: .exampleResponse)
        mappingConfidence = try c.decode(ForgeDataQuality.self, forKey: .mappingConfidence)
        validationStatus  = (try? c.decode(ForgeValidationStatus.self, forKey: .validationStatus)) ?? .unknown
        notes                  = try c.decodeIfPresent(String.self, forKey: .notes)
        expectedResponseFields = (try? c.decodeIfPresent([String].self, forKey: .expectedResponseFields)) ?? []
    }
}
