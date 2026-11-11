import SwiftUI

/// How an Agents row with the agent's latest words is drawn (#2530; Settings › Appearance › Layout, the Agents ⋯ menu's
/// Display): in every grouping, By Repository included (#3753), and Pinned. The BeautifulUI style draws its own capsules,
/// Compact List one line each; the Mac's and Apple Vision Pro's By Repository cards their own rows (`WorkspaceAgentRow`).
enum AgentRowStyle: String, CaseIterable, Identifiable {
    /// A: the agent's avatar, its state on the avatar's corner; the title and the time on a line, two lines of its latest
    /// words with Needs You at their end, where it runs. In a regular-width sidebar the words take one line.
    case avatar
    /// B: two lines: the state, the mark, the title and the time; one line of the latest words, the source at its end.
    case compact
    /// C: a card each, the state's color down its leading edge and its word spelled out; the one waiting for you tinted.
    case cards

    static let key = "agents.rowStyle"
    static let standard = AgentRowStyle.avatar
    var id: String { rawValue }
}

/// What the Agents list shows its agents as: one of the row styles, or Compact List (`dev.compact`). One choice in the two
/// places that set it (the Layout page, the Agents ⋯ menu's Display), kept in the two stored values it always was.
enum AgentListDisplay: String, CaseIterable, Identifiable {
    case avatar, compact, cards, list

    var id: String { rawValue }

    init(style: AgentRowStyle, list: Bool) {
        if list { self = .list; return }
        switch style {
        case .avatar: self = .avatar
        case .compact: self = .compact
        case .cards: self = .cards
        }
    }

    /// The row style it keeps (Compact List keeps the one chosen before it).
    var style: AgentRowStyle? {
        switch self {
        case .avatar: .avatar
        case .compact: .compact
        case .cards: .cards
        case .list: nil
        }
    }

    var title: String {
        switch self {
        case .avatar: String(localized: "Avatar [Agent Row Style]")
        case .compact: String(localized: "Two Lines [Agent Row Style]")
        case .cards: String(localized: "State Cards [Agent Row Style]")
        case .list: String(localized: "Compact List [Agents Menus 06829c]")
        }
    }

    /// What it looks like, under its preview on the Layout page.
    var summary: String {
        switch self {
        case .avatar: String(localized: "The agent's avatar, its title and time, two lines of what it last said, and where it runs.")
        case .compact: String(localized: "Two lines per agent: its title and time, then one line of what it last said.")
        case .cards: String(localized: "A card per agent, its state's color down the side; the one waiting for you is tinted.")
        case .list: String(localized: "One line per agent: its state, title and time.")
        }
    }

    var symbol: String {
        switch self {
        case .avatar: Theme.Icon.avatar
        case .compact: Theme.Icon.text
        case .cards: Theme.Icon.cardView
        case .list: Theme.Icon.listView
        }
    }
}

/// The stored choice behind `AgentListDisplay`, for a picker: reads and writes `agents.rowStyle` and `dev.compact`.
@MainActor
struct AgentListDisplayChoice {
    @StoredValue(AgentRowStyle.key) var style: AgentRowStyle = .standard
    @StoredValue("dev.compact") var list = false

    var display: AgentListDisplay {
        get { AgentListDisplay(style: style, list: list) }
        nonmutating set {
            if let s = newValue.style { style = s }
            list = newValue == .list
        }
    }

    var binding: Binding<AgentListDisplay> { Binding(get: { display }, set: { display = $0 }) }
}

/// Style A's avatar: the agent's mark in a round well, its state on the corner (`StatusDot`, which breathes for Needs You),
/// rimmed in the row's background so it reads apart from the well. VoiceOver reads both, as on the rows before.
struct AgentAvatar: View {
    var session: Session
    var dense = false
    /// Off where something else beside the row says the state (`SlotRow.dot`).
    var dot = true

    var body: some View {
        let side = dense ? Theme.Size.wellS : Theme.Size.wellL
        AgentMark(session, side: dense ? Theme.Size.markS : Theme.Size.markL)
            .scaledFrame(width: side, height: side)
            .background(Theme.Fill.well, in: Circle())
            .overlay(alignment: .bottomTrailing) {
                if dot {
                    StatusDot(state: session.state, size: dense ? Theme.Size.dot : Theme.Size.dotXL, waiting: session.waitsInBackground)
                        .padding(Theme.Size.rule)
                        .background(Theme.card, in: Circle())
                        .offset(x: Theme.Size.rule, y: Theme.Size.rule)
                }
            }
    }
}

extension SlotRow {
    /// The row in the list's chosen style (`AgentRowStyle`), outside the BeautifulUI style.
    @MainActor @ViewBuilder func styled(_ style: AgentRowStyle, dense: Bool) -> some View {
        switch style {
        case .avatar: avatarRow(dense: dense)
        case .compact: twoLineRow
        case .cards: cardRow
        }
    }

    /// Spelled out at the end of the latest words (A) or before them (B): the agent waits for you.
    var needsYouPill: Bool { session.state == .needsYou && !muted && !hideState && !pendingCount }

    @MainActor private var needsYouChip: some View {
        Chip(text: session.state.label, look: .tint(StatusPalette.needsYou.text), compact: true)
            .fixedSize()
    }

