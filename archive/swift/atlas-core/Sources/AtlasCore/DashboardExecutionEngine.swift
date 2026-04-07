import Foundation
import AtlasLogging
import AtlasShared
import AtlasSkills

// MARK: - DotPathExtractor

/// Extracts a value at a dot-separated path from a JSON string.
/// e.g. "current.temperature" from {"current":{"temperature":"72F"}} → "72F"
/// Returns nil when the input is not valid JSON or when the path is not found.
public struct DotPathExtractor: Sendable {

    public init() {}

    /// Attempt to extract a dot-path value from a JSON string.
    /// - Parameters:
    ///   - path: Dot-separated key path, e.g. "current.temperature"
    ///   - json: Raw JSON string (object or any valid JSON)
    /// - Returns: A string representation of the leaf value, or nil if not found.
    public func extract(path: String, from json: String) -> String? {
        guard let root = parseRoot(from: json),
              let value = value(path: path, from: root) else {
            return nil
        }
        return leafString(from: value)
    }

    func parseRoot(from json: String) -> Any? {
        guard let data = json.data(using: .utf8) else { return nil }
        return try? JSONSerialization.jsonObject(with: data, options: [])
    }

    func value(path: String, from root: Any) -> Any? {
        guard !path.isEmpty else { return nil }

        let keys = path.split(separator: ".").map(String.init)
        var current: Any = root

        for key in keys {
            if let dict = current as? [String: Any] {
                guard let next = dict[key] else { return nil }
                current = next
            } else if let arr = current as? [Any], let idx = Int(key), idx < arr.count {
                current = arr[idx]
            } else {
                return nil
            }
        }

        return current
    }

    func leafString(from value: Any) -> String? {
        switch value {
        case let s as String:
            return s
        case let n as NSNumber:
            // Distinguish bool from numeric
            if CFGetTypeID(n) == CFBooleanGetTypeID() {
                return n.boolValue ? "true" : "false"
            }
            return n.stringValue
        case is NSNull:
            return nil
        default:
            // For nested objects/arrays, serialize them back to JSON string
            if let data = try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys]),
               let str = String(data: data, encoding: .utf8) {
                return str
            }
            return "\(value)"
        }
    }
}

// MARK: - DashboardInputCoercion

struct DashboardInputCoercion {

    static func argumentsJSON(
        defaultInputs: [String: String]?,
        overrideInputs: [String: String],
        catalogItem: SkillActionCatalogItem?
    ) -> String {
        var mergedInputs = defaultInputs ?? [:]
        for (key, value) in overrideInputs {
            mergedInputs[key] = value
        }

        guard !mergedInputs.isEmpty else { return "{}" }

        let object = coerce(inputs: mergedInputs, schema: catalogItem?.action.inputSchema)
        guard
            JSONSerialization.isValidJSONObject(object),
            let data = try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]),
            let string = String(data: data, encoding: .utf8)
        else {
            return "{}"
        }

        return string
    }

    static func coerce(inputs: [String: String], schema: AtlasToolInputSchema?) -> [String: Any] {
        guard let schema else {
            return inputs
        }

        var coerced: [String: Any] = [:]
        for (key, value) in inputs {
            let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
            let property = schema.properties[key]
            coerced[key] = coerce(value: trimmed, key: key, property: property)
        }
        return coerced
    }

    private static func coerce(value: String, key: String, property: AtlasToolInputProperty?) -> Any {
        guard let property else {
            return normalizedString(value, forKey: key)
        }

        switch property.type.lowercased() {
        case "integer":
            return Int(value) ?? normalizedString(value, forKey: key)
        case "number":
            return Double(value) ?? normalizedString(value, forKey: key)
        case "boolean":
            if let bool = parseBoolean(value) {
                return bool
            }
            return normalizedString(value, forKey: key)
        case "array":
            return parseArray(value, itemType: property.items?.type)
        default:
            return normalizedString(value, forKey: key)
        }
    }

    private static func normalizedString(_ value: String, forKey key: String) -> String {
        switch key {
        case "temperatureUnit":
            switch value.lowercased() {
            case "f", "fahrenheit":
                return "fahrenheit"
            case "c", "celsius":
                return "celsius"
            default:
                return value
            }
        case "windSpeedUnit":
            switch value.lowercased() {
            case "km/h", "kph":
                return "kmh"
            case "m/s":
                return "ms"
            default:
                return value.lowercased()
            }
        default:
            return value
        }
    }

    private static func parseBoolean(_ value: String) -> Bool? {
        switch value.lowercased() {
        case "true", "yes", "1", "on":
            return true
        case "false", "no", "0", "off":
            return false
        default:
            return nil
        }
    }

    private static func parseArray(_ value: String, itemType: String?) -> [Any] {
        if
            let data = value.data(using: .utf8),
            let parsed = try? JSONSerialization.jsonObject(with: data),
            let array = parsed as? [Any] {
            return array.map { coerceArrayItem($0, itemType: itemType) }
        }

        let components = value
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }

        return components.map { coerceArrayItem($0, itemType: itemType) }
    }

    private static func coerceArrayItem(_ value: Any, itemType: String?) -> Any {
        guard let itemType else { return value }

        let stringValue: String
        if let string = value as? String {
            stringValue = string
        } else {
            stringValue = "\(value)"
        }

        switch itemType.lowercased() {
        case "integer":
            return Int(stringValue) ?? stringValue
        case "number":
            return Double(stringValue) ?? stringValue
        case "boolean":
            return parseBoolean(stringValue) ?? stringValue
        default:
            return stringValue
        }
    }
}

