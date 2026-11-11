import SwiftUI

/// An install, upgrade or removal on the extension's market page (#3307): what is going on, the hub's steps with the
/// one it is at (a bar while it downloads, when the hub knows the size), and for one that failed the step it stopped
/// at, why, and Retry. While it waits for the user's yes: Review Permissions opens the preview again, Cancel drops it. An older hub
/// has no steps: the section says what it is doing and for how long, so the page never looks stuck.
struct ExtensionOperationSection: View {
    var operation: ExtensionOperation
    var name: String
    /// A pack partly uninstalled (#4072): what that means and each group of members, said instead of the hub's sentence.
    var partial: [String]? = nil
    var retry: () -> Void
    var review: () -> Void
    var dismiss: () -> Void
    @Environment(\.dynamicTypeSize) private var typeSize

    private var failed: Bool { operation.state == .failed }
    private var waiting: Bool { operation.state == .needsConsent }
    /// Still downloading or checking (the consent not reached): the hub can stop it.
    private var cancelable: Bool {
        operation.isRunning && !operation.isLocal && operation.steps.contains { $0.id == "consent" && $0.state == .pending }
    }

    var body: some View {
        Section {
            headline
            // What to do (why it stopped and Retry, or the permissions to agree to) right under the title, before the
            // steps: eight of them push it off the screen.
            if failed { failure }
            if waiting { consent }
            if operation.steps.isEmpty {
                if operation.isRunning {
                    Text(ExtensionOperationText.waitingNote(operation.kind))
                        .font(Theme.Font.label).foregroundStyle(.secondary)
                        .accessibilityIdentifier("extension-operation-waiting")
                }
            } else {
                ForEach(operation.shownSteps) {
                    ExtensionOperationStepRow(step: $0, kind: operation.kind, current: $0.id == operation.current?.id)
                }
            }
            if cancelable {
                Button(action: dismiss) {
                    Text("Cancel").lineLimit(1).formActionWidth()
                }
                .themedButton()
                .padding(.vertical, Theme.Space.xs)
                .accessibilityIdentifier("extension-operation-cancel")
            }
        } header: {
            SettingsHeading(failed ? String(localized: "Didn’t Finish") : waiting ? String(localized: "Waiting for You") : String(localized: "Active"))
        }
    }

    private var headline: some View {
        HStack(spacing: Theme.Space.m) {
            Group {
                if failed {
                    Image(systemName: Theme.Icon.errorFilled).resizable().scaledToFit().foregroundStyle(Theme.danger)
                } else if waiting {
                    Image(systemName: Theme.Icon.infoFilled).resizable().scaledToFit().foregroundStyle(Theme.caution)
                } else {
                    ProgressView().controlSize(.small)
                }
            }
            .scaledFrame(width: Theme.Size.iconS, height: Theme.Size.iconS)
            // At the accessibility sizes the words wrap and the time goes under them: on one line the title was
            // cut to its first word (#4058).
            let large = typeSize.isAccessibilitySize
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(ExtensionOperationText.title(operation) + " " + name).font(Theme.Font.bodyStrong).lineLimit(large ? nil : 1)
                let line = [ExtensionOperationText.versions(operation), ExtensionOperationText.progress(operation),
                            ExtensionOperationText.actor(operation)].compactMap { $0 }
                if !line.isEmpty {
                    Text(line.joined(separator: " · ")).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(large ? nil : 1)
                }
                if large { elapsed }
            }
            Spacer(minLength: Theme.Space.s)
            if !large { elapsed }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("extension-operation")
        .accessibilityValue(operation.state.rawValue)
    }

    /// How long it has been going, while it goes.
    @ViewBuilder private var elapsed: some View {
        if operation.isRunning, let start = operation.startedAt {
            TimelineView(.periodic(from: start, by: 1)) { context in
                Text(ExtensionOperationText.elapsed(since: start, now: context.date))
                    .font(Theme.Font.label).foregroundStyle(.secondary).monospacedDigit().lineLimit(1)
            }
        }
    }

    @ViewBuilder private var failure: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            if let partial {
                Text(ExtensionPartialRemoval.title).font(Theme.Font.labelStrong).foregroundStyle(Theme.cautionText)
                // Each line is a different sentence (what it means, then one per group).
                ForEach(partial, id: \.self) { line in
                    Text(line).font(Theme.Font.label).textSelection(.enabled)
                }
            } else if let step = operation.failedStep {
                Text("Stopped at “\(ExtensionOperationText.step(step.id, kind: operation.kind))”")
                    .font(Theme.Font.labelStrong).foregroundStyle(Theme.danger)
            }
            if partial == nil, let e = operation.error, !e.isEmpty {
                Text(e).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("extension-operation-error")
        buttons(primary: String(localized: "Retry [Components b8784c]"), id: "extension-operation-retry", action: retry,
                secondary: String(localized: "Close"), secondaryID: "extension-operation-dismiss")
    }

