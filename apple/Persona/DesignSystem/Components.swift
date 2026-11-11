import BeautifulUI
import SwiftUI

/// Agent state dot (docs/protocol/agent-states.md): needs you breathes — the one dot that moves, and not with Reduce Motion;
/// working has a still ring around it (its spinner is in the work and tool rows); finished-unseen solid green, seen
/// muted gray. Waiting in the background (#6045): a clock in Working's amber, lighter.
struct StatusDot: View {
    var state: AgentState
    var size: CGFloat = 9
    /// It only waits in the background (`Session.waitsInBackground`).
    var waiting = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// The needs-you ring: one breath every `breath` seconds, out to `1 + breathScale` times the dot as it fades.
    static let breath = Theme.Motion.needsYouBreathDuration
    static let breathScale = Theme.Motion.needsYouBreathScale
    static let ringWidth = Theme.Size.lineMedium
    static let ringOpacity = Theme.Motion.needsYouRingOpacity

    nonisolated static func showsBreathingRing(state: AgentState, reduceMotion: Bool) -> Bool {
        state == .needsYou && !reduceMotion
    }

    var body: some View {
        Group {
            if state == .working, waiting {
                // As wide as the working ring around a dot.
                Theme.Icon.image(Theme.Icon.backgroundWaiting) // resizable: an Image, not ThemedIcon
                    .resizable().scaledToFit()
                    .frame(width: size * 1.7, height: size * 1.7)
                    .foregroundStyle(StatusPalette.backgroundWaiting.dot)
            } else if state == .working {
                StrokeShapeView(shape: Circle().scale(1.7), style: state.color.opacity(0.45),
                                strokeStyle: StrokeStyle(lineWidth: 1.5 * 1.7), isAntialiased: true,
                                background: Circle().fill(state.color))
            } else if Self.showsBreathingRing(state: state, reduceMotion: reduceMotion) {
                Circle().fill(state.color)
                    .overlay {
                        #if os(macOS)
                        // Core Animation runs it: the clock below relaid the whole window 30 times a second.
                        MacBreathingRing(color: state.color.opacity(Self.ringOpacity))
                            .allowsHitTesting(false)
                            .accessibilityHidden(true)
                        #else
                        VisibleWork { eligible in
                            BreathingRing(color: state.color.opacity(Self.ringOpacity), eligible: eligible)
                        }
                        .allowsHitTesting(false)
                        .accessibilityHidden(true)
                        #endif
                    }
            } else {
                Circle().fill(state.color)
            }
        }
        .frame(width: size, height: size)
        .accessibilityLabel(state == .working && waiting ? String(localized: "Waiting in the background") : state.label)
    }
}

/// The small Working spinner shared by activity rows. System animation runs without a per-frame SwiftUI timeline.
struct WorkingIndicator: View {
    var body: some View {
        ProgressView().controlSize(.mini).tint(Theme.busy)
            .accessibilityLabel(StateName.working.text)
    }
}

/// A dense row's state at its end (the Assistant sidebar's main chat and Directions, All Chats, #4538): Needs You a
/// still blue dot, Working the amber spinner, Unread a green dot, nothing for the rest. Still in a dense list, so only
/// the spinner turns; the row's own label says the state in words.
struct RowStateMark: View {
    var state: AgentState

    /// The dot's color; nil where the mark is the spinner or nothing.
    nonisolated static func dot(_ state: AgentState) -> Color? {
        switch state {
        case .needsYou: StatusPalette.needsYou.dot
        case .unread: StatusPalette.unread.dot
        default: nil
        }
    }

    var body: some View {
        if state == .working {
            WorkingIndicator()
        } else if let color = Self.dot(state) {
            Circle().fill(color).scaledFrame(width: Theme.Size.dot, height: Theme.Size.dot)
                .accessibilityLabel(state.label)
        }
    }
}

/// Machine glyph inside a soft tinted squircle with the state dot in the corner.
struct SessionAvatar: View {
    var machine: Machine?
    var state: AgentState
    var size: CGFloat = 44
    /// In place of the machine's symbol (the inbox shows what the agent asks: a question, a plan…).
    var symbol: String?
    /// The state dot on the corner; off where the square's color already says it (the inbox's Needs You rows).
    var dot = true

