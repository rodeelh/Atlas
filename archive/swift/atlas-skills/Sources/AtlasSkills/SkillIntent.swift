public enum SkillIntent: String, Codable, CaseIterable, Hashable, Sendable {
    case liveStructuredData = "live_structured_data"
    case exploratoryResearch = "exploratory_research"
    case localFileTask = "local_file_task"
    case atlasSystemTask = "atlas_system_task"
    case appAutomation = "app_automation"
    case generalReasoning = "general_reasoning"
    /// Pure conversational turn — greetings, acknowledgements. No tools needed.
    case conversational
    case unknown
}
