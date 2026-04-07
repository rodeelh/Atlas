import Foundation

enum AppleScriptTemplates {}

extension AppleScriptTemplates {

    static func listCalendarEvents(
        startDate: Date,
        endDate: Date,
        calendarName: String?,
        maxResults: Int
    ) -> String {
        let start = dateComponents(startDate)
        let end = dateComponents(endDate)
        let calFilter = calendarName.map { name in
            "if (name of cal) is \"\(sanitize(name))\" then"
        } ?? "if true then"

        return """
        tell application "Calendar"
            set output to ""
            set resultCount to 0
            repeat with cal in calendars
                \(calFilter)
                    set startDateVal to current date
                    set year of startDateVal to \(start.year)
                    set month of startDateVal to \(start.month)
                    set day of startDateVal to \(start.day)
                    set time of startDateVal to \(start.timeSeconds)
                    set endDateVal to current date
                    set year of endDateVal to \(end.year)
                    set month of endDateVal to \(end.month)
                    set day of endDateVal to \(end.day)
                    set time of endDateVal to \(end.timeSeconds)
                    try
                        set theEvents to (events of cal whose start date >= startDateVal and start date <= endDateVal)
                        repeat with e in theEvents
                            if resultCount >= \(maxResults) then exit repeat
                            set output to output & "TITLE:" & (summary of e) & "\\n"
                            set output to output & "START:" & ((start date of e) as text) & "\\n"
                            set output to output & "END:" & ((end date of e) as text) & "\\n"
                            set output to output & "CALENDAR:" & (name of cal) & "\\n"
                            try
                                set output to output & "LOCATION:" & (location of e) & "\\n"
                            end try
                            try
                                set output to output & "NOTES:" & (description of e) & "\\n"
                            end try
                            set output to output & "---\\n"
                            set resultCount to resultCount + 1
                        end repeat
                    end try
                end if
            end repeat
            if output is "" then return "No events found in the specified date range."
            return output
        end tell
        """
    }

    static func createCalendarEvent(
        title: String,
        startDate: Date,
        endDate: Date,
        calendarName: String,
        notes: String?,
        location: String?
    ) -> String {
        let start = dateComponents(startDate)
        let end = dateComponents(endDate)
        let notesLine = notes.map { "set description of newEvent to \"\(sanitize($0))\"" } ?? ""
        let locationLine = location.map { "set location of newEvent to \"\(sanitize($0))\"" } ?? ""

        return """
        tell application "Calendar"
            set targetCalendar to missing value
            repeat with cal in calendars
                if (name of cal) is "\(sanitize(calendarName))" then
                    set targetCalendar to cal
                    exit repeat
                end if
            end repeat
            if targetCalendar is missing value then
                return "Error: Calendar \\"\(sanitize(calendarName))\\" not found."
            end if
            set startDateVal to current date
            set year of startDateVal to \(start.year)
            set month of startDateVal to \(start.month)
            set day of startDateVal to \(start.day)
            set time of startDateVal to \(start.timeSeconds)
            set endDateVal to current date
            set year of endDateVal to \(end.year)
            set month of endDateVal to \(end.month)
            set day of endDateVal to \(end.day)
            set time of endDateVal to \(end.timeSeconds)
            tell targetCalendar
                set newEvent to make new event with properties {summary:"\(sanitize(title))", start date:startDateVal, end date:endDateVal}
                \(notesLine)
                \(locationLine)
            end tell
            return "Created event \\"\(sanitize(title))\\" in \\"" & (name of targetCalendar) & "\\"."
        end tell
        """
    }

    // MARK: - v2 additions

    static func listCalendars() -> String {
        return """
        tell application "Calendar"
            set output to ""
            repeat with cal in calendars
                set output to output & "NAME:" & (name of cal) & "\\n---\\n"
            end repeat
            if output is "" then return "No calendars found."
            return output
        end tell
        """
    }

    static func deleteCalendarEvent(
        title: String,
        startDate: Date,
        calendarName: String?
    ) -> String {
        let start = dateComponents(startDate)
        let windowStart = max(start.timeSeconds - 3600, 0)
        let windowEnd = start.timeSeconds + 3600
        let calFilter = calendarName.map { name in
            "if (name of cal) is \"\(sanitize(name))\" then"
        } ?? "if true then"

        return """
        tell application "Calendar"
            set found to false
            set winStart to current date
            set year of winStart to \(start.year)
            set month of winStart to \(start.month)
            set day of winStart to \(start.day)
            set time of winStart to \(windowStart)
            set winEnd to current date
            set year of winEnd to \(start.year)
            set month of winEnd to \(start.month)
            set day of winEnd to \(start.day)
            set time of winEnd to \(windowEnd)
            repeat with cal in calendars
                \(calFilter)
                    try
                        set matchingEvents to (events of cal whose summary is "\(sanitize(title))" and start date >= winStart and start date <= winEnd)
                        if (count of matchingEvents) > 0 then
                            delete item 1 of matchingEvents
                            set found to true
                            exit repeat
                        end if
                    end try
                end if
                if found then exit repeat
            end repeat
            if found then
                return "Deleted event \\"\(sanitize(title))\\"."
            else
                return "No event \\"\(sanitize(title))\\" found near the specified date and time."
            end if
        end tell
        """
    }

    // MARK: - Helpers

    private struct DateParts {
        let year: Int
        let month: Int
        let day: Int
        let timeSeconds: Int
    }

    private static func dateComponents(_ date: Date) -> DateParts {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone.current
        let c = cal.dateComponents([.year, .month, .day, .hour, .minute, .second], from: date)
        return DateParts(
            year: c.year ?? 2026,
            month: c.month ?? 1,
            day: c.day ?? 1,
            timeSeconds: ((c.hour ?? 0) * 3600) + ((c.minute ?? 0) * 60) + (c.second ?? 0)
        )
    }

    static func sanitize(_ string: String) -> String {
        string
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
    }
}
