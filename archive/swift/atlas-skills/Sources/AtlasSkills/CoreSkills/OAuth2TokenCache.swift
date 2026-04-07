import Foundation

// MARK: - OAuth2TokenCache

/// Process-scoped in-memory token cache for OAuth 2.0 Client Credentials tokens.
///
/// Tokens are keyed by "\(tokenURL)|\(clientIDKey)" to allow multiple APIs to share
/// the same cache without collision.
///
/// Thread-safety: implemented as a Swift actor so concurrent `ForgeSkill` executions
/// do not race on cache reads/writes.
///
/// Security: access tokens are NEVER logged. Expiry uses a 60-second safety margin
/// so tokens are never used in the final minute of their validity window.
public actor OAuth2TokenCache {
    public static let shared = OAuth2TokenCache()

    private struct CachedToken {
        let accessToken: String
        let expiresAt: Date
    }

    private var cache: [String: CachedToken] = [:]

    private init() {}

    /// Returns a valid cached token for `key`, or nil if absent or expired.
    public func token(for key: String) -> String? {
        guard let entry = cache[key], Date() < entry.expiresAt else {
            cache.removeValue(forKey: key)
            return nil
        }
        return entry.accessToken
    }

    /// Stores a token with a 60-second safety margin on the expiry.
    public func store(token: String, expiresIn: Int, for key: String) {
        let expiresAt = Date().addingTimeInterval(TimeInterval(expiresIn) - 60)
        cache[key] = CachedToken(accessToken: token, expiresAt: expiresAt)
    }

    /// Evicts a single entry — call this on a 401 to force a fresh token exchange.
    public func invalidate(for key: String) {
        cache.removeValue(forKey: key)
    }
}
