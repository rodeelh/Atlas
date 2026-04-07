import SwiftUI
import AppKit
import AtlasShared

// MARK: - Step visual metadata (kept out of ViewModel to avoid SwiftUI dependency)

private extension OnboardingViewModel.Step {
    var icon: String {
        switch self {
        case .welcome:        return "sparkles"
        case .identity:       return "person.fill"
        case .aiProvider:     return "brain"
        case .channels:       return "bubble.left.and.bubble.right.fill"
        case .skillKeys:      return "puzzlepiece.fill"
        case .permissions:    return "folder.badge.gear"
        case .network:        return "antenna.radiowaves.left.and.right"
        case .installService: return "gearshape.2.fill"
        case .ready:          return "checkmark.seal.fill"
        }
    }

    var iconTint: Color {
        switch self {
        case .welcome:        return .accentColor
        case .identity:       return .blue
        case .aiProvider:     return .purple
        case .channels:       return .teal
        case .skillKeys:      return .orange
        case .permissions:    return Color(red: 0.9, green: 0.5, blue: 0.1)
        case .network:        return .green
        case .installService: return .indigo
        case .ready:          return .green
        }
    }

    var headline: String {
        switch self {
        case .welcome:        return "Welcome to Atlas"
        case .identity:       return "Let's start with the basics"
        case .aiProvider:     return "Connect an AI provider"
        case .channels:       return "Pick your channels"
        case .skillKeys:      return "Skill integrations"
        case .permissions:    return "Access & permissions"
        case .network:        return "Remote access"
        case .installService: return "Install background service"
        case .ready:          return "You're all set"
        }
    }

    var subtitle: String {
        switch self {
        case .welcome:        return "Your personal AI operator for Mac."
        case .identity:       return "Help Atlas know who it's working with."
        case .aiProvider:     return "Atlas needs an AI model to power conversations."
        case .channels:       return "Choose where you want to reach Atlas from."
        case .skillKeys:      return "Optional API keys that unlock additional skills."
        case .permissions:    return "Grant access so Atlas can work with your files and apps."
        case .network:        return "Access Atlas from your phone or other devices on the network."
        case .installService: return "A lightweight launchd agent keeps Atlas running."
        case .ready:          return "Atlas will use these settings right away."
        }
    }
}

// MARK: - Main flow view

struct OnboardingFlowView: View {
    @ObservedObject var appState: AtlasAppState
    @StateObject private var viewModel: OnboardingViewModel
    @State private var goingForward = true

    init(appState: AtlasAppState) {
        self.appState = appState
        _viewModel = StateObject(
            wrappedValue: OnboardingViewModel(
                credentialSnapshot: OnboardingViewModel.CredentialSnapshot(
                    openAI: appState.credentialAvailability(for: .openAI),
                    anthropic: appState.credentialAvailability(for: .anthropic),
                    gemini: appState.credentialAvailability(for: .gemini),
                    lmStudio: appState.credentialAvailability(for: .lmStudio),
                    braveSearch: appState.credentialAvailability(for: .braveSearch),
                    telegram: appState.credentialAvailability(for: .telegram),
                    discord: appState.credentialAvailability(for: .discord)
                )
            )
        )
    }

    var body: some View {
        VStack(spacing: 0) {
            contentArea
                .frame(maxWidth: .infinity, maxHeight: .infinity)

            if let error = viewModel.errorMessage {
                Text(error)
                    .font(.subheadline)
                    .foregroundStyle(.red)
                    .padding(.horizontal, 36)
                    .padding(.bottom, 6)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }

            footerBar
        }
        .frame(width: 720, height: 600)
        .background(backgroundView)
        .onAppear {
            viewModel.refreshAccessState(using: appState)
            applyCredentialSnapshot()
        }
        .onChange(of: appState.onboardingCredentialSnapshot) { _, _ in
            applyCredentialSnapshot()
        }
        .onChange(of: appState.approvedFileAccessRoots) { _, _ in
            viewModel.refreshAccessState(using: appState)
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            viewModel.refreshAccessState(using: appState)
        }
    }