// MARK: - DashboardDisplayBinding

struct DashboardDisplayBinder {
    private let extractor = DotPathExtractor()

    func bind(widget: DashboardWidget, rawOutput: String) -> DashboardDisplayPayload? {
        if let bindingPayload = bindWithBinding(widget: widget, rawOutput: rawOutput) {
            return bindingPayload
        }

        // Backward-compatible fallback for older widgets.
        switch widget.type {
        case .statCard:
            if let dataKey = widget.dataKey, let value = extractor.extract(path: dataKey, from: rawOutput) {
                return DashboardDisplayPayload(value: value)
            }
            return rawOutput.isEmpty ? nil : DashboardDisplayPayload(value: rawOutput)
        case .summary:
            if let dataKey = widget.dataKey, let value = extractor.extract(path: dataKey, from: rawOutput) {
                return DashboardDisplayPayload(value: value, summary: value)
            }
            return rawOutput.isEmpty ? nil : DashboardDisplayPayload(summary: rawOutput)
        case .list:
            guard let root = extractor.parseRoot(from: rawOutput) else { return nil }
            let itemsNode = firstArrayCandidate(in: root)
            guard let items = itemsNode as? [Any] else { return nil }
            let displayItems = items.compactMap { summarizeItem($0, binding: nil) }
            return displayItems.isEmpty ? nil : DashboardDisplayPayload(items: displayItems)
        case .table:
            guard let root = extractor.parseRoot(from: rawOutput) else { return nil }
            let rowsNode = firstArrayCandidate(in: root)
            guard let rows = rowsNode as? [Any] else { return nil }
            let displayRows = rows.compactMap { summarizeRow($0, columns: widget.columns) }
            return displayRows.isEmpty ? nil : DashboardDisplayPayload(rows: displayRows)
        case .form, .search:
            return nil
        }
    }

