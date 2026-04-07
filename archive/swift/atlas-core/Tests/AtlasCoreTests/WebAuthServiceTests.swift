import XCTest
import AtlasMemory
@testable import AtlasCore

// MARK: - WebAuthServiceTests
//
// Unit tests for WebAuthService: HMAC-signed launch tokens and session management.
//
// All tests run without live network calls or Keychain access.
//
// Tests cover:
//  1. issueLaunchToken returns a non-empty two-part string
//  2. verifyLaunchToken succeeds for a freshly issued token
//  3. verifyLaunchToken fails for a token with a tampered payload
//  4. verifyLaunchToken fails for a token with a tampered signature
//  5. verifyLaunchToken fails when the same token is used twice (nonce replay)
//  6. createSession returns a valid session with correct TTL
//  7. validateSession returns true for a known session, false for unknown
//  8. validateSession returns false after invalidateSession
//  9. sessionSetCookieValue contains required cookie attributes
// 10. sessionID(fromCookieHeader:) correctly extracts the session ID
// 11. sessionID(fromCookieHeader:) returns nil for missing or wrong cookie

final class WebAuthServiceTests: XCTestCase {

    // MARK: - Helpers

    private func makeService() -> WebAuthService {
        // MemoryStore(":memory:") never fails — safe to force-unwrap in tests
        let store = try! MemoryStore(databasePath: ":memory:")
        return WebAuthService(memoryStore: store)
    }

    // MARK: - 1. issueLaunchToken returns a non-empty two-part string

    func testIssueLaunchTokenReturnsNonEmptyTwoPart() async {
        let service = makeService()
        let token   = await service.issueLaunchToken()

        XCTAssertFalse(token.isEmpty, "Token must not be empty.")

        let parts = token.split(separator: ".", maxSplits: 1, omittingEmptySubsequences: false)
        XCTAssertEqual(parts.count, 2, "Token must have exactly two dot-separated parts.")
        XCTAssertFalse(parts[0].isEmpty, "Token payload part must not be empty.")
        XCTAssertFalse(parts[1].isEmpty, "Token signature part must not be empty.")
    }

    // MARK: - 2. verifyLaunchToken succeeds for a freshly issued token

    func testVerifyFreshTokenSucceeds() async throws {
        let service = makeService()
        let token   = await service.issueLaunchToken()

        // Should not throw
        try await service.verifyLaunchToken(token)
    }

    // MARK: - 3. verifyLaunchToken fails for a token with a tampered payload

    func testVerifyTamperedPayloadFails() async {
        let service = makeService()
        let token   = await service.issueLaunchToken()

        // Replace the payload part with a different base64url string
        var parts   = token.split(separator: ".", maxSplits: 1).map(String.init)
        parts[0]    = "dGFtcGVyZWQ"  // base64url("tampered")
        let tampered = parts.joined(separator: ".")

        do {
            try await service.verifyLaunchToken(tampered)
            XCTFail("Tampered payload must throw.")
        } catch WebAuthService.WebAuthError.invalidToken {
            // expected
        } catch {
            XCTFail("Expected .invalidToken, got: \(error)")
        }
    }

    // MARK: - 4. verifyLaunchToken fails for a token with a tampered signature

    func testVerifyTamperedSignatureFails() async {
        let service = makeService()
        let token   = await service.issueLaunchToken()

        // Replace the signature part with garbage
        var parts   = token.split(separator: ".", maxSplits: 1).map(String.init)
        parts[1]    = "aW52YWxpZHNpZw"  // base64url("invalidsig")
        let tampered = parts.joined(separator: ".")

        do {
            try await service.verifyLaunchToken(tampered)
            XCTFail("Tampered signature must throw.")
        } catch WebAuthService.WebAuthError.invalidToken {
            // expected
        } catch {
            XCTFail("Expected .invalidToken, got: \(error)")
        }
    }

    // MARK: - 5. verifyLaunchToken fails when the same token is used twice (nonce replay)

    func testVerifyReplayedTokenFails() async throws {
        let service = makeService()
        let token   = await service.issueLaunchToken()

        // First use — should succeed
        try await service.verifyLaunchToken(token)

        // Second use — nonce is consumed, must fail
        do {
            try await service.verifyLaunchToken(token)
            XCTFail("Replayed token must throw.")
        } catch WebAuthService.WebAuthError.alreadyUsed {
            // expected
        } catch {
            XCTFail("Expected .alreadyUsed, got: \(error)")
        }
    }

