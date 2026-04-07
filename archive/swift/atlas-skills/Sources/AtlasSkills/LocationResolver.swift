import Foundation

public protocol LocationResolving: Sendable {
    func resolve(_ query: String) throws -> InfoResolvedLocation
}

public struct LocationResolver: LocationResolving, Sendable {
    private struct CatalogEntry: Sendable {
        let canonicalName: String
        let aliases: [String]
        let countryCode: String?
        let timezoneID: String?
    }

    private static let catalog: [CatalogEntry] = [
        CatalogEntry(canonicalName: "Orlando, Florida, United States", aliases: ["orlando"], countryCode: "US", timezoneID: "America/New_York"),
        CatalogEntry(canonicalName: "New York, New York, United States", aliases: ["new york", "nyc"], countryCode: "US", timezoneID: "America/New_York"),
        CatalogEntry(canonicalName: "Los Angeles, California, United States", aliases: ["los angeles", "la"], countryCode: "US", timezoneID: "America/Los_Angeles"),
        CatalogEntry(canonicalName: "Chicago, Illinois, United States", aliases: ["chicago"], countryCode: "US", timezoneID: "America/Chicago"),
        CatalogEntry(canonicalName: "London, United Kingdom", aliases: ["london", "uk", "united kingdom", "great britain", "britain"], countryCode: "GB", timezoneID: "Europe/London"),
        CatalogEntry(canonicalName: "Paris, France", aliases: ["paris", "france"], countryCode: "FR", timezoneID: "Europe/Paris"),
        CatalogEntry(canonicalName: "Berlin, Germany", aliases: ["berlin", "germany"], countryCode: "DE", timezoneID: "Europe/Berlin"),
        CatalogEntry(canonicalName: "Tokyo, Japan", aliases: ["tokyo", "japan"], countryCode: "JP", timezoneID: "Asia/Tokyo"),
        CatalogEntry(canonicalName: "Dubai, United Arab Emirates", aliases: ["dubai", "uae", "united arab emirates"], countryCode: "AE", timezoneID: "Asia/Dubai"),
        CatalogEntry(canonicalName: "Singapore", aliases: ["singapore"], countryCode: "SG", timezoneID: "Asia/Singapore"),
        CatalogEntry(canonicalName: "Seoul, South Korea", aliases: ["seoul", "south korea", "korea"], countryCode: "KR", timezoneID: "Asia/Seoul"),
        CatalogEntry(canonicalName: "Mumbai, India", aliases: ["mumbai", "bombay", "india"], countryCode: "IN", timezoneID: "Asia/Kolkata"),
        CatalogEntry(canonicalName: "Sydney, Australia", aliases: ["sydney"], countryCode: "AU", timezoneID: "Australia/Sydney"),
        CatalogEntry(canonicalName: "Toronto, Canada", aliases: ["toronto"], countryCode: "CA", timezoneID: "America/Toronto")
    ]

    private static let ambiguousCountryCodes: Set<String> = ["US", "CA", "AU", "BR", "RU", "MX"]

    public init() {}

