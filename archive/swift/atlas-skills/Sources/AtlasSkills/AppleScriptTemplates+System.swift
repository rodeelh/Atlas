import Foundation

extension AppleScriptTemplates {

    /// Returns basic system information: macOS version, hostname, current user, and CPU type.
    /// Uses Standard Additions `system info` — requires no TCC permissions.
    static func systemInfo() -> String {
        return """
        set sysInfo to system info
        set output to "OS:" & (system version of sysInfo) & "\\n"
        set output to output & "HOST:" & (host name of sysInfo) & "\\n"
        set output to output & "USER:" & (short user name of sysInfo) & "\\n"
        set output to output & "CPU:" & (cpu type of sysInfo) & "\\n"
        return output
        """
    }
}
