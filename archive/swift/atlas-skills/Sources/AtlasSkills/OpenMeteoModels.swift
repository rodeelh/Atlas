import Foundation

struct OpenMeteoGeocodingResponse: Codable, Sendable {
    let results: [OpenMeteoGeocodingResult]?
}

struct OpenMeteoGeocodingResult: Codable, Sendable {
    let name: String
    let latitude: Double
    let longitude: Double
    let country: String?
    let admin1: String?
    let timezone: String?
}

struct OpenMeteoForecastResponse: Codable, Sendable {
    let latitude: Double
    let longitude: Double
    let timezone: String?
    let current: OpenMeteoCurrentWeatherDTO?
    let hourly: OpenMeteoHourlyForecastDTO?
    let daily: OpenMeteoDailyForecastDTO?
}

struct OpenMeteoCurrentWeatherDTO: Codable, Sendable {
    let time: String
    let temperature2M: Double
    let apparentTemperature: Double?
    let weatherCode: Int?
    let windSpeed10M: Double
    let windDirection10M: Double?
    let isDay: Int?

    enum CodingKeys: String, CodingKey {
        case time
        case temperature2M = "temperature_2m"
        case apparentTemperature = "apparent_temperature"
        case weatherCode = "weather_code"
        case windSpeed10M = "wind_speed_10m"
        case windDirection10M = "wind_direction_10m"
        case isDay = "is_day"
    }
}

struct OpenMeteoDailyForecastDTO: Codable, Sendable {
    let time: [String]
    let weatherCode: [Int]?
    let temperature2MMax: [Double]
    let temperature2MMin: [Double]
    let precipitationProbabilityMax: [Double]?
    let windSpeed10MMax: [Double]?

    enum CodingKeys: String, CodingKey {
        case time
        case weatherCode = "weather_code"
        case temperature2MMax = "temperature_2m_max"
        case temperature2MMin = "temperature_2m_min"
        case precipitationProbabilityMax = "precipitation_probability_max"
        case windSpeed10MMax = "wind_speed_10m_max"
    }
}

struct OpenMeteoHourlyForecastDTO: Codable, Sendable {
    let time: [String]
    let temperature2M: [Double]
    let apparentTemperature: [Double]?
    let precipitationProbability: [Double]?
    let weatherCode: [Int]?
    let windSpeed10M: [Double]?
    let isDay: [Int]?

    enum CodingKeys: String, CodingKey {
        case time
        case temperature2M = "temperature_2m"
        case apparentTemperature = "apparent_temperature"
        case precipitationProbability = "precipitation_probability"
        case weatherCode = "weather_code"
        case windSpeed10M = "wind_speed_10m"
        case isDay = "is_day"
    }
}
