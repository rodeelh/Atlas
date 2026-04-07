import XCTest
@testable import AtlasCore
import AtlasShared
import AtlasSkills

// MARK: - APIValidationTests

final class APIValidationTests: XCTestCase {

    // MARK: - Test Helpers

    private func makeSecretsService(values: [String: String] = [:]) -> CoreSecretsService {
        CoreSecretsService(reader: { key in values[key] })
    }

    private func makeHTTPService() -> CoreHTTPService {
        CoreHTTPService()
    }

    private func makeTempDirectory() -> URL {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("APIValidationTests-\(UUID().uuidString)", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    // MARK: - Mock HTTP executor helper

    /// Creates a CoreSecretsService and a mock-based APIValidationService
    /// that returns a fixed response body and status code without making live network calls.
    private func makeValidationService(
        statusCode: Int,
        responseBody: Data,
        secretsValues: [String: String] = [:]
    ) -> APIValidationService {
        let secrets = makeSecretsService(values: secretsValues)
        let mockHTTP = CoreHTTPService() // will not be called — we inject via executor pattern below
        _ = mockHTTP  // suppress unused warning

        // We use the init that takes a CoreHTTPService; for mock tests we need a
        // different approach. APIValidationService uses CoreHTTPService directly.
        // Since we can't inject a mock executor into APIValidationService (unlike ForgeDryRunValidator),
        // these tests exercise the service with a mock secrets service only.
        // Network-dependent tests are in the "service integration" group below.
        return APIValidationService(
            httpService: CoreHTTPService(),
            secretsService: secrets
        )
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 1 — Model Round-Trip Tests
    // MARK: ─────────────────────────────────────────────────────────────────────

    func test_exampleInput_roundTrip() throws {
        let input = ExampleInput(
            name: "test-entry",
            inputs: ["lat": "40.7", "lon": "-74.0"],
            source: .catalog
        )
        let data = try JSONEncoder().encode(input)
        let decoded = try JSONDecoder().decode(ExampleInput.self, from: data)
        XCTAssertEqual(decoded.name, input.name)
        XCTAssertEqual(decoded.inputs, input.inputs)
        XCTAssertEqual(decoded.source, .catalog)
    }

    func test_validationResult_roundTrip() throws {
        let result = APIValidationResult(
            success: true,
            confidence: 0.85,
            exampleUsed: ExampleInput(name: "test", inputs: ["q": "london"], source: .generated),
            requestSummary: "GET api.example.com/v1/data",
            responsePreview: "{\"temp\": 22}",
            extractedFields: ["temp", "humidity"],
            failureCategory: nil,
            failureReason: nil,
            recommendation: .usable,
            attemptsCount: 2
        )
        let data = try AtlasJSON.encoder.encode(result)
        let decoded = try AtlasJSON.decoder.decode(APIValidationResult.self, from: data)
        XCTAssertEqual(decoded.success, true)
        XCTAssertEqual(decoded.confidence, 0.85)
        XCTAssertEqual(decoded.recommendation, .usable)
        XCTAssertEqual(decoded.extractedFields, ["temp", "humidity"])
        XCTAssertNil(decoded.failureCategory)
        XCTAssertEqual(decoded.attemptsCount, 2)
    }

    func test_auditRecord_roundTrip() throws {
        let record = APIValidationAuditRecord(
            id: "test-id-123",
            providerName: "Test API",
            endpoint: "/v1/data",
            exampleUsed: ExampleInput(name: "auto-gen", inputs: ["id": "1"], source: .generated),
            confidence: 0.72,
            recommendation: .usable,
            failureCategory: nil,
            responsePreviewTrimmed: "{\"result\": true}",
            timestamp: Date(timeIntervalSince1970: 1711396800) // fixed timestamp
        )
        let data = try AtlasJSON.encoder.encode(record)
        let decoded = try AtlasJSON.decoder.decode(APIValidationAuditRecord.self, from: data)
        XCTAssertEqual(decoded.id, "test-id-123")
        XCTAssertEqual(decoded.providerName, "Test API")
        XCTAssertEqual(decoded.confidence, 0.72)
        XCTAssertEqual(decoded.recommendation, .usable)
        XCTAssertNil(decoded.failureCategory)
        XCTAssertEqual(decoded.exampleUsed?.source, .generated)
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 2 — ExampleInputCatalog Tests
    // MARK: ─────────────────────────────────────────────────────────────────────

    private func makeRequest(
        providerName: String = "Test",
        baseURL: String = "https://api.example.com",
        endpoint: String = "/v1/data",
        requiredParams: [String] = [],
        exampleInputs: [ExampleInput] = []
    ) -> APIValidationRequest {
        APIValidationRequest(
            providerName: providerName,
            baseURL: baseURL,
            endpoint: endpoint,
            requiredParams: requiredParams,
            exampleInputs: exampleInputs
        )
    }

    func test_catalog_providedInputsWin() {
        let provided = ExampleInput(name: "user-supplied", inputs: ["city": "Tokyo"], source: .provided)
        let request = makeRequest(
            providerName: "weather",
            exampleInputs: [provided]
        )
        let result = ExampleInputCatalog.default.resolve(for: request)
        XCTAssertEqual(result.source, .provided)
        XCTAssertEqual(result.inputs["city"], "Tokyo")
        XCTAssertEqual(result.name, "user-supplied")
    }

    func test_catalog_weatherDomainMatch() {
        // "openweathermap" contains "weather" which matches the open-meteo catalog entry first.
        // The important invariant is that a catalog match IS found (not a generated fallback).
        let request = makeRequest(providerName: "openweathermap")
        let result = ExampleInputCatalog.default.resolve(for: request)
        XCTAssertEqual(result.source, .catalog,
            "A catalog entry should be found for 'openweathermap' (matches 'weather' keyword)")
        // Catalog provides meaningful inputs — not an empty generated set
        XCTAssertFalse(result.inputs.isEmpty, "Catalog entry should provide at least one input value")
    }

    func test_catalog_openMeteoMatch() {
        let request = makeRequest(providerName: "open-meteo", baseURL: "https://api.open-meteo.com")
        let result = ExampleInputCatalog.default.resolve(for: request)
        XCTAssertEqual(result.source, .catalog)
        XCTAssertNotNil(result.inputs["latitude"])
        XCTAssertNotNil(result.inputs["longitude"])
    }

    func test_catalog_generatedFallback_idParam() {
        let request = makeRequest(
            providerName: "UnknownAPI",
            baseURL: "https://api.unknown-service.io",
            requiredParams: ["itemId"]
        )
        let result = ExampleInputCatalog.default.resolve(for: request)
        XCTAssertEqual(result.source, .generated)
        XCTAssertEqual(result.inputs["itemId"], "1")
    }

    func test_catalog_generatedFallback_queryParam() {
        let request = makeRequest(
            providerName: "UnknownSearch",
            baseURL: "https://search.unknown.io",
            requiredParams: ["query"]
        )
        let result = ExampleInputCatalog.default.resolve(for: request)
        XCTAssertEqual(result.source, .generated)
        XCTAssertEqual(result.inputs["query"], "test")
    }

    func test_catalog_generatedFallback_locationParam() {
        let request = makeRequest(
            providerName: "UnknownGeo",
            baseURL: "https://geo.unknown.io",
            requiredParams: ["city", "country"]
        )
        let result = ExampleInputCatalog.default.resolve(for: request)
        XCTAssertEqual(result.source, .generated)
        XCTAssertEqual(result.inputs["city"], "London")
        XCTAssertEqual(result.inputs["country"], "US")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 3 — APIResponseInspector Tests
    // MARK: ─────────────────────────────────────────────────────────────────────

    private func makeHTTPURLResponse(statusCode: Int) -> HTTPURLResponse {
        HTTPURLResponse(url: URL(string: "https://api.example.com/v1")!, statusCode: statusCode, httpVersion: nil, headerFields: nil)!
    }

    func test_inspector_401_returnsReject() {
        let result = APIResponseInspector.inspect(
            data: Data("Unauthorized".utf8),
            response: makeHTTPURLResponse(statusCode: 401),
            expectedFields: []
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .reject)
        XCTAssertEqual(result.failureCategory, .httpError)
        XCTAssertEqual(result.confidence, 0.0)
    }

    func test_inspector_403_returnsReject() {
        let result = APIResponseInspector.inspect(
            data: Data("Forbidden".utf8),
            response: makeHTTPURLResponse(statusCode: 403),
            expectedFields: []
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .reject)
        XCTAssertEqual(result.failureCategory, .httpError)
        XCTAssertEqual(result.confidence, 0.0)
    }

    func test_inspector_500_returnsReject() {
        let result = APIResponseInspector.inspect(
            data: Data("Internal Server Error".utf8),
            response: makeHTTPURLResponse(statusCode: 500),
            expectedFields: []
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .reject)
        XCTAssertEqual(result.failureCategory, .httpError)
        XCTAssertEqual(result.confidence, 0.0)
    }

    func test_inspector_200_emptyBody_needsRevision() {
        let result = APIResponseInspector.inspect(
            data: Data(),
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .needsRevision)
        XCTAssertEqual(result.failureCategory, .emptyResponse)
    }

    func test_inspector_200_validJSON_noExpectedFields_usable() throws {
        let json = try JSONSerialization.data(withJSONObject: ["temperature": 22, "unit": "celsius"])
        let result = APIResponseInspector.inspect(
            data: json,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertTrue(result.success)
        XCTAssertEqual(result.recommendation, .usable)
        XCTAssertTrue(result.extractedFields.count > 0)
        XCTAssertGreaterThan(result.confidence, 0.5)
    }

    func test_inspector_200_validJSONArray_extractsFields() throws {
        let jsonArray = try JSONSerialization.data(withJSONObject: [["id": 1, "name": "Alice", "score": 95]])
        let result = APIResponseInspector.inspect(
            data: jsonArray,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertTrue(result.success)
        XCTAssertEqual(result.recommendation, .usable)
        XCTAssertTrue(result.extractedFields.contains("id") || result.extractedFields.contains("name") || result.extractedFields.contains("score"))
    }

    func test_inspector_200_expectedFieldsMatch_highConfidence() throws {
        let json = try JSONSerialization.data(withJSONObject: ["temperature": 22, "humidity": 65, "windSpeed": 12])
        let result = APIResponseInspector.inspect(
            data: json,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: ["temperature", "humidity"]
        )
        XCTAssertTrue(result.success)
        XCTAssertEqual(result.recommendation, .usable)
        XCTAssertGreaterThan(result.confidence, 0.8)
    }

    func test_inspector_200_expectedFieldsMissing_needsRevision() throws {
        let json = try JSONSerialization.data(withJSONObject: ["id": "abc"])
        let result = APIResponseInspector.inspect(
            data: json,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: ["temperature", "humidity", "windSpeed"]
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .needsRevision)
        XCTAssertEqual(result.failureCategory, .missingExpectedFields)
    }

    func test_inspector_404_needsRevision() {
        let result = APIResponseInspector.inspect(
            data: Data("Not Found".utf8),
            response: makeHTTPURLResponse(statusCode: 404),
            expectedFields: []
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .needsRevision)
        XCTAssertEqual(result.confidence, 0.1)
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 4 — APIValidationService Tests (no live network)
    // MARK: ─────────────────────────────────────────────────────────────────────

    func test_service_nonGET_returnsSkipped() async {
        let service = APIValidationService(
            httpService: CoreHTTPService(),
            secretsService: nil
        )
        let request = APIValidationRequest(
            providerName: "Test API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "POST"
        )
        let result = await service.validate(request)
        XCTAssertTrue(result.success)
        XCTAssertEqual(result.recommendation, .skipped)
    }

    func test_service_unsupportedAuth_rejectsEarly() async {
        let service = APIValidationService(
            httpService: CoreHTTPService(),
            secretsService: nil
        )
        let request = APIValidationRequest(
            providerName: "OAuth API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "oauth2AuthorizationCode"
        )
        let result = await service.validate(request)
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.failureCategory, .unsupportedAuth)
        XCTAssertEqual(result.recommendation, .reject)
    }

    func test_service_unknownAuth_rejectsEarly() async {
        let service = APIValidationService(
            httpService: CoreHTTPService(),
            secretsService: nil
        )
        let request = APIValidationRequest(
            providerName: "Mystery API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "unknown"
        )
        let result = await service.validate(request)
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.failureCategory, .unsupportedAuth)
        XCTAssertEqual(result.recommendation, .reject)
    }

    func test_service_missingCredential_rejectsEarly() async {
        // Secrets service returns nil (no credentials configured)
        let secrets = CoreSecretsService(reader: { _ in nil })
        let service = APIValidationService(
            httpService: CoreHTTPService(),
            secretsService: secrets
        )
        let request = APIValidationRequest(
            providerName: "Key API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "apiKeyHeader",
            authSecretKey: "com.projectatlas.missingkey",
            authHeaderName: "X-API-Key"
        )
        let result = await service.validate(request)
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.failureCategory, .missingCredentials)
        XCTAssertEqual(result.recommendation, .reject)
    }

    func test_service_emptyBaseURL_invalidRequestShape() async {
        let service = APIValidationService(
            httpService: CoreHTTPService(),
            secretsService: nil
        )
        let request = APIValidationRequest(
            providerName: "Test",
            baseURL: "",
            endpoint: "/v1/data"
        )
        let result = await service.validate(request)
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.failureCategory, .invalidRequestShape)
        XCTAssertEqual(result.recommendation, .reject)
    }

    func test_service_auditHistoryRecordsSkipped() async {
        let service = APIValidationService(
            httpService: CoreHTTPService(),
            secretsService: nil
        )
        let request = APIValidationRequest(
            providerName: "Test API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "POST" // non-GET → .skipped
        )
        _ = await service.validate(request)
        let history = await service.auditHistory()
        XCTAssertEqual(history.count, 1)
        XCTAssertEqual(history[0].recommendation, .skipped)
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 5 — APIValidationStore Tests
    // MARK: ─────────────────────────────────────────────────────────────────────

    private func makeRecord(
        id: String = UUID().uuidString,
        recommendation: APIValidationRecommendation = .usable,
        timestamp: Date = .now
    ) -> APIValidationAuditRecord {
        APIValidationAuditRecord(
            id: id,
            providerName: "Test Provider",
            endpoint: "/v1/data",
            exampleUsed: nil,
            confidence: 0.8,
            recommendation: recommendation,
            failureCategory: nil,
            responsePreviewTrimmed: nil,
            timestamp: timestamp
        )
    }

    func test_store_append_and_list() async {
        let store = APIValidationStore(directory: makeTempDirectory())
        let r1 = makeRecord(id: "r1")
        let r2 = makeRecord(id: "r2")
        await store.append(r1)
        await store.append(r2)
        let records = await store.listRecent(limit: 50)
        XCTAssertEqual(records.count, 2)
        XCTAssertTrue(records.contains(where: { $0.id == "r1" }))
        XCTAssertTrue(records.contains(where: { $0.id == "r2" }))
    }

    func test_store_respectsLimit() async {
        let store = APIValidationStore(directory: makeTempDirectory())
        for i in 0..<10 {
            await store.append(makeRecord(id: "record-\(i)"))
        }
        let records = await store.listRecent(limit: 3)
        XCTAssertEqual(records.count, 3)
    }

    func test_store_maxRecords_dropsOldest() async {
        let store = APIValidationStore(directory: makeTempDirectory())
        // Append 105 records (max is 100)
        for i in 0..<105 {
            let ts = Date(timeIntervalSince1970: Double(i))
            await store.append(makeRecord(id: "record-\(i)", timestamp: ts))
        }
        let records = await store.listRecent(limit: 200)
        XCTAssertEqual(records.count, 100, "Store should cap at 100 records")
        // Newest should be present, oldest should be dropped
        XCTAssertTrue(records.contains(where: { $0.id == "record-104" }),
                      "Most recent record should be present")
        XCTAssertFalse(records.contains(where: { $0.id == "record-0" }),
                       "Oldest record should have been dropped")
    }

    func test_store_clear() async {
        let store = APIValidationStore(directory: makeTempDirectory())
        await store.append(makeRecord())
        await store.append(makeRecord())
        await store.clear()
        let records = await store.listRecent()
        XCTAssertTrue(records.isEmpty)
    }

    func test_store_listNewestFirst() async {
        let store = APIValidationStore(directory: makeTempDirectory())
        let older = makeRecord(id: "older", timestamp: Date(timeIntervalSince1970: 1000))
        let newer = makeRecord(id: "newer", timestamp: Date(timeIntervalSince1970: 2000))
        await store.append(older)
        await store.append(newer)
        let records = await store.listRecent(limit: 50)
        XCTAssertEqual(records.first?.id, "newer", "listRecent should return newest first")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 6 — Forge Integration Tests
    // MARK: ─────────────────────────────────────────────────────────────────────

    private func makeForgeHandlers(store: ForgeProposalStore) -> (ForgeOrchestrationHandlers, ForgeProposalService) {
        let service = ForgeProposalService(store: store)
        let registry = SkillRegistry(
            defaults: UserDefaults(suiteName: "APIValidationTests.\(UUID().uuidString)")!
        )
        let coreSkills = CoreSkillsRuntime(
            registry: registry,
            secretsReader: { _ in nil }
        )
        Task { await service.configure(coreSkills: coreSkills, skillRegistry: registry) }
        let handlers = ForgeOrchestrationHandlers(
            startResearching: { title, message in
                await service.startResearching(title: title, message: message).id
            },
            stopResearching: { id in
                await service.stopResearching(id: id)
            },
            createProposal: { spec, plans, summary, rationale, contractJSON in
                try await service.createProposal(spec: spec, plans: plans, summary: summary, rationale: rationale, contractJSON: contractJSON)
            }
        )
        return (handlers, service)
    }

    private func makeForgeProposalStore() throws -> ForgeProposalStore {
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("APIValidationTests-\(UUID().uuidString).sqlite3")
        let memoryStore = try MemoryStore(databasePath: url.path)
        return ForgeProposalStore(memoryStore: memoryStore)
    }

    private func makeSkillExecutionContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }

    private func makeValidForgeInput() -> (specJSON: String, plansJSON: String, contractJSON: String) {
        let spec = #"{"id":"test-api","name":"Test API","description":"Test.","category":"utility","riskLevel":"low","tags":["test"],"actions":[{"id":"test-api.get","name":"Get","description":"Fetch data.","permissionLevel":"read","inputSchema":{"type":"object","properties":{"id":{"type":"string","description":"ID"}},"required":["id"],"additionalProperties":false}}]}"#
        let plans = #"[{"actionID":"test-api.get","type":"http","httpRequest":{"method":"GET","url":"https://jsonplaceholder.typicode.com/posts/{id}","headers":{},"query":{},"authType":"none"}}]"#
        let contract = #"{"providerName":"JSONPlaceholder","docsURL":"https://jsonplaceholder.typicode.com","docsQuality":"high","baseURL":"https://jsonplaceholder.typicode.com","endpoint":"/posts/{id}","method":"GET","authType":"none","requiredParams":["id"],"optionalParams":[],"paramLocations":{"id":"path"},"exampleResponse":"{\"id\":1,\"title\":\"Test\"}","mappingConfidence":"high","validationStatus":"unknown"}"#
        return (spec, plans, contract)
    }

    func test_forge_skipValidation_whenServiceNil() async throws {
        let store = try makeForgeProposalStore()
        let (handlers, _) = makeForgeHandlers(store: store)
        // No APIValidationService injected → validation is skipped
        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: nil,
            dryRunValidator: nil,
            apiValidationService: nil  // explicitly nil
        )
        let (specJSON, plansJSON, contractJSON) = makeValidForgeInput()
        let input = AtlasToolInput(argumentsJSON: """
            {"spec_json": \(encodeString(specJSON)), "plans_json": \(encodeString(plansJSON)), "summary": "Test skill", "contract_json": \(encodeString(contractJSON))}
            """)
        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: input,
            context: makeSkillExecutionContext()
        )
        // Should succeed (proposal created without validation)
        XCTAssertTrue(result.success, "Proposal should succeed when validation service is nil. Output: \(result.output)")
    }

    func test_forge_rejectProposal_whenValidationFails_unsupportedAuth() async throws {
        let store = try makeForgeProposalStore()
        let (handlers, _) = makeForgeHandlers(store: store)

        // Use an APIValidationService that will reject because of oauth2 auth in plans
        let service = APIValidationService(httpService: CoreHTTPService(), secretsService: nil)

        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: nil,
            dryRunValidator: nil,
            apiValidationService: service
        )

        // Craft a spec + plans with oauth2 auth → validation step 2 will reject
        let specJSON = #"{"id":"oauth-api","name":"OAuth API","description":"Test.","category":"utility","riskLevel":"low","tags":["test"],"actions":[{"id":"oauth-api.get","name":"Get","description":"Fetch.","permissionLevel":"read","inputSchema":{"type":"object","properties":{},"required":[],"additionalProperties":false}}]}"#
        let plansJSON = #"[{"actionID":"oauth-api.get","type":"http","httpRequest":{"method":"GET","url":"https://api.example.com/v1","headers":{},"query":{},"authType":"oauth2AuthorizationCode"}}]"#
        let contractJSON = #"{"providerName":"OAuth API","docsQuality":"high","baseURL":"https://api.example.com","endpoint":"/v1","method":"GET","authType":"oauth2AuthorizationCode","requiredParams":[],"optionalParams":[],"paramLocations":{},"exampleResponse":"{\"id\":1}","mappingConfidence":"high","validationStatus":"unknown"}"#

        let input = AtlasToolInput(argumentsJSON: """
            {"spec_json": \(encodeString(specJSON)), "plans_json": \(encodeString(plansJSON)), "summary": "OAuth API skill", "contract_json": \(encodeString(contractJSON))}
            """)

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: input,
            context: makeSkillExecutionContext()
        )
        // Should fail because validation rejects oauth2 auth
        // Note: Gate 6 also rejects oauth2, so either gate or validation will catch it.
        // The important thing is that the proposal is NOT created.
        XCTAssertFalse(result.success, "Proposal should be rejected for oauth2 auth. Output: \(result.output)")
    }

    func test_forge_postPlan_proceedsThroughValidation() async throws {
        let store = try makeForgeProposalStore()
        let (handlers, _) = makeForgeHandlers(store: store)

        // POST method → API validation returns .skipped, Forge proceeds to gates
        let service = APIValidationService(httpService: CoreHTTPService(), secretsService: nil)

        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: nil,
            dryRunValidator: nil,
            apiValidationService: service
        )

        // POST method in plan → API validation skips (not rejects), Forge proceeds to gates
        let specJSON = #"{"id":"post-api","name":"POST API","description":"Test.","category":"utility","riskLevel":"low","tags":["test"],"actions":[{"id":"post-api.create","name":"Create","description":"Create.","permissionLevel":"draft","inputSchema":{"type":"object","properties":{},"required":[],"additionalProperties":false}}]}"#
        let plansJSON = #"[{"actionID":"post-api.create","type":"http","httpRequest":{"method":"POST","url":"https://api.example.com/v1","headers":{},"query":{},"authType":"none"}}]"#
        let contractJSON = #"{"providerName":"POST API","docsQuality":"high","baseURL":"https://api.example.com","endpoint":"/v1","method":"POST","authType":"none","requiredParams":[],"optionalParams":[],"paramLocations":{},"exampleResponse":"{\"id\":1}","mappingConfidence":"high","validationStatus":"unknown"}"#

        let input = AtlasToolInput(argumentsJSON: """
            {"spec_json": \(encodeString(specJSON)), "plans_json": \(encodeString(plansJSON)), "summary": "POST API skill", "contract_json": \(encodeString(contractJSON))}
            """)

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: input,
            context: makeSkillExecutionContext()
        )
        // POST plan should no longer be rejected by API validation — it skips and proceeds to gates
        // (The gates themselves may still refuse for other reasons, but not because of POST method.)
        // We simply assert it did not fail due to API validation rejecting POST.
        XCTAssertFalse(
            result.output.contains("API validation rejected"),
            "POST plan should not be rejected by API validation. Output: \(result.output)"
        )
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 7 — Mock Executor Tests (full pipeline without live network)
    // MARK: ─────────────────────────────────────────────────────────────────────

