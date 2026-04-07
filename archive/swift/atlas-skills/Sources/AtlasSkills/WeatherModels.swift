import Foundation

public enum WeatherTemperatureUnit: String, Codable, CaseIterable, Hashable, Sendable {
    case celsius
    case fahrenheit

    static func parse(_ value: String?) throws -> Self? {
        guard let value, !value.isEmpty else {
            return nil
        }

        guard let unit = WeatherTemperatureUnit(rawValue: value.lowercased()) else {
            throw WeatherError.unsupportedTemperatureUnit(value)
        }

        return unit
    }

    init(rawValueOrDefault value: String?) throws {
        guard let value, !value.isEmpty else {
            self = .celsius
            return
        }

        guard let unit = WeatherTemperatureUnit(rawValue: value.lowercased()) else {
            throw WeatherError.unsupportedTemperatureUnit(value)
        }

        self = unit
    }
}

public enum WeatherTemperatureUnitSource: String, Codable, CaseIterable, Hashable, Sendable {
    case explicit
    case resolvedLocation
    case infoContext
    case fallback
}

public enum WeatherWindSpeedUnit: String, Codable, CaseIterable, Hashable, Sendable {
    case kmh
    case mph
    case ms
    case kn

    init(rawValueOrDefault value: String?) throws {
        guard let value, !value.isEmpty else {
            self = .kmh
            return
        }

        guard let unit = WeatherWindSpeedUnit(rawValue: value.lowercased()) else {
            throw WeatherError.unsupportedWindSpeedUnit(value)
        }

        self = unit
    }
}

public struct WeatherCurrentInput: Codable, Hashable, Sendable {
    public let locationQuery: String?
    public let latitude: Double?
    public let longitude: Double?
    public let temperatureUnit: String?
    public let windSpeedUnit: String?

    public init(
        locationQuery: String? = nil,
        latitude: Double? = nil,
        longitude: Double? = nil,
        temperatureUnit: String? = nil,
        windSpeedUnit: String? = nil
    ) {
        self.locationQuery = locationQuery
        self.latitude = latitude
        self.longitude = longitude
        self.temperatureUnit = temperatureUnit
        self.windSpeedUnit = windSpeedUnit
    }
}

public struct WeatherForecastInput: Codable, Hashable, Sendable {
    public let locationQuery: String?
    public let latitude: Double?
    public let longitude: Double?
    public let days: Int?
    public let temperatureUnit: String?
    public let windSpeedUnit: String?

    public init(
        locationQuery: String? = nil,
        latitude: Double? = nil,
        longitude: Double? = nil,
        days: Int? = nil,
        temperatureUnit: String? = nil,
        windSpeedUnit: String? = nil
    ) {
        self.locationQuery = locationQuery
        self.latitude = latitude
        self.longitude = longitude
        self.days = days
        self.temperatureUnit = temperatureUnit
        self.windSpeedUnit = windSpeedUnit
    }
}

public struct WeatherHourlyInput: Codable, Hashable, Sendable {
    public let locationQuery: String?
    public let latitude: Double?
    public let longitude: Double?
    public let hours: Int?
    public let temperatureUnit: String?
    public let windSpeedUnit: String?

    public init(
        locationQuery: String? = nil,
        latitude: Double? = nil,
        longitude: Double? = nil,
        hours: Int? = nil,
        temperatureUnit: String? = nil,
        windSpeedUnit: String? = nil
    ) {
        self.locationQuery = locationQuery
        self.latitude = latitude
        self.longitude = longitude
        self.hours = hours
        self.temperatureUnit = temperatureUnit
        self.windSpeedUnit = windSpeedUnit
    }
}

public struct WeatherBriefInput: Codable, Hashable, Sendable {
    public let locationQuery: String?
    public let latitude: Double?
    public let longitude: Double?
    public let temperatureUnit: String?
    public let windSpeedUnit: String?

    public init(
        locationQuery: String? = nil,
        latitude: Double? = nil,
        longitude: Double? = nil,
        temperatureUnit: String? = nil,
        windSpeedUnit: String? = nil
    ) {
        self.locationQuery = locationQuery
        self.latitude = latitude
        self.longitude = longitude
        self.temperatureUnit = temperatureUnit
        self.windSpeedUnit = windSpeedUnit
    }
}

public struct WeatherDayPlanInput: Codable, Hashable, Sendable {
    public let locationQuery: String?
    public let latitude: Double?
    public let longitude: Double?
    public let temperatureUnit: String?
    public let windSpeedUnit: String?

