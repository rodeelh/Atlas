import Foundation
import AtlasLogging
import AtlasShared
import AtlasTools

// MARK: - Forge Orchestration Handler Types

/// Closure bundle injected from `atlas-core` into `ForgeOrchestrationSkill`.
/// This avoids a circular import — `atlas-skills` cannot import `atlas-core`.
public struct ForgeOrchestrationHandlers: Sendable {
    /// Start a researching indicator visible in the Forge UI. Returns the item ID.
    public let startResearching: @Sendable (String, String) async -> UUID
    /// Stop (remove) a researching indicator by ID.
    public let stopResearching: @Sendable (UUID) async -> Void
    /// Create and persist a Forge proposal. Throws on failure.
    /// Parameters: spec, plans, summary, rationale, contractJSON
    public let createProposal: @Sendable (ForgeSkillSpec, [ForgeActionPlan], String, String?, String?) async throws -> ForgeProposalRecord

    public init(
        startResearching: @escaping @Sendable (String, String) async -> UUID,
        stopResearching: @escaping @Sendable (UUID) async -> Void,
        createProposal: @escaping @Sendable (ForgeSkillSpec, [ForgeActionPlan], String, String?, String?) async throws -> ForgeProposalRecord
    ) {
        self.startResearching = startResearching
        self.stopResearching = stopResearching
        self.createProposal = createProposal
    }
}

// MARK: - ForgeOrchestrationSkill

/// Built-in skill that lets the agent propose new Forge skills during a conversation.
///
/// ## Forge v1.4 Research + Validation Gating
///
/// For API skills (`kind == .api`) the agent MUST provide a populated
/// `APIResearchContract` (via `contract_json`) before a proposal can be created.
/// The full gate sequence for API proposals:
///
/// Contract gates (run against `APIResearchContract`):
/// 1. `docsQuality` >= medium
/// 2. `mappingConfidence` == high
/// 3. `endpoint` defined
/// 4. `method` is a known HTTP verb
/// 5. All required param locations are known
/// 6. `authType` (if set) is natively supported by `AuthCore`
///
/// Plan gates (run against `[ForgeActionPlan]`):
/// 7. Auth field completeness — every plan's typed `authType` has all required
///    companion fields (`authSecretKey`, `authHeaderName`, `authQueryParamName`)
/// 8. Credential readiness — required Keychain secrets actually exist
///    (requires injected `CoreSecretsService`; skipped when nil)
///
/// Post-gate validation:
/// - Dry-run — a real HTTP GET is attempted for each GET plan to verify the API
///   is reachable and responds acceptably (requires injected `ForgeDryRunValidator`)
///
/// If any gate or dry-run fails, the skill returns a refusal/clarification with no
/// proposal persisted. Non-API skills bypass all API gates.
///
/// Dependency injection:
/// - `handlers`: Provided by `AgentRuntime` after `ForgeProposalService` is ready.
///   If nil (daemon still bootstrapping), the skill returns a graceful "not ready" message.
/// - `secretsService`: Enables Gate 8. Injected by `AgentRuntime` via `BuiltInSkillsProvider`.
///   Tests that do not inject this service skip Gate 8.
/// - `dryRunValidator`: Enables pre-proposal HTTP validation. Injected by `AgentRuntime`.
///   Tests that do not inject this skip the dry-run.
public struct ForgeOrchestrationSkill: AtlasSkill {

    // MARK: - Static

    public static let skillID = "forge.orchestration"