    /// Creates an APIValidationService with an injectable mock HTTP executor.
    private func makeMockService(
        statusCode: Int,
        responseBody: Data,
        secretsValues: [String: String] = [:],
        shouldThrow: Bool = false
    ) -> APIValidationService {
        let secrets = CoreSecretsService(reader: { key in secretsValues[key] })
        let executor: APIValidationService.HTTPExecutor = { req in
            if shouldThrow {
                throw CoreHTTPError.networkError("mock network failure")
            }
            return CoreHTTPResponse(statusCode: statusCode, headers: [:], body: responseBody, url: req.url)
        }
        return APIValidationService(
            executor: executor,
            secretsService: secrets
        )
    }

    /// Creates a mock APIValidationService whose executor returns responses from a queue.
    /// First call → responses[0], second call → responses[1], etc.
    /// If more calls than responses, the last response is repeated.
    private func makeMockServiceSequenced(
        responses: [(statusCode: Int, body: Data)],
        secretsValues: [String: String] = [:]
    ) -> APIValidationService {
        let secrets = CoreSecretsService(reader: { key in secretsValues[key] })
        let box = ResponseBox(responses: responses)
        let executor: APIValidationService.HTTPExecutor = { req in
            let (code, body) = box.next()
            return CoreHTTPResponse(statusCode: code, headers: [:], body: body, url: req.url)
        }
        return APIValidationService(executor: executor, secretsService: secrets)
    }

