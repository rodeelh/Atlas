import SwiftUI

struct SettingsGlyph: View {
    let systemImage: String
    let color: Color

    var body: some View {
        Image(systemName: systemImage)
            .font(.system(size: 28, weight: .medium))
            .symbolRenderingMode(.hierarchical)
            .foregroundStyle(color)
            .frame(width: 56, height: 56)
    }
}

struct SettingsPageHeader: View {
    let title: String
    let message: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.largeTitle.weight(.semibold))
            if let message, !message.isEmpty {
                Text(message)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.top, 14)
        .padding(.bottom, 16)
    }
}

struct SettingsListRowLabel: View {
    let systemImage: String
    let color: Color
    let title: String
    let subtitle: String?

    init(systemImage: String, color: Color, title: String, subtitle: String? = nil) {
        self.systemImage = systemImage
        self.color = color
        self.title = title
        self.subtitle = subtitle
    }

    var body: some View {
        HStack(spacing: 12) {
            SettingsGlyph(systemImage: systemImage, color: color)

            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.body)
                    .foregroundStyle(.primary)

                if let subtitle, !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
            }
        }
    }
}

struct SettingsTrailingValue: View {
    let value: String

    var body: some View {
        Text(value)
            .font(.body)
            .foregroundStyle(.secondary)
            .multilineTextAlignment(.trailing)
            .lineLimit(2)
            .textSelection(.enabled)
    }
}

struct SettingsActionRow<Control: View>: View {
    let title: String
    let subtitle: String?
    @ViewBuilder let control: Control

    init(
        title: String,
        subtitle: String? = nil,
        @ViewBuilder control: () -> Control
    ) {
        self.title = title
        self.subtitle = subtitle
        self.control = control()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .center, spacing: 16) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .font(.body)
                        .foregroundStyle(.primary)

                    if let subtitle, !subtitle.isEmpty {
                        Text(subtitle)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }

                Spacer(minLength: 16)

                control
            }
        }
        .padding(.vertical, 2)
        .atlasSettingsRowInsets()
    }
}

private struct AtlasSettingsRowInsets: ViewModifier {
    let top: CGFloat
    let bottom: CGFloat
    let leading: CGFloat
    let trailing: CGFloat

    func body(content: Content) -> some View {
        content.listRowInsets(
            EdgeInsets(top: top, leading: leading, bottom: bottom, trailing: trailing)
        )
    }
}

private struct AtlasSettingsPageChrome: ViewModifier {
    let maxWidth: CGFloat

    func body(content: Content) -> some View {
        content
            .frame(maxWidth: maxWidth)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .padding(.horizontal, 32)
            .padding(.top, 16)
            .padding(.bottom, 32)
            .background(Color(nsColor: .windowBackgroundColor))
    }
}

extension View {
    func atlasSettingsPageChrome(maxWidth: CGFloat = 800) -> some View {
        modifier(AtlasSettingsPageChrome(maxWidth: maxWidth))
    }

    func atlasSettingsRowInsets(
        top: CGFloat = 10,
        bottom: CGFloat = 10,
        leading: CGFloat = 20,
        trailing: CGFloat = 20
    ) -> some View {
        modifier(
            AtlasSettingsRowInsets(
                top: top,
                bottom: bottom,
                leading: leading,
                trailing: trailing
            )
        )
    }

    func atlasIconBadge(tint: Color, cornerRadius: CGFloat) -> some View {
        modifier(AtlasSolidIconBadge(cornerRadius: cornerRadius))
    }

    @ViewBuilder
    func atlasPrimaryButtonStyle() -> some View {
        if #available(macOS 26, *) {
            self.buttonStyle(.glassProminent)
        } else {
            self.buttonStyle(.borderedProminent)
        }
    }
}

private struct AtlasSolidIconBadge: ViewModifier {
    let cornerRadius: CGFloat
    @Environment(\.colorScheme) private var colorScheme

    func body(content: Content) -> some View {
        content.background(
            RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                .fill(colorScheme == .dark ? Color.black : Color.white)
        )
    }
}

// MARK: - Settings panel background

struct SettingsGlassSurface: View {
    var body: some View {
        Group {
            if #available(macOS 26, *) {
                Color.clear
                    .glassEffect(.regular.tint(.white.opacity(0.03)), in: .rect(cornerRadius: 0))
            } else {
                Color(nsColor: .windowBackgroundColor)
            }
        }
    }
}

struct SettingsPanelBackground: View {
    var body: some View {
        if #available(macOS 26, *) {
            SettingsGlassSurface()
                .ignoresSafeArea()
        } else {
            ZStack {
                Color(nsColor: .windowBackgroundColor)
                LinearGradient(
                    colors: [
                        Color.accentColor.opacity(0.06),
                        Color.clear,
                        Color.purple.opacity(0.03)
                    ],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
            }
            .ignoresSafeArea()
        }
    }
}

// MARK: - Settings panel header

struct SettingsPanelHeader: View {
    let icon: String
    let tint: Color
    let title: String
    let subtitle: String

    var body: some View {
        HStack(alignment: .center, spacing: 16) {
            SettingsGlyph(systemImage: icon, color: tint)

            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(.title3.weight(.semibold))
                Text(subtitle)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}

// MARK: - Settings pane layout
// Fixed header (icon + title + subtitle) pinned above a Divider, then a
// ScrollView for content below. Header never scrolls.

struct SettingsPaneLayout<Content: View>: View {
    let icon: String
    let tint: Color
    let title: String
    let subtitle: String
    @ViewBuilder let content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            SettingsPanelHeader(icon: icon, tint: tint, title: title, subtitle: subtitle)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 48)
                .padding(.top, 18)
                .padding(.bottom, 14)
                .background(SettingsGlassSurface().ignoresSafeArea(.all, edges: [.top, .leading]))

            Divider()

            // Scrollable content
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    content()
                }
                .padding(.horizontal, 48)
                .padding(.top, 20)
                .padding(.bottom, 32)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .scrollIndicators(.hidden)
            .frame(minHeight: 0, maxHeight: .infinity)
        }
        .frame(minWidth: 620, maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(SettingsPanelBackground())
    }
}

// MARK: - Settings card

struct SettingsCard<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        Group {
            if #available(macOS 26, *) {
                content
                    .padding(14)
                    .glassEffect(.regular, in: .rect(cornerRadius: 10))
            } else {
                content
                    .padding(14)
                    .background(Color(nsColor: .controlBackgroundColor))
                    .clipShape(RoundedRectangle(cornerRadius: 10))
            }
        }
    }
}

// MARK: - Settings card row components

struct SettingsLabeledRow: View {
    let label: String
    let value: String?
    var valueColor: Color

    init(_ label: String, value: String? = nil, valueColor: Color = .secondary) {
        self.label = label
        self.value = value
        self.valueColor = valueColor
    }

    var body: some View {
        HStack(alignment: .center) {
            Text(label)
                .font(.body)
                .foregroundStyle(.primary)
            Spacer(minLength: 16)
            if let value {
                Text(value)
                    .font(.body)
                    .foregroundStyle(valueColor)
                    .multilineTextAlignment(.trailing)
                    .lineLimit(2)
            }
        }
    }
}

struct SettingsCardSectionHeader: View {
    let title: String

    var body: some View {
        Text(title.uppercased())
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            .kerning(0.3)
            .padding(.bottom, 4)
    }
}
