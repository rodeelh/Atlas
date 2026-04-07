import Foundation
import AtlasShared
import AtlasTools

public struct AppleScriptSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let executor: any AppleScriptExecuting

    public init() {
        self.init(executor: AppleScriptExecutor())
    }

    init(executor: any AppleScriptExecuting) {
        self.executor = executor

        self.manifest = SkillManifest(
            id: "applescript-automation",
            name: "App Automator",
            version: "2.0.0",
            description: "Read and automate Calendar, Reminders, Contacts, Mail, Safari, Notes, Music, and system info via AppleScript. Supports list discovery, mail compose/send, calendar delete, reminders search, message body reading, and structured JSON output.",
            category: .productivity,
            lifecycleState: .installed,
            capabilities: [
                .calendarRead,
                .calendarWrite,
                .remindersRead,
                .remindersWrite,
                .contactsRead,
                .mailRead,
                .safariRead,
                .safariNavigate,
                .notesRead,
                .notesWrite,
                .musicRead,
                .musicControl,
                .appleScriptCustom,
                .mailWrite,
                .calendarListCalendars,
                .remindersListLists,
                .systemInfo
            ],
            requiredPermissions: [
                .localRead,
                .liveWrite
            ],
            riskLevel: .high,
            trustProfile: .operational,
            freshnessType: .live,
            preferredQueryTypes: [
                .calendarRead,
                .calendarWrite,
                .remindersRead,
                .remindersWrite,
                .contactsRead,
                .mailRead,
                .safariRead,
                .safariNavigate,
                .notesRead,
                .notesWrite,
                .musicRead,
                .musicControl,
                .appleScriptCustom,
                .mailWrite,
                .calendarListCalendars,
                .remindersListLists,
                .systemInfo
            ],
            routingPriority: 85,
            canAnswerStructuredLiveData: true,
            canHandleLocalData: true,
            restrictionsSummary: [
                "Custom scripts are statically validated before execution",
                "do shell script, display dialog, and UI injection constructs are blocked",
                "All actions require TCC permission grants for the Atlas daemon binary",
                "Script execution is time-limited (15s default, 60s max)"
            ],
            supportsReadOnlyMode: false,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["calendar", "reminders", "contacts", "mail", "safari", "notes", "music", "applescript", "automation"],
            intent: .appAutomation,
            triggers: [
                // Calendar — write (more specific first)
                .init("schedule a meeting", queryType: .calendarWrite),
                .init("add to my calendar", queryType: .calendarWrite),
                .init("put on my calendar", queryType: .calendarWrite),
                .init("create a calendar event", queryType: .calendarWrite),
                .init("create an event", queryType: .calendarWrite),
                .init("add an event", queryType: .calendarWrite),
                .init("add event", queryType: .calendarWrite),
                .init("new event", queryType: .calendarWrite),
                // Calendar — read
                .init("what's on my calendar", queryType: .calendarRead),
                .init("what do i have on", queryType: .calendarRead),
                .init("what do i have tomorrow", queryType: .calendarRead),
                .init("upcoming meetings", queryType: .calendarRead),
                .init("upcoming events", queryType: .calendarRead),
                .init("my appointments", queryType: .calendarRead),
                .init("my meetings", queryType: .calendarRead),
                .init("my schedule", queryType: .calendarRead),
                .init("my events", queryType: .calendarRead),
                .init("my calendar", queryType: .calendarRead),
                // Reminders — write
                .init("add a reminder", queryType: .remindersWrite),
                .init("set a reminder", queryType: .remindersWrite),
                .init("create a reminder", queryType: .remindersWrite),
                .init("remind me", queryType: .remindersWrite),
                .init("add to my reminders", queryType: .remindersWrite),
                // Reminders — read
                .init("my reminders", queryType: .remindersRead),
                .init("my to-do list", queryType: .remindersRead),
                .init("my todo list", queryType: .remindersRead),
                .init("show my reminders", queryType: .remindersRead),
                // Notes — write
                .init("save this to notes", queryType: .notesWrite),
                .init("write a note", queryType: .notesWrite),
                .init("add to notes", queryType: .notesWrite),
                .init("create a note", queryType: .notesWrite),
                .init("add a note", queryType: .notesWrite),
                .init("new note", queryType: .notesWrite),
                // Notes — read
                .init("in apple notes", queryType: .notesRead),
                .init("my notes", queryType: .notesRead),
                .init("in notes", queryType: .notesRead),
                // Music
                .init("what's playing right now", queryType: .musicRead),
                .init("what's currently playing", queryType: .musicRead),
                .init("what is playing", queryType: .musicRead),
                .init("now playing", queryType: .musicRead),
                .init("current song", queryType: .musicRead),
                .init("pause the music", queryType: .musicControl),
                .init("pause music", queryType: .musicControl),
                .init("play music", queryType: .musicControl),
                .init("next track", queryType: .musicControl),
                .init("previous track", queryType: .musicControl),
                .init("skip track", queryType: .musicControl),
                .init("set volume", queryType: .musicControl),
                .init("shuffle", queryType: .musicControl),
                .init("in music app", queryType: .musicControl),
                .init("in spotify", queryType: .musicControl),
                // Contacts
                .init("find a contact", queryType: .contactsRead),
                .init("find contact", queryType: .contactsRead),
                .init("in my contacts", queryType: .contactsRead),
                .init("my contacts", queryType: .contactsRead),
                .init("address book", queryType: .contactsRead),
                // Mail
                .init("check my email", queryType: .mailRead),
                .init("read my email", queryType: .mailRead),
                .init("my unread emails", queryType: .mailRead),
                .init("unread emails", queryType: .mailRead),
                .init("my inbox", queryType: .mailRead),
                .init("recent emails", queryType: .mailRead),
                .init("check mail", queryType: .mailRead),
                .init("my mailbox", queryType: .mailRead),
                .init("my email", queryType: .mailRead),
                // Safari
                .init("what's open in safari", queryType: .safariRead),
                .init("safari tabs", queryType: .safariRead),
                .init("open tabs in safari", queryType: .safariRead),
                .init("current tab", queryType: .safariRead),
                .init("browser tab", queryType: .safariRead)
            ]
        )

        self.actions = [
            SkillActionDefinition(
                id: "applescript.calendar_read",
                name: "Read Calendar",
                description: "List or search Calendar events within a date range, optionally filtered by calendar name.",
                inputSchemaSummary: "startDate and endDate are required (ISO 8601). calendarName and maxResults are optional.",
                outputSchemaSummary: "Structured list of events with title, start/end, calendar, location, and notes.",
                permissionLevel: .read,
                sideEffectLevel: .sensitiveRead,
                preferredQueryTypes: [.calendarRead],
                routingPriority: 60,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "startDate": AtlasToolInputProperty(type: "string", description: "Start of date range in ISO 8601 format (e.g. 2026-03-22T00:00:00)."),
                        "endDate": AtlasToolInputProperty(type: "string", description: "End of date range in ISO 8601 format (e.g. 2026-03-29T23:59:59)."),
                        "calendarName": AtlasToolInputProperty(type: "string", description: "Optional calendar name to filter by. Omit to search all calendars."),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Maximum number of events to return. Defaults to 20.")
                    ],
                    required: ["startDate", "endDate"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.calendar_write",
                name: "Create or Delete Calendar Event",
                description: "Create a new Calendar event, or delete an existing one by title and start date.",
                inputSchemaSummary: "action (create or delete), title, and startDate are required. endDate and calendarName are required for create. calendarName is optional for delete.",
                outputSchemaSummary: "Confirmation message.",
                permissionLevel: .execute,
                sideEffectLevel: .liveWrite,
                preferredQueryTypes: [.calendarWrite],
                routingPriority: 60,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "action": AtlasToolInputProperty(type: "string", description: "Either 'create' to add a new event (default), or 'delete' to remove an existing one by title and start date."),
                        "title": AtlasToolInputProperty(type: "string", description: "Event title."),
                        "startDate": AtlasToolInputProperty(type: "string", description: "Event start in ISO 8601 format (e.g. 2026-03-22T09:00:00). Required for both create and delete."),
                        "endDate": AtlasToolInputProperty(type: "string", description: "Event end in ISO 8601 format. Required when action is 'create'."),
                        "calendarName": AtlasToolInputProperty(type: "string", description: "Calendar name. Required for create; optional filter for delete."),
                        "notes": AtlasToolInputProperty(type: "string", description: "Optional event notes. Only used when action is 'create'."),
                        "location": AtlasToolInputProperty(type: "string", description: "Optional event location. Only used when action is 'create'.")
                    ],
                    required: ["title", "startDate"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.reminders_read",
                name: "Read Reminders",
                description: "List or search reminders, optionally filtered by list name, keyword, and completion status.",
                inputSchemaSummary: "All parameters are optional. listName, query (keyword search), includeCompleted, and maxResults.",
                outputSchemaSummary: "JSON array of reminders with title, list, due, completed, and notes fields.",
                permissionLevel: .read,
                sideEffectLevel: .sensitiveRead,
                preferredQueryTypes: [.remindersRead],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "listName": AtlasToolInputProperty(type: "string", description: "Optional Reminders list name to filter by. Omit to search all lists."),
                        "query": AtlasToolInputProperty(type: "string", description: "Optional keyword to filter reminders by title. Case-insensitive contains match."),
                        "includeCompleted": AtlasToolInputProperty(type: "boolean", description: "Whether to include completed reminders. Defaults to false."),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Maximum number of reminders to return. Defaults to 20.")
                    ],
                    required: []
                )
            ),
            SkillActionDefinition(
                id: "applescript.reminders_write",
                name: "Create or Complete Reminder",
                description: "Create a new reminder in a Reminders list, or mark an existing reminder as complete.",
                inputSchemaSummary: "action (create or complete), name, and listName are required. dueDate and notes are optional for create.",
                outputSchemaSummary: "Confirmation message.",
                permissionLevel: .execute,
                sideEffectLevel: .liveWrite,
                preferredQueryTypes: [.remindersWrite],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "action": AtlasToolInputProperty(type: "string", description: "Either 'create' to add a new reminder, or 'complete' to mark one as done."),
                        "name": AtlasToolInputProperty(type: "string", description: "Reminder name."),
                        "listName": AtlasToolInputProperty(type: "string", description: "Reminders list name."),
                        "dueDate": AtlasToolInputProperty(type: "string", description: "Optional due date in ISO 8601 format. Only used when action is 'create'."),
                        "notes": AtlasToolInputProperty(type: "string", description: "Optional reminder notes. Only used when action is 'create'.")
                    ],
                    required: ["action", "name", "listName"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.contacts_read",
                name: "Search Contacts",
                description: "Search Contacts by name or email and return matching entries with name, email(s), phone(s), and organization.",
                inputSchemaSummary: "query is required. maxResults is optional.",
                outputSchemaSummary: "Structured list of matching contacts.",
                permissionLevel: .read,
                sideEffectLevel: .sensitiveRead,
                preferredQueryTypes: [.contactsRead],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "query": AtlasToolInputProperty(type: "string", description: "Name or partial name to search for."),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Maximum number of contacts to return. Defaults to 10.")
                    ],
                    required: ["query"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.mail_read",
                name: "Read Mail",
                description: "List recent messages, search by subject or sender, or read a message body by subject.",
                inputSchemaSummary: "action is required: 'list', 'search', or 'body'. query is required for search and body. mailbox and maxResults are optional.",
                outputSchemaSummary: "JSON array of message metadata for list/search. Full body text (truncated at 4000 chars) for body action.",
                permissionLevel: .read,
                sideEffectLevel: .sensitiveRead,
                preferredQueryTypes: [.mailRead],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "action": AtlasToolInputProperty(type: "string", description: "One of: 'list' (recent messages), 'search' (by subject/sender), or 'body' (read full message body by subject)."),
                        "query": AtlasToolInputProperty(type: "string", description: "Search query or subject string. Required when action is 'search' or 'body'."),
                        "mailbox": AtlasToolInputProperty(type: "string", description: "Optional mailbox name (e.g. 'Inbox'). Omit to search all mailboxes."),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Maximum number of messages to return for list/search. Defaults to 10.")
                    ],
                    required: ["action"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.safari_read",
                name: "Read Safari",
                description: "Get the current Safari tab URL and title, or list all open tabs across all windows.",
                inputSchemaSummary: "action is required: 'current_tab' or 'all_tabs'.",
                outputSchemaSummary: "Tab URL(s) and title(s).",
                permissionLevel: .read,
                sideEffectLevel: .sensitiveRead,
                preferredQueryTypes: [.safariRead],
                routingPriority: 60,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "action": AtlasToolInputProperty(type: "string", description: "Either 'current_tab' to get the frontmost tab, or 'all_tabs' to list every open tab.")
                    ],
                    required: ["action"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.safari_navigate",
                name: "Navigate Safari",
                description: "Navigate Safari to a URL in the current tab or a new tab. javascript: URLs are blocked.",
                inputSchemaSummary: "url is required. newTab is optional (defaults to false).",
                outputSchemaSummary: "Confirmation of navigation.",
                permissionLevel: .execute,
                sideEffectLevel: .liveWrite,
                preferredQueryTypes: [.safariNavigate],
                routingPriority: 60,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "url": AtlasToolInputProperty(type: "string", description: "The URL to navigate to. Must be a valid http/https URL. javascript: URLs are not permitted."),
                        "newTab": AtlasToolInputProperty(type: "boolean", description: "Open in a new tab. Defaults to false (reuses current tab).")
                    ],
                    required: ["url"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.notes_read",
                name: "Read Notes",
                description: "List, search, or read the full body of notes in the Notes app.",
                inputSchemaSummary: "action is required: 'list', 'search', or 'read'. query (for search), title (for read), and folderName are optional.",
                outputSchemaSummary: "Note titles, modification dates, folders, and body text (HTML stripped).",
                permissionLevel: .read,
                sideEffectLevel: .sensitiveRead,
                preferredQueryTypes: [.notesRead],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "action": AtlasToolInputProperty(type: "string", description: "One of: 'list' (all notes), 'search' (by title keyword), 'read' (full body of a specific note)."),
                        "query": AtlasToolInputProperty(type: "string", description: "Search keyword. Required when action is 'search'."),
                        "title": AtlasToolInputProperty(type: "string", description: "Exact note title. Required when action is 'read'."),
                        "folderName": AtlasToolInputProperty(type: "string", description: "Optional Notes folder name to scope the operation."),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Maximum notes to return for 'list' and 'search'. Defaults to 20.")
                    ],
                    required: ["action"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.notes_write",
                name: "Write Notes",
                description: "Create a new note or append text to an existing note in the Notes app.",
                inputSchemaSummary: "action, title, and body (for create) or textToAppend (for append) are required.",
                outputSchemaSummary: "Confirmation message.",
                permissionLevel: .execute,
                sideEffectLevel: .liveWrite,
                preferredQueryTypes: [.notesWrite],
                routingPriority: 55,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "action": AtlasToolInputProperty(type: "string", description: "Either 'create' to make a new note, or 'append' to add text to an existing note."),
                        "title": AtlasToolInputProperty(type: "string", description: "Note title. For 'create', this becomes the note's name. For 'append', this identifies the target note."),
                        "body": AtlasToolInputProperty(type: "string", description: "Note body content. Required when action is 'create'."),
                        "textToAppend": AtlasToolInputProperty(type: "string", description: "Text to append. Required when action is 'append'."),
                        "folderName": AtlasToolInputProperty(type: "string", description: "Optional Notes folder. Omit to use the default Notes folder.")
                    ],
                    required: ["action", "title"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.music_read",
                name: "Read Music",
                description: "Get the currently playing track and playback state, or search the Music library.",
                inputSchemaSummary: "action is required: 'now_playing' or 'search'. query is required for search.",
                outputSchemaSummary: "Track name, artist, album, duration, position, and player state.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.musicRead],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "action": AtlasToolInputProperty(type: "string", description: "Either 'now_playing' for current track info, or 'search' to find tracks in the library."),
                        "query": AtlasToolInputProperty(type: "string", description: "Search query. Required when action is 'search'."),
                        "targetApp": AtlasToolInputProperty(type: "string", description: "App to control: 'Music' (default) or 'Spotify'."),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Maximum search results. Defaults to 10.")
                    ],
                    required: ["action"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.music_control",
                name: "Control Music",
                description: "Control Music or Spotify playback: play a specific track by name/artist, play, pause, skip, go back, set volume, or toggle shuffle.",
                inputSchemaSummary: "command is required. query (track + artist) for play_track, volume (0–100) for set_volume, enabled (bool) for shuffle.",
                outputSchemaSummary: "Confirmation of the control action taken.",
                permissionLevel: .execute,
                sideEffectLevel: .draftWrite,
                preferredQueryTypes: [.musicControl],
                routingPriority: 50,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "command": AtlasToolInputProperty(type: "string", description: "One of: 'play_track', 'play', 'pause', 'next', 'previous', 'set_volume', 'set_shuffle'. Use 'play_track' to search and play a specific song."),
                        "query": AtlasToolInputProperty(type: "string", description: "Search query for the track to play. Include artist name for best results (e.g. 'Bad Romance Lady Gaga'). Required when command is 'play_track'."),
                        "volume": AtlasToolInputProperty(type: "integer", description: "Volume level 0–100. Required when command is 'set_volume'."),
                        "enabled": AtlasToolInputProperty(type: "boolean", description: "Shuffle on/off. Required when command is 'set_shuffle'."),
                        "targetApp": AtlasToolInputProperty(type: "string", description: "App to control: 'Music' (default) or 'Spotify'.")
                    ],
                    required: ["command"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.run_custom",
                name: "Run Custom AppleScript",
                description: "Run a custom AppleScript. Validated for blocked constructs before execution. Always requires approval.",
                inputSchemaSummary: "script and description are required. timeoutSeconds is optional (max 60).",
                outputSchemaSummary: "Raw script output, truncated at 4096 characters.",
                permissionLevel: .execute,
                sideEffectLevel: .liveWrite,
                preferredQueryTypes: [.appleScriptCustom],
                routingPriority: 30,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "script": AtlasToolInputProperty(type: "string", description: "The AppleScript to execute. Must not contain do shell script, display dialog, display alert, choose file, choose folder, keystroke, key code, open for access, write to, close access, or do JavaScript."),
                        "description": AtlasToolInputProperty(type: "string", description: "A plain-English description of what this script does. Shown in the approval request."),
                        "timeoutSeconds": AtlasToolInputProperty(type: "integer", description: "Execution timeout in seconds. Defaults to 15, maximum 60.")
                    ],
                    required: ["script", "description"]
                )
            ),
            // MARK: v2 new actions
            SkillActionDefinition(
                id: "applescript.mail_write",
                name: "Compose or Send Mail",
                description: "Compose a new email message. Saves to Drafts by default; set autoSend to true to send immediately.",
                inputSchemaSummary: "to, subject, and body are required. autoSend is optional (defaults to false).",
                outputSchemaSummary: "Confirmation of draft creation or send.",
                permissionLevel: .execute,
                sideEffectLevel: .liveWrite,
                preferredQueryTypes: [.mailWrite],
                routingPriority: 58,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "to": AtlasToolInputProperty(type: "string", description: "Recipient email address."),
                        "subject": AtlasToolInputProperty(type: "string", description: "Message subject line."),
                        "body": AtlasToolInputProperty(type: "string", description: "Plain text message body."),
                        "autoSend": AtlasToolInputProperty(type: "boolean", description: "Send the message immediately. Defaults to false (saves to Drafts). Requires approval when true.")
                    ],
                    required: ["to", "subject", "body"]
                )
            ),
            SkillActionDefinition(
                id: "applescript.calendar_list_calendars",
                name: "List Calendars",
                description: "Return all calendar names available in the Calendar app. Use this before creating events to confirm the exact calendar name.",
                inputSchemaSummary: "No inputs required.",
                outputSchemaSummary: "JSON array of calendar name objects.",
                permissionLevel: .read,
                sideEffectLevel: .sensitiveRead,
                preferredQueryTypes: [.calendarListCalendars],
                routingPriority: 62,
                inputSchema: AtlasToolInputSchema(properties: [:], required: [])
            ),
            SkillActionDefinition(
                id: "applescript.reminders_list_lists",
                name: "List Reminder Lists",
                description: "Return all Reminders list names. Use this before creating reminders to confirm the exact list name.",
                inputSchemaSummary: "No inputs required.",
                outputSchemaSummary: "JSON array of list name objects.",
                permissionLevel: .read,
                sideEffectLevel: .sensitiveRead,
                preferredQueryTypes: [.remindersListLists],
                routingPriority: 57,
                inputSchema: AtlasToolInputSchema(properties: [:], required: [])
            ),
            SkillActionDefinition(
                id: "applescript.system_info",
                name: "System Info",
                description: "Return basic system information: macOS version, hostname, current username, and CPU type. Requires no TCC permissions.",
                inputSchemaSummary: "No inputs required.",
                outputSchemaSummary: "OS version, hostname, username, and CPU type.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.systemInfo],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(properties: [:], required: [])
            )
        ]
    }

    // MARK: - Validation

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        guard FileManager.default.fileExists(atPath: "/usr/bin/osascript") else {
            return SkillValidationResult(
                skillID: manifest.id,
                status: .failed,
                summary: "osascript not found at /usr/bin/osascript. AppleScript is not available on this system.",
                issues: ["osascript binary is missing."]
            )
        }

        do {
            let result = try await executor.run("return \"ok\"", timeout: 5)
            guard result == "ok" else {
                return SkillValidationResult(
                    skillID: manifest.id,
                    status: .failed,
                    summary: "AppleScript execution test returned an unexpected result.",
                    issues: ["osascript returned '\(result)' instead of 'ok'."]
                )
            }
        } catch {
            return SkillValidationResult(
                skillID: manifest.id,
                status: .failed,
                summary: "AppleScript execution test failed: \(error.localizedDescription)",
                issues: [error.localizedDescription]
            )
        }

        return SkillValidationResult(
            skillID: manifest.id,
            status: .passed,
            summary: "App Automator is ready. TCC permissions (Calendar, Contacts, Mail, etc.) will be requested on first use of each action."
        )
    }

    // MARK: - Execute

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "applescript.calendar_read":   return try await calendarRead(input: input, context: context)
        case "applescript.calendar_write":  return try await calendarWrite(input: input, context: context)
        case "applescript.reminders_read":  return try await remindersRead(input: input, context: context)
        case "applescript.reminders_write": return try await remindersWrite(input: input, context: context)
        case "applescript.contacts_read":   return try await contactsRead(input: input, context: context)
        case "applescript.mail_read":       return try await mailRead(input: input, context: context)
        case "applescript.safari_read":     return try await safariRead(input: input, context: context)
        case "applescript.safari_navigate": return try await safariNavigate(input: input, context: context)
        case "applescript.notes_read":      return try await notesRead(input: input, context: context)
        case "applescript.notes_write":     return try await notesWrite(input: input, context: context)
        case "applescript.music_read":      return try await musicRead(input: input, context: context)
        case "applescript.music_control":   return try await musicControl(input: input, context: context)
        case "applescript.run_custom":              return try await runCustom(input: input, context: context)
        case "applescript.mail_write":              return try await mailWrite(input: input, context: context)
        case "applescript.calendar_list_calendars": return try await calendarListCalendars(context: context)
        case "applescript.reminders_list_lists":    return try await remindersListLists(context: context)
        case "applescript.system_info":             return try await systemInfo(context: context)
        default:
            throw AtlasToolError.invalidInput("The action '\(actionID)' is not supported by App Automator.")
        }
    }

    // MARK: - Action implementations

    private func calendarRead(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(CalendarReadInput.self)
        let startDate = try parseDate(payload.startDate, field: "startDate")
        let endDate = try parseDate(payload.endDate, field: "endDate")

        context.logger.info("Executing AppleScript calendar read", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.calendar_read",
            "calendar": payload.calendarName ?? "all"
        ])

        let script = AppleScriptTemplates.listCalendarEvents(
            startDate: startDate,
            endDate: endDate,
            calendarName: payload.calendarName,
            maxResults: payload.maxResults ?? 20
        )
        return await runAndWrap(script: script, actionID: "applescript.calendar_read", timeout: 15, parseOutput: true)
    }

    private func calendarWrite(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(CalendarWriteInput.self)
        let startDate = try parseDate(payload.startDate, field: "startDate")
        let action = payload.action ?? "create"

        context.logger.info("Executing AppleScript calendar write", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.calendar_write",
            "action": action,
            "title": summarize(payload.title)
        ])

        if action == "delete" {
            let script = AppleScriptTemplates.deleteCalendarEvent(
                title: payload.title,
                startDate: startDate,
                calendarName: payload.calendarName
            )
            return await runAndWrap(script: script, actionID: "applescript.calendar_write", timeout: 15)
        }

        guard let endDateStr = payload.endDate else {
            throw AtlasToolError.invalidInput("endDate is required when action is 'create'.")
        }
        guard let calendarName = payload.calendarName else {
            throw AtlasToolError.invalidInput("calendarName is required when action is 'create'.")
        }
        let endDate = try parseDate(endDateStr, field: "endDate")

        let script = AppleScriptTemplates.createCalendarEvent(
            title: payload.title,
            startDate: startDate,
            endDate: endDate,
            calendarName: calendarName,
            notes: payload.notes,
            location: payload.location
        )
        return await runAndWrap(script: script, actionID: "applescript.calendar_write", timeout: 15)
    }

    private func remindersRead(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(RemindersReadInput.self)

        context.logger.info("Executing AppleScript reminders read", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.reminders_read",
            "list": payload.listName ?? "all"
        ])

        let script = AppleScriptTemplates.listReminders(
            listName: payload.listName,
            query: payload.query,
            includeCompleted: payload.includeCompleted ?? false,
            maxResults: payload.maxResults ?? 20
        )
        return await runAndWrap(script: script, actionID: "applescript.reminders_read", timeout: 15, parseOutput: true)
    }

    private func remindersWrite(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(RemindersWriteInput.self)

        context.logger.info("Executing AppleScript reminders write", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.reminders_write",
            "action": payload.action,
            "name": summarize(payload.name)
        ])

        let script: String
        switch payload.action {
        case "complete":
            script = AppleScriptTemplates.completeReminder(
                name: payload.name,
                listName: payload.listName
            )
        default:
            let dueDate = try payload.dueDate.map { try parseDate($0, field: "dueDate") }
            script = AppleScriptTemplates.createReminder(
                name: payload.name,
                listName: payload.listName ?? "",
                dueDate: dueDate,
                notes: payload.notes
            )
        }
        return await runAndWrap(script: script, actionID: "applescript.reminders_write", timeout: 15)
    }

    private func contactsRead(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(ContactsReadInput.self)

        context.logger.info("Executing AppleScript contacts read", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.contacts_read",
            "query": summarize(payload.query)
        ])

        let script = AppleScriptTemplates.searchContacts(
            query: payload.query,
            maxResults: payload.maxResults ?? 10
        )
        return await runAndWrap(script: script, actionID: "applescript.contacts_read", timeout: 15, parseOutput: true)
    }

    private func mailRead(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(MailReadInput.self)

        context.logger.info("Executing AppleScript mail read", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.mail_read",
            "action": payload.action,
            "mailbox": payload.mailbox ?? "all"
        ])

        switch payload.action {
        case "body":
            guard let query = payload.query else {
                throw AtlasToolError.invalidInput("query (subject string) is required when action is 'body'.")
            }
            let script = AppleScriptTemplates.readMessageBody(
                subject: query,
                mailbox: payload.mailbox,
                maxBodyChars: 4000
            )
            return await runAndWrap(script: script, actionID: "applescript.mail_read", timeout: 20)
        case "search":
            guard let query = payload.query else {
                throw AtlasToolError.invalidInput("query is required when action is 'search'.")
            }
            let script = AppleScriptTemplates.searchMessages(
                query: query,
                mailbox: payload.mailbox,
                maxResults: payload.maxResults ?? 10
            )
            return await runAndWrap(script: script, actionID: "applescript.mail_read", timeout: 20, parseOutput: true)
        default:
            let script = AppleScriptTemplates.listRecentMessages(
                mailbox: payload.mailbox,
                count: payload.maxResults ?? 10
            )
            return await runAndWrap(script: script, actionID: "applescript.mail_read", timeout: 20, parseOutput: true)
        }
    }

    private func safariRead(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(SafariReadInput.self)

        context.logger.info("Executing AppleScript Safari read", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.safari_read",
            "action": payload.action
        ])

        let script: String
        switch payload.action {
        case "all_tabs":
            script = AppleScriptTemplates.listAllTabs()
        default:
            script = AppleScriptTemplates.getCurrentTab()
        }
        return await runAndWrap(script: script, actionID: "applescript.safari_read", timeout: 10)
    }

    private func safariNavigate(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(SafariNavigateInput.self)

        context.logger.info("Executing AppleScript Safari navigate", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.safari_navigate",
            "new_tab": "\(payload.newTab ?? false)"
        ])

        // navigateToURL throws AtlasToolError.invalidInput for javascript: URLs
        let script = try AppleScriptTemplates.navigateToURL(payload.url, newTab: payload.newTab ?? false)
        return await runAndWrap(script: script, actionID: "applescript.safari_navigate", timeout: 10)
    }

    private func notesRead(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(NotesReadInput.self)

        context.logger.info("Executing AppleScript Notes read", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.notes_read",
            "action": payload.action,
            "folder": payload.folderName ?? "all"
        ])

        let script: String
        switch payload.action {
        case "search":
            guard let query = payload.query else {
                throw AtlasToolError.invalidInput("query is required when action is 'search'.")
            }
            script = AppleScriptTemplates.searchNotes(query: query, maxResults: payload.maxResults ?? 20)
        case "read":
            guard let title = payload.title else {
                throw AtlasToolError.invalidInput("title is required when action is 'read'.")
            }
            let raw = try await executor.run(
                AppleScriptTemplates.readNote(title: title, folderName: payload.folderName),
                timeout: 15
            )
            // Strip HTML from Notes body — Notes stores body as HTML
            let cleaned = stripHTML(raw)
            return SkillExecutionResult(
                skillID: manifest.id,
                actionID: "applescript.notes_read",
                output: cleaned,
                summary: "Read note '\(title)'."
            )
        default:
            script = AppleScriptTemplates.listNotes(
                folderName: payload.folderName,
                maxResults: payload.maxResults ?? 20
            )
        }
        return await runAndWrap(script: script, actionID: "applescript.notes_read", timeout: 15, parseOutput: true)
    }

    private func notesWrite(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(NotesWriteInput.self)

        context.logger.info("Executing AppleScript Notes write", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.notes_write",
            "action": payload.action,
            "title": summarize(payload.title)
        ])

        let script: String
        switch payload.action {
        case "append":
            guard let text = payload.textToAppend else {
                throw AtlasToolError.invalidInput("textToAppend is required when action is 'append'.")
            }
            script = AppleScriptTemplates.appendToNote(
                title: payload.title,
                textToAppend: text,
                folderName: payload.folderName
            )
        default:
            guard let body = payload.body else {
                throw AtlasToolError.invalidInput("body is required when action is 'create'.")
            }
            script = AppleScriptTemplates.createNote(
                title: payload.title,
                body: body,
                folderName: payload.folderName
            )
        }
        return await runAndWrap(script: script, actionID: "applescript.notes_write", timeout: 15)
    }

    private func musicRead(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(MusicReadInput.self)
        let app = payload.targetApp ?? "Music"

        context.logger.info("Executing AppleScript music read", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.music_read",
            "action": payload.action,
            "app": app
        ])

        let script: String
        switch payload.action {
        case "search":
            guard let query = payload.query else {
                throw AtlasToolError.invalidInput("query is required when action is 'search'.")
            }
            script = AppleScriptTemplates.searchLibrary(
                query: query,
                maxResults: payload.maxResults ?? 10,
                targetApp: app
            )
            return await runAndWrap(script: script, actionID: "applescript.music_read", timeout: 10, parseOutput: true)
        default:
            script = AppleScriptTemplates.getNowPlaying(targetApp: app)
        }
        return await runAndWrap(script: script, actionID: "applescript.music_read", timeout: 10)
    }

    private func musicControl(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(MusicControlInput.self)
        let app = payload.targetApp ?? "Music"

        context.logger.info("Executing AppleScript music control", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.music_control",
            "command": payload.command,
            "app": app
        ])

        let script: String
        switch payload.command {
        case "play_track":
            guard let query = payload.query, !query.trimmingCharacters(in: .whitespaces).isEmpty else {
                throw AtlasToolError.invalidInput("query is required for command 'play_track'.")
            }
            script = AppleScriptTemplates.searchAndPlay(query: query, targetApp: app)
        case "play":         script = AppleScriptTemplates.play(targetApp: app)
        case "pause":        script = AppleScriptTemplates.pause(targetApp: app)
        case "next":         script = AppleScriptTemplates.nextTrack(targetApp: app)
        case "previous":     script = AppleScriptTemplates.previousTrack(targetApp: app)
        case "set_volume":
            guard let volume = payload.volume else {
                throw AtlasToolError.invalidInput("volume (0–100) is required for command 'set_volume'.")
            }
            script = AppleScriptTemplates.setVolume(volume, targetApp: app)
        case "set_shuffle":
            guard let enabled = payload.enabled else {
                throw AtlasToolError.invalidInput("enabled (true/false) is required for command 'set_shuffle'.")
            }
            script = AppleScriptTemplates.setShuffle(enabled, targetApp: app)
        default:
            throw AtlasToolError.invalidInput("Unknown music command '\(payload.command)'. Valid commands: play_track, play, pause, next, previous, set_volume, set_shuffle.")
        }
        return await runAndWrap(script: script, actionID: "applescript.music_control", timeout: 10)
    }

    private func runCustom(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(RunCustomInput.self)
        let timeout = TimeInterval(min(payload.timeoutSeconds ?? 15, 60))

        // Log description and character count only — never log full script text.
        // Note: the full script is visible to the user in the approval request, since the
        // input JSON (containing the script field) is surfaced by the gateway approval system.
        context.logger.info("Executing custom AppleScript", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.run_custom",
            "description": summarize(payload.description),
            "script_length": "\(payload.script.count)"
        ])

        try AppleScriptValidator().validate(payload.script)
        return await runAndWrap(script: payload.script, actionID: "applescript.run_custom", timeout: timeout)
    }

    private func mailWrite(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(MailWriteInput.self)
        let autoSend = payload.autoSend ?? false

        context.logger.info("Executing AppleScript mail write", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.mail_write",
            "auto_send": "\(autoSend)",
            "to": summarize(payload.to)
        ])

        let script = AppleScriptTemplates.composeMail(
            to: payload.to,
            subject: payload.subject,
            body: payload.body,
            autoSend: autoSend
        )
        return await runAndWrap(script: script, actionID: "applescript.mail_write", timeout: 20)
    }

    private func calendarListCalendars(
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        context.logger.info("Executing AppleScript calendar list calendars", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.calendar_list_calendars"
        ])
        let script = AppleScriptTemplates.listCalendars()
        return await runAndWrap(script: script, actionID: "applescript.calendar_list_calendars", timeout: 10, parseOutput: true)
    }

    private func remindersListLists(
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        context.logger.info("Executing AppleScript reminders list lists", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.reminders_list_lists"
        ])
        let script = AppleScriptTemplates.listReminderLists()
        return await runAndWrap(script: script, actionID: "applescript.reminders_list_lists", timeout: 10, parseOutput: true)
    }

    private func systemInfo(
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        context.logger.info("Executing AppleScript system info", metadata: [
            "skill_id": manifest.id,
            "action_id": "applescript.system_info"
        ])
        let script = AppleScriptTemplates.systemInfo()
        return await runAndWrap(script: script, actionID: "applescript.system_info", timeout: 10)
    }

    // MARK: - Shared helpers

    /// Runs a script and wraps the result as a SkillExecutionResult.
    /// TCC errors and timeouts become success:false results rather than thrown errors,
    /// so the agent loop receives a useful message instead of an exception.
    /// When parseOutput is true, KEY:value\n---\n delimited output is converted to a JSON array.
    private func runAndWrap(
        script: String,
        actionID: String,
        timeout: TimeInterval,
        parseOutput: Bool = false
    ) async -> SkillExecutionResult {
        do {
            let raw = try await executor.run(script, timeout: timeout)
            let output = parseOutput ? parseIfDelimited(raw) : raw
            return SkillExecutionResult(
                skillID: manifest.id,
                actionID: actionID,
                output: output,
                success: true,
                summary: raw.components(separatedBy: "\n").first ?? raw
            )
        } catch {
            let message = error.localizedDescription
            return SkillExecutionResult(
                skillID: manifest.id,
                actionID: actionID,
                output: message,
                success: false,
                summary: message
            )
        }
    }

    /// Converts KEY:value\n---\n delimited text into a JSON array of objects.
    /// Falls back to the raw string if the output doesn't contain the delimiter or parsing fails.
    private func parseIfDelimited(_ raw: String) -> String {
        guard raw.contains("---") else { return raw }

        let records = raw.components(separatedBy: "---")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }

        guard !records.isEmpty else { return raw }

        let objects: [[String: String]] = records.compactMap { record in
            let lines = record.components(separatedBy: "\n")
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty }
            guard !lines.isEmpty else { return nil }

            var dict: [String: String] = [:]
            for line in lines {
                // Split on the FIRST colon only — values may contain colons (e.g. timestamps)
                guard let colonIdx = line.firstIndex(of: ":") else { continue }
                let key = String(line[line.startIndex..<colonIdx])
                    .lowercased()
                    .trimmingCharacters(in: .whitespaces)
                let value = String(line[line.index(after: colonIdx)...])
                    .trimmingCharacters(in: .whitespaces)
                if !key.isEmpty && !value.isEmpty {
                    dict[key] = value
                }
            }
            return dict.isEmpty ? nil : dict
        }

        guard !objects.isEmpty,
              let jsonData = try? JSONSerialization.data(withJSONObject: objects, options: .prettyPrinted),
              let jsonString = String(data: jsonData, encoding: .utf8) else {
            return raw
        }
        return jsonString
    }

    private func parseDate(_ string: String, field: String) throws -> Date {
        let formatters: [ISO8601DateFormatter] = [
            { let f = ISO8601DateFormatter(); f.formatOptions = [.withInternetDateTime]; return f }(),
            { let f = ISO8601DateFormatter(); f.formatOptions = [.withFullDate, .withTime, .withColonSeparatorInTime]; return f }(),
            { let f = ISO8601DateFormatter(); f.formatOptions = [.withFullDate]; return f }()
        ]
        for formatter in formatters {
            if let date = formatter.date(from: string) { return date }
        }
        throw AtlasToolError.invalidInput("'\(field)' could not be parsed as an ISO 8601 date. Provide a value like '2026-03-22T09:00:00'.")
    }

    private func stripHTML(_ html: String) -> String {
        guard let regex = try? NSRegularExpression(pattern: "<[^>]+>") else { return html }
        let range = NSRange(html.startIndex..., in: html)
        let stripped = regex.stringByReplacingMatches(in: html, range: range, withTemplate: "")
        return stripped
            .replacingOccurrences(of: "&amp;", with: "&")
            .replacingOccurrences(of: "&lt;", with: "<")
            .replacingOccurrences(of: "&gt;", with: ">")
            .replacingOccurrences(of: "&nbsp;", with: " ")
            .replacingOccurrences(of: "&quot;", with: "\"")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func summarize(_ value: String) -> String {
        value.count <= 120 ? value : String(value.prefix(117)) + "..."
    }
}