    public init(
        locationQuery: String? = nil,
        latitude: Double? = nil,
        longitude: Double? = nil,
        temperatureUnit: String? = nil,
        windSpeedUnit: String? = nil
    ) {
        self.locationQuery = locationQuery
        self.latitude = latitude
        self.longitude = longitude
        self.temperatureUnit = temperatureUnit
        self.windSpeedUnit = windSpeedUnit
    }
}

public enum WeatherActivityType: String, Codable, CaseIterable, Hashable, Sendable {
    case walk
    case run
    case themePark = "theme_park"
    case outdoorDining = "outdoor_dining"
    case golf
    case beach
    case photography
}

public struct WeatherActivityWindowInput: Codable, Hashable, Sendable {
    public let locationQuery: String?
    public let latitude: Double?
    public let longitude: Double?
    public let activityType: WeatherActivityType
    public let hours: Int?
    public let temperatureUnit: String?
    public let windSpeedUnit: String?

    public init(
        locationQuery: String? = nil,
        latitude: Double? = nil,
        longitude: Double? = nil,
        activityType: WeatherActivityType,
        hours: Int? = nil,
        temperatureUnit: String? = nil,
        windSpeedUnit: String? = nil
    ) {
        self.locationQuery = locationQuery
        self.latitude = latitude
        self.longitude = longitude
        self.activityType = activityType
        self.hours = hours
        self.temperatureUnit = temperatureUnit
        self.windSpeedUnit = windSpeedUnit
    }
}

public struct WeatherResolvedLocation: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let latitude: Double
    public let longitude: Double
    public let timezone: String?

    public init(
        resolvedLocationName: String,
        countryCode: String? = nil,
        latitude: Double,
        longitude: Double,
        timezone: String?
    ) {
        self.resolvedLocationName = resolvedLocationName
        self.countryCode = countryCode
        self.latitude = latitude
        self.longitude = longitude
        self.timezone = timezone
    }
}

public struct WeatherCurrentOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let latitude: Double
    public let longitude: Double
    public let timezone: String?
    public let observationTime: String
    public let temperature: Double
    public let apparentTemperature: Double?
    public let weatherCode: Int?
    public let condition: String
    public let windSpeed: Double
    public let windDirection: Double?
    public let isDay: Bool?
    public let requestedTemperatureUnit: WeatherTemperatureUnit?
    public let resolvedTemperatureUnit: WeatherTemperatureUnit
    public let temperatureUnitSource: WeatherTemperatureUnitSource
    public let windSpeedUnit: WeatherWindSpeedUnit
    public let headline: String
    public let summary: String
    public let comfortLevel: String
    public let outdoorScore: Int
    public let rainRisk: String
    public let heatRisk: String
    public let recommendations: [String]
    public let sourceProvider: String

    public init(
        resolvedLocationName: String,
        countryCode: String? = nil,
        latitude: Double,
        longitude: Double,
        timezone: String?,
        observationTime: String,
        temperature: Double,
        apparentTemperature: Double?,
        weatherCode: Int?,
        condition: String,
        windSpeed: Double,
        windDirection: Double?,
        isDay: Bool?,
        requestedTemperatureUnit: WeatherTemperatureUnit? = nil,
        resolvedTemperatureUnit: WeatherTemperatureUnit,
        temperatureUnitSource: WeatherTemperatureUnitSource,
        windSpeedUnit: WeatherWindSpeedUnit,
        headline: String,
        summary: String,
        comfortLevel: String,
        outdoorScore: Int,
        rainRisk: String,
        heatRisk: String,
        recommendations: [String],
        sourceProvider: String
    ) {
        self.resolvedLocationName = resolvedLocationName
        self.countryCode = countryCode
        self.latitude = latitude
        self.longitude = longitude
        self.timezone = timezone
        self.observationTime = observationTime
        self.temperature = temperature
        self.apparentTemperature = apparentTemperature
        self.weatherCode = weatherCode
        self.condition = condition
        self.windSpeed = windSpeed
        self.windDirection = windDirection
        self.isDay = isDay
        self.requestedTemperatureUnit = requestedTemperatureUnit
        self.resolvedTemperatureUnit = resolvedTemperatureUnit
        self.temperatureUnitSource = temperatureUnitSource
        self.windSpeedUnit = windSpeedUnit
        self.headline = headline
        self.summary = summary
        self.comfortLevel = comfortLevel
        self.outdoorScore = outdoorScore
        self.rainRisk = rainRisk
        self.heatRisk = heatRisk
        self.recommendations = recommendations
        self.sourceProvider = sourceProvider
    }
}

