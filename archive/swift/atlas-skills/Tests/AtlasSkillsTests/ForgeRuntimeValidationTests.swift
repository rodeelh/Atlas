import XCTest
@testable import AtlasSkills
import AtlasGuard
import AtlasLogging
import AtlasShared
import AtlasTools

// MARK: - ForgeRuntimeValidationTests
//
// End-to-end validation of the Forge Dynamic Skill Runtime v0.
//
// This test suite proves that Atlas can install, enable, and execute a Forge-built
// skill dynamically — without compiled Swift code per skill and without a daemon restart.
//
// Validation target: weather-openmeteo (Open-Meteo, public API, no auth required)
// Covers: spec validation → package build → install → enable → resync → execution → failures
//
// Steps 1–5 are pure unit tests (no network).
// Step 7 (testForgeSkillExecutesRealHTTPCallToOpenMeteo) is an integration test
// that requires network access to api.open-meteo.com.

final class ForgeRuntimeValidationTests: XCTestCase {

    // MARK: - Helpers

    /// Isolated registry per test — prevents state leaking between runs.
    private func makeRegistry() -> SkillRegistry {
        let defaults = UserDefaults(suiteName: "ForgeRuntimeValidation.\(UUID().uuidString)")!
        return SkillRegistry(defaults: defaults)
    }

    /// CoreSecretsService with a no-op reader — Open-Meteo requires no secrets.
    private func makeSecrets() -> CoreSecretsService {
        let reader: CoreSecretsService.SecretsReader = { _ in nil }
        return CoreSecretsService(reader: reader)
    }

