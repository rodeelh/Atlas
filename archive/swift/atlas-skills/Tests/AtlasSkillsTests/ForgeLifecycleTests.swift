import XCTest
@testable import AtlasSkills
import AtlasGuard
import AtlasLogging
import AtlasShared
import AtlasTools

// MARK: - ForgeLifecycleTests
//
// Deterministic validation of the Forge Dynamic Skill Runtime using a mock HTTP layer.
//
// Hypothesis: weather-openmeteo (in ForgeRuntimeValidationTests) proves the runtime works
// against a real public API. This suite proves it works correctly in isolation — no network
// dependency, no flakiness, deterministic assertions on URL construction, body serialization,
// header injection, error propagation, and secrets handling.
//
// Test skill: "quote-service"
//   - skillID:   quote-service
//   - action:    quote-service.get  (GET, inputs: category + author, static query: format=json)
//   - action:    quote-service.rate (POST, inputs: quote_id + rating)
//   - No secrets (public API variant)
//
// Test skill: "quote-service-auth"
//   - skillID:  quote-service-auth
//   - action:   quote-service-auth.get (GET, secretHeader: com.mock.atlas.test.quotes)
//   - Covers: secret absent (no auth header), secret present (Authorization: Bearer injected)
//
// All HTTP is intercepted by AtlasMockURLProtocol — URLSession.shared is mocked at the
// URLProtocol level so no real network calls are made in any test in this file.

final class ForgeLifecycleTests: XCTestCase {

    // MARK: - Mock registration

    override func setUpWithError() throws {
        URLProtocol.registerClass(AtlasMockURLProtocol.self)
        AtlasMockURLProtocol.reset()
    }

    override func tearDownWithError() throws {
        URLProtocol.unregisterClass(AtlasMockURLProtocol.self)
        AtlasMockURLProtocol.reset()
    }

    // MARK: - Helpers

    private func makeRegistry() -> SkillRegistry {
        let defaults = UserDefaults(suiteName: "ForgeLifecycle.\(UUID().uuidString)")!
        return SkillRegistry(defaults: defaults)
    }