    // MARK: - 6. createSession returns a valid session with correct TTL

    func testCreateSessionReturnsValidSession() async {
        let service = makeService()
        let before  = Date()
        let session = await service.createSession()
        let after   = Date()

        XCTAssertFalse(session.id.isEmpty, "Session ID must not be empty.")
        XCTAssertTrue(session.isValid, "Freshly created session must be valid.")
        XCTAssertGreaterThanOrEqual(session.createdAt, before)
        XCTAssertLessThanOrEqual(session.createdAt, after)

        // TTL should be ~7 days from creation (allow ±5 s tolerance)
        let expectedExpiry = session.createdAt.addingTimeInterval(604_800)
        XCTAssertEqual(session.expiresAt.timeIntervalSince1970,
                       expectedExpiry.timeIntervalSince1970,
                       accuracy: 5.0,
                       "Session TTL must be 7 days.")
    }

    // MARK: - 7. validateSession returns true for a known session, false for unknown

    func testValidateSessionKnownVsUnknown() async {
        let service = makeService()
        let session = await service.createSession()

        let knownValid    = await service.validateSession(id: session.id)
        let unknownValid  = await service.validateSession(id: "nonexistent-id")
        let nilValid      = await service.validateSession(id: nil)

        XCTAssertTrue(knownValid, "validateSession must return true for a known session.")
        XCTAssertFalse(unknownValid, "validateSession must return false for an unknown ID.")
        XCTAssertFalse(nilValid, "validateSession must return false for nil.")
    }

    // MARK: - 8. validateSession returns false after invalidateSession

    func testValidateSessionAfterInvalidation() async {
        let service = makeService()
        let session = await service.createSession()

        let beforeInvalidate = await service.validateSession(id: session.id)
        XCTAssertTrue(beforeInvalidate)
        await service.invalidateSession(id: session.id)
        // Allow the internal async DB-delete Task to complete before re-validating.
        try? await Task.sleep(nanoseconds: 100_000_000)
        let afterInvalidate = await service.validateSession(id: session.id)
        XCTAssertFalse(afterInvalidate,
                       "validateSession must return false after explicit invalidation.")
    }

    // MARK: - 9. sessionSetCookieValue contains required cookie attributes

    func testSessionSetCookieValueAttributes() async {
        let service = makeService()
        let session = await service.createSession()
        let cookie  = service.sessionSetCookieValue(for: session)

        XCTAssertTrue(cookie.hasPrefix("\(WebAuthService.sessionCookieName)="),
                      "Cookie must start with the session cookie name.")
        XCTAssertTrue(cookie.contains(session.id),
                      "Cookie must contain the session ID.")
        XCTAssertTrue(cookie.contains("HttpOnly"),
                      "Cookie must be HttpOnly.")
        XCTAssertTrue(cookie.contains("SameSite=Strict"),
                      "Cookie must have SameSite=Strict.")
        XCTAssertTrue(cookie.contains("Path=/"),
                      "Cookie must have Path=/.")
        XCTAssertTrue(cookie.contains("Max-Age=604800"),
                      "Cookie must have Max-Age=604800 (7 days).")
        XCTAssertFalse(cookie.contains("Secure"),
                       "Cookie must NOT have Secure flag — server is HTTP-only on localhost.")
    }

    // MARK: - 10. sessionID(fromCookieHeader:) correctly extracts the session ID

    func testSessionIDExtractionFromCookieHeader() {
        let id     = "abc123def456"
        let header = "other_cookie=value; \(WebAuthService.sessionCookieName)=\(id); another=x"
        let result = WebAuthService.sessionID(fromCookieHeader: header)
        XCTAssertEqual(result, id, "sessionID must be extracted correctly from a multi-cookie header.")
    }

    // MARK: - 11. sessionID returns nil for missing or wrong cookie

    func testSessionIDReturnsNilForMissingCookie() {
        XCTAssertNil(WebAuthService.sessionID(fromCookieHeader: nil),
                     "nil header must return nil.")
        XCTAssertNil(WebAuthService.sessionID(fromCookieHeader: "other=value; unrelated=x"),
                     "Header without atlas_session must return nil.")
        XCTAssertNil(WebAuthService.sessionID(fromCookieHeader: ""),
                     "Empty header must return nil.")
    }
}