    private func bindWithBinding(widget: DashboardWidget, rawOutput: String) -> DashboardDisplayPayload? {
        guard let binding = widget.binding,
              let root = extractor.parseRoot(from: rawOutput) else {
            return nil
        }

        switch widget.type {
        case .statCard:
            guard let path = binding.valuePath ?? widget.dataKey else { return nil }
            guard let value = resolveString(path: path, from: root) else { return nil }
            return DashboardDisplayPayload(value: value)
        case .summary:
            let path = binding.summaryPath ?? binding.valuePath ?? widget.dataKey
            guard let path, let summary = resolveString(path: path, from: root) else { return nil }
            return DashboardDisplayPayload(value: summary, summary: summary)
        case .list:
            let itemsNode = resolveNode(path: binding.itemsPath, from: root) ?? firstArrayCandidate(in: root)
            guard let items = itemsNode as? [Any] else { return nil }
            let displayItems = items.compactMap { summarizeItem($0, binding: binding) }
            return displayItems.isEmpty ? nil : DashboardDisplayPayload(items: displayItems)
        case .table:
            let rowsNode = resolveNode(path: binding.rowsPath, from: root) ?? firstArrayCandidate(in: root)
            guard let rows = rowsNode as? [Any] else { return nil }
            let displayRows = rows.compactMap { summarizeRow($0, columns: widget.columns) }
            return displayRows.isEmpty ? nil : DashboardDisplayPayload(rows: displayRows)
        case .form, .search:
            return nil
        }
    }

    private func resolveNode(path: String?, from root: Any) -> Any? {
        guard let path, !path.isEmpty else { return nil }
        return extractor.value(path: path, from: root)
    }

    private func resolveString(path: String?, from root: Any) -> String? {
        guard let path, !path.isEmpty, let node = extractor.value(path: path, from: root) else { return nil }
        return extractor.leafString(from: node)
    }

    private func firstArrayCandidate(in root: Any) -> Any? {
        if let array = root as? [Any] {
            return array
        }
        guard let dict = root as? [String: Any] else { return nil }
        for key in ["results", "items", "sources", "rows", "data", "articles", "keyPoints", "caveats"] {
            if let array = dict[key] as? [Any] {
                return array
            }
        }
        return dict.values.first(where: { $0 is [Any] })
    }

    private func summarizeItem(_ value: Any, binding: DashboardWidgetBinding?) -> DashboardDisplayItem? {
        if let string = extractor.leafString(from: value), !(value is [String: Any]), !(value is [Any]) {
            return DashboardDisplayItem(primaryText: string)
        }

        guard let record = value as? [String: Any] else {
            return extractor.leafString(from: value).map { DashboardDisplayItem(primaryText: $0) }
        }

        let primary = boundOrHeuristicString(
            explicitPath: binding?.primaryTextPath,
            record: record,
            fallbackKeys: ["title", "name", "headline", "primaryText", "url", "symbol"]
        )

        guard let primary else { return nil }

        let secondary = boundOrHeuristicString(
            explicitPath: binding?.secondaryTextPath,
            record: record,
            fallbackKeys: ["domain", "snippet", "summary", "description", "secondaryText", "resolvedLocationName"]
        )

        let tertiary = boundOrHeuristicString(
            explicitPath: binding?.tertiaryTextPath,
            record: record,
            fallbackKeys: ["publishedAt", "date", "observationTime", "tertiaryText"]
        )

        let link = boundOrHeuristicString(
            explicitPath: binding?.linkPath,
            record: record,
            fallbackKeys: ["url", "link", "linkURL"]
        )

        let image = boundOrHeuristicString(
            explicitPath: binding?.imagePath,
            record: record,
            fallbackKeys: ["image", "imageURL", "thumbnailURL"]
        )

        return DashboardDisplayItem(
            primaryText: primary,
            secondaryText: secondary,
            tertiaryText: tertiary,
            linkURL: link,
            imageURL: image
        )
    }

    private func summarizeRow(_ value: Any, columns: [String]?) -> DashboardDisplayTableRow? {
        if let array = value as? [Any] {
            let values = array.compactMap { extractor.leafString(from: $0) }
            return values.isEmpty ? nil : DashboardDisplayTableRow(values: values)
        }

        guard let record = value as? [String: Any] else {
            return extractor.leafString(from: value).map { DashboardDisplayTableRow(values: [$0]) }
        }

        let orderedKeys = (columns?.isEmpty == false ? columns! : record.keys.sorted())
        let values = orderedKeys.map { key in
            extractor.leafString(from: record[key] as Any) ?? ""
        }
        return values.allSatisfy(\.isEmpty) ? nil : DashboardDisplayTableRow(values: values)
    }

