import Foundation

// MARK: - Shared Models

public struct FinanceQuote: Sendable {
    public let symbol: String
    public let name: String
    public let price: Double
    public let change: Double
    public let changePercent: Double
    public let volume: Int64
    public let marketCap: Int64?
    public let currency: String
    public let marketState: String

    public init(
        symbol: String, name: String, price: Double, change: Double,
        changePercent: Double, volume: Int64, marketCap: Int64?,
        currency: String, marketState: String
    ) {
        self.symbol = symbol; self.name = name; self.price = price
        self.change = change; self.changePercent = changePercent
        self.volume = volume; self.marketCap = marketCap
        self.currency = currency; self.marketState = marketState
    }
}

public struct FinanceFundamentals: Sendable {
    public let symbol: String
    public let name: String
    public let peRatio: Double?
    public let eps: Double?
    public let week52High: Double?
    public let week52Low: Double?
    public let dividendYield: Double?
    public let beta: Double?
    public let description: String?
    public let sector: String?
    public let industry: String?

    public init(
        symbol: String, name: String, peRatio: Double?, eps: Double?,
        week52High: Double?, week52Low: Double?, dividendYield: Double?,
        beta: Double?, description: String?, sector: String?, industry: String?
    ) {
        self.symbol = symbol; self.name = name
        self.peRatio = peRatio; self.eps = eps
        self.week52High = week52High; self.week52Low = week52Low
        self.dividendYield = dividendYield; self.beta = beta
        self.description = description; self.sector = sector; self.industry = industry
    }
}

public struct FinanceOHLCV: Sendable {
    public let timestamp: Date
    public let open: Double
    public let high: Double
    public let low: Double
    public let close: Double
    public let volume: Int64

    public init(timestamp: Date, open: Double, high: Double, low: Double, close: Double, volume: Int64) {
        self.timestamp = timestamp; self.open = open; self.high = high
        self.low = low; self.close = close; self.volume = volume
    }
}

public enum FinanceRange: String, Sendable {
    case week7  = "7d"
    case month1 = "1mo"
    case month3 = "3mo"
    case year1  = "1y"

    var yahooRange: String { rawValue }

    var yahooInterval: String {
        switch self {
        case .week7:  return "1d"
        case .month1: return "1d"
        case .month3: return "1d"
        case .year1:  return "1wk"
        }
    }
}

// MARK: - Protocol

public protocol FinanceProvider: Sendable {
    var providerName: String { get }
    func quote(symbol: String) async throws -> FinanceQuote
    func fundamentals(symbol: String) async throws -> FinanceFundamentals
    func history(symbol: String, range: FinanceRange) async throws -> [FinanceOHLCV]
}

public enum FinanceProviderError: Error, LocalizedError {
    case invalidSymbol(String)
    case networkError(String)
    case parseError(String)
    case rateLimited
    case notFound(String)

    public var errorDescription: String? {
        switch self {
        case .invalidSymbol(let s): return "'\(s)' is not a recognised ticker symbol."
        case .networkError(let m):  return "Network error: \(m)"
        case .parseError(let m):    return "Could not parse response: \(m)"
        case .rateLimited:          return "Rate limit reached. Please try again in a moment."
        case .notFound(let s):      return "No data found for '\(s)'."
        }
    }
}

// MARK: - YahooFinanceProvider

public struct YahooFinanceProvider: FinanceProvider {
    public let providerName = "Yahoo Finance"
    private let session: URLSession

    public init() {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 8
        self.session = URLSession(configuration: config)
    }

    public func quote(symbol: String) async throws -> FinanceQuote {
        let url = try yahooChartURL(symbol: symbol, range: "1d", interval: "1d")
        let json = try await fetch(url: url)
        return try parseQuote(json: json, symbol: symbol)
    }

