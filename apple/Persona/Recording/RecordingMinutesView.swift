#if os(iOS) || os(visionOS)
import SwiftUI
import UIKit

/// A recording's minutes as its page shows them (#2710, docs/apple/recording.md「Minutes」): written by the hub from the
/// transcript once the recording is done and synced, read from `GET /api/v1/kb/recordings/{id}` (`detail.minutes`).
@MainActor @Observable final class RecordingMinutesModel {
    enum State: Equatable {
        /// Not a meeting, and the hub wrote none: no section.
        case none
        /// Still recording or transcribing: minutes come after.
        case waitingForText
        /// The text isn't on the hub yet (offline, not paired).
        case waitingForSync
        /// The hub is writing them.
        case writing
        case ready(RecordingMinutes)
        case failed
        /// A meeting, synced, and the hub says nothing about minutes: it doesn't write them yet (an older hub).
        case unsupported
    }

    private(set) var state: State = .none
    /// `minutes_state` behind what is shown: `pending` over shown minutes while newer ones are written (Updating), `failed`
    /// when those didn't come (the shown ones stay).
    private(set) var status: String?
    /// The transcript expired on the hub (`trimmed_at`): its minutes stay, and none can be written again.
    private(set) var trimmed = false
    /// A write (Regenerate, Add to To-Dos) or the read-back after it is on its way: no other one starts meanwhile.
    private(set) var asking = false
    /// Bumped by Try Again: the page's polling starts over (its task is keyed on it).
    private(set) var round = 0
    @ObservationIgnored private let api: () -> APIClient?

    init(api: @escaping () -> APIClient? = { AppModel.shared.api }) { self.api = api }

    /// `detail.minutes`, and `detail.meta.minutes_state`: pending, ready, failed, or absent for a note.
    nonisolated struct Answer: Decodable, Sendable {
        struct Meta: Decodable {
            var state: String?
            var trimmedAt: String?
            enum CodingKeys: String, CodingKey { case state = "minutes_state", trimmedAt = "trimmed_at" }
        }
        struct Detail: Decodable {
            var minutes: RecordingMinutes?
            var meta: Meta?
        }
        var detail: Detail
    }

    /// Where `entry`'s minutes are now. `synced`: the hub has this revision of its text.
    func load(_ entry: RecordingEntry, synced: Bool) async {
        let meeting = entry.isMeeting
        if entry.active || !entry.transcribedAll {
            if case .ready = state { return } // a re-transcription keeps showing the minutes it has
            state = meeting ? .waitingForText : .none
            return
        }
        // No speech recognized: nothing goes to the hub, so there is nothing to write minutes from.
        guard RecordingSync.uploadable(entry) else { state = .none; return }
        guard let api = api(), !api.config.isDemo else { state = meeting ? .waitingForSync : .none; return }
        do {
            let answer = try await Self.fetch(entry.id, api)
            state = Self.state(minutes: answer.detail.minutes, status: answer.detail.meta?.state, meeting: meeting, synced: synced)
            status = answer.detail.meta?.state
            trimmed = answer.detail.meta?.trimmedAt?.isEmpty == false
            #if os(iOS)
            MinutesRelay.shared.note(entry, state: state)
            #endif
        } catch let error as APIError where error.status == 404 {
            state = meeting ? .waitingForSync : .none
        } catch {
            // Out of reach for now: what it showed stays; a meeting with nothing yet waits for the text to get there.
            if case .ready = state { return }
            state = meeting ? (synced ? .writing : .waitingForSync) : .none
        }
    }

    /// `GET /api/v1/kb/recordings/{id}`: the recording as the hub has it, its minutes included.
    static func fetch(_ id: UUID, _ api: APIClient) async throws -> Answer {
        try await api.get("/api/v1/kb/recordings/\(id.uuidString.lowercased())")
    }

