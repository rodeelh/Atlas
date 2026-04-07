import Foundation

// MARK: - Output Types

/// A point-in-time snapshot of runtime context for internal reasoning.
public struct CoreContextSnapshot: Sendable {
    public let currentDateTime: String
    public let timeZoneIdentifier: String
    public let timeZoneAbbreviation: String
    public let locale: String
    public let platform: String
}

// MARK: - CoreContextService

/// Internal context and environment primitives for CoreSkills, built-in skills, and Forge.
/// Provides time, locale, and runtime environment information.
/// Not part of the user-facing skill registry.
public struct CoreContextService: Sendable {

    public init() {}

    /// Returns the current date and time as an ISO-8601 string with timezone offset.
    public func currentDateTime() -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withFullDate, .withFullTime, .withTimeZone]
        return formatter.string(from: Date())
    }

    /// Returns the current timezone identifier and abbreviation.
    public func timezone() -> (identifier: String, abbreviation: String) {
        let tz = TimeZone.current
        return (tz.identifier, tz.abbreviation() ?? "UTC")
    }

    /// Returns a complete context snapshot for environment-aware operations.
    public func snapshot() -> CoreContextSnapshot {
        let tz = TimeZone.current
        return CoreContextSnapshot(
            currentDateTime: currentDateTime(),
            timeZoneIdentifier: tz.identifier,
            timeZoneAbbreviation: tz.abbreviation() ?? "UTC",
            locale: Locale.current.identifier,
            platform: "macOS"
        )
    }

    /// Returns a human-readable environment description suitable for internal logging.
    public func environmentDescription() -> String {
        let snap = snapshot()
        return """
        Platform: \(snap.platform)
        Time: \(snap.currentDateTime)
        Timezone: \(snap.timeZoneIdentifier) (\(snap.timeZoneAbbreviation))
        Locale: \(snap.locale)
        """
    }
}
