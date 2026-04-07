import AtlasShared

public struct BuiltInSkillsProvider: Sendable {
    private let fileAccessScopeStore: FileAccessScopeStore
    private let gremlinManaging: (any GremlinManaging)?
    private let forgeOrchestrationHandlers: ForgeOrchestrationHandlers?
    /// When provided, enables Gate 8 (credential readiness) and the dry-run validator
    /// in `ForgeOrchestrationSkill`. In production, `AgentRuntime` always passes this.
    /// Tests that do not supply it get Gates 7-only validation (no Gate 8, no dry-run).
    private let forgeCoreSkills: CoreSkillsRuntime?
    /// The delivery adapter for `SystemActionSkill` notifications.
    /// Defaults to `RelayNotificationSink` (daemon-context relay).
    /// Inject `UNNotificationSink` when skills run inside the Atlas app process.
    private let notificationSink: any NotificationSink

    public init(
        fileAccessScopeStore: FileAccessScopeStore = FileAccessScopeStore(),
        gremlinManaging: (any GremlinManaging)? = nil,
        forgeOrchestrationHandlers: ForgeOrchestrationHandlers? = nil,
        forgeCoreSkills: CoreSkillsRuntime? = nil,
        notificationSink: (any NotificationSink)? = nil
    ) {
        self.fileAccessScopeStore = fileAccessScopeStore
        self.gremlinManaging = gremlinManaging
        self.forgeOrchestrationHandlers = forgeOrchestrationHandlers
        self.forgeCoreSkills = forgeCoreSkills
        self.notificationSink = notificationSink ?? RelayNotificationSink()
    }

    public func makeSkills() -> [any AtlasSkill] {
        // Build Gate 8 + dry-run services from the CoreSkills runtime if available.
        let secretsService = forgeCoreSkills?.secrets
        let dryRunValidator = forgeCoreSkills.map { core in
            ForgeDryRunValidator(secretsService: core.secrets, httpService: core.http)
        }
        // Wire API Validation Service — runs before gates for API-kind proposals.
        let apiValidationService = forgeCoreSkills.map { core in
            APIValidationService(httpService: core.http, secretsService: core.secrets)
        }

        // Build the web search provider chain for WebResearchSkill.
        // Brave (API-key based) goes first if configured; DDG HTML is always the fallback.
        // One shared WebFetchClient is used for both DDG search fetches and skill page fetches
        // to avoid duplicate URLSession connection pools.
        let config = AtlasConfig()
        let webDomainPolicy = WebDomainPolicy()
        let webFetchClient = WebFetchClient(policy: webDomainPolicy)
        let webResearchProviders: [any WebSearchProviding] = {
            var chain: [any WebSearchProviding] = []
            if config.hasBraveSearchAPIKey(), let key = try? config.braveSearchAPIKey() {
                chain.append(BraveSearchAPIProvider(apiKey: key))
            }
            chain.append(DuckDuckGoHTMLSearchProvider(fetchClient: webFetchClient, domainPolicy: webDomainPolicy))
            return chain
        }()
        // Finance skill — pick the best available provider (Finnhub > Alpha Vantage > Yahoo default)
        let financeProvider: (any FinanceProvider)? = {
            let config = AtlasConfig()
            if config.hasFinnhubAPIKey(), let key = try? config.finnhubAPIKey() {
                return FinnhubProvider(apiKey: key)
            }
            if config.hasAlphaVantageAPIKey(), let key = try? config.alphaVantageAPIKey() {
                return AlphaVantageProvider(apiKey: key)
            }
            return nil // nil → FinanceSkill uses YahooFinanceProvider as default
        }()

        var skills: [any AtlasSkill] = [
            AtlasInfoSkill(),
            InfoSkill(),
            VisionSkill(),
            ImageGenerationSkill(),
            WeatherSkill(),
            WebResearchSkill(providers: webResearchProviders, fetchClient: webFetchClient, useJinaReader: config.webResearchUseJinaReader),
            WebSearchAPISkill(),
            FileSystemSkill(scopeStore: fileAccessScopeStore),
            SystemActionSkill(scopeStore: fileAccessScopeStore, notificationSink: notificationSink),
            AppleScriptSkill(),
            FinanceSkill(provider: financeProvider),
            ForgeOrchestrationSkill(
                handlers: forgeOrchestrationHandlers,
                secretsService: secretsService,
                dryRunValidator: dryRunValidator,
                apiValidationService: apiValidationService
            )
        ]
        if let gremlinManaging {
            skills.append(GremlinManagementSkill(gremlinManaging: gremlinManaging))
        }
        return skills
    }
}