    private func makeSecrets(returning value: String? = nil) -> CoreSecretsService {
        CoreSecretsService(reader: { _ in value })
    }

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "forge-lifecycle-tests"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }

    // MARK: - quote-service spec + plans

    private func makeQuoteSpec() -> ForgeSkillSpec {
        ForgeSkillSpec(
            id: "quote-service",
            name: "Daily Quotes",
            description: "Fetches inspirational quotes filtered by category and author",
            category: .utility,
            riskLevel: .low,
            tags: ["quotes", "api", "content"],
            actions: [
                ForgeActionSpec(
                    id: "quote-service.get",
                    name: "Get Quote",
                    description: "Fetch an inspirational quote. Supports filtering by category and author.",
                    permissionLevel: .read,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "category": AtlasToolInputProperty(
                                type: "string",
                                description: "Quote category (e.g. 'motivation', 'science', 'life')"
                            ),
                            "author": AtlasToolInputProperty(
                                type: "string",
                                description: "Optional author name to filter by (e.g. 'Einstein')"
                            )
                        ],
                        required: ["category", "author"],
                        additionalProperties: false
                    )
                ),
                ForgeActionSpec(
                    id: "quote-service.rate",
                    name: "Rate Quote",
                    description: "Submit a rating for a quote (1-5 stars).",
                    permissionLevel: .draft,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "quote_id": AtlasToolInputProperty(type: "string", description: "ID of the quote to rate"),
                            "rating": AtlasToolInputProperty(type: "string", description: "Star rating from 1 to 5")
                        ],
                        required: ["quote_id", "rating"],
                        additionalProperties: false
                    )
                )
            ]
        )
    }

    private func makeGetQuotePlan() -> ForgeActionPlan {
        ForgeActionPlan(
            actionID: "quote-service.get",
            type: .http,
            httpRequest: HTTPRequestPlan(
                method: "GET",
                url: "https://api.quotes.mock.atlas.test/v1/quotes",
                query: ["format": "json"]    // Static query param — must survive alongside input params
            )
        )
    }

    private func makeRateQuotePlan() -> ForgeActionPlan {
        ForgeActionPlan(
            actionID: "quote-service.rate",
            type: .http,
            httpRequest: HTTPRequestPlan(
                method: "POST",
                url: "https://api.quotes.mock.atlas.test/v1/ratings"
            )
        )
    }

    // MARK: - quote-service-auth spec + plan (requires secret)

    private func makeAuthQuoteSpec() -> ForgeSkillSpec {
        ForgeSkillSpec(
            id: "quote-service-auth",
            name: "Premium Quotes",
            description: "Fetches premium quotes requiring API key authentication",
            category: .utility,
            riskLevel: .low,
            tags: ["quotes", "api", "premium"],
            actions: [
                ForgeActionSpec(
                    id: "quote-service-auth.get",
                    name: "Get Premium Quote",
                    description: "Fetch a premium quote. Requires API key.",
                    permissionLevel: .read,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "category": AtlasToolInputProperty(type: "string", description: "Quote category")
                        ],
                        required: ["category"],
                        additionalProperties: false
                    )
                )
            ]
        )
    }

    private func makeAuthQuotePlan() -> ForgeActionPlan {
        ForgeActionPlan(
            actionID: "quote-service-auth.get",
            type: .http,
            httpRequest: HTTPRequestPlan(
                method: "GET",
                url: "https://api.auth.mock.atlas.test/v1/premium-quotes",
                secretHeader: "com.mock.atlas.test.quotes"  // Keychain service key
            )
        )
    }

    // MARK: - Mock response fixtures

    private let quoteSuccessJSON: [String: Any] = [
        "quote": "Life is like riding a bicycle. To keep your balance, you must keep moving.",
        "author": "Einstein",
        "category": "motivation",
        "id": "q-42"
    ]

    private let rateSuccessJSON: [String: Any] = [
        "quote_id": "q-42",
        "rating": 5,
        "accepted": true
    ]

    // MARK: - ═══════════ SPEC VALIDATION ═══════════

    func testQuoteSpecIsValid() {
        let result = CoreForgeService().validate(spec: makeQuoteSpec())
        XCTAssertTrue(result.isValid, "quote-service spec must be valid. Issues: \(result.issues)")
        XCTAssertTrue(result.issues.isEmpty)
    }

    func testSpecRejectsIDWithSpaces() {
        let badSpec = ForgeSkillSpec(
            id: "my quote service",
            name: "Bad ID",
            description: "ID has spaces",
            category: .utility,
            riskLevel: .low
        )
        let result = CoreForgeService().validate(spec: badSpec)
        XCTAssertFalse(result.isValid, "ID with spaces must be rejected")
        XCTAssertTrue(result.issues.contains(where: { $0.contains("spaces") }),
                      "Issue must mention spaces. Got: \(result.issues)")
    }

    func testSpecRejectsAtlasPrefix() {
        let badSpec = ForgeSkillSpec(
            id: "atlas.quotes",
            name: "Atlas Quotes",
            description: "Tries to use atlas. prefix",
            category: .utility,
            riskLevel: .low
        )
        let result = CoreForgeService().validate(spec: badSpec)
        XCTAssertFalse(result.isValid)
        XCTAssertTrue(result.issues.contains(where: { $0.contains("reserved") }),
                      "Got: \(result.issues)")
    }

    func testSpecProducesWarningForUnnamspacedAction() {
        let specWithBadActionID = ForgeSkillSpec(
            id: "quote-service",
            name: "Quotes",
            description: "A quote service",
            category: .utility,
            riskLevel: .low,
            actions: [
                ForgeActionSpec(
                    id: "get-quote",    // Missing "quote-service." prefix
                    name: "Get Quote",
                    description: "Fetch a quote",
                    permissionLevel: .read
                )
            ]
        )
        let result = CoreForgeService().validate(spec: specWithBadActionID)
        XCTAssertTrue(result.isValid, "Unnamespaced action ID is a warning, not a hard error")
        XCTAssertTrue(result.warnings.contains(where: { $0.contains("get-quote") }),
                      "Warning must reference the action ID. Got: \(result.warnings)")
    }

    func testSpecWithNoActionsProducesWarning() {
        let emptySpec = ForgeSkillSpec(
            id: "quote-service",
            name: "Daily Quotes",
            description: "A quote service with no actions",
            category: .utility,
            riskLevel: .low,
            actions: []
        )
        let result = CoreForgeService().validate(spec: emptySpec)
        XCTAssertTrue(result.isValid, "No actions is a warning, not a hard failure")
        XCTAssertFalse(result.warnings.isEmpty, "Missing: warning that skill has no callable actions")
    }

    func testSpecRejectsDuplicateActionIDs() {
        let spec = ForgeSkillSpec(
            id: "dup-action-skill",
            name: "Dup Action Skill",
            description: "Has two actions with the same ID",
            category: .utility,
            riskLevel: .low,
            actions: [
                ForgeActionSpec(id: "dup-action-skill.do", name: "Do A", description: "First.", permissionLevel: .read),
                ForgeActionSpec(id: "dup-action-skill.do", name: "Do B", description: "Second.", permissionLevel: .read)
            ]
        )
        let result = CoreForgeService().validate(spec: spec)
        XCTAssertFalse(result.isValid, "Duplicate action IDs must be a hard validation error.")
        XCTAssertTrue(result.issues.contains(where: { $0.contains("Duplicate") }),
                      "Issue must mention 'Duplicate'. Got: \(result.issues)")
    }

    // MARK: - ═══════════ PACKAGE BUILD ═══════════

    func testBuildPackageForQuoteService() throws {
        let forge = CoreForgeService()
        let (package, actionDefs) = try forge.buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan(), makeRateQuotePlan()]
        )

        // Manifest
        XCTAssertEqual(package.manifest.id, "quote-service")
        XCTAssertEqual(package.manifest.name, "Daily Quotes")
        XCTAssertEqual(package.manifest.source, "forge")
        XCTAssertEqual(package.manifest.lifecycleState, .installed)
        XCTAssertEqual(package.manifest.riskLevel, .low)
        XCTAssertFalse(package.manifest.isEnabledByDefault)
        XCTAssertTrue(package.manifest.isUserVisible)

        // Plans preserved
        XCTAssertEqual(package.actions.count, 2)
        XCTAssertNotNil(package.plan(for: "quote-service.get"), "GET plan must be retrievable")
        XCTAssertNotNil(package.plan(for: "quote-service.rate"), "POST plan must be retrievable")
        XCTAssertNil(package.plan(for: "quote-service.nonexistent"))

        let getPlan = try XCTUnwrap(package.plan(for: "quote-service.get")?.httpRequest)
        XCTAssertEqual(getPlan.method, "GET")
        XCTAssertEqual(getPlan.url, "https://api.quotes.mock.atlas.test/v1/quotes")
        XCTAssertEqual(getPlan.query?["format"], "json")

        let ratePlan = try XCTUnwrap(package.plan(for: "quote-service.rate")?.httpRequest)
        XCTAssertEqual(ratePlan.method, "POST")

        // Action definitions
        XCTAssertEqual(actionDefs.count, 2)
        let getDef  = try XCTUnwrap(actionDefs.first { $0.id == "quote-service.get" })
        let rateDef = try XCTUnwrap(actionDefs.first { $0.id == "quote-service.rate" })

        XCTAssertEqual(getDef.permissionLevel, .read)
        XCTAssertEqual(getDef.sideEffectLevel, .safeRead)
        XCTAssertEqual(rateDef.permissionLevel, .draft)
        XCTAssertEqual(rateDef.sideEffectLevel, .draftWrite)
    }

    func testMultiActionInputSchemaPreservedCorrectly() throws {
        let (_, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan(), makeRateQuotePlan()]
        )

        // quote-service.get: category + author both required
        let getDef = try XCTUnwrap(actionDefs.first { $0.id == "quote-service.get" })
        XCTAssertEqual(getDef.inputSchema.properties.count, 2)
        XCTAssertNotNil(getDef.inputSchema.properties["category"])
        XCTAssertNotNil(getDef.inputSchema.properties["author"])
        XCTAssertTrue(getDef.inputSchema.required.contains("category"))
        XCTAssertTrue(getDef.inputSchema.required.contains("author"))
        XCTAssertFalse(getDef.inputSchema.additionalProperties)

        // quote-service.rate: quote_id + rating
        let rateDef = try XCTUnwrap(actionDefs.first { $0.id == "quote-service.rate" })
        XCTAssertEqual(rateDef.inputSchema.properties.count, 2)
        XCTAssertNotNil(rateDef.inputSchema.properties["quote_id"])
        XCTAssertNotNil(rateDef.inputSchema.properties["rating"])

        // LLM-facing descriptions must not be empty
        let catProp = try XCTUnwrap(getDef.inputSchema.properties["category"])
        XCTAssertFalse(catProp.description.isEmpty, "category parameter must have a non-empty description")
    }

    func testBuildPackageThrowsWhenPlanReferencesUndeclaredActionID() throws {
        // Plans may only reference action IDs declared in the spec.
        // A mismatch (e.g. typo in plan.actionID) must throw rather than silently produce
        // a broken package whose action can never be executed.
        let spec = makeQuoteSpec()   // actions: quote-service.get, quote-service.rate

        let orphanPlan = ForgeActionPlan(
            actionID: "quote-service.nonexistent",   // Not in spec
            type: .http,
            httpRequest: HTTPRequestPlan(method: "GET", url: "https://api.quotes.mock.atlas.test/v1/quotes")
        )

        XCTAssertThrowsError(
            try CoreForgeService().buildPackage(spec: spec, plans: [makeGetQuotePlan(), orphanPlan]),
            "buildPackage must throw when a plan references an action ID not declared in the spec"
        ) { error in
            guard let forgeError = error as? ForgeError,
                  case .invalidSpec(let msg) = forgeError else {
                XCTFail("Expected ForgeError.invalidSpec, got: \(error)"); return
            }
            XCTAssertTrue(msg.contains("quote-service.nonexistent"),
                          "Error must identify the undeclared action ID. Got: \(msg)")
        }
    }

    // MARK: - ═══════════ URL PATH PLACEHOLDER SUBSTITUTION ═══════════

    func testURLPathPlaceholderSubstitutedFromInput() async throws {
        // URL template: /v1/quotes/{category} — {category} must be replaced by the input value.
        // This exercises ForgeSkill.substituteParams() which is distinct from query param injection.
        AtlasMockURLProtocol.register(host: "api.quotes.mock.atlas.test", json: quoteSuccessJSON)

        let spec = ForgeSkillSpec(
            id: "quote-path-skill",
            name: "Quote Path Skill",
            description: "Uses URL path placeholder for category.",
            category: .utility,
            riskLevel: .low,
            actions: [
                ForgeActionSpec(
                    id: "quote-path-skill.get",
                    name: "Get Quote By Category",
                    description: "Fetches a quote using category as a URL path segment.",
                    permissionLevel: .read,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "category": AtlasToolInputProperty(type: "string", description: "Category slug")
                        ],
                        additionalProperties: false
                    )
                )
            ]
        )
        let plans: [ForgeActionPlan] = [
            ForgeActionPlan(
                actionID: "quote-path-skill.get",
                type: .http,
                httpRequest: HTTPRequestPlan(
                    method: "GET",
                    url: "https://api.quotes.mock.atlas.test/v1/quotes/{category}"
                )
            )
        ]

        let (package, actionDefs) = try CoreForgeService().buildPackage(spec: spec, plans: plans)
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        _ = try await skill.execute(
            actionID: "quote-path-skill.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"motivation"}"#),
            context: makeContext()
        )

        let captured = try XCTUnwrap(AtlasMockURLProtocol.lastCaptured)
        XCTAssertTrue(
            captured.url.path.contains("motivation"),
            "Path placeholder {category} must be replaced with 'motivation'. Path: \(captured.url.path)"
        )
        XCTAssertFalse(
            captured.url.path.contains("{category}"),
            "Placeholder {category} must not remain in the final URL. Path: \(captured.url.path)"
        )
        // Path-substituted params must NOT also appear as query params
        let queryItems = URLComponents(url: captured.url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        XCTAssertNil(
            queryItems.first { $0.name == "category" },
            "A value used in path substitution must not additionally be appended as a query param."
        )
    }

    // MARK: - ═══════════ INSTALL + ENABLE + CATALOG ═══════════

    func testInstallAndEnableQuoteServiceUpdatesEnabledCatalog() async throws {
        let registry = makeRegistry()
        let counter  = ResyncCounter()
        let skills   = CoreSkillService(
            registry: registry,
            secrets: makeSecrets(),
            resyncCallback: { await counter.increment() }
        )

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan(), makeRateQuotePlan()]
        )

        // Install → .installed, no catalog entry, no resync
        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)

        let afterInstall = await registry.skill(id: "quote-service")
        XCTAssertEqual(afterInstall?.manifest.lifecycleState, .installed)

        let catalogAfterInstall = await registry.enabledActionCatalog()
        XCTAssertFalse(catalogAfterInstall.contains(where: { $0.skillID == "quote-service" }),
                       "Installed-but-not-enabled skill must not be in the enabled catalog")
        let resyncAfterInstall = await counter.count
        XCTAssertEqual(resyncAfterInstall, 0, "Install must not trigger a resync")

        // Enable → .enabled, both actions in catalog, resync fired
        _ = try await skills.enable(skillID: "quote-service")

        let afterEnable = await registry.skill(id: "quote-service")
        XCTAssertEqual(afterEnable?.manifest.lifecycleState, .enabled)

        let catalogAfterEnable = await registry.enabledActionCatalog()
        let getItem  = catalogAfterEnable.first { $0.action.id == "quote-service.get" }
        let rateItem = catalogAfterEnable.first { $0.action.id == "quote-service.rate" }
        XCTAssertNotNil(getItem, "quote-service.get must appear in enabled catalog after enable")
        XCTAssertNotNil(rateItem, "quote-service.rate must appear in enabled catalog after enable")

        let resyncAfterEnable = await counter.count
        XCTAssertEqual(resyncAfterEnable, 1, "Exactly one resync must fire on enable")
    }

    // MARK: - ═══════════ EXECUTION — GET with mock HTTP ═══════════

    func testGetExecutionReturnsMockResponseBody() async throws {
        AtlasMockURLProtocol.register(
            host: "api.quotes.mock.atlas.test",
            json: quoteSuccessJSON
        )

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan(), makeRateQuotePlan()]
        )
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        let result = try await skill.execute(
            actionID: "quote-service.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"motivation","author":"Einstein"}"#),
            context: makeContext()
        )

        XCTAssertEqual(result.skillID, "quote-service")
        XCTAssertEqual(result.actionID, "quote-service.get")
        XCTAssertTrue(result.success, "Mock returned 200 — success must be true")
        XCTAssertEqual(result.metadata["http_status"], "200")

        // Output must be the exact JSON body we registered
        let parsed = try XCTUnwrap(
            try? JSONSerialization.jsonObject(with: Data(result.output.utf8)) as? [String: Any],
            "Output must be valid JSON. Got: \(result.output.prefix(300))"
        )
        XCTAssertEqual(parsed["author"]   as? String, "Einstein")
        XCTAssertEqual(parsed["category"] as? String, "motivation")
        XCTAssertNotNil(parsed["quote"])
    }

    func testGetExecutionIncludesStaticQueryParamAlongsideInputParams() async throws {
        // Static plan query: {format: json}
        // Dynamic input:     {category: motivation, author: Einstein}
        // All three must appear in the final request URL.
        AtlasMockURLProtocol.register(host: "api.quotes.mock.atlas.test", json: quoteSuccessJSON)

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan()]
        )
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        _ = try await skill.execute(
            actionID: "quote-service.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"motivation","author":"Einstein"}"#),
            context: makeContext()
        )

        let captured = try XCTUnwrap(AtlasMockURLProtocol.lastCaptured,
                                     "URLProtocol must have captured the request")
        let query = captured.url.query ?? ""

        // Static plan query param
        XCTAssertTrue(query.contains("format=json"),
                      "Static plan query param 'format=json' must appear in final URL. Query: \(query)")
        // Dynamic input params
        XCTAssertTrue(query.contains("category=motivation"),
                      "Input param 'category' must appear in final URL. Query: \(query)")
        XCTAssertTrue(query.contains("author=Einstein"),
                      "Input param 'author' must appear in final URL. Query: \(query)")
    }

    func testGetExecutionURLContainsInputParamsEncoded() async throws {
        // Input with URL-unsafe characters: "life & work" contains '&' (query separator)
        // and ' ' (space). Both must be percent-encoded so the URL is structurally valid
        // and the parameter value round-trips correctly.
        AtlasMockURLProtocol.register(host: "api.quotes.mock.atlas.test", json: quoteSuccessJSON)

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan()]
        )
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        _ = try await skill.execute(
            actionID: "quote-service.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"life & work","author":"Einstein"}"#),
            context: makeContext()
        )

        let captured = try XCTUnwrap(AtlasMockURLProtocol.lastCaptured)
        let urlString = captured.url.absoluteString

        // Negative: raw unencoded form must never appear (would break query parsing)
        XCTAssertFalse(urlString.contains("life & work"),
                       "Raw 'life & work' must not appear verbatim in URL. URL: \(urlString)")

        // Positive: URLComponents must be able to decode the value back to the original string.
        // This proves the URL was built with correct percent-encoding — not just that the bad
        // chars were dropped.
        let components = try XCTUnwrap(
            URLComponents(url: captured.url, resolvingAgainstBaseURL: false),
            "Final URL must be structurally valid. URL: \(urlString)"
        )
        let categoryItem = components.queryItems?.first { $0.name == "category" }
        XCTAssertNotNil(categoryItem, "URL must contain a 'category' query parameter. URL: \(urlString)")
        XCTAssertEqual(
            categoryItem?.value, "life & work",
            "Percent-encoded category value must round-trip back to 'life & work'. URL: \(urlString)"
        )
    }

    // MARK: - ═══════════ EXECUTION — POST with mock HTTP ═══════════

    func testPostExecutionSendsInputAsJSONBody() async throws {
        AtlasMockURLProtocol.register(host: "api.quotes.mock.atlas.test", json: rateSuccessJSON)

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan(), makeRateQuotePlan()]
        )
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        let result = try await skill.execute(
            actionID: "quote-service.rate",
            input: AtlasToolInput(argumentsJSON: #"{"quote_id":"q-42","rating":"5"}"#),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "POST mock returned 200 — success must be true")
        XCTAssertEqual(result.metadata["http_status"], "200")

        let captured = try XCTUnwrap(AtlasMockURLProtocol.lastCaptured)
        XCTAssertEqual(captured.method, "POST")

        // Request body must be valid JSON containing the input params
        let bodyData = try XCTUnwrap(captured.body, "POST request must have a non-nil body")
        let bodyJSON = try XCTUnwrap(
            JSONSerialization.jsonObject(with: bodyData) as? [String: Any],
            "POST body must be valid JSON"
        )
        XCTAssertEqual(bodyJSON["quote_id"] as? String, "q-42")
        XCTAssertEqual(bodyJSON["rating"]   as? String, "5")

        // Content-Type must be application/json for POST
        let contentType = captured.headers["Content-Type"] ?? ""
        XCTAssertTrue(contentType.contains("application/json"),
                      "POST request must set Content-Type: application/json. Got: \(contentType)")
    }

    func testPostExecutionResponseBodyReturnedCorrectly() async throws {
        AtlasMockURLProtocol.register(host: "api.quotes.mock.atlas.test", json: rateSuccessJSON)

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan(), makeRateQuotePlan()]
        )
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        let result = try await skill.execute(
            actionID: "quote-service.rate",
            input: AtlasToolInput(argumentsJSON: #"{"quote_id":"q-42","rating":"4"}"#),
            context: makeContext()
        )

        let parsed = try XCTUnwrap(
            JSONSerialization.jsonObject(with: Data(result.output.utf8)) as? [String: Any]
        )
        XCTAssertEqual(parsed["accepted"] as? Bool, true)
        XCTAssertEqual(parsed["quote_id"] as? String, "q-42")
    }

    // MARK: - ═══════════ EXECUTION — Error cases ═══════════

    func testNon2xxResponseReturnedAsSuccessFalse() async throws {
        // Register a 422 Unprocessable Entity response
        AtlasMockURLProtocol.register(
            host: "api.quotes.mock.atlas.test",
            statusCode: 422,
            json: ["error": "Invalid category", "code": 422]
        )

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan()]
        )
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        let result = try await skill.execute(
            actionID: "quote-service.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"INVALID","author":"X"}"#),
            context: makeContext()
        )

        // ForgeSkill must not throw on non-2xx — it returns success:false
        XCTAssertFalse(result.success, "Non-2xx response must set success=false")
        XCTAssertEqual(result.metadata["http_status"], "422")
        XCTAssertFalse(result.output.isEmpty, "Output must contain the error response body")

        // Error body must be parseable JSON
        let errorBody = try XCTUnwrap(
            JSONSerialization.jsonObject(with: Data(result.output.utf8)) as? [String: Any]
        )
        XCTAssertNotNil(errorBody["error"])
    }

    func testNetworkErrorThrowsCoreHTTPError() async throws {
        AtlasMockURLProtocol.registerError(
            host: "api.quotes.mock.atlas.test",
            error: URLError(.notConnectedToInternet)
        )

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan()]
        )
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        do {
            _ = try await skill.execute(
                actionID: "quote-service.get",
                input: AtlasToolInput(argumentsJSON: #"{"category":"motivation","author":"X"}"#),
                context: makeContext()
            )
            XCTFail("Expected ForgeSkill to throw on network error")
        } catch let error as CoreHTTPError {
            if case .networkError(let msg) = error {
                XCTAssertFalse(msg.isEmpty, "Network error message must not be empty")
            } else {
                XCTFail("Expected CoreHTTPError.networkError, got: \(error)")
            }
        } catch {
            // CoreHTTPError wraps URLError — the catch above should match.
            // If we land here the error type is unexpected.
            XCTFail("Expected CoreHTTPError.networkError, got unexpected error: \(error)")
        }
    }

    func testUnknownActionIDThrowsCleanError() async throws {
        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan()]
        )
        let skill = ForgeSkill(package: package, actionDefinitions: actionDefs, secretsService: makeSecrets())

        do {
            _ = try await skill.execute(
                actionID: "quote-service.nonexistent",
                input: AtlasToolInput(argumentsJSON: #"{"category":"x","author":"y"}"#),
                context: makeContext()
            )
            XCTFail("Expected AtlasToolError.invalidInput for unknown actionID")
        } catch let error as AtlasToolError {
            guard case .invalidInput(let msg) = error else {
                XCTFail("Expected .invalidInput, got: \(error)"); return
            }
            XCTAssertTrue(msg.contains("quote-service"),
                          "Error must reference the skill ID. Got: \(msg)")
            XCTAssertTrue(msg.contains("quote-service.nonexistent"),
                          "Error must reference the unknown actionID. Got: \(msg)")
        }
    }

    // MARK: - ═══════════ SECRETS HANDLING ═══════════

    func testSecretAbsentDoesNotAddAuthorizationHeader() async throws {
        // No secret registered in Keychain (reader returns nil)
        AtlasMockURLProtocol.register(host: "api.auth.mock.atlas.test", json: quoteSuccessJSON)

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeAuthQuoteSpec(),
            plans: [makeAuthQuotePlan()]
        )
        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets(returning: nil)   // Secret not present
        )

        let result = try await skill.execute(
            actionID: "quote-service-auth.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"motivation"}"#),
            context: makeContext()
        )

        // Execution must still proceed — absent secret is not a throw
        XCTAssertTrue(result.success, "Absent secret must not prevent execution; API call proceeds")

        // No Authorization header must be present when secret is nil
        let captured = try XCTUnwrap(AtlasMockURLProtocol.lastCaptured)
        XCTAssertNil(captured.headers["Authorization"],
                     "Authorization header must NOT be sent when secret is absent")
    }

    func testSecretPresentInjectsAuthorizationBearer() async throws {
        let testToken = "test-api-key-12345"
        AtlasMockURLProtocol.register(host: "api.auth.mock.atlas.test", json: quoteSuccessJSON)

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeAuthQuoteSpec(),
            plans: [makeAuthQuotePlan()]
        )
        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets(returning: testToken)   // Secret is present
        )

        _ = try await skill.execute(
            actionID: "quote-service-auth.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"motivation"}"#),
            context: makeContext()
        )

        let captured = try XCTUnwrap(AtlasMockURLProtocol.lastCaptured)
        let auth = try XCTUnwrap(captured.headers["Authorization"],
                                 "Authorization header must be present when secret is configured")
        XCTAssertTrue(auth.hasPrefix("Bearer "),
                      "Authorization value must be 'Bearer <token>'. Got: \(auth)")
        XCTAssertTrue(auth.hasSuffix(testToken),
                      "Authorization value must contain the token. Got: \(auth)")
    }

    func testSecretValueNeverAppearsInExecutionOutput() async throws {
        // The secret must not leak into the SkillExecutionResult output, summary, or metadata.
        let secretToken = "SUPER_SECRET_TOKEN_XYZ_9999"
        AtlasMockURLProtocol.register(host: "api.auth.mock.atlas.test", json: quoteSuccessJSON)

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeAuthQuoteSpec(),
            plans: [makeAuthQuotePlan()]
        )
        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets(returning: secretToken)
        )

        let result = try await skill.execute(
            actionID: "quote-service-auth.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"motivation"}"#),
            context: makeContext()
        )

        // Secret must not appear anywhere in the result fields exposed to the agent/user
        XCTAssertFalse(result.output.contains(secretToken),
                       "Secret token must NEVER appear in execution output")
        XCTAssertFalse(result.summary.contains(secretToken),
                       "Secret token must NEVER appear in execution summary")
        let metadataValues = result.metadata.values.joined()
        XCTAssertFalse(metadataValues.contains(secretToken),
                       "Secret token must NEVER appear in metadata values")
    }

    func testKeychainErrorPropagatesAsExecutionFailed() async throws {
        // Prior to this fix, try? on secretsService.get() silently swallowed genuine
        // Keychain errors (ACL denial, data corruption) and treated them as "secret not
        // configured" — execution proceeded without auth and the user saw a mysterious 401.
        //
        // After the fix: any throw from the secrets reader surfaces as
        // AtlasToolError.executionFailed with the skill ID and credential key in the message.
        AtlasMockURLProtocol.register(host: "api.auth.mock.atlas.test", json: quoteSuccessJSON)

        let keychainError = NSError(
            domain: "test.keychain",
            code: -25291,
            userInfo: [NSLocalizedDescriptionKey: "errSecAuthFailed — simulated ACL denial"]
        )
        let throwingSecrets = CoreSecretsService(reader: { _ in throw keychainError })

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeAuthQuoteSpec(),
            plans: [makeAuthQuotePlan()]
        )
        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: throwingSecrets
        )

        do {
            _ = try await skill.execute(
                actionID: "quote-service-auth.get",
                input: AtlasToolInput(argumentsJSON: #"{"category":"motivation"}"#),
                context: makeContext()
            )
            XCTFail("Expected AtlasToolError.executionFailed when Keychain reader throws")
        } catch let error as AtlasToolError {
            guard case .executionFailed(let msg) = error else {
                XCTFail("Expected .executionFailed, got: \(error)"); return
            }
            XCTAssertTrue(msg.contains("quote-service-auth"),
                          "Error must identify the skill ID. Got: \(msg)")
            XCTAssertTrue(msg.contains("com.mock.atlas.test.quotes"),
                          "Error must identify the credential key. Got: \(msg)")
        } catch {
            XCTFail("Expected AtlasToolError, got unexpected error type: \(error)")
        }
    }

    func testSecretAbsentValidateConfigurationStillPassesForForgeSkills() async throws {
        // ForgeSkill.manifest.requiredSecrets is always [] because CoreForgeService.scaffold
        // does not populate requiredSecrets from plan.secretHeader. This means
        // validateConfiguration always passes — even when a plan requires a secret.
        //
        // This is a known limitation of Forge v1: secret validation at install time
        // is a no-op. The test documents this behavior explicitly.
        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeAuthQuoteSpec(),
            plans: [makeAuthQuotePlan()]
        )

        // Confirm manifest has no required secrets
        XCTAssertTrue(package.manifest.requiredSecrets.isEmpty,
                      "CoreForgeService.buildPackage does not populate manifest.requiredSecrets from plan.secretHeader — see Forge v1 known limitation")

        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets(returning: nil)  // Secret absent
        )

        let validationResult = await skill.validateConfiguration(
            context: SkillValidationContext(config: AtlasConfig(), logger: AtlasLogger(category: "forge-lifecycle"))
        )

        XCTAssertEqual(validationResult.status, .passed,
                       "validateConfiguration passes for Forge v1 skills regardless of plan.secretHeader (known limitation)")
    }

    // MARK: - ═══════════ DISABLE LIFECYCLE ═══════════

    func testDisableRemovesQuoteServiceFromCatalogAndTriggersResync() async throws {
        let registry = makeRegistry()
        let counter  = ResyncCounter()
        let skills   = CoreSkillService(
            registry: registry,
            secrets: makeSecrets(),
            resyncCallback: { await counter.increment() }
        )

        let (package, actionDefs) = try CoreForgeService().buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan(), makeRateQuotePlan()]
        )

        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)
        _ = try await skills.enable(skillID: "quote-service")

        // Confirm it is in catalog
        let catalogBeforeDisable = await registry.enabledActionCatalog()
        XCTAssertTrue(catalogBeforeDisable.contains(where: { $0.skillID == "quote-service" }),
                      "Skill must be in catalog before disable")

        _ = try await skills.disable(skillID: "quote-service")

        // Must be gone from catalog
        let catalogAfterDisable = await registry.enabledActionCatalog()
        XCTAssertFalse(catalogAfterDisable.contains(where: { $0.skillID == "quote-service" }),
                       "Disabled skill must be removed from enabled action catalog")

        // Skill still registered, just in .disabled state
        let record = await registry.skill(id: "quote-service")
        XCTAssertEqual(record?.manifest.lifecycleState, .disabled)

        // Resync must fire: once for enable, once for disable
        let totalResyncs = await counter.count
        XCTAssertEqual(totalResyncs, 2, "Two resyncs expected: one for enable, one for disable")
    }

    // MARK: - ═══════════ FULL LIFECYCLE SMOKE TEST (deterministic) ═══════════
    //
    // spec → build → install → enable → catalog → execute (mock) → disable
    // Zero real network calls.

    func testFullLifecycleSmokeTestWithMockHTTP() async throws {
        AtlasMockURLProtocol.register(host: "api.quotes.mock.atlas.test", json: quoteSuccessJSON)

        let registry = makeRegistry()
        let counter  = ResyncCounter()
        let skills   = CoreSkillService(
            registry: registry,
            secrets: makeSecrets(),
            resyncCallback: { await counter.increment() }
        )
        let forge = CoreForgeService()

        // 1. Validate spec
        XCTAssertTrue(forge.validate(spec: makeQuoteSpec()).isValid)

        // 2. Build package
        let (package, actionDefs) = try forge.buildPackage(
            spec: makeQuoteSpec(),
            plans: [makeGetQuotePlan(), makeRateQuotePlan()]
        )
        XCTAssertEqual(package.manifest.source, "forge")

        // 3. Install
        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)
        let stateAfterInstall = await registry.skill(id: "quote-service")
        XCTAssertEqual(stateAfterInstall?.manifest.lifecycleState, .installed)
        let catalogAfterInstall = await registry.enabledActionCatalog()
        XCTAssertFalse(catalogAfterInstall.contains(where: { $0.skillID == "quote-service" }))

        // 4. Enable
        _ = try await skills.enable(skillID: "quote-service")
        let stateAfterEnable = await registry.skill(id: "quote-service")
        XCTAssertEqual(stateAfterEnable?.manifest.lifecycleState, .enabled)
        let catalogAfterEnable = await registry.enabledActionCatalog()
        XCTAssertTrue(catalogAfterEnable.contains(where: { $0.skillID == "quote-service" }))
        let resyncsAfterEnable = await counter.count
        XCTAssertEqual(resyncsAfterEnable, 1)

        // 5. Execute via ForgeSkill adapter (mock HTTP)
        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets()
        )
        let result = try await skill.execute(
            actionID: "quote-service.get",
            input: AtlasToolInput(argumentsJSON: #"{"category":"motivation","author":"Einstein"}"#),
            context: makeContext()
        )
        XCTAssertTrue(result.success)
        XCTAssertEqual(result.metadata["http_status"], "200")
        XCTAssertFalse(result.output.isEmpty)

        // 6. Disable
        _ = try await skills.disable(skillID: "quote-service")
        let catalogAfterDisable = await registry.enabledActionCatalog()
        XCTAssertFalse(catalogAfterDisable.contains(where: { $0.skillID == "quote-service" }))
        let resyncsAfterDisable = await counter.count
        XCTAssertEqual(resyncsAfterDisable, 2)
    }
}

