import AtlasShared

public enum SkillPermission: String, Codable, CaseIterable, Hashable, Sendable {
    case infoRead = "info_read"
    case runtimeRead = "runtime_read"
    case skillRegistryRead = "skill_registry_read"
    case localRead = "local_read"
    case weatherRead = "weather_read"
    case publicWebRead = "public_web_read"
    case draftWrite = "draft_write"
    case liveWrite = "live_write"
    case destructiveWrite = "destructive_write"

    public var atlasPermissionLevel: PermissionLevel {
        switch self {
        case .infoRead, .runtimeRead, .skillRegistryRead, .localRead, .weatherRead, .publicWebRead:
            return .read
        case .draftWrite:
            return .draft
        case .liveWrite, .destructiveWrite:
            return .execute
        }
    }

    public var summary: String {
        rawValue.replacingOccurrences(of: "_", with: " ")
    }
}
