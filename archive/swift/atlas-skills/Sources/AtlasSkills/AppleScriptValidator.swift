import Foundation
import AtlasTools

/// Statically validates custom AppleScript strings before execution.
/// Used exclusively by the `applescript.run_custom` action.
struct AppleScriptValidator: Sendable {

    /// Validates a custom script for blocked constructs.
    /// Throws `AtlasToolError.invalidInput` if any disallowed pattern is found.
    func validate(_ script: String) throws {
        // 1. Strip invisible and bidirectional Unicode control characters.
        //    These can be inserted to make a blocked keyword invisible to a
        //    plain string search while still being interpreted by the AppleScript
        //    lexer (e.g. zero-width space between "do" and " shell script", or
        //    a right-to-left override that visually hides the keyword).
        let stripped = String(
            script.unicodeScalars.filter { !Self.blockedCodepoints.contains($0.value) }
        )

        // 2. NFKD-normalize so visually similar characters (fullwidth letters,
        //    ligatures, compatibility forms) are decomposed to their ASCII
        //    equivalents before the blocklist check.
        let normalized = stripped.applyingTransform(.init("NFKD"), reverse: false) ?? stripped
        let lower = normalized.lowercased()

        for construct in Self.blockedConstructs {
            if lower.contains(construct.pattern) {
                throw AtlasToolError.invalidInput(construct.reason)
            }
        }
    }

    // MARK: - Private constants

    /// Unicode codepoints that are invisible to humans but may be significant
    /// to a lexer. Stripped before blocklist matching.
    private static let blockedCodepoints: Set<UInt32> = [
        0x00AD, // soft hyphen
        0x200B, // zero-width space
        0x200C, // zero-width non-joiner
        0x200D, // zero-width joiner
        0x2060, // word joiner
        0xFEFF, // zero-width no-break space / BOM
        // Bidirectional formatting characters (U+202A – U+202E, U+2066 – U+2069)
        0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
        0x2066, 0x2067, 0x2068, 0x2069
    ]

    private static let blockedConstructs: [(pattern: String, reason: String)] = [
        (
            "do shell script",
            "'do shell script' is not permitted in custom scripts — it bypasses Atlas's security boundary entirely."
        ),
        (
            "run script",
            "'run script' is not permitted — it can load and execute arbitrary external AppleScript files."
        ),
        (
            "load script",
            "'load script' is not permitted — it can import external AppleScript libraries."
        ),
        (
            "display dialog",
            "'display dialog' is not permitted — it blocks the Atlas daemon process until dismissed."
        ),
        (
            "display alert",
            "'display alert' is not permitted — it blocks the Atlas daemon process until dismissed."
        ),
        (
            "choose file",
            "'choose file' is not permitted — it shows a blocking file picker dialog."
        ),
        (
            "choose folder",
            "'choose folder' is not permitted — it shows a blocking folder picker dialog."
        ),
        (
            "keystroke",
            "'keystroke' is not permitted — UI injection via System Events is not allowed."
        ),
        (
            "key code",
            "'key code' is not permitted — UI injection via System Events is not allowed."
        ),
        (
            "open for access",
            "'open for access' is not permitted — it enables file-system write access via Standard Additions."
        ),
        (
            "write to",
            "'write to' is not permitted — it writes data to files via Standard Additions."
        ),
        (
            "close access",
            "'close access' is not permitted — it finalizes file-write operations via Standard Additions."
        ),
        (
            "do javascript",
            "'do JavaScript' is not permitted — it executes arbitrary JavaScript inside Safari or WebKit."
        )
    ]
}
