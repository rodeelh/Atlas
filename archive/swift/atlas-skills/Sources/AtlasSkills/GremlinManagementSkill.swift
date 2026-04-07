import Foundation
import AtlasShared
import AtlasTools

// MARK: - Protocol

/// Protocol allowing atlas-skills to manage Gremlins without depending on atlas-memory.
public protocol GremlinManaging: Sendable {
    func loadGremlins() async throws -> [GremlinItem]
    func addGremlin(_ item: GremlinItem) async throws
    func updateGremlin(_ item: GremlinItem) async throws
    func deleteGremlin(id: String) async throws
    func runNow(id: String) async throws -> GremlinRun
    func runsForGremlin(gremlinID: String, limit: Int) async throws -> [GremlinRun]
    func validateSchedule(_ raw: String) async -> GremlinScheduleValidation
}

// MARK: - Typed input models

private struct GremlinCreateInput: Codable, Sendable {
    let name: String
    let prompt: String
    let schedule: String
    let emoji: String?
    let description: String?
    let tags: [String]?
    let destination: String?      // "platform:channelID" e.g. "telegram:123456"
    let maxRetries: Int?
    let timeoutSeconds: Int?
}

private struct GremlinUpdateInput: Codable, Sendable {
    let id: String
    let name: String?
    let prompt: String?
    let schedule: String?
    let emoji: String?
    let enabled: Bool?
    let description: String?
    let tags: [String]?
    let destination: String?      // set to "" to clear
    let maxRetries: Int?
    let timeoutSeconds: Int?      // set to 0 to clear
}

private struct GremlinIDInput: Codable, Sendable {
    let id: String
}

private struct GremlinRunHistoryInput: Codable, Sendable {
    let id: String
    let limit: Int?
}

private struct GremlinDuplicateInput: Codable, Sendable {
    let id: String
    let newName: String
    let newSchedule: String?
}

private struct GremlinSetDestinationInput: Codable, Sendable {
    let id: String
    let platform: String?   // nil or "" to clear
    let channelID: String?
}

private struct GremlinValidateScheduleInput: Codable, Sendable {
    let schedule: String
}

// MARK: - Skill