    private func applyCredentialSnapshot() {
        let snapshot = appState.onboardingCredentialSnapshot
        viewModel.applyCredentialSnapshot(
            OnboardingViewModel.CredentialSnapshot(
                openAI: snapshot.openAI,
                anthropic: snapshot.anthropic,
                gemini: snapshot.gemini,
                lmStudio: snapshot.lmStudio,
                braveSearch: snapshot.braveSearch,
                telegram: snapshot.telegram,
                discord: snapshot.discord
            )
        )
        viewModel.selectedAIProvider = appState.activeAIProvider
        viewModel.lmStudioBaseURL = appState.lmStudioBaseURL
    }

    // MARK: Background

    private var backgroundView: some View {
        ZStack {
            Color(nsColor: .windowBackgroundColor)
            LinearGradient(
                colors: [
                    Color.accentColor.opacity(0.09),
                    Color.clear,
                    Color.purple.opacity(0.05)
                ],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        }
    }

    // MARK: Content

    private var contentArea: some View {
        ZStack {
            ForEach(OnboardingViewModel.Step.allCases) { step in
                if step == viewModel.currentStep {
                    StepScrollView(step: step, viewModel: viewModel, appState: appState)
                        .transition(stepTransition)
                        .id(step)
                }
            }
        }
        .animation(.spring(response: 0.38, dampingFraction: 0.85), value: viewModel.currentStep)
    }

    private var stepTransition: AnyTransition {
        .asymmetric(
            insertion: .move(edge: goingForward ? .trailing : .leading).combined(with: .opacity),
            removal: .move(edge: goingForward ? .leading : .trailing).combined(with: .opacity)
        )
    }

    // MARK: Footer

    private var footerBar: some View {
        HStack(spacing: 12) {
            leadingButton
            Spacer()
            progressDots
            Spacer()
            primaryButton
        }
        .padding(.horizontal, 32)
        .padding(.vertical, 18)
        .background(Color(nsColor: .windowBackgroundColor).opacity(0.85))
        .overlay(alignment: .top) { Divider() }
    }

    @ViewBuilder
    private var leadingButton: some View {
        if viewModel.currentStep == .welcome {
            Button("Skip for Now") {
                goingForward = true
                Task { _ = await viewModel.skip(using: appState) }
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .disabled(viewModel.isBusy)
        } else {
            Button("Back") {
                goingForward = false
                viewModel.goBack()
            }
            .disabled(!viewModel.canGoBack || viewModel.isBusy)
        }
    }

    private var primaryButton: some View {
        Button(viewModel.primaryButtonTitle) {
            goingForward = true
            Task { await handlePrimary() }
        }
        .atlasPrimaryButtonStyle()
        .keyboardShortcut(.defaultAction)
        .disabled(viewModel.isBusy)
    }

    @ViewBuilder
    private var progressDots: some View {
        if #available(macOS 26, *) {
            GlassEffectContainer(spacing: 6) {
                dotsRow(glass: true)
            }
        } else {
            dotsRow(glass: false)
        }
    }

    private func dotsRow(glass: Bool) -> some View {
        HStack(spacing: 6) {
            ForEach(OnboardingViewModel.Step.allCases) { step in
                stepDot(for: step, glass: glass)
            }
        }
    }

    @ViewBuilder
    private func stepDot(for step: OnboardingViewModel.Step, glass: Bool) -> some View {
        let isActive = step == viewModel.currentStep
        if glass, #available(macOS 26, *) {
            Color.clear
                .frame(width: isActive ? 22 : 8, height: 8)
                .glassEffect(isActive ? .regular.tint(.accentColor) : .regular, in: .rect(cornerRadius: 4))
                .animation(.spring(response: 0.35, dampingFraction: 0.75), value: viewModel.currentStep)
        } else {
            RoundedRectangle(cornerRadius: 4)
                .fill(isActive ? Color.accentColor : Color.secondary.opacity(0.28))
                .frame(width: isActive ? 22 : 8, height: 8)
                .animation(.spring(response: 0.35, dampingFraction: 0.75), value: viewModel.currentStep)
        }
    }

