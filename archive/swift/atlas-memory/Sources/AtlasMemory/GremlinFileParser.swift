import Foundation
import AtlasShared

/// Handles parsing and serialisation of GREMLINS.md format.
public struct GremlinFileParser {

    public init() {}

    // MARK: - Parse

    public func parse(markdown: String) -> [GremlinItem] {
        var items: [GremlinItem] = []
        let lines = markdown.components(separatedBy: "\n")
        var currentBlock: [String] = []
        var inGremlin = false

        for line in lines {
            if line.hasPrefix("## ") {
                if inGremlin, let item = parseBlock(currentBlock) {
                    items.append(item)
                }
                currentBlock = [line]
                inGremlin = true
            } else if inGremlin {
                if line == "---" {
                    if let item = parseBlock(currentBlock) {
                        items.append(item)
                    }
                    currentBlock = []
                    inGremlin = false
                } else {
                    currentBlock.append(line)
                }
            }
        }

        if inGremlin, let item = parseBlock(currentBlock) {
            items.append(item)
        }

        return items
    }

    private func parseBlock(_ lines: [String]) -> GremlinItem? {
        guard let headingLine = lines.first, headingLine.hasPrefix("## ") else { return nil }

        let headingRaw = String(headingLine.dropFirst(3)).trimmingCharacters(in: .whitespaces)
        let (name, emoji) = splitNameEmoji(headingRaw)

        var scheduleRaw = ""
        var isEnabled = true
        var sourceType = "manual"
        var createdAt = ""
        var lastModifiedAt: String?
        var telegramChatID: Int64?
        var communicationDestination: CommunicationDestination?
        var workflowID: String?
        var workflowInputValues: [String: String]?
        var gremlinDescription: String?
        var tags: [String] = []
        var maxRetries = 0
        var timeoutSeconds: Int?
        var promptLines: [String] = []
        var pastStructuredFields = false

        for line in lines.dropFirst() {
            if !pastStructuredFields {
                if line.lowercased().hasPrefix("schedule:") {
                    scheduleRaw = String(line.dropFirst("schedule:".count)).trimmingCharacters(in: .whitespaces)
                    continue
                }
                if line.lowercased().hasPrefix("status:") {
                    let val = String(line.dropFirst("status:".count)).trimmingCharacters(in: .whitespaces).lowercased()
                    isEnabled = val == "enabled"
                    continue
                }
                if line.lowercased().hasPrefix("created:") {
                    let raw = String(line.dropFirst("created:".count)).trimmingCharacters(in: .whitespaces)
                    let parts = raw.components(separatedBy: " via ")
                    createdAt = parts.first?.trimmingCharacters(in: .whitespaces) ?? raw
                    if parts.count > 1 {
                        sourceType = parts[1].trimmingCharacters(in: .whitespaces)
                    }
                    continue
                }
                if line.lowercased().hasPrefix("modified:") {
                    lastModifiedAt = String(line.dropFirst("modified:".count)).trimmingCharacters(in: .whitespaces)
                    continue
                }
                if line.lowercased().hasPrefix("description:") {
                    let val = String(line.dropFirst("description:".count)).trimmingCharacters(in: .whitespaces)
                    gremlinDescription = val.isEmpty ? nil : val
                    continue
                }
                if line.lowercased().hasPrefix("tags:") {
                    let raw = String(line.dropFirst("tags:".count)).trimmingCharacters(in: .whitespaces)
                    tags = raw.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
                    continue
                }
                if line.lowercased().hasPrefix("max_retries:") {
                    let val = String(line.dropFirst("max_retries:".count)).trimmingCharacters(in: .whitespaces)
                    maxRetries = max(0, Int(val) ?? 0)
                    continue
                }
                if line.lowercased().hasPrefix("timeout_seconds:") {
                    let val = String(line.dropFirst("timeout_seconds:".count)).trimmingCharacters(in: .whitespaces)
                    timeoutSeconds = Int(val).flatMap { $0 > 0 ? $0 : nil }
                    continue
                }
                if line.lowercased().hasPrefix("notify_telegram:") {
                    let val = String(line.dropFirst("notify_telegram:".count)).trimmingCharacters(in: .whitespaces)
                    telegramChatID = Int64(val)
                    communicationDestination = telegramChatID.map {
                        CommunicationDestination(platform: .telegram, channelID: String($0))
                    }
                    continue
                }
                if line.lowercased().hasPrefix("notify_destination:") {
                    let raw = String(line.dropFirst("notify_destination:".count)).trimmingCharacters(in: .whitespaces)
                    let parts = raw.split(separator: ":", maxSplits: 1).map(String.init)
                    if parts.count == 2, let platform = ChatPlatform(rawValue: parts[0]) {
                        communicationDestination = CommunicationDestination(platform: platform, channelID: parts[1])
                    }
                    continue
                }
                if line.lowercased().hasPrefix("workflow_id:") {
                    workflowID = String(line.dropFirst("workflow_id:".count)).trimmingCharacters(in: .whitespaces)
                    continue
                }
                if line.lowercased().hasPrefix("workflow_inputs:") {
                    let raw = String(line.dropFirst("workflow_inputs:".count)).trimmingCharacters(in: .whitespaces)
                    if let data = raw.data(using: .utf8),
                       let decoded = try? AtlasJSON.decoder.decode([String: String].self, from: data) {
                        workflowInputValues = decoded
                    }
                    continue
                }
                if !line.trimmingCharacters(in: .whitespaces).isEmpty {
                    pastStructuredFields = true
                    promptLines.append(line)
                }
            } else {
                promptLines.append(line)
            }
        }

        let prompt = promptLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !scheduleRaw.isEmpty else { return nil }

        let id = slugify(name)
        return GremlinItem(
            id: id,
            name: name,
            emoji: emoji,
            prompt: prompt,
            scheduleRaw: scheduleRaw,
            isEnabled: isEnabled,
            sourceType: sourceType,
            createdAt: createdAt.isEmpty ? isoDateString(Date()) : createdAt,
            workflowID: workflowID,
            workflowInputValues: workflowInputValues,
            communicationDestination: communicationDestination,
            telegramChatID: telegramChatID,
            gremlinDescription: gremlinDescription,
            tags: tags,
            maxRetries: maxRetries,
            timeoutSeconds: timeoutSeconds,
            lastModifiedAt: lastModifiedAt
        )
    }

