import Foundation

public protocol TimeInfoProviding: Sendable {
    func validateProvider() -> TimeInfoProviderValidation
    func currentTime(in resolution: InfoTimeZoneResolution) -> InfoCurrentTimeOutput
    func currentDate(in resolution: InfoTimeZoneResolution) -> InfoCurrentDateOutput
    func convertTime(
        sourceTime: String,
        source: InfoTimeZoneResolution,
        destination: InfoTimeZoneResolution
    ) throws -> InfoTimezoneConversionOutput
}

public struct FoundationTimeInfoProvider: TimeInfoProviding, Sendable {
    public init() {}

    public func validateProvider() -> TimeInfoProviderValidation {
        TimeInfoProviderValidation(
            isAvailable: true,
            summary: "Foundation time, calendar, and timezone APIs are available."
        )
    }

    public func currentTime(in resolution: InfoTimeZoneResolution) -> InfoCurrentTimeOutput {
        let now = Date()

        return InfoCurrentTimeOutput(
            resolvedLocationName: resolution.resolvedLocationName,
            timezoneID: resolution.timeZone.identifier,
            utcOffset: utcOffsetString(for: resolution.timeZone, at: now),
            formattedTime: timeFormatter(timeZone: resolution.timeZone).string(from: now),
            isoTimestamp: isoTimestampFormatter(timeZone: resolution.timeZone).string(from: now)
        )
    }

    public func currentDate(in resolution: InfoTimeZoneResolution) -> InfoCurrentDateOutput {
        let now = Date()
        let dateFormatter = DateFormatter()
        dateFormatter.locale = Locale.autoupdatingCurrent
        dateFormatter.timeZone = resolution.timeZone
        dateFormatter.dateStyle = .long
        dateFormatter.timeStyle = .none

        let weekdayFormatter = DateFormatter()
        weekdayFormatter.locale = Locale.autoupdatingCurrent
        weekdayFormatter.timeZone = resolution.timeZone
        weekdayFormatter.dateFormat = "EEEE"

        let isoFormatter = DateFormatter()
        isoFormatter.locale = Locale(identifier: "en_US_POSIX")
        isoFormatter.timeZone = resolution.timeZone
        isoFormatter.dateFormat = "yyyy-MM-dd"

        return InfoCurrentDateOutput(
            resolvedLocationName: resolution.resolvedLocationName,
            timezoneID: resolution.timeZone.identifier,
            formattedDate: dateFormatter.string(from: now),
            weekday: weekdayFormatter.string(from: now),
            isoDate: isoFormatter.string(from: now)
        )
    }

    public func convertTime(
        sourceTime: String,
        source: InfoTimeZoneResolution,
        destination: InfoTimeZoneResolution
    ) throws -> InfoTimezoneConversionOutput {
        let date = try parse(sourceTime: sourceTime, in: source.timeZone)
        let sourceFormatted = timeFormatter(timeZone: source.timeZone).string(from: date)
        let destinationFormatted = timeFormatter(timeZone: destination.timeZone).string(from: date)

        let summary = "\(sourceFormatted) in \(source.timeZone.identifier) is \(destinationFormatted) in \(destination.timeZone.identifier)."

        return InfoTimezoneConversionOutput(
            sourceTimezoneID: source.timeZone.identifier,
            destinationTimezoneID: destination.timeZone.identifier,
            originalTime: sourceFormatted,
            convertedTime: destinationFormatted,
            formattedSummary: summary
        )
    }

    private func parse(sourceTime: String, in timeZone: TimeZone) throws -> Date {
        let trimmed = sourceTime.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw InfoError.invalidTime(sourceTime)
        }

        if let isoDate = ISO8601DateFormatter().date(from: trimmed) {
            return isoDate
        }

        let calendar = Calendar(identifier: .gregorian)
        let now = Date()
        var components = calendar.dateComponents(in: timeZone, from: now)

        let formats = [
            "h:mm a",
            "h a",
            "ha",
            "HH:mm",
            "H:mm",
            "HHmm",
            "yyyy-MM-dd HH:mm",
            "yyyy-MM-dd h:mm a"
        ]

        for format in formats {
            let formatter = DateFormatter()
            formatter.locale = Locale(identifier: "en_US_POSIX")
            formatter.timeZone = timeZone
            formatter.dateFormat = format

            if format.contains("yyyy"), let date = formatter.date(from: trimmed) {
                return date
            }

            if let timeDate = formatter.date(from: trimmed) {
                let timeComponents = calendar.dateComponents(in: timeZone, from: timeDate)
                components.hour = timeComponents.hour
                components.minute = timeComponents.minute
                components.second = 0
                if let resolved = calendar.date(from: components) {
                    return resolved
                }
            }
        }

        throw InfoError.invalidTime(sourceTime)
    }

    private func timeFormatter(timeZone: TimeZone) -> DateFormatter {
        let formatter = DateFormatter()
        formatter.locale = Locale.autoupdatingCurrent
        formatter.timeZone = timeZone
        formatter.timeStyle = .short
        formatter.dateStyle = .none
        return formatter
    }

    private func isoTimestampFormatter(timeZone: TimeZone) -> ISO8601DateFormatter {
        let formatter = ISO8601DateFormatter()
        formatter.timeZone = timeZone
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }

    private func utcOffsetString(for timeZone: TimeZone, at date: Date) -> String {
        let seconds = timeZone.secondsFromGMT(for: date)
        let sign = seconds >= 0 ? "+" : "-"
        let absoluteSeconds = abs(seconds)
        let hours = absoluteSeconds / 3600
        let minutes = (absoluteSeconds % 3600) / 60
        return String(format: "%@%02d:%02d", sign, hours, minutes)
    }
}
