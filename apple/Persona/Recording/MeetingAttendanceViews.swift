#if os(iOS)
import SwiftUI

// Persona in a recorded meeting (#2981, docs/apple/recording.md「Persona in meetings」): the session that attends, picked as
// a meeting recording starts or while it records (the Recordings page's options, the live sheet's Persona strip), and the
// answers in the live sheet's timeline. Only with a hub that lists `meeting_attendance`: without one these rows never show
// and the page is as it was.

/// "Who Attends: <session>", or a prompt to choose one.
struct MeetingAttendeeLabel: View {
    var title: String?
    var detail: String?

    var body: some View {
        LabeledContent {
            if let title {
                VStack(alignment: .trailing, spacing: Theme.Space.xxs) {
                    Text(title).foregroundStyle(.primary).lineLimit(1)
                    if let detail, !detail.isEmpty { Text(detail).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1) }
                }
            } else {
                Text("Choose [Meeting Attendee]").foregroundStyle(Theme.accent).lineLimit(1)
            }
        } label: {
            Text("Who Attends").lineLimit(1)
        }
        .font(Theme.Font.body)
    }
}

/// Who attends: the main chat, Persona's other chats, or one running Agent (`MeetingAttendees`).
struct MeetingAttendeePicker: View {
    var selected: SessionKey?
    var pick: (MeetingAttendees.Candidate) -> Void
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @Environment(\.dynamicTypeSize) private var size

    var body: some View {
        let all = MeetingAttendees.candidates(model)
        let chats = all.filter(\.persona)
        let agents = all.filter { !$0.persona }
        List {
            Section {
                ForEach(chats) { row($0) }
            } header: {
                Text("Persona")
            } footer: {
                Text("The main chat answers from earlier meetings, your projects and to-dos.")
            }
            Section {
                if agents.isEmpty {
                    Text("No Agents are running. Start one in Agents, or let the main chat attend.")
                        .font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                ForEach(agents) { row($0) }
            } header: {
                Text("Agents")
            } footer: {
                Text("An Agent also answers about the project it works on. Sessions on a work computer, and ones that never ask before they act, can't attend.")
            }
        }
        .listStyle(.insetGrouped)
        .themedPage()
        .navigationTitle(String(localized: "Who Attends"))
        .navigationBarTitleDisplayMode(.inline)
    }

    private func row(_ c: MeetingAttendees.Candidate) -> some View {
        Button {
            pick(c)
            dismiss()
        } label: {
            HStack(spacing: Theme.Space.m) {
                Group {
                    if c.persona {
                        ThemedIcon(Theme.Icon.persona).foregroundStyle(Theme.ai)
                    } else if let s = model.session(c.key) {
                        AgentMark(s)
                    }
                }
                .scaledFrame(width: Theme.Size.markS)
                .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(c.title).font(Theme.Font.body).foregroundStyle(.primary).lineLimit(size.isAccessibilitySize ? 3 : 1)
                        .accessibilityIdentifier("meeting-attendee-title")
                    if !c.detail.isEmpty {
                        Text(c.detail).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(size.isAccessibilitySize ? 2 : 1)
                    }
                }
                Spacer(minLength: Theme.Space.s)
                if c.key == selected {
                    ThemedIcon(Theme.Icon.done).foregroundStyle(Theme.accent)
                        .accessibilityLabel(Text("Selected [Meeting Attendee]"))
                }
            }
        }
        // The whole row taps (the list's own button), in the page's text colors rather than the list's tint.
        .tint(Color.primary)
        .accessibilityIdentifier(c.main ? "meeting-attendee-main" : c.persona ? "meeting-attendee-chat" : "meeting-attendee-agent")
    }
}