    /// Thread-safe response queue for sequenced mock executors.
    private final class ResponseBox: @unchecked Sendable {
        private var responses: [(statusCode: Int, body: Data)]
        private var index = 0
        init(responses: [(statusCode: Int, body: Data)]) { self.responses = responses }
        func next() -> (statusCode: Int, body: Data) {
            let i = min(index, responses.count - 1)
            index += 1
            return responses[i]
        }
    }

    func test_service_nonGET_returnsSkipped_notReject() async {
        // P0: POST-only APIs must return .skipped, not .reject
        let service = makeMockService(statusCode: 200, responseBody: Data())
        let request = APIValidationRequest(
            providerName: "POST API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/create",
            method: "POST"
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .skipped,
            "Non-GET plans must return .skipped so Forge is not falsely blocked. Got: \(result.recommendation.rawValue)")
        XCTAssertTrue(result.success, ".skipped should be treated as success (pass-through)")
    }

    func test_service_putMethod_returnsSkipped() async {
        let service = makeMockService(statusCode: 200, responseBody: Data())
        let request = APIValidationRequest(
            providerName: "PUT API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/resource/{id}",
            method: "PUT"
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .skipped)
    }

    func test_service_200_validJSON_usable() async throws {
        // Full pipeline: mock 200 with JSON object → .usable
        let json = try JSONSerialization.data(withJSONObject: ["temperature": 22, "humidity": 65, "city": "London"])
        let service = makeMockService(statusCode: 200, responseBody: json)
        let request = APIValidationRequest(
            providerName: "Weather API",
            baseURL: "https://api.weather.com",
            endpoint: "/v1/current",
            method: "GET",
            authType: "none"
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .usable)
        XCTAssertTrue(result.success)
        XCTAssertGreaterThan(result.confidence, 0.5)
        XCTAssertFalse(result.extractedFields.isEmpty)
    }