    var body: some View {
        let tint: Color = state == .idle || state == .offline || state == .unknown ? .secondary : state.color
        ZStack(alignment: .bottomTrailing) {
            RoundedRectangle(cornerRadius: size * 0.3, style: .continuous)
                .fill(Theme.Fill.chip)
                .frame(width: size, height: size)
                .overlay {
                    ThemedIcon(symbol ?? machine?.symbol ?? Theme.Icon.terminal)
                        .font(.system(size: size * 0.42, weight: .medium))
                        .foregroundStyle(tint)
                }
            if dot, state != .offline {
                StatusDot(state: state, size: size * 0.24)
                    .padding(2.5)
                    .background(Circle().fill(Color(.systemBackground)))
                    .offset(x: 3, y: 3)
            }
        }
    }
}

/// The BeautifulUI style's mark for an agent's state, its task badge (docs/protocol/agent-states.md): Needs You a red badge with "!", working the
/// turning ring, Unread the green check, seen an empty ring; offline and unknown a gray dot. Still, apart from the
/// working ring: the red badge says Needs You without the breathing dot's 30 fps redraw. Waiting in the background
/// (#6045): a clock in Working's amber, lighter.
struct AgentStateBadge: View {
    var state: AgentState
    /// It only waits in the background (`Session.waitsInBackground`).
    var waiting = false
    private static let attention = BUIGlyph("M12 6v8M12 18.5v.01")

    var body: some View {
        switch state {
        case .needsYou: badge(Theme.needsYou, Self.attention)
        case .working where waiting:
            Theme.Icon.image(Theme.Icon.backgroundWaiting) // resizable: an Image, not ThemedIcon
                .resizable().scaledToFit()
                .frame(width: Theme.Size.badge, height: Theme.Size.badge)
                .foregroundStyle(StatusPalette.backgroundWaiting.dot)
                .accessibilityLabel(String(localized: "Waiting in the background"))
        case .working: BUISpinner(size: Theme.Size.badge, lineWidth: Theme.Size.rule)
        case .unread: badge(Theme.positive, .check)
        case .idle: Circle().strokeBorder(Theme.cardStroke, lineWidth: Theme.Size.rule).frame(width: Theme.Size.badge, height: Theme.Size.badge)
        case .offline, .unknown: StatusDot(state: state, size: 9)
        }
    }

    private func badge(_ fill: Color, _ glyph: BUIGlyph) -> some View {
        Circle().fill(fill)
            .overlay { BUIIcon(glyph, size: 13, lineWidth: 3.5).foregroundStyle(.white) }
            .frame(width: Theme.Size.badge, height: Theme.Size.badge)
            .accessibilityHidden(true)
    }
}

/// A row's name and the chips that go with it: side by side; at the accessibility text sizes the chips on a line under
/// the name. A `Chip` never gives up width, and beside one a narrow phone left the name a character or none (#1796).
struct NameWithChips<Name: View, Chips: View>: View {
    @ViewBuilder var name: Name
    @ViewBuilder var chips: Chips
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        if typeSize.isAccessibilitySize {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                name
                HStack(spacing: Theme.Space.sm) { chips }
            }
        } else {
            HStack(spacing: Theme.Space.sm) {
                name
                chips
            }
        }
    }
}

/// The app's one small label (not a control): a fact beside other text. Three looks —
/// `.neutral`: secondary words on a soft gray (a model, a version, a tab, DMs);
/// `.tint`: a meaning's words on its soft fill (a state's tone, @Me, Recommended in the AI's color);
/// `.outline`: secondary words in a hairline (which agent: Claude / Codex).
/// The BeautifulUI style draws it as that library's pill (`BUIPill`).
struct Chip: View {
    enum Look {
        case neutral
        /// `text` defaults to the color itself; a state passes its tone (`.tone(_:)`).
        case tint(Color, text: Color? = nil)
        case outline

        static func tone(_ t: StatusTone) -> Look { .tint(t.tint, text: t.text) }

        /// A supervised task's review state (#3124): caution words on a soft caution fill, danger, positive, or neutral.
        static func review(_ t: ReviewTone) -> Look {
            switch t {
            case .caution: .tint(Theme.caution.opacity(0.14), text: Theme.cautionText)
            case .danger: .tint(Theme.danger)
            case .positive: .tint(Theme.positive)
            case .neutral: .neutral
            }
        }
    }

    var text: String
    var symbol: String?
    var look: Look = .neutral
    /// Truncates instead of keeping its full width when offered less (in a FlowLayoutRow, a chip longer than the line).
    var shrinks = false
    /// A step quieter, for a tag that sits under a row's time (the Agents row's source, #3043): the label's size in
    /// regular weight, on a tighter capsule, in every style.
    var compact = false

