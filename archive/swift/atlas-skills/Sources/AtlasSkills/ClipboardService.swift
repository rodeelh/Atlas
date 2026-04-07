import AppKit
import Foundation
import AtlasTools

protocol ClipboardServicing: Sendable {
    func copy(text: String) async throws -> SystemCopyToClipboardOutput
    func read() async -> SystemReadClipboardOutput
}

struct ClipboardService: ClipboardServicing {
    func copy(text: String) async throws -> SystemCopyToClipboardOutput {
        let trimmed = text.trimmingCharacters(in: .newlines)
        guard !trimmed.isEmpty else {
            throw AtlasToolError.invalidInput("Atlas can only copy non-empty text to the clipboard.")
        }

        return try await MainActor.run {
            let pasteboard = NSPasteboard.general
            pasteboard.clearContents()
            let success = pasteboard.setString(trimmed, forType: .string)
            guard success else {
                throw AtlasToolError.executionFailed("Atlas could not copy text to the clipboard.")
            }

            return SystemCopyToClipboardOutput(
                characterCount: trimmed.count,
                copied: true,
                message: "Copied \(trimmed.count) characters to the clipboard."
            )
        }
    }

    func read() async -> SystemReadClipboardOutput {
        await MainActor.run {
            let text = NSPasteboard.general.string(forType: .string)
            if let text, !text.isEmpty {
                return SystemReadClipboardOutput(
                    text: text,
                    isEmpty: false,
                    message: "Clipboard contains \(text.count) characters."
                )
            } else {
                return SystemReadClipboardOutput(
                    text: nil,
                    isEmpty: true,
                    message: "The clipboard is empty."
                )
            }
        }
    }
}