    @ViewBuilder private var consent: some View {
        Text("Downloaded and checked. Review the permissions it asks for; installing goes on once you agree.")
            .font(Theme.Font.label).foregroundStyle(.secondary)
        buttons(primary: String(localized: "Review Permissions"), id: "extension-operation-review", action: review,
                secondary: String(localized: "Cancel"), secondaryID: "extension-operation-cancel")
    }

    /// Two buttons in the card (a FormAction's clear row would cut the card in two).
    private func buttons(primary: String, id: String, action: @escaping () -> Void,
                         secondary: String, secondaryID: String) -> some View {
        HStack(spacing: Theme.Space.s) {
            Button(action: action) {
                Text(primary).lineLimit(1).formActionWidth()
            }
            .themedButton(prominent: true)
            .accessibilityIdentifier(id)
            Button(action: dismiss) {
                Text(secondary).lineLimit(1).formActionWidth()
            }
            .themedButton()
            .accessibilityIdentifier(secondaryID)
        }
        .padding(.vertical, Theme.Space.xs)
    }
}

/// One step: done, going (with its bar), waiting or where it failed.
private struct ExtensionOperationStepRow: View {
    var step: ExtensionOperationStep
    var kind: ExtensionOperation.Kind
    var current: Bool
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.m) {
            mark.scaledFrame(width: Theme.Size.markM, height: Theme.Size.markM)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(ExtensionOperationText.step(step.id, kind: kind))
                    .font(current ? Theme.Font.secondaryMedium : Theme.Font.secondary)
                    .foregroundStyle(step.state == .pending ? Color.secondary : Color.primary)
                    .lineLimit(typeSize.isAccessibilitySize ? nil : 1)
                if step.state == .running, step.id == "download" || (step.id == "tools" && step.bytes != nil) {
                    // A size the hub does not know: a bar that cannot say how far.
                    Group {
                        if let f = step.fraction { ProgressView(value: f) } else { ProgressView() }
                    }
                    .progressViewStyle(.linear)
                    .tint(Theme.accent)
                    .accessibilityLabel(ExtensionOperationText.step(step.id, kind: kind))
                }
                if let d = ExtensionOperationText.detail(step) {
                    Text(d).font(Theme.Font.label).foregroundStyle(.secondary).monospacedDigit().lineLimit(2)
                }
                // The tools step's programs (#4507), a line each: which one downloads, which use the machine's own.
                ForEach(step.tools ?? []) { tool in
                    ExtensionOperationToolLine(tool: tool)
                }
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("extension-operation-step-\(step.id)")
        .accessibilityValue(step.state.rawValue)
    }

    private var mark: some View { ExtensionOperationMark(state: step.state) }
}

/// One program of the tools step: its state's mark, its name and what it is doing.
private struct ExtensionOperationToolLine: View {
    var tool: ExtensionOperationStep.Tool

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            ExtensionOperationMark(state: tool.state)
                .scaledFrame(width: Theme.Size.markS, height: Theme.Size.markS)
            Text(tool.name).font(Theme.Font.code).lineLimit(1)
            if let note = ExtensionOperationText.tool(tool) {
                Text(note).font(Theme.Font.label).foregroundStyle(tool.state == .failed ? Theme.danger : Color.secondary).lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .accessibilityIdentifier("extension-operation-tool-\(tool.name)")
    }
}

/// A step's (or a program's) mark: done, going, failed or waiting.
private struct ExtensionOperationMark: View {
    var state: ExtensionOperationStep.State

    var body: some View {
        switch state {
        case .done:
            Image(systemName: Theme.Icon.successFilled).resizable().scaledToFit().foregroundStyle(Theme.positive)
        case .running:
            ProgressView().controlSize(.mini)
        case .failed:
            Image(systemName: Theme.Icon.errorFilled).resizable().scaledToFit().foregroundStyle(Theme.danger)
        case .pending, .skipped:
            Image(systemName: Theme.Icon.unchecked).resizable().scaledToFit().foregroundStyle(.tertiary)
        }
    }
}