    /// SkillExecutionContext suitable for unit tests.
    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "forge-validation"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }

    // MARK: - Shared Spec + Plan

    /// The canonical weather skill spec used across all tests.
    private func makeWeatherSpec() -> ForgeSkillSpec {
        ForgeSkillSpec(
            id: "weather-openmeteo",
            name: "Live Weather",
            description: "Fetches current weather using Open-Meteo API",
            category: .utility,
            riskLevel: .low,
            tags: ["weather", "api"],
            actions: [
                ForgeActionSpec(
                    id: "weather-openmeteo.current",
                    name: "Get Current Weather",
                    description: "Get current weather for a location using latitude and longitude",
                    permissionLevel: .read,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "latitude": AtlasToolInputProperty(type: "string", description: "Latitude of the location (e.g. 40.7128)"),
                            "longitude": AtlasToolInputProperty(type: "string", description: "Longitude of the location (e.g. -74.0060)")
                        ],
                        required: ["latitude", "longitude"],
                        additionalProperties: false
                    )
                )
            ]
        )
    }

    /// The execution plan for the weather action.
    private func makeWeatherPlan() -> ForgeActionPlan {
        ForgeActionPlan(
            actionID: "weather-openmeteo.current",
            type: .http,
            httpRequest: HTTPRequestPlan(
                method: "GET",
                url: "https://api.open-meteo.com/v1/forecast",
                query: ["current_weather": "true"]
            )
        )
    }

    // MARK: - STEP 1: Spec Validation

    func testForgeSpecIsValid() {
        let forge = CoreForgeService()
        let result = forge.validate(spec: makeWeatherSpec())

        XCTAssertTrue(result.isValid, "Weather spec should be valid. Issues: \(result.issues)")
        XCTAssertTrue(result.issues.isEmpty, "Expected no validation issues, got: \(result.issues)")
    }

    func testForgeSpecRejectsReservedPrefix() {
        let forge = CoreForgeService()
        let badSpec = ForgeSkillSpec(
            id: "core.weather",
            name: "Sneaky Weather",
            description: "Tries to use reserved prefix",
            category: .utility,
            riskLevel: .low
        )

        let result = forge.validate(spec: badSpec)

        XCTAssertFalse(result.isValid, "Spec with 'core.' prefix must be rejected")
        XCTAssertTrue(result.issues.contains(where: { $0.contains("reserved") }),
                      "Issue message should mention reserved prefix, got: \(result.issues)")
    }

    func testForgeSpecRejectsEmptyID() {
        let forge = CoreForgeService()
        let badSpec = ForgeSkillSpec(
            id: "",
            name: "No ID",
            description: "Missing ID skill",
            category: .utility,
            riskLevel: .low
        )

        let result = forge.validate(spec: badSpec)

        XCTAssertFalse(result.isValid)
        XCTAssertTrue(result.issues.contains(where: { $0.contains("ID must not be empty") }),
                      "Got: \(result.issues)")
    }

    func testForgeSpecWarnsDuplicateActionIDs() {
        let forge = CoreForgeService()
        let spec = ForgeSkillSpec(
            id: "dupe-test",
            name: "Dupe Test",
            description: "Has duplicate action IDs",
            category: .utility,
            riskLevel: .low,
            actions: [
                ForgeActionSpec(id: "dupe-test.action", name: "Action", description: "First", permissionLevel: .read),
                ForgeActionSpec(id: "dupe-test.action", name: "Action", description: "Second", permissionLevel: .read)
            ]
        )

        let result = forge.validate(spec: spec)

        XCTAssertFalse(result.isValid, "Duplicate action IDs must fail validation")
        XCTAssertTrue(result.issues.contains(where: { $0.contains("Duplicate action IDs") }),
                      "Got: \(result.issues)")
    }

    // MARK: - STEP 2+3: Package Building

    func testForgeBuildPackageSucceeds_ManifestIsCorrect() throws {
        let forge = CoreForgeService()
        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )

        // Manifest fields
        XCTAssertEqual(package.manifest.id, "weather-openmeteo")
        XCTAssertEqual(package.manifest.name, "Live Weather")
        XCTAssertEqual(package.manifest.source, "forge")
        XCTAssertEqual(package.manifest.lifecycleState, .installed)
        XCTAssertFalse(package.manifest.isEnabledByDefault, "Forge skills must NOT be enabled by default")
        XCTAssertTrue(package.manifest.isUserVisible, "Forge skills should be user-visible")
        XCTAssertEqual(package.manifest.riskLevel, .low)
        XCTAssertTrue(package.manifest.requiredSecrets.isEmpty, "Open-Meteo needs no secrets")

        // Package plans
        XCTAssertEqual(package.actions.count, 1)
        XCTAssertEqual(package.actions[0].actionID, "weather-openmeteo.current")
        XCTAssertEqual(package.actions[0].type, .http)
        XCTAssertNotNil(package.actions[0].httpRequest)

        // HTTPRequestPlan
        let plan = try XCTUnwrap(package.actions[0].httpRequest)
        XCTAssertEqual(plan.method, "GET")
        XCTAssertEqual(plan.url, "https://api.open-meteo.com/v1/forecast")
        XCTAssertEqual(plan.query?["current_weather"], "true")
        XCTAssertNil(plan.secretHeader, "Open-Meteo requires no auth header")

        // Action definitions
        XCTAssertEqual(actionDefs.count, 1)
        XCTAssertEqual(actionDefs[0].id, "weather-openmeteo.current")
        XCTAssertEqual(actionDefs[0].permissionLevel, .read)
        XCTAssertEqual(actionDefs[0].sideEffectLevel, .safeRead)
    }

    func testForgeBuildPackageRejectsUnknownPlanActionID() {
        let forge = CoreForgeService()
        let mismatchedPlan = ForgeActionPlan(
            actionID: "weather-openmeteo.nonexistent",
            type: .http,
            httpRequest: makeWeatherPlan().httpRequest
        )

        XCTAssertThrowsError(
            try forge.buildPackage(spec: makeWeatherSpec(), plans: [mismatchedPlan])
        ) { error in
            guard let forgeError = error as? ForgeError,
                  case .invalidSpec(let msg) = forgeError else {
                XCTFail("Expected ForgeError.invalidSpec, got: \(error)")
                return
            }
            XCTAssertTrue(msg.contains("weather-openmeteo.nonexistent"),
                          "Error message should name the unknown action ID. Got: \(msg)")
        }
    }

    // MARK: - STEP 4: Install

    func testForgeInstallPlacesSkillInInstalledState() async throws {
        let registry = makeRegistry()
        let forge = CoreForgeService()
        let skills = CoreSkillService(registry: registry, secrets: makeSecrets())

        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )

        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)

        let record = await registry.skill(id: "weather-openmeteo")
        XCTAssertNotNil(record, "Skill should be registered after install")
        XCTAssertEqual(record?.manifest.lifecycleState, .installed,
                       "Installed skill must be in .installed state, NOT .enabled")

        // Enabled catalog must NOT include the skill yet
        let catalog = await registry.enabledActionCatalog()
        XCTAssertFalse(catalog.contains(where: { $0.skillID == "weather-openmeteo" }),
                       "Installed-but-not-enabled skill must not appear in enabled action catalog")
    }

    func testForgeInstallIsIdempotent() async throws {
        let registry = makeRegistry()
        let forge = CoreForgeService()
        let skills = CoreSkillService(registry: registry, secrets: makeSecrets())

        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )

        // First install
        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)

        // Second install — must not throw (idempotent)
        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)

        let all = await registry.listAll()
        let weatherEntries = all.filter { $0.manifest.id == "weather-openmeteo" }
        XCTAssertEqual(weatherEntries.count, 1, "Idempotent install must not create duplicate entries")
    }

    // MARK: - STEP 5: Enable + Resync

    func testForgeEnableTransitionsToEnabledAndTriggersResync() async throws {
        let registry = makeRegistry()
        let forge = CoreForgeService()
        let counter = ResyncCounter()
        let skills = CoreSkillService(
            registry: registry,
            secrets: makeSecrets(),
            resyncCallback: { await counter.increment() }
        )

        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )
        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)

        // Pre-enable: resync has not been called
        let callsBefore = await counter.count
        XCTAssertEqual(callsBefore, 0, "Resync must not fire on install — only on enable/disable")

        // Enable
        _ = try await skills.enable(skillID: "weather-openmeteo")

        // Post-enable: state is .enabled, resync fired exactly once
        let record = await registry.skill(id: "weather-openmeteo")
        XCTAssertEqual(record?.manifest.lifecycleState, .enabled,
                       "Skill must be .enabled after enable()")

        let callsAfter = await counter.count
        XCTAssertEqual(callsAfter, 1,
                       "resyncSkillCatalog() must be called exactly once when a skill is enabled")
    }

    func testForgeDisableTriggersResyncAndRemovesFromCatalog() async throws {
        let registry = makeRegistry()
        let forge = CoreForgeService()
        let counter = ResyncCounter()
        let skills = CoreSkillService(
            registry: registry,
            secrets: makeSecrets(),
            resyncCallback: { await counter.increment() }
        )

        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )
        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)
        _ = try await skills.enable(skillID: "weather-openmeteo")

        let catalogAfterEnable = await registry.enabledActionCatalog()
        XCTAssertTrue(catalogAfterEnable.contains(where: { $0.skillID == "weather-openmeteo" }),
                      "Skill must be in catalog after enable")

        _ = try await skills.disable(skillID: "weather-openmeteo")

        let catalogAfterDisable = await registry.enabledActionCatalog()
        XCTAssertFalse(catalogAfterDisable.contains(where: { $0.skillID == "weather-openmeteo" }),
                       "Disabled skill must be removed from enabled action catalog")

        let totalResyncs = await counter.count
        XCTAssertEqual(totalResyncs, 2,
                       "Resync must fire once for enable and once for disable")
    }

    // MARK: - STEP 6: Schema + Catalog

    func testForgeActionInputSchemaExposedCorrectly() throws {
        let forge = CoreForgeService()
        let (_, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )

        let action = try XCTUnwrap(actionDefs.first)
        let schema = action.inputSchema

        // Both required parameters must be present with correct types
        XCTAssertEqual(schema.properties.count, 2,
                       "Schema must declare exactly latitude and longitude")

        let latProp = try XCTUnwrap(schema.properties["latitude"],
                                     "inputSchema must contain 'latitude' property")
        XCTAssertEqual(latProp.type, "string")
        XCTAssertFalse(latProp.description.isEmpty, "latitude property must have a description")

        let lonProp = try XCTUnwrap(schema.properties["longitude"],
                                     "inputSchema must contain 'longitude' property")
        XCTAssertEqual(lonProp.type, "string")
        XCTAssertFalse(lonProp.description.isEmpty, "longitude property must have a description")

        // Both are required
        XCTAssertTrue(schema.required.contains("latitude"),
                      "latitude must be in the required array")
        XCTAssertTrue(schema.required.contains("longitude"),
                      "longitude must be in the required array")

        XCTAssertFalse(schema.additionalProperties,
                       "additionalProperties must be false")
    }

    func testForgeSkillAppearsInEnabledCatalogAfterEnable() async throws {
        let registry = makeRegistry()
        let forge = CoreForgeService()
        let skills = CoreSkillService(registry: registry, secrets: makeSecrets())

        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )
        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)
        _ = try await skills.enable(skillID: "weather-openmeteo")

        let catalog = await registry.enabledActionCatalog()

        let item = catalog.first(where: {
            $0.skillID == "weather-openmeteo" && $0.action.id == "weather-openmeteo.current"
        })
        XCTAssertNotNil(item,
                        "Enabled catalog must contain weather-openmeteo.current after enable")

        // Verify the catalog item carries the correct schema so SkillActionToolAdapter
        // can build a properly-parameterised tool definition for the LLM.
        XCTAssertEqual(item?.action.inputSchema.properties.count, 2,
                       "Catalog action must expose the full inputSchema with 2 properties")
        XCTAssertNotNil(item?.action.inputSchema.properties["latitude"])
        XCTAssertNotNil(item?.action.inputSchema.properties["longitude"])
    }

    // MARK: - STEP 7: Execution — Failure Cases (no network required)

    func testForgeExecuteUnknownActionIDThrowsCleanError() async throws {
        let forge = CoreForgeService()
        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )

        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets()
        )

        do {
            _ = try await skill.execute(
                actionID: "weather-openmeteo.does_not_exist",
                input: AtlasToolInput(argumentsJSON: #"{"latitude":"40.7128","longitude":"-74.0060"}"#),
                context: makeContext()
            )
            XCTFail("Expected AtlasToolError.invalidInput for unknown actionID")
        } catch let error as AtlasToolError {
            if case .invalidInput(let msg) = error {
                XCTAssertTrue(msg.contains("weather-openmeteo"),
                              "Error message must reference the skill. Got: \(msg)")
                XCTAssertTrue(msg.contains("weather-openmeteo.does_not_exist"),
                              "Error message must reference the bad actionID. Got: \(msg)")
            } else {
                XCTFail("Expected .invalidInput, got: \(error)")
            }
        }
    }

    // MARK: - STEP 7: Execution — Real HTTP Integration Test

    // This test makes a live network call to api.open-meteo.com.
    // It validates the complete Forge execution path end-to-end:
    // skill lookup → URL construction → query param injection → HTTP → result.
    //
    // Open-Meteo is free, public, and requires no API key.
    // Input: New York City (lat 40.7128, lon -74.0060)
    func testForgeSkillExecutesRealHTTPCallToOpenMeteo() async throws {
        let forge = CoreForgeService()
        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )

        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets()
        )

        let result = try await skill.execute(
            actionID: "weather-openmeteo.current",
            input: AtlasToolInput(argumentsJSON: #"{"latitude":"40.7128","longitude":"-74.0060"}"#),
            context: makeContext()
        )

        // SkillExecutionResult fields
        XCTAssertEqual(result.skillID, "weather-openmeteo")
        XCTAssertEqual(result.actionID, "weather-openmeteo.current")
        XCTAssertTrue(result.success,
                      "Execution must succeed. HTTP status: \(result.metadata["http_status"] ?? "unknown"). Output: \(result.output.prefix(300))")

        // HTTP metadata
        let status = result.metadata["http_status"] ?? ""
        XCTAssertEqual(status, "200", "Expected HTTP 200 from Open-Meteo, got: \(status)")

        // Response body must be valid JSON and contain Open-Meteo fields
        let bodyData = Data(result.output.utf8)
        let json = try JSONSerialization.jsonObject(with: bodyData) as? [String: Any]
        XCTAssertNotNil(json, "Response body must be valid JSON. Got: \(result.output.prefix(200))")

        // Open-Meteo always returns latitude, longitude, and current_weather
        XCTAssertNotNil(json?["latitude"], "Response must include 'latitude'")
        XCTAssertNotNil(json?["longitude"], "Response must include 'longitude'")
        XCTAssertNotNil(json?["current_weather"],
                        "Response must include 'current_weather' (requested via query param). Got keys: \(json?.keys.sorted() ?? [])")

        let currentWeather = json?["current_weather"] as? [String: Any]
        XCTAssertNotNil(currentWeather?["temperature"], "current_weather must have 'temperature'")
        XCTAssertNotNil(currentWeather?["windspeed"], "current_weather must have 'windspeed'")
        XCTAssertNotNil(currentWeather?["weathercode"], "current_weather must have 'weathercode'")
    }

    // MARK: - STEP 8: Failure Tests

    // When required parameters are missing, the ForgeSkill still fires the HTTP request
    // (validation is the LLM's responsibility via the inputSchema). Open-Meteo returns
    // a 400/error JSON body. The skill must return a result without crashing —
    // success == false and the error JSON in output.
    func testForgeExecuteMissingLatitude_ReturnsAPIErrorResponseCleanly() async throws {
        let forge = CoreForgeService()
        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )

        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets()
        )

        // Only longitude provided — latitude is missing
        let result = try await skill.execute(
            actionID: "weather-openmeteo.current",
            input: AtlasToolInput(argumentsJSON: #"{"longitude":"-74.0060"}"#),
            context: makeContext()
        )

        // Must not throw — the skill should always return a SkillExecutionResult
        // even when the API returns a non-2xx response.
        XCTAssertEqual(result.skillID, "weather-openmeteo")
        XCTAssertFalse(result.success,
                       "Missing required param should yield success=false from a non-2xx API response")
        XCTAssertFalse(result.output.isEmpty,
                       "Output should contain the API's error message, not be empty")
    }

    func testForgeSkillValidateConfigurationPassesWithNoRequiredSecrets() async throws {
        let forge = CoreForgeService()
        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )

        let skill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets()
        )

        let validationContext = SkillValidationContext(
            config: AtlasConfig(),
            logger: AtlasLogger(category: "forge-validation")
        )

        let validationResult = await skill.validateConfiguration(context: validationContext)

        XCTAssertEqual(validationResult.status, .passed,
                       "Open-Meteo skill requires no secrets — validation must pass. Summary: \(validationResult.summary)")
    }

    // MARK: - Full Lifecycle Smoke Test
    //
    // Exercises all 7 steps in sequence using the shared registry:
    // spec → build → install → enable → catalog check → execute → disable

    func testForgeFullLifecycleSmokeTest() async throws {
        let registry = makeRegistry()
        let forge = CoreForgeService()
        let counter = ResyncCounter()
        let skills = CoreSkillService(
            registry: registry,
            secrets: makeSecrets(),
            resyncCallback: { await counter.increment() }
        )

        // 1. Validate spec
        let validation = forge.validate(spec: makeWeatherSpec())
        XCTAssertTrue(validation.isValid)

        // 2. Build package
        let (package, actionDefs) = try forge.buildPackage(
            spec: makeWeatherSpec(),
            plans: [makeWeatherPlan()]
        )
        XCTAssertEqual(package.manifest.id, "weather-openmeteo")

        // 3. Install → .installed, not in catalog
        try await skills.installForgeSkill(package: package, actionDefinitions: actionDefs)
        let stateAfterInstall = await registry.skill(id: "weather-openmeteo")
        XCTAssertEqual(stateAfterInstall?.manifest.lifecycleState, .installed)
        let catalogAfterInstall = await registry.enabledActionCatalog()
        XCTAssertFalse(catalogAfterInstall.contains(where: { $0.skillID == "weather-openmeteo" }))

        // 4. Enable → .enabled, catalog updated, resync called
        _ = try await skills.enable(skillID: "weather-openmeteo")
        let stateAfterEnable = await registry.skill(id: "weather-openmeteo")
        XCTAssertEqual(stateAfterEnable?.manifest.lifecycleState, .enabled)
        let catalogAfterEnable = await registry.enabledActionCatalog()
        XCTAssertTrue(catalogAfterEnable.contains(where: { $0.skillID == "weather-openmeteo" }))
        let resyncsAfterEnable = await counter.count
        XCTAssertEqual(resyncsAfterEnable, 1)

        // 5. Execute via the ForgeSkill adapter (real HTTP)
        let forgeSkill = ForgeSkill(
            package: package,
            actionDefinitions: actionDefs,
            secretsService: makeSecrets()
        )
        let result = try await forgeSkill.execute(
            actionID: "weather-openmeteo.current",
            input: AtlasToolInput(argumentsJSON: #"{"latitude":"40.7128","longitude":"-74.0060"}"#),
            context: makeContext()
        )
        XCTAssertTrue(result.success, "Full smoke test execution must succeed. Output: \(result.output.prefix(200))")
        XCTAssertEqual(result.metadata["http_status"], "200")

        // 6. Disable → out of catalog, resync called again
        _ = try await skills.disable(skillID: "weather-openmeteo")
        let catalogAfterDisable = await registry.enabledActionCatalog()
        XCTAssertFalse(catalogAfterDisable.contains(where: { $0.skillID == "weather-openmeteo" }))
        let resyncsAfterDisable = await counter.count
        XCTAssertEqual(resyncsAfterDisable, 2)
    }
}

// MARK: - ResyncCounter

/// Thread-safe counter used to verify resyncSkillCatalog() is called the correct number of times.
private actor ResyncCounter {
    private(set) var count = 0
    func increment() { count += 1 }
}