    // MARK: Primary action routing

    private func handlePrimary() async {
        switch viewModel.currentStep {
        case .aiProvider:
            await viewModel.saveAIProviderAndAdvance(using: appState)
        case .channels:
            await viewModel.saveChannelsAndAdvance(using: appState)
        case .skillKeys:
            await viewModel.saveSkillKeysAndAdvance(using: appState)
        case .ready:
            _ = await viewModel.complete(using: appState)
        default:
            viewModel.goForward()
        }
    }
}

// MARK: - Step scroll wrapper

private struct StepScrollView: View {
    let step: OnboardingViewModel.Step
    @ObservedObject var viewModel: OnboardingViewModel
    @ObservedObject var appState: AtlasAppState

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                stepHeader
                    .padding(.bottom, step == .welcome ? 28 : 24)
                stepBody
            }
            .padding(.horizontal, 48)
            .padding(.top, step == .welcome ? 48 : 38)
            .padding(.bottom, 28)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .scrollIndicators(.hidden)
    }

    // MARK: Header

    @ViewBuilder
    private var stepHeader: some View {
        if step == .welcome {
            welcomeHeader
        } else {
            compactHeader
        }
    }

    private var welcomeHeader: some View {
        VStack(alignment: .leading, spacing: 18) {
            Image(systemName: step.icon)
                .font(.system(size: 34, weight: .medium))
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(step.iconTint)
                .frame(width: 72, height: 72)
                .atlasIconBadge(tint: step.iconTint, cornerRadius: 18)

            VStack(alignment: .leading, spacing: 8) {
                Text(step.headline)
                    .font(.system(size: 30, weight: .bold))
                Text(step.subtitle)
                    .font(.title3)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var compactHeader: some View {
        HStack(alignment: .center, spacing: 16) {
            Image(systemName: step.icon)
                .font(.system(size: 22, weight: .medium))
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(step.iconTint)
                .frame(width: 52, height: 52)
                .atlasIconBadge(tint: step.iconTint, cornerRadius: 13)

            VStack(alignment: .leading, spacing: 4) {
                Text(step.headline)
                    .font(.title2.weight(.semibold))
                Text(step.subtitle)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    // MARK: Body routing

    @ViewBuilder
    private var stepBody: some View {
        switch step {
        case .welcome:        WelcomeBody()
        case .identity:       IdentityBody(viewModel: viewModel)
        case .aiProvider:     AIProviderBody(viewModel: viewModel, appState: appState)
        case .channels:       ChannelsBody(viewModel: viewModel)
        case .skillKeys:      SkillKeysBody(viewModel: viewModel)
        case .permissions:    PermissionsBody(viewModel: viewModel, appState: appState)
        case .network:        NetworkAccessBody(viewModel: viewModel, appState: appState)
        case .installService: InstallServiceBody(viewModel: viewModel, appState: appState)
        case .ready:          ReadyBody(viewModel: viewModel)
        }
    }
}

// MARK: - Welcome

private struct WelcomeBody: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text("Atlas runs silently in the background and helps you get things done — through chat, automation, and skills that connect to your apps and data.")
                .font(.body)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: 500)

            VStack(alignment: .leading, spacing: 12) {
                featureRow(icon: "brain", tint: .purple,
                           text: "Powered by your choice of AI model — conversations happen locally on your Mac")
                featureRow(icon: "bubble.left.and.bubble.right.fill", tint: .teal,
                           text: "Reachable from Telegram, Discord, and more — message Atlas from any device")
                featureRow(icon: "gearshape.2.fill", tint: .indigo,
                           text: "Runs as a background service — always available, minimal footprint")
            }
            .padding(.top, 4)
        }
    }

    private func featureRow(icon: String, tint: Color, text: String) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(tint)
                .frame(width: 20, alignment: .center)
                .padding(.top, 2)
            Text(text)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

// MARK: - Identity

private struct IdentityBody: View {
    @ObservedObject var viewModel: OnboardingViewModel

    var body: some View {
        VStack(alignment: .leading, spacing: 22) {
            VStack(alignment: .leading, spacing: 16) {
                inputField(label: "What should I call you?") {
                    TextField("Your name", text: $viewModel.userName)
                        .textFieldStyle(.roundedBorder)
                        .frame(maxWidth: 360)
                }

                inputField(label: "What would you like to call me?") {
                    TextField("Atlas", text: $viewModel.assistantName)
                        .textFieldStyle(.roundedBorder)
                        .frame(maxWidth: 360)
                }

                inputField(label: "Where are you based?") {
                    TextField("City, Region", text: $viewModel.location)
                        .textFieldStyle(.roundedBorder)
                        .frame(maxWidth: 360)

                    if !viewModel.location.isEmpty {
                        Text("Atlas will default to \(viewModel.derivedPreferences.temperatureUnit), \(viewModel.derivedPreferences.timeFormat), and \(viewModel.derivedPreferences.dateFormat).")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                }

                inputField(label: "About you (optional)") {
                    TextField("Your role, interests, how you like to work…",
                              text: $viewModel.aboutYou,
                              axis: .vertical)
                        .textFieldStyle(.roundedBorder)
                        .lineLimit(3, reservesSpace: true)
                        .frame(maxWidth: 480)
                }
            }

            Divider()

            VStack(alignment: .leading, spacing: 10) {
                Text("Action safety")
                    .font(.headline)
                Text("How should Atlas handle actions like creating calendar events, sending messages, or opening files?")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                Picker("", selection: $viewModel.actionSafetyMode) {
                    Text("Ask before every action").tag(AtlasActionSafetyMode.alwaysAskBeforeActions)
                    Text("Ask only for sensitive actions").tag(AtlasActionSafetyMode.askOnlyForRiskyActions)
                    Text("Run all actions automatically").tag(AtlasActionSafetyMode.moreAutonomous)
                }
                .pickerStyle(.radioGroup)
            }
        }
    }

    @ViewBuilder
    private func inputField<Content: View>(label: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(label).font(.subheadline).fontWeight(.medium)
            content()
        }
    }
}

// MARK: - AI Provider

private struct AIProviderBody: View {
    @ObservedObject var viewModel: OnboardingViewModel
    @ObservedObject var appState: AtlasAppState

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Active Provider")
                        .font(.headline)
                    Text("Choose the provider Atlas should use for conversations and internal tasks.")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Picker("", selection: $viewModel.selectedAIProvider) {
                    ForEach(AIProvider.allCases) { provider in
                        Text(provider.shortName).tag(provider)
                    }
                }
                .labelsHidden()
                .frame(width: 160)
            }

            providerCard(
                provider: .openAI,
                configured: viewModel.openAIKeyAlreadySet,
                status: appState.openAIStatusSummary,
                detail: appState.openAIValidationDetail
            ) {
                SecureField("Paste your OpenAI API key…", text: $viewModel.openAIKey)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 420)
            }

            providerCard(
                provider: .anthropic,
                configured: viewModel.anthropicKeyAlreadySet,
                status: appState.anthropicStatusSummary,
                detail: appState.anthropicValidationDetail
            ) {
                SecureField("Paste your Anthropic API key…", text: $viewModel.anthropicKey)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 420)
            }

            providerCard(
                provider: .gemini,
                configured: viewModel.geminiKeyAlreadySet,
                status: appState.geminiStatusSummary,
                detail: appState.geminiValidationDetail
            ) {
                SecureField("Paste your Gemini API key…", text: $viewModel.geminiKey)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 420)
            }

            providerCard(
                provider: .lmStudio,
                configured: viewModel.lmStudioReady,
                status: appState.lmStudioStatusSummary,
                detail: appState.lmStudioValidationDetail
            ) {
                TextField("http://localhost:1234", text: $viewModel.lmStudioBaseURL)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 420)
                Text("Atlas will validate that LM Studio is reachable before continuing.")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }

            if viewModel.isSavingKey {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Saving…").font(.subheadline).foregroundStyle(.secondary)
                }
            }

            if let error = viewModel.keyError {
                Text(error).font(.caption).foregroundStyle(.red)
            }

            Text("You can add or change this later in Settings → Setup.")
                .font(.caption).foregroundStyle(.tertiary)
        }
    }

    @ViewBuilder
    private func providerCard<Content: View>(
        provider: AIProvider,
        configured: Bool,
        status: String,
        detail: String?,
        @ViewBuilder content: () -> Content
    ) -> some View {
        let isSelected = viewModel.selectedAIProvider == provider

        VStack(alignment: .leading, spacing: 12) {
            HStack {
                VStack(alignment: .leading, spacing: 3) {
                    Text(provider.displayName)
                        .font(.headline)
                    Text(status)
                        .font(.subheadline)
                        .foregroundStyle(configured ? .green : .secondary)
                }
                Spacer()
                if configured {
                    Label("Configured", systemImage: "checkmark.circle.fill")
                        .font(.subheadline)
                        .foregroundStyle(.green)
                }
            }

            if let detail {
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if isSelected {
                content()
            }
        }
        .padding(14)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .overlay {
            RoundedRectangle(cornerRadius: 10)
                .stroke(isSelected ? Color.accentColor.opacity(0.45) : Color.clear, lineWidth: 1)
        }
    }
}

