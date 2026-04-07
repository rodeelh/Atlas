import Foundation

public protocol LocaleContextResolving: Sendable {
    func currentTimeZoneResolution() -> InfoTimeZoneResolution
    func currentCountryCode() -> String?
}

public struct LocaleContextResolver: LocaleContextResolving, Sendable {
    public init() {}

    public func currentTimeZoneResolution() -> InfoTimeZoneResolution {
        let timeZone = TimeZone.autoupdatingCurrent
        let locale = Locale.autoupdatingCurrent
        let city = timeZone.identifier
            .split(separator: "/")
            .last
            .map { String($0).replacingOccurrences(of: "_", with: " ") }
        let country = locale.region.map(\.identifier).flatMap { locale.localizedString(forRegionCode: $0) }
        let locationName = [city, country]
            .compactMap { value in
                guard let value, !value.isEmpty else { return nil }
                return value
            }
            .joined(separator: ", ")

        return InfoTimeZoneResolution(
            resolvedLocationName: locationName.isEmpty ? nil : locationName,
            timeZone: timeZone
        )
    }

    public func currentCountryCode() -> String? {
        Locale.autoupdatingCurrent.region.map(\.identifier)?.uppercased()
    }
}