    func test_service_401_returnsReject() async {
        let service = makeMockService(statusCode: 401, responseBody: Data("Unauthorized".utf8))
        let request = APIValidationRequest(
            providerName: "Authenticated API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "none"
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .reject)
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.confidence, 0.0)
    }

    func test_service_500_returnsReject() async {
        let service = makeMockService(statusCode: 500, responseBody: Data("Internal Server Error".utf8))
        let request = APIValidationRequest(
            providerName: "Broken API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "none"
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .reject)
        XCTAssertFalse(result.success)
    }

    func test_service_networkFailure_returnsReject() async {
        let service = makeMockService(statusCode: 0, responseBody: Data(), shouldThrow: true)
        let request = APIValidationRequest(
            providerName: "Unreachable API",
            baseURL: "https://unreachable.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "none"
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .reject)
        XCTAssertEqual(result.failureCategory, .networkFailure)
    }

    func test_service_expectedFields_matchRatio_usable() async throws {
        // P1: expectedFields populates the matchRatio scoring branch
        let json = try JSONSerialization.data(withJSONObject: [
            "temperature": 22, "humidity": 65, "windSpeed": 10, "city": "London"
        ])
        let service = makeMockService(statusCode: 200, responseBody: json)
        let request = APIValidationRequest(
            providerName: "Weather API",
            baseURL: "https://api.weather.com",
            endpoint: "/v1/current",
            method: "GET",
            authType: "none",
            expectedFields: ["temperature", "humidity", "windSpeed"]  // all present
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .usable)
        XCTAssertGreaterThan(result.confidence, 0.8, "High field-match ratio should yield confidence > 0.8")
    }

    func test_service_expectedFields_partialMatch_needsRevision() async throws {
        // P1: fewer than 50% of expectedFields found → .needsRevision
        let json = try JSONSerialization.data(withJSONObject: ["id": 1, "name": "test"])
        let service = makeMockService(statusCode: 200, responseBody: json)
        let request = APIValidationRequest(
            providerName: "Data API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "none",
            expectedFields: ["temperature", "humidity", "windSpeed", "pressure"]  // none present
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .needsRevision)
        XCTAssertEqual(result.failureCategory, .missingExpectedFields)
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 8 — Error-Body Detection Tests (P2)
    // MARK: ─────────────────────────────────────────────────────────────────────

    func test_inspector_errorBody_twoFields_needsRevision() throws {
        // P2: {"error": "invalid_request"} with 200 → .needsRevision, not .usable
        let json = try JSONSerialization.data(withJSONObject: ["error": "invalid_request", "code": 400])
        let result = APIResponseInspector.inspect(
            data: json,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertFalse(result.success, "Error body should not be treated as success")
        XCTAssertEqual(result.recommendation, .needsRevision)
        XCTAssertEqual(result.failureCategory, .unusableResponse)
        XCTAssertLessThan(result.confidence, 0.3, "Error body confidence should be low")
    }

    func test_inspector_errorBody_singleErrorField_needsRevision() throws {
        // {"message": "Not Found"} with 200 status
        let json = try JSONSerialization.data(withJSONObject: ["message": "Not Found"])
        let result = APIResponseInspector.inspect(
            data: json,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .needsRevision)
        XCTAssertEqual(result.failureCategory, .unusableResponse)
    }

    func test_inspector_realDataWithStatusField_notFalsePositive() throws {
        // A legitimate weather response with a "status" field among many others
        // → should NOT be flagged as error body (4 fields > 3)
        let json = try JSONSerialization.data(withJSONObject: [
            "status": "ok",
            "temperature": 22,
            "humidity": 65,
            "city": "London"
        ])
        let result = APIResponseInspector.inspect(
            data: json,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        // 4 fields with "status" — must NOT be flagged as error body
        XCTAssertTrue(result.success, "4-field response with 'status' should not be false-positive error body")
        XCTAssertEqual(result.recommendation, .usable)
    }

    func test_inspector_threeFieldsNoErrorIndicator_usable() throws {
        // {"lat": 40.7, "lon": -74.0, "timezone": "America/New_York"} — 3 fields, no error key
        let json = try JSONSerialization.data(withJSONObject: [
            "lat": 40.7, "lon": -74.0, "timezone": "America/New_York"
        ])
        let result = APIResponseInspector.inspect(
            data: json,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertTrue(result.success, "3-field non-error response should be usable")
        XCTAssertEqual(result.recommendation, .usable)
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 9 — Forge Integration: .skipped handling
    // MARK: ─────────────────────────────────────────────────────────────────────

    func test_forge_postOnlySkill_passesWithSkippedValidation() async throws {
        let store = try makeForgeProposalStore()
        let (handlers, _) = makeForgeHandlers(store: store)

        // Use a validation service backed by a mock executor that returns 200 (should not be called for POST)
        let mockExecutor: APIValidationService.HTTPExecutor = { req in
            // Should never be called for a POST plan
            XCTFail("HTTP executor should not be called for non-GET plans")
            return CoreHTTPResponse(statusCode: 200, headers: [:], body: Data(), url: req.url)
        }
        let validationService = APIValidationService(
            executor: mockExecutor,
            secretsService: nil
        )

        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: nil,
            dryRunValidator: nil,
            apiValidationService: validationService
        )

        // POST plan — should no longer falsely reject
        let specJSON = #"{"id":"create-api","name":"Create API","description":"Creates resources.","category":"utility","riskLevel":"low","tags":["test"],"actions":[{"id":"create-api.create","name":"Create","description":"Create resource.","permissionLevel":"draft","inputSchema":{"type":"object","properties":{"name":{"type":"string","description":"Name"}},"required":["name"],"additionalProperties":false}}]}"#
        let plansJSON = #"[{"actionID":"create-api.create","type":"http","httpRequest":{"method":"POST","url":"https://api.example.com/v1/resources","headers":{},"query":{},"authType":"none"}}]"#
        let contractJSON = #"{"providerName":"Create API","docsQuality":"high","baseURL":"https://api.example.com","endpoint":"/v1/resources","method":"POST","authType":"none","requiredParams":["name"],"optionalParams":[],"paramLocations":{"name":"body"},"exampleResponse":"{\"id\":1}","mappingConfidence":"high","validationStatus":"unknown"}"#

        let input = AtlasToolInput(argumentsJSON: """
            {"spec_json": \(encodeString(specJSON)), "plans_json": \(encodeString(plansJSON)), "summary": "Create API skill", "contract_json": \(encodeString(contractJSON))}
            """)

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: input,
            context: makeSkillExecutionContext()
        )
        // POST-only skill must no longer be rejected by API validation
        XCTAssertTrue(result.success, "POST-only skill should not be rejected by API validation. Output: \(result.output)")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 10 — Candidate Loop Tests
    // MARK: ─────────────────────────────────────────────────────────────────────

    func test_loop_stopsAfterFirstGoodResult() async throws {
        // Attempt 1 returns usable → loop must stop immediately (attemptsCount == 1)
        let json = try JSONSerialization.data(withJSONObject: ["temperature": 22, "city": "London"])
        let service = makeMockServiceSequenced(responses: [
            (200, json),
            (200, json)  // second response — should NOT be called
        ])
        let request = APIValidationRequest(
            providerName: "Weather",
            baseURL: "https://api.weather.com",
            endpoint: "/current",
            method: "GET",
            requiredParams: []
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .usable)
        XCTAssertEqual(result.attemptsCount, 1, "Loop must stop after first usable result")
    }

    func test_loop_maxTwoAttempts() async throws {
        // Both attempts return needsRevision → final result uses 2 attempts total
        let errorBody = try JSONSerialization.data(withJSONObject: ["error": "not_found"])
        let service = makeMockServiceSequenced(responses: [
            (200, errorBody),
            (200, errorBody)
        ])
        let request = APIValidationRequest(
            providerName: "Unknown API",
            baseURL: "https://api.unknown.com",
            endpoint: "/v1/items/{id}",
            method: "GET",
            requiredParams: ["id"],
            paramLocations: ["id": "path"]
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.attemptsCount, 2, "Loop must use at most 2 attempts")
        // Both returned error bodies → needsRevision
        XCTAssertEqual(result.recommendation, .needsRevision)
    }

    func test_loop_hardRejectStopsImmediately() async throws {
        // Attempt 1 returns 401 → immediate reject, no second attempt
        let service = makeMockServiceSequenced(responses: [
            (401, Data("Unauthorized".utf8)),
            (200, try! JSONSerialization.data(withJSONObject: ["data": "ok"]))  // must not be called
        ])
        let request = APIValidationRequest(
            providerName: "Auth API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "none"
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .reject)
        XCTAssertEqual(result.attemptsCount, 1, "Hard reject must stop loop immediately — no retry")
    }

    func test_loop_secondAttemptRescuesWeakFirst() async throws {
        // Attempt 1: error body ({"error": "bad_request"}) → needsRevision
        // Attempt 2: valid JSON → usable
        let errorBody = try JSONSerialization.data(withJSONObject: ["error": "bad_request"])
        let goodBody = try JSONSerialization.data(withJSONObject: [
            "temperature": 20, "humidity": 55, "windSpeed": 8
        ])
        let service = makeMockServiceSequenced(responses: [
            (200, errorBody),
            (200, goodBody)
        ])
        let request = APIValidationRequest(
            providerName: "Weather API",
            baseURL: "https://api.weather.com",
            endpoint: "/v1/current",
            method: "GET",
            requiredParams: ["city"],
            paramLocations: ["city": "query"]
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .usable,
            "Second attempt with alternate example should rescue a weak first result. Got: \(result.recommendation)")
        XCTAssertEqual(result.attemptsCount, 2)
    }

    func test_loop_attemptsCountOne_forImmediatePreflightFailure() async {
        // Pre-flight failures (missing credentials) are not subject to the loop
        let secrets = CoreSecretsService(reader: { _ in nil })
        let service = APIValidationService(
            executor: { _ in fatalError("Should not be called") },
            secretsService: secrets
        )
        let request = APIValidationRequest(
            providerName: "Secure API",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            method: "GET",
            authType: "apiKeyHeader",
            authSecretKey: "com.missing.key",
            authHeaderName: "X-API-Key"
        )
        let result = await service.validate(request)
        XCTAssertEqual(result.recommendation, .reject)
        XCTAssertEqual(result.failureCategory, .missingCredentials)
        // Pre-flight failures don't count as loop attempts
        XCTAssertEqual(result.attemptsCount, 1)
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 11 — Empty Response Tests
    // MARK: ─────────────────────────────────────────────────────────────────────

    func test_inspector_emptyArray_needsRevision() {
        let emptyArray = "[]".data(using: .utf8)!
        let result = APIResponseInspector.inspect(
            data: emptyArray,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .needsRevision)
        XCTAssertEqual(result.failureCategory, .emptyResponse,
            "Empty array should be classified as emptyResponse, not unusableResponse")
        XCTAssertLessThanOrEqual(result.confidence, 0.15)
    }

    func test_inspector_emptyObject_needsRevision() {
        let emptyObject = "{}".data(using: .utf8)!
        let result = APIResponseInspector.inspect(
            data: emptyObject,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.recommendation, .needsRevision)
        XCTAssertEqual(result.failureCategory, .emptyResponse,
            "Empty object should be classified as emptyResponse")
        XCTAssertLessThanOrEqual(result.confidence, 0.15)
    }

    func test_inspector_nonEmptyArray_extractsFirstElementFields() throws {
        let arr = try JSONSerialization.data(withJSONObject: [
            ["id": 1, "title": "Post One", "body": "Content"]
        ])
        let result = APIResponseInspector.inspect(
            data: arr,
            response: makeHTTPURLResponse(statusCode: 200),
            expectedFields: []
        )
        XCTAssertTrue(result.success, "Non-empty array should be usable")
        XCTAssertTrue(result.extractedFields.contains("id") ||
                      result.extractedFields.contains("title"),
                      "Fields from first array element should be extracted")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 12 — Multi-Plan Selection Tests (Forge integration)
    // MARK: ─────────────────────────────────────────────────────────────────────

    func test_forge_selectsPrimaryGetPlan_whenFirstPlanIsPost() async throws {
        let store = try makeForgeProposalStore()
        let (handlers, _) = makeForgeHandlers(store: store)

        // Executor that tracks call count — should only be called once (for the GET plan)
        var callCount = 0
        let json = try JSONSerialization.data(withJSONObject: ["id": 1, "name": "Test"])
        let mockExecutor: APIValidationService.HTTPExecutor = { req in
            callCount += 1
            return CoreHTTPResponse(statusCode: 200, headers: [:], body: json, url: req.url)
        }
        let validationService = APIValidationService(executor: mockExecutor, secretsService: nil)

        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: nil,
            dryRunValidator: nil,
            apiValidationService: validationService
        )

        // plans[0] = POST, plans[1] = GET → should validate using GET plan
        let specJSON = #"{"id":"mixed-api","name":"Mixed API","description":"Test.","category":"utility","riskLevel":"low","tags":[],"actions":[{"id":"mixed-api.create","name":"Create","description":"Create.","permissionLevel":"draft","inputSchema":{"type":"object","properties":{},"required":[],"additionalProperties":false}},{"id":"mixed-api.get","name":"Get","description":"Get.","permissionLevel":"read","inputSchema":{"type":"object","properties":{},"required":[],"additionalProperties":false}}]}"#
        let plansJSON = #"[{"actionID":"mixed-api.create","type":"http","httpRequest":{"method":"POST","url":"https://api.example.com/v1/items","headers":{},"query":{},"authType":"none"}},{"actionID":"mixed-api.get","type":"http","httpRequest":{"method":"GET","url":"https://api.example.com/v1/items","headers":{},"query":{},"authType":"none"}}]"#
        let contractJSON = #"{"providerName":"Mixed API","docsQuality":"high","baseURL":"https://api.example.com","endpoint":"/v1/items","method":"GET","authType":"none","requiredParams":[],"optionalParams":[],"paramLocations":{},"exampleResponse":"{\"id\":1,\"name\":\"Test\"}","mappingConfidence":"high","validationStatus":"unknown"}"#

        let input = AtlasToolInput(argumentsJSON: """
            {"spec_json": \(encodeString(specJSON)), "plans_json": \(encodeString(plansJSON)), "summary": "Mixed API skill", "contract_json": \(encodeString(contractJSON))}
            """)

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: input,
            context: makeSkillExecutionContext()
        )

        XCTAssertTrue(result.success,
            "Skill with POST+GET plans should succeed when GET plan is validated. Output: \(result.output)")
        XCTAssertGreaterThan(callCount, 0,
            "Executor should have been called — GET plan must be selected for validation")
    }

    func test_forge_noGetPlan_proceedsToGatesWithWarning() async throws {
        // All plans are POST → no GET plan found → validation skipped → gates run
        let store = try makeForgeProposalStore()
        let (handlers, _) = makeForgeHandlers(store: store)

        let mockExecutor: APIValidationService.HTTPExecutor = { req in
            XCTFail("Executor must not be called when no GET plan exists")
            return CoreHTTPResponse(statusCode: 200, headers: [:], body: Data(), url: req.url)
        }
        let validationService = APIValidationService(executor: mockExecutor, secretsService: nil)

        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: nil,
            dryRunValidator: nil,
            apiValidationService: validationService
        )

        // POST-only plans
        let specJSON = #"{"id":"post-only","name":"POST Only","description":"Test.","category":"utility","riskLevel":"low","tags":[],"actions":[{"id":"post-only.create","name":"Create","description":"Create.","permissionLevel":"draft","inputSchema":{"type":"object","properties":{"name":{"type":"string","description":"Name"}},"required":["name"],"additionalProperties":false}}]}"#
        let plansJSON = #"[{"actionID":"post-only.create","type":"http","httpRequest":{"method":"POST","url":"https://api.example.com/v1/items","headers":{},"query":{},"authType":"none"}}]"#
        let contractJSON = #"{"providerName":"POST Only API","docsQuality":"high","baseURL":"https://api.example.com","endpoint":"/v1/items","method":"POST","authType":"none","requiredParams":["name"],"optionalParams":[],"paramLocations":{"name":"body"},"exampleResponse":"{\"id\":1}","mappingConfidence":"high","validationStatus":"unknown"}"#

        let input = AtlasToolInput(argumentsJSON: """
            {"spec_json": \(encodeString(specJSON)), "plans_json": \(encodeString(plansJSON)), "summary": "POST-only skill", "contract_json": \(encodeString(contractJSON))}
            """)

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: input,
            context: makeSkillExecutionContext()
        )

        XCTAssertTrue(result.success,
            "POST-only skill must not be blocked when no GET plan found. Output: \(result.output)")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 13 — ExampleInputCatalog Alternate Resolution Tests
    // MARK: ─────────────────────────────────────────────────────────────────────

    func test_catalog_resolveAlternate_generatesDistinctValues() {
        let catalog = ExampleInputCatalog()
        let request = makeRequest(
            providerName: "Unknown",
            baseURL: "https://api.unknown.io",
            requiredParams: ["id", "city"]
        )
        let primary = catalog.resolve(for: request)
        let alternate = catalog.resolveAlternate(for: request, first: primary)
        XCTAssertNotNil(alternate, "Alternate should exist for request with requiredParams")
        XCTAssertNotEqual(alternate?.inputs["id"], primary.inputs["id"],
                         "Alternate 'id' value must differ from primary")
        XCTAssertNotEqual(alternate?.inputs["city"], primary.inputs["city"],
                         "Alternate 'city' value must differ from primary")
    }

    func test_catalog_resolveAlternate_usesSecondProvidedInput() {
        let provided1 = ExampleInput(name: "user-supplied-1", inputs: ["q": "Tokyo"], source: .provided)
        let provided2 = ExampleInput(name: "user-supplied-2", inputs: ["q": "Berlin"], source: .provided)
        let request = makeRequest(
            providerName: "search",
            requiredParams: ["q"],
            exampleInputs: [provided1, provided2]
        )
        let primary = ExampleInputCatalog.default.resolve(for: request)
        XCTAssertEqual(primary.source, .provided)
        XCTAssertEqual(primary.inputs["q"], "Tokyo")

        let alternate = ExampleInputCatalog.default.resolveAlternate(for: request, first: primary)
        XCTAssertEqual(alternate?.source, .provided)
        XCTAssertEqual(alternate?.inputs["q"], "Berlin",
                      "Alternate should use the second provided input when available")
    }

    func test_catalog_resolveAlternate_nilWhenNoRequiredParams() {
        let catalog = ExampleInputCatalog()
        let request = makeRequest(
            providerName: "UnknownAPI",
            baseURL: "https://api.example.com",
            endpoint: "/v1/data",
            requiredParams: []  // no params → no alternate
        )
        let primary = catalog.resolve(for: request)
        let alternate = catalog.resolveAlternate(for: request, first: primary)
        XCTAssertNil(alternate, "No alternate should exist when there are no required params")
    }

    // MARK: - Helpers

    /// Encodes a string as a JSON string literal (with escaping).
    private func encodeString(_ s: String) -> String {
        let escaped = s
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
            .replacingOccurrences(of: "\n", with: "\\n")
        return "\"\(escaped)\""
    }
}

// MARK: - Needed imports for tests
import AtlasGuard
import AtlasLogging
import AtlasMemory
import AtlasTools
