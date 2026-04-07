import Foundation
import AtlasTools

struct SystemActionPolicy: Sendable {
    let scopeStore: FileAccessScopeStore

    func approvedRootCount() async -> Int {
        await scopeStore.approvedRootCount()
    }

    func validateURL(for rawURL: String) throws -> URL {
        let trimmed = rawURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), let scheme = url.scheme?.lowercased() else {
            throw AtlasToolError.invalidInput("'\(rawURL)' is not a valid URL.")
        }
        guard scheme == "http" || scheme == "https" else {
            throw AtlasToolError.invalidInput("Only http and https URLs are supported by system.open_url.")
        }
        guard url.host != nil else {
            throw AtlasToolError.invalidInput("URL must include a host.")
        }
        return url
    }

    func resolveFileURL(for path: String, expectsDirectory: Bool? = nil) async throws -> URL {
        let access = try await scopeStore.resolveAccess(for: path)
        let url = access.targetURL
        let fileManager = FileManager.default

        guard fileManager.fileExists(atPath: url.path) else {
            throw AtlasToolError.invalidInput("'\(url.path)' does not exist inside the approved folders.")
        }

        var isDirectory: ObjCBool = false
        fileManager.fileExists(atPath: url.path, isDirectory: &isDirectory)

        if let expectsDirectory {
            if expectsDirectory, !isDirectory.boolValue {
                throw AtlasToolError.invalidInput("'\(url.path)' is not a folder inside the approved file scope.")
            }

            if !expectsDirectory, isDirectory.boolValue {
                throw AtlasToolError.invalidInput("'\(url.path)' is not a file inside the approved file scope.")
            }
        }

        return url
    }
}
