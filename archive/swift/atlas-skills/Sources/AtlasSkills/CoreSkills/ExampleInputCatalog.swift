import Foundation
import AtlasShared

// MARK: - ExampleInputCatalog

/// Resolves safe example inputs for an API validation request.
///
/// Resolution priority:
/// 1. If `request.exampleInputs` is non-empty → use first entry, source = `.provided`
/// 2. Match provider name / base URL against internal catalog → source = `.catalog`
/// 3. Fall back to constrained auto-generation from `requiredParams` → source = `.generated`
///
/// The catalog only contains safe, public, read-only values. No personal data.
/// All generated fallbacks are benign strings safe to send to any API.
public struct ExampleInputCatalog: Sendable {

    public static let `default` = ExampleInputCatalog()

    public init() {}

    // MARK: - Resolution

    /// Resolve the best example input for the given validation request.
    public func resolve(for request: APIValidationRequest) -> ExampleInput {
        // Priority 1: caller-provided example
        if let first = request.exampleInputs.first {
            return ExampleInput(name: first.name, inputs: first.inputs, source: .provided)
        }

        // Priority 2: internal catalog match
        let searchKey = "\(request.providerName) \(request.baseURL) \(request.endpoint)".lowercased()
        if let catalogEntry = matchCatalog(searchKey: searchKey) {
            return catalogEntry
        }

        // Priority 3: generated fallback
        return generateFallback(for: request)
    }

    /// Returns a second distinct candidate example that differs from `first`, for use
    /// by the APIValidationService retry loop when the first attempt returns `.needsRevision`.
    ///
    /// Strategy:
    /// - If `first` came from `provided` and more provided inputs exist, use inputs[1].
    /// - If `first` came from `catalog`, fall back to generated alternate values.
    /// - If `first` came from `generated`, produce alternate generated values.
    /// - Returns nil if no meaningful alternate exists (e.g. no required params at all).
    public func resolveAlternate(for request: APIValidationRequest, first: ExampleInput) -> ExampleInput? {
        // Provided[1] if available
        if first.source == .provided, request.exampleInputs.count > 1 {
            let second = request.exampleInputs[1]
            return ExampleInput(name: second.name, inputs: second.inputs, source: .provided)
        }
        // Catalog → generate alternate
        if first.source == .catalog {
            return generateAlternateFallback(for: request)
        }
        // Generated → generate alternate with different values (different city, id, etc.)
        if first.source == .generated {
            return generateAlternateFallback(for: request)
        }
        return nil
    }

    /// Returns a safe alternate value for a parameter — differs from `defaultValue(for:)`.
    /// Used to produce the second candidate in the retry loop.
    public func alternateValue(for paramName: String) -> String {
        let lower = paramName.lowercased()
        if lower.contains("id") { return "42" }
        if lower.contains("query") || lower == "q" { return "hello" }
        if lower.contains("location") || lower.contains("city") { return "Paris" }
        if lower.contains("country") { return "GB" }
        if lower.contains("user") { return "admin" }
        if lower.contains("lat") { return "51.5074" }
        if lower.contains("lon") || lower.contains("lng") { return "-0.1278" }
        if lower.contains("date") { return "2024-06-15" }
        return "sample"
    }

    // MARK: - Internal Catalog

    private struct CatalogEntry {
        let keywords: [String]
        let inputs: [String: String]
        let name: String
    }

    private static let catalog: [CatalogEntry] = [
        CatalogEntry(
            keywords: ["weather", "open-meteo", "openmeteo"],
            inputs: ["latitude": "40.7128", "longitude": "-74.0060"],
            name: "New York City weather"
        ),
        CatalogEntry(
            keywords: ["ipinfo", "ip-api", "ipgeolocation", "ipapi"],
            inputs: ["ip": "8.8.8.8"],
            name: "Google DNS IP lookup"
        ),
        CatalogEntry(
            keywords: ["country", "restcountries", "countries"],
            inputs: ["name": "France"],
            name: "France country lookup"
        ),
        CatalogEntry(
            keywords: ["github"],
            inputs: ["username": "octocat"],
            name: "GitHub octocat user"
        ),
        CatalogEntry(
            keywords: ["hackernews", "hacker-news", "hn"],
            inputs: ["id": "1"],
            name: "Hacker News item 1"
        ),
        CatalogEntry(
            keywords: ["jsonplaceholder"],
            inputs: ["id": "1"],
            name: "JSON Placeholder item 1"
        ),
        CatalogEntry(
            keywords: ["openweathermap", "openweather"],
            inputs: ["q": "London"],
            name: "London weather"
        ),
        CatalogEntry(
            keywords: ["coindesk", "coingecko", "crypto", "coin"],
            inputs: ["currency": "bitcoin"],
            name: "Bitcoin price"
        ),
        CatalogEntry(
            keywords: ["poke", "pokeapi", "pokemon"],
            inputs: ["name": "pikachu"],
            name: "Pikachu Pokedex entry"
        ),
        CatalogEntry(
            keywords: ["thecocktaildb", "cocktail"],
            inputs: ["i": "11007"],
            name: "Margarita cocktail"
        ),
        CatalogEntry(
            keywords: ["omdb", "movie", "film"],
            inputs: ["t": "Inception"],
            name: "Inception movie"
        ),
        CatalogEntry(
            keywords: ["news", "newsapi"],
            inputs: ["q": "technology"],
            name: "Technology news search"
        )
    ]

    private func matchCatalog(searchKey: String) -> ExampleInput? {
        for entry in Self.catalog {
            if entry.keywords.contains(where: { searchKey.contains($0) }) {
                return ExampleInput(name: entry.name, inputs: entry.inputs, source: .catalog)
            }
        }
        return nil
    }

    // MARK: - Fallback Generation

    private func generateFallback(for request: APIValidationRequest) -> ExampleInput {
        var inputs: [String: String] = [:]
        for param in request.requiredParams {
            inputs[param] = defaultValue(for: param)
        }
        return ExampleInput(name: "auto-generated", inputs: inputs, source: .generated)
    }

    private func generateAlternateFallback(for request: APIValidationRequest) -> ExampleInput? {
        guard !request.requiredParams.isEmpty else { return nil }
        var inputs: [String: String] = [:]
        for param in request.requiredParams {
            inputs[param] = alternateValue(for: param)
        }
        return ExampleInput(name: "auto-generated (alternate)", inputs: inputs, source: .generated)
    }

    /// Returns a safe, benign default string value for a parameter based on its name heuristics.
    func defaultValue(for paramName: String) -> String {
        let lower = paramName.lowercased()
        if lower.contains("id") { return "1" }
        if lower.contains("query") || lower == "q" { return "test" }
        if lower.contains("location") || lower.contains("city") { return "London" }
        if lower.contains("country") { return "US" }
        if lower.contains("user") { return "user" }
        if lower.contains("lat") { return "40.7" }
        if lower.contains("lon") || lower.contains("lng") { return "-74.0" }
        if lower.contains("date") { return "2024-01-01" }
        return "test"
    }
}