// MARK: - Channels

private struct ChannelsBody: View {
    @ObservedObject var viewModel: OnboardingViewModel

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            channelCard(
                name: "Telegram",
                icon: "paperplane.fill",
                tint: .teal,
                description: "Message Atlas from any device using a Telegram bot.",
                setupHint: "Create a bot via @BotFather and paste the token below.",
                placeholder: "Bot token (from @BotFather)",
                isEnabled: $viewModel.telegramEnabled,
                alreadySet: viewModel.telegramTokenAlreadySet,
                token: $viewModel.telegramToken
            )

            channelCard(
                name: "Discord",
                icon: "bubble.fill",
                tint: Color(red: 0.35, green: 0.4, blue: 0.9),
                description: "Chat with Atlas in your Discord server via a bot.",
                setupHint: "Create a bot at discord.com/developers and paste the token below.",
                placeholder: "Bot token",
                isEnabled: $viewModel.discordEnabled,
                alreadySet: viewModel.discordTokenAlreadySet,
                token: $viewModel.discordToken
            )

            if viewModel.isSavingKey {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Saving…").font(.subheadline).foregroundStyle(.secondary)
                }
                .padding(.top, 4)
            }

            if let error = viewModel.keyError {
                Text(error).font(.caption).foregroundStyle(.red)
            }

            Text("More channels (Slack, etc.) can be configured in Settings → Communications.")
                .font(.caption).foregroundStyle(.tertiary)
                .padding(.top, 2)
        }
    }

    private func channelCard(
        name: String,
        icon: String,
        tint: Color,
        description: String,
        setupHint: String,
        placeholder: String,
        isEnabled: Binding<Bool>,
        alreadySet: Bool,
        token: Binding<String>
    ) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header row
            HStack(spacing: 14) {
                Image(systemName: icon)
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(tint)
                    .frame(width: 34, height: 34)
                    .background(tint.opacity(0.12))
                    .clipShape(RoundedRectangle(cornerRadius: 8))

                VStack(alignment: .leading, spacing: 2) {
                    Text(name).font(.headline)
                    Text(description)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Spacer()

                if alreadySet {
                    Label("Connected", systemImage: "checkmark.circle.fill")
                        .font(.subheadline)
                        .foregroundStyle(.green)
                } else {
                    Toggle("", isOn: isEnabled)
                        .labelsHidden()
                        .toggleStyle(.switch)
                }
            }
            .padding(14)

            // Expandable credential section
            if isEnabled.wrappedValue && !alreadySet {
                Divider().padding(.horizontal, 14)

                VStack(alignment: .leading, spacing: 8) {
                    Text(setupHint)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    SecureField(placeholder, text: token)
                        .textFieldStyle(.roundedBorder)
                }
                .padding(14)
                .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .animation(.spring(response: 0.3, dampingFraction: 0.82), value: isEnabled.wrappedValue)
    }
}

