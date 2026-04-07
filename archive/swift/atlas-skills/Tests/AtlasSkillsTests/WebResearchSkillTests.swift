import XCTest
@testable import AtlasSkills
import AtlasGuard
import AtlasLogging
import AtlasShared

final class WebResearchSkillTests: XCTestCase {
    func testDomainPolicyRejectsLocalAndPrivateTargets() throws {
        let policy = WebDomainPolicy()

        XCTAssertThrowsError(try policy.validateURLString("http://localhost:8080"))
        XCTAssertThrowsError(try policy.validateURLString("http://127.0.0.1/test"))
        XCTAssertThrowsError(try policy.validateURLString("http://192.168.1.10"))
        XCTAssertThrowsError(try policy.validateURLString("http://printer.local"))
    }

    func testContentExtractorProducesReadableText() throws {
        let extractor = WebContentExtractor(maxCharacters: 5_000)
        let html = """
        <html>
          <head><title>Atlas Research</title><style>body { color: red; }</style></head>
          <body>
            <nav>Ignore this navigation</nav>
            <main>
              <h1>Atlas Research</h1>
              <p>Project Atlas is a macOS-first AI operator.</p>
              <script>console.log('ignore')</script>
              <p>It keeps web research read-only and policy-gated.</p>
            </main>
          </body>
        </html>
        """

        let extraction = try extractor.extract(
            contentType: "text/html",
            body: html,
            pageURL: URL(string: "https://example.com/research")!
        )

        XCTAssertEqual(extraction.title, "Atlas Research")
        XCTAssertTrue(extraction.extractedText.contains("Project Atlas is a macOS-first AI operator."))
        XCTAssertTrue(extraction.extractedText.contains("It keeps web research read-only and policy-gated."))
        XCTAssertFalse(extraction.extractedText.contains("Ignore this navigation"))
        XCTAssertFalse(extraction.extractedText.contains("console.log"))
    }

    func testWebResearchSkillFetchPageRejectsBlockedTarget() async throws {
        let skill = WebResearchSkill(
            searchProvider: MockSearchProvider(),
            fetchClient: MockFetchClient(),
            orchestrator: MockResearchOrchestrator()
        )

        let context = makeContext()

        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "web.fetch_page",
                input: AtlasToolInput(argumentsJSON: #"{"url":"http://localhost:8080"}"#),
                context: context
            )
        )
    }

    func testWebResearchSkillResearchReturnsStructuredOutput() async throws {
        let skill = WebResearchSkill(
            searchProvider: MockSearchProvider(),
            fetchClient: MockFetchClient(),
            orchestrator: MockResearchOrchestrator()
        )

        let result = try await skill.execute(
            actionID: "web.research",
            input: AtlasToolInput(argumentsJSON: #"{"query":"atlas operator","maxSources":2}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WebResearchOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.sources.count, 2)
        XCTAssertFalse(decoded.summary.isEmpty)
        XCTAssertFalse(decoded.keyPoints.isEmpty)
    }

    func testWebSearchActionReturnsNormalizedResults() async throws {
        let skill = WebResearchSkill(
            searchProvider: MockSearchProvider(),
            fetchClient: MockFetchClient(),
            orchestrator: MockResearchOrchestrator()
        )

        let result = try await skill.execute(
            actionID: "web.search",
            input: AtlasToolInput(argumentsJSON: #"{"query":"atlas","maxResults":3}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WebSearchOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.results.count, 2)
        XCTAssertEqual(decoded.results.first?.domain, "example.com")
    }

    func testWebToolSchemaIncludesArrayItemsForOpenAI() throws {
        let skill = WebResearchSkill(
            searchProvider: MockSearchProvider(),
            fetchClient: MockFetchClient(),
            orchestrator: MockResearchOrchestrator()
        )

        let searchAction = try XCTUnwrap(skill.actions.first(where: { $0.id == "web.search" }))
        let data = try AtlasJSON.encoder.encode(searchAction.inputSchema)
        let jsonObject = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let properties = try XCTUnwrap(jsonObject["properties"] as? [String: Any])
        let allowedDomains = try XCTUnwrap(properties["allowedDomains"] as? [String: Any])
        let items = try XCTUnwrap(allowedDomains["items"] as? [String: Any])

        XCTAssertEqual(allowedDomains["type"] as? String, "array")
        XCTAssertEqual(items["type"] as? String, "string")
    }

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }
}

private actor MockSearchProvider: WebSearchProviding {
    nonisolated let providerName = "Mock Search"

    nonisolated func validateProvider() -> WebSearchProviderValidation {
        WebSearchProviderValidation(isAvailable: true, summary: "Mock search available.")
    }

    func search(query: String, allowedDomains: [String]?, maxResults: Int) async throws -> [WebSearchResult] {
        Array([
            WebSearchResult(
                title: "Atlas Overview",
                url: "https://example.com/atlas",
                snippet: "Atlas is a native macOS AI operator.",
                domain: "example.com"
            ),
            WebSearchResult(
                title: "Atlas Research Notes",
                url: "https://docs.example.org/atlas",
                snippet: "The web skill stays read-only.",
                domain: "docs.example.org"
            )
        ].prefix(maxResults))
    }
}

private actor MockFetchClient: WebFetching {
    nonisolated func validateClient() -> [String] {
        []
    }

    func fetchResource(
        from url: URL,
        allowedDomains: [String]?,
        acceptedContentTypes: Set<String>
    ) async throws -> WebFetchedResource {
        WebFetchedResource(
            url: url,
            contentType: "text/html",
            fetchedAt: .now,
            body: """
            <html><head><title>Example</title></head><body><p>Atlas uses read-only web research.</p></body></html>
            """,
            responseTruncated: false
        )
    }
}

private actor MockResearchOrchestrator: WebResearchOrchestrating {
    func research(
        query: String,
        allowedDomains: [String]?,
        maxSources: Int
    ) async throws -> WebResearchOutput {
        WebResearchOutput(
            summary: "Atlas reviewed public sources about \(query).",
            keyPoints: [
                "Atlas keeps web research read-only.",
                "Atlas blocks private and internal targets."
            ],
            sources: [
                WebResearchSource(
                    title: "Atlas Overview",
                    url: "https://example.com/atlas",
                    domain: "example.com",
                    snippet: "Atlas overview.",
                    excerpt: "Atlas keeps web research read-only."
                ),
                WebResearchSource(
                    title: "Atlas Policy",
                    url: "https://docs.example.org/policy",
                    domain: "docs.example.org",
                    snippet: "Atlas policy.",
                    excerpt: "Atlas blocks private and internal targets."
                )
            ],
            caveats: [],
            confidenceSummary: "Moderate confidence."
        )
    }
}

private func XCTAssertThrowsErrorAsync(
    _ expression: @autoclosure () async throws -> some Any,
    file: StaticString = #filePath,
    line: UInt = #line
) async {
    do {
        _ = try await expression()
        XCTFail("Expected error to be thrown.", file: file, line: line)
    } catch {
    }
}
