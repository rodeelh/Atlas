import XCTest
import AtlasCore
import AtlasSkills
import AtlasShared

final class SkillRoutingPolicyTests: XCTestCase {
    func testWeatherQueriesPreferWeatherSkillOverWebResearch() {
        let policy = SkillRoutingPolicy()
        // "will it rain" is a forecast trigger; "weather in " (trailing space) triggers .weather
        let context = makeContext(message: "Will it rain in Boston tomorrow?")

        let decision = policy.decision(for: context)
        let ranked = policy.rank(context.actionCatalog, with: decision)

        XCTAssertEqual(decision.intent, .liveStructuredData)
        XCTAssertEqual(decision.queryType, .forecast)
        XCTAssertEqual(decision.preferredSkills.first, "weather")
        XCTAssertEqual(ranked.first?.skillID, "weather")
    }

    func testTimeQueriesPreferInfoSkillOverWebResearch() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "What time is it in Tokyo right now?")

        let decision = policy.decision(for: context)
        let ranked = policy.rank(context.actionCatalog, with: decision)

        XCTAssertEqual(decision.intent, .liveStructuredData)
        XCTAssertEqual(decision.queryType, .currentTime)
        XCTAssertEqual(decision.preferredSkills.first, "info")
        XCTAssertEqual(ranked.first?.skillID, "info")
    }

    func testSystemActionQueriesPreferSystemActionsSkill() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Open Xcode for me.")

        let decision = policy.decision(for: context)
        let ranked = policy.rank(context.actionCatalog, with: decision)

        XCTAssertEqual(decision.intent, .atlasSystemTask)
        XCTAssertEqual(decision.queryType, .openApp)
        XCTAssertEqual(decision.preferredSkills.first, "system-actions")
        XCTAssertEqual(ranked.first?.skillID, "system-actions")
    }

    func testImageQueriesPreferImageGenerationSkill() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Generate an app icon for Atlas.")

        let decision = policy.decision(for: context)
        let ranked = policy.rank(context.actionCatalog, with: decision)

        XCTAssertEqual(decision.queryType, .imageGenerate)
        XCTAssertEqual(decision.preferredSkills.first, "image-generation")
        XCTAssertEqual(ranked.first?.skillID, "image-generation")
        XCTAssertTrue(decision.routingHints.contains(where: { $0.text.contains("single active provider configured in Settings") }))
    }

    func testFileQueriesPreferFileSystemSkill() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Read the README.md file in the repo.")

        let decision = policy.decision(for: context)
        let ranked = policy.rank(context.actionCatalog, with: decision)

        XCTAssertEqual(decision.intent, .localFileTask)
        XCTAssertEqual(decision.preferredSkills.first, "file-system")
        XCTAssertEqual(ranked.first?.skillID, "file-system")
    }

    func testResearchQueriesPreferWebResearchSkill() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Compare the latest SwiftData and Core Data documentation guidance.")

        let decision = policy.decision(for: context)
        let ranked = policy.rank(context.actionCatalog, with: decision)

        XCTAssertEqual(decision.intent, .exploratoryResearch)
        XCTAssertEqual(decision.preferredSkills.first, "web-research")
        XCTAssertEqual(ranked.first?.skillID, "web-research")
    }

    func testUnclearQueriesFallBackWithoutSuppressingSkills() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Think through a better naming scheme for this feature.")

        let decision = policy.decision(for: context)
        let ranked = policy.rank(context.actionCatalog, with: decision)

        XCTAssertEqual(decision.intent, .generalReasoning)
        XCTAssertTrue(decision.preferredSkills.isEmpty)
        XCTAssertTrue(decision.suppressedSkills.isEmpty)
        XCTAssertEqual(ranked.count, context.actionCatalog.count)
    }

    // MARK: - AppleScript / App Automation routing

    func testCalendarQueryRouteToAppleScriptSkill() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "What's on my calendar today?")

        let decision = policy.decision(for: context)
        let ranked = policy.rank(context.actionCatalog, with: decision)

        XCTAssertEqual(decision.intent, .appAutomation)
        XCTAssertEqual(decision.preferredSkills.first, "applescript-automation")
        XCTAssertEqual(ranked.first?.skillID, "applescript-automation")
    }

    func testUpcomingMeetingsQueryRoutesToAppAutomation() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Do I have any upcoming meetings this week?")

        let decision = policy.decision(for: context)

        XCTAssertEqual(decision.intent, .appAutomation)
        XCTAssertEqual(decision.queryType, .calendarRead)
        XCTAssertEqual(decision.preferredSkills.first, "applescript-automation")
    }

    func testRemindMeQueryRoutesToAppAutomation() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Remind me to call John at 3pm.")

        let decision = policy.decision(for: context)

        XCTAssertEqual(decision.intent, .appAutomation)
        XCTAssertEqual(decision.queryType, .remindersWrite)
        XCTAssertEqual(decision.preferredSkills.first, "applescript-automation")
    }

    func testNotesQueryRoutesToAppAutomation() {
        let policy = SkillRoutingPolicy()
        // "write a note" is a notesWrite trigger; "Put this in a note" has no matching trigger
        let context = makeContext(message: "Write a note about the project kickoff meeting.")

        let decision = policy.decision(for: context)

        XCTAssertEqual(decision.intent, .appAutomation)
        XCTAssertEqual(decision.preferredSkills.first, "applescript-automation")
    }

    func testMusicControlQueryRoutesToAppAutomation() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Pause music.")

        let decision = policy.decision(for: context)

        XCTAssertEqual(decision.intent, .appAutomation)
        XCTAssertEqual(decision.queryType, .musicControl)
        XCTAssertEqual(decision.preferredSkills.first, "applescript-automation")
    }

    func testNowPlayingQueryRoutesToMusicRead() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "What's currently playing in Music?")

        let decision = policy.decision(for: context)

        XCTAssertEqual(decision.intent, .appAutomation)
        XCTAssertEqual(decision.queryType, .musicRead)
        XCTAssertEqual(decision.preferredSkills.first, "applescript-automation")
    }

    func testInboxQueryRoutesToMailRead() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Check my inbox for unread emails.")

        let decision = policy.decision(for: context)

        XCTAssertEqual(decision.intent, .appAutomation)
        XCTAssertEqual(decision.queryType, .mailRead)
        XCTAssertEqual(decision.preferredSkills.first, "applescript-automation")
    }

    func testSafariTabQueryRoutesToSafariRead() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "What's open in my current tab?")

        let decision = policy.decision(for: context)

        XCTAssertEqual(decision.intent, .appAutomation)
        XCTAssertEqual(decision.queryType, .safariRead)
        XCTAssertEqual(decision.preferredSkills.first, "applescript-automation")
    }

    func testAppAutomationRoutingHintIsInjected() {
        let policy = SkillRoutingPolicy()
        let context = makeContext(message: "Add a reminder to buy groceries.")

        let decision = policy.decision(for: context)

        XCTAssertTrue(decision.routingHints.contains(where: { $0.targetSkillID == "applescript-automation" }))
    }

    private func makeContext(message: String) -> SkillRoutingContext {
        let skills: [any AtlasSkill] = [
            InfoSkill(),
            ImageGenerationSkill(),
            SystemActionSkill(scopeStore: FileAccessScopeStore()),
            WeatherSkill(),
            WebResearchSkill(),
            FileSystemSkill(scopeStore: FileAccessScopeStore()),
            AtlasInfoSkill(),
            AppleScriptSkill()
        ]

        let records = skills.map {
            AtlasSkillRecord(
                manifest: $0.manifest.updatingLifecycleState(.enabled),
                actions: $0.actions,
                validation: nil
            )
        }

        let catalog = records.flatMap { record in
            record.actions.map { action in
                SkillActionCatalogItem(
                    skillID: record.manifest.id,
                    skillName: record.manifest.name,
                    skillDescription: record.manifest.description,
                    skillCategory: record.manifest.category,
                    trustProfile: record.manifest.trustProfile,
                    freshnessType: record.manifest.freshnessType,
                    action: action,
                    riskLevel: record.manifest.riskLevel,
                    preferredQueryTypes: Array(Set(record.manifest.preferredQueryTypes + action.preferredQueryTypes)),
                    routingPriority: record.manifest.routingPriority + action.routingPriority,
                    canAnswerStructuredLiveData: record.manifest.canAnswerStructuredLiveData,
                    canHandleLocalData: record.manifest.canHandleLocalData,
                    canHandleExploratoryQueries: record.manifest.canHandleExploratoryQueries
                )
            }
        }

        return SkillRoutingContext(
            userMessage: message,
            enabledSkills: records,
            actionCatalog: catalog
        )
    }
}
