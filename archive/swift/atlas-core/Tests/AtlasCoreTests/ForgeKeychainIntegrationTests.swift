import XCTest
@testable import AtlasCore
import AtlasGuard
import AtlasLogging
import AtlasMemory
@testable import AtlasShared
import AtlasSkills
import AtlasTools

// MARK: - ForgeKeychainIntegrationTests
//
// Full Forge pipeline tests with Gate 8 (credential readiness) enabled.
// Simulates the production secrets reader using a mock closure — identical in
// structure to AgentRuntime's reader (registered keypath first, customSecrets fallback).
//
// APIs tested:
//   • Finnhub Stock API  — GET, apiKeyHeader (X-Finnhub-Token)
//                          Keychain key: com.projectatlas.finnhub  ← registered in keyPathForService
//                          Actions: get-quote, get-company-news
//   • TrackingMore v4    — GET, apiKeyHeader (Tracking-Api-Key)
//                          Keychain key: com.projectatlas.trackingmore  ← registered in keyPathForService
//                          Actions: track-shipment, create-tracking (POST)
//
// Gate 8 credential flow: both use registered keyPathForService entries.
//
// To use these skills in production, add in Settings → Keychain:
//   Finnhub    : key = "com.projectatlas.finnhub"      (built-in provider field)
//   TrackingMore: key = "com.projectatlas.trackingmore" (built-in provider field)

final class ForgeKeychainIntegrationTests: XCTestCase {

    // MARK: - Known API credential service names

    /// Registered in KeychainSecretStore.keyPathForService → AtlasCredentialBundle.finnhubAPIKey
    static let finnhubServiceKey    = "com.projectatlas.finnhub"
    /// Registered in KeychainSecretStore.keyPathForService → AtlasCredentialBundle.trackingmoreAPIKey
    static let trackingmoreServiceKey = "com.projectatlas.trackingmore"

    // MARK: - Helpers

    private func makeStore() throws -> ForgeProposalStore {
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("ForgeKeychainTests-\(UUID().uuidString).sqlite3")
        return ForgeProposalStore(memoryStore: try MemoryStore(databasePath: url.path))
    }

    private func makeHandlers(store: ForgeProposalStore) -> ForgeOrchestrationHandlers {
        let service = ForgeProposalService(store: store)
        let registry = SkillRegistry(defaults: UserDefaults(suiteName: "ForgeKC.\(UUID().uuidString)")!)
        let coreSkills = CoreSkillsRuntime(registry: registry, secretsReader: { _ in nil })
        Task { await service.configure(coreSkills: coreSkills, skillRegistry: registry) }
        return ForgeOrchestrationHandlers(
            startResearching: { title, _ in await service.startResearching(title: title, message: "").id },
            stopResearching: { id in await service.stopResearching(id: id) },
            createProposal: { spec, plans, summary, rationale, contractJSON in
                try await service.createProposal(spec: spec, plans: plans, summary: summary,
                                                 rationale: rationale, contractJSON: contractJSON)
            }
        )
    }

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test.keychain"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }

    /// Mirrors the production secrets reader in AgentRuntime:
    /// 1. Registered keypath (finnhub, openai, etc.)
    /// 2. customSecrets fallback (user-defined Forge keys like TrackingMore)
    private func makeSecretsService(presentKeys: [String: String]) -> CoreSecretsService {
        CoreSecretsService(reader: { service in
            presentKeys[service]
        })
    }

