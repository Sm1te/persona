#if os(iOS)
import SwiftUI

/// Forward To (#3807, docs/apple/ios.md "Forwarding a message"): Share as Image at the top, then the message, Persona and
/// New Agent, the recent targets, and each computer's Agents, with search. A tap reads the whole message when the chat
/// holds it cut, then leaves the rest to the chat (`ForwardOutcome`): the target's chat opens with the forward over its
/// box. The lists come from the sessions the app already has, read once as the sheet opens.
struct ForwardSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let draft: ForwardDraft
    /// Called before the sheet goes; the chat acts on it once it has gone.
    var picked: (ForwardOutcome) -> Void
    @State private var query = ""
    @State private var sessions: [Session] = []
    @State private var busy: ForwardTarget?
    @State private var error: String?
    /// The recent targets, read once when the sheet opens.
    @State private var recents: [SessionKey] = []
    /// The lists, made when the sheet opens and again as the search changes, never in `body`.
    @State private var sections: [ForwardTargets.Section] = []

    private var from: SessionKey { draft.source.key }
    private var main: SessionKey? {
        guard let main = model.assistantMain, main.group == from.group else { return nil }
        return main
    }

    private func list() {
        let machines = model.machines.filter { $0.group == from.group }.map { (ref: $0.ref, name: $0.name) }
        sections = ForwardTargets.sections(sessions, from: from, recents: recents, main: main, machines: machines, query: query) {
            model.machine($0)?.online == true
        }
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Button {
                        picked(.share(draft.share))
                        dismiss()
                    } label: {
                        Label("Share as Image…", systemImage: Theme.Icon.share).lineLimit(1)
                    }
                    .accessibilityIdentifier("forward.shareImage")
                    let p = draft.preview
                    QuoteCard(heading: p.quoteHeading, icon: Theme.Icon.forwardMessage, meta: ForwardMessage.meta(p),
                              summary: p.forwardSummary, lines: 2, tint: Theme.accent, ink: .secondary) {}
                        .allowsHitTesting(false)
                        .accessibilityIdentifier("forward.preview")
                }
                if query.isEmpty {
                    Section {
                        if from != main { fixedRow(.persona, title: "Persona", detail: String(localized: "Main Chat"), icon: Theme.Icon.persona) }
                        fixedRow(.newAgent, title: String(localized: "New Agent…"), detail: String(localized: "Pick a computer, folder and model"),
                                 icon: Theme.Icon.add)
                    }
                }
                ForEach(sections) { section in
                    Section {
                        ForEach(section.sessions) { s in row(s, showsMachine: section.kind != .machine) }
                    } header: {
                        switch section.kind {
                        case .recent: Text("Recent")
                        case .machine: Text(verbatim: section.title)
                        case .results: EmptyView()
                        }
                    }
                }
                if !query.isEmpty && sections.isEmpty {
                    Text("No matching chats").foregroundStyle(.secondary).accessibilityIdentifier("forward.noMatch")
                }
                if let error {
                    Text(error).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                }
            }
            .pageSearchable(text: $query, placement: .navigationBarDrawer(displayMode: .always),
                            prompt: String(localized: "Search chats, computers or projects"))
            .navigationTitle("Forward To")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
            .disabled(busy != nil)
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
        .onAppear {
            sessions = model.sessionsNow
            recents = ForwardMemory(group: from.group).sessions
            list()
        }
        .onChange(of: query) { list() }
        .accessibilityIdentifier("forward.sheet")
    }

    private func fixedRow(_ target: ForwardTarget, title: String, detail: String, icon: String) -> some View {
        Button { pick(target) } label: {
            HStack(spacing: Theme.Space.m) {
                IconTile(icon, tint: Theme.accent)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(verbatim: title).foregroundStyle(.primary).lineLimit(1)
                    Text(verbatim: detail).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: Theme.Space.s)
                if busy == target { ProgressView() }
            }
        }
        .accessibilityIdentifier(target == .persona ? "forward.persona" : "forward.newAgent")
    }

    private func row(_ s: Session, showsMachine: Bool) -> some View {
        let machine = showsMachine && !s.assistant ? (model.machine(s.machineRef)?.name ?? s.machineID) : nil
        return Button { pick(.session(s.key)) } label: {
            HStack(spacing: Theme.Space.m) {
                if s.assistant { IconTile(Theme.Icon.persona, tint: Theme.accent) } else { AgentMark(s, side: Theme.Size.wellM) }
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(s.displayTitle).foregroundStyle(.primary).lineLimit(1)
                    Text(ForwardTargets.detail(s, machine: machine)).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: Theme.Space.s)
                if busy == .session(s.key) {
                    ProgressView()
                } else if !s.assistant {
                    HStack(spacing: Theme.Space.xs) {
                        Circle().fill(s.state.color).scaledFrame(width: Theme.Size.dot, height: Theme.Size.dot)
                        Text(s.state.label).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                    }
                    .fixedSize()
                }
            }
        }
        .accessibilityIdentifier("forward.session." + s.sessionID)
    }

    /// Reads the whole message (and its pictures), then hands it to the chat and goes.
    private func pick(_ target: ForwardTarget) {
        guard busy == nil else { return }
        busy = target
        error = nil
        Task {
            do {
                let done = try await draft.complete()
                Haptics.tap()
                picked(target == .newAgent ? .newAgent(done.ref, done.images) : .target(done.ref, done.images, target))
                dismiss()
            } catch {
                busy = nil
                self.error = error.isOffline ? String(localized: "Can't reach the hub") : error.shown
            }
        }
    }
}
#endif
