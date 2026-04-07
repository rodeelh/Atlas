import XCTest
@testable import AtlasCore
import AtlasGuard
import AtlasLogging
import AtlasMemory
import AtlasShared
import AtlasSkills
import AtlasTools

// MARK: - ForgeRealWorldTests
//
// Integration tests that simulate the complete Forge pipeline using real-world API
// examples — contract, spec, and plans exactly as a well-researched agent would produce them.
//
// Test coverage:
//   Part 1 — Positive (5 diverse real APIs): each must produce a successful proposal
//     1. OpenWeatherMap    — GET, apiKeyQuery
//     2. GitHub REST API   — GET, bearerTokenStatic, path param
//     3. Spotify Web API   — GET, oauth2ClientCredentials
//     4. NewsAPI           — GET, apiKeyHeader
//     5. JSONPlaceholder   — POST, no auth, bodyFields key remapping
//
//   Part 2 — Negative (common agent mistakes): each must be caught by the correct gate
//     6. Missing exampleResponse with high confidence       → Gate 3b
//     7. Required path param missing from paramLocations    → Gate 5
//     8. Wrong auth type (oauth2AuthorizationCode)          → Gate 6
//     9. Missing authHeaderName for apiKeyHeader auth       → Gate 7
//    10. Static query field used for user param             → silent issue documented
//
//   Part 3 — POST body routing
//    11. bodyFields remapping routes correctly to proposal
//    12. staticBodyFields merged into body correctly
//    13. No bodyFields → legacy flat mode still creates proposal

final class ForgeRealWorldTests: XCTestCase {

    // MARK: - Helpers

    private func makeStore() throws -> ForgeProposalStore {
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("ForgeRealWorldTests-\(UUID().uuidString).sqlite3")
        let memoryStore = try MemoryStore(databasePath: url.path)
        return ForgeProposalStore(memoryStore: memoryStore)
    }

    private func makeHandlers(store: ForgeProposalStore) -> ForgeOrchestrationHandlers {
        let service = ForgeProposalService(store: store)
        let registry = SkillRegistry(
            defaults: UserDefaults(suiteName: "ForgeRealWorld.\(UUID().uuidString)")!
        )
        let coreSkills = CoreSkillsRuntime(
            registry: registry,
            secretsReader: { _ in nil }
        )
        Task { await service.configure(coreSkills: coreSkills, skillRegistry: registry) }
        return ForgeOrchestrationHandlers(
            startResearching: { title, _ in await service.startResearching(title: title, message: "").id },
            stopResearching: { id in await service.stopResearching(id: id) },
            createProposal: { spec, plans, summary, rationale, contractJSON in
                try await service.createProposal(
                    spec: spec, plans: plans, summary: summary,
                    rationale: rationale, contractJSON: contractJSON
                )
            }
        )
    }

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test.forge"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }

