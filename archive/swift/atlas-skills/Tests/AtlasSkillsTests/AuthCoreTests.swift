import XCTest
@testable import AtlasSkills

// MARK: - AuthCoreTests
//
// Unit tests for AuthCore v1: APIAuthType serialization, AuthSupportLevel matrix,
// and refusal message content.
//
// Tests verify:
//  1. All APIAuthType cases round-trip through Codable correctly
//  2. Supported auth types return .supported from AuthCore
//  3. OAuth types return .requiresFutureOAuthSupport
//  4. Custom/unknown types return .unsupported
//  5. isSupported() returns the correct boolean for each type
//  6. canForge is true only for .supported
//  7. refusalMessage() is non-empty and type-appropriate for unsupported types

final class AuthCoreTests: XCTestCase {

    // MARK: - 1. Codable round-trip

    func testAPIAuthTypeCodableRoundTrip() throws {
        for authType in APIAuthType.allCases {
            let encoded = try JSONEncoder().encode(authType)
            let decoded = try JSONDecoder().decode(APIAuthType.self, from: encoded)
            XCTAssertEqual(decoded, authType,
                "APIAuthType.\(authType.rawValue) must survive a Codable round-trip.")
        }
    }

    func testAPIAuthTypeRawValuesMatchExpectedStrings() {
        XCTAssertEqual(APIAuthType.none.rawValue, "none")
        XCTAssertEqual(APIAuthType.apiKeyHeader.rawValue, "apiKeyHeader")
        XCTAssertEqual(APIAuthType.apiKeyQuery.rawValue, "apiKeyQuery")
        XCTAssertEqual(APIAuthType.bearerTokenStatic.rawValue, "bearerTokenStatic")
        XCTAssertEqual(APIAuthType.basicAuth.rawValue, "basicAuth")
        XCTAssertEqual(APIAuthType.oauth2AuthorizationCode.rawValue, "oauth2AuthorizationCode")
        XCTAssertEqual(APIAuthType.oauth2ClientCredentials.rawValue, "oauth2ClientCredentials")
        XCTAssertEqual(APIAuthType.customUnsupported.rawValue, "customUnsupported")
        XCTAssertEqual(APIAuthType.unknown.rawValue, "unknown")
    }

    func testAPIAuthTypeDecodesFromJSONString() throws {
        let json = #"{"authType":"apiKeyHeader"}"#
        struct Wrapper: Codable { let authType: APIAuthType }
        let decoded = try JSONDecoder().decode(Wrapper.self, from: Data(json.utf8))
        XCTAssertEqual(decoded.authType, .apiKeyHeader)
    }

    func testUnrecognisedAuthTypeDecodesAsUnknown() throws {
        // APIAuthType maps unrecognised raw values to .unknown rather than throwing,
        // so legacy stored contractJSON blobs remain decodable.
        let json = #"{"authType":"legacyApiKey"}"#
        struct Wrapper: Codable { let authType: APIAuthType? }
        let decoded = try JSONDecoder().decode(Wrapper.self, from: Data(json.utf8))
        XCTAssertEqual(decoded.authType, .unknown,
            "An unrecognised authType raw value must decode to .unknown (not throw).")
    }

    // MARK: - 2. Supported auth types

    func testNoneAuthTypeIsSupported() {
        XCTAssertEqual(AuthCore.supportLevel(for: .none), .supported)
        XCTAssertTrue(AuthCore.isSupported(.none))
    }

    func testAPIKeyHeaderIsSupported() {
        XCTAssertEqual(AuthCore.supportLevel(for: .apiKeyHeader), .supported)
        XCTAssertTrue(AuthCore.isSupported(.apiKeyHeader))
    }

    func testAPIKeyQueryIsSupported() {
        XCTAssertEqual(AuthCore.supportLevel(for: .apiKeyQuery), .supported)
        XCTAssertTrue(AuthCore.isSupported(.apiKeyQuery))
    }

    func testBearerTokenStaticIsSupported() {
        XCTAssertEqual(AuthCore.supportLevel(for: .bearerTokenStatic), .supported)
        XCTAssertTrue(AuthCore.isSupported(.bearerTokenStatic))
    }

    func testBasicAuthIsSupported() {
        XCTAssertEqual(AuthCore.supportLevel(for: .basicAuth), .supported)
        XCTAssertTrue(AuthCore.isSupported(.basicAuth))
    }

    // MARK: - 3. OAuth types

    func testOAuth2AuthorizationCodeRequiresFutureSupport() {
        XCTAssertEqual(AuthCore.supportLevel(for: .oauth2AuthorizationCode), .requiresFutureOAuthSupport)
        XCTAssertFalse(AuthCore.isSupported(.oauth2AuthorizationCode))
    }

