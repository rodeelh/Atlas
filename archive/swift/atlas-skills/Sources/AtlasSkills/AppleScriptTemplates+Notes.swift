import Foundation

extension AppleScriptTemplates {

    static func listNotes(
        folderName: String?,
        maxResults: Int
    ) -> String {
        let folderBlock: String
        if let folder = folderName {
            folderBlock = """
            set targetFolder to missing value
            repeat with f in folders
                if (name of f) is "\(sanitize(folder))" then
                    set targetFolder to f
                    exit repeat
                end if
            end repeat
            if targetFolder is missing value then
                return "Notes folder \\"\(sanitize(folder))\\" not found."
            end if
            set theNotes to notes of targetFolder
            """
        } else {
            folderBlock = "set theNotes to every note"
        }

        return """
        tell application "Notes"
            \(folderBlock)
            set output to ""
            set resultCount to 0
            repeat with n in theNotes
                if resultCount >= \(maxResults) then exit repeat
                set output to output & "TITLE:" & (name of n) & "\\n"
                set output to output & "MODIFIED:" & ((modification date of n) as text) & "\\n"
                try
                    set output to output & "FOLDER:" & (name of container of n) & "\\n"
                end try
                set output to output & "---\\n"
                set resultCount to resultCount + 1
            end repeat
            if output is "" then return "No notes found."
            return output
        end tell
        """
    }

    static func searchNotes(
        query: String,
        maxResults: Int
    ) -> String {
        return """
        tell application "Notes"
            set searchQuery to "\(sanitize(query))"
            set theNotes to every note whose name contains searchQuery
            set output to ""
            set resultCount to 0
            repeat with n in theNotes
                if resultCount >= \(maxResults) then exit repeat
                set output to output & "TITLE:" & (name of n) & "\\n"
                set output to output & "MODIFIED:" & ((modification date of n) as text) & "\\n"
                try
                    set output to output & "FOLDER:" & (name of container of n) & "\\n"
                end try
                set output to output & "---\\n"
                set resultCount to resultCount + 1
            end repeat
            if output is "" then return "No notes found matching \\"\(sanitize(query))\\"."
            return output
        end tell
        """
    }

    static func readNote(
        title: String,
        folderName: String?
    ) -> String {
        let folderBlock: String
        if let folder = folderName {
            folderBlock = """
            set targetFolder to missing value
            repeat with f in folders
                if (name of f) is "\(sanitize(folder))" then
                    set targetFolder to f
                    exit repeat
                end if
            end repeat
            if targetFolder is missing value then
                return "Notes folder \\"\(sanitize(folder))\\" not found."
            end if
            set matchingNotes to notes of targetFolder whose name is "\(sanitize(title))"
            """
        } else {
            folderBlock = "set matchingNotes to every note whose name is \"\(sanitize(title))\""
        }

        return """
        tell application "Notes"
            \(folderBlock)
            if (count of matchingNotes) is 0 then
                return "Note \\"\(sanitize(title))\\" not found."
            end if
            set n to item 1 of matchingNotes
            set rawBody to body of n
            return "TITLE:" & (name of n) & "\\nMODIFIED:" & ((modification date of n) as text) & "\\nBODY:" & rawBody
        end tell
        """
    }

    static func createNote(
        title: String,
        body: String,
        folderName: String?
    ) -> String {
        let folderBlock: String
        if let folder = folderName {
            folderBlock = """
            set targetFolder to missing value
            repeat with f in folders
                if (name of f) is "\(sanitize(folder))" then
                    set targetFolder to f
                    exit repeat
                end if
            end repeat
            if targetFolder is missing value then
                return "Notes folder \\"\(sanitize(folder))\\" not found."
            end if
            tell targetFolder
                make new note with properties {name:"\(sanitize(title))", body:"\(sanitize(body))"}
            end tell
            """
        } else {
            folderBlock = "make new note with properties {name:\"\(sanitize(title))\", body:\"\(sanitize(body))\"}"
        }

        return """
        tell application "Notes"
            \(folderBlock)
            return "Created note \\"\(sanitize(title))\\"."
        end tell
        """
    }

    static func appendToNote(
        title: String,
        textToAppend: String,
        folderName: String?
    ) -> String {
        let folderBlock: String
        if let folder = folderName {
            folderBlock = """
            set targetFolder to missing value
            repeat with f in folders
                if (name of f) is "\(sanitize(folder))" then
                    set targetFolder to f
                    exit repeat
                end if
            end repeat
            if targetFolder is missing value then
                return "Notes folder \\"\(sanitize(folder))\\" not found."
            end if
            set matchingNotes to notes of targetFolder whose name is "\(sanitize(title))"
            """
        } else {
            folderBlock = "set matchingNotes to every note whose name is \"\(sanitize(title))\""
        }

        return """
        tell application "Notes"
            \(folderBlock)
            if (count of matchingNotes) is 0 then
                return "Note \\"\(sanitize(title))\\" not found."
            end if
            set n to item 1 of matchingNotes
            set body of n to (body of n) & "<br>\(sanitize(textToAppend))"
            return "Appended to note \\"\(sanitize(title))\\"."
        end tell
        """
    }
}
