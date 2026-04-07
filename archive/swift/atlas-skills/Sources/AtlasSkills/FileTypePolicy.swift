import Foundation

struct FileTypePolicy: Sendable {
    let supportedExtensions: Set<String>

    init(
        supportedExtensions: Set<String> = [
            "txt", "md", "json", "yaml", "yml", "xml", "csv",
            "swift", "py", "js", "ts", "html", "css"
        ]
    ) {
        self.supportedExtensions = supportedExtensions
    }

    func fileType(for url: URL) -> String? {
        let ext = url.pathExtension.lowercased()
        guard !ext.isEmpty, supportedExtensions.contains(ext) else {
            return nil
        }

        return ext
    }

    func decodeText(from data: Data) -> String? {
        if let utf8 = String(data: data, encoding: .utf8) {
            return utf8
        }

        if let utf16 = String(data: data, encoding: .utf16) {
            return utf16
        }

        if let ascii = String(data: data, encoding: .ascii) {
            return ascii
        }

        if let latin1 = String(data: data, encoding: .isoLatin1) {
            return latin1
        }

        return nil
    }

    func isLikelyBinary(_ data: Data) -> Bool {
        guard !data.isEmpty else {
            return false
        }

        let sample = data.prefix(2048)
        if sample.contains(0) {
            return true
        }

        let controlCharacters = sample.reduce(into: 0) { count, byte in
            if byte < 0x09 || (byte > 0x0D && byte < 0x20) {
                count += 1
            }
        }

        return Double(controlCharacters) / Double(sample.count) > 0.12
    }
}