/// Why Persona can't follow the meeting right now, in plain words (the live sheet's Persona strip shows it).
@MainActor enum MeetingAttendanceProblem {
    static func text(_ entry: RecordingEntry, live: MeetingLive, sync: RecordingSync) -> String? {
        let attendance = entry.attendance ?? RecordingAttendance(role: .off)
        if attendance.stopped == .unsupported { return String(localized: "This hub can't follow meetings, so Persona stopped attending this one.") }
        guard attendance.role.attends else { return nil }
        if attendance.stopped == .liveFull { return String(localized: "The hub takes no more of this meeting's lines. Questions still work.") }
        if sync.attendanceProblem[entry.id] != nil { return String(localized: "The hub didn't take Persona's part in this meeting. Choose the role again.") }
        switch live.state(entry.id).problem {
        case nil, "": return nil
        case "attendee_unavailable": return String(localized: "Who attends can't take messages now. Persona catches up once it can.")
        case "attendee_not_allowed": return String(localized: "The hub won't pass other people's words to this session. Questions still work.")
        case "live_full": return String(localized: "The hub takes no more of this meeting's lines. Questions still work.")
        default: return String(localized: "Persona can't follow this meeting right now.")
        }
    }
}

/// One question and its answer: what was asked (or heard) at its time on the recording, then Persona looking, the answer
/// with what it drew on (and, said out loud, Stop Speaking), or what went wrong with Retry.
struct MeetingExchangeRow: View {
    var exchange: MeetingAsk.Exchange
    var entry: RecordingEntry
    @Environment(\.dynamicTypeSize) private var size
    @State private var ask = MeetingAsk.shared
    @State private var speaker = MeetingSpeaker.shared

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                Text(exchange.question ?? String(localized: "What was just said"))
                    .font(Theme.Font.secondaryMedium).foregroundStyle(.secondary).lineLimit(size.isAccessibilitySize ? 6 : 3)
                Spacer(minLength: Theme.Space.s)
                Text(RecordingFormat.clock(exchange.at)).font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
            switch exchange.state {
            case .asking:
                HStack(spacing: Theme.Space.s) {
                    ProgressView()
                    Text("Persona is looking…").font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                .accessibilityIdentifier("meeting-answer-waiting")
            case .answered(let text):
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                    ThemedIcon(Theme.Icon.persona).foregroundStyle(Theme.ai).accessibilityHidden(true)
                    Text(text).font(Theme.Font.body).textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("meeting-answer")
                }
                if !exchange.sources.isEmpty {
                    Text("From \(ListFormatter.localizedString(byJoining: exchange.sources))")
                        .font(Theme.Font.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("meeting-answer-sources")
                }
                if speaker.speaking == exchange.id {
                    if size.isAccessibilitySize {
                        VStack(alignment: .leading, spacing: Theme.Space.s) { speaking; stopSpeaking }
                    } else {
                        HStack(spacing: Theme.Space.m) { speaking; Spacer(minLength: Theme.Space.s); stopSpeaking }
                    }
                }
            case .failed(let message):
                Text(message).font(Theme.Font.secondary).foregroundStyle(Theme.danger)
                    .accessibilityIdentifier("meeting-answer-failed")
                Button { ask.retry(exchange.id, entry: entry) } label: {
                    Label("Retry", systemImage: Theme.Icon.retry).lineLimit(1)
                }
                .themedButton()
                .accessibilityIdentifier("meeting-answer-retry")
            }
        }
        .padding(.vertical, Theme.Space.xs)
        .accessibilityElement(children: .contain)
    }

    private var speaking: some View {
        Label("Speaking", systemImage: Theme.Icon.voiceSpeaking)
            .font(Theme.Font.secondary).foregroundStyle(Theme.ai).lineLimit(1)
            .accessibilityIdentifier(Self.speakingID(speaker.voice))
    }

    /// Which voice says it, for UI tests (an identifier, never read out): the realtime voice, the on-device one, or
    /// neither yet (the realtime voice is being asked for).
    static func speakingID(_ voice: MeetingSpeaker.Voice?) -> String {
        switch voice {
        case .realtime?: "meeting-speaking-realtime"
        case .onDevice?: "meeting-speaking-device"
        case nil: "meeting-speaking"
        }
    }

    private var stopSpeaking: some View {
        Button { ask.stopSpeaking() } label: {
            Label("Stop Speaking", systemImage: Theme.Icon.interrupt).lineLimit(1)
        }
        .themedButton()
        .accessibilityIdentifier("meeting-stop-speaking")
    }
}
#endif