    /// The hub's answer as a state: its minutes (`ready`, even with nothing to note); else what `minutes_state` says. A
    /// meeting it says nothing about waits for its text to get there, and once there is from a hub that writes no
    /// minutes (the contract sets `pending` as soon as a meeting is stored).
    nonisolated static func state(minutes: RecordingMinutes?, status: String?, meeting: Bool, synced: Bool) -> State {
        if let minutes, status == "ready" || !minutes.isEmpty { return .ready(minutes) }
        switch status {
        case "failed": return .failed
        case "pending": return .writing
        default: return meeting ? (synced ? .unsupported : .waitingForSync) : .none
        }
    }

    /// Writes them again (`POST /api/v1/recordings/{id}/minutes`): after a failure, or when they missed something. Minutes
    /// shown stay while the new ones are written (Updating), as the hub keeps them.
    func regenerate(_ id: UUID) async {
        guard let api = api(), !asking else { return }
        asking = true
        defer { asking = false }
        do {
            _ = try await api.request("POST", "/api/v1/recordings/\(id.uuidString.lowercased())/minutes")
            if case .ready = state { status = "pending" } else { state = .writing }
            round += 1
        } catch let error as APIError where error.code == "transcript_expired" {
            trimmed = true
            AppModel.shared.toast = Toast(String(localized: "The transcript has expired. Only these minutes are kept."), style: .error)
        } catch let error as APIError where error.status == 409 {
            // The transcript isn't complete on the hub yet: the minutes come once it is.
            if case .ready = state {} else { state = .waitingForSync }
            round += 1
        } catch {
            AppModel.shared.toast = Toast(String(localized: "Couldn't ask the hub for new minutes: \(error.localizedDescription)"), style: .error)
        }
    }

    /// Whether Regenerate goes with the minutes shown: the whole transcript is on the hub, nothing is being written, and
    /// it hasn't expired (#4062, as the web and Android offer it).
    func canRegenerate(_ entry: RecordingEntry, synced: Bool) -> Bool {
        guard case .ready = state else { return false }
        return synced && !trimmed && status != "pending" && !entry.active && entry.transcribedAll
    }

    /// Newer minutes are being written over the ones shown.
    var updating: Bool {
        if case .ready = state { return status == "pending" }
        return false
    }

    /// `{"todos": [Todo], "minutes": Minutes}`: only the minutes, with the confirmed actions' `todo_id`, are read.
    nonisolated struct Added: Decodable, Sendable { var minutes: RecordingMinutes? }

    /// Makes the action items `ids` to-dos (`POST /api/v1/recordings/{id}/minutes/todos` with the version shown), then
    /// reads the recording back before anything else is sent. Minutes rewritten since they were shown (`409
    /// minutes_stale`) are shown as they are now, and nothing is sent again: the user sees the new ones first.
    func add(_ ids: [String], to entry: RecordingEntry, synced: Bool) async {
        guard case .ready(let minutes) = state, let version = minutes.version, !ids.isEmpty, let api = api(), !asking else { return }
        asking = true
        defer { asking = false }
        struct Body: Encodable { var version: Int; var actions: [String] }
        do {
            let answer: Added = try await api.post("/api/v1/recordings/\(entry.id.uuidString.lowercased())/minutes/todos",
                                                   body: Body(version: version, actions: ids))
            if let minutes = answer.minutes { state = .ready(minutes) }
            Haptics.success()
            AppModel.shared.toast = Toast(ids.count == 1 ? String(localized: "Added to To-Dos")
                                          : String(localized: "Added \(ids.count) to To-Dos"), style: .success)
        } catch let error as APIError where error.code == "minutes_stale" {
            AppModel.shared.toast = Toast(String(localized: "The minutes changed. Showing the latest version; review the action items before adding them."), style: .error)
        } catch {
            AppModel.shared.toast = Toast(String(localized: "Couldn't add to To-Dos: \(error.localizedDescription)"), style: .error)
        }
        await load(entry, synced: synced)
    }

    /// Whether to ask again soon: while they are on their way, or newer ones are.
    var waiting: Bool {
        switch state {
        case .writing, .waitingForSync, .waitingForText: true
        case .ready: status == "pending"
        default: false
        }
    }
}