// MARK: - Skill Keys

private struct SkillKeysBody: View {
    @ObservedObject var viewModel: OnboardingViewModel

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            skillKeyCard(
                name: "Web Search",
                icon: "magnifyingglass",
                tint: .orange,
                description: "Powers the web.search skill with real-time results.",
                hint: "Get a free key at brave.com/search/api",
                placeholder: "Brave Search API key",
                alreadySet: viewModel.braveSearchKeyAlreadySet,
                alreadySetLabel: "Brave Search key configured.",
                token: $viewModel.braveSearchKey
            )

            if viewModel.isSavingKey {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Saving…").font(.subheadline).foregroundStyle(.secondary)
                }
            }

            if let error = viewModel.keyError {
                Text(error).font(.caption).foregroundStyle(.red)
            }

            Text("More skill integrations (finance, image generation, etc.) can be configured in Settings → Setup.")
                .font(.caption).foregroundStyle(.tertiary)
        }
    }

    private func skillKeyCard(
        name: String,
        icon: String,
        tint: Color,
        description: String,
        hint: String,
        placeholder: String,
        alreadySet: Bool,
        alreadySetLabel: String,
        token: Binding<String>
    ) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                Image(systemName: icon)
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(tint)
                    .frame(width: 32, height: 32)
                    .background(tint.opacity(0.12))
                    .clipShape(RoundedRectangle(cornerRadius: 7))

                VStack(alignment: .leading, spacing: 2) {
                    Text(name).font(.headline)
                    Text(description)
                        .font(.caption).foregroundStyle(.secondary)
                }
            }

            if alreadySet {
                HStack(spacing: 8) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    Text(alreadySetLabel).font(.subheadline).foregroundStyle(.secondary)
                }
            }

            SecureField(placeholder, text: token)
                .textFieldStyle(.roundedBorder)
                .frame(maxWidth: 400)

            Text(hint).font(.caption).foregroundStyle(.tertiary)
        }
        .padding(14)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }
}