    /// Run a full proposal through ForgeOrchestrationSkill (no secrets/dryRun service — gates 8 + dry-run skipped).
    private func propose(
        spec: String,
        plans: String,
        contract: String,
        summary: String = "Test skill",
        kind: String = "api"
    ) async throws -> SkillExecutionResult {
        let store = try makeStore()
        let handlers = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: nil,    // Gate 8 skipped — credentials not verified in unit tests
            dryRunValidator: nil    // Dry-run skipped — no live network
        )
        let args: [String: Any] = [
            "kind": kind,
            "spec_json": spec,
            "plans_json": plans,
            "contract_json": contract,
            "summary": summary
        ]
        let data = try! JSONSerialization.data(withJSONObject: args)
        let input = AtlasToolInput(argumentsJSON: String(data: data, encoding: .utf8)!)
        return try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: input,
            context: makeContext()
        )
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 1 — Positive: Real-world APIs must all produce valid proposals
    // MARK: ─────────────────────────────────────────────────────────────────────

    // MARK: 1. OpenWeatherMap — GET, apiKeyQuery
    //
    // API: https://openweathermap.org/current
    // Auth: API key appended as ?appid=KEY
    // Key param routing: "q" (city) → query param, apiKeyQuery appends "appid" automatically
    //
    func testOpenWeatherMap_currentWeather_passesAllGates() async throws {
        let contract = """
        {
          "providerName": "OpenWeatherMap",
          "docsURL": "https://openweathermap.org/current",
          "docsQuality": "high",
          "baseURL": "https://api.openweathermap.org",
          "endpoint": "/data/2.5/weather",
          "method": "GET",
          "authType": "apiKeyQuery",
          "requiredParams": ["q"],
          "optionalParams": ["units", "lang"],
          "paramLocations": {"q": "query"},
          "exampleRequest": "GET /data/2.5/weather?q=London&appid=KEY&units=metric",
          "exampleResponse": "{\\"coord\\":{\\"lon\\":-0.1257,\\"lat\\":51.5085},\\"weather\\":[{\\"id\\":804,\\"main\\":\\"Clouds\\",\\"description\\":\\"overcast clouds\\"}],\\"main\\":{\\"temp\\":15.2,\\"feels_like\\":14.1,\\"humidity\\":80},\\"name\\":\\"London\\",\\"cod\\":200}",
          "expectedResponseFields": ["coord", "weather", "main", "name", "cod"],
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "openweathermap",
          "name": "OpenWeatherMap",
          "description": "Get current weather conditions and forecasts from OpenWeatherMap.",
          "category": "utility",
          "riskLevel": "low",
          "tags": ["weather", "forecast", "temperature"],
          "actions": [{
            "id": "openweathermap.current-weather",
            "name": "Current Weather",
            "description": "Get current weather conditions for a city by name.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {
                "q": {"type": "string", "description": "City name (e.g. 'London' or 'London,GB')"},
                "units": {"type": "string", "description": "Temperature units: standard (Kelvin), metric (Celsius), imperial (Fahrenheit)"}
              },
              "required": ["q"],
              "additionalProperties": false
            }
          }]
        }
        """
        // Note: "q" is NOT in `query` (static field) — it's in inputSchema and will be
        // routed as a URL query param at runtime because the method is GET.
        // `query` is empty — no static params needed.
        // apiKeyQuery injects "appid" automatically from Keychain.
        let plans = """
        [{
          "actionID": "openweathermap.current-weather",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://api.openweathermap.org/data/2.5/weather",
            "headers": {},
            "query": {},
            "authType": "apiKeyQuery",
            "authSecretKey": "com.projectatlas.openweathermap",
            "authQueryParamName": "appid"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Get current weather for any city using OpenWeatherMap.")
        XCTAssertTrue(result.success,
            "OpenWeatherMap (apiKeyQuery) must create a valid proposal. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("openweathermap"),
            "Proposal output should reference the skill ID. Output: \(result.output)")
    }

    // MARK: 2. GitHub REST API — GET, bearerTokenStatic, path param
    //
    // API: https://docs.github.com/en/rest/repos/repos#list-repositories-for-a-user
    // Auth: Personal Access Token as Bearer
    // Key param routing: "username" → {username} path substitution
    //                    optional "per_page", "sort" → appended as query params
    //
    func testGitHub_listUserRepos_passesAllGates() async throws {
        let contract = """
        {
          "providerName": "GitHub REST API",
          "docsURL": "https://docs.github.com/en/rest/repos/repos#list-repositories-for-a-user",
          "docsQuality": "high",
          "baseURL": "https://api.github.com",
          "endpoint": "/users/{username}/repos",
          "method": "GET",
          "authType": "bearerTokenStatic",
          "requiredParams": ["username"],
          "optionalParams": ["per_page", "sort", "type"],
          "paramLocations": {"username": "path"},
          "exampleRequest": "GET /users/torvalds/repos",
          "exampleResponse": "[{\\"id\\":2325298,\\"name\\":\\"linux\\",\\"full_name\\":\\"torvalds/linux\\",\\"private\\":false,\\"description\\":\\"Linux kernel source tree\\",\\"html_url\\":\\"https://github.com/torvalds/linux\\",\\"language\\":\\"C\\",\\"stargazers_count\\":185000}]",
          "expectedResponseFields": ["id", "name", "full_name", "private", "description", "html_url"],
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "github-repos",
          "name": "GitHub Repositories",
          "description": "Browse public repositories and user profiles via the GitHub REST API.",
          "category": "developer",
          "riskLevel": "low",
          "tags": ["github", "repos", "developer", "code"],
          "actions": [{
            "id": "github-repos.list-user-repos",
            "name": "List User Repositories",
            "description": "List public repositories for a GitHub user, sorted and paginated.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {
                "username": {"type": "string", "description": "GitHub username (e.g. 'torvalds')"},
                "per_page": {"type": "string", "description": "Number of repos per page (1-100, default 30)"},
                "sort": {"type": "string", "description": "Sort order: created, updated, pushed, or full_name"}
              },
              "required": ["username"],
              "additionalProperties": false
            }
          }]
        }
        """
        // "username" is substituted into {username} in the URL path.
        // "per_page" and "sort" are appended as query params automatically (GET, not path-substituted).
        // Static header "Accept: application/vnd.github+json" is hardcoded in headers.
        let plans = """
        [{
          "actionID": "github-repos.list-user-repos",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://api.github.com/users/{username}/repos",
            "headers": {
              "Accept": "application/vnd.github+json",
              "X-GitHub-Api-Version": "2022-11-28"
            },
            "query": {},
            "authType": "bearerTokenStatic",
            "authSecretKey": "com.projectatlas.github"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "List a GitHub user's public repositories.")
        XCTAssertTrue(result.success,
            "GitHub (bearerTokenStatic + path param) must create a valid proposal. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("github-repos"),
            "Proposal output should reference the skill ID. Output: \(result.output)")
    }

    // MARK: 3. Spotify Web API — GET, oauth2ClientCredentials
    //
    // API: https://developer.spotify.com/documentation/web-api/reference/search
    // Auth: OAuth 2.0 Client Credentials — token endpoint https://accounts.spotify.com/api/token
    // Key param routing: "q" and "type" → query params (GET)
    //
    func testSpotify_searchTracks_oauth2ClientCredentials_passesAllGates() async throws {
        let contract = """
        {
          "providerName": "Spotify Web API",
          "docsURL": "https://developer.spotify.com/documentation/web-api/reference/search",
          "docsQuality": "high",
          "baseURL": "https://api.spotify.com",
          "endpoint": "/v1/search",
          "method": "GET",
          "authType": "oauth2ClientCredentials",
          "requiredParams": ["q", "type"],
          "optionalParams": ["limit", "offset", "market"],
          "paramLocations": {"q": "query", "type": "query"},
          "exampleRequest": "GET /v1/search?q=Radiohead&type=artist",
          "exampleResponse": "{\\"artists\\":{\\"href\\":\\"https://api.spotify.com/v1/search?query=Radiohead\\",\\"items\\":[{\\"id\\":\\"4Z8W4fKeB5YxbusRsdQVPb\\",\\"name\\":\\"Radiohead\\",\\"popularity\\":80,\\"type\\":\\"artist\\",\\"genres\\":[\\"alternative rock\\"]}],\\"total\\":1,\\"limit\\":20,\\"offset\\":0}}",
          "expectedResponseFields": ["artists"],
          "mappingConfidence": "high",
          "validationStatus": "unknown",
          "notes": "Client Credentials flow does not require user login. Suitable for searching public catalog."
        }
        """
        let spec = """
        {
          "id": "spotify",
          "name": "Spotify",
          "description": "Search for music, artists, albums, and playlists on Spotify.",
          "category": "utility",
          "riskLevel": "low",
          "tags": ["spotify", "music", "search", "streaming"],
          "actions": [{
            "id": "spotify.search",
            "name": "Search Spotify",
            "description": "Search for tracks, artists, albums, or playlists by keyword.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {
                "q": {"type": "string", "description": "Search query (e.g. 'Radiohead' or 'track:Creep artist:Radiohead')"},
                "type": {"type": "string", "description": "Comma-separated types to search: track, artist, album, playlist"},
                "limit": {"type": "string", "description": "Max results per type (1–50, default 20)"},
                "market": {"type": "string", "description": "ISO 3166-1 alpha-2 country code to filter results (e.g. US)"}
              },
              "required": ["q", "type"],
              "additionalProperties": false
            }
          }]
        }
        """
        // oauth2ClientCredentials: set oauth2TokenURL + oauth2ClientIDKey + oauth2ClientSecretKey.
        // No authSecretKey needed (that's for single-key auth types).
        // No oauth2Scope needed for the public search endpoint.
        let plans = """
        [{
          "actionID": "spotify.search",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://api.spotify.com/v1/search",
            "headers": {},
            "query": {},
            "authType": "oauth2ClientCredentials",
            "oauth2TokenURL": "https://accounts.spotify.com/api/token",
            "oauth2ClientIDKey": "com.projectatlas.spotify.clientid",
            "oauth2ClientSecretKey": "com.projectatlas.spotify.secret"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Search Spotify's music catalog for tracks, artists, and albums.")
        XCTAssertTrue(result.success,
            "Spotify (oauth2ClientCredentials) must create a valid proposal. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("spotify"),
            "Proposal output should reference the skill ID. Output: \(result.output)")
    }

    // MARK: 4. NewsAPI — GET, apiKeyHeader
    //
    // API: https://newsapi.org/docs/endpoints/top-headlines
    // Auth: API key in X-Api-Key header
    // Key param routing: "country" → query param (GET)
    //
    func testNewsAPI_topHeadlines_apiKeyHeader_passesAllGates() async throws {
        let contract = """
        {
          "providerName": "NewsAPI",
          "docsURL": "https://newsapi.org/docs/endpoints/top-headlines",
          "docsQuality": "high",
          "baseURL": "https://newsapi.org",
          "endpoint": "/v2/top-headlines",
          "method": "GET",
          "authType": "apiKeyHeader",
          "requiredParams": ["country"],
          "optionalParams": ["category", "q", "pageSize", "page"],
          "paramLocations": {"country": "query"},
          "exampleRequest": "GET /v2/top-headlines?country=us",
          "exampleResponse": "{\\"status\\":\\"ok\\",\\"totalResults\\":70,\\"articles\\":[{\\"source\\":{\\"id\\":\\"cnn\\",\\"name\\":\\"CNN\\"},\\"author\\":\\"John Doe\\",\\"title\\":\\"Breaking: Major Event\\",\\"description\\":\\"Details about the event.\\",\\"url\\":\\"https://cnn.com/article\\",\\"publishedAt\\":\\"2024-01-15T12:00:00Z\\"}]}",
          "expectedResponseFields": ["status", "totalResults", "articles"],
          "mappingConfidence": "high",
          "validationStatus": "unknown",
          "notes": "The 'sources' and 'country' params cannot be used simultaneously."
        }
        """
        let spec = """
        {
          "id": "newsapi",
          "name": "NewsAPI",
          "description": "Get top news headlines and search articles from hundreds of news sources.",
          "category": "research",
          "riskLevel": "low",
          "tags": ["news", "headlines", "articles", "journalism"],
          "actions": [{
            "id": "newsapi.top-headlines",
            "name": "Top Headlines",
            "description": "Get top headlines for a country, optionally filtered by category or keyword.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {
                "country": {"type": "string", "description": "2-letter ISO 3166-1 country code (e.g. us, gb, de, au)"},
                "category": {"type": "string", "description": "Category: business, entertainment, general, health, science, sports, technology"},
                "q": {"type": "string", "description": "Keywords to search for in headlines"},
                "pageSize": {"type": "string", "description": "Number of results per page (1-100, default 20)"}
              },
              "required": ["country"],
              "additionalProperties": false
            }
          }]
        }
        """
        // apiKeyHeader: authHeaderName must be the exact header name the API expects.
        // NewsAPI uses "X-Api-Key" (note capitalisation — this is the documented header name).
        let plans = """
        [{
          "actionID": "newsapi.top-headlines",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://newsapi.org/v2/top-headlines",
            "headers": {},
            "query": {},
            "authType": "apiKeyHeader",
            "authSecretKey": "com.projectatlas.newsapi",
            "authHeaderName": "X-Api-Key"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Fetch top news headlines by country from NewsAPI.")
        XCTAssertTrue(result.success,
            "NewsAPI (apiKeyHeader) must create a valid proposal. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("newsapi"),
            "Proposal output should reference the skill ID. Output: \(result.output)")
    }

    // MARK: 5. JSONPlaceholder — POST, no auth, bodyFields key remapping
    //
    // API: https://jsonplaceholder.typicode.com/posts (public, no auth, accepts JSON body)
    // Demonstrates bodyFields: agent's inputSchema uses readable names ("title", "body", "userId")
    // that happen to match the API body keys here — but we also test staticBodyFields.
    //
    func testJSONPlaceholder_createPost_POST_bodyFields_passesAllGates() async throws {
        let contract = """
        {
          "providerName": "JSONPlaceholder",
          "docsURL": "https://jsonplaceholder.typicode.com",
          "docsQuality": "high",
          "baseURL": "https://jsonplaceholder.typicode.com",
          "endpoint": "/posts",
          "method": "POST",
          "authType": "none",
          "requiredParams": ["title", "body", "userId"],
          "optionalParams": [],
          "paramLocations": {"title": "body", "body": "body", "userId": "body"},
          "exampleRequest": "POST /posts with body {title, body, userId}",
          "exampleResponse": "{\\"id\\":101,\\"title\\":\\"My Post\\",\\"body\\":\\"Post content here.\\",\\"userId\\":1}",
          "expectedResponseFields": ["id", "title", "body", "userId"],
          "mappingConfidence": "high",
          "validationStatus": "unknown",
          "notes": "JSONPlaceholder is a public fake REST API for testing. Responses are simulated."
        }
        """
        let spec = """
        {
          "id": "jsonplaceholder",
          "name": "JSONPlaceholder",
          "description": "Fake REST API for testing and prototyping — create and fetch posts.",
          "category": "developer",
          "riskLevel": "low",
          "tags": ["testing", "mock", "rest", "api"],
          "actions": [{
            "id": "jsonplaceholder.create-post",
            "name": "Create Post",
            "description": "Create a new post. Returns the created post with an assigned ID.",
            "permissionLevel": "draft",
            "inputSchema": {
              "type": "object",
              "properties": {
                "postTitle": {"type": "string", "description": "Title of the post"},
                "postBody": {"type": "string", "description": "Body text of the post"},
                "authorId": {"type": "string", "description": "User ID of the author (integer as string)"}
              },
              "required": ["postTitle", "postBody", "authorId"],
              "additionalProperties": false
            }
          }]
        }
        """
        // bodyFields maps agent inputSchema names → API body key names:
        //   "postTitle" → "title", "postBody" → "body", "authorId" → "userId"
        // authorId will be coerced from String to Int by coerceJSONValue.
        // No staticBodyFields needed here.
        let plans = """
        [{
          "actionID": "jsonplaceholder.create-post",
          "type": "http",
          "httpRequest": {
            "method": "POST",
            "url": "https://jsonplaceholder.typicode.com/posts",
            "headers": {},
            "query": {},
            "authType": "none",
            "bodyFields": {
              "title":  "postTitle",
              "body":   "postBody",
              "userId": "authorId"
            }
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Create posts via JSONPlaceholder fake REST API.")
        XCTAssertTrue(result.success,
            "JSONPlaceholder POST with bodyFields must create a valid proposal. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("jsonplaceholder"),
            "Proposal output should reference the skill ID. Output: \(result.output)")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 2 — Negative: Common agent mistakes caught by specific gates
    // MARK: ─────────────────────────────────────────────────────────────────────

    // MARK: 6. Gate 3b: Claims high confidence but omits exampleResponse
    //
    // Realistic scenario: agent reads docs page that has no example response section,
    // sets mappingConfidence "high" anyway, and leaves exampleResponse blank.
    // Expected: Gate 3b fires → needsClarification, no proposal created.
    //
    func testGate3b_highConfidenceWithoutExampleResponse_isRejected() async throws {
        let contract = """
        {
          "providerName": "Some Weather API",
          "docsURL": "https://api.someweather.example/docs",
          "docsQuality": "high",
          "baseURL": "https://api.someweather.example",
          "endpoint": "/v1/current",
          "method": "GET",
          "authType": "apiKeyQuery",
          "requiredParams": ["city"],
          "optionalParams": [],
          "paramLocations": {"city": "query"},
          "exampleRequest": "GET /v1/current?city=London&key=KEY",
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "someweather",
          "name": "Some Weather",
          "description": "Get weather data.",
          "category": "utility",
          "riskLevel": "low",
          "tags": ["weather"],
          "actions": [{
            "id": "someweather.current",
            "name": "Current",
            "description": "Get current weather.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {
                "city": {"type": "string", "description": "City name"}
              },
              "required": ["city"],
              "additionalProperties": false
            }
          }]
        }
        """
        let plans = """
        [{
          "actionID": "someweather.current",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://api.someweather.example/v1/current",
            "authType": "apiKeyQuery",
            "authSecretKey": "com.projectatlas.someweather",
            "authQueryParamName": "key"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Get current weather.")
        XCTAssertFalse(result.success,
            "Gate 3b: high confidence without exampleResponse must be rejected.")
        XCTAssertTrue(
            result.output.contains("exampleResponse") || result.output.contains("example response"),
            "Gate 3b: rejection message must reference exampleResponse. Output: \(result.output)"
        )
    }

    // MARK: 7. Gate 5: Required path param missing from paramLocations
    //
    // Realistic scenario: agent correctly uses {userID} in the URL path but forgets
    // to declare "userID": "path" in paramLocations. Common oversight.
    // Expected: Gate 5 fires → needsClarification listing "userID", no proposal created.
    //
    func testGate5_requiredPathParamMissingFromParamLocations_isRejected() async throws {
        let contract = """
        {
          "providerName": "Example User API",
          "docsURL": "https://api.example.com/docs/users",
          "docsQuality": "high",
          "baseURL": "https://api.example.com",
          "endpoint": "/v1/users/{userID}/profile",
          "method": "GET",
          "authType": "bearerTokenStatic",
          "requiredParams": ["userID"],
          "optionalParams": [],
          "paramLocations": {},
          "exampleResponse": "{\\"id\\":\\"u123\\",\\"name\\":\\"Jane Smith\\",\\"email\\":\\"jane@example.com\\"}",
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "example-users",
          "name": "Example Users",
          "description": "Fetch user profiles.",
          "category": "utility",
          "riskLevel": "low",
          "tags": ["users"],
          "actions": [{
            "id": "example-users.get-profile",
            "name": "Get Profile",
            "description": "Get a user profile.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {
                "userID": {"type": "string", "description": "User ID"}
              },
              "required": ["userID"],
              "additionalProperties": false
            }
          }]
        }
        """
        let plans = """
        [{
          "actionID": "example-users.get-profile",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://api.example.com/v1/users/{userID}/profile",
            "authType": "bearerTokenStatic",
            "authSecretKey": "com.projectatlas.exampleusers"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Fetch user profile.")
        XCTAssertFalse(result.success,
            "Gate 5: required param missing from paramLocations must be rejected.")
        XCTAssertTrue(
            result.output.contains("userID") || result.output.contains("location"),
            "Gate 5: rejection must mention the unmapped parameter. Output: \(result.output)"
        )
    }

    // MARK: 8. Gate 6: Auth type is oauth2AuthorizationCode (browser flow, not supported)
    //
    // Realistic scenario: agent looks at a social login API and correctly identifies it
    // uses OAuth 2.0, but picks the wrong subtype (authorization code vs. client credentials).
    // Expected: Gate 6 hard refusal, no proposal created.
    //
    func testGate6_oauth2AuthorizationCode_isHardRefusal() async throws {
        let contract = """
        {
          "providerName": "Social Login API",
          "docsURL": "https://example-social.com/docs/oauth",
          "docsQuality": "high",
          "baseURL": "https://api.example-social.com",
          "endpoint": "/v1/me",
          "method": "GET",
          "authType": "oauth2AuthorizationCode",
          "requiredParams": [],
          "optionalParams": [],
          "paramLocations": {},
          "exampleResponse": "{\\"id\\":\\"user123\\",\\"name\\":\\"Alice\\",\\"email\\":\\"alice@example.com\\"}",
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "social-profile",
          "name": "Social Profile",
          "description": "Get user profile from social login API.",
          "category": "utility",
          "riskLevel": "medium",
          "tags": ["social", "oauth"],
          "actions": [{
            "id": "social-profile.get-me",
            "name": "Get My Profile",
            "description": "Get the authenticated user profile.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {},
              "required": [],
              "additionalProperties": false
            }
          }]
        }
        """
        let plans = """
        [{
          "actionID": "social-profile.get-me",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://api.example-social.com/v1/me",
            "authType": "oauth2AuthorizationCode"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Get user profile via social login.")
        XCTAssertFalse(result.success,
            "Gate 6: oauth2AuthorizationCode must be hard-refused.")
        XCTAssertTrue(
            result.output.lowercased().contains("oauth") || result.output.contains("authorization"),
            "Gate 6: refusal must explain the auth type limitation. Output: \(result.output)"
        )
        // Must not be a clarification — this is a hard refusal
        XCTAssertFalse(
            result.output.contains("could you provide") || result.output.contains("need a few more details"),
            "Gate 6: must be a hard refusal, not a clarification. Output: \(result.output)"
        )
    }

    // MARK: 9. Gate 7: apiKeyHeader without authHeaderName — incomplete auth plan
    //
    // Realistic scenario: agent sets authType "apiKeyHeader" and authSecretKey correctly,
    // but forgets authHeaderName (which header to inject the key into).
    // Expected: Gate 7 needsClarification asking for authHeaderName.
    //
    func testGate7_apiKeyHeader_missingAuthHeaderName_isRejected() async throws {
        let contract = """
        {
          "providerName": "Some API",
          "docsURL": "https://api.some-service.example/docs",
          "docsQuality": "high",
          "baseURL": "https://api.some-service.example",
          "endpoint": "/v1/data",
          "method": "GET",
          "authType": "apiKeyHeader",
          "requiredParams": [],
          "optionalParams": [],
          "paramLocations": {},
          "exampleResponse": "{\\"data\\":[],\\"count\\":0}",
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "some-service",
          "name": "Some Service",
          "description": "Fetch data from some service.",
          "category": "utility",
          "riskLevel": "low",
          "tags": ["data"],
          "actions": [{
            "id": "some-service.get-data",
            "name": "Get Data",
            "description": "Get data.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {},
              "required": [],
              "additionalProperties": false
            }
          }]
        }
        """
        // authType = apiKeyHeader but authHeaderName is missing (null) — Gate 7 must catch this
        let plans = """
        [{
          "actionID": "some-service.get-data",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://api.some-service.example/v1/data",
            "authType": "apiKeyHeader",
            "authSecretKey": "com.projectatlas.someservice",
            "authHeaderName": null
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Get data from some service.")
        XCTAssertFalse(result.success,
            "Gate 7: apiKeyHeader with missing authHeaderName must be rejected.")
        XCTAssertTrue(
            result.output.contains("authHeaderName"),
            "Gate 7: rejection must mention the missing authHeaderName field. Output: \(result.output)"
        )
    }

    // MARK: 10. Documentation: Static query field mistakenly used for user input
    //
    // This is a known agent mistake that PASSES the gates (no gate catches it) but will
    // cause incorrect runtime behavior: the user's query param is hardcoded into every
    // request regardless of what the user asks for.
    //
    // This test documents the current behavior so we know it passes the gate pipeline,
    // and notes that runtime behavior will be wrong (q is always "cats" for every call).
    //
    func testKnownGap_staticQueryFieldForUserInput_passesGatesButBehaviorIsWrong() async throws {
        let contract = """
        {
          "providerName": "Example Search API",
          "docsURL": "https://api.examplesearch.example/docs",
          "docsQuality": "high",
          "baseURL": "https://api.examplesearch.example",
          "endpoint": "/v1/search",
          "method": "GET",
          "authType": "none",
          "requiredParams": ["q"],
          "optionalParams": [],
          "paramLocations": {"q": "query"},
          "exampleResponse": "{\\"results\\":[{\\"title\\":\\"Example\\",\\"url\\":\\"https://example.com\\"}]}",
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "example-search",
          "name": "Example Search",
          "description": "Search the web.",
          "category": "research",
          "riskLevel": "low",
          "tags": ["search"],
          "actions": [{
            "id": "example-search.search",
            "name": "Search",
            "description": "Search.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {
                "q": {"type": "string", "description": "Search query"}
              },
              "required": ["q"],
              "additionalProperties": false
            }
          }]
        }
        """
        // KNOWN AGENT MISTAKE: "q" is placed in static `query` dict (hardcoded "cats")
        // instead of being left out (so the runtime routes it from user input).
        // This passes all gates but means every search will always search "cats" — not user's query.
        // No gate catches this because the gate doesn't cross-reference query keys with inputSchema.
        let plans = """
        [{
          "actionID": "example-search.search",
          "type": "http",
          "httpRequest": {
            "method": "GET",
            "url": "https://api.examplesearch.example/v1/search",
            "query": {"q": "cats"},
            "authType": "none"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Search the web.")
        // This INTENTIONALLY passes — document the known gap.
        // The proposal is created successfully but will exhibit wrong runtime behavior.
        // TODO: A future Gate 3c could cross-reference `query` keys with inputSchema param names
        //       and warn when a user-declared required input appears in static `query`.
        XCTAssertTrue(result.success,
            "Known gap: static query field misuse passes gates (no gate catches it). Output: \(result.output)")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Part 3 — POST body routing validation
    // MARK: ─────────────────────────────────────────────────────────────────────

    // MARK: 11. POST with bodyFields remapping creates a valid proposal
    //
    // The API expects {"searchTerm": "...", "maxCount": 10} but the agent's
    // inputSchema uses {"query": "...", "limit": "10"} — bodyFields bridges the gap.
    //
    func testPOST_bodyFieldsRemapping_createsValidProposal() async throws {
        let contract = """
        {
          "providerName": "Example POST Search API",
          "docsURL": "https://api.example-post.example/docs/search",
          "docsQuality": "high",
          "baseURL": "https://api.example-post.example",
          "endpoint": "/v1/search",
          "method": "POST",
          "authType": "bearerTokenStatic",
          "requiredParams": ["searchTerm"],
          "optionalParams": ["maxCount"],
          "paramLocations": {"searchTerm": "body", "maxCount": "body"},
          "exampleRequest": "POST /v1/search { \\"searchTerm\\": \\"cats\\", \\"maxCount\\": 10 }",
          "exampleResponse": "{\\"results\\":[{\\"id\\":1,\\"title\\":\\"Cat facts\\",\\"score\\":0.95}],\\"totalCount\\":42}",
          "expectedResponseFields": ["results", "totalCount"],
          "mappingConfidence": "high",
          "validationStatus": "unknown",
          "notes": "API body key is 'searchTerm' but skill uses 'query' for user-friendliness. Use bodyFields to remap."
        }
        """
        let spec = """
        {
          "id": "example-post-search",
          "name": "Example POST Search",
          "description": "Search using a POST endpoint with JSON body.",
          "category": "research",
          "riskLevel": "low",
          "tags": ["search", "post"],
          "actions": [{
            "id": "example-post-search.search",
            "name": "Search",
            "description": "Search using POST body.",
            "permissionLevel": "read",
            "inputSchema": {
              "type": "object",
              "properties": {
                "query": {"type": "string", "description": "Search query"},
                "limit": {"type": "string", "description": "Maximum number of results (default 10)"}
              },
              "required": ["query"],
              "additionalProperties": false
            }
          }]
        }
        """
        // bodyFields maps: API key "searchTerm" ← inputParam "query"
        //                  API key "maxCount"   ← inputParam "limit"
        // staticBodyFields: always send "version": "2" (hardcoded API requirement)
        let plans = """
        [{
          "actionID": "example-post-search.search",
          "type": "http",
          "httpRequest": {
            "method": "POST",
            "url": "https://api.example-post.example/v1/search",
            "headers": {},
            "query": {},
            "authType": "bearerTokenStatic",
            "authSecretKey": "com.projectatlas.examplepostsearch",
            "bodyFields": {
              "searchTerm": "query",
              "maxCount": "limit"
            },
            "staticBodyFields": {
              "version": "2"
            }
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Search using POST endpoint with remapped body keys.")
        XCTAssertTrue(result.success,
            "POST with bodyFields + staticBodyFields must create a valid proposal. Output: \(result.output)")
    }

    // MARK: 12. POST with staticBodyFields only (flat mode + static merge)
    //
    // All input params pass through with their original names (no bodyFields),
    // but a static "format": "json" is always merged into the body.
    //
    func testPOST_staticBodyFieldsOnly_legacyFlatMode_createsValidProposal() async throws {
        let contract = """
        {
          "providerName": "Example Flat POST API",
          "docsURL": "https://api.example-flat.example/docs",
          "docsQuality": "high",
          "baseURL": "https://api.example-flat.example",
          "endpoint": "/v2/create",
          "method": "POST",
          "authType": "apiKeyHeader",
          "requiredParams": ["name", "description"],
          "optionalParams": [],
          "paramLocations": {"name": "body", "description": "body"},
          "exampleResponse": "{\\"id\\":\\"item-001\\",\\"name\\":\\"My Item\\",\\"description\\":\\"Details\\",\\"format\\":\\"json\\"}",
          "expectedResponseFields": ["id", "name", "description"],
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "example-flat-post",
          "name": "Example Flat POST",
          "description": "Create items via POST with static body field.",
          "category": "utility",
          "riskLevel": "medium",
          "tags": ["post", "create"],
          "actions": [{
            "id": "example-flat-post.create",
            "name": "Create Item",
            "description": "Create a new item.",
            "permissionLevel": "draft",
            "inputSchema": {
              "type": "object",
              "properties": {
                "name": {"type": "string", "description": "Item name"},
                "description": {"type": "string", "description": "Item description"}
              },
              "required": ["name", "description"],
              "additionalProperties": false
            }
          }]
        }
        """
        // No bodyFields → flat mode: all input params sent with original names.
        // staticBodyFields adds "format": "json" to every request body.
        let plans = """
        [{
          "actionID": "example-flat-post.create",
          "type": "http",
          "httpRequest": {
            "method": "POST",
            "url": "https://api.example-flat.example/v2/create",
            "headers": {},
            "query": {},
            "authType": "apiKeyHeader",
            "authSecretKey": "com.projectatlas.exampleflatpost",
            "authHeaderName": "X-API-Key",
            "staticBodyFields": {
              "format": "json"
            }
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Create items via POST with static format field.")
        XCTAssertTrue(result.success,
            "POST with staticBodyFields only (flat mode) must create a valid proposal. Output: \(result.output)")
    }

    // MARK: 13. POST with no bodyFields and no staticBodyFields (pure legacy flat mode)
    //
    // Backward compatibility: old skills with no bodyFields/staticBodyFields still work.
    // All input params become the JSON body with their original names.
    //
    func testPOST_noBodyFieldsNoStatic_legacyCompatibility_createsValidProposal() async throws {
        let contract = """
        {
          "providerName": "Legacy API",
          "docsURL": "https://api.legacy.example/docs",
          "docsQuality": "high",
          "baseURL": "https://api.legacy.example",
          "endpoint": "/v1/items",
          "method": "POST",
          "authType": "bearerTokenStatic",
          "requiredParams": ["name"],
          "optionalParams": ["description"],
          "paramLocations": {"name": "body", "description": "body"},
          "exampleResponse": "{\\"id\\":1,\\"name\\":\\"Widget\\",\\"description\\":\\"A widget\\"}",
          "expectedResponseFields": ["id", "name", "description"],
          "mappingConfidence": "high",
          "validationStatus": "unknown"
        }
        """
        let spec = """
        {
          "id": "legacy-api",
          "name": "Legacy API",
          "description": "Create items via legacy POST.",
          "category": "utility",
          "riskLevel": "low",
          "tags": ["legacy"],
          "actions": [{
            "id": "legacy-api.create",
            "name": "Create",
            "description": "Create an item.",
            "permissionLevel": "draft",
            "inputSchema": {
              "type": "object",
              "properties": {
                "name": {"type": "string", "description": "Item name"},
                "description": {"type": "string", "description": "Optional description"}
              },
              "required": ["name"],
              "additionalProperties": false
            }
          }]
        }
        """
        // No bodyFields, no staticBodyFields — pure legacy flat body mode.
        // All input params sent as-is in the JSON body.
        let plans = """
        [{
          "actionID": "legacy-api.create",
          "type": "http",
          "httpRequest": {
            "method": "POST",
            "url": "https://api.legacy.example/v1/items",
            "authType": "bearerTokenStatic",
            "authSecretKey": "com.projectatlas.legacyapi"
          }
        }]
        """
        let result = try await propose(spec: spec, plans: plans, contract: contract,
                                       summary: "Create items via legacy POST API.")
        XCTAssertTrue(result.success,
            "Legacy POST with no bodyFields must create a valid proposal. Output: \(result.output)")
    }
}
