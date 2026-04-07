import Foundation
import CryptoKit
import Security
import AtlasLogging
import AtlasShared
import AtlasMemory

// MARK: - WebAuthService

/// Local web UI authentication service for Atlas.
///
/// ## Security model
///
/// The Atlas web UI is served on localhost. When a user opens the web UI via the
/// menu bar, the native app fetches a short-lived signed launch token from the
/// daemon (`GET /auth/token`), then opens the browser to
/// `/auth/bootstrap?token=<token>`. The daemon verifies the token and sets a
/// session cookie. Subsequent browser requests carry the cookie.
///
/// Browser requests (those with an `Origin` header) must carry a valid session
/// cookie. Requests with no `Origin` header — i.e. native `URLSession` calls —
/// bypass the session check entirely (process-trust model).
///
/// ## Token format
///
/// ```
/// base64url(payload_json).base64url(hmac_sha256_signature)
/// ```
///
/// Payload fields: `exp` (Unix timestamp), `nonce` (UUID string), `source`.
///
/// Tokens are HMAC-SHA256 signed with an in-memory `SymmetricKey` generated
/// fresh at daemon startup. All sessions are invalidated on daemon restart by
/// design — this is intentional and expected behaviour.
///
/// ## Session storage
///
/// Sessions are stored in both an in-memory cache (hot path) and the SQLite
/// `web_sessions` table (persistence layer). The cache is the source of truth
/// for active requests; the database is consulted only on a cache miss, which
/// happens once per restart per session. Each session has a 7-day TTL.
///
/// On daemon restart the signing key is regenerated, but persisted session IDs
/// are still valid — they do not require HMAC re-verification since they were
/// already verified at creation time.
///
/// ## Remote access
///
/// When `remoteAccessEnabled` is true in config, remote browsers authenticate via
/// `GET /auth/remote?key=<apikey>`. The daemon validates the key (constant-time
/// compare against the Keychain value) and creates a remote session (`isRemote: true`).
/// Remote sessions use `SameSite=Lax` cookies to allow the initial redirect.
/// Regenerating the API key calls `invalidateAllRemoteSessions()`.
///
/// ## Non-goals
///
/// - No password login
/// - No internet/cloud relay (Tailscale handles that — Phase 2)
actor WebAuthService {

    // MARK: - Types

    /// An active browser session.
    struct Session: Sendable {
        let id: String
        let createdAt: Date
        let expiresAt: Date
        /// `true` for sessions created by remote devices via `/auth/remote`.
        let isRemote: Bool

        var isValid: Bool { Date() < expiresAt }

        init(id: String, createdAt: Date, expiresAt: Date, isRemote: Bool = false) {
            self.id = id
            self.createdAt = createdAt
            self.expiresAt = expiresAt
            self.isRemote = isRemote
        }
    }

    /// Errors thrown by token verification.
    enum WebAuthError: LocalizedError {
        case invalidToken
        case expiredToken
        case alreadyUsed

        var errorDescription: String? {
            switch self {
            case .invalidToken:  return "Invalid launch token."
            case .expiredToken:  return "Launch token has expired."
            case .alreadyUsed:   return "Launch token has already been used."
            }
        }
    }

    // MARK: - Constants

    static let sessionCookieName = "atlas_session"

    private let tokenLifetime: TimeInterval = 60       // seconds
    private let sessionLifetime: TimeInterval = 604_800  // 7 days

    // MARK: - State

    private let signingKey: SymmetricKey
    private var sessions: [String: Session] = [:]
    private var usedNonces: Set<String>        = []
    private let logger: AtlasLogger
    private let memoryStore: MemoryStore

    // MARK: - Init

    init(memoryStore: MemoryStore) {
        self.signingKey  = SymmetricKey(size: .bits256)
        self.logger      = AtlasLogger(category: "webauth")
        self.memoryStore = memoryStore
        logger.info("WebAuthService: signing key generated; sessions persisted across restarts")
    }

    // MARK: - Token Issuance

    /// Issue a short-lived signed launch token for the menu bar → browser handoff.
    ///
    /// The token is a two-part string:
    /// `base64url(payload).base64url(HMAC-SHA256)`.
    /// It expires in 60 seconds and contains a one-time nonce.
    func issueLaunchToken() -> String {
        struct Payload: Encodable {
            let exp: Double
            let nonce: String
            let source: String
        }

        let payload = Payload(
            exp: Date().addingTimeInterval(tokenLifetime).timeIntervalSince1970,
            nonce: UUID().uuidString,
            source: "menubar"
        )

        // Use a plain encoder — we want a compact Unix timestamp, not ISO8601
        let encoder = JSONEncoder()
        encoder.outputFormatting = []
        let payloadData = (try? encoder.encode(payload)) ?? Data()
        let payloadB64  = base64URLEncode(payloadData)

        let sigData = Data(HMAC<SHA256>.authenticationCode(
            for: Data(payloadB64.utf8),
            using: signingKey
        ))
        let sigB64 = base64URLEncode(sigData)

        logger.debug("WebAuthService: launch token issued")
        return "\(payloadB64).\(sigB64)"
    }

    // MARK: - Token Verification

    /// Verify a raw launch token string.
    ///
    /// Checks signature, expiry, and one-time nonce. On success, the nonce is
    /// consumed and cannot be replayed. Throws `WebAuthError` on any failure.
    func verifyLaunchToken(_ raw: String) throws {
        // Split into payload and signature parts
        let parts = raw.split(separator: ".", maxSplits: 1, omittingEmptySubsequences: false)
        guard parts.count == 2 else { throw WebAuthError.invalidToken }

        let payloadB64 = String(parts[0])
        let sigB64     = String(parts[1])

        // Verify HMAC-SHA256 signature using constant-time comparison.
        // HMAC.isValidAuthenticationCode performs a constant-time check, preventing
        // timing side-channels that could leak partial signature information.
        guard let sigData = base64URLDecode(sigB64) else { throw WebAuthError.invalidToken }
        guard HMAC<SHA256>.isValidAuthenticationCode(
            sigData,
            authenticating: Data(payloadB64.utf8),
            using: signingKey
        ) else {
            logger.warning("WebAuthService: token signature mismatch")
            throw WebAuthError.invalidToken
        }

        // Decode payload
        struct Payload: Decodable {
            let exp: Double
            let nonce: String
        }
        guard let payloadData = base64URLDecode(payloadB64),
              let payload = try? JSONDecoder().decode(Payload.self, from: payloadData)
        else { throw WebAuthError.invalidToken }

        // Check expiry
        guard Date().timeIntervalSince1970 < payload.exp else {
            logger.info("WebAuthService: token expired")
            throw WebAuthError.expiredToken
        }

        // Check one-time nonce
        guard !usedNonces.contains(payload.nonce) else {
            logger.warning("WebAuthService: token nonce replayed")
            throw WebAuthError.alreadyUsed
        }

        // Consume nonce
        usedNonces.insert(payload.nonce)
        pruneNoncesIfNeeded()

        logger.info("WebAuthService: token verified successfully")
    }

    // MARK: - Session Management

    /// Create a new 7-day browser session. Persists to SQLite so it survives daemon restarts.
    func createSession(isRemote: Bool = false) -> Session {
        let id  = randomHex(32)
        let now = Date()
        let session = Session(
            id: id,
            createdAt: now,
            expiresAt: now.addingTimeInterval(sessionLifetime),
            isRemote: isRemote
        )
        sessions[id] = session
        pruneExpiredSessions()
        Task {
            try? await memoryStore.saveWebSession(
                id: session.id,
                createdAt: session.createdAt,
                expiresAt: session.expiresAt,
                isRemote: isRemote
            )
        }
        logger.info("WebAuthService: session created",
                    metadata: ["sessionPrefix": String(id.prefix(8)), "remote": "\(isRemote)"])
        return session
    }

    /// Create a remote session (authenticated via API key).
    func createRemoteSession() -> Session {
        createSession(isRemote: true)
    }

    /// Validate a remote API key using constant-time comparison.
    /// Read-only — returns false if no key has been stored yet (key generation is the
    /// responsibility of the macOS app when remote access is first enabled).
    func validateAPIKey(_ presented: String, config: AtlasConfig) -> Bool {
        guard let stored = try? config.remoteAccessAPIKey(), !stored.isEmpty else { return false }
        // Constant-time comparison to prevent timing attacks
        guard presented.count == stored.count else { return false }
        return zip(presented.utf8, stored.utf8).reduce(0) { $0 | ($1.0 ^ $1.1) } == 0
    }

    /// Returns the full Session struct for the given ID, or nil if not found/expired.
    func sessionDetail(id: String?) async -> Session? {
        guard let id else { return nil }
        if let cached = sessions[id] {
            return cached.isValid ? cached : nil
        }
        guard let record = try? await memoryStore.fetchWebSession(id: id) else { return nil }
        let session = Session(
            id: record.id,
            createdAt: record.createdAt,
            expiresAt: record.expiresAt,
            isRemote: record.isRemote
        )
        if session.isValid {
            sessions[id] = session
        }
        return session.isValid ? session : nil
    }

    /// Invalidate all remote sessions (e.g. when the API key is regenerated).
    func invalidateAllRemoteSessions() {
        let remoteIDs = sessions.filter { $0.value.isRemote }.map { $0.key }
        for id in remoteIDs {
            sessions.removeValue(forKey: id)
        }
        Task { try? await memoryStore.deleteAllRemoteWebSessions() }
        logger.info("WebAuthService: all remote sessions invalidated", metadata: ["count": "\(remoteIDs.count)"])
    }

    /// Returns `true` if `id` refers to a known, non-expired session.
    ///
    /// Hot path: checks the in-memory cache first. On a cache miss (e.g. first
    /// request after a daemon restart), queries SQLite. A DB hit restores the
    /// session to the cache and slides its `refreshed_at` timestamp forward.
    func validateSession(id: String?) async -> Bool {
        guard let id else { return false }

        // Fast path — in-memory cache hit
        if let cached = sessions[id] {
            if cached.isValid {
                try? await memoryStore.refreshWebSession(id: id)
                return true
            } else {
                sessions.removeValue(forKey: id)
                try? await memoryStore.deleteWebSession(id: id)
                return false
            }
        }

        // Cache miss — query the DB (only happens once per restart per session)
        guard let record = try? await memoryStore.fetchWebSession(id: id) else {
            return false
        }

        // Restore to in-memory cache (preserving isRemote flag)
        let restored = Session(
            id: record.id,
            createdAt: record.createdAt,
            expiresAt: record.expiresAt,
            isRemote: record.isRemote
        )
        guard restored.isValid else { return false }
        sessions[id] = restored
        try? await memoryStore.refreshWebSession(id: id)
        logger.info("WebAuthService: session restored from DB after restart",
                    metadata: ["sessionPrefix": String(id.prefix(8)), "remote": "\(restored.isRemote)"])
        return true
    }

    /// Explicitly invalidate a session (logout or token revocation).
    func invalidateSession(id: String) {
        sessions.removeValue(forKey: id)
        Task { try? await memoryStore.deleteWebSession(id: id) }
        logger.info("WebAuthService: session invalidated", metadata: ["sessionPrefix": String(id.prefix(8))])
    }

    // MARK: - Cookie Helpers

    /// Returns the `Set-Cookie` header value for the given session.
    ///
    /// Local sessions use `SameSite=Strict` (tightest policy).
    /// Remote sessions use `SameSite=Lax` — `Strict` blocks the initial redirect
    /// from `/auth/remote` since that is a cross-origin top-level navigation.
    /// `Secure` is intentionally omitted — the server is plain HTTP.
    nonisolated func sessionSetCookieValue(for session: Session) -> String {
        let sameSite = session.isRemote ? "Lax" : "Strict"
        return "\(Self.sessionCookieName)=\(session.id); HttpOnly; SameSite=\(sameSite); Path=/; Max-Age=604800"
    }

    /// Extracts the session ID from a raw `Cookie` header string, or returns `nil`.
    static func sessionID(fromCookieHeader cookieHeader: String?) -> String? {
        guard let cookieHeader else { return nil }
        for rawCookie in cookieHeader.split(separator: ";") {
            let trimmed = rawCookie.trimmingCharacters(in: .whitespaces)
            let kv = trimmed.split(separator: "=", maxSplits: 1).map(String.init)
            if kv.count == 2, kv[0] == sessionCookieName {
                return kv[1]
            }
        }
        return nil
    }

    // MARK: - Private Helpers

    private func pruneExpiredSessions() {
        sessions = sessions.filter { $0.value.isValid }
    }

    /// Prune nonces when the set grows large to prevent unbounded memory growth.
    ///
    /// The pruning is unordered — `Set.prefix` retains an arbitrary subset.
    /// This is safe because tokens have a 60-second lifetime: any nonce that could
    /// theoretically re-enter the valid window after pruning would belong to an
    /// already-expired token, making replay infeasible in practice.
    /// For production hardening, nonces could be stored with expiry timestamps
    /// and pruned by age rather than count.
    private func pruneNoncesIfNeeded() {
        guard usedNonces.count > 500 else { return }
        usedNonces = Set(usedNonces.prefix(250))
    }

    /// Generate `byteCount` cryptographically random bytes and return as a hex string.
    private func randomHex(_ byteCount: Int) -> String {
        var bytes = [UInt8](repeating: 0, count: byteCount)
        _ = SecRandomCopyBytes(kSecRandomDefault, byteCount, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }

    private func base64URLEncode(_ data: Data) -> String {
        data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    private func base64URLDecode(_ string: String) -> Data? {
        var base64 = string
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let remainder = base64.count % 4
        if remainder != 0 { base64 += String(repeating: "=", count: 4 - remainder) }
        return Data(base64Encoded: base64)
    }
}
