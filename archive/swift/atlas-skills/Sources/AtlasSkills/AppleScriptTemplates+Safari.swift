import Foundation
import AtlasTools

extension AppleScriptTemplates {

    static func getCurrentTab() -> String {
        return """
        tell application "Safari"
            if (count of windows) is 0 then return "No Safari windows are open."
            set w to front window
            if (count of tabs of w) is 0 then return "No tabs in the front window."
            set t to current tab of w
            set output to "URL:" & (URL of t) & "\\n"
            set output to output & "TITLE:" & (name of t) & "\\n"
            return output
        end tell
        """
    }

    static func listAllTabs() -> String {
        return """
        tell application "Safari"
            if (count of windows) is 0 then return "No Safari windows are open."
            set output to ""
            set windowIndex to 1
            repeat with w in windows
                try
                    repeat with t in tabs of w
                        set output to output & "WINDOW:" & windowIndex & "\\n"
                        set output to output & "URL:" & (URL of t) & "\\n"
                        set output to output & "TITLE:" & (name of t) & "\\n"
                        set output to output & "---\\n"
                    end repeat
                end try
                set windowIndex to windowIndex + 1
            end repeat
            if output is "" then return "No tabs found."
            return output
        end tell
        """
    }

    static func navigateToURL(_ url: String, newTab: Bool) throws -> String {
        guard !url.lowercased().hasPrefix("javascript:") else {
            throw AtlasToolError.invalidInput("javascript: URLs are not permitted in safari_navigate.")
        }

        let sanitizedURL = sanitize(url)
        if newTab {
            return """
            tell application "Safari"
                if (count of windows) is 0 then
                    make new document with properties {URL:"\(sanitizedURL)"}
                else
                    tell front window
                        set newTab to make new tab with properties {URL:"\(sanitizedURL)"}
                        set current tab to newTab
                    end tell
                end if
                return "Opened \\"\(sanitizedURL)\\" in a new tab."
            end tell
            """
        } else {
            return """
            tell application "Safari"
                if (count of windows) is 0 then
                    make new document with properties {URL:"\(sanitizedURL)"}
                else
                    set URL of current tab of front window to "\(sanitizedURL)"
                end if
                return "Navigated to \\"\(sanitizedURL)\\"."
            end tell
            """
        }
    }
}
