import Foundation
import AtlasShared

public actor OpenMeteoWeatherProvider: WeatherProviding {
    public nonisolated let providerName = "Open-Meteo"

    private let session: URLSession
    private let geocodingBaseURL: URL
    private let forecastBaseURL: URL
    private let maxResponseBytes: Int

    public init(
        timeoutSeconds: TimeInterval = 12,
        maxResponseBytes: Int = 500_000,
        geocodingBaseURL: URL = URL(string: "https://geocoding-api.open-meteo.com/v1/search")!,
        forecastBaseURL: URL = URL(string: "https://api.open-meteo.com/v1/forecast")!
    ) {
        self.geocodingBaseURL = geocodingBaseURL
        self.forecastBaseURL = forecastBaseURL
        self.maxResponseBytes = max(64_000, maxResponseBytes)

        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = timeoutSeconds
        configuration.timeoutIntervalForResource = timeoutSeconds
        configuration.waitsForConnectivity = false
        configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.urlCache = nil
        configuration.httpAdditionalHeaders = [
            "User-Agent": "AtlasWeather/1.0",
            "Accept": "application/json"
        ]

        self.session = URLSession(configuration: configuration)
    }

    public nonisolated func validateProvider() -> WeatherProviderValidation {
        WeatherProviderValidation(
            isAvailable: true,
            summary: "Open-Meteo geocoding and forecast endpoints are configured in read-only mode."
        )
    }

    public func resolveLocation(query: String) async throws -> WeatherResolvedLocation {
        let normalizedQuery = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !normalizedQuery.isEmpty else {
            throw WeatherError.emptyLocationQuery
        }
        for candidate in Self.geocodingQueryCandidates(for: normalizedQuery) {
            let response = try await performGeocodingRequest(query: candidate)
            if let result = response.results?.first {
                return WeatherResolvedLocation(
                    resolvedLocationName: resolvedLocationName(from: result),
                    countryCode: countryCode(fromCountryName: result.country),
                    latitude: result.latitude,
                    longitude: result.longitude,
                    timezone: result.timezone
                )
            }
        }

        throw WeatherError.unresolvedLocation(normalizedQuery)
    }

    public func currentWeather(
        for location: WeatherResolvedLocation,
        context: WeatherRequestContext
    ) async throws -> WeatherCurrentOutput {
        var components = URLComponents(url: forecastBaseURL, resolvingAgainstBaseURL: false)
        components?.queryItems = [
            URLQueryItem(name: "latitude", value: coordinateString(location.latitude)),
            URLQueryItem(name: "longitude", value: coordinateString(location.longitude)),
            URLQueryItem(name: "current", value: "temperature_2m,apparent_temperature,weather_code,wind_speed_10m,wind_direction_10m,is_day"),
            URLQueryItem(name: "temperature_unit", value: context.temperatureUnit.rawValue),
            URLQueryItem(name: "wind_speed_unit", value: context.windSpeedUnit.rawValue),
            URLQueryItem(name: "timezone", value: "auto")
        ]

        guard let url = components?.url else {
            throw WeatherError.providerFailure("Atlas could not construct the Open-Meteo current weather request.")
        }

        let response = try await performRequest(url: url, decodeAs: OpenMeteoForecastResponse.self)
        guard let current = response.current else {
            throw WeatherError.malformedResponse
        }

        return WeatherCurrentOutput(
            resolvedLocationName: location.resolvedLocationName,
            countryCode: location.countryCode,
            latitude: response.latitude,
            longitude: response.longitude,
            timezone: response.timezone ?? location.timezone,
            observationTime: current.time,
            temperature: current.temperature2M,
            apparentTemperature: current.apparentTemperature,
            weatherCode: current.weatherCode,
            condition: WeatherConditionMapper().label(for: current.weatherCode),
            windSpeed: current.windSpeed10M,
            windDirection: current.windDirection10M,
            isDay: current.isDay.map { $0 == 1 },
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

    public func forecast(
        for location: WeatherResolvedLocation,
        days: Int,
        context: WeatherRequestContext
    ) async throws -> WeatherForecastOutput {
        var components = URLComponents(url: forecastBaseURL, resolvingAgainstBaseURL: false)
        components?.queryItems = [
            URLQueryItem(name: "latitude", value: coordinateString(location.latitude)),
            URLQueryItem(name: "longitude", value: coordinateString(location.longitude)),
            URLQueryItem(name: "daily", value: "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,wind_speed_10m_max"),
            URLQueryItem(name: "forecast_days", value: "\(days)"),
            URLQueryItem(name: "temperature_unit", value: context.temperatureUnit.rawValue),
            URLQueryItem(name: "wind_speed_unit", value: context.windSpeedUnit.rawValue),
            URLQueryItem(name: "timezone", value: "auto")
        ]

        guard let url = components?.url else {
            throw WeatherError.providerFailure("Atlas could not construct the Open-Meteo forecast request.")
        }

        let response = try await performRequest(url: url, decodeAs: OpenMeteoForecastResponse.self)
        guard let daily = response.daily else {
            throw WeatherError.malformedResponse
        }

        let forecasts = zip(daily.time.indices, daily.time).map { index, date in
            DailyForecast(
                date: date,
                weatherCode: value(in: daily.weatherCode, at: index),
                condition: WeatherConditionMapper().label(for: value(in: daily.weatherCode, at: index)),
                temperatureMax: daily.temperature2MMax[index],
                temperatureMin: daily.temperature2MMin[index],
                precipitationProbabilityMax: value(in: daily.precipitationProbabilityMax, at: index),
                windSpeedMax: value(in: daily.windSpeed10MMax, at: index)
            )
        }

        return WeatherForecastOutput(
            resolvedLocationName: location.resolvedLocationName,
            countryCode: location.countryCode,
            latitude: response.latitude,
            longitude: response.longitude,
            timezone: response.timezone ?? location.timezone,
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

    public func hourlyForecast(
        for location: WeatherResolvedLocation,
        hours: Int,
        context: WeatherRequestContext
    ) async throws -> WeatherHourlyOutput {
        var components = URLComponents(url: forecastBaseURL, resolvingAgainstBaseURL: false)
        components?.queryItems = [
            URLQueryItem(name: "latitude", value: coordinateString(location.latitude)),
            URLQueryItem(name: "longitude", value: coordinateString(location.longitude)),
            URLQueryItem(name: "hourly", value: "temperature_2m,apparent_temperature,precipitation_probability,weather_code,wind_speed_10m,is_day"),
            URLQueryItem(name: "forecast_hours", value: "\(hours)"),
            URLQueryItem(name: "temperature_unit", value: context.temperatureUnit.rawValue),
            URLQueryItem(name: "wind_speed_unit", value: context.windSpeedUnit.rawValue),
            URLQueryItem(name: "timezone", value: "auto")
        ]

        guard let url = components?.url else {
            throw WeatherError.providerFailure("Atlas could not construct the Open-Meteo hourly weather request.")
        }

        let response = try await performRequest(url: url, decodeAs: OpenMeteoForecastResponse.self)
        guard let hourly = response.hourly else {
            throw WeatherError.malformedResponse
        }

        let forecasts = zip(hourly.time.indices, hourly.time).map { index, time in
            HourlyForecast(
                time: time,
                temperature: hourly.temperature2M[index],
                apparentTemperature: value(in: hourly.apparentTemperature, at: index),
                weatherCode: value(in: hourly.weatherCode, at: index),
                condition: WeatherConditionMapper().label(for: value(in: hourly.weatherCode, at: index)),
                precipitationProbability: value(in: hourly.precipitationProbability, at: index),
                windSpeed: value(in: hourly.windSpeed10M, at: index),
                isDay: value(in: hourly.isDay, at: index).map { $0 == 1 }
            )
        }

        return WeatherHourlyOutput(
            resolvedLocationName: location.resolvedLocationName,
            countryCode: location.countryCode,
            latitude: response.latitude,
            longitude: response.longitude,
            timezone: response.timezone ?? location.timezone,
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

    private func performRequest<Response: Decodable>(
        url: URL,
        decodeAs type: Response.Type
    ) async throws -> Response {
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw WeatherError.providerFailure("Atlas did not receive a valid Open-Meteo response.")
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            throw WeatherError.providerFailure("The Open-Meteo API returned status code \(httpResponse.statusCode).")
        }

        guard data.count <= maxResponseBytes else {
            throw WeatherError.providerFailure("The Open-Meteo response exceeded the safe size limit.")
        }

        do {
            return try AtlasJSON.decoder.decode(Response.self, from: data)
        } catch {
            throw WeatherError.providerFailure("Atlas could not decode the Open-Meteo response.")
        }
    }

    private func performGeocodingRequest(query: String) async throws -> OpenMeteoGeocodingResponse {
        var components = URLComponents(url: geocodingBaseURL, resolvingAgainstBaseURL: false)
        components?.queryItems = [
            URLQueryItem(name: "name", value: query),
            URLQueryItem(name: "count", value: "5"),
            URLQueryItem(name: "language", value: "en"),
            URLQueryItem(name: "format", value: "json")
        ]

        guard let url = components?.url else {
            throw WeatherError.providerFailure("Atlas could not construct the Open-Meteo geocoding request.")
        }

        return try await performRequest(url: url, decodeAs: OpenMeteoGeocodingResponse.self)
    }

    static func geocodingQueryCandidates(for query: String) -> [String] {
        let normalized = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard normalized.isEmpty == false else {
            return []
        }

        let commaSeparatedParts = normalized
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { $0.isEmpty == false }

        let cityOnly = commaSeparatedParts.first

        return [normalized, cityOnly]
            .compactMap { $0 }
            .reduce(into: [String]()) { candidates, candidate in
                if candidates.contains(candidate) == false {
                    candidates.append(candidate)
                }
            }
    }

    private func resolvedLocationName(from result: OpenMeteoGeocodingResult) -> String {
        [result.name, result.admin1, result.country]
            .compactMap { value -> String? in
                guard let value, !value.isEmpty else { return nil }
                return value
            }
            .reduce(into: [String]()) { values, part in
                if values.contains(part) == false {
                    values.append(part)
                }
            }
            .joined(separator: ", ")
    }

    private func countryCode(fromCountryName countryName: String?) -> String? {
        guard let countryName, countryName.isEmpty == false else {
            return nil
        }

        if countryName.count == 2 {
            return countryName.uppercased()
        }

        for region in Locale.Region.isoRegions {
            let code = region.identifier
            let localized = Locale.autoupdatingCurrent.localizedString(forRegionCode: code)
            if localized?.caseInsensitiveCompare(countryName) == .orderedSame {
                return code.uppercased()
            }
        }

        return nil
    }

    private func coordinateString(_ value: Double) -> String {
        String(format: "%.4f", value)
    }

    private func value<T>(in array: [T]?, at index: Int) -> T? {
        guard let array, array.indices.contains(index) else {
            return nil
        }
        return array[index]
    }
}