public struct DailyForecast: Codable, Hashable, Sendable {
    public let date: String
    public let weatherCode: Int?
    public let condition: String
    public let temperatureMax: Double
    public let temperatureMin: Double
    public let precipitationProbabilityMax: Double?
    public let windSpeedMax: Double?

    public init(
        date: String,
        weatherCode: Int?,
        condition: String,
        temperatureMax: Double,
        temperatureMin: Double,
        precipitationProbabilityMax: Double?,
        windSpeedMax: Double?
    ) {
        self.date = date
        self.weatherCode = weatherCode
        self.condition = condition
        self.temperatureMax = temperatureMax
        self.temperatureMin = temperatureMin
        self.precipitationProbabilityMax = precipitationProbabilityMax
        self.windSpeedMax = windSpeedMax
    }
}

public struct WeatherForecastOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let latitude: Double
    public let longitude: Double
    public let timezone: String?
    public let requestedTemperatureUnit: WeatherTemperatureUnit?
    public let resolvedTemperatureUnit: WeatherTemperatureUnit
    public let temperatureUnitSource: WeatherTemperatureUnitSource
    public let windSpeedUnit: WeatherWindSpeedUnit
    public let headline: String
    public let summary: String
    public let recommendations: [String]
    public let dailyForecasts: [DailyForecast]
    public let sourceProvider: String

    public init(
        resolvedLocationName: String,
        countryCode: String? = nil,
        latitude: Double,
        longitude: Double,
        timezone: String?,
        requestedTemperatureUnit: WeatherTemperatureUnit? = nil,
        resolvedTemperatureUnit: WeatherTemperatureUnit,
        temperatureUnitSource: WeatherTemperatureUnitSource,
        windSpeedUnit: WeatherWindSpeedUnit,
        headline: String,
        summary: String,
        recommendations: [String],
        dailyForecasts: [DailyForecast],
        sourceProvider: String
    ) {
        self.resolvedLocationName = resolvedLocationName
        self.countryCode = countryCode
        self.latitude = latitude
        self.longitude = longitude
        self.timezone = timezone
        self.requestedTemperatureUnit = requestedTemperatureUnit
        self.resolvedTemperatureUnit = resolvedTemperatureUnit
        self.temperatureUnitSource = temperatureUnitSource
        self.windSpeedUnit = windSpeedUnit
        self.headline = headline
        self.summary = summary
        self.recommendations = recommendations
        self.dailyForecasts = dailyForecasts
        self.sourceProvider = sourceProvider
    }
}

public struct HourlyForecast: Codable, Hashable, Sendable {
    public let time: String
    public let temperature: Double
    public let apparentTemperature: Double?
    public let weatherCode: Int?
    public let condition: String
    public let precipitationProbability: Double?
    public let windSpeed: Double?
    public let isDay: Bool?
}

public struct WeatherHourlyOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let latitude: Double
    public let longitude: Double
    public let timezone: String?
    public let requestedTemperatureUnit: WeatherTemperatureUnit?
    public let resolvedTemperatureUnit: WeatherTemperatureUnit
    public let temperatureUnitSource: WeatherTemperatureUnitSource
    public let windSpeedUnit: WeatherWindSpeedUnit
    public let headline: String
    public let summary: String
    public let hourlyForecasts: [HourlyForecast]
    public let sourceProvider: String
}

public struct WeatherBriefOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let timezone: String?
    public let requestedTemperatureUnit: WeatherTemperatureUnit?
    public let resolvedTemperatureUnit: WeatherTemperatureUnit
    public let temperatureUnitSource: WeatherTemperatureUnitSource
    public let currentTemperature: Double
    public let currentCondition: String
    public let dayHigh: Double?
    public let dayLow: Double?
    public let headline: String
    public let summary: String
    public let recommendations: [String]
    public let outdoorScore: Int
    public let sourceProvider: String
}

public struct WeatherDayPlanSegment: Codable, Hashable, Sendable {
    public let label: String
    public let summary: String
    public let outdoorScore: Int
    public let rainRisk: String
}

