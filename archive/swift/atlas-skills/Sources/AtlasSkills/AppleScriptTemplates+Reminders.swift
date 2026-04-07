import Foundation

extension AppleScriptTemplates {

    static func listReminders(
        listName: String?,
        query: String? = nil,
        includeCompleted: Bool,
        maxResults: Int
    ) -> String {
        let listFilter = listName.map { name in
            "if (name of reminderList) is \"\(sanitize(name))\" then"
        } ?? "if true then"

        // Build a single compound filter covering completion + optional keyword search
        var conditions: [String] = []
        if !includeCompleted { conditions.append("(completed of r) is false") }
        if let q = query, !q.isEmpty { conditions.append("(name of r) contains \"\(sanitize(q))\"") }
        let filterLine = conditions.isEmpty
            ? ""
            : "if \(conditions.joined(separator: " and ")) then"
        let filterEnd = conditions.isEmpty ? "" : "end if"

        return """
        tell application "Reminders"
            set output to ""
            set resultCount to 0
            repeat with reminderList in lists
                \(listFilter)
                    repeat with r in reminders of reminderList
                        if resultCount >= \(maxResults) then exit repeat
                        \(filterLine)
                            set output to output & "TITLE:" & (name of r) & "\\n"
                            set output to output & "LIST:" & (name of reminderList) & "\\n"
                            set output to output & "COMPLETED:" & ((completed of r) as text) & "\\n"
                            try
                                set output to output & "DUE:" & ((due date of r) as text) & "\\n"
                            end try
                            try
                                set output to output & "NOTES:" & (body of r) & "\\n"
                            end try
                            set output to output & "---\\n"
                            set resultCount to resultCount + 1
                        \(filterEnd)
                    end repeat
                end if
            end repeat
            if output is "" then return "No reminders found."
            return output
        end tell
        """
    }

    static func listReminderLists() -> String {
        return """
        tell application "Reminders"
            set output to ""
            repeat with reminderList in lists
                set output to output & "NAME:" & (name of reminderList) & "\\n---\\n"
            end repeat
            if output is "" then return "No Reminders lists found."
            return output
        end tell
        """
    }

    static func createReminder(
        name: String,
        listName: String,
        dueDate: Date?,
        notes: String?
    ) -> String {
        var dueDateBlock = ""
        if let due = dueDate {
            var cal = Calendar(identifier: .gregorian)
            cal.timeZone = TimeZone.current
            let c = cal.dateComponents([.year, .month, .day, .hour, .minute, .second], from: due)
            let y = c.year ?? 2026
            let m = c.month ?? 1
            let d = c.day ?? 1
            let t = ((c.hour ?? 0) * 3600) + ((c.minute ?? 0) * 60) + (c.second ?? 0)
            dueDateBlock = """
                set dueDateVal to current date
                set year of dueDateVal to \(y)
                set month of dueDateVal to \(m)
                set day of dueDateVal to \(d)
                set time of dueDateVal to \(t)
                set due date of newReminder to dueDateVal
            """
        }
        let notesLine = notes.map { "set body of newReminder to \"\(sanitize($0))\"" } ?? ""

        return """
        tell application "Reminders"
            set targetList to missing value
            repeat with reminderList in lists
                if (name of reminderList) is "\(sanitize(listName))" then
                    set targetList to reminderList
                    exit repeat
                end if
            end repeat
            if targetList is missing value then
                return "Error: Reminders list \\"\(sanitize(listName))\\" not found."
            end if
            tell targetList
                set newReminder to make new reminder with properties {name:"\(sanitize(name))"}
                \(dueDateBlock)
                \(notesLine)
            end tell
            return "Created reminder \\"\(sanitize(name))\\" in \\"\(sanitize(listName))\\"."
        end tell
        """
    }

    static func completeReminder(
        name: String,
        listName: String?
    ) -> String {
        let listFilter = listName.map { lname in
            "if (name of reminderList) is \"\(sanitize(lname))\" then"
        } ?? "if true then"

        return """
        tell application "Reminders"
            set found to false
            repeat with reminderList in lists
                \(listFilter)
                    repeat with r in reminders of reminderList
                        if (name of r) is "\(sanitize(name))" then
                            set completed of r to true
                            set found to true
                            exit repeat
                        end if
                    end repeat
                end if
                if found then exit repeat
            end repeat
            if found then
                return "Completed reminder \\"\(sanitize(name))\\"."
            else
                return "Reminder \\"\(sanitize(name))\\" not found."
            end if
        end tell
        """
    }
}
