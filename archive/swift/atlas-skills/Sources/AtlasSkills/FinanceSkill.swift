import Foundation
import AtlasTools
import AtlasShared

public struct FinanceSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]
    private let provider: any FinanceProvider

    public init(provider: (any FinanceProvider)? = nil) {
        self.provider = provider ?? YahooFinanceProvider()

        let actionDefs: [SkillActionDefinition] = [
            SkillActionDefinition(
                id: "finance.quote",
                name: "Stock Quote",
                description: "Get the current price, daily change, volume, and market cap for a stock ticker symbol.",
                inputSchemaSummary: "symbol (required, e.g. AAPL, TSLA, MSFT).",
                outputSchemaSummary: "price, change, changePercent, volume, marketCap, currency, marketState.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.exploratoryResearch],
                routingPriority: 35,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "symbol": AtlasToolInputProperty(
                            type: "string",
                            description: "Stock ticker symbol, e.g. AAPL, TSLA, NVDA."
                        )
                    ],
                    required: ["symbol"]
                )
            ),
            SkillActionDefinition(
                id: "finance.fundamentals",
                name: "Stock Fundamentals",
                description: "Get key financial fundamentals for a stock: P/E ratio, EPS, 52-week range, dividend yield, beta, sector, and company description.",
                inputSchemaSummary: "symbol (required).",
                outputSchemaSummary: "peRatio, eps, week52High, week52Low, dividendYield, beta, sector, industry, description.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.exploratoryResearch, .docsResearch],
                routingPriority: 30,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "symbol": AtlasToolInputProperty(
                            type: "string",
                            description: "Stock ticker symbol."
                        )
                    ],
                    required: ["symbol"]
                )
            ),
            SkillActionDefinition(
                id: "finance.history",
                name: "Stock Price History",
                description: "Get historical daily OHLCV (open, high, low, close, volume) price data for a stock over a given range.",
                inputSchemaSummary: "symbol (required), range optional: 7d, 1mo, 3mo, 1y (default 1mo).",
                outputSchemaSummary: "Array of {date, open, high, low, close, volume} entries.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.exploratoryResearch, .docsResearch],
                routingPriority: 25,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "symbol": AtlasToolInputProperty(
                            type: "string",
                            description: "Stock ticker symbol."
                        ),
                        "range": AtlasToolInputProperty(
                            type: "string",
                            description: "Time range: '7d', '1mo', '3mo', or '1y'. Defaults to '1mo'."
                        )
                    ],
                    required: ["symbol"]
                )
            )
        ]
        self.actions = actionDefs

        self.manifest = SkillManifest(
            id: "finance",
            name: "Finance",
            version: "1.0.0",
            description: "Live stock quotes, fundamentals, and price history from financial data providers.",
            category: .research,
            lifecycleState: .installed,
            capabilities: [.publicWebFetch],
            requiredPermissions: [.publicWebRead],
            riskLevel: .low,
            trustProfile: .exactStructured,
            freshnessType: .live,
            preferredQueryTypes: [.exploratoryResearch, .docsResearch],
            routingPriority: 40,
            canAnswerStructuredLiveData: true,
            allowedDomains: [
                "query1.finance.yahoo.com",
                "query2.finance.yahoo.com",
                "finnhub.io",
                "www.alphavantage.co"
            ],
            restrictionsSummary: [
                "GET only — no writes",
                "Providers: Yahoo Finance (default), Finnhub, Alpha Vantage",
                "Real-time and historical stock data"
            ],
            supportsReadOnlyMode: true,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["finance", "stocks", "quotes", "markets"],
            intent: .liveStructuredData,
            triggers: [
                .init("stock price", queryType: .exploratoryResearch),
                .init("stock quote", queryType: .exploratoryResearch),
                .init("share price", queryType: .exploratoryResearch),
                .init("market cap", queryType: .exploratoryResearch),
                .init("stock history", queryType: .exploratoryResearch),
                .init("price history", queryType: .exploratoryResearch),
                .init("pe ratio", queryType: .exploratoryResearch),
                .init("earnings per share", queryType: .exploratoryResearch),
                .init("52 week", queryType: .exploratoryResearch),
                .init("dividend yield", queryType: .exploratoryResearch),
                .init("beta", queryType: .exploratoryResearch),
                .init("fundamentals", queryType: .exploratoryResearch),
                .init("stock data", queryType: .exploratoryResearch),
                .init("how is", queryType: .exploratoryResearch),
                .init("ticker", queryType: .exploratoryResearch)
            ]
        )
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "finance.quote":        return try await executeQuote(input: input, context: context)
        case "finance.fundamentals": return try await executeFundamentals(input: input, context: context)
        case "finance.history":      return try await executeHistory(input: input, context: context)
        default:
            throw AtlasToolError.invalidInput("Action '\(actionID)' not supported by FinanceSkill.")
        }
    }

    // MARK: - Action implementations

    private func executeQuote(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        struct In: Decodable { let symbol: String }
        let symbol = try input.decode(In.self).symbol.uppercased()
        context.logger.info("finance.quote", metadata: [
            "symbol": "\(symbol)",
            "provider": "\(provider.providerName)"
        ])
        let q = try await provider.quote(symbol: symbol)
        let sign = q.change >= 0 ? "+" : ""
        var output: [String: Any] = [
            "symbol": q.symbol,
            "name": q.name,
            "price": q.price,
            "change": q.change,
            "changePercent": q.changePercent,
            "volume": q.volume,
            "currency": q.currency,
            "marketState": q.marketState,
            "provider": provider.providerName
        ]
        if let mc = q.marketCap { output["marketCap"] = mc }
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "finance.quote",
            output: financeEncodeAny(output),
            summary: "\(q.symbol) (\(q.name)): \(q.currency) \(String(format: "%.2f", q.price)) \(sign)\(String(format: "%.2f", q.change)) (\(sign)\(String(format: "%.2f", q.changePercent))%) via \(provider.providerName).",
            metadata: ["symbol": q.symbol, "provider": provider.providerName]
        )
    }

    private func executeFundamentals(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        struct In: Decodable { let symbol: String }
        let symbol = try input.decode(In.self).symbol.uppercased()
        context.logger.info("finance.fundamentals", metadata: ["symbol": "\(symbol)"])
        let f = try await provider.fundamentals(symbol: symbol)
        var output: [String: Any] = [
            "symbol": f.symbol,
            "name": f.name,
            "provider": provider.providerName
        ]
        if let v = f.peRatio { output["peRatio"]       = v }
        if let v = f.eps { output["eps"]           = v }
        if let v = f.week52High { output["week52High"]    = v }
        if let v = f.week52Low { output["week52Low"]     = v }
        if let v = f.dividendYield { output["dividendYield"] = v }
        if let v = f.beta { output["beta"]          = v }
        if let v = f.sector { output["sector"]        = v }
        if let v = f.industry { output["industry"]      = v }
        if let v = f.description { output["description"]   = String(v.prefix(600)) }
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "finance.fundamentals",
            output: financeEncodeAny(output),
            summary: "Fundamentals for \(f.symbol): P/E \(f.peRatio.map { String(format: "%.1f", $0) } ?? "n/a"), EPS \(f.eps.map { String(format: "%.2f", $0) } ?? "n/a"), 52w \(f.week52Low.map { String(format: "%.2f", $0) } ?? "?")–\(f.week52High.map { String(format: "%.2f", $0) } ?? "?").",
            metadata: ["symbol": f.symbol, "provider": provider.providerName]
        )
    }

    private func executeHistory(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        struct In: Decodable { let symbol: String; let range: String? }
        let payload = try input.decode(In.self)
        let symbol  = payload.symbol.uppercased()
        let range   = FinanceRange(rawValue: payload.range ?? "1mo") ?? .month1
        context.logger.info("finance.history", metadata: [
            "symbol": "\(symbol)",
            "range": "\(range.rawValue)"
        ])
        let candles = try await provider.history(symbol: symbol, range: range)
        let fmt = DateFormatter()
        fmt.dateFormat = "yyyy-MM-dd"
        let rows: [[String: Any]] = candles.map { c in
            ["date": fmt.string(from: c.timestamp), "open": c.open, "high": c.high,
             "low": c.low, "close": c.close, "volume": c.volume]
        }
        let output: [String: Any] = [
            "symbol": symbol,
            "range": range.rawValue,
            "count": rows.count,
            "candles": rows,
            "provider": provider.providerName
        ]
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "finance.history",
            output: financeEncodeAny(output),
            summary: "\(rows.count) daily candles for \(symbol) over \(range.rawValue) via \(provider.providerName).",
            metadata: ["symbol": symbol, "range": range.rawValue, "count": "\(rows.count)"]
        )
    }

    // MARK: - Helpers

    private func financeEncodeAny(_ dict: [String: Any]) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: dict, options: [.prettyPrinted, .sortedKeys]),
              let string = String(data: data, encoding: .utf8) else {
            return "{}"
        }
        return string
    }
}