    // MARK: - Serialise

    public func serialise(_ items: [GremlinItem]) -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy-MM-dd"
        let today = formatter.string(from: Date())

        var lines: [String] = [
            "# Gremlins",
            "",
            "_Your automations. Each one runs on a schedule, using Atlas's full skill stack._",
            "_Last updated: \(today)_",
            ""
        ]

        for item in items {
            lines.append("---")
            lines.append("")
            let heading = item.emoji.isEmpty ? "## \(item.name)" : "## \(item.name) \(item.emoji)"
            lines.append(heading)
            lines.append("schedule: \(item.scheduleRaw)")
            lines.append("status: \(item.isEnabled ? "enabled" : "disabled")")
            lines.append("created: \(item.createdAt) via \(item.sourceType)")
            lines.append("modified: \(item.lastModifiedAt ?? today)")
            if let desc = item.gremlinDescription {
                lines.append("description: \(desc)")
            }
            if !item.tags.isEmpty {
                lines.append("tags: \(item.tags.joined(separator: ", "))")
            }
            if item.maxRetries > 0 {
                lines.append("max_retries: \(item.maxRetries)")
            }
            if let timeout = item.timeoutSeconds {
                lines.append("timeout_seconds: \(timeout)")
            }
            if let destination = item.communicationDestination {
                lines.append("notify_destination: \(destination.platform.rawValue):\(destination.channelID)")
            } else if let chatID = item.telegramChatID {
                lines.append("notify_telegram: \(chatID)")
            }
            if let workflowID = item.workflowID {
                lines.append("workflow_id: \(workflowID)")
            }
            if let workflowInputValues = item.workflowInputValues,
               let data = try? AtlasJSON.encoder.encode(workflowInputValues),
               let json = String(data: data, encoding: .utf8) {
                lines.append("workflow_inputs: \(json)")
            }
            lines.append("")
            lines.append(item.prompt)
            lines.append("")
        }