// MARK: - Input types

private struct CalendarReadInput: Decodable {
    let startDate: String
    let endDate: String
    let calendarName: String?
    let maxResults: Int?
}

private struct CalendarWriteInput: Decodable {
    let action: String?
    let title: String
    let startDate: String
    let endDate: String?
    let calendarName: String?
    let notes: String?
    let location: String?
}

private struct RemindersReadInput: Decodable {
    let listName: String?
    let query: String?
    let includeCompleted: Bool?
    let maxResults: Int?
}

private struct RemindersWriteInput: Decodable {
    let action: String
    let name: String
    let listName: String?
    let dueDate: String?
    let notes: String?
}

private struct ContactsReadInput: Decodable {
    let query: String
    let maxResults: Int?
}

private struct MailReadInput: Decodable {
    let action: String
    let query: String?
    let mailbox: String?
    let maxResults: Int?
}

private struct SafariReadInput: Decodable {
    let action: String
}

private struct SafariNavigateInput: Decodable {
    let url: String
    let newTab: Bool?
}

private struct NotesReadInput: Decodable {
    let action: String
    let query: String?
    let title: String?
    let folderName: String?
    let maxResults: Int?
}

private struct NotesWriteInput: Decodable {
    let action: String
    let title: String
    let body: String?
    let textToAppend: String?
    let folderName: String?
}

private struct MusicReadInput: Decodable {
    let action: String
    let query: String?
    let targetApp: String?
    let maxResults: Int?
}

private struct MusicControlInput: Decodable {
    let command: String
    let query: String?
    let volume: Int?
    let enabled: Bool?
    let targetApp: String?
}

private struct RunCustomInput: Decodable {
    let script: String
    let description: String
    let timeoutSeconds: Int?
}

private struct MailWriteInput: Decodable {
    let to: String
    let subject: String
    let body: String
    let autoSend: Bool?
}
