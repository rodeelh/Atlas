import XCTest
@testable import AtlasCore
import AtlasShared
import AtlasSkills

// MARK: - DashboardTests

final class DashboardTests: XCTestCase {

    // MARK: - Helpers

    /// A minimal valid DashboardSpec for reuse across tests.
    private func makeValidSpec(
        id: String = "test-dashboard",
        widgets: [DashboardWidget] = [
            DashboardWidget(
                id: "w1",
                type: .statCard,
                title: "Current Temp",
                skillID: "weather",
                action: "weather.current"
            )
        ]
    ) -> DashboardSpec {
        DashboardSpec(
            id: id,
            title: "Test Dashboard",
            icon: "cloud.sun.fill",
            description: "A test dashboard.",
            sourceSkillIDs: ["weather"],
            widgets: widgets,
            emptyState: nil
        )
    }

    private func makeTempDirectory() -> URL {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("DashboardTests-\(UUID().uuidString)", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    private func makeActionCatalog() -> [SkillActionCatalogItem] {
        [
            SkillActionCatalogItem(
                skillID: "weather",
                skillName: "Weather",
                skillDescription: "Weather lookups",
                skillCategory: .utility,
                trustProfile: .exactStructured,
                freshnessType: .live,
                action: SkillActionDefinition(
                    id: "weather.current",
                    name: "Current Weather",
                    description: "Get current weather",
                    inputSchemaSummary: "locationQuery required",
                    outputSchemaSummary: "temperature, condition",
                    permissionLevel: .read,
                    sideEffectLevel: .safeRead,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "locationQuery": AtlasToolInputProperty(type: "string", description: "Location query"),
                            "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Temperature unit")
                        ],
                        required: ["locationQuery"]
                    )
                ),
                riskLevel: .low,
                preferredQueryTypes: [.weather],
                routingPriority: 100,
                canAnswerStructuredLiveData: true,
                canHandleLocalData: false,
                canHandleExploratoryQueries: false
            ),
            SkillActionCatalogItem(
                skillID: "finance",
                skillName: "Finance",
                skillDescription: "Stock quotes",
                skillCategory: .research,
                trustProfile: .exactStructured,
                freshnessType: .live,
                action: SkillActionDefinition(
                    id: "finance.quote",
                    name: "Quote",
                    description: "Get quote",
                    inputSchemaSummary: "symbol required",
                    outputSchemaSummary: "price, changePercent",
                    permissionLevel: .read,
                    sideEffectLevel: .safeRead,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "symbol": AtlasToolInputProperty(type: "string", description: "Ticker")
                        ],
                        required: ["symbol"]
                    )
                ),
                riskLevel: .low,
                preferredQueryTypes: [.exploratoryResearch],
                routingPriority: 100,
                canAnswerStructuredLiveData: true,
                canHandleLocalData: false,
                canHandleExploratoryQueries: true
            )
        ]
    }

    // MARK: - Validator Tests

    func test_validSpec_passes() {
        let validator = DashboardValidator()
        let spec = makeValidSpec()
        let result = validator.validate(spec)
        XCTAssertTrue(result.isValid, "Expected valid spec to pass. Errors: \(result.errors)")
        XCTAssertTrue(result.errors.isEmpty)
    }

    func test_emptyWidgetList_fails() {
        let validator = DashboardValidator()
        let spec = DashboardSpec(
            id: "empty-widgets",
            title: "No Widgets",
            icon: "square",
            description: "Test.",
            sourceSkillIDs: ["weather"],
            widgets: [],
            emptyState: nil
        )
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("at least one widget") }),
                  "Expected 'at least one widget' error. Got: \(result.errors)")
    }

    func test_emptySpecTitle_fails() {
        let validator = DashboardValidator()
        let spec = DashboardSpec(
            id: "some-id",
            title: "",
            icon: "square",
            description: "Test.",
            sourceSkillIDs: ["weather"],
            widgets: [DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "weather", action: "weather.current")],
            emptyState: nil
        )
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("title") }),
                  "Expected title error. Got: \(result.errors)")
    }

    func test_widgetSkillIDNotInSourceSkillIDs_fails() {
        let validator = DashboardValidator()
        let spec = DashboardSpec(
            id: "bad-skill",
            title: "Bad Skill",
            icon: "square",
            description: "Test.",
            sourceSkillIDs: ["weather"],
            widgets: [DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "unknown-skill", action: "weather.current")],
            emptyState: nil
        )
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("not listed in sourceSkillIDs") }),
                  "Expected sourceSkillIDs error. Got: \(result.errors)")
    }

    func test_formWidgetWithNoFields_fails() {
        let validator = DashboardValidator()
        let spec = DashboardSpec(
            id: "form-no-fields",
            title: "Form Dashboard",
            icon: "square",
            description: "Test.",
            sourceSkillIDs: ["weather"],
            widgets: [DashboardWidget(id: "w1", type: .form, title: "My Form", skillID: "weather", action: "weather.current", fields: [])],
            emptyState: nil
        )
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("form") && $0.contains("field") }),
                  "Expected form fields error. Got: \(result.errors)")
    }

    func test_tableWidgetWithNoColumns_fails() {
        let validator = DashboardValidator()
        let spec = DashboardSpec(
            id: "table-no-cols",
            title: "Table Dashboard",
            icon: "square",
            description: "Test.",
            sourceSkillIDs: ["weather"],
            widgets: [DashboardWidget(id: "w1", type: .table, title: "My Table", skillID: "weather", action: "weather.current", columns: [])],
            emptyState: nil
        )
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("table") && $0.contains("column") }),
                  "Expected table columns error. Got: \(result.errors)")
    }

    func test_emptySpecID_fails() {
        let validator = DashboardValidator()
        let spec = DashboardSpec(
            id: "",
            title: "Valid Title",
            icon: "square",
            description: "Test.",
            sourceSkillIDs: ["weather"],
            widgets: [DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "weather", action: "weather.current")],
            emptyState: nil
        )
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("id") }),
                  "Expected id error. Got: \(result.errors)")
    }

    /// Validates all widget types round-trip through the constrained vocabulary correctly.
    func test_allWidgetTypesRoundTrip() throws {
        let types: [DashboardWidgetType] = [.statCard, .summary, .list, .table, .form, .search]
        for type in types {
            let encoded = try AtlasJSON.encoder.encode(type)
            let decoded = try AtlasJSON.decoder.decode(DashboardWidgetType.self, from: encoded)
            XCTAssertEqual(decoded, type, "Round-trip failed for \(type.rawValue)")
        }
    }

    // MARK: - DashboardStore Tests

    func test_store_proposalCanBeAdded() async throws {
        let store = DashboardStore(directory: makeTempDirectory())
        let proposal = DashboardProposal(
            spec: makeValidSpec(),
            summary: "Test summary",
            rationale: "Test rationale"
        )
        try await store.addProposal(proposal)
        let proposals = await store.listProposals()
        XCTAssertEqual(proposals.count, 1)
        XCTAssertEqual(proposals[0].proposalID, proposal.proposalID)
    }

    func test_store_installTransitionsStatusToInstalled() async throws {
        let store = DashboardStore(directory: makeTempDirectory())
        let proposal = DashboardProposal(
            spec: makeValidSpec(),
            summary: "Test summary",
            rationale: "Test rationale"
        )
        try await store.addProposal(proposal)
        try await store.install(proposalID: proposal.proposalID)

        let proposals = await store.listProposals()
        XCTAssertEqual(proposals[0].status, .installed)
    }

    func test_store_rejectTransitionsStatusToRejected() async throws {
        let store = DashboardStore(directory: makeTempDirectory())
        let proposal = DashboardProposal(
            spec: makeValidSpec(),
            summary: "Test summary",
            rationale: "Test rationale"
        )
        try await store.addProposal(proposal)
        try await store.reject(proposalID: proposal.proposalID)

        let proposals = await store.listProposals()
        XCTAssertEqual(proposals[0].status, .rejected)
    }

    func test_store_installedListReflectsInstalls() async throws {
        let store = DashboardStore(directory: makeTempDirectory())
        let proposal = DashboardProposal(
            spec: makeValidSpec(id: "my-dash"),
            summary: "Test summary",
            rationale: "Test rationale"
        )
        try await store.addProposal(proposal)
        try await store.install(proposalID: proposal.proposalID)

        let installed = await store.listInstalled()
        XCTAssertEqual(installed.count, 1)
        XCTAssertEqual(installed[0].id, "my-dash")
    }

    func test_store_removeDeletesFromInstalledList() async throws {
        let store = DashboardStore(directory: makeTempDirectory())
        let proposal = DashboardProposal(
            spec: makeValidSpec(id: "removable-dash"),
            summary: "Test summary",
            rationale: "Test rationale"
        )
        try await store.addProposal(proposal)
        try await store.install(proposalID: proposal.proposalID)

        var installed = await store.listInstalled()
        XCTAssertEqual(installed.count, 1)

        try await store.remove(dashboardID: "removable-dash")
        installed = await store.listInstalled()
        XCTAssertEqual(installed.count, 0)
    }

    // MARK: - Validator v2 Tests (action / dataKey / defaultInputs)

    func test_widget_emptyAction_fails() {
        let validator = DashboardValidator()
        let spec = DashboardSpec(
            id: "bad-action",
            title: "Bad Action",
            icon: "square",
            description: "Test.",
            sourceSkillIDs: ["weather"],
            widgets: [DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "weather", action: "")],
            emptyState: nil
        )
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("action") }),
                  "Expected action error. Got: \(result.errors)")
    }

    func test_catalogValidation_requiresRealActionAndRequiredInputs() {
        let validator = DashboardValidator(actionCatalog: makeActionCatalog())
        let spec = makeValidSpec(widgets: [
            DashboardWidget(
                id: "w1",
                type: .statCard,
                title: "Temp",
                skillID: "weather",
                action: "weather.current",
                dataKey: "temperature",
                defaultInputs: ["locationQuery": "Orlando, FL"]
            )
        ])
        let result = validator.validate(spec)
        XCTAssertTrue(result.isValid, "Expected real catalog-bound widget to pass. Errors: \(result.errors)")
    }

    func test_catalogValidation_rejectsInventedAction() {
        let validator = DashboardValidator(actionCatalog: makeActionCatalog())
        let spec = makeValidSpec(widgets: [
            DashboardWidget(
                id: "w1",
                type: .statCard,
                title: "Temp",
                skillID: "weather",
                action: "getWeather",
                defaultInputs: ["locationQuery": "Orlando, FL"]
            )
        ])
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("action 'getWeather'") }))
    }

    func test_catalogValidation_rejectsUnknownDefaultInputKey() {
        let validator = DashboardValidator(actionCatalog: makeActionCatalog())
        let spec = makeValidSpec(widgets: [
            DashboardWidget(
                id: "w1",
                type: .statCard,
                title: "Temp",
                skillID: "weather",
                action: "weather.current",
                defaultInputs: ["location": "Orlando, FL"]
            )
        ])
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("defaultInputs key 'location'") }))
        XCTAssert(result.errors.contains(where: { $0.contains("requires defaultInputs for locationQuery") }))
    }

    func test_catalogValidation_rejectsMissingRequiredAutoFetchInputs() {
        let validator = DashboardValidator(actionCatalog: makeActionCatalog())
        let spec = DashboardSpec(
            id: "finance-dashboard",
            title: "Finance Dashboard",
            icon: "chart.line.uptrend.xyaxis",
            description: "Finance test",
            sourceSkillIDs: ["finance"],
            widgets: [
            DashboardWidget(
                id: "w1",
                type: .statCard,
                title: "Price",
                skillID: "finance",
                action: "finance.quote"
            )
            ],
            emptyState: nil
        )
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("requires defaultInputs for symbol") }))
    }

    func test_widget_validDataKey_passes() {
        let validator = DashboardValidator()
        let spec = makeValidSpec(widgets: [
            DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "weather",
                            action: "getWeather", dataKey: "current.temperature")
        ])
        let result = validator.validate(spec)
        XCTAssertTrue(result.isValid, "Expected valid spec to pass. Errors: \(result.errors)")
    }

    func test_widget_invalidDataKey_fails() {
        let validator = DashboardValidator()
        let spec = makeValidSpec(widgets: [
            DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "weather",
                            action: "getWeather", dataKey: "has spaces here")
        ])
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("dataKey") }),
                  "Expected dataKey error. Got: \(result.errors)")
    }

    func test_widget_emptyDataKey_fails() {
        let validator = DashboardValidator()
        let spec = makeValidSpec(widgets: [
            DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "weather",
                            action: "getWeather", dataKey: "")
        ])
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("dataKey") }),
                  "Expected empty dataKey error. Got: \(result.errors)")
    }

    func test_widget_emptyDefaultInputsKey_fails() {
        let validator = DashboardValidator()
        let spec = makeValidSpec(widgets: [
            DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "weather",
                            action: "getWeather", defaultInputs: ["": "San Francisco"])
        ])
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("empty key") }),
                  "Expected empty key error. Got: \(result.errors)")
    }

    func test_widget_emptyDefaultInputsValue_fails() {
        let validator = DashboardValidator()
        let spec = makeValidSpec(widgets: [
            DashboardWidget(id: "w1", type: .statCard, title: "Temp", skillID: "weather",
                            action: "getWeather", defaultInputs: ["location": ""])
        ])
        let result = validator.validate(spec)
        XCTAssertFalse(result.isValid)
        XCTAssert(result.errors.contains(where: { $0.contains("must not be empty") }),
                  "Expected empty value error. Got: \(result.errors)")
    }

    // MARK: - DotPathExtractor Tests

    func test_dotPath_extraction_simpleKey() {
        let extractor = DotPathExtractor()
        let json = #"{"temperature": "72F"}"#
        let result = extractor.extract(path: "temperature", from: json)
        XCTAssertEqual(result, "72F")
    }

    func test_dotPath_extraction_nestedKey() {
        let extractor = DotPathExtractor()
        let json = #"{"current": {"temp": "68F"}}"#
        let result = extractor.extract(path: "current.temp", from: json)
        XCTAssertEqual(result, "68F")
    }

    func test_dotPath_extraction_numericValue() {
        let extractor = DotPathExtractor()
        let json = #"{"current": {"temperature_2m": 22.5}}"#
        let result = extractor.extract(path: "current.temperature_2m", from: json)
        XCTAssertNotNil(result)
        XCTAssertEqual(result, "22.5")
    }

    func test_dotPath_extraction_missingPath() {
        let extractor = DotPathExtractor()
        let json = #"{"current": {"temp": "68F"}}"#
        let result = extractor.extract(path: "missing.key", from: json)
        XCTAssertNil(result, "Missing path should return nil, not crash")
    }

    func test_dotPath_extraction_nonJSON() {
        let extractor = DotPathExtractor()
        let result = extractor.extract(path: "some.path", from: "This is plain text, not JSON.")
        XCTAssertNil(result, "Non-JSON input should return nil gracefully")
    }

    func test_dotPath_extraction_emptyPath() {
        let extractor = DotPathExtractor()
        let json = #"{"temp": "72F"}"#
        let result = extractor.extract(path: "", from: json)
        XCTAssertNil(result, "Empty path should return nil")
    }

    // MARK: - Input Coercion Tests

    func test_inputCoercion_normalizesWeatherTemperatureUnitShorthand() throws {
        let schema = AtlasToolInputSchema(
            properties: [
                "locationQuery": AtlasToolInputProperty(type: "string", description: "Location"),
                "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Temperature unit")
            ],
            required: ["locationQuery"]
        )

        let coerced = DashboardInputCoercion.coerce(
            inputs: ["locationQuery": "Orlando, FL", "temperatureUnit": "F"],
            schema: schema
        )

        XCTAssertEqual(coerced["locationQuery"] as? String, "Orlando, FL")
        XCTAssertEqual(coerced["temperatureUnit"] as? String, "fahrenheit")
    }

    func test_inputCoercion_convertsIntegerStringForStructuredActions() throws {
        let schema = AtlasToolInputSchema(
            properties: [
                "query": AtlasToolInputProperty(type: "string", description: "Query"),
                "maxResults": AtlasToolInputProperty(type: "integer", description: "Result cap")
            ],
            required: ["query"]
        )

        let argumentsJSON = DashboardInputCoercion.argumentsJSON(
            defaultInputs: ["query": "Orlando", "maxResults": "5"],
            overrideInputs: [:],
            catalogItem: SkillActionCatalogItem(
                skillID: "web-research",
                skillName: "Web Research",
                skillDescription: "Web research",
                skillCategory: .research,
                trustProfile: .exploratory,
                freshnessType: .external,
                action: SkillActionDefinition(
                    id: "web.news",
                    name: "News Search",
                    description: "Search news",
                    inputSchemaSummary: "query required, maxResults optional",
                    outputSchemaSummary: "results",
                    permissionLevel: .read,
                    sideEffectLevel: .safeRead,
                    inputSchema: schema
                ),
                riskLevel: .low,
                preferredQueryTypes: [.exploratoryResearch],
                routingPriority: 50,
                canAnswerStructuredLiveData: true,
                canHandleLocalData: false,
                canHandleExploratoryQueries: true
            )
        )

        let payload = try JSONSerialization.jsonObject(with: Data(argumentsJSON.utf8)) as? [String: Any]
        XCTAssertEqual(payload?["query"] as? String, "Orlando")
        XCTAssertEqual(payload?["maxResults"] as? Int, 5)
    }

    func test_displayBinder_bindsNewsResultsIntoListPayload() throws {
        let binder = DashboardDisplayBinder()
        let widget = DashboardWidget(
            id: "news",
            type: .list,
            title: "Top News",
            skillID: "web-research",
            action: "web.news",
            binding: DashboardWidgetBinding(
                itemsPath: "results",
                primaryTextPath: "title",
                secondaryTextPath: "domain",
                linkPath: "url"
            )
        )
        let raw = #"{"results":[{"title":"Apple unveils new product","url":"https://example.com/apple","snippet":"Latest launch","domain":"example.com"}]}"#

        let payload = binder.bind(widget: widget, rawOutput: raw)
        XCTAssertEqual(payload?.items?.count, 1)
        XCTAssertEqual(payload?.items?.first?.primaryText, "Apple unveils new product")
        XCTAssertEqual(payload?.items?.first?.secondaryText, "example.com")
        XCTAssertEqual(payload?.items?.first?.linkURL, "https://example.com/apple")
    }

    func test_displayBinder_usesHeuristicsForLegacyListWidget() throws {
        let binder = DashboardDisplayBinder()
        let widget = DashboardWidget(
            id: "news",
            type: .list,
            title: "Top News",
            skillID: "web-research",
            action: "web.news"
        )
        let raw = #"{"results":[{"title":"Apple news","domain":"example.com"}]}"#

        let payload = binder.bind(widget: widget, rawOutput: raw)
        XCTAssertEqual(payload?.items?.count, 1)
        XCTAssertEqual(payload?.items?.first?.primaryText, "Apple news")
    }

    // MARK: - DashboardWidget defaultInputs Round-trip

    func test_widget_defaultInputs_roundTrip() throws {
        let widget = DashboardWidget(
            id: "temp-widget",
            type: .statCard,
            title: "Temperature",
            skillID: "weather",
            action: "getWeather",
            defaultInputs: ["location": "San Francisco"],
            emptyMessage: "N/A"
        )
        let data = try AtlasJSON.encoder.encode(widget)
        let decoded = try AtlasJSON.decoder.decode(DashboardWidget.self, from: data)
        XCTAssertEqual(decoded.defaultInputs?["location"], "San Francisco")
        XCTAssertEqual(decoded.action, "getWeather")
    }

    func test_widget_defaultInputs_backwardCompat_nil() throws {
        // Simulate old JSON without defaultInputs
        let oldJSON = #"{"id":"w1","type":"stat_card","title":"Temp","skillID":"weather"}"#
        let decoded = try AtlasJSON.decoder.decode(DashboardWidget.self, from: Data(oldJSON.utf8))
        XCTAssertNil(decoded.defaultInputs, "Old JSON without defaultInputs should decode to nil")
        XCTAssertNil(decoded.action)
    }

    // MARK: - Renderer Tests

    /// Verifies that a DashboardSpec containing all widget types can be encoded/decoded
    /// without error — structural validation of the renderer's data model.
    func test_renderer_allWidgetTypesEncodeDecodeSafely() throws {
        let spec = DashboardSpec(
            id: "all-widgets",
            title: "All Widgets Test",
            icon: "square.grid.2x2",
            description: "Tests all widget types.",
            sourceSkillIDs: ["weather", "web", "atlas.info"],
            widgets: [
                DashboardWidget(id: "s1", type: .statCard, title: "Stat", skillID: "weather", action: "weather.current"),
                DashboardWidget(id: "s2", type: .summary, title: "Summary", skillID: "weather", action: "weather.current"),
                DashboardWidget(id: "s3", type: .list, title: "List", skillID: "web", action: "web.search"),
                DashboardWidget(id: "s4", type: .table, title: "Table", skillID: "web", action: "web.search", columns: ["Name", "Value"]),
                DashboardWidget(
                    id: "s5",
                    type: .form,
                    title: "Form",
                    skillID: "atlas.info",
                    action: "get_runtime_status",
                    fields: [WidgetField(key: "q", label: "Query", type: "text", required: true)]
                ),
                DashboardWidget(
                    id: "s6",
                    type: .search,
                    title: "Search",
                    skillID: "web",
                    action: "web.search",
                    fields: [WidgetField(key: "query", label: "Search", type: "text", required: true)]
                )
            ]
        )

        // Validate
        let result = DashboardValidator().validate(spec)
        XCTAssertTrue(result.isValid, "Expected all-widget spec to be valid. Errors: \(result.errors)")

        // Encode → Decode round-trip
        let data = try AtlasJSON.encoder.encode(spec)
        let decoded = try AtlasJSON.decoder.decode(DashboardSpec.self, from: data)

        XCTAssertEqual(decoded.id, spec.id)
        XCTAssertEqual(decoded.widgets.count, spec.widgets.count)
        XCTAssertEqual(decoded.widgets[3].columns, ["Name", "Value"])
        XCTAssertEqual(decoded.widgets[4].fields?.first?.key, "q")
    }
}