    var body: some View {
        if compact {
            Text(text).lineLimit(1)
                .font(Theme.Font.label)
                .foregroundStyle(words)
                .padding(.horizontal, Theme.Space.sm)
                .padding(.vertical, Theme.Space.xxs / 2)
                .background(Capsule().fill(fill))
                .fixedSize()
        } else if AppStyle.current.usesBeautifulUI {
            BUIPill(text, tone: pillTone, icon: symbol.map { .symbol(Theme.Icon.styled($0)) }, truncates: shrinks)
        } else {
            plain
        }
    }

    /// The BeautifulUI style: its pill in the look's colors.
    private var pillTone: BUIPill.Tone {
        switch look {
        case .neutral: .neutral
        // A tone's tint is already soft; a bare color gets its own.
        case .tint(let c, let text): .custom(ink: text ?? c, fill: text == nil ? c.opacity(0.12) : c)
        case .outline: .outline
        }
    }

    private var plain: some View {
        Group {
            if let symbol {
                HStack(spacing: Theme.Space.xs) {
                    ThemedIcon(symbol).font(Theme.Font.labelStrong)
                    Text(text).lineLimit(1)
                }
            } else {
                Text(text).lineLimit(1)
            }
        }
        .font(Theme.Font.labelMedium)
        .foregroundStyle(words)
        .padding(.horizontal, Theme.Space.s)
        .padding(.vertical, 2.5)
        .background {
            if case .outline = look {
                Capsule().fill(fill).stroke(Color.secondary.opacity(0.4), lineWidth: 0.8)
            } else {
                Capsule().fill(fill)
            }
        }
        .fixedSize(horizontal: !shrinks, vertical: true)
    }

    private var words: Color {
        switch look {
        case .neutral, .outline: .secondary
        case .tint(let c, let text): text ?? c
        }
    }

    private var fill: Color {
        switch look {
        case .neutral: Theme.Fill.chip
        case .tint(let c, let text): text == nil ? c.opacity(0.12) : c // a tone's tint is already soft
        case .outline: .clear
        }
    }
}

/// Something a message names that a tap opens — a file (`FileChips`), a machine's port (`PortChips`) — as a badge
/// under it: its icon and name in the accent on a soft accent capsule, then a detail (a page's title) a step quieter.
/// `dimmed`: it is not there any more (a stopped server), in secondary words on the gray chip fill.
struct LinkBadge: View {
    var symbol: String
    var text: String
    var detail: String? = nil
    var dimmed = false

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            ThemedIcon(symbol).font(Theme.Font.label)
            Text(text).font(Theme.Font.labelMedium).lineLimit(1)
            if let detail, !detail.isEmpty {
                Text(detail).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .padding(.horizontal, Theme.Space.s)
        .padding(.vertical, Theme.Space.sm)
        .background(dimmed ? Theme.Fill.chip : Theme.accent.opacity(0.1), in: .capsule)
        .foregroundStyle(dimmed ? AnyShapeStyle(.secondary) : AnyShapeStyle(Theme.accent))
    }
}

/// A chip that turns a filter or a choice on and off (the inbox's sources, the Feishu accounts): in the BeautifulUI
/// style its filter chip; otherwise a capsule filled with the accent while on. `count` follows the title.
struct FilterChip: View {
    var title: String
    var symbol: String?
    var count: Int?
    var on: Bool
    var action: () -> Void

    var body: some View {
        if AppStyle.current.usesBeautifulUI {
            BUIFilterChip(title, count: count, isActive: on) { tapped() }
        } else {
            Button(action: tapped) {
                HStack(spacing: Theme.Space.xs) {
                    if let symbol { ThemedIcon(symbol).font(Theme.Font.label) }
                    Text(title)
                    if let count { Text(count > 99 ? "99+" : "\(count)").monospacedDigit().opacity(0.7) }
                }
                .font(on ? Theme.Font.secondaryStrong : Theme.Font.secondary)
                .lineLimit(1)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.sm)
                .foregroundStyle(on ? Color.white : Color.primary)
                .background(on ? AnyShapeStyle(Theme.accent) : AnyShapeStyle(Color.secondary.opacity(0.12)), in: .capsule)
            }
            .buttonStyle(.plain)
            // Like BUIFilterChip: tell VoiceOver which chip is on, not only through the fill.
            .accessibilityAddTraits(on ? .isSelected : [])
        }
    }

    private func tapped() {
        withAnimation(Theme.Motion.standard) { action() }
        Haptics.tap()
    }
}

