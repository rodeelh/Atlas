import AppKit
import EventKit
import Contacts
import Foundation
import AtlasShared
import AtlasSkills

@MainActor
final class OnboardingViewModel: ObservableObject {
    struct CredentialSnapshot: Equatable {
        let openAI: CredentialAvailability
        let anthropic: CredentialAvailability
        let gemini: CredentialAvailability
        let lmStudio: CredentialAvailability
        let braveSearch: CredentialAvailability
        let telegram: CredentialAvailability
        let discord: CredentialAvailability

        static let empty = CredentialSnapshot(
            openAI: .missing,
            anthropic: .missing,
            gemini: .missing,
            lmStudio: .configured,
            braveSearch: .missing,
            telegram: .missing,
            discord: .missing
        )
    }

    enum Step: Int, CaseIterable, Identifiable {
        case welcome
        case identity
        case aiProvider
        case channels
        case skillKeys
        case permissions
        case network
        case installService
        case ready

        var id: Int { rawValue }

        var title: String {
            switch self {
            case .welcome:        return "Welcome"
            case .identity:       return "Identity"
            case .aiProvider:     return "AI Model"
            case .channels:       return "Channels"
            case .skillKeys:      return "Integrations"
            case .permissions:    return "Access"
            case .network:        return "Network"
            case .installService: return "Background Service"
            case .ready:          return "Ready"
            }
        }
    }

    enum PermissionStatus: Equatable {
        case notDetermined, granted, denied
        var isGranted: Bool { self == .granted }
    }

    struct DerivedPreferences: Equatable {
        let temperatureUnit: String
        let timeFormat: String
        let dateFormat: String
    }

    // MARK: - Step state

    @Published var currentStep: Step = .welcome

    // Identity
    @Published var userName: String
    @Published var assistantName: String
    @Published var location: String

    // About You
    @Published var aboutYou: String = ""

    // Action Safety
    @Published var actionSafetyMode: AtlasActionSafetyMode

    // API keys
    @Published var selectedAIProvider: AIProvider = .openAI
    @Published var openAIKey = ""
    @Published var anthropicKey = ""
    @Published var geminiKey = ""
    @Published var lmStudioBaseURL = "http://localhost:1234"
    @Published var braveSearchKey = ""
    @Published var telegramToken = ""
    @Published var telegramEnabled = false
    @Published var discordToken = ""
    @Published var discordEnabled = false
    @Published var isSavingKey = false
    @Published var keyError: String?

    // Pre-existing key status (relevant on re-run)
    @Published private(set) var openAIKeyAlreadySet: Bool
    @Published private(set) var anthropicKeyAlreadySet: Bool
    @Published private(set) var geminiKeyAlreadySet: Bool
    @Published private(set) var lmStudioReady = true
    @Published private(set) var braveSearchKeyAlreadySet: Bool
    @Published private(set) var telegramTokenAlreadySet: Bool
    @Published private(set) var discordTokenAlreadySet: Bool

    // File access
    @Published var desktopGranted = false
    @Published var documentsGranted = false
    @Published var downloadsGranted = false

    // System permissions
    @Published var calendarStatus: PermissionStatus = .notDetermined
    @Published var remindersStatus: PermissionStatus = .notDetermined
    @Published var contactsStatus: PermissionStatus = .notDetermined

    // Daemon install
    @Published var isInstallingDaemon = false
    @Published var installError: String?
    @Published var daemonInstalled = false

    // General
    @Published var isSaving = false
    @Published var errorMessage: String?

    private let locale: Locale
    private let timeZone: TimeZone