public struct GremlinManagementSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let gremlinManager: any GremlinManaging

    public init(gremlinManaging: any GremlinManaging) {
        self.gremlinManager = gremlinManaging

        self.manifest = SkillManifest(
            id: "gremlin-management",
            name: "Gremlin Management",
            version: "2.0.0",
            description: "Create, update, delete, enable, disable, run, inspect, and validate Gremlins — scheduled automations that execute Atlas prompts on a cron-like schedule.",
            category: .system,
            lifecycleState: .installed,
            capabilities: [],
            requiredPermissions: [.draftWrite],
            riskLevel: .medium,
            trustProfile: .localExact,
            freshnessType: .local,
            preferredQueryTypes: [],
            routingPriority: 80,
            canHandleLocalData: true,
            restrictionsSummary: ["Gremlins run with full agent permissions"],
            supportsReadOnlyMode: false,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["automations", "scheduler", "gremlins"],
            intent: .atlasSystemTask,
            triggers: [
                .init("set up a gremlin"),
                .init("create automation"),
                .init("schedule a task"),
                .init("add a daily brief"),
                .init("run gremlin"),
                .init("list gremlins"),
                .init("list automations"),
                .init("set up a reminder"),
                .init("create a gremlin"),
                .init("new gremlin"),
                .init("delete gremlin"),
                .init("enable gremlin"),
                .init("disable gremlin"),
                .init("update gremlin"),
                .init("get gremlin"),
                .init("show gremlin"),
                .init("gremlin history"),
                .init("when does gremlin run"),
                .init("duplicate gremlin"),
                .init("clone automation"),
                .init("validate schedule"),
                .init("set notification destination")
            ]
        )

        self.actions = [
            SkillActionDefinition(
                id: "gremlin-management.create_gremlin",
                name: "Create Gremlin",
                description: "Create a new scheduled automation (Gremlin). Schedule is validated before saving.",
                inputSchemaSummary: "name, prompt, schedule required. emoji, description, tags, destination (platform:channelID), maxRetries, timeoutSeconds optional.",
                outputSchemaSummary: "Created GremlinItem as JSON",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "name": AtlasToolInputProperty(type: "string", description: "Short display name"),
                        "prompt": AtlasToolInputProperty(type: "string", description: "The Atlas prompt this Gremlin executes"),
                        "schedule": AtlasToolInputProperty(type: "string", description: "Schedule: daily 08:00, weekly monday 09:00, monthly 1 09:00, weekdays 08:00, weekends 10:00, every 30 minutes, once 2026-12-01, cron 0 8 * * 1-5, or append timezone: daily 08:00 America/New_York"),
                        "emoji": AtlasToolInputProperty(type: "string", description: "Emoji icon (default ⚡)"),
                        "description": AtlasToolInputProperty(type: "string", description: "Human-readable summary of what this Gremlin does"),
                        "tags": AtlasToolInputProperty(type: "array", description: "Label array for grouping, e.g. [\"daily\", \"reports\"]"),
                        "destination": AtlasToolInputProperty(type: "string", description: "Delivery destination as platform:channelID, e.g. telegram:123456 or discord:987654"),
                        "maxRetries": AtlasToolInputProperty(type: "integer", description: "Auto-retry count on failure (default 0)"),
                        "timeoutSeconds": AtlasToolInputProperty(type: "integer", description: "Kill run after N seconds (omit for no limit)")
                    ],
                    required: ["name", "prompt", "schedule"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.list_gremlins",
                name: "List Gremlins",
                description: "List all Gremlins as a JSON array.",
                inputSchemaSummary: "No parameters",
                outputSchemaSummary: "JSON array of GremlinItem",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(properties: [:])
            ),
            SkillActionDefinition(
                id: "gremlin-management.get_gremlin",
                name: "Get Gremlin",
                description: "Fetch a single Gremlin by ID, including its last 3 runs.",
                inputSchemaSummary: "id required",
                outputSchemaSummary: "JSON with gremlin and recentRuns array",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: ["id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug")],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.update_gremlin",
                name: "Update Gremlin",
                description: "Update an existing Gremlin. Only provided fields are changed.",
                inputSchemaSummary: "id required. Optional: name, prompt, schedule, emoji, enabled, description, tags, destination, maxRetries, timeoutSeconds",
                outputSchemaSummary: "Updated GremlinItem as JSON",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug"),
                        "name": AtlasToolInputProperty(type: "string", description: "New name"),
                        "prompt": AtlasToolInputProperty(type: "string", description: "New prompt"),
                        "schedule": AtlasToolInputProperty(type: "string", description: "New schedule string"),
                        "emoji": AtlasToolInputProperty(type: "string", description: "New emoji"),
                        "enabled": AtlasToolInputProperty(type: "boolean", description: "true to enable, false to disable"),
                        "description": AtlasToolInputProperty(type: "string", description: "New description"),
                        "tags": AtlasToolInputProperty(type: "array", description: "New tags array"),
                        "destination": AtlasToolInputProperty(type: "string", description: "New destination (platform:channelID), or empty string to clear"),
                        "maxRetries": AtlasToolInputProperty(type: "integer", description: "New retry count"),
                        "timeoutSeconds": AtlasToolInputProperty(type: "integer", description: "New timeout, 0 to clear")
                    ],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.delete_gremlin",
                name: "Delete Gremlin",
                description: "Permanently delete a Gremlin.",
                inputSchemaSummary: "id required",
                outputSchemaSummary: "Confirmation message",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: ["id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug")],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.enable_gremlin",
                name: "Enable Gremlin",
                description: "Enable a Gremlin so it runs on its schedule.",
                inputSchemaSummary: "id required",
                outputSchemaSummary: "Updated GremlinItem as JSON",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: ["id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug")],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.disable_gremlin",
                name: "Disable Gremlin",
                description: "Disable a Gremlin without deleting it.",
                inputSchemaSummary: "id required",
                outputSchemaSummary: "Updated GremlinItem as JSON",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: ["id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug")],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.run_gremlin_now",
                name: "Run Gremlin Now",
                description: "Immediately trigger a Gremlin run outside its scheduled time.",
                inputSchemaSummary: "id required",
                outputSchemaSummary: "GremlinRun result as JSON",
                permissionLevel: .execute,
                sideEffectLevel: .liveWrite,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: ["id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug")],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.run_history",
                name: "Run History",
                description: "Fetch the run history for a Gremlin.",
                inputSchemaSummary: "id required, limit optional (default 10)",
                outputSchemaSummary: "JSON array of GremlinRun",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug"),
                        "limit": AtlasToolInputProperty(type: "integer", description: "Maximum number of runs to return (default 10, max 50)")
                    ],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.next_run",
                name: "Next Run",
                description: "Return the next scheduled fire time for a Gremlin.",
                inputSchemaSummary: "id required",
                outputSchemaSummary: "JSON with nextRunAt (ISO8601), humanDescription, and secondsFromNow",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: ["id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug")],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.set_destination",
                name: "Set Destination",
                description: "Set or clear the communication delivery destination for a Gremlin.",
                inputSchemaSummary: "id required. platform + channelID to set, omit both to clear.",
                outputSchemaSummary: "Updated GremlinItem as JSON",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "id": AtlasToolInputProperty(type: "string", description: "Gremlin ID/slug"),
                        "platform": AtlasToolInputProperty(type: "string", description: "Platform: telegram or discord. Omit to clear."),
                        "channelID": AtlasToolInputProperty(type: "string", description: "Channel or chat ID for the platform.")
                    ],
                    required: ["id"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.duplicate_gremlin",
                name: "Duplicate Gremlin",
                description: "Clone a Gremlin with a new name, optionally with a new schedule.",
                inputSchemaSummary: "id and newName required. newSchedule optional.",
                outputSchemaSummary: "New GremlinItem as JSON",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "id": AtlasToolInputProperty(type: "string", description: "Source Gremlin ID/slug"),
                        "newName": AtlasToolInputProperty(type: "string", description: "Name for the cloned Gremlin"),
                        "newSchedule": AtlasToolInputProperty(type: "string", description: "Override schedule for the clone (uses source schedule if omitted)")
                    ],
                    required: ["id", "newName"]
                )
            ),
            SkillActionDefinition(
                id: "gremlin-management.validate_schedule",
                name: "Validate Schedule",
                description: "Validate a schedule string and preview the next 3 fire times without creating a Gremlin.",
                inputSchemaSummary: "schedule required",
                outputSchemaSummary: "JSON with isValid, nextFireDates (array of ISO8601), and errorMessage",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                inputSchema: AtlasToolInputSchema(
                    properties: ["schedule": AtlasToolInputProperty(type: "string", description: "Schedule string to validate")],
                    required: ["schedule"]
                )
            )
        ]
    }

    // MARK: - Execute

    public func execute(actionID: String, input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        switch actionID {
        case "gremlin-management.create_gremlin":    return try await createGremlin(input: input)
        case "gremlin-management.list_gremlins":     return try await listGremlins()
        case "gremlin-management.get_gremlin":       return try await getGremlin(input: input)
        case "gremlin-management.update_gremlin":    return try await updateGremlin(input: input)
        case "gremlin-management.delete_gremlin":    return try await deleteGremlin(input: input)
        case "gremlin-management.enable_gremlin":    return try await setEnabled(input: input, enabled: true)
        case "gremlin-management.disable_gremlin":   return try await setEnabled(input: input, enabled: false)
        case "gremlin-management.run_gremlin_now":   return try await runGremlinNow(input: input)
        case "gremlin-management.run_history":       return try await runHistory(input: input)
        case "gremlin-management.next_run":          return try await nextRun(input: input)
        case "gremlin-management.set_destination":   return try await setDestination(input: input)
        case "gremlin-management.duplicate_gremlin": return try await duplicateGremlin(input: input)
        case "gremlin-management.validate_schedule": return try await validateSchedule(input: input)
        default:
            throw AtlasToolError.invalidInput("Unknown action '\(actionID)'.")
        }
    }

    // MARK: - Action implementations

    private func createGremlin(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinCreateInput.self)
        guard !payload.name.trimmingCharacters(in: .whitespaces).isEmpty else {
            throw AtlasToolError.invalidInput("Gremlin name is required.")
        }
        guard !payload.prompt.trimmingCharacters(in: .whitespaces).isEmpty else {
            throw AtlasToolError.invalidInput("Gremlin prompt is required.")
        }

        // Validate schedule before committing
        let validation = await gremlinManager.validateSchedule(payload.schedule)
        guard validation.isValid else {
            throw AtlasToolError.invalidInput(validation.errorMessage ?? "Invalid schedule '\(payload.schedule)'.")
        }

        let destination = parseDestination(payload.destination)
        let id = slugify(payload.name)
        let item = GremlinItem(
            id: id,
            name: payload.name,
            emoji: payload.emoji ?? "⚡",
            prompt: payload.prompt,
            scheduleRaw: payload.schedule,
            isEnabled: true,
            sourceType: "chat",
            createdAt: isoDate(),
            communicationDestination: destination,
            gremlinDescription: payload.description,
            tags: payload.tags ?? [],
            maxRetries: payload.maxRetries ?? 0,
            timeoutSeconds: payload.timeoutSeconds,
            lastModifiedAt: isoDate()
        )

        try await gremlinManager.addGremlin(item)

        let nextFireDesc = validation.nextFireDates.first.map { "Next fire: \(formatDate($0))." } ?? ""
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.create_gremlin",
            output: try encodeJSON(item),
            success: true,
            summary: "Created Gremlin '\(payload.name)' (id: \(id)). \(nextFireDesc)"
        )
    }

    private func listGremlins() async throws -> SkillExecutionResult {
        let items = try await gremlinManager.loadGremlins()
        let output = try encodeJSON(items)
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.list_gremlins",
            output: output,
            success: true,
            summary: "\(items.count) gremlin\(items.count == 1 ? "" : "s") configured."
        )
    }

    private func getGremlin(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinIDInput.self)
        let items = try await gremlinManager.loadGremlins()
        guard let gremlin = items.first(where: { $0.id == payload.id }) else {
            throw AtlasToolError.invalidInput("Gremlin '\(payload.id)' not found.")
        }
        let runs = (try? await gremlinManager.runsForGremlin(gremlinID: payload.id, limit: 3)) ?? []

        struct GremlinDetail: Encodable {
            let gremlin: GremlinItem
            let recentRuns: [GremlinRun]
        }
        let detail = GremlinDetail(gremlin: gremlin, recentRuns: runs)
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.get_gremlin",
            output: try encodeJSON(detail),
            success: true,
            summary: "Fetched Gremlin '\(gremlin.name)' with \(runs.count) recent run\(runs.count == 1 ? "" : "s")."
        )
    }

    private func updateGremlin(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinUpdateInput.self)
        let items = try await gremlinManager.loadGremlins()
        guard let existing = items.first(where: { $0.id == payload.id }) else {
            throw AtlasToolError.invalidInput("Gremlin '\(payload.id)' not found.")
        }

        // Validate new schedule if provided
        let newSchedule = payload.schedule ?? existing.scheduleRaw
        if let schedule = payload.schedule {
            let validation = await gremlinManager.validateSchedule(schedule)
            guard validation.isValid else {
                throw AtlasToolError.invalidInput(validation.errorMessage ?? "Invalid schedule '\(schedule)'.")
            }
        }

        // Resolve destination update
        let destination: CommunicationDestination?
        if let destStr = payload.destination {
            destination = destStr.isEmpty ? nil : parseDestination(destStr)
        } else {
            destination = existing.communicationDestination
        }

        // Resolve timeout: 0 means clear
        let timeout: Int?
        if let t = payload.timeoutSeconds {
            timeout = t == 0 ? nil : t
        } else {
            timeout = existing.timeoutSeconds
        }

        let updated = GremlinItem(
            id: existing.id,
            name: payload.name ?? existing.name,
            emoji: payload.emoji ?? existing.emoji,
            prompt: payload.prompt ?? existing.prompt,
            scheduleRaw: newSchedule,
            isEnabled: payload.enabled ?? existing.isEnabled,
            sourceType: existing.sourceType,
            createdAt: existing.createdAt,
            workflowID: existing.workflowID,
            workflowInputValues: existing.workflowInputValues,
            communicationDestination: destination,
            telegramChatID: existing.telegramChatID,
            gremlinDescription: payload.description ?? existing.gremlinDescription,
            tags: payload.tags ?? existing.tags,
            maxRetries: payload.maxRetries ?? existing.maxRetries,
            timeoutSeconds: timeout,
            lastModifiedAt: isoDate()
        )

        try await gremlinManager.updateGremlin(updated)
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.update_gremlin",
            output: try encodeJSON(updated),
            success: true,
            summary: "Updated Gremlin '\(updated.name)'."
        )
    }

    private func deleteGremlin(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinIDInput.self)
        try await gremlinManager.deleteGremlin(id: payload.id)
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.delete_gremlin",
            output: "{\"deleted\": \"\(payload.id)\"}",
            success: true,
            summary: "Deleted Gremlin '\(payload.id)'."
        )
    }

    private func setEnabled(input: AtlasToolInput, enabled: Bool) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinIDInput.self)
        let items = try await gremlinManager.loadGremlins()
        guard let existing = items.first(where: { $0.id == payload.id }) else {
            throw AtlasToolError.invalidInput("Gremlin '\(payload.id)' not found.")
        }
        let updated = GremlinItem(
            id: existing.id, name: existing.name, emoji: existing.emoji,
            prompt: existing.prompt, scheduleRaw: existing.scheduleRaw,
            isEnabled: enabled, sourceType: existing.sourceType, createdAt: existing.createdAt,
            workflowID: existing.workflowID, workflowInputValues: existing.workflowInputValues,
            communicationDestination: existing.communicationDestination,
            telegramChatID: existing.telegramChatID,
            gremlinDescription: existing.gremlinDescription, tags: existing.tags,
            maxRetries: existing.maxRetries, timeoutSeconds: existing.timeoutSeconds,
            lastModifiedAt: isoDate()
        )
        try await gremlinManager.updateGremlin(updated)
        let verb = enabled ? "Enabled" : "Disabled"
        return SkillExecutionResult(
            skillID: "gremlin-management",
            actionID: enabled ? "gremlin-management.enable_gremlin" : "gremlin-management.disable_gremlin",
            output: try encodeJSON(updated),
            success: true,
            summary: "\(verb) Gremlin '\(existing.name)'."
        )
    }

    private func runGremlinNow(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinIDInput.self)
        let run = try await gremlinManager.runNow(id: payload.id)
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.run_gremlin_now",
            output: try encodeJSON(run),
            success: run.status == .success,
            summary: "Run completed with status '\(run.status.rawValue)'. \(run.output ?? run.errorMessage ?? "")"
        )
    }

    private func runHistory(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinRunHistoryInput.self)
        let limit = min(50, max(1, payload.limit ?? 10))
        let runs = try await gremlinManager.runsForGremlin(gremlinID: payload.id, limit: limit)
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.run_history",
            output: try encodeJSON(runs),
            success: true,
            summary: "\(runs.count) run\(runs.count == 1 ? "" : "s") returned for '\(payload.id)'."
        )
    }

    private func nextRun(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinIDInput.self)
        let items = try await gremlinManager.loadGremlins()
        guard let gremlin = items.first(where: { $0.id == payload.id }) else {
            throw AtlasToolError.invalidInput("Gremlin '\(payload.id)' not found.")
        }

        guard gremlin.isEnabled else {
            return SkillExecutionResult(
                skillID: "gremlin-management", actionID: "gremlin-management.next_run",
                output: "{\"enabled\": false, \"message\": \"This Gremlin is disabled.\"}",
                success: true,
                summary: "Gremlin '\(gremlin.name)' is disabled."
            )
        }

        let validation = await gremlinManager.validateSchedule(gremlin.scheduleRaw)
        guard validation.isValid, let nextDate = validation.nextFireDates.first else {
            return SkillExecutionResult(
                skillID: "gremlin-management", actionID: "gremlin-management.next_run",
                output: "{\"error\": \"\(validation.errorMessage ?? "Schedule could not be computed.")\"}",
                success: false,
                summary: "Could not compute next run for '\(gremlin.name)'."
            )
        }

        let secondsFromNow = Int(nextDate.timeIntervalSinceNow)
        let iso = isoDateTime(nextDate)

        struct NextRunResult: Encodable {
            let gremlinID: String
            let gremlinName: String
            let nextRunAt: String
            let secondsFromNow: Int
            let humanDescription: String
        }
        let result = NextRunResult(
            gremlinID: gremlin.id,
            gremlinName: gremlin.name,
            nextRunAt: iso,
            secondsFromNow: secondsFromNow,
            humanDescription: "'\(gremlin.name)' next fires \(humanCountdown(secondsFromNow))."
        )
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.next_run",
            output: try encodeJSON(result),
            success: true,
            summary: result.humanDescription
        )
    }

    private func setDestination(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinSetDestinationInput.self)
        let items = try await gremlinManager.loadGremlins()
        guard let existing = items.first(where: { $0.id == payload.id }) else {
            throw AtlasToolError.invalidInput("Gremlin '\(payload.id)' not found.")
        }

        let destination: CommunicationDestination?
        if let platform = payload.platform, !platform.isEmpty,
           let channelID = payload.channelID, !channelID.isEmpty,
           let chatPlatform = ChatPlatform(rawValue: platform) {
            destination = CommunicationDestination(platform: chatPlatform, channelID: channelID)
        } else {
            destination = nil
        }

        let updated = GremlinItem(
            id: existing.id, name: existing.name, emoji: existing.emoji,
            prompt: existing.prompt, scheduleRaw: existing.scheduleRaw,
            isEnabled: existing.isEnabled, sourceType: existing.sourceType,
            createdAt: existing.createdAt, workflowID: existing.workflowID,
            workflowInputValues: existing.workflowInputValues,
            communicationDestination: destination, telegramChatID: nil,
            gremlinDescription: existing.gremlinDescription, tags: existing.tags,
            maxRetries: existing.maxRetries, timeoutSeconds: existing.timeoutSeconds,
            lastModifiedAt: isoDate()
        )
        try await gremlinManager.updateGremlin(updated)

        let destDesc = destination.map { "\($0.platform.rawValue):\($0.channelID)" } ?? "cleared"
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.set_destination",
            output: try encodeJSON(updated),
            success: true,
            summary: "Destination for '\(existing.name)' \(destDesc)."
        )
    }

    private func duplicateGremlin(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinDuplicateInput.self)
        let items = try await gremlinManager.loadGremlins()
        guard let source = items.first(where: { $0.id == payload.id }) else {
            throw AtlasToolError.invalidInput("Gremlin '\(payload.id)' not found.")
        }

        let newSchedule = payload.newSchedule ?? source.scheduleRaw
        if payload.newSchedule != nil {
            let validation = await gremlinManager.validateSchedule(newSchedule)
            guard validation.isValid else {
                throw AtlasToolError.invalidInput(validation.errorMessage ?? "Invalid schedule '\(newSchedule)'.")
            }
        }

        let newID = slugify(payload.newName)
        let clone = GremlinItem(
            id: newID,
            name: payload.newName,
            emoji: source.emoji,
            prompt: source.prompt,
            scheduleRaw: newSchedule,
            isEnabled: false,  // clones start disabled to avoid accidental immediate runs
            sourceType: "chat",
            createdAt: isoDate(),
            workflowID: source.workflowID,
            workflowInputValues: source.workflowInputValues,
            communicationDestination: source.communicationDestination,
            gremlinDescription: source.gremlinDescription,
            tags: source.tags,
            maxRetries: source.maxRetries,
            timeoutSeconds: source.timeoutSeconds,
            lastModifiedAt: isoDate()
        )

        try await gremlinManager.addGremlin(clone)
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.duplicate_gremlin",
            output: try encodeJSON(clone),
            success: true,
            summary: "Duplicated '\(source.name)' → '\(payload.newName)' (id: \(newID)). Clone starts disabled."
        )
    }

    private func validateSchedule(input: AtlasToolInput) async throws -> SkillExecutionResult {
        let payload = try input.decode(GremlinValidateScheduleInput.self)
        let result = await gremlinManager.validateSchedule(payload.schedule)

        struct ValidationOutput: Encodable {
            let isValid: Bool
            let schedule: String
            let nextFireDates: [String]
            let errorMessage: String?
        }
        let output = ValidationOutput(
            isValid: result.isValid,
            schedule: payload.schedule,
            nextFireDates: result.nextFireDates.map { isoDateTime($0) },
            errorMessage: result.errorMessage
        )
        return SkillExecutionResult(
            skillID: "gremlin-management", actionID: "gremlin-management.validate_schedule",
            output: try encodeJSON(output),
            success: result.isValid,
            summary: result.isValid
                ? "Valid schedule. Next: \(result.nextFireDates.first.map { formatDate($0) } ?? "N/A")."
                : (result.errorMessage ?? "Invalid schedule.")
        )
    }

    // MARK: - Helpers

    private func encodeJSON<T: Encodable>(_ value: T) throws -> String {
        let data = try AtlasJSON.encoder.encode(value)
        guard let str = String(data: data, encoding: .utf8) else {
            throw AtlasToolError.executionFailed("Failed to encode output.")
        }
        return str
    }

    private func slugify(_ name: String) -> String {
        name.lowercased()
            .replacingOccurrences(of: " ", with: "-")
            .filter { $0.isLetter || $0.isNumber || $0 == "-" }
    }

    private func isoDate() -> String {
        let fmt = DateFormatter()
        fmt.dateFormat = "yyyy-MM-dd"
        return fmt.string(from: Date())
    }

    private func isoDateTime(_ date: Date) -> String {
        let fmt = ISO8601DateFormatter()
        fmt.formatOptions = [.withInternetDateTime]
        return fmt.string(from: date)
    }

    private func formatDate(_ date: Date) -> String {
        let fmt = DateFormatter()
        fmt.dateStyle = .medium
        fmt.timeStyle = .short
        return fmt.string(from: date)
    }

    private func humanCountdown(_ seconds: Int) -> String {
        if seconds < 60 { return "in \(seconds)s" }
        if seconds < 3600 { return "in \(seconds / 60)m" }
        if seconds < 86400 { return "in \(seconds / 3600)h \((seconds % 3600) / 60)m" }
        return "in \(seconds / 86400)d"
    }

    private func parseDestination(_ raw: String?) -> CommunicationDestination? {
        guard let raw, !raw.isEmpty else { return nil }
        let parts = raw.split(separator: ":", maxSplits: 1).map(String.init)
        guard parts.count == 2, let platform = ChatPlatform(rawValue: parts[0]) else { return nil }
        return CommunicationDestination(platform: platform, channelID: parts[1])
    }
}