/// A count on a row or a section header: bold digits on a capsule, nothing at zero, "99+" past 99, the exact number to
/// VoiceOver. Its looks share the web's names (#4538): `.needs` Needs You's words on its tint, `.neutral` white on
/// `Theme.countBadge` gray, `.accent` white on the accent; `.tone` is any other state's words on its tint.
struct CountBadge: View {
    enum Look {
        case solid(Color)
        case tone(StatusTone)

        /// What waits for the user to act (#4538): Needs You's words on its neutral tint, as the web's `needs` tone.
        static var needs: Look { .tone(StatusPalette.needsYou) }
        /// A passive total: unread messages, items in a group.
        static var neutral: Look { .solid(Theme.countBadge) }
        /// The selected item's count, or one about you (@Me).
        static var accent: Look { .solid(Theme.accent) }
    }

    var count: Int
    var look: Look = .neutral
    /// What VoiceOver says for it ("3 need you"); by default the exact number, past 99 too.
    var label: String? = nil

    /// What it shows: nothing at zero, "99+" past 99.
    nonisolated static func text(_ count: Int) -> String? {
        count > 0 ? (count > 99 ? "99+" : "\(count)") : nil
    }

    var body: some View {
        if let text = Self.text(count) {
            Text(text)
                .font(Theme.Font.labelHeavy.monospacedDigit())
                .contentTransition(.numericText(value: Double(count)))
                .foregroundStyle(words)
                .padding(.horizontal, Theme.Space.sm)
                .padding(.vertical, Theme.Space.xxs)
                .background(fill, in: .capsule)
                .fixedSize()
                .accessibilityLabel(label ?? String(count))
        }
    }

    private var words: Color {
        switch look {
        case .solid: .white
        case .tone(let t): t.text
        }
    }

    private var fill: Color {
        switch look {
        case .solid(let c): c
        case .tone(let t): t.tint
        }
    }
}

struct StatusLabel: View {
    var state: AgentState

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            StatusDot(state: state, size: 7)
            Text(state.label)
                .font(Theme.Font.labelStrong)
                .foregroundStyle(state.tone.text)
        }
    }
}

/// A few options side by side, one chosen: the system's segmented control; in the BeautifulUI style its segmented switch.
struct SegmentedChoice<Value: Hashable>: View {
    var title: String
    var options: [(value: Value, label: String)]
    @Binding var selection: Value

    init(_ title: String, _ options: [(value: Value, label: String)], selection: Binding<Value>) {
        self.title = title
        self.options = options
        _selection = selection
    }

    var body: some View {
        if AppStyle.current.usesBeautifulUI {
            BUISegmented(options.map { .init($0.value, $0.label) }, selection: $selection, fills: true)
        } else {
            Picker(title, selection: $selection) {
                ForEach(options.indices, id: \.self) { i in Text(options[i].label).tag(options[i].value) }
            }
            .pickerStyle(.segmented)
        }
    }
}

struct EmptyStateView: View {
    var symbol: String
    var title: String
    var message: String
    var action: (title: String, run: () -> Void)?
    /// The mark's color when it means something (`Theme.caution` for a warning); the accent otherwise. BeautifulUI
    /// keeps its own quiet mark.
    var tint: Color? = nil

    var body: some View {
        if AppStyle.current.usesBeautifulUI {
            // BeautifulUI's empty state: the mark on a raised tile, the title, a line of help and one action.
            BUIEmptyState(symbol: Theme.Icon.styled(symbol), title: title, message: message, actionTitle: action?.title, action: action?.run)
                .frame(maxWidth: .infinity)
        } else {
            plain
        }
    }

    private var plain: some View {
        VStack(spacing: Theme.Space.l) {
            ThemedIcon(symbol)
                .font(Theme.Font.display).imageScale(.large)
                .foregroundStyle((tint ?? Theme.accent).gradient)
                .symbolEffect(.bounce, value: title)
            Text(title).font(Theme.Font.title)
            Text(message)
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            if let action {
                Button(action: action.run) { Text(action.title).lineLimit(1) }
                    .themedButton(prominent: true)
                    .padding(.top, Theme.Space.xs)
            }
        }
        .padding(Theme.Space.xxl)
        .frame(maxWidth: .infinity)
    }
}

