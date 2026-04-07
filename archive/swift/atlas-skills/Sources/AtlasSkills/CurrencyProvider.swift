import Foundation
import AtlasShared

public protocol CurrencyProviding: Sendable {
    var providerName: String { get }
    func validateProvider() -> CurrencyProviderValidation
    func convert(amount: Double, from fromCurrency: String, to toCurrency: String) async throws -> CurrencyConversionQuote
}

public actor OpenExchangeRateCurrencyProvider: CurrencyProviding {
    public nonisolated let providerName = "open.er-api.com"

    private let session: URLSession
    private let baseURL: URL
    private let maxResponseBytes: Int

    public init(
        timeoutSeconds: TimeInterval = 10,
        maxResponseBytes: Int = 200_000,
        baseURL: URL = URL(string: "https://open.er-api.com/v6/latest/")!
    ) {
        self.baseURL = baseURL
        self.maxResponseBytes = max(64_000, maxResponseBytes)

        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = timeoutSeconds
        configuration.timeoutIntervalForResource = timeoutSeconds
        configuration.waitsForConnectivity = false
        configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.urlCache = nil
        configuration.httpAdditionalHeaders = [
            "Accept": "application/json",
            "User-Agent": "AtlasInfo/1.0"
        ]

        self.session = URLSession(configuration: configuration)
    }

    public nonisolated func validateProvider() -> CurrencyProviderValidation {
        CurrencyProviderValidation(
            isAvailable: true,
            summary: "Live exchange rates are fetched on demand from open.er-api.com in read-only mode."
        )
    }

    public func convert(amount: Double, from fromCurrency: String, to toCurrency: String) async throws -> CurrencyConversionQuote {
        guard amount.isFinite else {
            throw InfoError.invalidAmount(amount)
        }

        let fromCode = fromCurrency.uppercased()
        let toCode = toCurrency.uppercased()
        let url = baseURL.appendingPathComponent(fromCode)

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw InfoError.providerFailure("Atlas did not receive a valid exchange-rate response.")
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            throw InfoError.conversionUnavailable("The exchange-rate provider returned status code \(httpResponse.statusCode).")
        }

        guard data.count <= maxResponseBytes else {
            throw InfoError.providerFailure("The exchange-rate response exceeded the safe size limit.")
        }

        let payload: OpenExchangeRateResponse
        do {
            payload = try AtlasJSON.decoder.decode(OpenExchangeRateResponse.self, from: data)
        } catch {
            throw InfoError.providerFailure("Atlas could not decode the exchange-rate response.")
        }

        guard payload.result.lowercased() == "success" else {
            throw InfoError.conversionUnavailable("The exchange-rate provider could not complete this conversion right now.")
        }

        guard let exchangeRate = payload.rates[toCode] else {
            throw InfoError.invalidCurrencyCode(toCode)
        }

        let convertedAmount = amount * exchangeRate
        let timestamp = payload.timeLastUpdateUnix.map { Date(timeIntervalSince1970: TimeInterval($0)) } ?? .now

        return CurrencyConversionQuote(
            originalAmount: amount,
            convertedAmount: convertedAmount,
            exchangeRate: exchangeRate,
            timestamp: timestamp
        )
    }
}

private struct OpenExchangeRateResponse: Decodable {
    let result: String
    let timeLastUpdateUnix: Int?
    let rates: [String: Double]

    enum CodingKeys: String, CodingKey {
        case result
        case timeLastUpdateUnix = "time_last_update_unix"
        case rates
    }
}
