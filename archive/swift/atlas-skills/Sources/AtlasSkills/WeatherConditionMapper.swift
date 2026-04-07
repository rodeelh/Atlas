struct WeatherConditionMapper: Sendable {
    func label(for code: Int?) -> String {
        guard let code else {
            return "unknown"
        }

        switch code {
        case 0:
            return "clear"
        case 1, 2:
            return "partly cloudy"
        case 3:
            return "cloudy"
        case 45, 48:
            return "fog"
        case 51, 53, 55, 56, 57, 61, 63, 65, 66, 67, 80, 81, 82:
            return "rain"
        case 71, 73, 75, 77, 85, 86:
            return "snow"
        case 95, 96, 99:
            return "thunderstorm"
        default:
            return "unknown"
        }
    }
}