// MARK: - Permissions

// MARK: - Network Access

private struct NetworkAccessBody: View {
    @ObservedObject var viewModel: OnboardingViewModel
    @ObservedObject var appState: AtlasAppState

    @State private var remoteEnabled = false

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            // LAN option
            optionRow(
                icon: "wifi",
                tint: .green,
                title: "Local Network (LAN)",
                description: "Access Atlas from any device on your home or office network. An API key is generated automatically.",
                badge: nil,
                isEnabled: true
            ) {
                Toggle("Enable LAN access", isOn: $remoteEnabled)
                    .labelsHidden()
                    .onChange(of: remoteEnabled) { _, newValue in
                        Task { await appState.setRemoteAccess(newValue) }
                    }
            }

            Divider()

            // Tailscale placeholder
            optionRow(
                icon: "lock.shield",
                tint: .secondary,
                title: "Tailscale",
                description: "Secure, zero-config remote access from anywhere — over your personal Tailscale network.",
                badge: "Coming soon",
                isEnabled: false
            ) {
                EmptyView()
            }

            Text("You can change these settings at any time in Settings → Network.")
                .font(.caption)
                .foregroundStyle(.tertiary)
        }
        .onAppear { remoteEnabled = appState.remoteAccessEnabled }
    }

    private func optionRow<Control: View>(
        icon: String,
        tint: Color,
        title: String,
        description: String,
        badge: String?,
        isEnabled: Bool,
        @ViewBuilder control: () -> Control
    ) -> some View {
        HStack(alignment: .top, spacing: 14) {
            Image(systemName: icon)
                .font(.system(size: 16, weight: .medium))
                .foregroundStyle(isEnabled ? tint : .secondary)
                .frame(width: 36, height: 36)
                .background((isEnabled ? tint : Color.secondary).opacity(0.12))
                .clipShape(RoundedRectangle(cornerRadius: 9))

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(title).font(.body.weight(.medium))
                        .foregroundStyle(isEnabled ? .primary : .secondary)
                    if let badge {
                        Text(badge)
                            .font(.caption.weight(.medium))
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 7)
                            .padding(.vertical, 2)
                            .background(.secondary.opacity(0.12))
                            .clipShape(Capsule())
                    }
                }
                Text(description)
                    .font(.caption)
                    .foregroundStyle(isEnabled ? .secondary : .tertiary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Spacer(minLength: 8)
            control().disabled(!isEnabled)
        }
    }
}