    /// Injected into the agent system prompt when this skill is enabled.
    public static let systemPromptBlock = """
    ## Forge: Creating Custom API Skills

    You can propose new skills for external APIs using the forge.orchestration.propose tool.
    The user must review and approve any proposal before the skill is installed or callable.

    **When to use Forge:**
    - The user explicitly asks you to create, forge, add, or build a skill for an external API or web service.
    - No existing built-in skill covers the requested integration.

    **When NOT to use Forge:**
    - Do NOT use Forge for one-off web lookups — use the web or search skills instead.
    - Do NOT use Forge speculatively without a clear user request.
    - Do NOT use Forge when the request is too vague — ask a clarifying question first.

    **Step 1 — Classify the skill kind:**
    Before anything else, decide what kind of skill this is:
    - `api` — calls an external HTTP API (most common)
    - `composed` — chains two or more existing Atlas skills
    - `transform` — converts data from one format to another
    - `workflow` — sequences multiple steps with data flowing between them

    **Step 2 — For `api` skills only: research the API first.**
    You MUST research the API and populate `contract_json` before calling forge.orchestration.propose.
    Use the web research skill to find the official API documentation.

    Before setting mappingConfidence to "high", verify ALL of the following from the docs:
    □ The exact base URL (scheme + host, e.g. "https://api.example.com")
    □ The exact endpoint path, including any required path parameters (e.g. "/v1/users/{userID}")
    □ Every required parameter: its exact name AND where it goes (path / query / body)
    □ The HTTP method (GET, POST, PUT, PATCH, DELETE) — do not assume GET
    □ The auth mechanism: exact header name, query param name, or token endpoint
    □ At least one complete example response showing actual field names and types
    □ All required fields in the JSON response that will be displayed to the user

    The contract_json must be a JSON object with these fields:
    {
      "providerName": "Human-readable API name",
      "docsURL": "https://docs.example.com/api",
      "docsQuality": "low|medium|high",
      "baseURL": "https://api.example.com",
      "endpoint": "/v1/resource/{param}",
      "method": "GET",
      "authType": "none|apiKeyHeader|apiKeyQuery|bearerTokenStatic|basicAuth|oauth2AuthorizationCode|oauth2ClientCredentials|customUnsupported|unknown",
      "requiredParams": ["param1", "param2"],
      "optionalParams": ["limit"],
      "paramLocations": {"param1": "path", "param2": "query"},
      "exampleRequest": "GET /v1/resource/abc?format=json",
      "exampleResponse": "{\\"id\\": \\"abc\\", \\"value\\": 42, \\"name\\": \\"Example\\"}",
      "expectedResponseFields": ["id", "value", "name"],
      "mappingConfidence": "low|medium|high",
      "validationStatus": "unknown|pass|fail",
      "notes": "Optional notes about limitations or uncertainties"
    }

    IMPORTANT: `exampleResponse` must be a real response example from the docs (not a placeholder).
    `expectedResponseFields` must list the top-level JSON field names that will appear in a success response.
    Both are required when mappingConfidence is "high".

    **Auth type classification (AuthCore v1):**
    Always set authType to the most specific matching case:
    - "none" — no auth required (public API)
    - "apiKeyHeader" — API key in a custom header (e.g. X-API-Key, Api-Key)
    - "apiKeyQuery" — API key as a URL query parameter (e.g. ?api_key=...)
    - "bearerTokenStatic" — static Bearer token in Authorization header
    - "basicAuth" — HTTP Basic auth (user:pass, base64-encoded)
    - "oauth2AuthorizationCode" — OAuth browser flow → NOT supported, Forge will refuse
    - "oauth2ClientCredentials" — OAuth 2.0 server-to-server (Client Credentials) → SUPPORTED.
      Use for APIs that issue tokens via a token endpoint (Spotify, Salesforce, Slack, etc.).
      Requires oauth2TokenURL, oauth2ClientIDKey, oauth2ClientSecretKey.
      Optionally include oauth2Scope if the API requires a scope parameter.
    - "customUnsupported" — non-standard auth → NOT supported, Forge will refuse
    - "unknown" — could not identify auth → NOT supported, research further first

    **Contract quality gates (API skills only) — proposal is REFUSED unless ALL pass:**
    - docsQuality must be "medium" or "high"
    - mappingConfidence must be "high" — you are certain about every field name, location, and type
    - endpoint must be defined
    - method must be a valid HTTP verb
    - paramLocations must be defined for every entry in requiredParams
    - authType must be natively supported (none, apiKeyHeader, apiKeyQuery, bearerTokenStatic, basicAuth, oauth2ClientCredentials)
      → oauth2ClientCredentials is fully supported; use it for machine-to-machine flows
      → oauth2AuthorizationCode, custom, and unknown auth types are hard refusals in this version

    If you are uncertain about any field → set mappingConfidence to "low" or "medium"
    and ask the user for clarification BEFORE calling forge.orchestration.propose.
    Never guess on field names, locations, or auth schemes.

    **Step 3 — Design the spec and plans.**

    **How the Atlas runtime routes agent inputs to HTTP calls — read carefully:**

    URL path substitution:
    - Any `{paramName}` in the `url` field is replaced with the agent-supplied input value.
    - Example: url="/v1/users/{userID}", input={"userID":"42"} → path becomes /v1/users/42.
    - A param substituted into the path is NOT also sent as a query parameter or body field.

    GET requests — remaining inputs become query parameters:
    - All input params NOT substituted into the path are appended as URL query items.
    - Example: url="/v1/search", input={"q":"cats","limit":"10"} → ?q=cats&limit=10.
    - The `query` field in the plan is for STATIC values hardcoded into EVERY request
      (e.g. format=json, version=2). Do NOT put user-supplied params in `query`.

    POST / PUT / PATCH requests — inputs become the JSON request body:
    - By default: all agent input params are serialised as a flat JSON object body.
    - Use `bodyFields` when the API's body key names differ from the skill's inputSchema names.
      Format: {"apiBodyKey": "inputParamName"}. Only listed params are included.
      Example: {"q": "searchQuery", "maxResults": "limit"} sends {"q": "...", "maxResults": 10}.
    - Use `staticBodyFields` for literal values that must be included in every request body
      regardless of user input (e.g. {"model": "gpt-4o", "stream": "false"}).
    - String inputs that look like integers, decimals, or booleans are coerced automatically.

    Other rules:
    1. Use a short, lowercase-hyphenated skill ID (e.g. "github-repos", "openweather").
    2. Action IDs must be namespaced: "<skillID>.<verb-noun>" (e.g. "openweather.current-conditions").
    3. For auth: set `authType` to the correct type and `authSecretKey` to the Keychain service name
       (e.g. "com.projectatlas.myapi"). The user stores the credential in Settings → Keychain.
       See the AuthCore v1 auth examples in the plans_json documentation below.

    **After creating a proposal:** tell the user concisely that the draft is ready for review in
    the Forge panel. Do not dump raw JSON or claim the skill is installed. Example:
    "I've drafted a skill proposal for that. You can review and install it from the Forge panel."

    **ForgeSkillSpec format (spec_json):**
    {"id":"skill-id","name":"Skill Name","description":"What this skill does.","category":"utility","riskLevel":"low","tags":["tag"],"actions":[{"id":"skill-id.action-name","name":"Action Name","description":"What this action does.","permissionLevel":"read","inputSchema":{"type":"object","properties":{"param":{"type":"string","description":"Description"}},"required":["param"],"additionalProperties":false}}]}

    Valid categories: system, utility, creative, communication, automation, research, developer, productivity
    Valid risk levels: low (public read-only), medium (authenticated/write), high (critical operations)
    Valid permission levels: read, draft, execute

    **ForgeActionPlan format (plans_json) — AuthCore v1 auth fields:**
    Use authType + authSecretKey (and optionally authHeaderName / authQueryParamName) for all new skills.
    The secretHeader field is legacy (pre-AuthCore) and only injects Bearer — prefer authType instead.

    Auth examples by type:
    - none:                    {"authType":"none"}
    - apiKeyHeader:            {"authType":"apiKeyHeader","authSecretKey":"com.projectatlas.myapi","authHeaderName":"X-API-Key"}
    - apiKeyQuery:             {"authType":"apiKeyQuery","authSecretKey":"com.projectatlas.myapi","authQueryParamName":"api_key"}
    - bearerTokenStatic:       {"authType":"bearerTokenStatic","authSecretKey":"com.projectatlas.myapi"}
    - basicAuth:               {"authType":"basicAuth","authSecretKey":"com.projectatlas.myapi"}
                               (user stores pre-encoded base64 value in Keychain; Atlas injects as Authorization: Basic)
    - oauth2ClientCredentials: {"authType":"oauth2ClientCredentials","oauth2TokenURL":"https://accounts.spotify.com/api/token","oauth2ClientIDKey":"com.projectatlas.spotify.clientid","oauth2ClientSecretKey":"com.projectatlas.spotify.secret","oauth2Scope":"user-read-playback-state"}

    Full plan example (all optional fields shown — set unused ones to null):
    [{"actionID":"skill-id.action-name","type":"http","httpRequest":{"method":"GET","url":"https://api.example.com/endpoint/{param}","headers":{},"query":{"static_key":"static_value"},"authType":"apiKeyHeader","authSecretKey":"com.projectatlas.myapi","authHeaderName":"X-API-Key","authQueryParamName":null,"oauth2TokenURL":null,"oauth2ClientIDKey":null,"oauth2ClientSecretKey":null,"oauth2Scope":null,"bodyFields":null,"staticBodyFields":null,"secretHeader":null}}]

    POST body mapping example — API uses "q" but skill inputSchema uses "searchQuery":
    {"method":"POST","url":"https://api.example.com/search","authType":"bearerTokenStatic","authSecretKey":"com.projectatlas.myapi","bodyFields":{"q":"searchQuery","limit":"maxResults"},"staticBodyFields":{"model":"gpt-4o"}}
    """

