import Foundation
import AtlasShared

/// macOS implementation of `FileAccessGrantAdapter`.
///
/// Produces security-scoped bookmarks so the runtime can re-acquire read access to
/// user-approved directories across app restarts without requiring another permission
/// prompt. This adapter is shell-side only — no runtime business logic lives here.
///
/// Usage: the shell (e.g. `AtlasAppState`) calls `createGrant(for:)` after the user
/// selects a folder via `NSOpenPanel`, then passes the returned data to
/// `FileAccessScopeStore.addRoot(bookmarkData:)`.
public struct MacOSBookmarkGrantAdapter: FileAccessGrantAdapter {
    public init() {}

    public func createGrant(for url: URL) throws -> Data {
        try FileAccessScopeStore.makeBookmarkData(for: url)
    }
}
