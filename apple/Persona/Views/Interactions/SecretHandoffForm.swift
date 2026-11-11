import SwiftUI

/// The hand-off card (`SecretHandoff`): which variables an Agent gets for one run, from which item, on which computer,
/// where it runs and what it is asked, never a reference or a value. Deny and Allow, and Always Allow under them when the
/// card offers it, with what it covers. Only these buttons approve it (`AppModel.resolve(_:_:handoffCard:)` says so on
/// the request); every quick path leaves it alone (`Interaction.requiresInput`), and no key approves it.
/// The same card on the iPhone, iPad, Mac and Vision Pro (none of them approves it unless its app sends the feature).
struct SecretHandoffForm: View {
    @Environment(AppModel.self) private var model
    var interaction: Interaction
    @State private var sending = false

    var body: some View {
        let request = SecretHandoffRequest(interaction.toolInput)
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text(lead(request))
                .font(Theme.Font.secondary)
                .fixedSize(horizontal: false, vertical: true)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                ForEach(request.variables) { v in
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(verbatim: v.name).font(Theme.Font.code)
                        Text(verbatim: v.item)
                            .font(Theme.Font.caption)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("secret-handoff.variable")
                }
            }
            if !request.cwd.isEmpty || !request.prompt.isEmpty {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    if !request.cwd.isEmpty {
                        Text(verbatim: request.cwd).font(Theme.Font.code).lineLimit(1).truncationMode(.middle)
                    }
                    if !request.prompt.isEmpty {
                        Text(verbatim: request.prompt).font(Theme.Font.caption).lineLimit(2)
                    }
                }
                .foregroundStyle(.secondary)
            }
            Text("Persona and the assistant never see the values.")
                .font(Theme.Font.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                Button { submit(Resolution(allow: false)) } label: {
                    Text("Deny").lineLimit(1).minimumScaleFactor(0.8).frame(maxWidth: .infinity)
                }
                .themedButton()
                Button { submit(Resolution(allow: true)) } label: {
                    Text("Allow [Notification Actions ce7ef2]").lineLimit(1).minimumScaleFactor(0.8).frame(maxWidth: .infinity)
                }
                .themedButton(prominent: true)
                .disabled(request.variables.isEmpty)
                .accessibilityIdentifier("secret-handoff.allow")
            }
            .controlSize(.large)
            .disabled(sending)
            if let scope = interaction.alwaysLabel, !scope.isEmpty {
                Button("Always Allow") { submit(Resolution(allow: true, always: true)) }
                    #if os(macOS)
                    .buttonStyle(.link)
                    #endif
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
                    .font(Theme.Font.caption)
                    .frame(maxWidth: .infinity)
                    .disabled(sending || request.variables.isEmpty)
                    .accessibilityIdentifier("secret-handoff.always")
                Text("Always Allow \(scope).")
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        // The panel's scroll area clips at the form's edge: the glass buttons' rims, drawn just outside them, stay inside.
        .padding(.horizontal, Theme.Space.xxs)
        .padding(.bottom, Theme.Space.xxs)
    }

    /// Who gets the values, where, and for how long.
    private func lead(_ r: SecretHandoffRequest) -> String {
        switch (r.agent.isEmpty, r.machine.isEmpty) {
        case (false, false): String(localized: "“\(r.agent)” on \(r.machine) gets these for this run only:")
        case (true, false): String(localized: "An Agent on \(r.machine) gets these for this run only:")
        default: String(localized: "An Agent gets these for this run only:")
        }
    }

    private func submit(_ resolution: Resolution) {
        guard !sending else { return }
        sending = true
        Task {
            await model.resolve(interaction, resolution, handoffCard: true)
            sending = false
        }
    }
}
