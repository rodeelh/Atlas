import Foundation
import AtlasShared
import AtlasTools

public struct InfoSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let timeProvider: any TimeInfoProviding
    private let currencyProvider: any CurrencyProviding
    private let localeContextResolver: any LocaleContextResolving
    private let locationResolver: any LocationResolving
    private let currencyMetadataResolver: CurrencyMetadataResolver

    public init(
        timeProvider: (any TimeInfoProviding)? = nil,
        currencyProvider: (any CurrencyProviding)? = nil,
        localeContextResolver: (any LocaleContextResolving)? = nil,
        locationResolver: (any LocationResolving)? = nil,
        currencyMetadataResolver: CurrencyMetadataResolver = CurrencyMetadataResolver()
    ) {
        self.timeProvider = timeProvider ?? FoundationTimeInfoProvider()
        self.currencyProvider = currencyProvider ?? OpenExchangeRateCurrencyProvider()
        self.localeContextResolver = localeContextResolver ?? LocaleContextResolver()
        self.locationResolver = locationResolver ?? LocationResolver()
        self.currencyMetadataResolver = currencyMetadataResolver

        self.manifest = SkillManifest(
            id: "info",
            name: "Info",
            version: "1.0.0",
            description: "Time, date, timezone, and currency information.",
            category: .utility,
            lifecycleState: .installed,
            capabilities: [
                .currentTime,
                .currentDate,
                .timezoneConversion,
                .currencyLookup,
                .currencyConversion
            ],
            requiredPermissions: [
                .infoRead
            ],
            riskLevel: .low,
            trustProfile: .exactStructured,
            freshnessType: .live,
            preferredQueryTypes: [
                .currentTime,
                .currentDate,
                .timezoneConversion,
                .currencyLookup,
                .currencyConversion
            ],
            routingPriority: 85,
            canAnswerStructuredLiveData: true,
            restrictionsSummary: [
                "Native Foundation time and date handling",
                "Read-only utility actions",
                "Currency metadata resolved locally",
                "Currency conversion uses live exchange rates when available"
            ],
            supportsReadOnlyMode: true,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["time", "date", "timezone", "currency", "utility"],
            intent: .liveStructuredData,
            triggers: [
                .init("what time would that be", queryType: .timezoneConversion),
                .init("convert the time", queryType: .timezoneConversion),
                .init("convert time to", queryType: .timezoneConversion),
                .init("time zone", queryType: .timezoneConversion),
                .init("timezone", queryType: .timezoneConversion),
                .init("exchange rate", queryType: .currencyConversion),
                .init("convert usd", queryType: .currencyConversion),
                .init("convert eur", queryType: .currencyConversion),
                .init("convert gbp", queryType: .currencyConversion),
                .init("convert jpy", queryType: .currencyConversion),
                .init("what currency", queryType: .currencyLookup),
                .init("money do they use", queryType: .currencyLookup),
                .init("currency", queryType: .currencyLookup),
                .init("what day is it", queryType: .currentDate),
                .init("today's date", queryType: .currentDate),
                .init("what is the date", queryType: .currentDate),
                .init("what date is it", queryType: .currentDate),
                .init("what time is it", queryType: .currentTime),
                .init("current time", queryType: .currentTime),
                .init("time in ", queryType: .currentTime)
            ]
        )

        self.actions = [
            SkillActionDefinition(
                id: "info.current_time",
                name: "Current Time",
                description: "Return the current time for a timezone, location, or the local system context.",
                inputSchemaSummary: "locationQuery and timezoneID are optional; Atlas falls back to the local system timezone when needed.",
                outputSchemaSummary: "Resolved location, timezone, UTC offset, formatted time, and ISO timestamp.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.currentTime],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "Optional city, place, or country query."),
                        "timezoneID": AtlasToolInputProperty(type: "string", description: "Optional explicit timezone identifier such as Asia/Tokyo.")
                    ]
                )
            ),
            SkillActionDefinition(
                id: "info.current_date",
                name: "Current Date",
                description: "Return the current date for a timezone, location, or the local system context.",
                inputSchemaSummary: "locationQuery and timezoneID are optional; Atlas falls back to the local system timezone when needed.",
                outputSchemaSummary: "Resolved location, timezone, formatted date, weekday, and ISO date.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.currentDate],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "Optional city, place, or country query."),
                        "timezoneID": AtlasToolInputProperty(type: "string", description: "Optional explicit timezone identifier such as Asia/Tokyo.")
                    ]
                )
            ),
            SkillActionDefinition(
                id: "info.timezone_convert",
                name: "Timezone Convert",
                description: "Convert a clock time from one timezone to another timezone or destination location.",
                inputSchemaSummary: "sourceTime is required; sourceTimezoneID, destinationTimezoneID, and destinationLocationQuery are optional but enough timezone context must be provided.",
                outputSchemaSummary: "Source and destination timezones, normalized original time, converted time, and a summary sentence.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.timezoneConversion],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "sourceTime": AtlasToolInputProperty(type: "string", description: "Time such as 3 PM, 15:00, or 2026-03-19 15:00."),
                        "sourceTimezoneID": AtlasToolInputProperty(type: "string", description: "Optional explicit source timezone identifier."),
                        "destinationTimezoneID": AtlasToolInputProperty(type: "string", description: "Optional explicit destination timezone identifier."),
                        "destinationLocationQuery": AtlasToolInputProperty(type: "string", description: "Optional destination location to resolve into a timezone.")
                    ],
                    required: ["sourceTime"]
                )
            ),
            SkillActionDefinition(
                id: "info.currency_for_location",
                name: "Currency For Location",
                description: "Look up the currency used for a city, place, or country.",
                inputSchemaSummary: "locationQuery is required.",
                outputSchemaSummary: "Resolved place, country code, currency code, currency name, and symbol.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.currencyLookup],
                routingPriority: 40,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "City, place, or country name.")
                    ],
                    required: ["locationQuery"]
                )
            ),
            SkillActionDefinition(
                id: "info.currency_convert",
                name: "Currency Convert",
                description: "Convert an amount between ISO currency codes using a provider abstraction.",
                inputSchemaSummary: "amount, fromCurrency, and toCurrency are required.",
                outputSchemaSummary: "Original amount, normalized currencies, converted amount, exchange rate, provider, and timestamp.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.currencyConversion],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "amount": AtlasToolInputProperty(type: "number", description: "Amount to convert."),
                        "fromCurrency": AtlasToolInputProperty(type: "string", description: "Source ISO currency code such as USD."),
                        "toCurrency": AtlasToolInputProperty(type: "string", description: "Destination ISO currency code such as EUR.")
                    ],
                    required: ["amount", "fromCurrency", "toCurrency"]
                )
            )
        ]
    }

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        let timeValidation = timeProvider.validateProvider()
        let currencyValidation = currencyProvider.validateProvider()
        let localCountryCode = localeContextResolver.currentCountryCode()
        let metadataReady = localCountryCode.flatMap { currencyMetadataResolver.metadata(forCountryCode: $0) } != nil || localCountryCode == nil

        var issues = timeValidation.issues + currencyValidation.issues
        if !metadataReady {
            issues.append("Local currency metadata could not be inferred from the current system locale.")
        }

        let status: SkillValidationStatus
        if !timeValidation.isAvailable {
            status = .failed
        } else if currencyValidation.isAvailable && metadataReady {
            status = .passed
        } else {
            status = .warning
        }

        let summary: String
        switch status {
        case .passed:
            summary = "Native time/date handling and currency metadata are ready. Live conversion is available on demand."
        case .warning:
            summary = "Info is ready for time/date lookups. Currency conversion or local metadata may be limited until the provider responds."
        case .failed:
            summary = timeValidation.summary
        case .notValidated:
            summary = "Info has not been validated."
        }

        return SkillValidationResult(
            skillID: manifest.id,
            status: status,
            summary: summary,
            issues: issues,
            validatedAt: .now
        )
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "info.current_time":
            return try await currentTime(input: input, context: context)
        case "info.current_date":
            return try await currentDate(input: input, context: context)
        case "info.timezone_convert":
            return try await timezoneConvert(input: input, context: context)
        case "info.currency_for_location":
            return try await currencyForLocation(input: input, context: context)
        case "info.currency_convert":
            return try await currencyConvert(input: input, context: context)
        default:
            throw AtlasToolError.invalidInput("The action '\(actionID)' is not supported by Info.")
        }
    }

    private func currentTime(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(InfoCurrentTimeInput.self)
        let resolution = try await resolveTimeZone(
            locationQuery: payload.locationQuery,
            timezoneID: payload.timezoneID,
            context: context
        )
        let output = timeProvider.currentTime(in: resolution)

        context.logger.info("Executing Info current time request", metadata: [
            "skill_id": manifest.id,
            "action_id": "info.current_time",
            "location_query": summarize(payload.locationQuery ?? "local"),
            "timezone_id": output.timezoneID
        ])

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "info.current_time",
            output: try encode(output),
            summary: "Retrieved the current time for \(output.resolvedLocationName ?? output.timezoneID).",
            metadata: [
                "timezone_id": output.timezoneID,
                "utc_offset": output.utcOffset
            ]
        )
    }

    private func currentDate(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(InfoCurrentDateInput.self)
        let resolution = try await resolveTimeZone(
            locationQuery: payload.locationQuery,
            timezoneID: payload.timezoneID,
            context: context
        )
        let output = timeProvider.currentDate(in: resolution)

        context.logger.info("Executing Info current date request", metadata: [
            "skill_id": manifest.id,
            "action_id": "info.current_date",
            "location_query": summarize(payload.locationQuery ?? "local"),
            "timezone_id": output.timezoneID
        ])

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "info.current_date",
            output: try encode(output),
            summary: "Retrieved the current date for \(output.resolvedLocationName ?? output.timezoneID).",
            metadata: [
                "timezone_id": output.timezoneID,
                "weekday": output.weekday
            ]
        )
    }

    private func timezoneConvert(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(InfoTimezoneConvertInput.self)
        let source = try await resolveTimeZone(locationQuery: nil, timezoneID: payload.sourceTimezoneID, context: context)
        let destination = try await resolveDestinationTimeZone(
            timezoneID: payload.destinationTimezoneID,
            locationQuery: payload.destinationLocationQuery,
            context: context
        )
        let output = try timeProvider.convertTime(
            sourceTime: payload.sourceTime,
            source: source,
            destination: destination
        )

        context.logger.info("Executing Info timezone conversion request", metadata: [
            "skill_id": manifest.id,
            "action_id": "info.timezone_convert",
            "source_timezone": output.sourceTimezoneID,
            "destination_timezone": output.destinationTimezoneID
        ])

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "info.timezone_convert",
            output: try encode(output),
            summary: output.formattedSummary,
            metadata: [
                "source_timezone": output.sourceTimezoneID,
                "destination_timezone": output.destinationTimezoneID
            ]
        )
    }

    private func currencyForLocation(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(InfoCurrencyForLocationInput.self)
        let location = try locationResolver.resolve(payload.locationQuery)
        guard let countryCode = location.countryCode else {
            throw InfoError.unresolvedLocation(payload.locationQuery)
        }
        guard let metadata = currencyMetadataResolver.metadata(forCountryCode: countryCode) else {
            throw InfoError.providerFailure("Atlas could not resolve currency metadata for \(location.resolvedLocationName).")
        }

        let output = InfoCurrencyForLocationOutput(
            resolvedLocationName: location.resolvedLocationName,
            countryCode: countryCode,
            currencyCode: metadata.currencyCode,
            currencyName: metadata.currencyName,
            currencySymbol: metadata.currencySymbol
        )

        context.logger.info("Executing Info currency-for-location request", metadata: [
            "skill_id": manifest.id,
            "action_id": "info.currency_for_location",
            "location_query": summarize(payload.locationQuery),
            "currency_code": output.currencyCode
        ])

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "info.currency_for_location",
            output: try encode(output),
            summary: "Resolved \(output.currencyCode) for \(output.resolvedLocationName).",
            metadata: [
                "country_code": output.countryCode ?? "unknown",
                "currency_code": output.currencyCode
            ]
        )
    }

    private func currencyConvert(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(InfoCurrencyConvertInput.self)
        guard payload.amount.isFinite else {
            throw InfoError.invalidAmount(payload.amount)
        }

        let fromCode = try normalizedCurrencyCode(payload.fromCurrency)
        let toCode = try normalizedCurrencyCode(payload.toCurrency)
        let quote = try await currencyProvider.convert(amount: payload.amount, from: fromCode, to: toCode)
        let output = InfoCurrencyConvertOutput(
            originalAmount: payload.amount,
            fromCurrency: fromCode,
            toCurrency: toCode,
            convertedAmount: quote.convertedAmount,
            exchangeRate: quote.exchangeRate,
            providerName: currencyProvider.providerName,
            timestamp: ISO8601DateFormatter().string(from: quote.timestamp)
        )

        context.logger.info("Executing Info currency conversion request", metadata: [
            "skill_id": manifest.id,
            "action_id": "info.currency_convert",
            "from_currency": fromCode,
            "to_currency": toCode,
            "provider": currencyProvider.providerName
        ])

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "info.currency_convert",
            output: try encode(output),
            summary: "Converted \(formatAmount(payload.amount)) \(fromCode) to \(formatAmount(quote.convertedAmount)) \(toCode).",
            metadata: [
                "provider": currencyProvider.providerName,
                "exchange_rate": String(format: "%.6f", quote.exchangeRate)
            ]
        )
    }

    private func resolveTimeZone(
        locationQuery: String?,
        timezoneID: String?,
        context: SkillExecutionContext
    ) async throws -> InfoTimeZoneResolution {
        if let timezoneID, !timezoneID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            guard let timeZone = TimeZone(identifier: timezoneID) else {
                throw InfoError.invalidTimezone(timezoneID)
            }
            return InfoTimeZoneResolution(resolvedLocationName: nil, timeZone: timeZone)
        }

        if let locationQuery, !locationQuery.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            let resolved = try locationResolver.resolve(locationQuery)
            guard let timezoneID = resolved.timezoneID else {
                throw InfoError.ambiguousLocation(locationQuery)
            }
            guard let timeZone = TimeZone(identifier: timezoneID) else {
                throw InfoError.invalidTimezone(timezoneID)
            }
            return InfoTimeZoneResolution(
                resolvedLocationName: resolved.resolvedLocationName,
                timeZone: timeZone
            )
        }

        if let memoryResolution = try await preferredLocationResolution(from: context) {
            return memoryResolution
        }

        return localeContextResolver.currentTimeZoneResolution()
    }

    private func resolveDestinationTimeZone(
        timezoneID: String?,
        locationQuery: String?,
        context: SkillExecutionContext
    ) async throws -> InfoTimeZoneResolution {
        if timezoneID == nil && (locationQuery?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ?? true) {
            throw InfoError.missingDestination
        }
        return try await resolveTimeZone(locationQuery: locationQuery, timezoneID: timezoneID, context: context)
    }

    private func preferredLocationResolution(from context: SkillExecutionContext) async throws -> InfoTimeZoneResolution? {
        let memories = await context.memoryItemsProvider()
        guard let locationMemory = memories.first(where: { memory in
            memory.title == "Preferred location" && !memory.content.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }) else {
            return nil
        }

        let preferredLocation = normalizedPreferredLocation(from: locationMemory.content)
        guard let resolved = try? locationResolver.resolve(preferredLocation),
              let timezoneID = resolved.timezoneID,
              let timeZone = TimeZone(identifier: timezoneID) else {
            return nil
        }

        return InfoTimeZoneResolution(
            resolvedLocationName: resolved.resolvedLocationName,
            timeZone: timeZone
        )
    }

    private func normalizedPreferredLocation(from content: String) -> String {
        let trimmed = content.trimmingCharacters(in: .whitespacesAndNewlines)
        let prefix = "User is based in "
        if trimmed.hasPrefix(prefix) {
            return String(trimmed.dropFirst(prefix.count)).trimmingCharacters(in: CharacterSet(charactersIn: ". "))
        }
        return trimmed
    }

    private func normalizedCurrencyCode(_ value: String) throws -> String {
        let code = currencyMetadataResolver.normalizeCurrencyCode(value)
        guard currencyMetadataResolver.metadata(forCurrencyCode: code) != nil else {
            throw InfoError.invalidCurrencyCode(code)
        }
        return code
    }

    private func encode<T: Encodable>(_ value: T) throws -> String {
        let data = try AtlasJSON.encoder.encode(value)
        guard let string = String(data: data, encoding: .utf8) else {
            throw AtlasToolError.executionFailed("Atlas could not encode Info output.")
        }
        return string
    }

    private func summarize(_ value: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.count <= 120 {
            return trimmed
        }
        return String(trimmed.prefix(117)) + "..."
    }

    private func formatAmount(_ amount: Double) -> String {
        let formatter = NumberFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.numberStyle = .decimal
        formatter.maximumFractionDigits = 2
        formatter.minimumFractionDigits = amount.rounded() == amount ? 0 : 2
        return formatter.string(from: NSNumber(value: amount)) ?? String(format: "%.2f", amount)
    }
}