// MARK: - Permissions

private struct PermissionsBody: View {
    @ObservedObject var viewModel: OnboardingViewModel
    @ObservedObject var appState: AtlasAppState

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            // File access
            permissionGroup(
                title: "File Access",
                description: "Let Atlas read and manage files in these folders."
            ) {
                folderRow(name: "Desktop", icon: "desktopcomputer",
                          granted: viewModel.desktopGranted) {
                    Task { await viewModel.grantFolder("Desktop", using: appState) }
                }
                folderRow(name: "Documents", icon: "doc.fill",
                          granted: viewModel.documentsGranted) {
                    Task { await viewModel.grantFolder("Documents", using: appState) }
                }
                folderRow(name: "Downloads", icon: "arrow.down.circle.fill",
                          granted: viewModel.downloadsGranted) {
                    Task { await viewModel.grantFolder("Downloads", using: appState) }
                }
            }

            Divider()

            // System permissions
            permissionGroup(
                title: "App Access",
                description: "Required by the Calendar, Reminders, and Contacts skills."
            ) {
                systemRow(name: "Calendar", icon: "calendar",
                          status: viewModel.calendarStatus) {
                    Task { await viewModel.requestCalendarPermission() }
                }
                systemRow(name: "Reminders", icon: "checklist",
                          status: viewModel.remindersStatus) {
                    Task { await viewModel.requestRemindersPermission() }
                }
                systemRow(name: "Contacts", icon: "person.2.fill",
                          status: viewModel.contactsStatus) {
                    Task { await viewModel.requestContactsPermission() }
                }
            }

            Text("All permissions are optional and can be changed later in Settings → Setup.")
                .font(.caption).foregroundStyle(.tertiary)
        }
    }

    private func permissionGroup<Content: View>(
        title: String,
        description: String,
        @ViewBuilder rows: () -> Content
    ) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.headline)
                Text(description).font(.subheadline).foregroundStyle(.secondary)
            }
            rows()
        }
    }

    private func folderRow(name: String, icon: String, granted: Bool, action: @escaping () -> Void) -> some View {
        HStack(spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 13))
                .foregroundStyle(granted ? .green : .secondary)
                .frame(width: 20)
            Text(name).font(.subheadline)
            Spacer()
            if granted {
                Label("Granted", systemImage: "checkmark.circle.fill")
                    .font(.subheadline).foregroundStyle(.green)
            } else {
                Button("Grant Access", action: action)
                    .buttonStyle(.bordered).controlSize(.small)
            }
        }
        .padding(.vertical, 3)
    }

    @ViewBuilder
    private func systemRow(
        name: String,
        icon: String,
        status: OnboardingViewModel.PermissionStatus,
        action: @escaping () -> Void
    ) -> some View {
        HStack(spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 13))
                .foregroundStyle(statusColor(status))
                .frame(width: 20)
            Text(name).font(.subheadline)
            Spacer()
            statusControl(status: status, action: action)
        }
        .padding(.vertical, 3)
    }

    private func statusColor(_ status: OnboardingViewModel.PermissionStatus) -> Color {
        switch status {
        case .granted:       return .green
        case .denied:        return .red
        case .notDetermined: return .secondary
        }
    }

    @ViewBuilder
    private func statusControl(status: OnboardingViewModel.PermissionStatus, action: @escaping () -> Void) -> some View {
        switch status {
        case .granted:
            Label("Granted", systemImage: "checkmark.circle.fill")
                .font(.subheadline).foregroundStyle(.green)
        case .denied:
            Button("Open System Settings", action: openPrivacySettings)
                .buttonStyle(.bordered)
                .controlSize(.small)
        case .notDetermined:
            Button("Allow Access", action: action)
                .buttonStyle(.bordered).controlSize(.small)
        }
    }

    private func openPrivacySettings() {
        guard let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy") else {
            return
        }
        NSWorkspace.shared.open(url)
    }
}