// MARK: - ResyncCounter

private actor ResyncCounter {
    private(set) var count = 0
    func increment() { count += 1 }
}

// MARK: - AtlasMockURLProtocol

/// URLProtocol subclass that intercepts HTTP requests made via URLSession.shared
/// during tests. Registered/unregistered in setUp/tearDown.
///
/// Only intercepts requests whose host is in the `registrations` dictionary.
/// All test URLs use the `*.mock.atlas.test` domain convention.
///
/// Thread safety: XCTest runs methods in a class serially.
/// `nonisolated(unsafe)` is safe here since there is no concurrent mutation.
private final class AtlasMockURLProtocol: URLProtocol, @unchecked Sendable {

    // MARK: - Static state

    struct Registration: Sendable {
        let statusCode: Int
        let body: Data
        let networkError: Error?
    }

    struct CapturedRequest: Sendable {
        let method: String
        let url: URL
        let headers: [String: String]
        let body: Data?
    }

    nonisolated(unsafe) static var registrations: [String: Registration] = [:]
    nonisolated(unsafe) static var capturedRequests: [CapturedRequest]      = []

    static var lastCaptured: CapturedRequest? { capturedRequests.last }

    // MARK: - Registration helpers

    static func register(host: String, statusCode: Int = 200, json: [String: Any]) {
        let body = (try? JSONSerialization.data(withJSONObject: json, options: [])) ?? Data()
        registrations[host] = Registration(statusCode: statusCode, body: body, networkError: nil)
    }