/// The minutes' sections in a recording's page: when and where, who was there, key points, decisions, action items and open questions, each
/// with the time it was said (a tap shows that line of the transcript); Copy and Share. Before they exist, what they wait for.
struct RecordingMinutesSections: View {
    var entry: RecordingEntry
    var model: RecordingMinutesModel
    /// The hub has this revision of its text (Regenerate needs the whole transcript there).
    var synced: Bool
    var jump: (Double) -> Void
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        switch model.state {
        case .none:
            EmptyView()
        case .waitingForText:
            waiting(String(localized: "Minutes are written once the recording ends and its text is transcribed."), busy: false)
        case .waitingForSync:
            waiting(String(localized: "Minutes are written once the text reaches the hub."), busy: false)
        case .writing:
            waiting(String(localized: "The hub is turning the transcript into key points, decisions, action items and open questions."), busy: true)
        case .unsupported:
            Section("Minutes") {
                Text("This hub doesn't write meeting minutes yet. They come after it's updated.")
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
            }
        case .failed:
            Section("Minutes") {
                Label("The minutes couldn't be written.", systemImage: Theme.Icon.error).foregroundStyle(Theme.danger)
                    .accessibilityIdentifier("minutes-failed")
                Button { Task { await model.regenerate(entry.id) } } label: {
                    Label("Retry", systemImage: Theme.Icon.retry).lineLimit(1)
                }
                .themedButton()
                .disabled(model.asking)
                .accessibilityIdentifier("minutes-retry")
            }
        case .ready(let minutes):
            ready(minutes)
        }
    }

    private func waiting(_ detail: String, busy: Bool) -> some View {
        Section("Minutes") {
            HStack(spacing: Theme.Space.s) {
                if busy { ProgressView() }
                Text(busy ? String(localized: "Writing the minutes…") : String(localized: "Minutes come next")).font(Theme.Font.bodyStrong)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("minutes-waiting")
            Text(detail).font(Theme.Font.caption).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private func ready(_ minutes: RecordingMinutes) -> some View {
        Section {
            if let title = minutes.title, !title.isEmpty, title != entry.displayTitle {
                Text(title).font(Theme.Font.bodyStrong)
            }
            // When in the app's own date style, as the header above it (#4875), and where.
            let line = [minutes.whenLine, minutes.place].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " · ")
            if !line.isEmpty {
                Label(line, systemImage: Theme.Icon.calendarEvent).font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(2)
                    .accessibilityIdentifier("minutes-when")
            }
            if !minutes.attendees.isEmpty {
                Label(ListFormatter.localizedString(byJoining: minutes.attendees), systemImage: Theme.Icon.people)
                    .font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(2)
            }
            HStack(spacing: Theme.Space.m) {
                Button { copy(minutes) } label: { Label("Copy", systemImage: Theme.Icon.copy).lineLimit(1) }
                    .themedButton()
                    .accessibilityIdentifier("minutes-copy")
                ShareLink(item: text(minutes), subject: Text(entry.displayTitle)) {
                    Label("Share", systemImage: Theme.Icon.share).lineLimit(1)
                }
                .themedButton()
                .accessibilityIdentifier("minutes-share")
            }
            if model.updating {
                // Newer ones on their way: these stay until they come (#4062).
                HStack(spacing: Theme.Space.s) {
                    ProgressView()
                    Text("Updating the minutes…").font(Theme.Font.body)
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("minutes-updating")
            } else if model.status == "failed" {
                Text("The new minutes couldn't be written. These are the earlier ones.")
                    .font(Theme.Font.caption).foregroundStyle(Theme.danger)
            }
            if model.trimmed {
                Text("The transcript has expired. Only these minutes are kept.").font(Theme.Font.caption).foregroundStyle(.secondary)
            }
            if model.canRegenerate(entry, synced: synced) {
                Button { Task { await model.regenerate(entry.id) } } label: {
                    Label("Regenerate", systemImage: Theme.Icon.retry).lineLimit(1)
                }
                .disabled(model.asking)
                .accessibilityIdentifier("minutes-regenerate")
            }
        } header: {
            Text("Minutes")
        }
        if minutes.isEmpty {
            Section { Text("Nothing to note: no points, decisions or action items came up.").font(Theme.Font.secondary).foregroundStyle(.secondary) }
        }
        items(String(localized: "Key Points"), Theme.Icon.keyPoint, minutes.points)
        items(String(localized: "Decisions"), Theme.Icon.decision, minutes.decisions)
        actions(minutes)
        items(String(localized: "Open Questions"), Theme.Icon.openQuestion, minutes.openQuestions)
    }

    @ViewBuilder private func items(_ heading: String, _ symbol: String, _ list: [RecordingMinutes.Item]) -> some View {
        if !list.isEmpty {
            Section(heading) {
                ForEach(Array(list.enumerated()), id: \.offset) { _, item in row(item, symbol) { EmptyView() } }
            }
        }
    }

    /// The action items, each offered as a to-do the user confirms (docs/assistant/meeting-minutes.md, #4062): Add to To-Dos,
    /// or Added once it is one; Add All when more than one is left. A hub that doesn't name its actions offers neither.
    @ViewBuilder private func actions(_ minutes: RecordingMinutes) -> some View {
        if !minutes.actions.isEmpty {
            let offered = minutes.version != nil
            let left = minutes.actions.filter { !$0.added }.compactMap(\.id)
            Section(String(localized: "Action Items")) {
                ForEach(Array(minutes.actions.enumerated()), id: \.offset) { _, item in
                    row(item, Theme.Icon.todo) {
                        if item.added {
                            Chip(text: String(localized: "Added [Recording Minutes a4062d]"), look: .tint(Theme.positive), compact: true)
                                .accessibilityIdentifier("minutes-added")
                        } else if offered, let id = item.id, !id.isEmpty {
                            Button { Task { await model.add([id], to: entry, synced: synced) } } label: {
                                Label("Add to To-Dos", systemImage: Theme.Icon.add).lineLimit(1).minimumScaleFactor(0.8)
                            }
                            .themedButton()
                            .disabled(model.asking)
                            .accessibilityLabel(String(localized: "Add to To-Dos: \(item.text)"))
                            .accessibilityIdentifier("minutes-add")
                        }
                    }
                }
                if offered, left.count > 1 {
                    Button { Task { await model.add(left, to: entry, synced: synced) } } label: {
                        Label("Add All", systemImage: Theme.Icon.add).lineLimit(1)
                    }
                    .disabled(model.asking)
                    .accessibilityIdentifier("minutes-add-all")
                }
            }
        }
    }

    /// A point, decision, action item or open question: its words, who and by when, its time; `below` under the words (an
    /// action item's Add to To-Dos). At the accessibility sizes the time goes under the words, which then have the row's
    /// width: beside it they broke every three characters and Add to To-Dos read "+ …" (#4062).
    private func row<Below: View>(_ item: RecordingMinutes.Item, _ symbol: String, @ViewBuilder below: () -> Below) -> some View {
        let large = typeSize.isAccessibilitySize
        return HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            ThemedIcon(symbol).font(Theme.Font.secondary).foregroundStyle(Theme.accent)
                .scaledFrame(width: Theme.Size.iconXS)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(item.text).font(Theme.Font.body).textSelection(.enabled)
                if let byline = RecordingMinutes.byline(item) {
                    Text(byline).font(Theme.Font.caption).foregroundStyle(.secondary)
                }
                if large { time(item) }
                below()
            }
            Spacer(minLength: Theme.Space.s)
            if !large { time(item) }
        }
    }

    /// When it was said: a tap shows that line of the transcript.
    @ViewBuilder private func time(_ item: RecordingMinutes.Item) -> some View {
        if let at = item.at {
            Button { jump(at) } label: {
                Chip(text: RecordingFormat.clock(at), look: .tint(Theme.accent), compact: true)
                    .scaledFrame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String(localized: "Show in transcript at \(RecordingFormat.clock(at))"))
            .accessibilityIdentifier("minutes-time")
        }
    }

    private func text(_ minutes: RecordingMinutes) -> String {
        minutes.text(title: entry.displayTitle)
    }

    private func copy(_ minutes: RecordingMinutes) {
        UIPasteboard.general.string = text(minutes)
        Haptics.success()
        AppModel.shared.toast = Toast(String(localized: "Minutes copied"), style: .success)
    }
}
#endif
