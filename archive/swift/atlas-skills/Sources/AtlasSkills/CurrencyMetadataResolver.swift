import Foundation

public struct CurrencyMetadataResolver: Sendable {
    public init() {}

    public func metadata(forCountryCode countryCode: String) -> CurrencyMetadata? {
        let normalizedCode = countryCode.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
        guard normalizedCode.count == 2 else { return nil }

        let matchingLocale = Locale.availableIdentifiers.lazy
            .map(Locale.init(identifier:))
            .first { locale in
                locale.region.map(\.identifier)?.uppercased() == normalizedCode
                    && locale.currency.map(\.identifier) != nil
            }

        guard let locale = matchingLocale, let currencyCode = locale.currency.map(\.identifier) else {
            return nil
        }

        return CurrencyMetadata(
            currencyCode: currencyCode.uppercased(),
            currencyName: Locale.autoupdatingCurrent.localizedString(forCurrencyCode: currencyCode),
            currencySymbol: locale.currencySymbol
        )
    }

    public func metadata(forCurrencyCode currencyCode: String) -> CurrencyMetadata? {
        let normalizedCode = normalizeCurrencyCode(currencyCode)
        guard Locale.commonISOCurrencyCodes.contains(normalizedCode) else {
            return nil
        }

        let locale = Locale.availableIdentifiers.lazy
            .map(Locale.init(identifier:))
            .first { $0.currency.map(\.identifier)?.uppercased() == normalizedCode }

        return CurrencyMetadata(
            currencyCode: normalizedCode,
            currencyName: Locale.autoupdatingCurrent.localizedString(forCurrencyCode: normalizedCode),
            currencySymbol: locale?.currencySymbol
        )
    }

    public func normalizeCurrencyCode(_ value: String) -> String {
        value.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
    }
}
