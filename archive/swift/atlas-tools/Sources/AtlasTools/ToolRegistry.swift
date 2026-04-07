import AtlasShared

public actor ToolRegistry {
    private var tools: [String: any AtlasTool]

    public init(tools: [any AtlasTool] = []) {
        self.tools = Dictionary(uniqueKeysWithValues: tools.map { ($0.toolName, $0) })
    }

    public func register(_ tool: any AtlasTool) {
        tools[tool.toolName] = tool
    }

    public func register(_ tools: [any AtlasTool]) {
        for tool in tools {
            register(tool)
        }
    }

    public func registerDefaultTools() {
        register(ReadFileTool())
        register(ListDirectoryTool())
        register(SummarizeTextTool())
    }

    public func tool(named name: String) -> (any AtlasTool)? {
        tools[name]
    }

    public func removeTools(withPrefix prefix: String) {
        let keys = tools.keys.filter { $0.hasPrefix(prefix) }
        for key in keys {
            tools.removeValue(forKey: key)
        }
    }

    public func definitions() -> [AtlasToolDefinition] {
        tools.values
            .map(\.definition)
            .sorted { $0.name < $1.name }
    }
}
