import Foundation
import AtlasShared
import AtlasSkills

// MARK: - DashboardValidationResult

public struct DashboardValidationResult: Sendable {
    public let isValid: Bool
    public let errors: [String]

    public init(isValid: Bool, errors: [String]) {
        self.isValid = isValid
        self.errors = errors
    }
}

// MARK: - DashboardValidator

/// Validates a DashboardSpec against the schema rules before it can be installed.
public struct DashboardValidator: Sendable {
    private let actionCatalog: [SkillActionCatalogItem]
    private let actionsBySkillID: [String: [String: SkillActionCatalogItem]]

    public init(actionCatalog: [SkillActionCatalogItem] = []) {
        self.actionCatalog = actionCatalog
        self.actionsBySkillID = Dictionary(
            uniqueKeysWithValues: Dictionary(grouping: actionCatalog, by: \.skillID).map { skillID, items in
                (skillID, Dictionary(uniqueKeysWithValues: items.map { ($0.action.id, $0) }))
            }
        )
    }

    /// Validate the given spec. Returns a result with isValid and any error messages.
    public func validate(_ spec: DashboardSpec) -> DashboardValidationResult {
        var errors: [String] = []

        // Rule 1: spec.id must be non-empty
        if spec.id.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            errors.append("Dashboard spec id must not be empty.")
        }