    public func fundamentals(symbol: String) async throws -> FinanceFundamentals {
        guard let url = URL(string: "https://query1.finance.yahoo.com/v10/finance/quoteSummary/\(symbol)?modules=summaryDetail,defaultKeyStatistics,assetProfile") else {
            throw FinanceProviderError.invalidSymbol(symbol)
        }
        var req = URLRequest(url: url)
        req.setValue("Mozilla/5.0 (compatible; Atlas/1.0)", forHTTPHeaderField: "User-Agent")
        guard let (data, resp) = try? await session.data(for: req),
              (resp as? HTTPURLResponse)?.statusCode == 200,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw FinanceProviderError.networkError("quoteSummary failed for \(symbol)")
        }
        return try parseFundamentals(json: json, symbol: symbol)
    }

    public func history(symbol: String, range: FinanceRange) async throws -> [FinanceOHLCV] {
        let url = try yahooChartURL(symbol: symbol, range: range.yahooRange, interval: range.yahooInterval)
        let json = try await fetch(url: url)
        return try parseHistory(json: json)
    }

    // MARK: Helpers

    private func yahooChartURL(symbol: String, range: String, interval: String) throws -> URL {
        guard let url = URL(string: "https://query1.finance.yahoo.com/v8/finance/chart/\(symbol)?interval=\(interval)&range=\(range)") else {
            throw FinanceProviderError.invalidSymbol(symbol)
        }
        return url
    }

    private func fetch(url: URL) async throws -> [String: Any] {
        var req = URLRequest(url: url)
        req.setValue("Mozilla/5.0 (compatible; Atlas/1.0)", forHTTPHeaderField: "User-Agent")
        guard let (data, resp) = try? await session.data(for: req),
              (resp as? HTTPURLResponse)?.statusCode == 200 else {
            throw FinanceProviderError.networkError("Request failed for \(url)")
        }
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw FinanceProviderError.parseError("Invalid JSON from Yahoo Finance")
        }
        if let chart = json["chart"] as? [String: Any],
           let error = chart["error"] as? [String: Any],
           let code = error["code"] as? String, code == "Not Found" {
            throw FinanceProviderError.notFound(url.absoluteString)
        }
        return json
    }

    private func parseQuote(json: [String: Any], symbol: String) throws -> FinanceQuote {
        guard let chart = json["chart"] as? [String: Any],
              let result = (chart["result"] as? [[String: Any]])?.first,
              let meta = result["meta"] as? [String: Any] else {
            throw FinanceProviderError.parseError("Missing chart.result[0].meta")
        }
        let price     = meta["regularMarketPrice"] as? Double ?? 0
        let prevClose = meta["chartPreviousClose"] as? Double ?? meta["previousClose"] as? Double ?? price
        let change    = price - prevClose
        let changePct = prevClose > 0 ? (change / prevClose) * 100 : 0
        let volume: Int64 = meta["regularMarketVolume"] as? Int64
                         ?? (meta["regularMarketVolume"] as? Int).map(Int64.init) ?? 0
        let marketCap: Int64? = meta["marketCap"] as? Int64
                             ?? (meta["marketCap"] as? Double).map(Int64.init)
        return FinanceQuote(
            symbol: (meta["symbol"] as? String ?? symbol).uppercased(),
            name: meta["longName"] as? String ?? meta["shortName"] as? String ?? symbol,
            price: price,
            change: change,
            changePercent: changePct,
            volume: volume,
            marketCap: marketCap,
            currency: meta["currency"] as? String ?? "USD",
            marketState: meta["marketState"] as? String ?? "UNKNOWN"
        )
    }

    private func parseFundamentals(json: [String: Any], symbol: String) throws -> FinanceFundamentals {
        let result  = (json["quoteSummary"] as? [String: Any])?["result"] as? [[String: Any]]
        let summary = result?.first
        let detail  = summary?["summaryDetail"] as? [String: Any]
        let stats   = summary?["defaultKeyStatistics"] as? [String: Any]
        let profile = summary?["assetProfile"] as? [String: Any]

        func raw(_ d: [String: Any]?, _ key: String) -> Double? {
            if let v = d?[key] as? Double { return v }
            if let sub = d?[key] as? [String: Any] { return sub["raw"] as? Double }
            return nil
        }

        return FinanceFundamentals(
            symbol: symbol.uppercased(),
            name: profile?["longName"] as? String ?? symbol,
            peRatio: raw(detail, "trailingPE"),
            eps: raw(stats, "trailingEps"),
            week52High: raw(detail, "fiftyTwoWeekHigh"),
            week52Low: raw(detail, "fiftyTwoWeekLow"),
            dividendYield: raw(detail, "dividendYield").map { $0 * 100 },
            beta: raw(detail, "beta"),
            description: profile?["longBusinessSummary"] as? String,
            sector: profile?["sector"] as? String,
            industry: profile?["industry"] as? String
        )
    }

    private func parseHistory(json: [String: Any]) throws -> [FinanceOHLCV] {
        guard let chart = json["chart"] as? [String: Any],
              let result = (chart["result"] as? [[String: Any]])?.first,
              let timestamps = result["timestamp"] as? [Double],
              let indicators = result["indicators"] as? [String: Any],
              let quotes = (indicators["quote"] as? [[String: Any]])?.first else {
            throw FinanceProviderError.parseError("Missing OHLCV data in chart response")
        }
        let opens: [Double?] = quotes["open"]   as? [Double?] ?? []
        let highs: [Double?] = quotes["high"]   as? [Double?] ?? []
        let lows: [Double?] = quotes["low"]    as? [Double?] ?? []
        let closes: [Double?] = quotes["close"]  as? [Double?] ?? []
        let volumes: [Int64?]  = quotes["volume"] as? [Int64?]  ?? []

        return zip(timestamps, (0..<timestamps.count)).compactMap { ts, i in
            guard let o = opens[safe: i] ?? nil,
                  let h = highs[safe: i] ?? nil,
                  let l = lows[safe: i] ?? nil,
                  let c = closes[safe: i] ?? nil else { return nil }
            let v: Int64 = (volumes[safe: i] ?? nil) ?? 0
            return FinanceOHLCV(
                timestamp: Date(timeIntervalSince1970: ts),
                open: o, high: h, low: l, close: c, volume: v
            )
        }
    }
}