extension View {
    /// Rounded card surface used across lists; in the BeautifulUI style its surface (the style's card color, its ring and
    /// soft shadow).
    @ViewBuilder func card(padding: CGFloat = 14, radius: CGFloat = Theme.Radius.card) -> some View {
        if AppStyle.current.usesBeautifulUI {
            self.padding(padding)
                .buiSurface(radius: radius, elevation: .card)
        } else {
            self.padding(padding)
                .background(Color(.secondarySystemGroupedBackground), in: .rect(cornerRadius: radius, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: radius, style: .continuous).stroke(Theme.cardStroke, lineWidth: 0.5))
        }
    }
}

/// Asks before closing an agent: quitting Claude and closing its herdr tab on a computer can't be undone.
/// Asks before closing an agent: a small sheet that says which one (name, where it runs) and what closing does, with
/// Close and Cancel. (The system confirmation dialog was a wall of text with no cancel button.)
struct CloseAgentConfirmation: ViewModifier {
    @Binding var session: Session?
    var onClosed: (SessionKey) -> Void = { _ in }
    @State private var completed: SessionKey?

    func body(content: Content) -> some View {
        content.sheet(item: $session, onDismiss: {
            if let key = completed { completed = nil; onClosed(key) }
        }) { s in
            CloseAgentSheet(session: s) { completed = $0 }
        }
    }
}

struct CloseAgentSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var session: Session
    var onClosed: (SessionKey) -> Void = { _ in }
    @State private var closing = false
    @State private var failure: String?
    @State private var height: CGFloat = 380
    /// The worktree Persona made that it works in (#6115), and what to do with it once it is closed.
    @State private var worktree: WorktreeStatus?
    @State private var cleanup = AgentWorktree.Cleanup()

    private var name: String {
        let n = session.agentName ?? session.title
        return n.isEmpty ? session.folderName : n
    }

    private var place: String {
        var parts: [String] = []
        if let m = model.machine(session.machineRef)?.title { parts.append(m) }
        if let space = session.spaceName, !space.isEmpty {
            // A tab named by its number reads "#1", as in the chat's header.
            let slot = session.slotName.map { $0.allSatisfy(\.isNumber) ? "#" + $0 : $0 }
            parts.append(slot.map { "\(space) › \($0)" } ?? space)
        } else if !session.folderName.isEmpty {
            parts.append(session.folderName)
        }
        return parts.joined(separator: " · ")
    }

    var body: some View {
        VStack(spacing: 0) {
            ThemedIcon(Theme.Icon.close)
                .font(Theme.Font.title)
                .foregroundStyle(Theme.danger)
                .scaledFrame(width: Theme.Size.box56, height: Theme.Size.box56)
                .background(Theme.danger.opacity(0.12), in: .circle)
                .padding(.top, 28)
            Text("Stop and Close This Agent?")
                .font(Theme.Font.title)
                .padding(.top, Theme.Space.l)
            VStack(spacing: Theme.Space.xs) {
                Text(name).font(Theme.Font.heading).lineLimit(2).multilineTextAlignment(.center)
                HStack(spacing: Theme.Space.sm) {
                    Text(session.agentLabel)
                        .font(Theme.Font.labelStrong)
                        .padding(.horizontal, Theme.Space.s)
                        .padding(.vertical, Theme.Space.xxs)
                        .overlay(Capsule().stroke(Color.secondary.opacity(0.4), lineWidth: 1))
                    if !place.isEmpty { Text(place).lineLimit(1) }
                }
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
            }
            .padding(.top, Theme.Space.m)
            .padding(.horizontal, Theme.Space.xl)

            VStack(alignment: .leading, spacing: Theme.Space.m) {
                fact(Theme.Icon.end, String(localized: "Stops the current task, quits \(session.agentLabel), and closes its terminal tab (only this pane when split)"))
                fact(Theme.Icon.retry, String(localized: "The chat history is kept; continue later with \(session.resumeCommand)"))
            }
            .padding(Theme.Space.l)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .padding(.horizontal, Theme.Space.xl)
            .padding(.top, Theme.Space.xl)

            if let worktree {
                WorktreeCleanupSection(status: worktree, cleanup: $cleanup, home: model.machine(session.machineRef)?.home)
                    .padding(.horizontal, Theme.Space.xl)
                    .padding(.top, Theme.Space.m)
                    .disabled(closing)
            }

            if let failure {
                Text(failure)
                    .font(Theme.Font.secondary)
                    .foregroundStyle(Theme.danger)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, Theme.Space.xl)
                    .padding(.top, Theme.Space.m)
                    .accessibilityIdentifier("agent.close.error")
            }
            VStack(spacing: Theme.Space.m) {
                Button {
                    closing = true
                    failure = nil
                    Haptics.tap()
                    Task {
                        if await model.close(session.key) {
                            onClosed(session.key)
                            dismiss()
                            // Once it is gone: the node refuses a worktree an agent still works in.
                            if let worktree { await model.cleanUpWorktree(session.machineRef, worktree, cleanup) }
                        } else {
                            failure = model.toast?.message ?? String(localized: "Couldn't stop. The agent and its tab were kept; try again.")
                            closing = false
                        }
                    }
                } label: {
                    Group {
                        if closing { ProgressView().tint(Theme.onSolid) } else { Text("Stop and Close").lineLimit(1) }
                    }
                    .font(Theme.Font.heading)
                    .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                }
                .themedButton(prominent: true)
                .tint(Theme.danger)
                .keyboardShortcut(.defaultAction)
                .disabled(closing || !AgentWorktree.ready(worktree, cleanup))
                .accessibilityIdentifier("agent.close.confirm")
                Button { dismiss() } label: {
                    Text("Cancel").lineLimit(1).font(Theme.Font.heading).scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                }
                .themedButton()
                .keyboardShortcut(.cancelAction)
                .disabled(closing)
            }
            .controlSize(.large)
            .padding(.horizontal, Theme.Space.xl)
            .padding(.top, Theme.Space.xl)
            .padding(.bottom, Theme.Space.m)
        }
        .fixedSize(horizontal: false, vertical: true)
        .onGeometryChange(for: CGFloat.self, of: { $0.size.height }) { height = $0 }
        .presentationDetents([.height(height)])
        .presentationDragIndicator(.visible)
        .interactiveDismissDisabled(closing)
        .task { worktree = await model.ourWorktree(session) }
        .animation(Theme.Motion.layout, value: worktree)
    }

    private func fact(_ symbol: String, _ text: String) -> some View {
        Label {
            Text(text).font(Theme.Font.secondary).fixedSize(horizontal: false, vertical: true)
        } icon: {
            ThemedIcon(symbol).font(Theme.Font.secondary).foregroundStyle(.secondary).scaledFrame(width: Theme.Size.iconS)
        }
    }
}