    // MARK: - AtlasSkill

    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let handlers: ForgeOrchestrationHandlers?
    /// Gate 8: credential readiness check. Nil → Gate 8 skipped (test environments).
    private let secretsService: CoreSecretsService?
    /// Post-gate dry-run validator. Nil → dry-run skipped (test environments).
    private let dryRunValidator: ForgeDryRunValidator?
    /// API Validation Service — runs BEFORE gates for API skills. Nil → skipped (test environments).
    private let apiValidationService: APIValidationService?

    public init(
        handlers: ForgeOrchestrationHandlers? = nil,
        secretsService: CoreSecretsService? = nil,
        dryRunValidator: ForgeDryRunValidator? = nil,
        apiValidationService: APIValidationService? = nil
    ) {
        self.handlers = handlers
        self.secretsService = secretsService
        self.dryRunValidator = dryRunValidator
        self.apiValidationService = apiValidationService

        self.manifest = SkillManifest(
            id: ForgeOrchestrationSkill.skillID,
            name: "Forge Orchestration",
            version: "1.4.0",
            description: "Propose new custom API skills. Use forge.orchestration.propose when the user explicitly asks to create or build a skill for an external API.",
            category: .system,
            lifecycleState: .installed,
            capabilities: [],
            requiredPermissions: [.draftWrite],
            riskLevel: .low,
            trustProfile: .localExact,
            freshnessType: .staticKnowledge,
            preferredQueryTypes: [],
            routingPriority: 90,
            canAnswerStructuredLiveData: false,
            canHandleLocalData: false,
            canHandleExploratoryQueries: false,
            restrictionsSummary: ["Creates pending proposals only — user must approve before installation"],
            supportsReadOnlyMode: false,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["forge", "skills", "api", "integrations"],
            intent: .atlasSystemTask,
            triggers: [
                .init("forge a skill"),
                .init("forge a skill for"),
                .init("create a skill for"),
                .init("build a skill for"),
                .init("add a skill for"),
                .init("make a skill for"),
                .init("create an api skill"),
                .init("create an integration"),
                .init("add API integration"),
                .init("build an integration for")
            ]
        )

        self.actions = [
            SkillActionDefinition(
                id: "forge.orchestration.propose",
                name: "Propose Forge Skill",
                description: "Create a pending Forge skill proposal. For API skills you MUST research the API first and provide contract_json — the proposal is refused if the contract fails quality gates. For non-API skills (composed/transform/workflow) contract_json is not required.",
                inputSchemaSummary: "kind (optional, default api), spec_json (required), plans_json (required), summary (required), contract_json (required for api kind), rationale (optional)",
                outputSchemaSummary: "Proposal confirmation, clarification request, or refusal with explanation.",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                routingPriority: 90,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "kind": AtlasToolInputProperty(
                            type: "string",
                            description: "Skill kind: 'api' (calls external HTTP API), 'composed' (chains Atlas skills), 'transform' (converts data), 'workflow' (sequences steps). Defaults to 'api' if omitted."
                        ),
                        "contract_json": AtlasToolInputProperty(
                            type: "string",
                            description: "Required for api skills. JSON-encoded APIResearchContract capturing what you learned from researching the API: providerName, docsURL, docsQuality (low/medium/high), baseURL, endpoint, method, authType, requiredParams, optionalParams, paramLocations (param→location map), exampleRequest, exampleResponse, mappingConfidence (must be high to pass gate), validationStatus, notes."
                        ),
                        "spec_json": AtlasToolInputProperty(
                            type: "string",
                            description: "JSON-encoded ForgeSkillSpec. Must include: id (lowercase-hyphenated, no spaces), name, description, category (utility/research/developer/productivity/communication/automation/creative/system), riskLevel (low/medium/high), tags (array), and actions (array with id, name, description, permissionLevel, inputSchema)."
                        ),
                        "plans_json": AtlasToolInputProperty(
                            type: "string",
                            description: "JSON-encoded array of ForgeActionPlan. Each element: {\"actionID\":\"<id>\",\"type\":\"http\",\"httpRequest\":{\"method\":\"GET|POST|PUT|PATCH|DELETE\",\"url\":\"https://...\",\"headers\":{},\"query\":{\"staticKey\":\"staticValue\"},\"authType\":\"none|apiKeyHeader|apiKeyQuery|bearerTokenStatic|basicAuth|oauth2ClientCredentials\",\"authSecretKey\":\"com.projectatlas.myapi\",\"authHeaderName\":\"X-API-Key\",\"authQueryParamName\":null,\"oauth2TokenURL\":null,\"oauth2ClientIDKey\":null,\"oauth2ClientSecretKey\":null,\"oauth2Scope\":null,\"bodyFields\":{\"apiKey\":\"inputParamName\"},\"staticBodyFields\":{\"model\":\"gpt-4o\"},\"secretHeader\":null}}. Use {paramName} in the URL for path substitution — substituted path params are not re-added as query or body params. For GET: remaining input params become URL query items. For POST/PUT/PATCH: use bodyFields to map input param names to API body key names (only listed params are sent); use staticBodyFields for literal values always included in every request body. When bodyFields is null, all input params are sent with their original names. Set authType + authSecretKey for v1 credential injection. For oauth2ClientCredentials, set oauth2TokenURL, oauth2ClientIDKey, oauth2ClientSecretKey (and optionally oauth2Scope) instead of authSecretKey. The query field is ONLY for static key/value pairs that go on every request — do not put user-supplied params there."
                        ),
                        "summary": AtlasToolInputProperty(
                            type: "string",
                            description: "Human-readable explanation of what this skill does and why it is useful. Displayed to the user in the Forge approval UI."
                        ),
                        "rationale": AtlasToolInputProperty(
                            type: "string",
                            description: "Optional: why you are proposing this skill now — what user request triggered it."
                        )
                    ],
                    required: ["spec_json", "plans_json", "summary"]
                )
            )
        ]
    }

    // MARK: - Validation

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        SkillValidationResult(
            skillID: manifest.id,
            status: .passed,
            summary: "Forge Orchestration skill is ready."
        )
    }

    // MARK: - Execution

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        guard actionID == "forge.orchestration.propose" else {
            throw AtlasToolError.invalidInput(
                "ForgeOrchestrationSkill does not define action '\(actionID)'."
            )
        }

        // Guard: handlers must be injected (requires ForgeProposalService to be ready).
        guard let handlers else {
            return SkillExecutionResult(
                skillID: manifest.id,
                actionID: actionID,
                output: "Forge Orchestration is not yet ready — the runtime is still initialising. Please try again in a moment.",
                success: false,
                summary: "Forge runtime not ready."
            )
        }

        let args = (try? input.dictionary()) ?? [:]

        // ── Required inputs ────────────────────────────────────────────────────
        // These are returned as structured refusals rather than throws so they
        // degrade gracefully through SkillActionToolAdapter (PATH A) and never
        // surface as the generic "Atlas could not complete the requested tool call."
        guard let specJSON = args["spec_json"], !specJSON.isEmpty else {
            return SkillExecutionResult(
                skillID: manifest.id, actionID: actionID,
                output: "forge.orchestration.propose requires 'spec_json'. Provide a JSON-encoded ForgeSkillSpec.",
                success: false, summary: "Forge: missing spec_json argument."
            )
        }
        guard let plansJSON = args["plans_json"], !plansJSON.isEmpty else {
            return SkillExecutionResult(
                skillID: manifest.id, actionID: actionID,
                output: "forge.orchestration.propose requires 'plans_json'. Provide a JSON-encoded array of ForgeActionPlan.",
                success: false, summary: "Forge: missing plans_json argument."
            )
        }
        guard let summary = args["summary"], !summary.isEmpty else {
            return SkillExecutionResult(
                skillID: manifest.id, actionID: actionID,
                output: "forge.orchestration.propose requires 'summary'. Provide a brief description of what the skill does.",
                success: false, summary: "Forge: missing summary argument."
            )
        }
        let rationale = args["rationale"]

        // ── Skill kind (default .api for backward compatibility) ───────────────
        let kindRaw = (args["kind"] ?? "api").lowercased().trimmingCharacters(in: .whitespacesAndNewlines)
        let kind = ForgeSkillKind(rawValue: kindRaw) ?? .api

        // ── Decode ForgeSkillSpec (needed for name in gate messages) ───────────
        let spec: ForgeSkillSpec
        do {
            spec = try JSONDecoder().decode(ForgeSkillSpec.self, from: Data(specJSON.utf8))
        } catch {
            return SkillExecutionResult(
                skillID: manifest.id, actionID: actionID,
                output: "Could not decode spec_json as ForgeSkillSpec: \(error.localizedDescription). " +
                        "Ensure spec_json is well-formed JSON matching the ForgeSkillSpec format.",
                success: false, summary: "Forge: spec_json decode failed."
            )
        }

        // ── Decode [ForgeActionPlan] ────────────────────────────────────────────
        let plans: [ForgeActionPlan]
        do {
            plans = try JSONDecoder().decode([ForgeActionPlan].self, from: Data(plansJSON.utf8))
        } catch {
            return SkillExecutionResult(
                skillID: manifest.id, actionID: actionID,
                output: "Could not decode plans_json as [ForgeActionPlan]: \(error.localizedDescription). " +
                        "Ensure plans_json is a well-formed JSON array matching the ForgeActionPlan format.",
                success: false, summary: "Forge: plans_json decode failed."
            )
        }

        // ── VALIDATION GATE ────────────────────────────────────────────────────
        var persistedContractJSON: String?

        // ── API Validation (runs BEFORE all gates, API skills only) ──────────────
        // When an APIValidationService is injected, validate the real API endpoint
        // before spending tokens on gate evaluation or proposal creation.
        // If the service is nil (test environments), this step is silently skipped.
        //
        // Note: The first plan's HTTP details are used for the validation request.
        // If validation rejects or needs revision, we return early — no proposal is created.
        if kind == .api, let validationService = apiValidationService {
            // Select the primary GET-capable plan for validation.
            // Using the first GET plan (not plans.first) ensures we don't skip a
            // valid GET endpoint because an earlier POST plan happens to come first.
            let primaryGetPlan = plans.first(where: {
                $0.httpRequest?.method.uppercased() == "GET"
            })
            if let primaryPlan = primaryGetPlan, let http = primaryPlan.httpRequest {
                // Attempt to extract providerName, baseURL, endpoint, and params from the
                // contract JSON (if present), falling back to plan-level data.
                let contractForValidation = args["contract_json"].flatMap {
                    try? JSONDecoder().decode(APIResearchContract.self, from: Data($0.utf8))
                }
                let providerName = contractForValidation?.providerName ?? spec.name
                // Parse base URL and endpoint from the plan URL. The plan URL is the full URL,
                // so we extract base (scheme+host) and use the contract endpoint if available.
                let planURL = URL(string: http.url)
                let baseURL: String
                let endpoint: String
                if let planURL, let host = planURL.host, let scheme = planURL.scheme {
                    baseURL = "\(scheme)://\(host)"
                    endpoint = contractForValidation?.endpoint ?? planURL.path
                } else {
                    baseURL = contractForValidation?.baseURL ?? http.url
                    endpoint = contractForValidation?.endpoint ?? ""
                }

                let validationRequest = APIValidationRequest(
                    providerName: providerName,
                    baseURL: baseURL,
                    endpoint: endpoint,
                    method: http.method,
                    authType: http.authType?.rawValue ?? "none",
                    authSecretKey: http.authSecretKey,
                    authHeaderName: http.authHeaderName,
                    authQueryParamName: http.authQueryParamName,
                    requiredParams: contractForValidation?.requiredParams ?? [],
                    paramLocations: contractForValidation?.paramLocations ?? [:],
                    exampleInputs: [],
                    expectedFields: contractForValidation?.expectedResponseFields ?? [],
                    useCaseSummary: summary
                )
                let validationResult = await validationService.validate(validationRequest)

                switch validationResult.recommendation {
                case .reject:
                    context.logger.warning("ForgeOrchestration: API validation rejected proposal", metadata: [
                        "skill_id": spec.id,
                        "failure": validationResult.failureReason ?? "unknown"
                    ])
                    return SkillExecutionResult(
                        skillID: manifest.id,
                        actionID: actionID,
                        output: """
                            API validation rejected this proposal.

                            \(validationResult.failureReason ?? "The API endpoint returned an unacceptable response.")

                            Please check the endpoint URL, authentication configuration, and try again. \
                            You can also research the API further to ensure the contract details are correct.
                            """,
                        success: false,
                        summary: "Forge refused — API validation rejected."
                    )

                case .needsRevision:
                    context.logger.info("ForgeOrchestration: API validation needs revision", metadata: [
                        "skill_id": spec.id
                    ])
                    return SkillExecutionResult(
                        skillID: manifest.id,
                        actionID: actionID,
                        output: """
                            API validation completed but the response needs attention before this \
                            skill can be created.

                            \(validationResult.failureReason ?? "The API response did not match expectations.")

                            Confidence: \(String(format: "%.0f%%", validationResult.confidence * 100))

                            Please review the API configuration and try again with adjusted parameters.
                            """,
                        success: false,
                        summary: "Forge needs revision — API validation flagged issues."
                    )

                case .usable:
                    context.logger.info("ForgeOrchestration: API validation passed", metadata: [
                        "skill_id": spec.id,
                        "confidence": String(format: "%.2f", validationResult.confidence)
                    ])
                    // Proceed to existing gate sequence

                case .skipped:
                    context.logger.info("ForgeOrchestration: API validation skipped (non-GET plan)", metadata: [
                        "skill_id": spec.id,
                        "method": http.method
                    ])
                    // Non-GET plans cannot be live-validated — proceed to gates
                }
            } else {
                // No GET-capable plan found — API validation cannot run.
                // Log a warning and proceed to Forge gates; the dry-run validator
                // will also skip non-GET plans, so this path is expected for write-only APIs.
                context.logger.warning(
                    "ForgeOrchestration: API validation skipped — no GET-capable plan found",
                    metadata: [
                        "skill_id": spec.id,
                        "plan_count": "\(plans.count)",
                        "methods": plans.compactMap { $0.httpRequest?.method }.joined(separator: ", ")
                    ]
                )
            }
        }

        switch kind {
        case .api:
            // API skills require a research contract. Return a structured refusal if missing or
            // malformed — do NOT throw, so the failure degrades as a Forge response (PATH A),
            // never as the generic "Atlas could not complete the requested tool call."
            guard let contractJSON = args["contract_json"], !contractJSON.isEmpty else {
                return SkillExecutionResult(
                    skillID: manifest.id, actionID: actionID,
                    output: "forge.orchestration.propose requires 'contract_json' for API skills. " +
                            "Research the target API first, then provide a populated APIResearchContract. " +
                            "Set kind to 'composed', 'transform', or 'workflow' if this is not an HTTP API skill.",
                    success: false, summary: "Forge: missing contract_json for API skill."
                )
            }

            let contract: APIResearchContract
            do {
                contract = try JSONDecoder().decode(APIResearchContract.self, from: Data(contractJSON.utf8))
            } catch {
                return SkillExecutionResult(
                    skillID: manifest.id, actionID: actionID,
                    output: "Could not decode contract_json as APIResearchContract: \(error.localizedDescription). " +
                            "Ensure contract_json is a valid JSON object with providerName, docsQuality, mappingConfidence, and other required fields.",
                    success: false, summary: "Forge: contract_json decode failed."
                )
            }

            let gateOutcome = ForgeValidationGate().evaluate(contract: contract, skillName: spec.name)
            switch gateOutcome {
            case .refuse(let message):
                context.logger.warning("ForgeOrchestration: gate refused proposal", metadata: [
                    "skill_id": spec.id,
                    "docs_quality": contract.docsQuality.rawValue,
                    "mapping_confidence": contract.mappingConfidence.rawValue
                ])
                return SkillExecutionResult(
                    skillID: manifest.id,
                    actionID: actionID,
                    output: message,
                    success: false,
                    summary: "Forge refused — API contract failed quality gates."
                )
            case .needsClarification(let question):
                context.logger.info("ForgeOrchestration: gate needs clarification", metadata: [
                    "skill_id": spec.id
                ])
                return SkillExecutionResult(
                    skillID: manifest.id,
                    actionID: actionID,
                    output: question,
                    success: false,
                    summary: "Forge needs clarification before creating proposal."
                )
            case .pass:
                context.logger.info("ForgeOrchestration: contract passed gate", metadata: [
                    "skill_id": spec.id,
                    "provider": contract.providerName
                ])
                persistedContractJSON = contractJSON
            }

            // ── Gate 7: auth plan field completeness ──────────────────────────
            // Checks that every HTTPRequestPlan whose authType is explicitly set
            // also has the companion fields needed for runtime injection.
            let gate7 = ForgeValidationGate().evaluatePlans(plans, skillName: spec.name)
            switch gate7 {
            case .refuse(let message):
                context.logger.warning("ForgeOrchestration: gate 7 refused proposal", metadata: [
                    "skill_id": spec.id
                ])
                return SkillExecutionResult(
                    skillID: manifest.id, actionID: actionID,
                    output: message, success: false,
                    summary: "Forge refused — auth plan fields incomplete."
                )
            case .needsClarification(let question):
                context.logger.info("ForgeOrchestration: gate 7 needs clarification", metadata: [
                    "skill_id": spec.id
                ])
                return SkillExecutionResult(
                    skillID: manifest.id, actionID: actionID,
                    output: question, success: false,
                    summary: "Forge needs clarification on auth plan fields."
                )
            case .pass:
                break
            }

            // ── Gate 8: credential readiness ──────────────────────────────────
            // Only runs when a CoreSecretsService was injected. Skipped in test
            // environments that do not provide one.
            if let secrets = secretsService {
                let gate8 = await ForgeCredentialGate().evaluate(
                    plans: plans, skillName: spec.name, secrets: secrets
                )
                switch gate8 {
                case .refuse(let message):
                    context.logger.warning("ForgeOrchestration: gate 8 refused proposal", metadata: [
                        "skill_id": spec.id
                    ])
                    return SkillExecutionResult(
                        skillID: manifest.id, actionID: actionID,
                        output: message, success: false,
                        summary: "Forge refused — credential check failed."
                    )
                case .needsClarification(let question):
                    context.logger.info("ForgeOrchestration: gate 8 needs clarification", metadata: [
                        "skill_id": spec.id
                    ])
                    return SkillExecutionResult(
                        skillID: manifest.id, actionID: actionID,
                        output: question, success: false,
                        summary: "Forge needs credential configured before proceeding."
                    )
                case .pass:
                    break
                }
            }

            // ── Dry-run validation ─────────────────────────────────────────────
            // Only runs when a ForgeDryRunValidator was injected. Skipped in test
            // environments that do not provide one.
            if let dryRun = dryRunValidator {
                let dryRunResult = await dryRun.validate(plans: plans, skillName: spec.name)
                if case .fail(let reason) = dryRunResult {
                    context.logger.warning("ForgeOrchestration: dry-run failed", metadata: [
                        "skill_id": spec.id
                    ])
                    return SkillExecutionResult(
                        skillID: manifest.id,
                        actionID: actionID,
                        output: """
                            I attempted to validate this API request and it appears invalid \
                            or unreachable.

                            \(reason)

                            Please check the endpoint URL, base URL, and API configuration, \
                            then try again.
                            """,
                        success: false,
                        summary: "Forge refused — dry-run validation failed."
                    )
                }
                // .pass and .skipped both allow the proposal to proceed
            }

        case .composed:
            // For v1.2: no API gate. Spec/plans validation below is sufficient.
            // Future: verify referenced skill IDs exist in the live registry.
            break

        case .transform, .workflow:
            // For v1.2: no API gate. Input schema and spec structure validation below.
            break
        }

        // ── Spec validation (all kinds) ────────────────────────────────────────
        let validation = CoreForgeService().validate(spec: spec)
        if !validation.isValid {
            return SkillExecutionResult(
                skillID: manifest.id, actionID: actionID,
                output: "Forge spec validation failed:\n" +
                        validation.issues.map { "• \($0)" }.joined(separator: "\n") +
                        "\nFix these issues and call forge.orchestration.propose again.",
                success: false, summary: "Forge: spec validation failed."
            )
        }

        // ── Start researching indicator ────────────────────────────────────────
        let researchingID = await handlers.startResearching(
            "Forging: \(spec.name)",
            "Generating skill proposal for \(spec.name)…"
        )

        // ── Create and persist the proposal ───────────────────────────────────
        do {
            let proposal = try await handlers.createProposal(spec, plans, summary, rationale, persistedContractJSON)
            await handlers.stopResearching(researchingID)

            context.logger.info("ForgeOrchestration: proposal created", metadata: [
                "proposal_id": proposal.id.uuidString,
                "skill_id": proposal.skillID,
                "kind": kind.rawValue
            ])

            let domainsNote = proposal.domains.isEmpty
                ? "no external domains"
                : proposal.domains.joined(separator: ", ")

            let output = """
            Forge proposal created.

            Proposal ID: \(proposal.id.uuidString)
            Skill: \(proposal.name) (\(proposal.skillID))
            Actions: \(proposal.actionNames.joined(separator: ", "))
            Domains: \(domainsNote)
            Risk level: \(proposal.riskLevel)

            The proposal is pending your review. Open the Skills → Forge panel to inspect, install, and enable it. The skill will not be active until you approve it.
            """

            return SkillExecutionResult(
                skillID: manifest.id,
                actionID: actionID,
                output: output,
                success: true,
                summary: "Forge proposal '\(proposal.name)' created — pending user approval.",
                metadata: [
                    "proposal_id": proposal.id.uuidString,
                    "skill_id": proposal.skillID
                ]
            )

        } catch {
            // Always clear the researching indicator even on failure.
            await handlers.stopResearching(researchingID)

            throw AtlasToolError.executionFailed(
                "Forge proposal creation failed: \(error.localizedDescription)"
            )
        }
    }
}