private extension Array {
    subscript(safe index: Int) -> Element? {
        indices.contains(index) ? self[index] : nil
    }
}

// MARK: - FinnhubProvider

public struct FinnhubProvider: FinanceProvider {
    public let providerName = "Finnhub"
    private let apiKey: String
    private let session: URLSession

    public init(apiKey: String) {
        self.apiKey = apiKey
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 8
        self.session = URLSession(configuration: config)
    }

    private func fetch<T: Decodable>(_ url: URL, as type: T.Type) async throws -> T {
        var req = URLRequest(url: url)
        req.setValue(apiKey, forHTTPHeaderField: "X-Finnhub-Token")
        guard let (data, resp) = try? await session.data(for: req),
              (resp as? HTTPURLResponse)?.statusCode == 200 else {
            throw FinanceProviderError.networkError("Finnhub request failed")
        }
        guard let result = try? JSONDecoder().decode(T.self, from: data) else {
            throw FinanceProviderError.parseError("Could not decode Finnhub response")
        }
        return result
    }

    public func quote(symbol: String) async throws -> FinanceQuote {
        struct FinnhubQuote: Decodable { let c: Double; let d: Double; let dp: Double; let v: Double }
        struct FinnhubProfile: Decodable { let name: String?; let currency: String?; let marketCapitalization: Double? }
        guard let quoteURL   = URL(string: "https://finnhub.io/api/v1/quote?symbol=\(symbol)"),
              let profileURL = URL(string: "https://finnhub.io/api/v1/stock/profile2?symbol=\(symbol)") else {
            throw FinanceProviderError.invalidSymbol(symbol)
        }
        async let q = fetch(quoteURL, as: FinnhubQuote.self)
        async let p = fetch(profileURL, as: FinnhubProfile.self)
        let (quote, profile) = try await (q, p)
        if quote.c == 0 { throw FinanceProviderError.notFound(symbol) }
        return FinanceQuote(
            symbol: symbol.uppercased(),
            name: profile.name ?? symbol,
            price: quote.c,
            change: quote.d,
            changePercent: quote.dp,
            volume: Int64(quote.v),
            marketCap: profile.marketCapitalization.map { Int64($0 * 1_000_000) },
            currency: profile.currency ?? "USD",
            marketState: "UNKNOWN"
        )
    }