/// Asks before Delete Chat: says that it only leaves the lists (every app's), and that the record on the computer stays and
/// brings it back once used again. Delete runs it; a failure shows as the toast and the conversation stays.
/// When the agent worked in a worktree Persona made (#6115), its computer is asked first and the question is a sheet that
/// also offers to keep or remove the worktree (`DeleteAgentSheet`).
struct DeleteSessionConfirmation: ViewModifier {
    @Environment(AppModel.self) private var model
    @Binding var session: Session?
    var onDeleted: (SessionKey) -> Void = { _ in }
    /// What is shown for `session`: the alert, or the sheet with its worktree; nil while its computer is asked.
    @State private var alerting: Session?
    @State private var withWorktree: DeleteAgentSheet.Request?

    func body(content: Content) -> some View {
        content.alert("Delete This Chat?", isPresented: Binding(get: { alerting != nil }, set: { if !$0 { alerting = nil; session = nil } }),
                      presenting: alerting) { s in
            Button("Delete", role: .destructive) {
                Task { if await model.deleteSession(s.key) { onDeleted(s.key) } }
            }
            Button("Cancel", role: .cancel) {}
        } message: { s in
            Text(DeleteSessionText.message(title: s.displayTitle, machine: model.machine(s.machineRef)?.title,
                                           resume: s.canTerminal ? s.resumeCommand : nil))
        }
        .sheet(item: $withWorktree, onDismiss: { session = nil }) { r in
            DeleteAgentSheet(session: r.session, worktree: r.worktree, onDeleted: onDeleted)
        }
        .task(id: session?.key) {
            guard let s = session else { return }
            // A short wait at most: an unanswered computer leaves the plain question.
            let wt = await model.ourWorktree(s, timeout: 4)
            guard !Task.isCancelled, session?.key == s.key else { return }
            if let wt { withWorktree = .init(session: s, worktree: wt) } else { alerting = s }
        }
    }
}