// MARK: - Install Service

private struct InstallServiceBody: View {
    @ObservedObject var viewModel: OnboardingViewModel
    @ObservedObject var appState: AtlasAppState

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            VStack(alignment: .leading, spacing: 12) {
                infoRow(icon: "clock.fill", text: "Starts automatically at login via launchd")
                infoRow(icon: "wifi", text: "Runs on localhost — your data never leaves your Mac")
                infoRow(icon: "memorychip", text: "Uses minimal resources when idle")
            }

            if viewModel.daemonInstalled {
                HStack(spacing: 10) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    Text("Background service is running.")
                        .font(.subheadline).foregroundStyle(.secondary)
                }
            } else if viewModel.isInstallingDaemon {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Installing…").font(.subheadline).foregroundStyle(.secondary)
                }
            } else {
                Button {
                    Task { await viewModel.installDaemon(using: appState) }
                } label: {
                    Label("Install Service", systemImage: "arrow.down.circle")
                }
                .buttonStyle(.borderedProminent)
            }

            if let error = viewModel.installError {
                Text(error).font(.caption).foregroundStyle(.red)
            }

            Text("You can skip this and install the service later from the menu bar.")
                .font(.caption).foregroundStyle(.tertiary)
        }
    }

    private func infoRow(icon: String, text: String) -> some View {
        HStack(spacing: 10) {
            Image(systemName: icon)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .frame(width: 18)
            Text(text).font(.subheadline).foregroundStyle(.secondary)
        }
    }
}

// MARK: - Ready

private struct ReadyBody: View {
    @ObservedObject var viewModel: OnboardingViewModel

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 10) {
                summaryCard(label: "Your name",
                            value: viewModel.userName.isEmpty ? "Not set" : viewModel.userName)
                summaryCard(label: "Atlas name",
                            value: viewModel.assistantName)
                summaryCard(label: "Location",
                            value: viewModel.location.isEmpty ? "Not set" : viewModel.location)
                summaryCard(label: "AI provider",
                            value: (viewModel.openAIKeyAlreadySet || !viewModel.openAIKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty) ? "Configured ✓" : "Skipped")
                summaryCard(label: "Telegram",
                            value: (viewModel.telegramTokenAlreadySet || viewModel.telegramEnabled) ? "Enabled ✓" : "Skipped")
                summaryCard(label: "Discord",
                            value: (viewModel.discordTokenAlreadySet || viewModel.discordEnabled) ? "Enabled ✓" : "Skipped")
                summaryCard(label: "Background service",
                            value: viewModel.daemonInstalled ? "Installed ✓" : "Not installed")
                summaryCard(label: "Web search",
                            value: (viewModel.braveSearchKeyAlreadySet || !viewModel.braveSearchKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty) ? "Configured ✓" : "Skipped")
            }
            .frame(maxWidth: 560)

            if !viewModel.location.isEmpty {
                Text("Based on your location: \(viewModel.derivedPreferences.temperatureUnit) · \(viewModel.derivedPreferences.timeFormat) · \(viewModel.derivedPreferences.dateFormat)")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func summaryCard(label: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label)
                .font(.caption)
                .foregroundStyle(.tertiary)
            Text(value)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(value.hasSuffix("✓") ? Color.green : Color.primary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }
}