    public func fundamentals(symbol: String) async throws -> FinanceFundamentals {
        struct FinnhubMetrics: Decodable {
            struct Metric: Decodable {
                let peNormalizedAnnual: Double?
                let epsNormalizedAnnual: Double?
                let week52High: Double?
                let week52Low: Double?
                let dividendYieldIndicatedAnnual: Double?
                let beta: Double?
                enum CodingKeys: String, CodingKey {
                    case peNormalizedAnnual, epsNormalizedAnnual
                    case week52High = "52WeekHigh"
                    case week52Low  = "52WeekLow"
                    case dividendYieldIndicatedAnnual, beta
                }
            }
            let metric: Metric
        }
        struct FinnhubProfile: Decodable { let name: String?; let finnhubIndustry: String?; let description: String? }
        guard let metricsURL = URL(string: "https://finnhub.io/api/v1/stock/metric?symbol=\(symbol)&metric=all"),
              let profileURL = URL(string: "https://finnhub.io/api/v1/stock/profile2?symbol=\(symbol)") else {
            throw FinanceProviderError.invalidSymbol(symbol)
        }
        async let m = fetch(metricsURL, as: FinnhubMetrics.self)
        async let p = fetch(profileURL, as: FinnhubProfile.self)
        let (metrics, profile) = try await (m, p)
        return FinanceFundamentals(
            symbol: symbol.uppercased(),
            name: profile.name ?? symbol,
            peRatio: metrics.metric.peNormalizedAnnual,
            eps: metrics.metric.epsNormalizedAnnual,
            week52High: metrics.metric.week52High,
            week52Low: metrics.metric.week52Low,
            dividendYield: metrics.metric.dividendYieldIndicatedAnnual,
            beta: metrics.metric.beta,
            description: profile.description,
            sector: nil,
            industry: profile.finnhubIndustry
        )
    }

    public func history(symbol: String, range: FinanceRange) async throws -> [FinanceOHLCV] {
        struct FinnhubCandles: Decodable { let c: [Double]; let h: [Double]; let l: [Double]; let o: [Double]; let t: [Int64]; let v: [Double]; let s: String }
        let to: Int64 = Int64(Date().timeIntervalSince1970)
        let days: Int64
        switch range {
        case .week7:  days = 7
        case .month1: days = 31
        case .month3: days = 91
        case .year1:  days = 365
        }
        let from = to - days * 86400
        guard let url = URL(string: "https://finnhub.io/api/v1/stock/candle?symbol=\(symbol)&resolution=D&from=\(from)&to=\(to)") else {
            throw FinanceProviderError.invalidSymbol(symbol)
        }
        let candles = try await fetch(url, as: FinnhubCandles.self)
        guard candles.s == "ok" else { throw FinanceProviderError.notFound(symbol) }
        return zip(candles.t, (0..<candles.t.count)).map { ts, i in
            FinanceOHLCV(
                timestamp: Date(timeIntervalSince1970: Double(ts)),
                open: candles.o[i], high: candles.h[i], low: candles.l[i], close: candles.c[i],
                volume: Int64(candles.v[i])
            )
        }
    }
}

// MARK: - AlphaVantageProvider

public struct AlphaVantageProvider: FinanceProvider {
    public let providerName = "Alpha Vantage"
    private let apiKey: String
    private let session: URLSession

    public init(apiKey: String) {
        self.apiKey = apiKey
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 10
        self.session = URLSession(configuration: config)
    }