    private func boundOrHeuristicString(
        explicitPath: String?,
        record: [String: Any],
        fallbackKeys: [String]
    ) -> String? {
        if let explicitPath, !explicitPath.isEmpty {
            if explicitPath.contains(".") {
                return extractor.value(path: explicitPath, from: record).flatMap { extractor.leafString(from: $0) }
            }
            return record[explicitPath].flatMap { extractor.leafString(from: $0) }
        }

        for key in fallbackKeys {
            if let value = record[key], let string = extractor.leafString(from: value), !string.isEmpty {
                return string
            }
        }
        return nil
    }
}

struct DashboardDisplayRepairer {
    let openAI: (any OpenAIQuerying)?

    func repair(widget: DashboardWidget, rawOutput: String) async -> DashboardDisplayPayload? {
        guard let openAI, !rawOutput.isEmpty else { return nil }

        let systemPrompt = """
        You normalize dashboard widget data for Atlas.
        Return exactly one valid JSON object matching this shape:
        {
          "value": "string or null",
          "summary": "string or null",
          "items": [
            {
              "primaryText": "string",
              "secondaryText": "string or null",
              "tertiaryText": "string or null",
              "linkURL": "string or null",
              "imageURL": "string or null"
            }
          ],
          "rows": [
            { "values": ["string"] }
          ]
        }
        Use only fields that make sense for the widget type "\(widget.type.rawValue)".
        No markdown. No explanation.
        """

        let userContent = """
        Widget title: \(widget.title)
        Widget type: \(widget.type.rawValue)
        Binding: \(bindingDescription(widget.binding))
        Raw output:
        \(rawOutput)
        """

        do {
            let response = try await openAI.complete(systemPrompt: systemPrompt, userContent: userContent)
            guard let json = extractJSONObject(from: response),
                  let data = json.data(using: .utf8) else { return nil }
            return try? AtlasJSON.decoder.decode(DashboardDisplayPayload.self, from: data)
        } catch {
            return nil
        }
    }

    private func bindingDescription(_ binding: DashboardWidgetBinding?) -> String {
        guard let binding else { return "none" }
        let pairs: [(String, String?)] = [
            ("valuePath", binding.valuePath),
            ("itemsPath", binding.itemsPath),
            ("rowsPath", binding.rowsPath),
            ("primaryTextPath", binding.primaryTextPath),
            ("secondaryTextPath", binding.secondaryTextPath),
            ("tertiaryTextPath", binding.tertiaryTextPath),
            ("linkPath", binding.linkPath),
            ("imagePath", binding.imagePath),
            ("summaryPath", binding.summaryPath)
        ]
        return pairs.compactMap { key, value in value.map { "\(key)=\($0)" } }.joined(separator: ", ")
    }

    private func extractJSONObject(from text: String) -> String? {
        guard let start = text.firstIndex(of: "{") else { return nil }
        var depth = 0
        var inString = false
        var escaped = false

        for (offset, char) in text[start...].enumerated() {
            let index = text.index(start, offsetBy: offset)
            if inString {
                if escaped {
                    escaped = false
                    continue
                }
                if char == "\\" {
                    escaped = true
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
                    return String(text[start...index])
                }
            }
        }
        return nil
    }
}

// MARK: - DashboardExecutionEngine

/// Handles widget action execution for installed dashboards.
/// Bridges the Dashboard system with the SkillExecutionGateway.
public struct DashboardExecutionEngine: Sendable {

    private let gateway: SkillExecutionGateway
    private let actionsBySkillAndAction: [String: [String: SkillActionCatalogItem]]
    private let repairer: DashboardDisplayRepairer
    private let binder = DashboardDisplayBinder()
    private let extractor = DotPathExtractor()
    private let logger: AtlasLogger