/// Delete Chat for an agent that worked in a worktree Persona made: the question, the worktree's keep / remove, Delete.
struct DeleteAgentSheet: View {
    struct Request: Identifiable {
        var session: Session
        var worktree: WorktreeStatus
        var id: SessionKey { session.key }
    }

    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var session: Session
    var worktree: WorktreeStatus
    var onDeleted: (SessionKey) -> Void
    @State private var cleanup = AgentWorktree.Cleanup()
    @State private var deleting = false
    @State private var height: CGFloat = 360

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text("Delete This Chat?").font(Theme.Font.title)
            Text(DeleteSessionText.message(title: session.displayTitle, machine: model.machine(session.machineRef)?.title,
                                           resume: session.canTerminal ? session.resumeCommand : nil))
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            WorktreeCleanupSection(status: worktree, cleanup: $cleanup, home: model.machine(session.machineRef)?.home)
                .disabled(deleting)
            VStack(spacing: Theme.Space.m) {
                Button(role: .destructive) {
                    deleting = true
                    Haptics.tap()
                    Task {
                        if await model.deleteSession(session.key) {
                            onDeleted(session.key)
                            dismiss()
                            await model.cleanUpWorktree(session.machineRef, worktree, cleanup)
                        } else {
                            deleting = false
                        }
                    }
                } label: {
                    Group {
                        if deleting { ProgressView().tint(Theme.onSolid) } else { Text("Delete").lineLimit(1) }
                    }
                    .font(Theme.Font.heading)
                    .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                }
                .themedButton(prominent: true)
                .tint(Theme.danger)
                .keyboardShortcut(.defaultAction)
                .disabled(deleting || !AgentWorktree.ready(worktree, cleanup))
                .accessibilityIdentifier("chat.delete.confirm")
                Button { dismiss() } label: {
                    Text("Cancel").lineLimit(1).font(Theme.Font.heading).scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                }
                .themedButton()
                .keyboardShortcut(.cancelAction)
                .disabled(deleting)
            }
            .controlSize(.large)
            .padding(.top, Theme.Space.s)
        }
        .padding(.horizontal, Theme.Space.xl)
        .padding(.top, Theme.Space.xxl)
        .padding(.bottom, Theme.Space.m)
        .fixedSize(horizontal: false, vertical: true)
        .onGeometryChange(for: CGFloat.self, of: { $0.size.height }) { height = $0 }
        .presentationDetents([.height(height)])
        .presentationDragIndicator(.visible)
        .interactiveDismissDisabled(deleting)
    }
}

extension View {
    func confirmDeleteSession(_ session: Binding<Session?>, onDeleted: @escaping (SessionKey) -> Void = { _ in }) -> some View {
        modifier(DeleteSessionConfirmation(session: session, onDeleted: onDeleted))
    }

    func confirmClose(_ session: Binding<Session?>, onClosed: @escaping (SessionKey) -> Void = { _ in }) -> some View {
        modifier(CloseAgentConfirmation(session: session, onClosed: onClosed))
    }
}

/// Someone without a picture (a mail sender): the first letter on a disc.
struct Monogram<Fill: ShapeStyle>: View {
    var name: String
    var size: CGFloat = 40
    var fill: Fill
    /// The letter's color; by default the hierarchical primary (`Color.primary` turned white in a Mac list's selected
    /// row, on the light gray disc).
    var ink: Color?
    /// The letter's text level: a smaller disc takes a smaller one.
    var font: Font = Theme.Font.heading
    /// How many of the name's characters it shows (an extension's avatar shows the one or two it is given).
    var letters = 1

    var body: some View {
        Circle().fill(fill).frame(width: size, height: size)
            .overlay(Text(String((name.isEmpty ? "?" : name).prefix(letters))).font(font)
                .foregroundStyle(ink.map { AnyShapeStyle($0) } ?? AnyShapeStyle(.primary)))
    }
}

/// What a page that loads shows: a spinner, the thing, or why it failed.
enum Loadable<Value> {
    case loading
    case loaded(Value)
    case failed(String)

    var value: Value? {
        if case .loaded(let v) = self { v } else { nil }
    }
}

extension Loadable: Equatable where Value: Equatable {}
extension Loadable: Sendable where Value: Sendable {}

/// A `Loadable` drawn the one way: a centered spinner, the content, or the error with Retry.
struct LoadableContent<Value, Content: View>: View {
    var state: Loadable<Value>
    var retry: (() -> Void)?
    @ViewBuilder var content: (Value) -> Content

