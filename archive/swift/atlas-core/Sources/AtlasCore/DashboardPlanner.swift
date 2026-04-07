import Foundation
import AtlasLogging
import AtlasShared
import AtlasSkills

// MARK: - DashboardPlannerError

public enum DashboardPlannerError: LocalizedError, Sendable {
    case missingAPIKey
    case noJSONFound
    case noAvailableActions([String])
    case validationFailed([String])

    public var errorDescription: String? {
        switch self {
        case .missingAPIKey:
            return "No AI API key available — cannot plan a dashboard without it."
        case .noJSONFound:
            return "The model did not return a valid DashboardSpec JSON object."
        case .noAvailableActions(let skillIDs):
            if skillIDs.isEmpty {
                return "No enabled skill actions are available for dashboard planning."
            }
            return "No enabled skill actions are available for: \(skillIDs.joined(separator: ", "))."
        case .validationFailed(let errs):
            return "Dashboard spec validation failed: \(errs.joined(separator: "; "))"
        }
    }
}

// MARK: - DashboardPlanner

/// Produces a DashboardProposal from a user intent string and a list of available skill IDs.
/// Uses the active AI provider via the `OpenAIQuerying` protocol, following the same pattern
/// used by `MindReflectionService` and `SkillsEngine`.
public struct DashboardPlanner: Sendable {
    private let openAI: any OpenAIQuerying
    private let actionCatalog: [SkillActionCatalogItem]
    private let model: String?
    private let logger = AtlasLogger(category: "dashboard.planner")

    public init(openAI: any OpenAIQuerying, actionCatalog: [SkillActionCatalogItem], model: String? = nil) {
        self.openAI = openAI
        self.actionCatalog = actionCatalog
        self.model = model
    }

    // MARK: - Plan

    /// Produce a pending DashboardProposal from user intent + available skill IDs.
    public func plan(intent: String, skillIDs: [String]) async throws -> DashboardProposal {
        let requestedSkillIDs = skillIDs.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty }
        let filteredCatalog = requestedSkillIDs.isEmpty
            ? actionCatalog
            : actionCatalog.filter { requestedSkillIDs.contains($0.skillID) }

        guard !filteredCatalog.isEmpty else {
            throw DashboardPlannerError.noAvailableActions(requestedSkillIDs)
        }

        let validator = DashboardValidator(actionCatalog: filteredCatalog)
        let systemPrompt = buildSystemPrompt(catalog: filteredCatalog)
        let userContent  = intent

        logger.info("Requesting dashboard plan from AI provider", metadata: [
            "intent_length": "\(intent.count)",
            "skill_count": "\(skillIDs.count)"
        ])

        let rawResponse = try await openAI.complete(systemPrompt: systemPrompt, userContent: userContent, model: model)

        // Extract JSON block from the response
        let specJSON: String
        if let extracted = extractJSONObject(from: rawResponse) {
            specJSON = extracted
        } else {
            logger.warning("Dashboard planner returned non-JSON response; attempting repair", metadata: [
                "response_preview": summarize(rawResponse)
            ])
            let repaired = try await repairResponse(rawResponse, catalog: filteredCatalog)
            guard let extracted = extractJSONObject(from: repaired) else {
                logger.error("No JSON object found in AI response for dashboard planning", metadata: [
                    "response_preview": summarize(rawResponse)
                ])
                throw DashboardPlannerError.noJSONFound
            }
            specJSON = extracted
        }

        // Decode spec
        let data = Data(specJSON.utf8)
        let spec: DashboardSpec
        do {
            spec = try AtlasJSON.decoder.decode(DashboardSpec.self, from: data)
        } catch {
            logger.warning("Dashboard planner returned malformed JSON; attempting repair", metadata: [
                "error": error.localizedDescription
            ])
            let repaired = try await repairResponse(specJSON, catalog: filteredCatalog)
            guard let repairedJSON = extractJSONObject(from: repaired) else {
                logger.error("Failed to decode DashboardSpec from AI response", metadata: [
                    "error": error.localizedDescription
                ])
                throw DashboardPlannerError.noJSONFound
            }
            let repairedData = Data(repairedJSON.utf8)
            do {
                spec = try AtlasJSON.decoder.decode(DashboardSpec.self, from: repairedData)
            } catch {
                logger.error("Failed to decode repaired DashboardSpec from AI response", metadata: [
                    "error": error.localizedDescription
                ])
                throw DashboardPlannerError.noJSONFound
            }
        }