    func testOAuth2ClientCredentialsIsNowSupported() {
        // AuthCore v2: oauth2ClientCredentials is fully supported via OAuth2ClientCredentialsService.
        XCTAssertEqual(AuthCore.supportLevel(for: .oauth2ClientCredentials), .supported)
        XCTAssertTrue(AuthCore.isSupported(.oauth2ClientCredentials))
    }

    // MARK: - 4. Unsupported types

    func testCustomUnsupportedIsUnsupported() {
        XCTAssertEqual(AuthCore.supportLevel(for: .customUnsupported), .unsupported)
        XCTAssertFalse(AuthCore.isSupported(.customUnsupported))
    }

    func testUnknownIsUnsupported() {
        XCTAssertEqual(AuthCore.supportLevel(for: .unknown), .unsupported)
        XCTAssertFalse(AuthCore.isSupported(.unknown))
    }

    // MARK: - 5. canForge matches isSupported

    func testCanForgeMatchesIsSupportedForAllTypes() {
        for authType in APIAuthType.allCases {
            let level = AuthCore.supportLevel(for: authType)
            XCTAssertEqual(
                level.canForge,
                AuthCore.isSupported(authType),
                "canForge and isSupported must agree for \(authType.rawValue)."
            )
        }
    }

    // MARK: - 6. Supported type enumeration (AuthCore v2)

    func testExactlySupportedTypesAreSupported() {
        let supported = APIAuthType.allCases.filter { AuthCore.isSupported($0) }
        XCTAssertEqual(supported.count, 6,
            "AuthCore v2 supports 6 auth types: none, apiKeyHeader, apiKeyQuery, bearerTokenStatic, basicAuth, oauth2ClientCredentials.")
        XCTAssertTrue(supported.contains(.none))
        XCTAssertTrue(supported.contains(.apiKeyHeader))
        XCTAssertTrue(supported.contains(.apiKeyQuery))
        XCTAssertTrue(supported.contains(.bearerTokenStatic))
        XCTAssertTrue(supported.contains(.basicAuth))
        XCTAssertTrue(supported.contains(.oauth2ClientCredentials))
    }

    func testExactly3TypesAreUnsupportedOrDeferred() {
        let unsupported = APIAuthType.allCases.filter { !AuthCore.isSupported($0) }
        XCTAssertEqual(unsupported.count, 3,
            "AuthCore v2 defers 3 auth types: oauth2AuthorizationCode, customUnsupported, unknown.")
        XCTAssertTrue(unsupported.contains(.oauth2AuthorizationCode))
        XCTAssertTrue(unsupported.contains(.customUnsupported))
        XCTAssertTrue(unsupported.contains(.unknown))
    }

    // MARK: - 7. Refusal messages

    func testOAuthAuthorizationCodeRefusalMentionsOAuth() {
        let msg = AuthCore.refusalMessage(for: .oauth2AuthorizationCode, skillName: "Test Skill")
        XCTAssertTrue(
            msg.lowercased().contains("oauth"),
            "Refusal for oauth2AuthorizationCode must mention OAuth. Got: \(msg)"
        )
        XCTAssertTrue(
            msg.contains("Test Skill"),
            "Refusal message must include the skill name."
        )
    }

    func testOAuth2ClientCredentialsHasNoRefusal() {
        // oauth2ClientCredentials is now supported — refusalMessage should indicate it is not refused.
        let msg = AuthCore.refusalMessage(for: .oauth2ClientCredentials, skillName: "My Service")
        XCTAssertTrue(msg.contains("supported"),
            "oauth2ClientCredentials is supported — refusalMessage must indicate no refusal needed. Got: \(msg)")
    }

    func testCustomUnsupportedRefusalMentionsCustomOrProprietary() {
        let msg = AuthCore.refusalMessage(for: .customUnsupported, skillName: "Widget API")
        let lower = msg.lowercased()
        XCTAssertTrue(
            lower.contains("custom") || lower.contains("proprietary"),
            "Refusal for customUnsupported must mention custom or proprietary. Got: \(msg)"
        )
    }

    func testUnknownAuthRefusalMentionsResearch() {
        let msg = AuthCore.refusalMessage(for: .unknown, skillName: "Mystery API")
        let lower = msg.lowercased()
        XCTAssertTrue(
            lower.contains("research") || lower.contains("identified") || lower.contains("identify"),
            "Refusal for unknown must suggest further research. Got: \(msg)"
        )
    }

    func testRefusalMessagesListSupportedTypesForUnknown() {
        let msg = AuthCore.refusalMessage(for: .unknown, skillName: "X")
        XCTAssertTrue(
            msg.contains("apiKeyHeader") || msg.contains("bearerTokenStatic"),
            "Refusal for unknown auth must list supported alternatives so the user knows what to do."
        )
    }
}
