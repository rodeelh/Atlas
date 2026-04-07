import SwiftUI

struct CredentialEntrySheet: View {
    let kind: AtlasCredentialKind
    let isSaving: Bool
    let onSave: (String) -> Void
    let onCancel: () -> Void

    @State private var secret = ""
    @FocusState private var isSecretFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VStack(alignment: .leading, spacing: 6) {
                Text(kind.updateActionTitle)
                    .font(.title3.weight(.semibold))

                Text("Enter a new \(kind.fieldTitle.lowercased()). Atlas stores it securely in your macOS Keychain.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack(alignment: .center, spacing: 18) {
                Text(kind.fieldTitle)
                    .font(.body.weight(.medium))
                    .frame(width: 82, alignment: .leading)
                SecureField("Stored in Keychain", text: $secret)
                    .textFieldStyle(.roundedBorder)
                    .focused($isSecretFocused)
                    .frame(width: 320)
                    .onSubmit(save)

                Spacer(minLength: 0)
            }

            Divider()

            HStack {
                Spacer()

                Button("Cancel", role: .cancel, action: onCancel)
                    .keyboardShortcut(.cancelAction)

                Button(isSaving ? "Saving…" : "Save", action: save)
                    .keyboardShortcut(.defaultAction)
                    .disabled(isSaving || secret.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(24)
        .frame(width: 520)
        .onAppear {
            DispatchQueue.main.async {
                isSecretFocused = true
            }
        }
    }

    private func save() {
        onSave(secret)
    }
}
