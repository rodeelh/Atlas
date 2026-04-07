import Foundation
import AtlasShared
import AtlasTools

public struct WeatherSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let provider: any WeatherProviding
    private let locationResolver: any LocationResolving
    private let localeContextResolver: any LocaleContextResolving

    public init(
        provider: (any WeatherProviding)? = nil,
        locationResolver: (any LocationResolving)? = nil,
        localeContextResolver: (any LocaleContextResolving)? = nil
    ) {
        self.provider = provider ?? OpenMeteoWeatherProvider()
        self.locationResolver = locationResolver ?? LocationResolver()
        self.localeContextResolver = localeContextResolver ?? LocaleContextResolver()

        self.manifest = SkillManifest(
            id: "weather",
            name: "Weather",
            version: "2.0.0",
            description: "Retrieve weather conditions, hourly outlooks, planning briefs, and activity-friendly windows for a location.",
            category: .utility,
            lifecycleState: .installed,
            capabilities: [
                .weatherCurrent,
                .weatherForecast,
                .weatherHourly,
                .weatherBrief,
                .weatherDayPlan,
                .weatherActivityWindow
            ],
            requiredPermissions: [
                .weatherRead
            ],
            riskLevel: .low,
            trustProfile: .exactStructured,
            freshnessType: .live,
            preferredQueryTypes: [.weather, .forecast],
            routingPriority: 80,
            canAnswerStructuredLiveData: true,
            restrictionsSummary: [
                "Provider: Open-Meteo",
                "GET only",
                "No authentication or cookies",
                "Hourly outlook limited to 48 hours",
                "Short forecast limited to 7 days"
            ],
            supportsReadOnlyMode: true,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["weather", "forecast", "hourly", "planning", "utility"],
            intent: .liveStructuredData,
            triggers: [
                .init("weather brief", queryType: .weather),
                .init("best time to go out", queryType: .weather),
                .init("hourly weather", queryType: .forecast),
                .init("weather this afternoon", queryType: .forecast),
                .init("theme park weather", queryType: .forecast),
                .init("weather today", queryType: .weather),
                .init("weather right now", queryType: .weather),
                .init("weather in ", queryType: .weather),
                .init("forecast", queryType: .forecast),
                .init("will it rain", queryType: .forecast),
                .init("weekend weather", queryType: .forecast),
                .init("temperature outside", queryType: .weather)
            ]
        )

        self.actions = [
            SkillActionDefinition(
                id: "weather.current",
                name: "Current Weather",
                description: "Retrieve the current weather for a location or coordinates with Atlas summaries and inferred units.",
                inputSchemaSummary: "locationQuery or latitude and longitude are required; temperatureUnit and windSpeedUnit are optional and Atlas can infer units from location or local context.",
                outputSchemaSummary: "Resolved place details, observation time, temperature, condition, comfort, outdoor score, and provider.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.weather],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "City or place name to resolve with Open-Meteo geocoding."),
                        "latitude": AtlasToolInputProperty(type: "number", description: "Latitude to use directly instead of geocoding."),
                        "longitude": AtlasToolInputProperty(type: "number", description: "Longitude to use directly instead of geocoding."),
                        "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Optional temperature unit: celsius or fahrenheit."),
                        "windSpeedUnit": AtlasToolInputProperty(type: "string", description: "Optional wind speed unit: kmh, mph, ms, or kn.")
                    ]
                )
            ),
            SkillActionDefinition(
                id: "weather.forecast",
                name: "Short Forecast",
                description: "Retrieve a concise daily forecast for a location or coordinates.",
                inputSchemaSummary: "locationQuery or latitude and longitude are required; days, temperatureUnit, and windSpeedUnit are optional.",
                outputSchemaSummary: "Resolved place details, daily forecasts, trend summary, recommendations, and provider.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.forecast],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "City or place name to resolve with Open-Meteo geocoding."),
                        "latitude": AtlasToolInputProperty(type: "number", description: "Latitude to use directly instead of geocoding."),
                        "longitude": AtlasToolInputProperty(type: "number", description: "Longitude to use directly instead of geocoding."),
                        "days": AtlasToolInputProperty(type: "integer", description: "Optional forecast day count between 1 and 7."),
                        "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Optional temperature unit: celsius or fahrenheit."),
                        "windSpeedUnit": AtlasToolInputProperty(type: "string", description: "Optional wind speed unit: kmh, mph, ms, or kn.")
                    ]
                )
            ),
            SkillActionDefinition(
                id: "weather.hourly",
                name: "Hourly Outlook",
                description: "Retrieve the next several hours of weather in a dashboard-friendly format.",
                inputSchemaSummary: "locationQuery or latitude and longitude are required; hours defaults to 12 and must be between 1 and 48.",
                outputSchemaSummary: "Resolved place details, hourly forecast rows, and a concise outlook summary.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.forecast],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "City or place name to resolve with Open-Meteo geocoding."),
                        "latitude": AtlasToolInputProperty(type: "number", description: "Latitude to use directly instead of geocoding."),
                        "longitude": AtlasToolInputProperty(type: "number", description: "Longitude to use directly instead of geocoding."),
                        "hours": AtlasToolInputProperty(type: "integer", description: "Optional hourly horizon between 1 and 48."),
                        "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Optional temperature unit: celsius or fahrenheit."),
                        "windSpeedUnit": AtlasToolInputProperty(type: "string", description: "Optional wind speed unit: kmh, mph, ms, or kn.")
                    ]
                )
            ),
            SkillActionDefinition(
                id: "weather.brief",
                name: "Weather Brief",
                description: "Return a concise Atlas-native weather brief that works well for chat, notifications, and dashboards.",
                inputSchemaSummary: "locationQuery or latitude and longitude are required; units are optional.",
                outputSchemaSummary: "Headline, summary, recommendations, current conditions, and day range.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.weather, .forecast],
                routingPriority: 60,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "City or place name."),
                        "latitude": AtlasToolInputProperty(type: "number", description: "Latitude to use directly."),
                        "longitude": AtlasToolInputProperty(type: "number", description: "Longitude to use directly."),
                        "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Optional temperature unit: celsius or fahrenheit."),
                        "windSpeedUnit": AtlasToolInputProperty(type: "string", description: "Optional wind speed unit: kmh, mph, ms, or kn.")
                    ]
                )
            ),
            SkillActionDefinition(
                id: "weather.dayplan",
                name: "Day Plan",
                description: "Return a morning, afternoon, and evening weather plan with practical recommendations.",
                inputSchemaSummary: "locationQuery or latitude and longitude are required; units are optional.",
                outputSchemaSummary: "Headline, summary, umbrella guidance, hydration guidance, outfit guidance, and day segments.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.forecast],
                routingPriority: 58,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "City or place name."),
                        "latitude": AtlasToolInputProperty(type: "number", description: "Latitude to use directly."),
                        "longitude": AtlasToolInputProperty(type: "number", description: "Longitude to use directly."),
                        "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Optional temperature unit: celsius or fahrenheit."),
                        "windSpeedUnit": AtlasToolInputProperty(type: "string", description: "Optional wind speed unit: kmh, mph, ms, or kn.")
                    ]
                )
            ),
            SkillActionDefinition(
                id: "weather.activity_window",
                name: "Activity Window",
                description: "Recommend the best and worst upcoming time windows for an outdoor activity.",
                inputSchemaSummary: "locationQuery or latitude and longitude are required; activityType is required; hours defaults to 12.",
                outputSchemaSummary: "Headline, summary, best windows, avoid windows, and recommendations for the activity.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.forecast],
                routingPriority: 62,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "locationQuery": AtlasToolInputProperty(type: "string", description: "City or place name."),
                        "latitude": AtlasToolInputProperty(type: "number", description: "Latitude to use directly."),
                        "longitude": AtlasToolInputProperty(type: "number", description: "Longitude to use directly."),
                        "activityType": AtlasToolInputProperty(type: "string", description: "walk, run, theme_park, outdoor_dining, golf, beach, or photography."),
                        "hours": AtlasToolInputProperty(type: "integer", description: "Optional hourly horizon between 1 and 24."),
                        "temperatureUnit": AtlasToolInputProperty(type: "string", description: "Optional temperature unit: celsius or fahrenheit."),
                        "windSpeedUnit": AtlasToolInputProperty(type: "string", description: "Optional wind speed unit: kmh, mph, ms, or kn.")
                    ],
                    required: ["activityType"]
                )
            )
        ]
    }

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        let validation = provider.validateProvider()
        return SkillValidationResult(
            skillID: manifest.id,
            status: validation.isAvailable ? .passed : .failed,
            summary: validation.summary,
            issues: validation.issues,
            validatedAt: .now
        )
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "weather.current":
            return try await currentWeather(input: input, context: context)
        case "weather.forecast":
            return try await forecast(input: input, context: context)
        case "weather.hourly":
            return try await hourlyWeather(input: input, context: context)
        case "weather.brief":
            return try await brief(input: input, context: context)
        case "weather.dayplan":
            return try await dayPlan(input: input, context: context)
        case "weather.activity_window":
            return try await activityWindow(input: input, context: context)
        default:
            throw AtlasToolError.invalidInput("The action '\(actionID)' is not supported by Weather.")
        }
    }

    private func currentWeather(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WeatherCurrentInput.self)
        let location = try await resolveLocation(
            query: payload.locationQuery,
            latitude: payload.latitude,
            longitude: payload.longitude
        )
        let requestContext = try resolveRequestContext(
            temperatureUnitValue: payload.temperatureUnit,
            windSpeedUnitValue: payload.windSpeedUnit,
            locationQuery: payload.locationQuery,
            resolvedLocation: location
        )

        context.logger.info("Executing Weather current request", metadata: [
            "skill_id": manifest.id,
            "action_id": "weather.current",
            "location_query": summarize(payload.locationQuery ?? ""),
            "resolved_location": summarize(location.resolvedLocationName),
            "provider": provider.providerName
        ])

        let baseOutput = try await provider.currentWeather(for: location, context: requestContext)
        let enriched = enrichCurrent(baseOutput, requestContext: requestContext)

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "weather.current",
            output: try encode(enriched),
            summary: enriched.summary,
            metadata: [
                "provider": provider.providerName,
                "condition": enriched.condition,
                "temperature_unit": enriched.resolvedTemperatureUnit.rawValue
            ]
        )
    }

    private func forecast(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WeatherForecastInput.self)
        let location = try await resolveLocation(
            query: payload.locationQuery,
            latitude: payload.latitude,
            longitude: payload.longitude
        )
        let requestContext = try resolveRequestContext(
            temperatureUnitValue: payload.temperatureUnit,
            windSpeedUnitValue: payload.windSpeedUnit,
            locationQuery: payload.locationQuery,
            resolvedLocation: location
        )
        let days = try normalizedDays(payload.days)

        context.logger.info("Executing Weather forecast request", metadata: [
            "skill_id": manifest.id,
            "action_id": "weather.forecast",
            "location_query": summarize(payload.locationQuery ?? ""),
            "resolved_location": summarize(location.resolvedLocationName),
            "days": "\(days)",
            "provider": provider.providerName
        ])

        let baseOutput = try await provider.forecast(for: location, days: days, context: requestContext)
        let enriched = enrichForecast(baseOutput, requestContext: requestContext)

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "weather.forecast",
            output: try encode(enriched),
            summary: enriched.summary,
            metadata: [
                "provider": provider.providerName,
                "day_count": "\(enriched.dailyForecasts.count)",
                "temperature_unit": enriched.resolvedTemperatureUnit.rawValue
            ]
        )
    }

    private func hourlyWeather(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WeatherHourlyInput.self)
        let location = try await resolveLocation(
            query: payload.locationQuery,
            latitude: payload.latitude,
            longitude: payload.longitude
        )
        let requestContext = try resolveRequestContext(
            temperatureUnitValue: payload.temperatureUnit,
            windSpeedUnitValue: payload.windSpeedUnit,
            locationQuery: payload.locationQuery,
            resolvedLocation: location
        )
        let hours = try normalizedHours(payload.hours, range: 1...48, defaultValue: 12)
        let baseOutput = try await provider.hourlyForecast(for: location, hours: hours, context: requestContext)
        let enriched = enrichHourly(baseOutput, requestContext: requestContext)

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "weather.hourly",
            output: try encode(enriched),
            summary: enriched.summary,
            metadata: [
                "provider": provider.providerName,
                "hour_count": "\(enriched.hourlyForecasts.count)"
            ]
        )
    }

    private func brief(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WeatherBriefInput.self)
        let location = try await resolveLocation(
            query: payload.locationQuery,
            latitude: payload.latitude,
            longitude: payload.longitude
        )
        let requestContext = try resolveRequestContext(
            temperatureUnitValue: payload.temperatureUnit,
            windSpeedUnitValue: payload.windSpeedUnit,
            locationQuery: payload.locationQuery,
            resolvedLocation: location
        )

        async let currentTask = provider.currentWeather(for: location, context: requestContext)
        async let forecastTask = provider.forecast(for: location, days: 1, context: requestContext)
        let current = enrichCurrent(try await currentTask, requestContext: requestContext)
        let forecast = enrichForecast(try await forecastTask, requestContext: requestContext)
        let day = forecast.dailyForecasts.first

        let recommendations = {
            let merged = mergeRecommendations(current.recommendations, forecast.recommendations)
            return merged.isEmpty ? ["Conditions look fairly manageable overall."] : merged
        }()

        let output = WeatherBriefOutput(
            resolvedLocationName: current.resolvedLocationName,
            countryCode: current.countryCode,
            timezone: current.timezone,
            requestedTemperatureUnit: requestContext.requestedTemperatureUnit,
            resolvedTemperatureUnit: requestContext.resolvedTemperatureUnit,
            temperatureUnitSource: requestContext.temperatureUnitSource,
            currentTemperature: current.temperature,
            currentCondition: current.condition,
            dayHigh: day?.temperatureMax,
            dayLow: day?.temperatureMin,
            headline: current.headline,
            summary: "\(current.summary) \(forecast.summary)",
            recommendations: recommendations,
            outdoorScore: current.outdoorScore,
            sourceProvider: provider.providerName
        )

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "weather.brief",
            output: try encode(output),
            summary: output.summary,
            metadata: [
                "provider": provider.providerName,
                "temperature_unit": output.resolvedTemperatureUnit.rawValue
            ]
        )
    }

    private func dayPlan(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WeatherDayPlanInput.self)
        let location = try await resolveLocation(
            query: payload.locationQuery,
            latitude: payload.latitude,
            longitude: payload.longitude
        )
        let requestContext = try resolveRequestContext(
            temperatureUnitValue: payload.temperatureUnit,
            windSpeedUnitValue: payload.windSpeedUnit,
            locationQuery: payload.locationQuery,
            resolvedLocation: location
        )

        async let currentTask = provider.currentWeather(for: location, context: requestContext)
        async let hourlyTask = provider.hourlyForecast(for: location, hours: 18, context: requestContext)
        let current = enrichCurrent(try await currentTask, requestContext: requestContext)
        let hourly = enrichHourly(try await hourlyTask, requestContext: requestContext)
        let segments = buildDayPlanSegments(from: hourly.hourlyForecasts, unit: requestContext.resolvedTemperatureUnit)
        let umbrellaRecommended = hourly.hourlyForecasts.contains { ($0.precipitationProbability ?? 0) >= 45 }
        let hydrationGuidance = hydrationGuidance(for: current.temperature, unit: requestContext.resolvedTemperatureUnit)
        let outfitGuidance = outfitGuidance(for: current.temperature, condition: current.condition, unit: requestContext.resolvedTemperatureUnit)
        let recommendations = mergeRecommendations(
            current.recommendations,
            umbrellaRecommended ? ["Keep an umbrella or rain layer nearby."] : ["A rain layer probably is not necessary today."]
        )

        let output = WeatherDayPlanOutput(
            resolvedLocationName: current.resolvedLocationName,
            countryCode: current.countryCode,
            timezone: current.timezone,
            requestedTemperatureUnit: requestContext.requestedTemperatureUnit,
            resolvedTemperatureUnit: requestContext.resolvedTemperatureUnit,
            temperatureUnitSource: requestContext.temperatureUnitSource,
            headline: "Day plan for \(current.resolvedLocationName)",
            summary: "Morning starts \(segments.first?.summary.lowercased() ?? "steady"), with the day trending \(current.condition).",
            umbrellaRecommended: umbrellaRecommended,
            hydrationGuidance: hydrationGuidance,
            outfitGuidance: outfitGuidance,
            recommendations: recommendations,
            segments: segments,
            sourceProvider: provider.providerName
        )

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "weather.dayplan",
            output: try encode(output),
            summary: output.summary,
            metadata: [
                "provider": provider.providerName,
                "umbrella": umbrellaRecommended ? "true" : "false"
            ]
        )
    }

    private func activityWindow(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WeatherActivityWindowInput.self)
        let location = try await resolveLocation(
            query: payload.locationQuery,
            latitude: payload.latitude,
            longitude: payload.longitude
        )
        let requestContext = try resolveRequestContext(
            temperatureUnitValue: payload.temperatureUnit,
            windSpeedUnitValue: payload.windSpeedUnit,
            locationQuery: payload.locationQuery,
            resolvedLocation: location
        )
        let hours = try normalizedHours(payload.hours, range: 1...24, defaultValue: 12)
        let hourly = enrichHourly(
            try await provider.hourlyForecast(for: location, hours: hours, context: requestContext),
            requestContext: requestContext
        )
        let scored = scoreActivityWindows(for: payload.activityType, hourly: hourly.hourlyForecasts, unit: requestContext.resolvedTemperatureUnit)
        let recommendations = activityRecommendations(for: payload.activityType, bestWindows: scored.best, avoidWindows: scored.avoid)

        let output = WeatherActivityWindowOutput(
            resolvedLocationName: hourly.resolvedLocationName,
            countryCode: hourly.countryCode,
            timezone: hourly.timezone,
            activityType: payload.activityType,
            requestedTemperatureUnit: requestContext.requestedTemperatureUnit,
            resolvedTemperatureUnit: requestContext.resolvedTemperatureUnit,
            temperatureUnitSource: requestContext.temperatureUnitSource,
            headline: scored.best.isEmpty
                ? "No standout \(payload.activityType.rawValue.replacingOccurrences(of: "_", with: " ")) window"
                : "Best \(payload.activityType.rawValue.replacingOccurrences(of: "_", with: " ")) window coming up",
            summary: scored.best.first?.summary ?? "Conditions stay mixed across the next \(hours) hours.",
            bestWindows: scored.best,
            avoidWindows: scored.avoid,
            recommendations: recommendations,
            sourceProvider: provider.providerName
        )

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "weather.activity_window",
            output: try encode(output),
            summary: output.summary,
            metadata: [
                "provider": provider.providerName,
                "activity_type": payload.activityType.rawValue
            ]
        )
    }

    private func resolveLocation(
        query: String?,
        latitude: Double?,
        longitude: Double?
    ) async throws -> WeatherResolvedLocation {
        if let latitude, let longitude {
            guard (-90...90).contains(latitude), (-180...180).contains(longitude) else {
                throw WeatherError.invalidCoordinates
            }

            return WeatherResolvedLocation(
                resolvedLocationName: String(format: "%.4f, %.4f", latitude, longitude),
                countryCode: nil,
                latitude: latitude,
                longitude: longitude,
                timezone: nil
            )
        }

        if latitude != nil || longitude != nil {
            throw WeatherError.invalidCoordinates
        }

        guard let query else {
            throw WeatherError.missingLocation
        }

        return try await provider.resolveLocation(query: query)
    }

    private func resolveRequestContext(
        temperatureUnitValue: String?,
        windSpeedUnitValue: String?,
        locationQuery: String?,
        resolvedLocation: WeatherResolvedLocation
    ) throws -> WeatherRequestContext {
        let explicitTemperatureUnit = try WeatherTemperatureUnit.parse(temperatureUnitValue)
        let countryCode = inferCountryCode(locationQuery: locationQuery, resolvedLocation: resolvedLocation)

        let resolvedTemperatureUnit: WeatherTemperatureUnit
        let temperatureUnitSource: WeatherTemperatureUnitSource

        if let explicitTemperatureUnit {
            resolvedTemperatureUnit = explicitTemperatureUnit
            temperatureUnitSource = .explicit
        } else if let countryCode {
            resolvedTemperatureUnit = usesFahrenheit(countryCode: countryCode) ? .fahrenheit : .celsius
            temperatureUnitSource = resolvedLocation.countryCode != nil ? .resolvedLocation : .infoContext
        } else {
            resolvedTemperatureUnit = .celsius
            temperatureUnitSource = .fallback
        }

        let windSpeedUnit = try WeatherWindSpeedUnit(rawValueOrDefault: windSpeedUnitValue)

        return WeatherRequestContext(
            requestedTemperatureUnit: explicitTemperatureUnit,
            resolvedTemperatureUnit: resolvedTemperatureUnit,
            temperatureUnitSource: temperatureUnitSource,
            windSpeedUnit: windSpeedUnit
        )
    }

    private func inferCountryCode(
        locationQuery: String?,
        resolvedLocation: WeatherResolvedLocation
    ) -> String? {
        if let resolved = resolvedLocation.countryCode {
            return resolved
        }

        if let locationQuery, let infoLocation = try? locationResolver.resolve(locationQuery), let countryCode = infoLocation.countryCode {
            return countryCode
        }

        return localeContextResolver.currentCountryCode()
    }

    private func usesFahrenheit(countryCode: String) -> Bool {
        let normalized = countryCode.uppercased()
        return ["US", "BS", "BZ", "KY", "LR", "FM", "MH", "PW"].contains(normalized)
    }

    private func enrichCurrent(
        _ output: WeatherCurrentOutput,
        requestContext: WeatherRequestContext
    ) -> WeatherCurrentOutput {
        let comfort = comfortLevel(for: output.temperature, unit: requestContext.resolvedTemperatureUnit)
        let rainRisk = rainRiskLabel(precipitationProbability: nil, condition: output.condition)
        let heatRisk = heatRiskLabel(for: output.apparentTemperature ?? output.temperature, unit: requestContext.resolvedTemperatureUnit)
        let outdoorScore = outdoorScore(
            temperature: output.apparentTemperature ?? output.temperature,
            condition: output.condition,
            precipitationProbability: nil,
            windSpeed: output.windSpeed,
            isDay: output.isDay,
            unit: requestContext.resolvedTemperatureUnit
        )
        let recommendations = recommendationsForCurrent(
            condition: output.condition,
            rainRisk: rainRisk,
            heatRisk: heatRisk,
            score: outdoorScore
        )

        return WeatherCurrentOutput(
            resolvedLocationName: output.resolvedLocationName,
            countryCode: output.countryCode,
            latitude: output.latitude,
            longitude: output.longitude,
            timezone: output.timezone,
            observationTime: output.observationTime,
            temperature: output.temperature,
            apparentTemperature: output.apparentTemperature,
            weatherCode: output.weatherCode,
            condition: output.condition,
            windSpeed: output.windSpeed,
            windDirection: output.windDirection,
            isDay: output.isDay,
            requestedTemperatureUnit: requestContext.requestedTemperatureUnit,
            resolvedTemperatureUnit: requestContext.resolvedTemperatureUnit,
            temperatureUnitSource: requestContext.temperatureUnitSource,
            windSpeedUnit: requestContext.windSpeedUnit,
            headline: "\(output.condition.capitalized) in \(output.resolvedLocationName)",
            summary: "It feels \(comfort.lowercased()) right now with \(output.condition) conditions in \(output.resolvedLocationName).",
            comfortLevel: comfort,
            outdoorScore: outdoorScore,
            rainRisk: rainRisk,
            heatRisk: heatRisk,
            recommendations: recommendations,
            sourceProvider: output.sourceProvider
        )
    }

    private func enrichForecast(
        _ output: WeatherForecastOutput,
        requestContext: WeatherRequestContext
    ) -> WeatherForecastOutput {
        let recommendationSource = output.dailyForecasts.flatMap { forecast in
            recommendationsForForecastDay(forecast, unit: requestContext.resolvedTemperatureUnit)
        }

        let summary = output.dailyForecasts.isEmpty
            ? "No forecast data is available."
            : "Next \(output.dailyForecasts.count) day\(output.dailyForecasts.count == 1 ? "" : "s") trend \(output.dailyForecasts.map(\.condition).joined(separator: ", "))."

        return WeatherForecastOutput(
            resolvedLocationName: output.resolvedLocationName,
            countryCode: output.countryCode,
            latitude: output.latitude,
            longitude: output.longitude,
            timezone: output.timezone,
            requestedTemperatureUnit: requestContext.requestedTemperatureUnit,
            resolvedTemperatureUnit: requestContext.resolvedTemperatureUnit,
            temperatureUnitSource: requestContext.temperatureUnitSource,
            windSpeedUnit: requestContext.windSpeedUnit,
            headline: "Forecast for \(output.resolvedLocationName)",
            summary: summary,
            recommendations: mergeRecommendations(recommendationSource),
            dailyForecasts: output.dailyForecasts,
            sourceProvider: output.sourceProvider
        )
    }

    private func enrichHourly(
        _ output: WeatherHourlyOutput,
        requestContext: WeatherRequestContext
    ) -> WeatherHourlyOutput {
        let headline = output.hourlyForecasts.first.map {
            "Hourly outlook starts with \($0.condition) and \(formatTemperature($0.temperature, unit: requestContext.resolvedTemperatureUnit))."
        } ?? "Hourly outlook unavailable."
        let summary = output.hourlyForecasts.isEmpty
            ? "No hourly weather data is available."
            : "Next \(output.hourlyForecasts.count) hours are ready for planning in \(output.resolvedLocationName)."

        return WeatherHourlyOutput(
            resolvedLocationName: output.resolvedLocationName,
            countryCode: output.countryCode,
            latitude: output.latitude,
            longitude: output.longitude,
            timezone: output.timezone,
            requestedTemperatureUnit: requestContext.requestedTemperatureUnit,
            resolvedTemperatureUnit: requestContext.resolvedTemperatureUnit,
            temperatureUnitSource: requestContext.temperatureUnitSource,
            windSpeedUnit: requestContext.windSpeedUnit,
            headline: headline,
            summary: summary,
            hourlyForecasts: output.hourlyForecasts,
            sourceProvider: output.sourceProvider
        )
    }

    private func comfortLevel(for temperature: Double, unit: WeatherTemperatureUnit) -> String {
        let celsius = toCelsius(temperature, unit: unit)
        switch celsius {
        case ..<4:
            return "Cold"
        case ..<12:
            return "Cool"
        case ..<24:
            return "Comfortable"
        case ..<30:
            return "Warm"
        default:
            return "Hot"
        }
    }

    private func rainRiskLabel(precipitationProbability: Double?, condition: String) -> String {
        if let precipitationProbability {
            switch precipitationProbability {
            case ..<20: return "Low"
            case ..<50: return "Moderate"
            default: return "High"
            }
        }

        let normalized = condition.lowercased()
        if normalized.contains("rain") || normalized.contains("storm") || normalized.contains("snow") {
            return "High"
        }
        if normalized.contains("cloud") || normalized.contains("fog") {
            return "Moderate"
        }
        return "Low"
    }

    private func heatRiskLabel(for temperature: Double, unit: WeatherTemperatureUnit) -> String {
        let celsius = toCelsius(temperature, unit: unit)
        switch celsius {
        case ..<26: return "Low"
        case ..<32: return "Moderate"
        default: return "High"
        }
    }

    private func outdoorScore(
        temperature: Double,
        condition: String,
        precipitationProbability: Double?,
        windSpeed: Double?,
        isDay: Bool?,
        unit: WeatherTemperatureUnit
    ) -> Int {
        var score = 85
        let celsius = toCelsius(temperature, unit: unit)
        if celsius < 4 || celsius > 33 { score -= 28 } else if celsius < 10 || celsius > 29 { score -= 14 }

        if let precipitationProbability {
            if precipitationProbability >= 65 { score -= 28 } else if precipitationProbability >= 35 { score -= 12 }
        }

        let normalized = condition.lowercased()
        if normalized.contains("storm") { score -= 35 } else if normalized.contains("rain") || normalized.contains("snow") { score -= 20 } else if normalized.contains("fog") { score -= 10 }

        if let windSpeed, windSpeed >= 25 { score -= 12 }
        if isDay == false { score -= 6 }

        return max(0, min(100, score))
    }

    private func recommendationsForCurrent(
        condition: String,
        rainRisk: String,
        heatRisk: String,
        score: Int
    ) -> [String] {
        var results: [String] = []
        if heatRisk == "High" {
            results.append("Plan for hydration and lighter clothing.")
        }
        if rainRisk == "High" {
            results.append("Bring an umbrella or rain layer.")
        }
        if score >= 75 {
            results.append("This is a good window for outdoor plans.")
        } else if score <= 45 {
            results.append("Indoor plans may be more comfortable right now.")
        }
        if condition.lowercased().contains("wind") {
            results.append("Expect breezy conditions outdoors.")
        }
        return results
    }

    private func recommendationsForForecastDay(
        _ forecast: DailyForecast,
        unit: WeatherTemperatureUnit
    ) -> [String] {
        var results: [String] = []
        let heatRisk = heatRiskLabel(for: forecast.temperatureMax, unit: unit)
        if heatRisk == "High" {
            results.append("One of the forecast days runs hot.")
        }
        if (forecast.precipitationProbabilityMax ?? 0) >= 50 {
            results.append("Rain looks likely on at least one forecast day.")
        }
        return results
    }

    private func mergeRecommendations(_ groups: [String]...) -> [String] {
        mergeRecommendations(groups.flatMap { $0 })
    }

    private func mergeRecommendations(_ items: [String]) -> [String] {
        items.reduce(into: [String]()) { values, item in
            if values.contains(item) == false {
                values.append(item)
            }
        }
    }

    private func buildDayPlanSegments(
        from hourly: [HourlyForecast],
        unit: WeatherTemperatureUnit
    ) -> [WeatherDayPlanSegment] {
        let groups: [(String, [HourlyForecast])] = [
            ("Morning", Array(hourly.prefix(6))),
            ("Afternoon", Array(hourly.dropFirst(6).prefix(6))),
            ("Evening", Array(hourly.dropFirst(12).prefix(6)))
        ]

        return groups.compactMap { label, values in
            guard values.isEmpty == false else { return nil }
            let avgTemp = values.map(\.temperature).reduce(0, +) / Double(values.count)
            let rainMax = values.compactMap(\.precipitationProbability).max()
            let condition = values.first?.condition ?? "steady"
            let score = values
                .map { outdoorScore(temperature: $0.apparentTemperature ?? $0.temperature, condition: $0.condition, precipitationProbability: $0.precipitationProbability, windSpeed: $0.windSpeed, isDay: $0.isDay, unit: unit) }
                .reduce(0, +) / max(1, values.count)

            return WeatherDayPlanSegment(
                label: label,
                summary: "\(condition.capitalized) around \(formatTemperature(avgTemp, unit: unit)).",
                outdoorScore: score,
                rainRisk: rainRiskLabel(precipitationProbability: rainMax, condition: condition)
            )
        }
    }

    private func hydrationGuidance(for temperature: Double, unit: WeatherTemperatureUnit) -> String {
        let celsius = toCelsius(temperature, unit: unit)
        switch celsius {
        case ..<24:
            return "Normal hydration should be enough."
        case ..<30:
            return "Carry water if you will be outside for a while."
        default:
            return "Plan for extra water and shade breaks."
        }
    }

    private func outfitGuidance(for temperature: Double, condition: String, unit: WeatherTemperatureUnit) -> String {
        let celsius = toCelsius(temperature, unit: unit)
        if celsius < 10 {
            return "Bring a jacket or warmer layer."
        }
        if condition.lowercased().contains("rain") {
            return "Light layers plus a rain shell will work well."
        }
        if celsius > 28 {
            return "Light breathable clothing is the best fit."
        }
        return "Standard daywear should feel comfortable."
    }

    private func scoreActivityWindows(
        for activityType: WeatherActivityType,
        hourly: [HourlyForecast],
        unit: WeatherTemperatureUnit
    ) -> (best: [WeatherActivityWindow], avoid: [WeatherActivityWindow]) {
        let windows = hourly.map { hour -> WeatherActivityWindow in
            let score = activityScore(for: activityType, hour: hour, unit: unit)
            return WeatherActivityWindow(
                label: hourLabel(for: hour.time),
                startTime: hour.time,
                endTime: hour.time,
                score: score,
                summary: "\(hour.condition.capitalized) around \(formatTemperature(hour.temperature, unit: unit))."
            )
        }

        let best = Array(windows.sorted(by: { $0.score > $1.score }).prefix(2))
        let avoid = Array(windows.sorted(by: { $0.score < $1.score }).prefix(2))
        return (best, avoid)
    }

    private func activityScore(
        for activityType: WeatherActivityType,
        hour: HourlyForecast,
        unit: WeatherTemperatureUnit
    ) -> Int {
        var score = outdoorScore(
            temperature: hour.apparentTemperature ?? hour.temperature,
            condition: hour.condition,
            precipitationProbability: hour.precipitationProbability,
            windSpeed: hour.windSpeed,
            isDay: hour.isDay,
            unit: unit
        )

        let celsius = toCelsius(hour.apparentTemperature ?? hour.temperature, unit: unit)
        switch activityType {
        case .run:
            if celsius > 26 { score -= 15 }
            if celsius < 2 { score -= 10 }
        case .themePark:
            if celsius > 31 { score -= 18 }
            if (hour.precipitationProbability ?? 0) > 40 { score -= 15 }
        case .outdoorDining:
            if hour.isDay == false { score += 6 }
            if let wind = hour.windSpeed, wind > 20 { score -= 12 }
        case .beach:
            if celsius < 24 { score -= 15 }
            if celsius > 34 { score -= 10 }
        case .photography:
            if hour.condition.lowercased().contains("clear") == false { score += 4 }
        case .golf, .walk:
            break
        }

        return max(0, min(100, score))
    }

    private func activityRecommendations(
        for activityType: WeatherActivityType,
        bestWindows: [WeatherActivityWindow],
        avoidWindows: [WeatherActivityWindow]
    ) -> [String] {
        var results: [String] = []
        if let best = bestWindows.first {
            results.append("Best \(activityType.rawValue.replacingOccurrences(of: "_", with: " ")) window: \(best.label).")
        }
        if let avoid = avoidWindows.first {
            results.append("Least comfortable window: \(avoid.label).")
        }
        return results
    }

    private func hourLabel(for isoTimestamp: String) -> String {
        let timestamp = isoTimestamp.split(separator: "T").last.map(String.init) ?? isoTimestamp
        return timestamp
    }

    private func normalizedDays(_ days: Int?) throws -> Int {
        let resolved = days ?? 3
        guard (1...7).contains(resolved) else {
            throw WeatherError.invalidDayCount(resolved)
        }
        return resolved
    }

    private func normalizedHours(_ hours: Int?, range: ClosedRange<Int>, defaultValue: Int) throws -> Int {
        let resolved = hours ?? defaultValue
        guard range.contains(resolved) else {
            throw WeatherError.providerFailure("The hourly range '\(resolved)' is invalid. Use a value between \(range.lowerBound) and \(range.upperBound).")
        }
        return resolved
    }

    private func toCelsius(_ temperature: Double, unit: WeatherTemperatureUnit) -> Double {
        switch unit {
        case .celsius:
            return temperature
        case .fahrenheit:
            return (temperature - 32) * 5 / 9
        }
    }

    private func formatTemperature(_ temperature: Double, unit: WeatherTemperatureUnit) -> String {
        let rounded = Int(temperature.rounded())
        let suffix = unit == .fahrenheit ? "F" : "C"
        return "\(rounded)°\(suffix)"
    }

    private func encode<T: Encodable>(_ value: T) throws -> String {
        let data = try AtlasJSON.encoder.encode(value)
        guard let string = String(data: data, encoding: .utf8) else {
            throw AtlasToolError.executionFailed("Atlas could not encode Weather output.")
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
}