    @MainActor private var titleText: some View {
        UltracodeMark.after(session.displayTitle, on: ultracodeMark)
            .font(session.state == .unread ? Theme.Font.bodyHeavy : Theme.Font.bodyStrong)
            .lineLimit(1)
            .animation(UltracodeMark.motion, value: ultracodeMark)
    }

    @MainActor private var timeText: some View {
        AgentTimeText(time: time, fallback: session.updatedAt, font: Theme.Font.label)
    }

    /// Where it runs, one line: the source first (its tag, a tap away for a Lark group), then what `placeText` says, its
    /// to-do, a muted agent's bell. `states`: with Awaiting Reply and Waiting in the Background, unless the row spells
    /// its state out already (C).
    @MainActor private func placeLine(states: Bool = true) -> some View {
        HStack(spacing: Theme.Space.xs) {
            sourceLabel
            placeText(states: states).lineLimit(1)
            if !session.modelShortName.isEmpty {
                Chip(text: session.modelShortName, look: .outline, compact: true)
            }
            if let todo = session.todo {
                ViewThatFits(in: .horizontal) {
                    Chip(text: todo.title, symbol: Theme.Icon.todo, look: .outline)
                    ThemedIcon(Theme.Icon.todo).help(todo.title).accessibilityLabel(String(localized: "To-do: \(todo.title)"))
                }
            }
            if muted {
                ThemedIcon(Theme.Icon.muted).font(Theme.Font.label).accessibilityLabel("Notifications muted")
            }
        }
        .font(Theme.Font.caption)
        .foregroundStyle(.secondary)
    }

    // MARK: A · Avatar

    @MainActor private func avatarRow(dense: Bool) -> some View {
        HStack(alignment: .top, spacing: dense ? Theme.Space.s : Theme.Space.m) {
            AgentAvatar(session: session, dense: dense, dot: dot)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                TimeBesideOrUnder {
                    titleText.frame(maxWidth: .infinity, alignment: .leading)
                } time: {
                    timeText
                }
                if let words = session.listPreview(muted: muted) {
                    HStack(alignment: .top, spacing: Theme.Space.s) {
                        Text(words)
                            .font(dense ? Theme.Font.caption : Theme.Font.secondary)
                            .foregroundStyle(.secondary)
                            .lineLimit(dense ? 1 : 2)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        if needsYouPill { needsYouChip }
                    }
                } else if needsYouPill {
                    needsYouChip
                }
                if !session.runningWorkflows.isEmpty { WorkflowLine(runs: session.runningWorkflows) }
                if hasPlaceLine { placeLine().padding(.top, Theme.Space.xxs) }
            }
        }
        .padding(.vertical, dense ? Theme.Space.xxs : Theme.Space.xs)
    }

    // MARK: B · Two lines

    @MainActor private var twoLineRow: some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            if dot { StatusDot(state: session.state, size: Theme.Size.dot, waiting: session.waitsInBackground) }
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                TimeBesideOrUnder {
                    HStack(spacing: Theme.Space.xs) {
                        if !session.assistant { AgentMark(session, side: Theme.Size.markS) }
                        titleText
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                } time: {
                    timeText
                }
                HStack(spacing: Theme.Space.xs) {
                    if needsYouPill { needsYouChip }
                    let words = Text(session.listPreview(muted: muted) ?? placeString)
                    (stateWords.map { $0 + Text(verbatim: " · ") + words } ?? words)
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    if muted {
                        ThemedIcon(Theme.Icon.muted).font(Theme.Font.label).foregroundStyle(.secondary).accessibilityLabel("Notifications muted")
                    }
                    sourceLabel
                }
                if !session.runningWorkflows.isEmpty { WorkflowLine(runs: session.runningWorkflows) }
            }
        }
        .padding(.vertical, Theme.Space.xxs)
    }

    // MARK: C · State cards

    @MainActor private var cardRow: some View {
        let tone = session.stateTone(muted: muted)
        let waits = session.state == .needsYou && !muted
        // Its state in words, but not a quiet one's (Seen) nor where the section says it.
        let word = hideState || muted || (session.state == .idle && !session.awaitsYourReply(muted: muted)) ? nil : session.stateLabel(muted: muted)
        return VStack(alignment: .leading, spacing: Theme.Space.xs) {
            TimeBesideOrUnder {
                HStack(spacing: Theme.Space.xs) {
                    if !session.assistant { AgentMark(session, side: Theme.Size.markS) }
                    titleText
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            } time: {
                timeText
            }
            if let words = session.listPreview(muted: muted) {
                Text(words).font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(2)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            if !session.runningWorkflows.isEmpty { WorkflowLine(runs: session.runningWorkflows) }
            HStack(spacing: Theme.Space.s) {
                if dot, let word {
                    Text(word).font(Theme.Font.captionMedium).foregroundStyle(tone.text).lineLimit(1).fixedSize()
                }
                placeLine(states: !(dot && word != nil))
            }
        }
        .padding(.vertical, Theme.Space.s)
        .padding(.leading, Theme.Space.l)
        .padding(.trailing, Theme.Space.m)
        .background(waits ? StatusPalette.needsYouWash : Theme.card, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).stroke(Theme.cardStroke, lineWidth: Theme.Size.hairline))
        .overlay(alignment: .leading) {
            if dot {
                Capsule().fill(tone.dot)
                    .scaledFrame(width: Theme.Size.barWide)
                    .padding(.vertical, Theme.Space.s)
                    .padding(.leading, Theme.Space.xs)
            }
        }
    }
}