private struct MockOpenAIQuery: OpenAIQuerying {
    let response: String

    func complete(systemPrompt: String, userContent: String, model: String?) async throws -> String {
        response
    }
}

private actor CapturingOpenAIQuery: OpenAIQuerying {
    let response: String
    private(set) var capturedModel: String?

    init(response: String) {
        self.response = response
    }

    func complete(systemPrompt: String, userContent: String, model: String?) async throws -> String {
        capturedModel = model
        return response
    }

    func lastModel() -> String? {
        capturedModel
    }
}

final class DashboardPlannerTests: XCTestCase {
    private func makeActionCatalog() -> [SkillActionCatalogItem] {
        [
            SkillActionCatalogItem(
                skillID: "weather",
                skillName: "Weather",
                skillDescription: "Weather lookups",
                skillCategory: .utility,
                trustProfile: .exactStructured,
                freshnessType: .live,
                action: SkillActionDefinition(
                    id: "weather.current",
                    name: "Current Weather",
                    description: "Get current weather",
                    inputSchemaSummary: "locationQuery required",
                    outputSchemaSummary: "temperature, condition",
                    permissionLevel: .read,
                    sideEffectLevel: .safeRead,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "locationQuery": AtlasToolInputProperty(type: "string", description: "Location query"),
                            "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Temperature unit")
                        ],
                        required: ["locationQuery"]
                    )
                ),
                riskLevel: .low,
                preferredQueryTypes: [.weather],
                routingPriority: 100,
                canAnswerStructuredLiveData: true,
                canHandleLocalData: false,
                canHandleExploratoryQueries: false
            )
        ]
    }

    func test_plannerRejectsProposalWithInventedActionAgainstCatalog() async throws {
        let mock = MockOpenAIQuery(response: #"""
        {
          "id":"weather-dashboard",
          "title":"Weather",
          "icon":"cloud.sun.fill",
          "description":"Test",
          "sourceSkillIDs":["weather"],
          "widgets":[
            {
              "id":"w1",
              "type":"stat_card",
              "title":"Current Temperature",
              "skillID":"weather",
              "action":"getWeather",
              "defaultInputs":{"location":"Orlando, FL"},
              "dataKey":"current.temperature"
            }
          ]
        }
        """#)
        let planner = DashboardPlanner(openAI: mock, actionCatalog: makeActionCatalog())

        do {
            _ = try await planner.plan(intent: "weather", skillIDs: ["weather"])
            XCTFail("Expected catalog-grounded planner validation to fail.")
        } catch let error as DashboardPlannerError {
            guard case .validationFailed(let errors) = error else {
                XCTFail("Expected validationFailed, got \(error)")
                return
            }
            XCTAssertTrue(errors.contains(where: { $0.contains("action 'getWeather'") }))
        }
    }

    func test_plannerExtractsValidJSONFromWrappedResponse() async throws {
        let mock = MockOpenAIQuery(response: #"""
        Here is the dashboard spec:
        {
          "id":"weather-dashboard",
          "title":"Weather",
          "icon":"cloud.sun.fill",
          "description":"Uses {live} weather data.",
          "sourceSkillIDs":["weather"],
          "widgets":[
            {
              "id":"w1",
              "type":"stat_card",
              "title":"Current Temperature",
              "skillID":"weather",
              "action":"weather.current",
              "defaultInputs":{"locationQuery":"Orlando, FL"},
              "dataKey":"temperature"
            }
          ],
          "emptyState":"No data."
        }
        Thanks!
        """#)
        let planner = DashboardPlanner(openAI: mock, actionCatalog: makeActionCatalog())
        let proposal = try await planner.plan(intent: "weather", skillIDs: ["weather"])
        XCTAssertEqual(proposal.spec.id, "weather-dashboard")
        XCTAssertEqual(proposal.spec.widgets.first?.action, "weather.current")
    }

    func test_plannerNormalizesActionIDUsedAsSkillID() async throws {
        let mock = MockOpenAIQuery(response: #"""
        {
          "id":"market-dashboard",
          "title":"Market Dashboard",
          "icon":"chart.line.uptrend.xyaxis",
          "description":"AAPL price and news.",
          "sourceSkillIDs":["finance","web.research"],
          "widgets":[
            {
              "id":"aapl-price",
              "type":"stat_card",
              "title":"AAPL Price",
              "skillID":"finance",
              "action":"finance.quote",
              "defaultInputs":{"symbol":"AAPL"},
              "dataKey":"price"
            },
            {
              "id":"apple-stock-top-news",
              "type":"list",
              "title":"Top News",
              "skillID":"web.research",
              "action":"web.research",
              "defaultInputs":{"query":"AAPL top news"},
              "dataKey":"sources"
            }
          ],
          "emptyState":"No data."
        }
        """#)
        let planner = DashboardPlanner(openAI: mock, actionCatalog: [
            SkillActionCatalogItem(
                skillID: "finance",
                skillName: "Finance",
                skillDescription: "Stock quotes",
                skillCategory: .research,
                trustProfile: .exactStructured,
                freshnessType: .live,
                action: SkillActionDefinition(
                    id: "finance.quote",
                    name: "Quote",
                    description: "Get quote",
                    inputSchemaSummary: "symbol required",
                    outputSchemaSummary: "price, changePercent",
                    permissionLevel: .read,
                    sideEffectLevel: .safeRead,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "symbol": AtlasToolInputProperty(type: "string", description: "Ticker")
                        ],
                        required: ["symbol"]
                    )
                ),
                riskLevel: .low,
                preferredQueryTypes: [.exploratoryResearch],
                routingPriority: 100,
                canAnswerStructuredLiveData: true,
                canHandleLocalData: false,
                canHandleExploratoryQueries: true
            ),
            SkillActionCatalogItem(
                skillID: "web-research",
                skillName: "Web Research",
                skillDescription: "Web research",
                skillCategory: .research,
                trustProfile: .exploratory,
                freshnessType: .external,
                action: SkillActionDefinition(
                    id: "web.research",
                    name: "Research Topic",
                    description: "Research topic",
                    inputSchemaSummary: "query required",
                    outputSchemaSummary: "summary, keyPoints, sources",
                    permissionLevel: .read,
                    sideEffectLevel: .safeRead,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "query": AtlasToolInputProperty(type: "string", description: "Query")
                        ],
                        required: ["query"]
                    )
                ),
                riskLevel: .medium,
                preferredQueryTypes: [.exploratoryResearch],
                routingPriority: 90,
                canAnswerStructuredLiveData: false,
                canHandleLocalData: false,
                canHandleExploratoryQueries: true
            )
        ])

        let proposal = try await planner.plan(intent: "AAPL price and top news", skillIDs: ["finance", "web-research"])
        let newsWidget = try XCTUnwrap(proposal.spec.widgets.first(where: { $0.id == "apple-stock-top-news" }))
        XCTAssertEqual(newsWidget.skillID, "web-research")
        XCTAssertEqual(proposal.spec.sourceSkillIDs.sorted(), ["finance", "web-research"])
    }

    func test_plannerNormalizesTemperatureUnitShorthand() async throws {
        let mock = MockOpenAIQuery(response: #"""
        {
          "id":"weather-dashboard",
          "title":"Weather",
          "icon":"cloud.sun.fill",
          "description":"Current weather.",
          "sourceSkillIDs":["weather"],
          "widgets":[
            {
              "id":"w1",
              "type":"stat_card",
              "title":"Current Temperature",
              "skillID":"weather",
              "action":"weather.current",
              "defaultInputs":{"locationQuery":"Orlando, FL","temperatureUnit":"F"},
              "dataKey":"temperature"
            }
          ],
          "emptyState":"No data."
        }
        """#)
        let planner = DashboardPlanner(openAI: mock, actionCatalog: makeActionCatalog())
        let proposal = try await planner.plan(intent: "weather", skillIDs: ["weather"])
        XCTAssertEqual(proposal.spec.widgets.first?.defaultInputs?["temperatureUnit"], "fahrenheit")
        XCTAssertEqual(proposal.spec.widgets.first?.binding?.valuePath, "temperature")
    }

    func test_plannerPassesExplicitModelToOpenAI() async throws {
        let mock = CapturingOpenAIQuery(response: #"""
        {
          "id":"weather-dashboard",
          "title":"Weather",
          "icon":"cloud.sun.fill",
          "description":"Current weather.",
          "sourceSkillIDs":["weather"],
          "widgets":[
            {
              "id":"w1",
              "type":"stat_card",
              "title":"Current Temperature",
              "skillID":"weather",
              "action":"weather.current",
              "defaultInputs":{"locationQuery":"Orlando, FL"},
              "binding":{"valuePath":"temperature"},
              "dataKey":"temperature"
            }
          ],
          "emptyState":"No data."
        }
        """#)
        let planner = DashboardPlanner(
            openAI: mock,
            actionCatalog: makeActionCatalog(),
            model: "gpt-5.4"
        )

        _ = try await planner.plan(intent: "weather", skillIDs: ["weather"])
        let capturedModel = await mock.lastModel()
        XCTAssertEqual(capturedModel, "gpt-5.4")
    }
}