        let normalizedSpec = normalize(spec: spec, against: filteredCatalog)

        // Validate
        let result = validator.validate(normalizedSpec)
        guard result.isValid else {
            logger.error("Dashboard spec validation failed", metadata: [
                "errors": result.errors.joined(separator: "; ")
            ])
            throw DashboardPlannerError.validationFailed(result.errors)
        }

        let proposal = DashboardProposal(
            spec: normalizedSpec,
            summary: "Dashboard generated for intent: \(intent)",
            rationale: "Atlas inferred this dashboard layout from the requested capabilities and available skills.",
            status: .pending,
            createdAt: .now
        )

        logger.info("Dashboard proposal created", metadata: [
            "proposal_id": proposal.proposalID,
            "spec_id": spec.id,
            "widget_count": "\(spec.widgets.count)"
        ])

        return proposal
    }

    // MARK: - Prompt Building

    private func buildSystemPrompt(catalog: [SkillActionCatalogItem]) -> String {
        let grouped = Dictionary(grouping: catalog, by: \.skillID)
        let sortedSkillIDs = grouped.keys.sorted()
        let skillList = sortedSkillIDs.map { skillID in
            guard let actions = grouped[skillID]?.sorted(by: { $0.action.id < $1.action.id }),
                  let first = actions.first else {
                return "  - \(skillID)"
            }

            let header = "- \(skillID) (\(first.skillName))"
            let actionLines = actions.map { item in
                let required = item.action.inputSchema.required.isEmpty
                    ? "none"
                    : item.action.inputSchema.required.joined(separator: ", ")
                let inputKeys = item.action.inputSchema.properties.keys.sorted()
                let inputSummary = inputKeys.isEmpty ? "none" : inputKeys.joined(separator: ", ")
                return """
                  action=\(item.action.id)
                  requiredInputs=\(required)
                  inputKeys=\(inputSummary)
                  outputShape=\(item.action.outputSchemaSummary)
                """
            }.joined(separator: "\n")
            return "\(header)\n  skillDescription=\(first.skillDescription)\n\(actionLines)"
        }.joined(separator: "\n")

        return """
        You are a dashboard planner for Atlas.
        Return exactly one valid DashboardSpec JSON object.
        Do not return markdown. Do not explain your work. Do not wrap in code fences.

        Allowed widget types:
        - stat_card
        - summary
        - list
        - table
        - form
        - search

        Widget rules:
        - Every widget must use a real skillID from the catalog below.
        - Every widget must use a real action for that skill.
        - skillID is the skill identifier such as "web-research" or "finance". It is not the same as an action ID.
        - For stat_card, summary, list, and table widgets, provide defaultInputs for every required input.
        - Use the exact input key names from the catalog.
        - Always include a binding object for stat_card, summary, list, and table widgets.
        - For stat_card and summary, binding.valuePath or binding.summaryPath should point at the display field.
        - For list widgets, binding.itemsPath should point to the array and primaryTextPath / secondaryTextPath should describe how each item should render.
        - For table widgets, binding.rowsPath should point to the array of rows.
        - Use dataKey only for backward compatibility; prefer binding.
        - table widgets need columns.
        - form and search widgets need fields.

        Real catalog:
        \(skillList)

        JSON shape:
        {
          "id": "string",
          "title": "string",
          "icon": "string",
          "description": "string",
          "sourceSkillIDs": ["skillID"],
          "widgets": [
            {
              "id": "string",
              "type": "stat_card|summary|list|table|form|search",
              "title": "string",
              "skillID": "string",
              "action": "string",
              "defaultInputs": { "key": "value" },
              "binding": {
                "valuePath": "string",
                "itemsPath": "string",
                "rowsPath": "string",
                "primaryTextPath": "string",
                "secondaryTextPath": "string",
                "tertiaryTextPath": "string",
                "linkPath": "string",
                "imagePath": "string",
                "summaryPath": "string"
              },
              "dataKey": "string",
              "fields": [
                { "key": "string", "label": "string", "type": "text|number|select|date", "required": true, "options": ["string"] }
              ],
              "columns": ["string"],
              "emptyMessage": "string"
            }
          ],
          "emptyState": "string"
        }

        Good example:
        {
          "id": "weather-dashboard",
          "title": "Weather Dashboard",
          "icon": "cloud.sun.fill",
          "description": "Current weather and short forecast.",
          "sourceSkillIDs": ["weather"],
          "widgets": [
            {
              "id": "current-weather",
              "type": "stat_card",
              "title": "Current Temperature",
              "skillID": "weather",
              "action": "weather.current",
              "defaultInputs": { "locationQuery": "Orlando, FL" },
              "binding": { "valuePath": "temperature" },
              "dataKey": "temperature",
              "emptyMessage": "Weather unavailable"
            }
          ],
          "emptyState": "No dashboard data available."
        }
        """
    }

    private func repairResponse(_ rawResponse: String, catalog: [SkillActionCatalogItem]) async throws -> String {
        let repairPrompt = """
        Rewrite the following content as exactly one valid DashboardSpec JSON object.
        Keep only valid JSON. No markdown. No code fences. No explanation.
        Use only real skill IDs, action IDs, and input keys from this catalog summary:
        \(compactCatalogSummary(catalog))
        """
        return try await openAI.complete(systemPrompt: repairPrompt, userContent: rawResponse)
    }

    private func compactCatalogSummary(_ catalog: [SkillActionCatalogItem]) -> String {
        Dictionary(grouping: catalog, by: \.skillID)
            .sorted { $0.key < $1.key }
            .map { skillID, items in
                let actions = items
                    .sorted { $0.action.id < $1.action.id }
                    .map { item in
                        let required = item.action.inputSchema.required.sorted().joined(separator: ",")
                        let keys = item.action.inputSchema.properties.keys.sorted().joined(separator: ",")
                        return "\(item.action.id)[required=\(required.isEmpty ? "none" : required);keys=\(keys.isEmpty ? "none" : keys)]"
                    }
                    .joined(separator: " | ")
                return "\(skillID): \(actions)"
            }
            .joined(separator: "\n")
    }

    private func normalize(spec: DashboardSpec, against catalog: [SkillActionCatalogItem]) -> DashboardSpec {
        let actionToSkillIDs = Dictionary(grouping: catalog, by: { $0.action.id })
            .mapValues { Array(Set($0.map(\.skillID))).sorted() }

        let normalizedWidgets = spec.widgets.map { widget -> DashboardWidget in
            let normalizedDefaultInputs = normalize(defaultInputs: widget.defaultInputs)
            let normalizedBinding = normalizeBinding(for: widget)
            guard
                let action = widget.action?.trimmingCharacters(in: .whitespacesAndNewlines),
                !action.isEmpty,
                let matchingSkillIDs = actionToSkillIDs[action],
                matchingSkillIDs.count == 1
            else {
                return DashboardWidget(
                    id: widget.id,
                    type: widget.type,
                    title: widget.title,
                    skillID: widget.skillID,
                    action: widget.action,
                    dataKey: widget.dataKey,
                    defaultInputs: normalizedDefaultInputs,
                    binding: normalizedBinding,
                    fields: widget.fields,
                    columns: widget.columns,
                    emptyMessage: widget.emptyMessage
                )
            }

            let expectedSkillID = matchingSkillIDs[0]
            guard widget.skillID != expectedSkillID || normalizedDefaultInputs != widget.defaultInputs else {
                return widget
            }

            return DashboardWidget(
                id: widget.id,
                type: widget.type,
                title: widget.title,
                skillID: expectedSkillID,
                action: action,
                dataKey: widget.dataKey,
                defaultInputs: normalizedDefaultInputs,
                binding: normalizedBinding,
                fields: widget.fields,
                columns: widget.columns,
                emptyMessage: widget.emptyMessage
            )
        }

        let normalizedSourceSkillIDs = Array(Set(normalizedWidgets.map(\.skillID))).sorted()

        return DashboardSpec(
            id: spec.id,
            title: spec.title,
            icon: spec.icon,
            description: spec.description,
            sourceSkillIDs: normalizedSourceSkillIDs,
            widgets: normalizedWidgets,
            emptyState: spec.emptyState,
            isPinned: spec.isPinned,
            lastAccessedAt: spec.lastAccessedAt
        )
    }

    private func normalize(defaultInputs: [String: String]?) -> [String: String]? {
        guard let defaultInputs else { return nil }
        var normalized = defaultInputs

        if let value = normalized["temperatureUnit"] {
            switch value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
            case "f":
                normalized["temperatureUnit"] = "fahrenheit"
            case "c":
                normalized["temperatureUnit"] = "celsius"
            default:
                break
            }
        }

        return normalized
    }

    private func normalizeBinding(for widget: DashboardWidget) -> DashboardWidgetBinding? {
        if let binding = widget.binding {
            return binding
        }

        switch widget.type {
        case .statCard:
            guard let dataKey = widget.dataKey else { return nil }
            return DashboardWidgetBinding(valuePath: dataKey)
        case .summary:
            if let dataKey = widget.dataKey {
                return DashboardWidgetBinding(valuePath: dataKey, summaryPath: dataKey)
            }
            return nil
        case .list:
            if let dataKey = widget.dataKey {
                return DashboardWidgetBinding(itemsPath: dataKey)
            }
            return nil
        case .table:
            if let dataKey = widget.dataKey {
                return DashboardWidgetBinding(rowsPath: dataKey)
            }
            return nil
        case .form, .search:
            return widget.binding
        }
    }

    // MARK: - JSON Extraction

    /// Extracts the first valid JSON object `{...}` from raw text (handles markdown code fences).
    private func extractJSONObject(from text: String) -> String? {
        let stripped = stripMarkdownCodeFence(from: text)
        guard let start = stripped.firstIndex(of: "{") else { return nil }
        var depth = 0
        var end: String.Index?
        var inString = false
        var isEscaped = false

        for (offset, char) in stripped[start...].enumerated() {
            let index = stripped.index(start, offsetBy: offset)

            if inString {
                if isEscaped {
                    isEscaped = false
                    continue
                }
                if char == "\\" {
                    isEscaped = true
                } else if char == "\"" {
                    inString = false
                }
                continue
            }

            if char == "\"" {
                inString = true
                continue
            }

            if char == "{" {
                depth += 1
            } else if char == "}" {
                depth -= 1
                if depth == 0 {
                    end = index
                    break
                }
            }
        }

        guard let end else { return nil }
        return String(stripped[start...end])
    }

    private func stripMarkdownCodeFence(from text: String) -> String {
        var result = text
        // Remove ```json ... ``` and ``` ... ```
        if let r = result.range(of: "```json\n") { result.removeSubrange(r) }
        if let r = result.range(of: "```\n") { result.removeSubrange(r) }
        if let r = result.range(of: "\n```") { result.removeSubrange(r) }
        if let r = result.range(of: "```") { result.removeSubrange(r) }
        return result.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func summarize(_ text: String, limit: Int = 280) -> String {
        let squashed = text.replacingOccurrences(of: "\n", with: " ").trimmingCharacters(in: .whitespacesAndNewlines)
        return String(squashed.prefix(limit))
    }
}