public struct WeatherDayPlanOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let timezone: String?
    public let requestedTemperatureUnit: WeatherTemperatureUnit?
    public let resolvedTemperatureUnit: WeatherTemperatureUnit
    public let temperatureUnitSource: WeatherTemperatureUnitSource
    public let headline: String
    public let summary: String
    public let umbrellaRecommended: Bool
    public let hydrationGuidance: String
    public let outfitGuidance: String
    public let recommendations: [String]
    public let segments: [WeatherDayPlanSegment]
    public let sourceProvider: String
}

public struct WeatherActivityWindow: Codable, Hashable, Sendable {
    public let label: String
    public let startTime: String
    public let endTime: String
    public let score: Int
    public let summary: String
}

public struct WeatherActivityWindowOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let timezone: String?
    public let activityType: WeatherActivityType
    public let requestedTemperatureUnit: WeatherTemperatureUnit?
    public let resolvedTemperatureUnit: WeatherTemperatureUnit
    public let temperatureUnitSource: WeatherTemperatureUnitSource
    public let headline: String
    public let summary: String
    public let bestWindows: [WeatherActivityWindow]
    public let avoidWindows: [WeatherActivityWindow]
    public let recommendations: [String]
    public let sourceProvider: String
}

public struct WeatherProviderValidation: Hashable, Sendable {
    public let isAvailable: Bool
    public let summary: String
    public let issues: [String]

    public init(isAvailable: Bool, summary: String, issues: [String] = []) {
        self.isAvailable = isAvailable
        self.summary = summary
        self.issues = issues
    }
}

public struct WeatherRequestContext: Hashable, Sendable {
    public let requestedTemperatureUnit: WeatherTemperatureUnit?
    public let resolvedTemperatureUnit: WeatherTemperatureUnit
    public let temperatureUnitSource: WeatherTemperatureUnitSource
    public let windSpeedUnit: WeatherWindSpeedUnit

    public init(
        requestedTemperatureUnit: WeatherTemperatureUnit?,
        resolvedTemperatureUnit: WeatherTemperatureUnit,
        temperatureUnitSource: WeatherTemperatureUnitSource,
        windSpeedUnit: WeatherWindSpeedUnit
    ) {
        self.requestedTemperatureUnit = requestedTemperatureUnit
        self.resolvedTemperatureUnit = resolvedTemperatureUnit
        self.temperatureUnitSource = temperatureUnitSource
        self.windSpeedUnit = windSpeedUnit
    }

    public var temperatureUnit: WeatherTemperatureUnit {
        resolvedTemperatureUnit
    }
}

public enum WeatherError: LocalizedError, Hashable, Sendable {
    case missingLocation
    case invalidCoordinates
    case emptyLocationQuery
    case unresolvedLocation(String)
    case unsupportedTemperatureUnit(String)
    case unsupportedWindSpeedUnit(String)
    case invalidDayCount(Int)
    case providerFailure(String)
    case malformedResponse

    public var errorDescription: String? {
        switch self {
        case .missingLocation:
            return "Provide either a location query or latitude and longitude."
        case .invalidCoordinates:
            return "Latitude and longitude must both be valid coordinates."
        case .emptyLocationQuery:
            return "The weather location query cannot be empty."
        case .unresolvedLocation(let query):
            return "Atlas could not resolve '\(query)' to a weather location."
        case .unsupportedTemperatureUnit(let unit):
            return "The temperature unit '\(unit)' is not supported. Use celsius or fahrenheit."
        case .unsupportedWindSpeedUnit(let unit):
            return "The wind speed unit '\(unit)' is not supported. Use kmh, mph, ms, or kn."
        case .invalidDayCount(let days):
            return "The forecast day count '\(days)' is invalid. Use a value between 1 and 7."
        case .providerFailure(let details):
            return details
        case .malformedResponse:
            return "The weather provider returned an unexpected response."
        }
    }
}

public protocol WeatherProviding: Sendable {
    var providerName: String { get }
    func validateProvider() -> WeatherProviderValidation
    func resolveLocation(query: String) async throws -> WeatherResolvedLocation
    func currentWeather(
        for location: WeatherResolvedLocation,
        context: WeatherRequestContext
    ) async throws -> WeatherCurrentOutput
    func forecast(
        for location: WeatherResolvedLocation,
        days: Int,
        context: WeatherRequestContext
    ) async throws -> WeatherForecastOutput
    func hourlyForecast(
        for location: WeatherResolvedLocation,
        hours: Int,
        context: WeatherRequestContext
    ) async throws -> WeatherHourlyOutput
}