    init(
        locale: Locale = .autoupdatingCurrent,
        timeZone: TimeZone = .autoupdatingCurrent,
        userName: String = NSFullUserName(),
        assistantName: String = AtlasConfig().personaName,
        actionSafetyMode: AtlasActionSafetyMode = AtlasConfig().actionSafetyMode,
        credentialSnapshot: CredentialSnapshot = .empty
    ) {
        self.locale = locale
        self.timeZone = timeZone
        self.userName = userName.trimmingCharacters(in: .whitespacesAndNewlines)
        self.assistantName = assistantName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "Atlas" : assistantName
        self.location = Self.defaultLocation(locale: locale, timeZone: timeZone)
        self.actionSafetyMode = actionSafetyMode
        self.openAIKeyAlreadySet = credentialSnapshot.openAI.isConfigured
        self.anthropicKeyAlreadySet = credentialSnapshot.anthropic.isConfigured
        self.geminiKeyAlreadySet = credentialSnapshot.gemini.isConfigured
        self.lmStudioReady = credentialSnapshot.lmStudio.isConfigured
        self.braveSearchKeyAlreadySet = credentialSnapshot.braveSearch.isConfigured
        self.telegramTokenAlreadySet = credentialSnapshot.telegram.isConfigured
        self.discordTokenAlreadySet = credentialSnapshot.discord.isConfigured
        self.telegramEnabled = credentialSnapshot.telegram.isConfigured
        self.discordEnabled = credentialSnapshot.discord.isConfigured
        refreshPermissionStatuses()
    }

    func applyCredentialSnapshot(_ snapshot: CredentialSnapshot) {
        openAIKeyAlreadySet = snapshot.openAI.isConfigured
        anthropicKeyAlreadySet = snapshot.anthropic.isConfigured
        geminiKeyAlreadySet = snapshot.gemini.isConfigured
        lmStudioReady = snapshot.lmStudio.isConfigured
        braveSearchKeyAlreadySet = snapshot.braveSearch.isConfigured
        telegramTokenAlreadySet = snapshot.telegram.isConfigured
        discordTokenAlreadySet = snapshot.discord.isConfigured
        telegramEnabled = snapshot.telegram.isConfigured
        discordEnabled = snapshot.discord.isConfigured
        keyError = nil
        if case .keychainError(let message) = snapshot.openAI {
            keyError = message
        } else if case .keychainError(let message) = snapshot.anthropic {
            keyError = message
        } else if case .keychainError(let message) = snapshot.gemini {
            keyError = message
        } else if case .keychainError(let message) = snapshot.telegram {
            keyError = message
        } else if case .keychainError(let message) = snapshot.discord {
            keyError = message
        } else if case .keychainError(let message) = snapshot.braveSearch {
            keyError = message
        }
    }

    // MARK: - Derived

    var progressValue: Double {
        Double(currentStep.rawValue + 1) / Double(Step.allCases.count)
    }

    var canGoBack: Bool {
        currentStep != .welcome
    }

    var primaryButtonTitle: String {
        switch currentStep {
        case .ready:
            return "Start Using Atlas"
        case .aiProvider:
            return (openAIKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !openAIKeyAlreadySet) ? "Skip" : "Continue"
        case .installService:
            return daemonInstalled ? "Continue" : "Skip"
        default:
            return "Continue"
        }
    }

    var isBusy: Bool {
        isSaving || isSavingKey || isInstallingDaemon
    }

    var derivedPreferences: DerivedPreferences {
        Self.derivePreferences(from: location, locale: locale)
    }

    // MARK: - Navigation

    func goBack() {
        guard let previous = Step(rawValue: currentStep.rawValue - 1) else { return }
        currentStep = previous
    }

