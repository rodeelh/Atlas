import XCTest
@testable import AtlasSkills
import AtlasGuard
import AtlasLogging
import AtlasShared

final class WeatherSkillTests: XCTestCase {
    func testOpenMeteoGeocodingCandidatesIncludeCityFallback() {
        XCTAssertEqual(
            OpenMeteoWeatherProvider.geocodingQueryCandidates(for: "Orlando, FL"),
            ["Orlando, FL", "Orlando"]
        )
    }

    func testWeatherCurrentReturnsNormalizedOutput() async throws {
        let skill = WeatherSkill(provider: MockWeatherProvider())

        let result = try await skill.execute(
            actionID: "weather.current",
            input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Boston","temperatureUnit":"fahrenheit","windSpeedUnit":"mph"}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WeatherCurrentOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.resolvedLocationName, "Boston, Massachusetts, United States")
        XCTAssertEqual(decoded.condition, "partly cloudy")
        XCTAssertEqual(decoded.resolvedTemperatureUnit, .fahrenheit)
        XCTAssertEqual(decoded.sourceProvider, "Mock Open-Meteo")
    }

    func testWeatherCurrentSupportsDirectCoordinatesWithoutGeocoding() async throws {
        let provider = MockWeatherProvider()
        let skill = WeatherSkill(provider: provider)

        let result = try await skill.execute(
            actionID: "weather.current",
            input: AtlasToolInput(argumentsJSON: #"{"latitude":42.3601,"longitude":-71.0589}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WeatherCurrentOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.resolvedLocationName, "42.3601, -71.0589")
        let resolveCount = await provider.locationResolutionCount()
        XCTAssertEqual(resolveCount, 0)
    }

    func testWeatherCurrentInfersFahrenheitFromLocalContext() async throws {
        let skill = WeatherSkill(
            provider: MockWeatherProvider(countryCode: nil),
            localeContextResolver: MockLocaleContextResolver(countryCode: "US")
        )

        let result = try await skill.execute(
            actionID: "weather.current",
            input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Boston"}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WeatherCurrentOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.resolvedTemperatureUnit, .fahrenheit)
        XCTAssertEqual(decoded.temperatureUnitSource, .infoContext)
    }

    func testWeatherForecastReturnsStructuredSummary() async throws {
        let skill = WeatherSkill(provider: MockWeatherProvider())

        let result = try await skill.execute(
            actionID: "weather.forecast",
            input: AtlasToolInput(argumentsJSON: #"{"latitude":42.3601,"longitude":-71.0589,"days":2}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WeatherForecastOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.dailyForecasts.count, 2)
        XCTAssertEqual(decoded.dailyForecasts.first?.condition, "clear")
        XCTAssertFalse(decoded.summary.isEmpty)
        XCTAssertEqual(decoded.sourceProvider, "Mock Open-Meteo")
    }

    func testWeatherBriefReturnsRecommendations() async throws {
        let skill = WeatherSkill(provider: MockWeatherProvider())

        let result = try await skill.execute(
            actionID: "weather.brief",
            input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Boston"}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WeatherBriefOutput.self, from: Data(result.output.utf8))
        XCTAssertFalse(decoded.headline.isEmpty)
        XCTAssertFalse(decoded.summary.isEmpty)
        XCTAssertFalse(decoded.recommendations.isEmpty)
    }

    func testWeatherHourlyReturnsRequestedHourCount() async throws {
        let skill = WeatherSkill(provider: MockWeatherProvider())

        let result = try await skill.execute(
            actionID: "weather.hourly",
            input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Boston","hours":4}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WeatherHourlyOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(decoded.hourlyForecasts.count, 4)
    }

    func testWeatherActivityWindowReturnsBestWindows() async throws {
        let skill = WeatherSkill(provider: MockWeatherProvider())

        let result = try await skill.execute(
            actionID: "weather.activity_window",
            input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Boston","activityType":"walk","hours":6}"#),
            context: makeContext()
        )

        let decoded = try AtlasJSON.decoder.decode(WeatherActivityWindowOutput.self, from: Data(result.output.utf8))
        XCTAssertFalse(decoded.bestWindows.isEmpty)
        XCTAssertFalse(decoded.recommendations.isEmpty)
    }

    func testWeatherRejectsMissingLocationInvalidUnitsAndOutOfRangeDays() async throws {
        let skill = WeatherSkill(provider: MockWeatherProvider())

        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "weather.current",
                input: AtlasToolInput(argumentsJSON: "{}"),
                context: makeContext()
            )
        )

        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "weather.forecast",
                input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Boston","temperatureUnit":"kelvin"}"#),
                context: makeContext()
            )
        )

        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "weather.forecast",
                input: AtlasToolInput(argumentsJSON: #"{"locationQuery":"Boston","days":8}"#),
                context: makeContext()
            )
        )
    }

    func testWeatherConditionMapperProducesExpectedLabels() {
        let mapper = WeatherConditionMapper()

        XCTAssertEqual(mapper.label(for: 0), "clear")
        XCTAssertEqual(mapper.label(for: 2), "partly cloudy")
        XCTAssertEqual(mapper.label(for: 3), "cloudy")
        XCTAssertEqual(mapper.label(for: 45), "fog")
        XCTAssertEqual(mapper.label(for: 61), "rain")
        XCTAssertEqual(mapper.label(for: 71), "snow")
        XCTAssertEqual(mapper.label(for: 95), "thunderstorm")
        XCTAssertEqual(mapper.label(for: 999), "unknown")
    }

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }
}

private actor MockWeatherProvider: WeatherProviding {
    nonisolated let providerName = "Mock Open-Meteo"
    private let countryCode: String?
    private(set) var resolveLocationCallCount = 0

    init(countryCode: String? = "US") {
        self.countryCode = countryCode
    }

    nonisolated func validateProvider() -> WeatherProviderValidation {
        WeatherProviderValidation(
            isAvailable: true,
            summary: "Mock weather provider is configured."
        )
    }

    func resolveLocation(query: String) async throws -> WeatherResolvedLocation {
        resolveLocationCallCount += 1
        return WeatherResolvedLocation(
            resolvedLocationName: "Boston, Massachusetts, United States",
            countryCode: countryCode,
            latitude: 42.3601,
            longitude: -71.0589,
            timezone: "America/New_York"
        )
    }

    func locationResolutionCount() -> Int {
        resolveLocationCallCount
    }

    func currentWeather(
        for location: WeatherResolvedLocation,
        context: WeatherRequestContext
    ) async throws -> WeatherCurrentOutput {
        WeatherCurrentOutput(
            resolvedLocationName: location.resolvedLocationName,
            countryCode: location.countryCode,
            latitude: location.latitude,
            longitude: location.longitude,
            timezone: location.timezone,
            observationTime: "2026-03-19T08:00",
            temperature: context.temperatureUnit == .fahrenheit ? 48.5 : 9.2,
            apparentTemperature: context.temperatureUnit == .fahrenheit ? 45.0 : 7.2,
            weatherCode: 2,
            condition: "partly cloudy",
            windSpeed: context.windSpeedUnit == .mph ? 7.0 : 11.3,
            windDirection: 220,
            isDay: true,
            requestedTemperatureUnit: context.requestedTemperatureUnit,
            resolvedTemperatureUnit: context.resolvedTemperatureUnit,
            temperatureUnitSource: context.temperatureUnitSource,
            windSpeedUnit: context.windSpeedUnit,
            headline: "",
            summary: "",
            comfortLevel: "",
            outdoorScore: 0,
            rainRisk: "",
            heatRisk: "",
            recommendations: [],
            sourceProvider: providerName
        )
    }

    func forecast(
        for location: WeatherResolvedLocation,
        days: Int,
        context: WeatherRequestContext
    ) async throws -> WeatherForecastOutput {
        var forecasts: [DailyForecast] = []
        forecasts.reserveCapacity(days)
        for offset in 0..<days {
            let date = "2026-03-\(String(format: "%02d", 19 + offset))"
            let isFirstDay = offset == 0
            let temperatureMax = context.temperatureUnit == .fahrenheit ? Double(54 + offset) : Double(12 + offset)
            let temperatureMin = context.temperatureUnit == .fahrenheit ? Double(38 + offset) : Double(3 + offset)
            let windSpeedMax = context.windSpeedUnit == .mph ? Double(9 + offset) : Double(14 + offset)

            forecasts.append(
                DailyForecast(
                    date: date,
                    weatherCode: isFirstDay ? 0 : 61,
                    condition: isFirstDay ? "clear" : "rain",
                    temperatureMax: temperatureMax,
                    temperatureMin: temperatureMin,
                    precipitationProbabilityMax: isFirstDay ? 10 : 65,
                    windSpeedMax: windSpeedMax
                )
            )
        }

        return WeatherForecastOutput(
            resolvedLocationName: location.resolvedLocationName,
            countryCode: location.countryCode,
            latitude: location.latitude,
            longitude: location.longitude,
            timezone: location.timezone,
            requestedTemperatureUnit: context.requestedTemperatureUnit,
            resolvedTemperatureUnit: context.resolvedTemperatureUnit,
            temperatureUnitSource: context.temperatureUnitSource,
            windSpeedUnit: context.windSpeedUnit,
            headline: "",
            summary: "",
            recommendations: [],
            dailyForecasts: forecasts,
            sourceProvider: providerName
        )
    }

    func hourlyForecast(
        for location: WeatherResolvedLocation,
        hours: Int,
        context: WeatherRequestContext
    ) async throws -> WeatherHourlyOutput {
        var forecasts: [HourlyForecast] = []
        forecasts.reserveCapacity(hours)
        for offset in 0..<hours {
            let time = "2026-03-19T\(String(format: "%02d", 8 + offset)):00"
            let temperature = context.temperatureUnit == .fahrenheit ? Double(48 + offset) : Double(9 + offset)
            let apparentTemperature = context.temperatureUnit == .fahrenheit ? Double(46 + offset) : Double(8 + offset)
            let windSpeed = context.windSpeedUnit == .mph ? Double(7 + offset) : Double(11 + offset)

            forecasts.append(
                HourlyForecast(
                    time: time,
                    temperature: temperature,
                    apparentTemperature: apparentTemperature,
                    weatherCode: offset < 2 ? 1 : 61,
                    condition: offset < 2 ? "partly cloudy" : "rain",
                    precipitationProbability: offset < 2 ? 15 : 65,
                    windSpeed: windSpeed,
                    isDay: true
                )
            )
        }

        return WeatherHourlyOutput(
            resolvedLocationName: location.resolvedLocationName,
            countryCode: location.countryCode,
            latitude: location.latitude,
            longitude: location.longitude,
            timezone: location.timezone,
            requestedTemperatureUnit: context.requestedTemperatureUnit,
            resolvedTemperatureUnit: context.resolvedTemperatureUnit,
            temperatureUnitSource: context.temperatureUnitSource,
            windSpeedUnit: context.windSpeedUnit,
            headline: "",
            summary: "",
            hourlyForecasts: forecasts,
            sourceProvider: providerName
        )
    }
}

private struct MockLocaleContextResolver: LocaleContextResolving {
    let countryCode: String?

    func currentTimeZoneResolution() -> InfoTimeZoneResolution {
        InfoTimeZoneResolution(resolvedLocationName: "Boston, United States", timeZone: TimeZone(identifier: "America/New_York")!)
    }

    func currentCountryCode() -> String? {
        countryCode
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
