import Foundation

extension AppleScriptTemplates {

    static func searchContacts(
        query: String,
        maxResults: Int
    ) -> String {
        return """
        tell application "Contacts"
            set output to ""
            set resultCount to 0
            set searchQuery to "\(sanitize(query))"
            set matchingPeople to every person whose (name contains searchQuery) or ¬
                (first name contains searchQuery) or ¬
                (last name contains searchQuery)
            repeat with p in matchingPeople
                if resultCount >= \(maxResults) then exit repeat
                set output to output & "NAME:" & (name of p) & "\\n"
                try
                    set theEmails to every email of p
                    repeat with e in theEmails
                        set output to output & "EMAIL:" & (value of e) & "\\n"
                    end repeat
                end try
                try
                    set thePhones to every phone of p
                    repeat with ph in thePhones
                        set output to output & "PHONE:" & (value of ph) & "\\n"
                    end repeat
                end try
                try
                    set output to output & "ORGANIZATION:" & (organization of p) & "\\n"
                end try
                set output to output & "---\\n"
                set resultCount to resultCount + 1
            end repeat
            if output is "" then return "No contacts found matching \\"\(sanitize(query))\\"."
            return output
        end tell
        """
    }
}
