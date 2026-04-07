import Foundation
import AtlasShared
import AtlasTools

public struct SystemActionSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let policy: SystemActionPolicy
    private let executor: any SystemActionExecuting

    public init(
        scopeStore: FileAccessScopeStore = FileAccessScopeStore(),
        notificationSink: (any NotificationSink)? = nil
    ) {
        self.init(
            scopeStore: scopeStore,
            executor: SystemActionExecutor(
                notificationService: notificationSink.map { NotificationService(sink: $0) }
            )
        )
    }

    init(
        scopeStore: FileAccessScopeStore,
        executor: any SystemActionExecuting
    ) {
        self.policy = SystemActionPolicy(scopeStore: scopeStore)
        self.executor = executor

        self.manifest = SkillManifest(
            id: "system-actions",
            name: "System Actions",
            version: "2.0.0",
            description: "Perform native macOS actions: open apps, files, folders, URLs, manage the clipboard, send and schedule notifications, query app state, and control running applications.",
            category: .system,
            lifecycleState: .installed,
            capabilities: [
                .openApp,
                .openFile,
                .openFolder,
                .revealInFinder,
                .copyToClipboard,
                .readClipboard,
                .sendNotification,
                .scheduleNotification,
                .openURL,
                .appStateQuery,
                .activateApp,
                .quitApp
            ],
            requiredPermissions: [
                .draftWrite
            ],
            riskLevel: .high,
            trustProfile: .localExact,
            freshnessType: .local,
            preferredQueryTypes: [
                .openApp,
                .openFile,
                .openFolder,
                .revealInFinder,
                .copyToClipboard,
                .readClipboard,
                .sendNotification,
                .scheduleNotification,
                .openURL,
                .runningApps,
                .frontmostApp,
                .isAppRunning,
                .openFileWithApp,
                .activateApp,
                .quitApp
            ],
            routingPriority: 75,
            canHandleLocalData: true,
            restrictionsSummary: [
                "File actions are limited to approved folders",
                "URL actions support http/https only",
                "Uses native macOS APIs only",
                "No shell, scripts, or system setting changes"
            ],
            supportsReadOnlyMode: false,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["system", "approval", "finder", "clipboard", "notification", "apps"],
            intent: .atlasSystemTask,
            triggers: [
                .init("copy to clipboard", queryType: .copyToClipboard),
                .init("copy this to clipboard", queryType: .copyToClipboard),
                .init("clipboard", queryType: .copyToClipboard),
                .init("what's on my clipboard", queryType: .readClipboard),
                .init("read clipboard", queryType: .readClipboard),
                .init("paste from clipboard", queryType: .readClipboard),
                .init("send me a notification", queryType: .sendNotification),
                .init("send a notification", queryType: .sendNotification),
                .init("send notification", queryType: .sendNotification),
                .init("notify me", queryType: .sendNotification),
                .init("remind me in", queryType: .scheduleNotification),
                .init("schedule a notification", queryType: .scheduleNotification),
                .init("set a reminder", queryType: .scheduleNotification),
                .init("reveal in finder", queryType: .revealInFinder),
                .init("show in finder", queryType: .revealInFinder),
                .init("open folder", queryType: .openFolder),
                .init("show folder", queryType: .openFolder),
                .init("launch the app", queryType: .openApp),
                .init("launch app", queryType: .openApp),
                .init("open the app", queryType: .openApp),
                .init("open app", queryType: .openApp),
                .init("open xcode", queryType: .openApp),
                .init("open finder", queryType: .openApp),
                .init("open terminal", queryType: .openApp),
                .init("open safari", queryType: .openApp),
                .init("launch safari", queryType: .openApp),
                .init("launch xcode", queryType: .openApp),
                .init("open file", queryType: .openFile),
                .init("open url", queryType: .openURL),
                .init("open link", queryType: .openURL),
                .init("open website", queryType: .openURL),
                .init("navigate to", queryType: .openURL),
                .init("what apps are running", queryType: .runningApps),
                .init("running applications", queryType: .runningApps),
                .init("list running apps", queryType: .runningApps),
                .init("what app is in focus", queryType: .frontmostApp),
                .init("active app", queryType: .frontmostApp),
                .init("frontmost app", queryType: .frontmostApp),
                .init("is xcode running", queryType: .isAppRunning),
                .init("is app running", queryType: .isAppRunning),
                .init("open with", queryType: .openFileWithApp),
                .init("open file with app", queryType: .openFileWithApp),
                .init("bring to front", queryType: .activateApp),
                .init("activate app", queryType: .activateApp),
                .init("focus app", queryType: .activateApp),
                .init("quit app", queryType: .quitApp),
                .init("close app", queryType: .quitApp),
                .init("force quit", queryType: .quitApp)
            ]
        )

        self.actions = [
            // MARK: open_app
            SkillActionDefinition(
                id: "system.open_app",
                name: "Open App",
                description: "Open a macOS application by name using native app resolution.",
                inputSchemaSummary: "appName is required.",
                outputSchemaSummary: "Requested and resolved app names, bundle identifier, launch status, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.openApp],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "appName": AtlasToolInputProperty(type: "string", description: "Application name such as Xcode, Finder, or Safari.")
                    ],
                    required: ["appName"]
                )
            ),
            // MARK: open_file
            SkillActionDefinition(
                id: "system.open_file",
                name: "Open File",
                description: "Open a file inside the approved file scope with its default application.",
                inputSchemaSummary: "path is required and must be inside an approved folder.",
                outputSchemaSummary: "Path, open status, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.openFile],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "path": AtlasToolInputProperty(type: "string", description: "File path inside an approved file scope.")
                    ],
                    required: ["path"]
                )
            ),
            // MARK: open_folder
            SkillActionDefinition(
                id: "system.open_folder",
                name: "Open Folder",
                description: "Open a folder inside the approved file scope in Finder.",
                inputSchemaSummary: "path is required and must be inside an approved folder.",
                outputSchemaSummary: "Path, open status, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.openFolder],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "path": AtlasToolInputProperty(type: "string", description: "Folder path inside an approved file scope.")
                    ],
                    required: ["path"]
                )
            ),
            // MARK: reveal_in_finder
            SkillActionDefinition(
                id: "system.reveal_in_finder",
                name: "Reveal In Finder",
                description: "Reveal a file or folder inside the approved file scope in Finder.",
                inputSchemaSummary: "path is required and must be inside an approved folder.",
                outputSchemaSummary: "Path, reveal status, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.revealInFinder],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "path": AtlasToolInputProperty(type: "string", description: "File or folder path inside an approved file scope.")
                    ],
                    required: ["path"]
                )
            ),
            // MARK: open_file_with_app
            SkillActionDefinition(
                id: "system.open_file_with_app",
                name: "Open File With App",
                description: "Open a file inside the approved file scope using a specific application.",
                inputSchemaSummary: "path and appName are required.",
                outputSchemaSummary: "Path, open status, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.openFileWithApp],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "path": AtlasToolInputProperty(type: "string", description: "File path inside an approved file scope."),
                        "appName": AtlasToolInputProperty(type: "string", description: "Name of the application to open the file with, such as Preview or VSCode.")
                    ],
                    required: ["path", "appName"]
                )
            ),
            // MARK: open_url
            SkillActionDefinition(
                id: "system.open_url",
                name: "Open URL",
                description: "Open an http or https URL in the default browser.",
                inputSchemaSummary: "url is required and must be an http or https URL.",
                outputSchemaSummary: "URL, open status, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.openURL],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "url": AtlasToolInputProperty(type: "string", description: "An http or https URL to open in the default browser.")
                    ],
                    required: ["url"]
                )
            ),
            // MARK: copy_to_clipboard
            SkillActionDefinition(
                id: "system.copy_to_clipboard",
                name: "Copy To Clipboard",
                description: "Copy text to the macOS clipboard.",
                inputSchemaSummary: "text is required.",
                outputSchemaSummary: "Character count, copy status, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.copyToClipboard],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "text": AtlasToolInputProperty(type: "string", description: "Text to copy to the clipboard.")
                    ],
                    required: ["text"]
                )
            ),
            // MARK: read_clipboard
            SkillActionDefinition(
                id: "system.read_clipboard",
                name: "Read Clipboard",
                description: "Read the current text content of the macOS clipboard.",
                inputSchemaSummary: "No input required.",
                outputSchemaSummary: "Clipboard text (if any), isEmpty flag, and a message.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.readClipboard],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(properties: [:], required: [])
            ),
            // MARK: send_notification
            SkillActionDefinition(
                id: "system.send_notification",
                name: "Send Notification",
                description: "Send a local macOS notification immediately.",
                inputSchemaSummary: "title and body are required.",
                outputSchemaSummary: "Notification title, delivery state, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.sendNotification],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "title": AtlasToolInputProperty(type: "string", description: "Notification title."),
                        "body": AtlasToolInputProperty(type: "string", description: "Notification body text.")
                    ],
                    required: ["title", "body"]
                )
            ),
            // MARK: schedule_notification
            SkillActionDefinition(
                id: "system.schedule_notification",
                name: "Schedule Notification",
                description: "Schedule a local macOS notification to fire after a delay.",
                inputSchemaSummary: "title, body, and delaySeconds are required.",
                outputSchemaSummary: "Notification title, scheduled state, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.scheduleNotification],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "title": AtlasToolInputProperty(type: "string", description: "Notification title."),
                        "body": AtlasToolInputProperty(type: "string", description: "Notification body text."),
                        "delaySeconds": AtlasToolInputProperty(type: "integer", description: "Number of seconds to wait before firing the notification.")
                    ],
                    required: ["title", "body", "delaySeconds"]
                )
            ),
            // MARK: running_apps
            SkillActionDefinition(
                id: "system.running_apps",
                name: "Running Apps",
                description: "List all currently running user-facing applications.",
                inputSchemaSummary: "No input required.",
                outputSchemaSummary: "Array of running apps with name, bundle ID, and active state.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.runningApps],
                routingPriority: 40,
                inputSchema: AtlasToolInputSchema(properties: [:], required: [])
            ),
            // MARK: frontmost_app
            SkillActionDefinition(
                id: "system.frontmost_app",
                name: "Frontmost App",
                description: "Return the name and bundle identifier of the currently active (frontmost) application.",
                inputSchemaSummary: "No input required.",
                outputSchemaSummary: "App name, bundle ID, availability flag, and a message.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.frontmostApp],
                routingPriority: 40,
                inputSchema: AtlasToolInputSchema(properties: [:], required: [])
            ),
            // MARK: is_app_running
            SkillActionDefinition(
                id: "system.is_app_running",
                name: "Is App Running",
                description: "Check whether a specific application is currently running.",
                inputSchemaSummary: "appName is required.",
                outputSchemaSummary: "App name, running flag, instance count, and a message.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.isAppRunning],
                routingPriority: 40,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "appName": AtlasToolInputProperty(type: "string", description: "Application name to check, such as Xcode or Safari.")
                    ],
                    required: ["appName"]
                )
            ),
            // MARK: activate_app
            SkillActionDefinition(
                id: "system.activate_app",
                name: "Activate App",
                description: "Bring a running application to the foreground.",
                inputSchemaSummary: "appName is required. The app must already be running.",
                outputSchemaSummary: "App name, activated flag, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.activateApp],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "appName": AtlasToolInputProperty(type: "string", description: "Name of the running application to bring to the front.")
                    ],
                    required: ["appName"]
                )
            ),
            // MARK: quit_app
            SkillActionDefinition(
                id: "system.quit_app",
                name: "Quit App",
                description: "Quit a running application. Use force=true to force-quit unresponsive apps.",
                inputSchemaSummary: "appName is required. force is optional (default false).",
                outputSchemaSummary: "App name, terminated flag, and a message.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.quitApp],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "appName": AtlasToolInputProperty(type: "string", description: "Name of the running application to quit."),
                        "force": AtlasToolInputProperty(type: "boolean", description: "If true, force-quit the application. Default is false.")
                    ],
                    required: ["appName"]
                )
            )
        ]
    }

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        let approvedRootCount = await policy.approvedRootCount()
        let notificationStatus = await executor.validateNotificationCapability()
        let providerSummary = await executor.providerSummary()

        var issues: [String] = []
        if approvedRootCount == 0 {
            issues.append("No approved folders are configured yet, so file and Finder actions are limited.")
        }
        issues.append(contentsOf: notificationStatus.issues)

        let status: SkillValidationStatus
        if notificationStatus.isAvailable {
            status = approvedRootCount == 0 ? .warning : .passed
        } else {
            status = .warning
        }

        let summary: String
        if approvedRootCount == 0 {
            summary = "System Actions v2 is ready for app launch, clipboard, URLs, app state queries, and notifications. File actions will work after Atlas has at least one approved folder."
        } else {
            summary = "System Actions v2 is ready with \(approvedRootCount) approved folder\(approvedRootCount == 1 ? "" : "s")."
        }

        let combinedSummary: String
        if providerSummary.isEmpty {
            combinedSummary = summary
        } else {
            combinedSummary = summary + "\n" + providerSummary.joined(separator: " • ")
        }

        return SkillValidationResult(
            skillID: manifest.id,
            status: status,
            summary: combinedSummary,
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
        case "system.open_app":           return try await openApp(input: input, context: context)
        case "system.open_file":          return try await openFile(input: input, context: context)
        case "system.open_folder":        return try await openFolder(input: input, context: context)
        case "system.reveal_in_finder":   return try await revealInFinder(input: input, context: context)
        case "system.open_file_with_app": return try await openFileWithApp(input: input, context: context)
        case "system.open_url":           return try await openURL(input: input, context: context)
        case "system.copy_to_clipboard":  return try await copyToClipboard(input: input, context: context)
        case "system.read_clipboard":     return try await readClipboard(context: context)
        case "system.send_notification":  return try await sendNotification(input: input, context: context)
        case "system.schedule_notification": return try await scheduleNotification(input: input, context: context)
        case "system.running_apps":       return try await runningApps(context: context)
        case "system.frontmost_app":      return try await frontmostApp(context: context)
        case "system.is_app_running":     return try await isAppRunning(input: input, context: context)
        case "system.activate_app":       return try await activateApp(input: input, context: context)
        case "system.quit_app":           return try await quitApp(input: input, context: context)
        default:
            throw AtlasToolError.invalidInput("The action '\(actionID)' is not supported by System Actions.")
        }
    }

    // MARK: - Action handlers

    private func openApp(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemOpenAppInput.self)
        context.logger.info("System Actions: open_app", metadata: ["app_name": summarize(payload.appName)])
        let output = try await executor.openApp(named: payload.appName)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.open_app",
            output: try encode(output), summary: output.message,
            metadata: ["resolved_app_name": summarize(output.resolvedAppName)]
        )
    }

    private func openFile(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemOpenFileInput.self)
        let url = try await policy.resolveFileURL(for: payload.path, expectsDirectory: false)
        context.logger.info("System Actions: open_file", metadata: ["path": summarizePath(url.path)])
        let output = try await executor.openFile(at: url)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.open_file",
            output: try encode(output), summary: output.message,
            metadata: ["path": summarizePath(url.path)]
        )
    }

    private func openFolder(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemOpenFolderInput.self)
        let url = try await policy.resolveFileURL(for: payload.path, expectsDirectory: true)
        context.logger.info("System Actions: open_folder", metadata: ["path": summarizePath(url.path)])
        let output = try await executor.openFolder(at: url)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.open_folder",
            output: try encode(output), summary: output.message,
            metadata: ["path": summarizePath(url.path)]
        )
    }

    private func revealInFinder(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemRevealInFinderInput.self)
        let url = try await policy.resolveFileURL(for: payload.path, expectsDirectory: nil)
        context.logger.info("System Actions: reveal_in_finder", metadata: ["path": summarizePath(url.path)])
        let output = try await executor.revealInFinder(url)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.reveal_in_finder",
            output: try encode(output), summary: output.message,
            metadata: ["path": summarizePath(url.path)]
        )
    }

    private func openFileWithApp(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemOpenFileWithAppInput.self)
        let url = try await policy.resolveFileURL(for: payload.path, expectsDirectory: false)
        context.logger.info("System Actions: open_file_with_app", metadata: [
            "path": summarizePath(url.path),
            "app_name": summarize(payload.appName)
        ])
        let output = try await executor.openFileWithApp(at: url, appName: payload.appName)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.open_file_with_app",
            output: try encode(output), summary: output.message,
            metadata: ["path": summarizePath(url.path), "app_name": summarize(payload.appName)]
        )
    }

    private func openURL(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemOpenURLInput.self)
        let url = try policy.validateURL(for: payload.url)
        context.logger.info("System Actions: open_url", metadata: ["url": summarize(url.absoluteString)])
        let output = try await executor.openURL(url)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.open_url",
            output: try encode(output), summary: output.message,
            metadata: ["url": summarize(url.absoluteString)]
        )
    }

    private func copyToClipboard(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemCopyToClipboardInput.self)
        context.logger.info("System Actions: copy_to_clipboard", metadata: ["character_count": "\(payload.text.count)"])
        let output = try await executor.copyToClipboard(payload.text)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.copy_to_clipboard",
            output: try encode(output), summary: output.message,
            metadata: ["character_count": "\(output.characterCount)"]
        )
    }

    private func readClipboard(context: SkillExecutionContext) async throws -> SkillExecutionResult {
        context.logger.info("System Actions: read_clipboard")
        let output = await executor.readClipboard()
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.read_clipboard",
            output: try encode(output), summary: output.message,
            metadata: ["is_empty": "\(output.isEmpty)"]
        )
    }

    private func sendNotification(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemSendNotificationInput.self)
        context.logger.info("System Actions: send_notification", metadata: ["title": summarize(payload.title)])
        let output = try await executor.sendNotification(title: payload.title, body: payload.body)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.send_notification",
            output: try encode(output), summary: output.message,
            metadata: ["title": summarize(output.title)]
        )
    }

    private func scheduleNotification(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemScheduleNotificationInput.self)
        context.logger.info("System Actions: schedule_notification", metadata: [
            "title": summarize(payload.title),
            "delay_seconds": "\(payload.delaySeconds)"
        ])
        let output = try await executor.scheduleNotification(title: payload.title, body: payload.body, delaySeconds: payload.delaySeconds)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.schedule_notification",
            output: try encode(output), summary: output.message,
            metadata: ["title": summarize(output.title)]
        )
    }

    private func runningApps(context: SkillExecutionContext) async throws -> SkillExecutionResult {
        context.logger.info("System Actions: running_apps")
        let output = await executor.runningApps()
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.running_apps",
            output: try encode(output), summary: output.message,
            metadata: ["count": "\(output.count)"]
        )
    }

    private func frontmostApp(context: SkillExecutionContext) async throws -> SkillExecutionResult {
        context.logger.info("System Actions: frontmost_app")
        let output = await executor.frontmostApp()
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.frontmost_app",
            output: try encode(output), summary: output.message,
            metadata: ["app_name": output.name.map { summarize($0) } ?? "none"]
        )
    }

    private func isAppRunning(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemIsAppRunningInput.self)
        context.logger.info("System Actions: is_app_running", metadata: ["app_name": summarize(payload.appName)])
        let output = await executor.isAppRunning(named: payload.appName)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.is_app_running",
            output: try encode(output), summary: output.message,
            metadata: ["is_running": "\(output.isRunning)"]
        )
    }

    private func activateApp(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemActivateAppInput.self)
        context.logger.info("System Actions: activate_app", metadata: ["app_name": summarize(payload.appName)])
        let output = try await executor.activateApp(named: payload.appName)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.activate_app",
            output: try encode(output), summary: output.message,
            metadata: ["app_name": summarize(output.appName)]
        )
    }

    private func quitApp(input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let payload = try input.decode(SystemQuitAppInput.self)
        context.logger.info("System Actions: quit_app", metadata: [
            "app_name": summarize(payload.appName),
            "force": "\(payload.force ?? false)"
        ])
        let output = try await executor.quitApp(named: payload.appName, force: payload.force ?? false)
        return SkillExecutionResult(
            skillID: manifest.id, actionID: "system.quit_app",
            output: try encode(output), summary: output.message,
            metadata: ["app_name": summarize(output.appName)]
        )
    }

    // MARK: - Helpers

    private func encode<T: Encodable>(_ value: T) throws -> String {
        let data = try AtlasJSON.encoder.encode(value)
        guard let string = String(data: data, encoding: .utf8) else {
            throw AtlasToolError.executionFailed("Atlas could not encode System Actions output.")
        }
        return string
    }

    private func summarize(_ value: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.count <= 120 ? trimmed : String(trimmed.prefix(117)) + "..."
    }

    private func summarizePath(_ path: String) -> String {
        let url = URL(fileURLWithPath: path)
        return url.pathComponents.count <= 2 ? path : "…/" + url.lastPathComponent
    }
}