        // Rule 2: spec.title must be non-empty
        if spec.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            errors.append("Dashboard title must not be empty.")
        }

        // Rule 3: sourceSkillIDs must all be non-empty strings
        if spec.sourceSkillIDs.isEmpty {
            errors.append("Dashboard must declare at least one source skill ID.")
        }
        for skillID in spec.sourceSkillIDs where skillID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            errors.append("All sourceSkillIDs must be non-empty strings.")
            break
        }
        if !actionsBySkillID.isEmpty {
            for skillID in spec.sourceSkillIDs where actionsBySkillID[skillID] == nil {
                errors.append("Dashboard source skill '\(skillID)' is not currently enabled or has no dashboard-usable actions.")
            }
        }

        // Rule 4: widgets must not be empty
        if spec.widgets.isEmpty {
            errors.append("Dashboard must have at least one widget.")
        }

        // Rule 5: each widget must be valid
        for widget in spec.widgets {
            validateWidget(widget, sourceSkillIDs: spec.sourceSkillIDs, errors: &errors)
        }

        return DashboardValidationResult(isValid: errors.isEmpty, errors: errors)
    }

    private func validateWidget(_ widget: DashboardWidget, sourceSkillIDs: [String], errors: inout [String]) {
        let prefix = "Widget '\(widget.id)'"

        // Non-empty id
        if widget.id.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            errors.append("A widget has an empty id.")
        }

        // Non-empty title
        if widget.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            errors.append("\(prefix): title must not be empty.")
        }

        // Non-empty skillID
        if widget.skillID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            errors.append("\(prefix): skillID must not be empty.")
        } else if !sourceSkillIDs.contains(widget.skillID) {
            // Rule: widget skillID must be in sourceSkillIDs
            errors.append("\(prefix): skillID '\(widget.skillID)' is not listed in sourceSkillIDs.")
        } else if !actionsBySkillID.isEmpty, actionsBySkillID[widget.skillID] == nil {
            errors.append("\(prefix): skillID '\(widget.skillID)' is not currently enabled or has no available actions.")
        }

        // Executable widget types must have a non-empty action
        let executableTypes: Set<DashboardWidgetType> = [.statCard, .summary, .list, .table, .form, .search]
        if executableTypes.contains(widget.type) {
            guard let action = widget.action?.trimmingCharacters(in: .whitespacesAndNewlines), !action.isEmpty else {
                errors.append("\(prefix): action must not be empty.")
                return
            }

            if !actionsBySkillID.isEmpty {
                guard let catalogItem = actionsBySkillID[widget.skillID]?[action] else {
                    errors.append("\(prefix): action '\(action)' is not available for skill '\(widget.skillID)'.")
                    return
                }
                validateInputs(for: widget, catalogItem: catalogItem, prefix: prefix, errors: &errors)
            }
        }

        // dataKey format: if present, must match ^[a-zA-Z_][a-zA-Z0-9_.]*$
        if let dataKey = widget.dataKey {
            if dataKey.isEmpty {
                errors.append("\(prefix): dataKey must not be an empty string.")
            } else {
                let validPattern = #"^[a-zA-Z_][a-zA-Z0-9_.]*$"#
                if dataKey.range(of: validPattern, options: .regularExpression) == nil {
                    errors.append("\(prefix): dataKey '\(dataKey)' contains invalid characters (use letters, digits, underscores, dots only).")
                }
            }
        }

        validateBinding(for: widget, prefix: prefix, errors: &errors)

        // defaultInputs: all keys and values must be non-empty strings
        if let defaultInputs = widget.defaultInputs {
            for (key, value) in defaultInputs {
                if key.isEmpty {
                    errors.append("\(prefix): defaultInputs contains an empty key.")
                    break
                }
                if value.isEmpty {
                    errors.append("\(prefix): defaultInputs value for key '\(key)' must not be empty.")
                    break
                }
            }
        }

        // Form / search widgets must have at least one field
        if widget.type == .form || widget.type == .search {
            let fields = widget.fields ?? []
            if fields.isEmpty {
                errors.append("\(prefix): \(widget.type.rawValue) widget must have at least one field.")
            }
        }

        // Table widgets must have at least one column
        if widget.type == .table {
            let columns = widget.columns ?? []
            if columns.isEmpty {
                errors.append("\(prefix): table widget must have at least one column.")
            }
        }
    }

    private func validateInputs(
        for widget: DashboardWidget,
        catalogItem: SkillActionCatalogItem,
        prefix: String,
        errors: inout [String]
    ) {
        let schema = catalogItem.action.inputSchema
        let knownKeys = Set(schema.properties.keys)

        if let defaultInputs = widget.defaultInputs, !schema.additionalProperties {
            for key in defaultInputs.keys where !knownKeys.contains(key) {
                errors.append("\(prefix): defaultInputs key '\(key)' is not valid for action '\(catalogItem.action.id)'.")
            }
        }

        let autoFetchTypes: Set<DashboardWidgetType> = [.statCard, .summary, .list, .table]
        if autoFetchTypes.contains(widget.type) {
            let provided = Set((widget.defaultInputs ?? [:]).keys)
            let missingRequired = schema.required.filter { !provided.contains($0) }
            if !missingRequired.isEmpty {
                errors.append("\(prefix): action '\(catalogItem.action.id)' requires defaultInputs for \(missingRequired.joined(separator: ", ")).")
            }
        }

        if widget.type == .form || widget.type == .search {
            let fields = widget.fields ?? []
            for field in fields where !knownKeys.contains(field.key) {
                errors.append("\(prefix): field '\(field.key)' is not valid for action '\(catalogItem.action.id)'.")
            }
        }
    }

    private func validateBinding(
        for widget: DashboardWidget,
        prefix: String,
        errors: inout [String]
    ) {
        guard let binding = widget.binding else { return }

        let paths = [
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

        let validPattern = #"^[a-zA-Z_][a-zA-Z0-9_.]*$"#
        for (label, path) in paths {
            guard let path else { continue }
            if path.isEmpty {
                errors.append("\(prefix): binding.\(label) must not be empty.")
            } else if path.range(of: validPattern, options: .regularExpression) == nil {
                errors.append("\(prefix): binding.\(label) '\(path)' contains invalid characters.")
            }
        }

        switch widget.type {
        case .statCard:
            if binding.valuePath == nil, widget.dataKey == nil {
                errors.append("\(prefix): stat_card widgets should provide binding.valuePath or dataKey.")
            }
        case .summary:
            if binding.summaryPath == nil, binding.valuePath == nil, widget.dataKey == nil {
                errors.append("\(prefix): summary widgets should provide binding.summaryPath, binding.valuePath, or dataKey.")
            }
        case .list:
            if binding.itemsPath == nil, widget.dataKey == nil {
                errors.append("\(prefix): list widgets should provide binding.itemsPath or dataKey.")
            }
        case .table:
            if binding.rowsPath == nil, widget.dataKey == nil {
                errors.append("\(prefix): table widgets should provide binding.rowsPath or dataKey.")
            }
        case .form, .search:
            break
        }
    }
}
