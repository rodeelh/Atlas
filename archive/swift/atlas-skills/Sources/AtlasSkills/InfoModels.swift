import Foundation

public struct InfoCurrentTimeInput: Codable, Hashable, Sendable {
    public let locationQuery: String?
    public let timezoneID: String?

    public init(locationQuery: String? = nil, timezoneID: String? = nil) {
        self.locationQuery = locationQuery
        self.timezoneID = timezoneID
    }
}

public struct InfoCurrentDateInput: Codable, Hashable, Sendable {
    public let locationQuery: String?
    public let timezoneID: String?

    public init(locationQuery: String? = nil, timezoneID: String? = nil) {
        self.locationQuery = locationQuery
        self.timezoneID = timezoneID
    }
}

public struct InfoTimezoneConvertInput: Codable, Hashable, Sendable {
    public let sourceTime: String
    public let sourceTimezoneID: String?
    public let destinationTimezoneID: String?
    public let destinationLocationQuery: String?

    public init(
        sourceTime: String,
        sourceTimezoneID: String? = nil,
        destinationTimezoneID: String? = nil,
        destinationLocationQuery: String? = nil
    ) {
        self.sourceTime = sourceTime
        self.sourceTimezoneID = sourceTimezoneID
        self.destinationTimezoneID = destinationTimezoneID
        self.destinationLocationQuery = destinationLocationQuery
    }
}

public struct InfoCurrencyForLocationInput: Codable, Hashable, Sendable {
    public let locationQuery: String

    public init(locationQuery: String) {
        self.locationQuery = locationQuery
    }
}

public struct InfoCurrencyConvertInput: Codable, Hashable, Sendable {
    public let amount: Double
    public let fromCurrency: String
    public let toCurrency: String

    public init(amount: Double, fromCurrency: String, toCurrency: String) {
        self.amount = amount
        self.fromCurrency = fromCurrency
        self.toCurrency = toCurrency
    }
}

public struct InfoCurrentTimeOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String?
    public let timezoneID: String
    public let utcOffset: String
    public let formattedTime: String
    public let isoTimestamp: String
}

public struct InfoCurrentDateOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String?
    public let timezoneID: String
    public let formattedDate: String
    public let weekday: String
    public let isoDate: String
}

public struct InfoTimezoneConversionOutput: Codable, Hashable, Sendable {
    public let sourceTimezoneID: String
    public let destinationTimezoneID: String
    public let originalTime: String
    public let convertedTime: String
    public let formattedSummary: String
}

public struct InfoCurrencyForLocationOutput: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let currencyCode: String
    public let currencyName: String?
    public let currencySymbol: String?
}

public struct InfoCurrencyConvertOutput: Codable, Hashable, Sendable {
    public let originalAmount: Double
    public let fromCurrency: String
    public let toCurrency: String
    public let convertedAmount: Double
    public let exchangeRate: Double
    public let providerName: String
    public let timestamp: String
}

public struct InfoResolvedLocation: Codable, Hashable, Sendable {
    public let resolvedLocationName: String
    public let countryCode: String?
    public let timezoneID: String?

    public init(
        resolvedLocationName: String,
        countryCode: String?,
        timezoneID: String?
    ) {
        self.resolvedLocationName = resolvedLocationName
        self.countryCode = countryCode
        self.timezoneID = timezoneID
    }
}

public struct InfoTimeZoneResolution: Hashable, Sendable {
    public let resolvedLocationName: String?
    public let timeZone: TimeZone

    public init(resolvedLocationName: String?, timeZone: TimeZone) {
        self.resolvedLocationName = resolvedLocationName
        self.timeZone = timeZone
    }
}

public struct CurrencyMetadata: Codable, Hashable, Sendable {
    public let currencyCode: String
    public let currencyName: String?
    public let currencySymbol: String?

    public init(currencyCode: String, currencyName: String?, currencySymbol: String?) {
        self.currencyCode = currencyCode
        self.currencyName = currencyName
        self.currencySymbol = currencySymbol
    }
}

public struct CurrencyConversionQuote: Hashable, Sendable {
    public let originalAmount: Double
    public let convertedAmount: Double
    public let exchangeRate: Double
    public let timestamp: Date

    public init(
        originalAmount: Double,
        convertedAmount: Double,
        exchangeRate: Double,
        timestamp: Date
    ) {
        self.originalAmount = originalAmount
        self.convertedAmount = convertedAmount
        self.exchangeRate = exchangeRate
        self.timestamp = timestamp
    }
}

public struct TimeInfoProviderValidation: Hashable, Sendable {
    public let isAvailable: Bool
    public let summary: String
    public let issues: [String]

    public init(isAvailable: Bool, summary: String, issues: [String] = []) {
        self.isAvailable = isAvailable
        self.summary = summary
        self.issues = issues
    }
}

public struct CurrencyProviderValidation: Hashable, Sendable {
    public let isAvailable: Bool
    public let summary: String
    public let issues: [String]

    public init(isAvailable: Bool, summary: String, issues: [String] = []) {
        self.isAvailable = isAvailable
        self.summary = summary
        self.issues = issues
    }
}

public enum InfoError: LocalizedError, Hashable, Sendable {
    case missingLocation
    case missingDestination
    case emptyLocationQuery
    case unresolvedLocation(String)
    case ambiguousLocation(String)
    case invalidTimezone(String)
    case invalidTime(String)
    case invalidAmount(Double)
    case invalidCurrencyCode(String)
    case conversionUnavailable(String)
    case providerFailure(String)

    public var errorDescription: String? {
        switch self {
        case .missingLocation:
            return "Provide a location or timezone, or let Atlas use the local system timezone."
        case .missingDestination:
            return "Provide a destination timezone or destination location."
        case .emptyLocationQuery:
            return "The location query cannot be empty."
        case .unresolvedLocation(let query):
            return "Atlas could not resolve '\(query)' to a supported location."
        case .ambiguousLocation(let query):
            return "The location '\(query)' is too broad for an exact timezone. Try a city or explicit timezone identifier."
        case .invalidTimezone(let timezoneID):
            return "The timezone '\(timezoneID)' is not recognized."
        case .invalidTime(let sourceTime):
            return "Atlas could not parse '\(sourceTime)' as a time."
        case .invalidAmount(let amount):
            return "The amount '\(amount)' is invalid."
        case .invalidCurrencyCode(let code):
            return "The currency code '\(code)' is not supported."
        case .conversionUnavailable(let details):
            return details
        case .providerFailure(let details):
            return details
        }
    }
}