    public func resolve(_ query: String) throws -> InfoResolvedLocation {
        let normalized = normalize(query)
        guard !normalized.isEmpty else {
            throw InfoError.emptyLocationQuery
        }

        if let timeZone = TimeZone(identifier: query) {
            return InfoResolvedLocation(
                resolvedLocationName: prettyName(for: timeZone.identifier),
                countryCode: countryCode(forTimeZoneID: timeZone.identifier),
                timezoneID: timeZone.identifier
            )
        }

        if let exact = Self.catalog.first(where: { $0.aliases.contains(normalized) || normalize($0.canonicalName) == normalized }) {
            return InfoResolvedLocation(
                resolvedLocationName: exact.canonicalName,
                countryCode: exact.countryCode,
                timezoneID: exact.timezoneID
            )
        }

        if let matchedTimeZoneID = TimeZone.knownTimeZoneIdentifiers.first(where: { matches(timeZoneID: $0, normalizedQuery: normalized) }) {
            return InfoResolvedLocation(
                resolvedLocationName: prettyName(for: matchedTimeZoneID),
                countryCode: countryCode(forTimeZoneID: matchedTimeZoneID),
                timezoneID: matchedTimeZoneID
            )
        }

        if let resolvedCountryCode = countryCode(forQuery: normalized) {
            if Self.ambiguousCountryCodes.contains(resolvedCountryCode) {
                return InfoResolvedLocation(
                    resolvedLocationName: Locale.autoupdatingCurrent.localizedString(forRegionCode: resolvedCountryCode) ?? resolvedCountryCode,
                    countryCode: resolvedCountryCode,
                    timezoneID: nil
                )
            }

            let locationName = Locale.autoupdatingCurrent.localizedString(forRegionCode: resolvedCountryCode) ?? resolvedCountryCode
            return InfoResolvedLocation(
                resolvedLocationName: locationName,
                countryCode: resolvedCountryCode,
                timezoneID: primaryTimeZone(forCountryCode: resolvedCountryCode)
            )
        }

        throw InfoError.unresolvedLocation(query)
    }

    private func normalize(_ value: String) -> String {
        value
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased()
            .replacingOccurrences(of: ",", with: " ")
            .replacingOccurrences(of: "_", with: " ")
            .replacingOccurrences(of: "-", with: " ")
            .split(whereSeparator: \.isWhitespace)
            .joined(separator: " ")
    }

    private func matches(timeZoneID: String, normalizedQuery: String) -> Bool {
        let pieces = timeZoneID.lowercased().split(separator: "/").map {
            $0.replacingOccurrences(of: "_", with: " ")
        }
        return pieces.contains(normalizedQuery) || normalize(timeZoneID) == normalizedQuery
    }

    private func prettyName(for timeZoneID: String) -> String {
        let timeZone = TimeZone(identifier: timeZoneID)
        let city = timeZoneID
            .split(separator: "/")
            .last
            .map { String($0).replacingOccurrences(of: "_", with: " ") }
        let country = timeZone.flatMap { countryCode(forTimeZoneID: $0.identifier) }
            .flatMap { Locale.autoupdatingCurrent.localizedString(forRegionCode: $0) }

        return [city, country]
            .compactMap { value in
                guard let value, !value.isEmpty else { return nil }
                return value
            }
            .joined(separator: ", ")
    }

    private func countryCode(forQuery normalizedQuery: String) -> String? {
        let directAliases: [String: String] = [
            "us": "US",
            "usa": "US",
            "united states": "US",
            "uk": "GB",
            "united kingdom": "GB",
            "uae": "AE",
            "united arab emirates": "AE"
        ]
        if let code = directAliases[normalizedQuery] {
            return code
        }

        for region in Locale.Region.isoRegions {
            let code = region.identifier
            let localized = Locale.autoupdatingCurrent.localizedString(forRegionCode: code)?.lowercased()
            if localized == normalizedQuery {
                return code.uppercased()
            }
        }

        return nil
    }

    private func primaryTimeZone(forCountryCode targetCountryCode: String) -> String? {
        if let catalogMatch = Self.catalog.first(where: { $0.countryCode == targetCountryCode && $0.timezoneID != nil }) {
            return catalogMatch.timezoneID
        }

        return TimeZone.knownTimeZoneIdentifiers.first { identifier in
            countryCode(forTimeZoneID: identifier) == targetCountryCode
        }
    }

    private func countryCode(forTimeZoneID identifier: String) -> String? {
        if let catalogMatch = Self.catalog.first(where: { $0.timezoneID == identifier }) {
            return catalogMatch.countryCode
        }

        let city = identifier.split(separator: "/").last.map(String.init)?.replacingOccurrences(of: "_", with: " ")
        guard let city else { return nil }

        if let match = Self.catalog.first(where: { entry in
            entry.canonicalName.lowercased().contains(city.lowercased())
        }) {
            return match.countryCode
        }

        return nil
    }
}