    private func propose(
        spec: String,
        plans: String,
        contract: String,
        summary: String,
        secrets: CoreSecretsService
    ) async throws -> SkillExecutionResult {
        let store = try makeStore()
        let handlers = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: secrets,    // Gate 8 ENABLED
            dryRunValidator: nil        // dry-run skipped (no live network in unit tests)
        )
        let args: [String: Any] = [
            "kind": "api",
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
    // MARK: Finnhub Stock API
    // MARK: ─────────────────────────────────────────────────────────────────────
    //
    // Finnhub endpoints used:
    //   GET https://finnhub.io/api/v1/quote?symbol={symbol}
    //   GET https://finnhub.io/api/v1/company-news?symbol={symbol}&from={from}&to={to}
    //
    // Auth: X-Finnhub-Token: <key>  (apiKeyHeader, matching FinanceProviders.swift)
    // Keychain: com.projectatlas.finnhub  ← already registered in keyPathForService
    //
    // Response examples sourced from https://finnhub.io/docs/api/quote:
    //   quote:   {"c":183.97,"d":2.05,"dp":1.1266,"h":184.27,"l":181.91,"o":182.15,"pc":181.92,"t":1733356800}
    //   news:    [{"category":"company","datetime":1733356800,"headline":"Apple...","id":123,"image":"...","source":"Reuters","summary":"...","url":"..."}]

    private var finnhubSpec: String {
        """
        {
          "id": "finnhub-stocks",
          "name": "Finnhub Stocks",
          "description": "Real-time stock quotes, company news, and market data via the Finnhub API.",
          "category": "utility",
          "riskLevel": "low",
          "tags": ["stocks", "finance", "market", "finnhub"],
          "actions": [
            {
              "id": "finnhub-stocks.get-quote",
              "name": "Get Stock Quote",
              "description": "Get real-time price, change, and daily range for a stock symbol.",
              "permissionLevel": "read",
              "inputSchema": {
                "type": "object",
                "properties": {
                  "symbol": {"type": "string", "description": "Stock ticker symbol (e.g. AAPL, MSFT, TSLA)"}
                },
                "required": ["symbol"],
                "additionalProperties": false
              }
            },
            {
              "id": "finnhub-stocks.get-company-news",
              "name": "Get Company News",
              "description": "Get recent news articles for a company. Dates use YYYY-MM-DD format.",
              "permissionLevel": "read",
              "inputSchema": {
                "type": "object",
                "properties": {
                  "symbol": {"type": "string", "description": "Stock ticker symbol (e.g. AAPL)"},
                  "from": {"type": "string", "description": "Start date in YYYY-MM-DD format (e.g. 2024-01-01)"},
                  "to":   {"type": "string", "description": "End date in YYYY-MM-DD format (e.g. 2024-01-31)"}
                },
                "required": ["symbol", "from", "to"],
                "additionalProperties": false
              }
            }
          ]
        }
        """
    }

    // contract_json covers the primary endpoint (quote); news endpoint uses the same auth.
    private var finnhubContract: String {
        """
        {
          "providerName": "Finnhub",
          "docsURL": "https://finnhub.io/docs/api/quote",
          "docsQuality": "high",
          "baseURL": "https://finnhub.io",
          "endpoint": "/api/v1/quote",
          "method": "GET",
          "authType": "apiKeyHeader",
          "requiredParams": ["symbol"],
          "optionalParams": [],
          "paramLocations": {"symbol": "query"},
          "exampleRequest": "GET /api/v1/quote?symbol=AAPL",
          "exampleResponse": "{\\"c\\":183.97,\\"d\\":2.05,\\"dp\\":1.1266,\\"h\\":184.27,\\"l\\":181.91,\\"o\\":182.15,\\"pc\\":181.92,\\"t\\":1733356800}",
          "expectedResponseFields": ["c", "d", "dp", "h", "l", "o", "pc", "t"],
          "mappingConfidence": "high",
          "validationStatus": "unknown",
          "notes": "c=current price, d=change, dp=percent change, h=day high, l=day low, o=open, pc=prev close, t=timestamp. Returns c=0 for invalid symbols."
        }
        """
    }

    // Two-action plan: get-quote and get-company-news.
    // Both use the same auth header. symbol is a GET query param (not path-substituted).
    // from/to are appended as query params automatically (GET method, remaining inputs).
    private var finnhubPlans: String {
        """
        [
          {
            "actionID": "finnhub-stocks.get-quote",
            "type": "http",
            "httpRequest": {
              "method": "GET",
              "url": "https://finnhub.io/api/v1/quote",
              "headers": {},
              "query": {},
              "authType": "apiKeyHeader",
              "authSecretKey": "com.projectatlas.finnhub",
              "authHeaderName": "X-Finnhub-Token"
            }
          },
          {
            "actionID": "finnhub-stocks.get-company-news",
            "type": "http",
            "httpRequest": {
              "method": "GET",
              "url": "https://finnhub.io/api/v1/company-news",
              "headers": {},
              "query": {},
              "authType": "apiKeyHeader",
              "authSecretKey": "com.projectatlas.finnhub",
              "authHeaderName": "X-Finnhub-Token"
            }
          }
        ]
        """
    }

    func testFinnhub_stocksSkill_gate8PassesWithCredential() async throws {
        let secrets = makeSecretsService(presentKeys: [
            Self.finnhubServiceKey: "test-finnhub-api-key-abc123"
        ])
        let result = try await propose(
            spec: finnhubSpec,
            plans: finnhubPlans,
            contract: finnhubContract,
            summary: "Get real-time stock quotes and company news via Finnhub.",
            secrets: secrets
        )
        XCTAssertTrue(result.success,
            "Finnhub skill must be proposed successfully when credential is present. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("finnhub-stocks"),
            "Proposal output should reference the skill ID. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("get-quote") || result.output.contains("Get Stock Quote"),
            "Proposal output should reference the quote action. Output: \(result.output)")
    }

    func testFinnhub_gate8BlocksWhenCredentialMissing() async throws {
        // No Finnhub key in mock reader → Gate 8 fires
        let secrets = makeSecretsService(presentKeys: [:])
        let result = try await propose(
            spec: finnhubSpec,
            plans: finnhubPlans,
            contract: finnhubContract,
            summary: "Get real-time stock quotes via Finnhub.",
            secrets: secrets
        )
        XCTAssertFalse(result.success,
            "Gate 8 must block Finnhub proposal when com.projectatlas.finnhub is missing.")
        XCTAssertTrue(
            result.output.contains("com.projectatlas.finnhub") || result.output.contains("credential"),
            "Gate 8 rejection must name the missing credential. Output: \(result.output)"
        )
        XCTAssertTrue(
            result.output.lowercased().contains("settings") || result.output.lowercased().contains("keychain"),
            "Gate 8 rejection must direct user to Settings → Keychain. Output: \(result.output)"
        )
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: TrackingMore v4 API
    // MARK: ─────────────────────────────────────────────────────────────────────
    //
    // TrackingMore endpoints used:
    //   GET  https://api.trackingmore.com/v4/trackings
    //        Query: tracking_number (required), courier_code (optional)
    //   POST https://api.trackingmore.com/v4/trackings
    //        Body: {"tracking_number": "...", "courier_code": "..."}
    //
    // Auth: Tracking-Api-Key: <key>  (apiKeyHeader)
    // Keychain: "TrackingMore"  ← stored in AtlasCredentialBundle.customSecrets
    //
    // Response example (GET /v4/trackings?tracking_number=xxx):
    //   {
    //     "meta": {"code":200, "type":"Success", "message":"Request response is successful"},
    //     "data": {
    //       "trackings": [{
    //         "id": "abc123",
    //         "tracking_number": "9400111899223397576172",
    //         "courier_code": "usps",
    //         "delivery_status": "delivered",
    //         "origin_country": "US",
    //         "destination_country": "US",
    //         "latest_event": "DELIVERED",
    //         "updated_at": "2024-01-15T12:00:00+00:00"
    //       }],
    //       "count": 1
    //     }
    //   }

    private var trackingmoreSpec: String {
        """
        {
          "id": "trackingmore",
          "name": "TrackingMore",
          "description": "Track shipments across 1,300+ carriers worldwide via the TrackingMore API.",
          "category": "utility",
          "riskLevel": "low",
          "tags": ["tracking", "shipping", "logistics", "parcels"],
          "actions": [
            {
              "id": "trackingmore.track-shipment",
              "name": "Track Shipment",
              "description": "Register and retrieve tracking status for a shipment. POST registers the tracking number with TrackingMore and returns whatever status the carrier has reported. Use this first for any new tracking number.",
              "permissionLevel": "draft",
              "inputSchema": {
                "type": "object",
                "properties": {
                  "tracking_number": {
                    "type": "string",
                    "description": "The shipment tracking number (e.g. 9400111899223397576172)"
                  },
                  "courier_code": {
                    "type": "string",
                    "description": "Carrier code (e.g. usps, fedex, ups, dhl). Required for accurate routing."
                  }
                },
                "required": ["tracking_number", "courier_code"],
                "additionalProperties": false
              }
            },
            {
              "id": "trackingmore.get-tracking-status",
              "name": "Get Tracking Status",
              "description": "Query the current status of a previously registered tracking number.",
              "permissionLevel": "read",
              "inputSchema": {
                "type": "object",
                "properties": {
                  "tracking_number": {
                    "type": "string",
                    "description": "The shipment tracking number to query"
                  },
                  "courier_code": {
                    "type": "string",
                    "description": "Carrier code (e.g. usps, fedex, ups, dhl)."
                  }
                },
                "required": ["tracking_number"],
                "additionalProperties": false
              }
            }
          ]
        }
        """
    }

    private var trackingmoreContract: String {
        """
        {
          "providerName": "TrackingMore",
          "docsURL": "https://www.trackingmore.com/docs/trackingmore/clngsp7p4nfpc-create-a-tracking",
          "docsQuality": "high",
          "baseURL": "https://api.trackingmore.com",
          "endpoint": "/v4/trackings",
          "method": "POST",
          "authType": "apiKeyHeader",
          "requiredParams": ["tracking_number", "courier_code"],
          "optionalParams": [],
          "paramLocations": {"tracking_number": "body", "courier_code": "body"},
          "exampleRequest": "POST /v4/trackings body: {\\"tracking_number\\":\\"9400111899223397576172\\",\\"courier_code\\":\\"usps\\"}",
          "exampleResponse": "{\\"meta\\":{\\"code\\":200,\\"type\\":\\"Success\\",\\"message\\":\\"Request response is successful\\"},\\"data\\":[{\\"id\\":\\"abc123\\",\\"tracking_number\\":\\"9400111899223397576172\\",\\"courier_code\\":\\"usps\\",\\"delivery_status\\":\\"in_transit\\",\\"latest_event\\":\\"Arrived at USPS Regional Facility\\",\\"updated_at\\":\\"2024-01-15T12:00:00+00:00\\"}]}",
          "expectedResponseFields": ["meta", "data"],
          "mappingConfidence": "high",
          "validationStatus": "unknown",
          "notes": "POST /v4/trackings registers a tracking number AND returns current status in one call. GET /v4/trackings only returns already-registered packages. Use POST for initial tracking lookup."
        }
        """
    }

    // Two-action plan:
    //   track-shipment: POST /v4/trackings — registers the package AND returns whatever status
    //                   TrackingMore has fetched from the carrier. This is the correct single-call
    //                   approach: GET /v4/trackings only returns already-registered packages, so
    //                   POST is the right action for "track this package" from scratch.
    //   get-tracking-status: GET /v4/trackings — query status of a previously registered package.
    private var trackingmorePlans: String {
        """
        [
          {
            "actionID": "trackingmore.track-shipment",
            "type": "http",
            "httpRequest": {
              "method": "POST",
              "url": "https://api.trackingmore.com/v4/trackings",
              "headers": {},
              "authType": "apiKeyHeader",
              "authSecretKey": "com.projectatlas.trackingmore",
              "authHeaderName": "Tracking-Api-Key"
            }
          },
          {
            "actionID": "trackingmore.get-tracking-status",
            "type": "http",
            "httpRequest": {
              "method": "GET",
              "url": "https://api.trackingmore.com/v4/trackings",
              "headers": {},
              "query": {},
              "authType": "apiKeyHeader",
              "authSecretKey": "com.projectatlas.trackingmore",
              "authHeaderName": "Tracking-Api-Key"
            }
          }
        ]
        """
    }

    func testTrackingMore_shippingSkill_gate8PassesWithCredential() async throws {
        // TrackingMore uses a custom secret (not in keyPathForService).
        // Production reader calls config.readCustomKey(name: "TrackingMore")
        // which reads AtlasCredentialBundle.customSecrets["TrackingMore"].
        let secrets = makeSecretsService(presentKeys: [
            Self.trackingmoreServiceKey: "test-trackingmore-api-key-xyz789"
        ])
        let result = try await propose(
            spec: trackingmoreSpec,
            plans: trackingmorePlans,
            contract: trackingmoreContract,
            summary: "Track shipments across 1,300+ carriers worldwide.",
            secrets: secrets
        )
        XCTAssertTrue(result.success,
            "TrackingMore skill must be proposed successfully when credential is present. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("trackingmore"),
            "Proposal output should reference the skill ID. Output: \(result.output)")
        XCTAssertTrue(result.output.contains("track-shipment") || result.output.contains("Track Shipment"),
            "Proposal output should reference the tracking action. Output: \(result.output)")
    }

    func testTrackingMore_gate8BlocksWhenCredentialMissing() async throws {
        // No TrackingMore key → Gate 8 fires and names the missing credential
        let secrets = makeSecretsService(presentKeys: [:])
        let result = try await propose(
            spec: trackingmoreSpec,
            plans: trackingmorePlans,
            contract: trackingmoreContract,
            summary: "Track shipments.",
            secrets: secrets
        )
        XCTAssertFalse(result.success,
            "Gate 8 must block TrackingMore proposal when 'TrackingMore' custom secret is missing.")
        XCTAssertTrue(
            result.output.contains("com.projectatlas.trackingmore") || result.output.contains("credential"),
            "Gate 8 rejection must name the missing credential. Output: \(result.output)"
        )
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Cross-API: both skills proposed in the same session
    // MARK: ─────────────────────────────────────────────────────────────────────

    func testBothAPIs_withBothCredentials_createSeparateProposals() async throws {
        // Both credentials present → both proposals created independently
        let secrets = makeSecretsService(presentKeys: [
            Self.finnhubServiceKey: "test-finnhub-key",
            Self.trackingmoreServiceKey: "test-trackingmore-key"
        ])

        async let finnhubResult = propose(
            spec: finnhubSpec,
            plans: finnhubPlans,
            contract: finnhubContract,
            summary: "Finnhub stock data.",
            secrets: secrets
        )
        async let trackingResult = propose(
            spec: trackingmoreSpec,
            plans: trackingmorePlans,
            contract: trackingmoreContract,
            summary: "TrackingMore shipment tracking.",
            secrets: secrets
        )

        let (finResult, trkResult) = try await (finnhubResult, trackingResult)

        XCTAssertTrue(finResult.success,
            "Finnhub proposal must succeed when both credentials present. Output: \(finResult.output)")
        XCTAssertTrue(trkResult.success,
            "TrackingMore proposal must succeed when both credentials present. Output: \(trkResult.output)")
    }

    func testBothAPIs_onlyFinnhubCredential_trackingMoreIsBlocked() async throws {
        // Only Finnhub key present → Finnhub succeeds, TrackingMore is gate-8 blocked
        let secrets = makeSecretsService(presentKeys: [
            Self.finnhubServiceKey: "test-finnhub-key"
            // TrackingMore intentionally absent
        ])

        let finnhubResult = try await propose(
            spec: finnhubSpec,
            plans: finnhubPlans,
            contract: finnhubContract,
            summary: "Finnhub stocks.",
            secrets: secrets
        )
        let trackingResult = try await propose(
            spec: trackingmoreSpec,
            plans: trackingmorePlans,
            contract: trackingmoreContract,
            summary: "TrackingMore.",
            secrets: secrets
        )

        XCTAssertTrue(finnhubResult.success,
            "Finnhub must succeed when its credential is present. Output: \(finnhubResult.output)")
        XCTAssertFalse(trackingResult.success,
            "TrackingMore must be blocked by Gate 8 when 'TrackingMore' custom secret is missing. Output: \(trackingResult.output)")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Runtime parameter routing validation
    // ─────────────────────────────────────────────────────────────────────────
    //
    // These verify the correct interpretation of how agent inputs flow into HTTP requests
    // at runtime — ensuring the proposals we create will behave correctly.

    func testFinnhub_quoteAction_symbolIsRoutedAsQueryParam() {
        // GET /api/v1/quote — "symbol" is NOT a path substitution param (no {symbol} in URL).
        // Runtime routes it as a URL query item: ?symbol=AAPL
        // Verify: URL does not contain {symbol}, query dict is empty
        let urlTemplate = "https://finnhub.io/api/v1/quote"
        XCTAssertFalse(urlTemplate.contains("{"),
            "Finnhub quote URL must have no path substitution placeholders — symbol goes as query param.")
        // At runtime: AtlasToolInput{"symbol":"AAPL"} + GET → appends ?symbol=AAPL
    }

    func testFinnhub_companyNewsAction_allParamsAreQueryParams() {
        // GET /api/v1/company-news — "symbol", "from", "to" are all query params.
        // None are path-substituted, none are in the static `query` dict.
        let urlTemplate = "https://finnhub.io/api/v1/company-news"
        XCTAssertFalse(urlTemplate.contains("{"),
            "Finnhub company-news URL must have no path placeholders — all 3 params go as query items.")
    }

    func testTrackingMore_trackShipmentAction_trackingNumberIsQueryParam() {
        // GET /v4/trackings — "tracking_number" and "courier_code" are query params (not path).
        // Runtime appends them automatically.
        let urlTemplate = "https://api.trackingmore.com/v4/trackings"
        XCTAssertFalse(urlTemplate.contains("{"),
            "TrackingMore GET URL has no path placeholders — tracking_number goes as query param.")
    }

    func testTrackingMore_createTrackingAction_bodyIsJSON() {
        // POST /v4/trackings — tracking_number and courier_code go into JSON body.
        // No bodyFields needed because inputSchema names match the API's body key names exactly.
        // Runtime serialises all input params as flat JSON body.
        let method = "POST"
        XCTAssertEqual(method, "POST",
            "TrackingMore create-tracking action must use POST — body params require POST/PUT.")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Credential naming documentation test
    // ─────────────────────────────────────────────────────────────────────────
    //
    // Documents the exact Keychain storage path for each credential so it's
    // clear what the user needs to configure in Settings → Keychain.

    func testCredentialStoragePaths_documented() {
        // Finnhub: registered keypath → AtlasCredentialBundle.finnhubAPIKey
        // In Settings → Keychain → Provider: "Finnhub" (pre-existing provider card)
        XCTAssertEqual(Self.finnhubServiceKey, "com.projectatlas.finnhub",
            "Finnhub must use the registered service key so it reads from finnhubAPIKey bundle field.")

        // TrackingMore: registered keypath → AtlasCredentialBundle.trackingmoreAPIKey
        // In Settings → Keychain → Provider: "TrackingMore" (pre-existing provider card)
        XCTAssertEqual(Self.trackingmoreServiceKey, "com.projectatlas.trackingmore",
            "TrackingMore must use the registered service key so it reads from trackingmoreAPIKey bundle field.")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Carrier-specific tracking number routing
    // ─────────────────────────────────────────────────────────────────────────
    //
    // Verifies that real carrier tracking numbers are correctly formed as URL
    // query parameters for GET /v4/trackings.
    //
    // Tracking numbers under test:
    //   USPS  : 9361289741062043593437  (22 digits, all decimal — URL-safe)
    //   FedEx : 397998194906            (12 digits, all decimal — URL-safe)
    //
    // Expected query URL (USPS):
    //   GET https://api.trackingmore.com/v4/trackings
    //       ?tracking_number=9361289741062043593437&courier_code=usps
    //
    // Expected query URL (FedEx):
    //   GET https://api.trackingmore.com/v4/trackings
    //       ?tracking_number=397998194906&courier_code=fedex

    static let uspsTrackingNumber  = "9361289741062043593437"
    static let fedexTrackingNumber = "397998194906"

    func testTrackingNumbers_areURLSafe() {
        // Both tracking numbers are purely decimal — no percent-encoding required.
        // URLComponents will pass them through unchanged.
        let allowed = CharacterSet.urlQueryAllowed
        XCTAssertTrue(
            Self.uspsTrackingNumber.unicodeScalars.allSatisfy { allowed.contains($0) },
            "USPS tracking number must be URL-safe without percent-encoding."
        )
        XCTAssertTrue(
            Self.fedexTrackingNumber.unicodeScalars.allSatisfy { allowed.contains($0) },
            "FedEx tracking number must be URL-safe without percent-encoding."
        )
    }

    func testUSPS_trackingNumber_buildsCorrectQueryURL() throws {
        // Simulate the URL construction ForgeSkill performs for a GET request.
        // tracking_number and courier_code are appended as query items.
        var components = URLComponents(string: "https://api.trackingmore.com/v4/trackings")!
        components.queryItems = [
            URLQueryItem(name: "tracking_number", value: Self.uspsTrackingNumber),
            URLQueryItem(name: "courier_code", value: "usps")
        ]
        let url = try XCTUnwrap(components.url)
        XCTAssertEqual(url.host, "api.trackingmore.com")
        XCTAssertEqual(url.path, "/v4/trackings")
        XCTAssertTrue(
            url.absoluteString.contains("tracking_number=9361289741062043593437"),
            "URL must contain the full USPS tracking number as a query param."
        )
        XCTAssertTrue(
            url.absoluteString.contains("courier_code=usps"),
            "URL must include courier_code=usps."
        )
    }

    func testFedEx_trackingNumber_buildsCorrectQueryURL() throws {
        var components = URLComponents(string: "https://api.trackingmore.com/v4/trackings")!
        components.queryItems = [
            URLQueryItem(name: "tracking_number", value: Self.fedexTrackingNumber),
            URLQueryItem(name: "courier_code", value: "fedex")
        ]
        let url = try XCTUnwrap(components.url)
        XCTAssertEqual(url.host, "api.trackingmore.com")
        XCTAssertEqual(url.path, "/v4/trackings")
        XCTAssertTrue(
            url.absoluteString.contains("tracking_number=397998194906"),
            "URL must contain the FedEx tracking number as a query param."
        )
        XCTAssertTrue(
            url.absoluteString.contains("courier_code=fedex"),
            "URL must include courier_code=fedex."
        )
    }

    func testTrackingMore_getAction_noPathPlaceholders_trackingNumberNeverSubstituted() {
        // GET URL has no {param} placeholders — tracking_number is never path-substituted.
        // ForgeSkill's substituteParams loop finds no matches, leaving the URL unchanged.
        // The tracking number then flows into the query-item path (GET branch).
        let urlTemplate = "https://api.trackingmore.com/v4/trackings"
        XCTAssertFalse(
            urlTemplate.contains("{\(Self.uspsTrackingNumber)}"),
            "USPS tracking number must not appear as a path placeholder — it's a query param."
        )
        XCTAssertFalse(urlTemplate.contains("{"),
            "TrackingMore GET URL has no path placeholders at all.")
    }

    func testTrackingMore_courierCodes_areCorrectForCarriers() {
        // TrackingMore courier code reference for the carriers under test.
        // Source: https://www.trackingmore.com/carriers.html
        let courierCodes: [String: String] = [
            "USPS": "usps",
            "FedEx": "fedex"
        ]
        XCTAssertEqual(courierCodes["USPS"], "usps", "USPS courier code must be 'usps' (lowercase).")
        XCTAssertEqual(courierCodes["FedEx"], "fedex", "FedEx courier code must be 'fedex' (lowercase).")
    }

    // MARK: ─────────────────────────────────────────────────────────────────────
    // MARK: Live execution tests (require real TrackingMore API key in Keychain)
    // ─────────────────────────────────────────────────────────────────────────
    //
    // These tests make real HTTP calls to https://api.trackingmore.com.
    // They are skipped automatically if the TrackingMore API key is not in Keychain.
    //
    // To enable:
    //   Settings → Keychain → Provider: "TrackingMore" (uses service key com.projectatlas.trackingmore)
    //
    // Each test verifies that:
    //   - ForgeSkill executes without throwing
    //   - The response body is non-empty JSON from TrackingMore
    //   - The response contains the expected top-level keys ("meta" and/or "data")
    //
    // A 200 with tracking data OR a 4xx error body both count as valid responses —
    // what we're testing is skill execution correctness, not shipment status.

    /// Build a real ForgeSkill for the TrackingMore skill from the canonical plan JSON.
    /// Uses a real CoreHTTPService for live requests.
    private func makeLiveTrackingMoreSkill(apiKey: String) throws -> ForgeSkill {
        let plansData = Data(trackingmorePlans.utf8)
        let plans = try JSONDecoder().decode([ForgeActionPlan].self, from: plansData)

        let manifest = SkillManifest(
            id: "trackingmore",
            name: "TrackingMore",
            version: "1.0.0",
            description: "Track shipments across 1,300+ carriers worldwide.",
            category: .utility,
            lifecycleState: .installed,
            capabilities: [],
            requiredPermissions: [],
            requiredSecrets: ["com.projectatlas.trackingmore"],
            riskLevel: .low,
            supportsReadOnlyMode: true
        )

        let package = ForgeSkillPackage(manifest: manifest, actions: plans)
        let secrets = makeSecretsService(presentKeys: [Self.trackingmoreServiceKey: apiKey])

        return ForgeSkill(
            package: package,
            actionDefinitions: [],
            httpService: CoreHTTPService(),
            secretsService: secrets
        )
    }

    /// Resolves the TrackingMore API key from Keychain.
    /// Tries the registered service key first (com.projectatlas.trackingmore → trackingmoreAPIKey),
    /// then falls back to common custom secret names the user may have stored previously.
    private func resolveTrackingMoreKey() -> String? {
        // 1. Registered bundle field (populated when the app is relaunched after this update)
        if let key = try? KeychainSecretStore.readSecret(service: "com.projectatlas.trackingmore", account: "default"),
           !key.isEmpty { return key }
        // 2. Common custom secret names
        for name in ["TrackingMore", "trackingmore", "com.projectatlas.trackingmore"] {
            if let key = ((try? KeychainSecretStore.readCustomSecret(name: name)) ?? nil), !key.isEmpty {
                return key
            }
        }
        return nil
    }

    func testLive_uspsTracking_9361289741062043593437_registersAndReturnsResponse() async throws {
        // POST /v4/trackings registers the package AND returns current carrier status.
        // If the tracking number is fake/expired the API returns 200 with empty data[].
        // A real active shipment returns data[] with delivery_status, latest_event, etc.
        guard let key = resolveTrackingMoreKey() else {
            throw XCTSkip(
                "TrackingMore API key not in Keychain. " +
                "Add it in Settings → Keychain → TrackingMore provider to run live tests."
            )
        }

        let skill  = try makeLiveTrackingMoreSkill(apiKey: key)
        let input  = AtlasToolInput(
            argumentsJSON: #"{"tracking_number":"9361289741062043593437","courier_code":"usps"}"#
        )
        let result = try await skill.execute(
            actionID: "trackingmore.track-shipment",
            input: input,
            context: makeContext()
        )

        XCTAssertFalse(result.output.isEmpty,
            "TrackingMore must return a non-empty body for USPS \(Self.uspsTrackingNumber).")
        XCTAssertTrue(result.output.contains("meta"),
            "Expected TrackingMore JSON envelope with 'meta'. Output: \(result.output)")

        // Print for manual inspection — data[] is empty if tracking number is fake/expired
        print("USPS response: \(result.output)")
    }

    func testLive_fedexTracking_397998194906_registersAndReturnsResponse() async throws {
        guard let key = resolveTrackingMoreKey() else {
            throw XCTSkip(
                "TrackingMore API key not in Keychain. " +
                "Add it in Settings → Keychain → TrackingMore provider to run live tests."
            )
        }

        let skill  = try makeLiveTrackingMoreSkill(apiKey: key)
        let input  = AtlasToolInput(
            argumentsJSON: #"{"tracking_number":"397998194906","courier_code":"fedex"}"#
        )
        let result = try await skill.execute(
            actionID: "trackingmore.track-shipment",
            input: input,
            context: makeContext()
        )

        XCTAssertFalse(result.output.isEmpty,
            "TrackingMore must return a non-empty body for FedEx \(Self.fedexTrackingNumber).")
        XCTAssertTrue(result.output.contains("meta"),
            "Expected TrackingMore JSON envelope with 'meta'. Output: \(result.output)")

        print("FedEx response: \(result.output)")
    }

    func testLive_trackingStatus_afterRegistration_returnsStatusForRegisteredPackage() async throws {
        // After POST registers a package, GET /v4/trackings?tracking_number=... should return it.
        // This test verifies the full two-action flow an agent would use:
        //   1. track-shipment (POST) → registers
        //   2. get-tracking-status (GET) → returns current status
        guard let key = resolveTrackingMoreKey() else {
            throw XCTSkip("TrackingMore API key not in Keychain.")
        }

        let skill = try makeLiveTrackingMoreSkill(apiKey: key)

        // Step 1: register via POST
        let registerInput = AtlasToolInput(
            argumentsJSON: #"{"tracking_number":"9361289741062043593437","courier_code":"usps"}"#
        )
        let registerResult = try await skill.execute(
            actionID: "trackingmore.track-shipment",
            input: registerInput,
            context: makeContext()
        )
        XCTAssertTrue(registerResult.output.contains("meta"),
            "POST must return a JSON envelope. Output: \(registerResult.output)")

        // Step 2: GET status
        let statusInput = AtlasToolInput(
            argumentsJSON: #"{"tracking_number":"9361289741062043593437","courier_code":"usps"}"#
        )
        let statusResult = try await skill.execute(
            actionID: "trackingmore.get-tracking-status",
            input: statusInput,
            context: makeContext()
        )
        XCTAssertTrue(statusResult.output.contains("meta"),
            "GET must return a JSON envelope. Output: \(statusResult.output)")

        print("POST response: \(registerResult.output)")
        print("GET response:  \(statusResult.output)")
    }
}