    func goForward() {
        errorMessage = nil
        switch currentStep {
        case .identity:
            guard !assistantName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                errorMessage = "Choose what you'd like Atlas to be called."
                return
            }
        default:
            break
        }
        guard let next = Step(rawValue: currentStep.rawValue + 1) else { return }
        currentStep = next
    }

    // MARK: - Async actions

    func saveAIProviderAndAdvance(using appState: AtlasAppState) async {
        isSavingKey = true
        keyError = nil
        defer { isSavingKey = false }

        do {
            await appState.persistPreferredAIProvider(selectedAIProvider)

            switch selectedAIProvider {
            case .openAI:
                let trimmed = openAIKey.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty {
                    try await appState.updateCredential(trimmed, for: .openAI)
                }
            case .anthropic:
                let trimmed = anthropicKey.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty {
                    try await appState.updateCredential(trimmed, for: .anthropic)
                }
            case .gemini:
                let trimmed = geminiKey.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty {
                    try await appState.updateCredential(trimmed, for: .gemini)
                }
            case .lmStudio:
                await appState.persistLMStudioBaseURL(lmStudioBaseURL)
                try await appState.validateCredential(.lmStudio)
            }
        } catch {
            keyError = error.localizedDescription
            return
        }
        goForward()
    }

    func saveChannelsAndAdvance(using appState: AtlasAppState) async {
        let trimmedTelegram = telegramToken.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedDiscord = discordToken.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedTelegram.isEmpty || !trimmedDiscord.isEmpty else {
            goForward()
            return
        }
        isSavingKey = true
        keyError = nil
        defer { isSavingKey = false }
        do {
            if !trimmedTelegram.isEmpty {
                try await appState.updateCredential(trimmedTelegram, for: .telegram)
            }
            if !trimmedDiscord.isEmpty {
                try await appState.updateCredential(trimmedDiscord, for: .discord)
            }
        } catch {
            keyError = error.localizedDescription
            return
        }
        goForward()
    }

    func saveSkillKeysAndAdvance(using appState: AtlasAppState) async {
        let trimmed = braveSearchKey.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty {
            isSavingKey = true
            keyError = nil
            defer { isSavingKey = false }
            do {
                try await appState.updateCredential(trimmed, for: .braveSearch)
            } catch {
                keyError = error.localizedDescription
                return
            }
        }
        goForward()
    }

    func installDaemon(using appState: AtlasAppState) async {
        isInstallingDaemon = true
        installError = nil
        defer { isInstallingDaemon = false }
        await appState.installDaemon()
        if appState.lastError == nil {
            daemonInstalled = true
            goForward()
        } else {
            installError = appState.lastError ?? "Daemon installation failed."
        }
    }

    // MARK: - File access

    func grantFolder(_ name: String, using appState: AtlasAppState) async {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let targetURL = home.appendingPathComponent(name)

        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.resolvesAliases = true
        panel.directoryURL = targetURL
        panel.title = "Grant Atlas Access to \(name)"
        panel.prompt = "Grant Access"
        panel.message = "Atlas will be able to read and manage files inside your \(name) folder."

        guard panel.runModal() == .OK, let url = panel.url else { return }

        let success = await appState.addFolderBookmark(for: url)
        if success {
            switch name {
            case "Desktop":   desktopGranted = true
            case "Documents": documentsGranted = true
            case "Downloads": downloadsGranted = true
            default: break
            }
        }
    }

    // MARK: - System permissions

    func refreshPermissionStatuses() {
        // Calendar
        switch EKEventStore.authorizationStatus(for: .event) {
        case .fullAccess:             calendarStatus = .granted
        case .denied, .restricted:    calendarStatus = .denied
        default:                      calendarStatus = .notDetermined
        }
        // Reminders
        switch EKEventStore.authorizationStatus(for: .reminder) {
        case .fullAccess:             remindersStatus = .granted
        case .denied, .restricted:    remindersStatus = .denied
        default:                      remindersStatus = .notDetermined
        }
        // Contacts
        switch CNContactStore.authorizationStatus(for: .contacts) {
        case .authorized:             contactsStatus = .granted
        case .denied, .restricted:    contactsStatus = .denied
        default:                      contactsStatus = .notDetermined
        }
    }

    func refreshAccessState(using appState: AtlasAppState) {
        refreshPermissionStatuses()
        syncApprovedFolders(appState.approvedFileAccessRoots)
    }

    private func syncApprovedFolders(_ roots: [ApprovedFileAccessRoot]) {
        let grantedPaths = Set(roots.map { URL(fileURLWithPath: $0.path).standardizedFileURL.path })
        let home = FileManager.default.homeDirectoryForCurrentUser
        desktopGranted = grantedPaths.contains(home.appendingPathComponent("Desktop").standardizedFileURL.path)
        documentsGranted = grantedPaths.contains(home.appendingPathComponent("Documents").standardizedFileURL.path)
        downloadsGranted = grantedPaths.contains(home.appendingPathComponent("Downloads").standardizedFileURL.path)
    }

    func requestCalendarPermission() async {
        let store = EKEventStore()
        if #available(macOS 14, *) {
            do {
                try await store.requestFullAccessToEvents()
                calendarStatus = .granted
            } catch {
                calendarStatus = .denied
            }
        } else {
            let granted = await withCheckedContinuation { continuation in
                store.requestAccess(to: .event) { granted, _ in continuation.resume(returning: granted) }
            }
            calendarStatus = granted ? .granted : .denied
        }
    }

    func requestRemindersPermission() async {
        let store = EKEventStore()
        if #available(macOS 14, *) {
            do {
                try await store.requestFullAccessToReminders()
                remindersStatus = .granted
            } catch {
                remindersStatus = .denied
            }
        } else {
            let granted = await withCheckedContinuation { continuation in
                store.requestAccess(to: .reminder) { granted, _ in continuation.resume(returning: granted) }
            }
            remindersStatus = granted ? .granted : .denied
        }
    }

    func requestContactsPermission() async {
        let store = CNContactStore()
        let granted = await withCheckedContinuation { continuation in
            store.requestAccess(for: .contacts) { granted, _ in continuation.resume(returning: granted) }
        }
        contactsStatus = granted ? .granted : .denied
    }

    // MARK: - Completion

    func complete(using appState: AtlasAppState) async -> Bool {
        guard !isSaving else { return false }
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }

        do {
            try await appState.completeOnboarding(
                userName: userName,
                assistantName: assistantName,
                location: location,
                inferredTemperatureUnit: derivedPreferences.temperatureUnit,
                inferredTimeFormat: derivedPreferences.timeFormat,
                inferredDateFormat: derivedPreferences.dateFormat,
                aboutYou: aboutYou,
                actionSafetyMode: actionSafetyMode
            )
            return true
        } catch {
            errorMessage = error.localizedDescription
            return false
        }
    }

    func skip(using appState: AtlasAppState) async -> Bool {
        guard !isSaving else { return false }

        let fallbackAssistantName = assistantName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "Atlas" : assistantName
        let fallbackLocation = location.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? Self.defaultLocation(locale: locale, timeZone: timeZone)
            : location
        let derived = Self.derivePreferences(from: fallbackLocation, locale: locale)

        isSaving = true
        errorMessage = nil
        defer { isSaving = false }

        do {
            try await appState.completeOnboarding(
                userName: userName,
                assistantName: fallbackAssistantName,
                location: fallbackLocation,
                inferredTemperatureUnit: derived.temperatureUnit,
                inferredTimeFormat: derived.timeFormat,
                inferredDateFormat: derived.dateFormat,
                aboutYou: aboutYou,
                actionSafetyMode: actionSafetyMode
            )
            return true
        } catch {
            errorMessage = error.localizedDescription
            return false
        }
    }

    // MARK: - Location / locale helpers

    static func defaultLocation(locale: Locale = .autoupdatingCurrent, timeZone: TimeZone = .autoupdatingCurrent) -> String {
        let city = normalizedCityName(from: timeZone.identifier)
        let regionCode = currentRegionCode(for: locale)
        let region = regionCode.flatMap { locale.localizedString(forRegionCode: $0) } ?? regionCode

        switch (city?.isEmpty == false ? city : nil, region?.isEmpty == false ? region : nil) {
        case let (city?, region?):  return "\(city), \(region)"
        case let (city?, nil):      return city
        case let (nil, region?):    return region
        default:                    return ""
        }
    }

    static func derivePreferences(from location: String, locale: Locale = .autoupdatingCurrent) -> DerivedPreferences {
        let regionCode = inferredRegionCode(from: location, locale: locale)
        let usesFahrenheit = ["US", "BS", "BZ", "KY", "LR", "PW"].contains(regionCode)
        let usesTwelveHourTime = twelveHourRegions.contains(regionCode)

        let dateFormat: String
        switch regionCode {
        case "US":             dateFormat = "Month/Day/Year"
        case "JP", "CN", "KR": dateFormat = "Year/Month/Day"
        default:               dateFormat = "Day/Month/Year"
        }

        return DerivedPreferences(
            temperatureUnit: usesFahrenheit ? "Fahrenheit" : "Celsius",
            timeFormat: usesTwelveHourTime ? "12-hour time" : "24-hour time",
            dateFormat: dateFormat
        )
    }

    private static func inferredRegionCode(from location: String, locale: Locale) -> String {
        let normalized = location.uppercased()
        let tokenMap: [(String, String)] = [
            ("UNITED STATES", "US"), ("USA", "US"), ("U.S.", "US"),
            ("BAHAMAS", "BS"), ("BELIZE", "BZ"), ("CAYMAN", "KY"),
            ("LIBERIA", "LR"), ("PALAU", "PW"), ("JAPAN", "JP"),
            ("CHINA", "CN"), ("KOREA", "KR"), ("UNITED KINGDOM", "GB"),
            ("ENGLAND", "GB"), ("SCOTLAND", "GB"), ("WALES", "GB"),
            ("CANADA", "CA"), ("AUSTRALIA", "AU")
        ]
        for (token, code) in tokenMap where normalized.contains(token) { return code }
        if usStateTokens.contains(where: { normalized.contains($0) }) { return "US" }
        return currentRegionCode(for: locale) ?? "US"
    }

    private static func normalizedCityName(from timeZoneIdentifier: String) -> String? {
        let components = timeZoneIdentifier.split(separator: "/")
        guard let rawCity = components.last else { return nil }
        let city = rawCity.replacingOccurrences(of: "_", with: " ")
        guard !city.isEmpty, city != "GMT", city != "UTC" else { return nil }
        return city
    }

    private static func currentRegionCode(for locale: Locale) -> String? {
        if #available(macOS 13, *) { return locale.region?.identifier }
        return (locale as NSLocale).object(forKey: .countryCode) as? String
    }

    private static let usStateTokens: Set<String> = [
        "ALABAMA", "ALASKA", "ARIZONA", "ARKANSAS", "CALIFORNIA", "COLORADO", "CONNECTICUT",
        "DELAWARE", "FLORIDA", "GEORGIA", "HAWAII", "IDAHO", "ILLINOIS", "INDIANA", "IOWA",
        "KANSAS", "KENTUCKY", "LOUISIANA", "MAINE", "MARYLAND", "MASSACHUSETTS", "MICHIGAN",
        "MINNESOTA", "MISSISSIPPI", "MISSOURI", "MONTANA", "NEBRASKA", "NEVADA", "NEW HAMPSHIRE",
        "NEW JERSEY", "NEW MEXICO", "NEW YORK", "NORTH CAROLINA", "NORTH DAKOTA", "OHIO",
        "OKLAHOMA", "OREGON", "PENNSYLVANIA", "RHODE ISLAND", "SOUTH CAROLINA", "SOUTH DAKOTA",
        "TENNESSEE", "TEXAS", "UTAH", "VERMONT", "VIRGINIA", "WASHINGTON", "WEST VIRGINIA",
        "WISCONSIN", "WYOMING", "WASHINGTON, DC", "D.C."
    ]

    private static let twelveHourRegions: Set<String> = [
        "US", "AU", "CA", "NZ", "PH", "IN", "MY", "EG", "SA", "CO", "PK"
    ]
}