    public init(
        gateway: SkillExecutionGateway,
        actionCatalog: [SkillActionCatalogItem] = [],
        openAI: (any OpenAIQuerying)? = nil,
        logger: AtlasLogger = AtlasLogger(category: "dashboard.execution")
    ) {
        self.gateway = gateway
        self.actionsBySkillAndAction = Dictionary(grouping: actionCatalog, by: \.skillID)
            .mapValues { Dictionary(uniqueKeysWithValues: $0.map { ($0.action.id, $0) }) }
        self.repairer = DashboardDisplayRepairer(openAI: openAI)
        self.logger = logger
    }

    // MARK: - Execute Widget

    /// Execute a single widget action and return the result.
    /// - Parameters:
    ///   - widget: The widget definition from the installed DashboardSpec.
    ///   - inputs: Additional inputs from the caller (e.g. form submission), merged over defaultInputs.
    ///   - skillContext: The SkillExecutionContext to use (built by AgentRuntime).
    public func execute(
        widget: DashboardWidget,
        inputs: [String: String],
        skillContext: SkillExecutionContext
    ) async -> WidgetExecutionResult {
        guard let action = widget.action, !action.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            logger.warning("Widget has no action configured", metadata: ["widget_id": widget.id])
            return WidgetExecutionResult(
                widgetID: widget.id,
                rawOutput: "",
                extractedValue: nil,
                displayPayload: nil,
                success: false,
                error: DashboardExecutionError.widgetNotExecutable(widget.id).localizedDescription
            )
        }

        let catalogItem = actionsBySkillAndAction[widget.skillID]?[action]
        let argumentsJSON = DashboardInputCoercion.argumentsJSON(
            defaultInputs: widget.defaultInputs,
            overrideInputs: inputs,
            catalogItem: catalogItem
        )

        let request = SkillExecutionRequest(
            skillID: widget.skillID,
            actionID: action,
            input: AtlasToolInput(argumentsJSON: argumentsJSON),
            conversationID: nil,
            toolCallID: UUID()
        )

        do {
            let result = try await gateway.execute(request, context: skillContext)

            logger.info("Widget skill executed", metadata: [
                "widget_id": widget.id,
                "skill_id": widget.skillID,
                "action": action,
                "success": "\(result.success)"
            ])

            let extracted: String?
            if let dataKey = widget.dataKey, !dataKey.isEmpty {
                extracted = extractor.extract(path: dataKey, from: result.output)
                if extracted == nil {
                    logger.info("Dot-path extraction returned nil", metadata: [
                        "widget_id": widget.id,
                        "data_key": dataKey
                    ])
                }
            } else {
                extracted = nil
            }

            let displayPayload = await displayPayload(for: widget, rawOutput: result.output, extractedValue: extracted)

            return WidgetExecutionResult(
                widgetID: widget.id,
                rawOutput: result.output,
                extractedValue: extracted,
                displayPayload: displayPayload,
                success: result.success,
                error: result.success ? nil : result.output
            )
        } catch {
            logger.error("Widget skill execution failed", metadata: [
                "widget_id": widget.id,
                "skill_id": widget.skillID,
                "action": action,
                "error": error.localizedDescription
            ])

            return WidgetExecutionResult(
                widgetID: widget.id,
                rawOutput: "",
                extractedValue: nil,
                displayPayload: nil,
                success: false,
                error: error.localizedDescription
            )
        }
    }

    private func displayPayload(
        for widget: DashboardWidget,
        rawOutput: String,
        extractedValue: String?
    ) async -> DashboardDisplayPayload? {
        if let bound = binder.bind(widget: widget, rawOutput: rawOutput) {
            return bound
        }

        if let extractedValue, widget.type == .statCard || widget.type == .summary {
            return widget.type == .summary
                ? DashboardDisplayPayload(value: extractedValue, summary: extractedValue)
                : DashboardDisplayPayload(value: extractedValue)
        }

        let repaired = await repairer.repair(widget: widget, rawOutput: rawOutput)
        if repaired != nil {
            logger.info("Model repaired dashboard widget payload", metadata: [
                "widget_id": widget.id,
                "widget_type": widget.type.rawValue
            ])
        }
        return repaired
    }
}