        return lines.joined(separator: "\n")
    }

    // MARK: - Schedule Validation

    /// Validates a schedule string and returns the next 3 projected fire times on success.
    public func validateSchedule(_ raw: String) -> GremlinScheduleValidation {
        guard let next1 = nextRunDate(for: raw, after: .now) else {
            return GremlinScheduleValidation(
                isValid: false,
                nextFireDates: [],
                errorMessage: unreckognisedScheduleMessage(raw)
            )
        }
        var dates = [next1]
        if let next2 = nextRunDate(for: raw, after: next1.addingTimeInterval(60)) {
            dates.append(next2)
            if let next3 = nextRunDate(for: raw, after: next2.addingTimeInterval(60)) {
                dates.append(next3)
            }
        }
        return GremlinScheduleValidation(isValid: true, nextFireDates: dates, errorMessage: nil)
    }

    private func unreckognisedScheduleMessage(_ raw: String) -> String {
        let examples = [
            "daily 08:00",
            "daily 08:00 America/New_York",
            "weekly monday 09:00",
            "monthly 1 09:00",
            "weekdays 08:00",
            "weekends 10:00",
            "every 30 minutes",
            "every 2 hours",
            "once 2026-12-01",
            "cron 0 8 * * 1-5"
        ]
        return "Unrecognised schedule format '\(raw)'. Examples: \(examples.joined(separator: ", "))."
    }

    // MARK: - Schedule Parsing

    /// Compute the next run date for a given schedule string after a reference date.
    public func nextRunDate(for scheduleRaw: String, after reference: Date = .now) -> Date? {
        let raw = scheduleRaw.trimmingCharacters(in: .whitespaces).lowercased()
        let cal = Calendar.current

        if raw.hasPrefix("daily ") {
            let rest = String(raw.dropFirst("daily ".count)).trimmingCharacters(in: .whitespaces)
            // "daily HH:MM" or "daily HH:MM TZ"
            let parts = rest.split(separator: " ", maxSplits: 1).map(String.init)
            let timePart = parts[0]
            let tz = parts.count > 1 ? TimeZone(identifier: parts[1]) ?? TimeZone(abbreviation: parts[1]) : nil
            return nextDailyDate(timePart: timePart, timezone: tz ?? cal.timeZone, after: reference)

        } else if raw.hasPrefix("weekly ") {
            // "weekly <day> HH:MM" or "weekly <day> HH:MM TZ"
            let rest = String(raw.dropFirst("weekly ".count)).trimmingCharacters(in: .whitespaces)
            let parts = rest.split(separator: " ").map(String.init)
            guard parts.count >= 2 else { return nil }
            let dayName = parts[0]
            let timePart = parts[1]
            let tz = parts.count > 2 ? TimeZone(identifier: parts[2]) ?? TimeZone(abbreviation: parts[2]) : nil
            guard let targetWeekday = weekdayNumber(from: dayName),
                  let timeComponents = parseTimeComponents(timePart) else { return nil }
            return nextWeeklyDate(weekday: targetWeekday, hour: timeComponents.hour,
                                  minute: timeComponents.minute,
                                  timezone: tz ?? cal.timeZone, after: reference)

        } else if raw.hasPrefix("monthly ") {
            // "monthly <dayOfMonth> HH:MM" e.g. "monthly 1 09:00"
            let rest = String(raw.dropFirst("monthly ".count)).trimmingCharacters(in: .whitespaces)
            let parts = rest.split(separator: " ").map(String.init)
            guard parts.count >= 2,
                  let dayOfMonth = Int(parts[0]), (1...31).contains(dayOfMonth),
                  let timeComponents = parseTimeComponents(parts[1]) else { return nil }
            let tz = parts.count > 2 ? TimeZone(identifier: parts[2]) : nil
            return nextMonthlyDate(day: dayOfMonth, hour: timeComponents.hour,
                                   minute: timeComponents.minute,
                                   timezone: tz ?? cal.timeZone, after: reference)

        } else if raw.hasPrefix("weekdays ") {
            // "weekdays HH:MM" — Monday–Friday
            let rest = String(raw.dropFirst("weekdays ".count)).trimmingCharacters(in: .whitespaces)
            let parts = rest.split(separator: " ").map(String.init)
            guard let timeComponents = parseTimeComponents(parts[0]) else { return nil }
            let tz = parts.count > 1 ? TimeZone(identifier: parts[1]) : nil
            return nextWeekdayOrWeekendDate(
                hour: timeComponents.hour, minute: timeComponents.minute,
                timezone: tz ?? cal.timeZone, after: reference,
                allowedWeekdays: Set(2...6)  // Mon=2 … Fri=6 in Calendar
            )

        } else if raw.hasPrefix("weekends ") {
            // "weekends HH:MM" — Saturday–Sunday
            let rest = String(raw.dropFirst("weekends ".count)).trimmingCharacters(in: .whitespaces)
            let parts = rest.split(separator: " ").map(String.init)
            guard let timeComponents = parseTimeComponents(parts[0]) else { return nil }
            let tz = parts.count > 1 ? TimeZone(identifier: parts[1]) : nil
            return nextWeekdayOrWeekendDate(
                hour: timeComponents.hour, minute: timeComponents.minute,
                timezone: tz ?? cal.timeZone, after: reference,
                allowedWeekdays: Set([1, 7])  // Sun=1, Sat=7 in Calendar
            )

        } else if raw.hasPrefix("every ") && raw.hasSuffix("minutes") {
            let middle = raw.dropFirst("every ".count).dropLast("minutes".count).trimmingCharacters(in: .whitespaces)
            guard let n = Int(middle), n > 0 else { return nil }
            return reference.addingTimeInterval(TimeInterval(n * 60))

        } else if raw.hasPrefix("every ") && raw.hasSuffix("hours") {
            let middle = raw.dropFirst("every ".count).dropLast("hours".count).trimmingCharacters(in: .whitespaces)
            guard let n = Int(middle), n > 0 else { return nil }
            return reference.addingTimeInterval(TimeInterval(n * 3600))

        } else if raw.hasPrefix("once ") {
            let datePart = String(raw.dropFirst("once ".count)).trimmingCharacters(in: .whitespaces)
            guard let date = parseISO(datePart), date > reference else { return nil }
            return date

        } else if raw.hasPrefix("cron ") {
            let expression = String(raw.dropFirst("cron ".count)).trimmingCharacters(in: .whitespaces)
            return parseCronExpression(expression, after: reference)
        }

        return nil
    }

    // MARK: - Schedule helpers

    private func nextDailyDate(timePart: String, timezone: TimeZone, after reference: Date) -> Date? {
        guard let tc = parseTimeComponents(timePart) else { return nil }
        var cal = Calendar.current
        cal.timeZone = timezone
        guard let candidate = cal.date(bySettingHour: tc.hour, minute: tc.minute, second: 0, of: reference) else { return nil }
        if candidate > reference { return candidate }
        return cal.date(byAdding: .day, value: 1, to: candidate)
    }

    private func nextWeeklyDate(weekday: Int, hour: Int, minute: Int,
                                timezone: TimeZone, after reference: Date) -> Date? {
        var cal = Calendar.current
        cal.timeZone = timezone
        let comps = cal.dateComponents([.year, .month, .day, .weekday], from: reference)
        let currentWeekday = comps.weekday ?? 1
        var daysAhead = weekday - currentWeekday
        if daysAhead < 0 { daysAhead += 7 }

        var candidate = cal.date(byAdding: .day, value: daysAhead, to: reference)!
        candidate = cal.date(bySettingHour: hour, minute: minute, second: 0, of: candidate)!
        if candidate <= reference {
            candidate = cal.date(byAdding: .day, value: 7, to: candidate)!
        }
        return candidate
    }

    private func nextMonthlyDate(day: Int, hour: Int, minute: Int,
                                 timezone: TimeZone, after reference: Date) -> Date? {
        var cal = Calendar.current
        cal.timeZone = timezone
        let comps = cal.dateComponents([.year, .month], from: reference)
        guard let year = comps.year, let month = comps.month else { return nil }

        // Try this month first, then walk forward up to 12 months
        for offset in 0...12 {
            var dc = DateComponents()
            dc.year = year
            dc.month = month + offset
            dc.day = day
            dc.hour = hour
            dc.minute = minute
            dc.second = 0
            if let candidate = cal.date(from: dc), candidate > reference {
                return candidate
            }
        }
        return nil
    }

    private func nextWeekdayOrWeekendDate(hour: Int, minute: Int, timezone: TimeZone,
                                          after reference: Date, allowedWeekdays: Set<Int>) -> Date? {
        var cal = Calendar.current
        cal.timeZone = timezone
        var candidate = cal.date(bySettingHour: hour, minute: minute, second: 0, of: reference) ?? reference

        for _ in 0..<14 {
            if candidate > reference {
                let wd = cal.component(.weekday, from: candidate)
                if allowedWeekdays.contains(wd) { return candidate }
            }
            candidate = cal.date(byAdding: .day, value: 1, to: candidate)!
            candidate = cal.date(bySettingHour: hour, minute: minute, second: 0, of: candidate)!
        }
        return nil
    }

    // MARK: - Cron parsing

    private struct CronField {
        let allowed: Set<Int>
        let isWildcard: Bool

        static func parse(_ raw: String, range: ClosedRange<Int>) -> CronField? {
            let t = raw.trimmingCharacters(in: .whitespaces)
            if t == "*" { return CronField(allowed: Set(range), isWildcard: true) }
            if t.hasPrefix("*/") {
                guard let step = Int(t.dropFirst(2)), step > 0 else { return nil }
                return CronField(allowed: Set(stride(from: range.lowerBound, through: range.upperBound, by: step)), isWildcard: false)
            }
            var values = Set<Int>()
            for part in t.split(separator: ",") {
                let s = String(part)
                if let dashIdx = s.firstIndex(of: "-") {
                    let lo = Int(s[s.startIndex..<dashIdx]) ?? -1
                    let hi = Int(s[s.index(after: dashIdx)...]) ?? -1
                    guard lo >= 0, hi >= 0, lo <= hi else { return nil }
                    values.formUnion(Set(lo...hi))
                } else if let n = Int(s) {
                    values.insert(n)
                } else {
                    return nil
                }
            }
            let filtered = values.filter { range.contains($0) }
            guard !filtered.isEmpty else { return nil }
            return CronField(allowed: filtered, isWildcard: false)
        }
    }

    private func parseCronExpression(_ expression: String, after reference: Date) -> Date? {
        let parts = expression.split(separator: " ").map(String.init)
        guard parts.count == 5,
              let minuteField = CronField.parse(parts[0], range: 0...59),
              let hourField   = CronField.parse(parts[1], range: 0...23),
              let domField    = CronField.parse(parts[2], range: 1...31),
              let monthField  = CronField.parse(parts[3], range: 1...12),
              let dowField    = CronField.parse(parts[4], range: 0...7) else { return nil }

        return nextCronDate(
            minuteField: minuteField, hourField: hourField,
            domField: domField, monthField: monthField, dowField: dowField,
            after: reference
        )
    }

    private func nextCronDate(
        minuteField: CronField, hourField: CronField,
        domField: CronField, monthField: CronField, dowField: CronField,
        after reference: Date
    ) -> Date? {
        var cal = Calendar.current
        cal.timeZone = .current

        // Start from the next minute
        guard let start = cal.date(byAdding: .minute, value: 1, to: reference) else { return nil }
        var c = cal.dateComponents([.year, .month, .day, .hour, .minute], from: start)
        c.second = 0; c.nanosecond = 0
        guard var candidate = cal.date(from: c) else { return nil }

        let limit = cal.date(byAdding: .year, value: 2, to: reference)!

        while candidate < limit {
            let comps = cal.dateComponents([.year, .month, .day, .hour, .minute, .weekday], from: candidate)
            guard let year = comps.year, let month = comps.month, let day = comps.day,
                  let hour = comps.hour, let minute = comps.minute,
                  let weekday = comps.weekday else { break }

            // Month check
            if !monthField.allowed.contains(month) {
                let nextMonths = monthField.allowed.filter { $0 > month }.sorted()
                if let nm = nextMonths.first {
                    candidate = cal.date(from: DateComponents(year: year, month: nm, day: 1, hour: 0, minute: 0, second: 0)) ?? limit
                } else {
                    candidate = cal.date(from: DateComponents(year: year + 1, month: monthField.allowed.sorted().first!, day: 1, hour: 0, minute: 0, second: 0)) ?? limit
                }
                continue
            }

            // Day check — cron weekday: Calendar 1=Sun → cron 0=Sun
            let cronDow = weekday == 1 ? 0 : weekday - 1
            let domMatch = domField.allowed.contains(day)
            let dowMatch = dowField.allowed.contains(cronDow) || (cronDow == 0 && dowField.allowed.contains(7))
            let dayMatch: Bool
            if domField.isWildcard && dowField.isWildcard { dayMatch = true } else if domField.isWildcard { dayMatch = dowMatch } else if dowField.isWildcard { dayMatch = domMatch } else { dayMatch = domMatch || dowMatch }

            if !dayMatch {
                let nextDay = cal.date(from: DateComponents(year: year, month: month, day: day + 1, hour: 0, minute: 0, second: 0))
                    ?? cal.date(from: DateComponents(year: year, month: month + 1, day: 1, hour: 0, minute: 0, second: 0))
                    ?? limit
                candidate = nextDay
                continue
            }

            // Hour check
            if !hourField.allowed.contains(hour) {
                let nextHours = hourField.allowed.filter { $0 > hour }.sorted()
                if let nh = nextHours.first {
                    candidate = cal.date(from: DateComponents(year: year, month: month, day: day, hour: nh, minute: 0, second: 0)) ?? limit
                } else {
                    let nextDay = cal.date(from: DateComponents(year: year, month: month, day: day + 1, hour: 0, minute: 0, second: 0))
                        ?? cal.date(from: DateComponents(year: year, month: month + 1, day: 1, hour: 0, minute: 0, second: 0))
                        ?? limit
                    candidate = nextDay
                }
                continue
            }

            // Minute check
            if !minuteField.allowed.contains(minute) {
                let nextMins = minuteField.allowed.filter { $0 > minute }.sorted()
                if let nm = nextMins.first {
                    candidate = cal.date(from: DateComponents(year: year, month: month, day: day, hour: hour, minute: nm, second: 0)) ?? limit
                } else {
                    let nextHours = hourField.allowed.filter { $0 > hour }.sorted()
                    let firstMin = minuteField.allowed.sorted().first!
                    if let nh = nextHours.first {
                        candidate = cal.date(from: DateComponents(year: year, month: month, day: day, hour: nh, minute: firstMin, second: 0)) ?? limit
                    } else {
                        let nextDay = cal.date(from: DateComponents(year: year, month: month, day: day + 1, hour: hourField.allowed.sorted().first!, minute: firstMin, second: 0))
                            ?? cal.date(from: DateComponents(year: year, month: month + 1, day: 1, hour: hourField.allowed.sorted().first!, minute: firstMin, second: 0))
                            ?? limit
                        candidate = nextDay
                    }
                }
                continue
            }

            return candidate
        }

        return nil
    }

    // MARK: - Helpers

    private func slugify(_ name: String) -> String {
        name.lowercased()
            .replacingOccurrences(of: " ", with: "-")
            .filter { $0.isLetter || $0.isNumber || $0 == "-" }
    }

    private func splitNameEmoji(_ raw: String) -> (name: String, emoji: String) {
        let words = raw.components(separatedBy: " ")
        guard let last = words.last, !last.isEmpty else { return (raw, "") }
        let isEmoji = last.unicodeScalars.contains { scalar in
            scalar.properties.isEmoji && scalar.value > 0x00FF
        }
        if isEmoji {
            return (words.dropLast().joined(separator: " "), last)
        }
        return (raw, "")
    }

    func isoDateString(_ date: Date) -> String {
        let fmt = DateFormatter()
        fmt.dateFormat = "yyyy-MM-dd"
        return fmt.string(from: date)
    }

    private func parseHHMM(_ raw: String, relativeTo reference: Date) -> Date? {
        let parts = raw.components(separatedBy: ":")
        guard parts.count >= 2,
              let hour = Int(parts[0]),
              let minute = Int(parts[1]) else { return nil }
        let cal = Calendar.current
        return cal.date(bySettingHour: hour, minute: minute, second: 0, of: reference)
    }

    private struct TimeComponents {
        let hour: Int
        let minute: Int
    }

    private func parseTimeComponents(_ raw: String) -> TimeComponents? {
        let parts = raw.components(separatedBy: ":")
        guard parts.count >= 2,
              let hour = Int(parts[0]),
              let minute = Int(parts[1]) else { return nil }
        return TimeComponents(hour: hour, minute: minute)
    }

    private func weekdayNumber(from name: String) -> Int? {
        // Calendar weekday: Sunday=1
        switch name.lowercased() {
        case "sunday", "sun": return 1
        case "monday", "mon": return 2
        case "tuesday", "tue": return 3
        case "wednesday", "wed": return 4
        case "thursday", "thu": return 5
        case "friday", "fri": return 6
        case "saturday", "sat": return 7
        default: return nil
        }
    }

    private func parseISO(_ raw: String) -> Date? {
        let formatters: [DateFormatter] = [
            { let f = DateFormatter(); f.dateFormat = "yyyy-MM-dd'T'HH:mm:ssZ"; return f }(),
            { let f = DateFormatter(); f.dateFormat = "yyyy-MM-dd'T'HH:mm:ss"; return f }(),
            { let f = DateFormatter(); f.dateFormat = "yyyy-MM-dd"; return f }()
        ]
        for fmt in formatters {
            if let date = fmt.date(from: raw) { return date }
        }
        return nil
    }
}