    static func registerError(host: String, error: Error) {
        registrations[host] = Registration(statusCode: 0, body: Data(), networkError: error)
    }

    static func reset() {
        registrations    = [:]
        capturedRequests = []
    }

    // MARK: - URLProtocol overrides

    override class func canInit(with request: URLRequest) -> Bool {
        guard let host = request.url?.host else { return false }
        return registrations[host] != nil
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        // Capture request for post-execution assertions.
        //
        // URLSession.shared.data(for:) converts httpBody → httpBodyStream before the
        // request reaches the URLProtocol, so httpBody is always nil here. We read
        // the stream ourselves to reconstruct the body for test assertions.
        var bodyData: Data? = request.httpBody
        if bodyData == nil, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            var accumulated = Data()
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count > 0 { accumulated.append(contentsOf: buffer.prefix(count)) }
            }
            stream.close()
            bodyData = accumulated.isEmpty ? nil : accumulated
        }
        let captured = CapturedRequest(
            method: request.httpMethod ?? "GET",
            url: request.url ?? URL(string: "about:blank")!,
            headers: request.allHTTPHeaderFields ?? [:],
            body: bodyData
        )
        Self.capturedRequests.append(captured)

        guard let host = request.url?.host,
              let reg  = Self.registrations[host] else {
            client?.urlProtocol(self, didFailWithError: URLError(.resourceUnavailable))
            return
        }

        if let error = reg.networkError {
            client?.urlProtocol(self, didFailWithError: error)
            return
        }

        let httpResponse = HTTPURLResponse(
            url: request.url ?? URL(string: "about:blank")!,
            statusCode: reg.statusCode,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: httpResponse, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: reg.body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