    private func fetchJSON(_ url: URL) async throws -> [String: Any] {
        var req = URLRequest(url: url)
        req.setValue("Mozilla/5.0 (compatible; Atlas/1.0)", forHTTPHeaderField: "User-Agent")
        guard let (data, resp) = try? await session.data(for: req),
              (resp as? HTTPURLResponse)?.statusCode == 200,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw FinanceProviderError.networkError("Alpha Vantage request failed")
        }
        if json["Note"] != nil || json["Information"] != nil { throw FinanceProviderError.rateLimited }
        return json
    }

    public func quote(symbol: String) async throws -> FinanceQuote {
        guard let url = URL(string: "https://www.alphavantage.co/query?function=GLOBAL_QUOTE&symbol=\(symbol)&apikey=\(apiKey)") else {
            throw FinanceProviderError.invalidSymbol(symbol)
        }
        let json = try await fetchJSON(url)
        guard let q = json["Global Quote"] as? [String: String],
              let priceStr = q["05. price"], let price = Double(priceStr) else {
            throw FinanceProviderError.notFound(symbol)
        }
        let change    = Double(q["09. change"] ?? "") ?? 0
        let changePct = Double((q["10. change percent"] ?? "").replacingOccurrences(of: "%", with: "")) ?? 0
        let volume    = Int64(q["06. volume"] ?? "") ?? 0
        return FinanceQuote(
            symbol: symbol.uppercased(),
            name: symbol.uppercased(),
            price: price,
            change: change,
            changePercent: changePct,
            volume: volume,
            marketCap: nil,
            currency: "USD",
            marketState: "UNKNOWN"
        )
    }

    public func fundamentals(symbol: String) async throws -> FinanceFundamentals {
        guard let url = URL(string: "https://www.alphavantage.co/query?function=OVERVIEW&symbol=\(symbol)&apikey=\(apiKey)") else {
            throw FinanceProviderError.invalidSymbol(symbol)
        }
        let json = try await fetchJSON(url)
        func d(_ key: String) -> Double? { (json[key] as? String).flatMap(Double.init) }
        return FinanceFundamentals(
            symbol: symbol.uppercased(),
            name: json["Name"] as? String ?? symbol,
            peRatio: d("PERatio"),
            eps: d("EPS"),
            week52High: d("52WeekHigh"),
            week52Low: d("52WeekLow"),
            dividendYield: d("DividendYield").map { $0 * 100 },
            beta: d("Beta"),
            description: json["Description"] as? String,
            sector: json["Sector"] as? String,
            industry: json["Industry"] as? String
        )
    }

    public func history(symbol: String, range: FinanceRange) async throws -> [FinanceOHLCV] {
        let outputSize = range == .year1 ? "full" : "compact"
        guard let url = URL(string: "https://www.alphavantage.co/query?function=TIME_SERIES_DAILY&symbol=\(symbol)&outputsize=\(outputSize)&apikey=\(apiKey)") else {
            throw FinanceProviderError.invalidSymbol(symbol)
        }
        let json = try await fetchJSON(url)
        guard let timeSeries = json["Time Series (Daily)"] as? [String: [String: String]] else {
            throw FinanceProviderError.parseError("Missing time series data")
        }
        let cutoff: Date
        switch range {
        case .week7:  cutoff = Date().addingTimeInterval(-7  * 86400)
        case .month1: cutoff = Date().addingTimeInterval(-31 * 86400)
        case .month3: cutoff = Date().addingTimeInterval(-91 * 86400)
        case .year1:  cutoff = Date().addingTimeInterval(-365 * 86400)
        }
        let fmt = DateFormatter()
        fmt.dateFormat = "yyyy-MM-dd"
        return timeSeries.compactMap { dateStr, vals -> FinanceOHLCV? in
            guard let date = fmt.date(from: dateStr), date >= cutoff,
                  let o = Double(vals["1. open"]   ?? ""),
                  let h = Double(vals["2. high"]   ?? ""),
                  let l = Double(vals["3. low"]    ?? ""),
                  let c = Double(vals["4. close"]  ?? "") else { return nil }
            return FinanceOHLCV(
                timestamp: date, open: o, high: h, low: l, close: c,
                volume: Int64(vals["5. volume"] ?? "") ?? 0
            )
        }.sorted { $0.timestamp < $1.timestamp }
    }
}