    var body: some View {
        switch state {
        case .loading:
            if AppStyle.current.usesBeautifulUI {
                // A page loading, not a row: BeautifulUI's pixel loader is fine here.
                LoadingState(String(localized: "Loading [Components d0676d]"), pattern: .dots).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        case .loaded(let value):
            content(value)
        case .failed(let message):
            if AppStyle.current.usesBeautifulUI {
                BUIEmptyState(symbol: Theme.Icon.styled(Theme.Icon.error), title: message, actionTitle: retry == nil ? nil : String(localized: "Retry [Components b8784c]"), action: retry)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ContentUnavailableView {
                    Label(message, systemImage: Theme.Icon.error)
                } actions: {
                    if let retry { Button(action: retry) { Text("Retry [Components b8784c]").lineLimit(1) }.themedButton() }
                }
            }
        }
    }
}

/// Chips in a row that wrap when they do not fit (a row can have several status columns).
struct FlowLayoutRow: Layout {
    var spacing: CGFloat = Theme.Space.sm

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width.flatMap { $0.isFinite ? $0 : nil } ?? .greatestFiniteMagnitude
        var x: CGFloat = 0, y: CGFloat = 0, line: CGFloat = 0, widest: CGFloat = 0
        for s in subviews {
            let size = Self.size(s, within: width)
            if x > 0 && x + size.width > width { y += line + spacing; x = 0; line = 0 }
            x += size.width + spacing
            line = max(line, size.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: min(widest, width), height: y + line)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, line: CGFloat = 0
        for s in subviews {
            let size = Self.size(s, within: bounds.width)
            if x > bounds.minX && x + size.width > bounds.maxX { y += line + spacing; x = bounds.minX; line = 0 }
            s.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            line = max(line, size.height)
        }
    }

    /// Its ideal size, or the line's width if that is narrower (a subview that can shrink, like `Chip(shrinks:)`).
    private static func size(_ s: LayoutSubview, within width: CGFloat) -> CGSize {
        let ideal = s.sizeThatFits(.unspecified)
        return ideal.width <= width ? ideal : s.sizeThatFits(ProposedViewSize(width: width, height: nil))
    }
}

/// `ViewThatFits(in: .horizontal)` for candidates of one line, measured once: their ideal sizes are kept in the layout's
/// cache (made again when a candidate changes), and a new width only picks among them. On the Mac every frame of a
/// window resize offered the chat's header and work lines a new width, and `ViewThatFits` measured all its candidates
/// again each time: with the composer and the list that was a whole frame over budget (#1337). Shown: the first whose
/// ideal width fits, else the last at the offered width, like `ViewThatFits`; the others are placed out of sight.
/// They are still in the accessibility tree, 100 000 pt up, where a UI test or VoiceOver finds them and cannot reach them
/// (#2181): give it `.firstFittingText(_:)`, one element in the shown one's place.
struct FirstFitting: Layout {
    struct Cache { var ideal: [CGSize] }

    func makeCache(subviews: Subviews) -> Cache { Cache(ideal: subviews.map { $0.sizeThatFits(.unspecified) }) }
    func updateCache(_ cache: inout Cache, subviews: Subviews) { cache = makeCache(subviews: subviews) }

    /// Which candidate a width shows: the first whose ideal width fits (the first when the width is open), else the last.
    static func pick(_ ideal: [CGFloat], width: CGFloat?) -> Int {
        guard let width, width.isFinite else { return 0 }
        return ideal.firstIndex { $0 <= width } ?? max(0, ideal.count - 1)
    }

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout Cache) -> CGSize {
        guard !subviews.isEmpty else { return .zero }
        let i = Self.pick(cache.ideal.map(\.width), width: proposal.width)
        if let w = proposal.width, cache.ideal[i].width > w { return subviews[i].sizeThatFits(proposal) } // the last, cut short
        return cache.ideal[i]
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout Cache) {
        let shown = Self.pick(cache.ideal.map(\.width), width: proposal.width ?? bounds.width)
        for (i, s) in subviews.enumerated() {
            if i == shown { s.place(at: bounds.origin, proposal: ProposedViewSize(bounds.size)) }
            else { s.place(at: CGPoint(x: bounds.minX, y: bounds.minY - Self.away), proposal: .unspecified) }
        }
    }

    /// How far above its place a candidate not shown goes: off any window.
    private static let away: CGFloat = 100_000
}

extension View {
    /// A `FirstFitting`'s candidates as one static text that says `text`, where the shown one is drawn.
    func firstFittingText(_ text: String) -> some View {
        accessibilityElement(children: .ignore).accessibilityLabel(text).accessibilityAddTraits(.isStaticText)
    }
}
