import XCTest
@testable import AtlasSkills
import AtlasGuard
import AtlasLogging
import AtlasShared

final class InfoSkillTests: XCTestCase {
    func testCurrentTimeUsesResolvedTimezone() async throws {
        let skill = makeSkill()

        let result = try await skill.execute(
            actionID: "info.current_time",
            input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Tokyo"}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(InfoCurrentTimeOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.resolvedLocationName, "Tokyo, Japan")
        XCTAssertEqual(decoded.timezoneID, "Asia/Tokyo")
        XCTAssertTrue(!decoded.formattedTime.isEmpty)
    }

    func testCurrentDateFallsBackToLocalContext() async throws {
        let skill = makeSkill()

        let result = try await skill.execute(
            actionID: "info.current_date",
            input: AtlasToolInput(argumentsJSON: "{}"),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(InfoCurrentDateOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.timezoneID, "America/New_York")
        XCTAssertTrue(!decoded.formattedDate.isEmpty)
        XCTAssertTrue(!decoded.weekday.isEmpty)
    }

    func testCurrentTimeFallsBackToPreferredLocationMemoryBeforeSystemLocale() async throws {
        let skill = makeSkill()
        let preferredLocation = MemoryItem(
            category: .profile,
            title: "Preferred location",
            content: "Tokyo",
            source: .userExplicit,
            confidence: 0.98,
            importance: 0.95,
            isUserConfirmed: true,
            tags: ["location", "weather"]
        )

        let result = try await skill.execute(
            actionID: "info.current_time",
            input: AtlasToolInput(argumentsJSON: "{}"),
            context: makeContext(memories: [preferredLocation])
        )

        let decoded = try AtlasJSON.decoder.decode(InfoCurrentTimeOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.resolvedLocationName, "Tokyo, Japan")
        XCTAssertEqual(decoded.timezoneID, "Asia/Tokyo")
    }

    func testTimezoneConversionProducesStructuredSummary() async throws {
        let skill = makeSkill()

        let result = try await skill.execute(
            actionID: "info.timezone_convert",
            input: AtlasToolInput(argumentsJSON: #"{"sourceTime":"3 PM","sourceTimezoneID":"America/New_York","destinationLocationQuery":"Dubai"}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(InfoTimezoneConversionOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.sourceTimezoneID, "America/New_York")
        XCTAssertEqual(decoded.destinationTimezoneID, "Asia/Dubai")
        XCTAssertTrue(decoded.formattedSummary.contains("America/New_York"))
        XCTAssertTrue(decoded.formattedSummary.contains("Asia/Dubai"))
    }

    func testCurrencyForLocationReturnsCurrencyMetadata() async throws {
        let skill = makeSkill()

        let result = try await skill.execute(
            actionID: "info.currency_for_location",
            input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Japan"}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(InfoCurrencyForLocationOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.countryCode, "JP")
        XCTAssertEqual(decoded.currencyCode, "JPY")
    }

    func testCurrencyConvertReturnsStructuredOutput() async throws {
        let skill = makeSkill()

        let result = try await skill.execute(
            actionID: "info.currency_convert",
            input: AtlasToolInput(argumentsJSON: #"{"amount":200,"fromCurrency":"usd","toCurrency":"eur"}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(InfoCurrencyConvertOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.fromCurrency, "USD")
        XCTAssertEqual(decoded.toCurrency, "EUR")
        XCTAssertEqual(decoded.exchangeRate, 0.92, accuracy: 0.0001)
        XCTAssertEqual(decoded.convertedAmount, 184, accuracy: 0.001)
        XCTAssertEqual(decoded.providerName, "Mock Rates")
    }

    func testCurrencyConvertRejectsInvalidCurrencyCodes() async throws {
        let skill = makeSkill()

        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "info.currency_convert",
                input: AtlasToolInput(argumentsJSON: #"{"amount":100,"fromCurrency":"usd","toCurrency":"zzz"}"#),
                context: makeContext()
            )
        )
    }

    private func makeSkill() -> InfoSkill {
        InfoSkill(
            timeProvider: FoundationTimeInfoProvider(),
            currencyProvider: MockCurrencyProvider(),
            localeContextResolver: MockLocaleContextResolver(),
            locationResolver: MockLocationResolver()
        )
    }

    private func makeContext(memories: [MemoryItem] = []) -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] },
            memoryItemsProvider: { memories }
        )
    }
}

private struct MockLocationResolver: LocationResolving {
    func resolve(_ query: String) throws -> InfoResolvedLocation {
        switch query.lowercased() {
        case "tokyo", "japan":
            return InfoResolvedLocation(resolvedLocationName: "Tokyo, Japan", countryCode: "JP", timezoneID: "Asia/Tokyo")
        case "dubai":
            return InfoResolvedLocation(resolvedLocationName: "Dubai, United Arab Emirates", countryCode: "AE", timezoneID: "Asia/Dubai")
        default:
            throw InfoError.unresolvedLocation(query)
        }
    }
}

private struct MockLocaleContextResolver: LocaleContextResolving {
    func currentTimeZoneResolution() -> InfoTimeZoneResolution {
        InfoTimeZoneResolution(
            resolvedLocationName: "Orlando, United States",
            timeZone: TimeZone(identifier: "America/New_York")!
        )
    }

    func currentCountryCode() -> String? {
        "US"
    }
}

private actor MockCurrencyProvider: CurrencyProviding {
    nonisolated let providerName = "Mock Rates"

    nonisolated func validateProvider() -> CurrencyProviderValidation {
        CurrencyProviderValidation(
            isAvailable: true,
            summary: "Mock rates are configured."
        )
    }

    func convert(amount: Double, from fromCurrency: String, to toCurrency: String) async throws -> CurrencyConversionQuote {
        guard fromCurrency == "USD", toCurrency == "EUR" else {
            throw InfoError.invalidCurrencyCode(toCurrency)
        }

        return CurrencyConversionQuote(
            originalAmount: amount,
            convertedAmount: amount * 0.92,
            exchangeRate: 0.92,
            timestamp: Date(timeIntervalSince1970: 1_710_000_000)
        )
    }
}

private func XCTAssertThrowsErrorAsync(
    _ expression: @autoclosure () async throws -> some Any,
    file: StaticString = #filePath,
    line: UInt = #line
) async {
    do {
        _ = try await expression()
        XCTFail("Expected error to be thrown.", file: file, line: line)
    } catch {
    }
}
