import BeautifulUI
import SwiftUI

// Components of the ChatGPT-style shell (docs/apple/ios.md「New shell」). Neutral in every style (Theme.Shell): the shell is laid
// out like ChatGPT's, the styles still color the rest of the app.

/// The top switch: Assistant | Agents in a glass capsule, the selected one on a soft gray pill that glides across. Agents carries
/// the count of agents waiting on the user. Tapping Assistant while it is selected opens `chatMenu` (the conversation's
/// menu: Knowledge, Details, the launcher…), like tapping the title in ChatGPT; tapping Agents while it is selected opens
/// the list's options (Group By, Display, Machines…).
///
/// In the shell the selection is the shell's (`ShellState.page`, docs/apple/shell.md), not `tab`: a tap selects at once, and a
/// segment is its page's menu only while the strip rests on that page, so a segment never stands for a page that is
/// not on screen (a tab that said Agents over Assistant made Agents the menu of an empty list of options, 2026-11-02).
///
/// The switch reads nothing that the hub's pushes change: an open menu is laid out again whenever its button is, and
/// went back to its top under the finger choosing a machine (TestFlight 70, docs/lessons/ios.md). The count is
/// `WaitingBadge`, beside the menu rather than in its label, and reads the agents itself.
struct ShellSwitch<ChatMenu: View, WorkMenu: View>: View {
    @Binding var tab: AppTab
    /// Across the top bar, or down the side rail on iPhone Duo (`ShellSideRail`): a segment there is the page's symbol over
    /// its name, as a tab bar's item on the side.
    var axis: Axis = .horizontal
    @ViewBuilder var chatMenu: () -> ChatMenu
    @ViewBuilder var workMenu: () -> WorkMenu
    @Namespace private var pill
    /// The strip under the switch: its pill follows the pages while they slide (`ShellPager`).
    @Environment(\.shell) private var shell
    /// In the moving bar (`ShellMovingBar`): the pill glides with the strip. The pages' own bars are transparent while
    /// it moves, so theirs sits on the page in front and does not read the strip at all (`PagerPill`).
    @Environment(\.shellPillFollowsStrip) private var follows
    @Environment(\.buiTheme) private var theme
    /// A phone's tabs (iPhone Duo open, #2623): the switch stands in for the tab bar, Persona's badge with it.
    @Environment(\.pageTabSwitch) private var pageTabSwitch

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("ShellSwitch")
        #endif
        if AppStyle.current.usesBeautifulUI {
            // BeautifulUI's pill segmented control: a field-colored track, the chosen segment a raised capsule.
            row
                .padding(Theme.Space.xs)
                .background(theme.colors.field, in: .capsule)
                .overlay(Capsule().strokeBorder(theme.colors.line, lineWidth: Theme.Size.hairline))
                .animation(Theme.Motion.state, value: chosen)
        } else {
            row
                .padding(Theme.Space.xs)
                .surface(interactive: true, in: .capsule)
                .animation(Theme.Motion.state, value: chosen)
        }
    }

    /// The page the switch shows as chosen: the shell's, or `tab` outside it.
    private var chosen: AppTab {
        guard let shell else { return tab }
        return ShellState.tab(for: shell.page)
    }

    /// Whether the chosen segment opens its menu: not while the strip slides (a tap then picks a page). Only `moving`,
    /// not where the strip is: `pager.page` changes in the slide's animated transaction, and a switch reading it took
    /// that animation into a bar a pushed page had taken out of the window, so the slide never ended (`ShellPager.settle`).
    /// At rest the strip is on the shell's page anyway.
    private func opensMenu(_ value: AppTab) -> Bool {
        guard chosen == value else { return false }
        // No menu to open (the switch over a phone's tabs, iPhone Duo open, #3680): the chosen segment is a plain button,
        // not an empty menu that VoiceOver called List Options.
        if value == .chats, WorkMenu.self == EmptyView.self { return false }
        if value == .assistant, ChatMenu.self == EmptyView.self { return false }
        guard let pager = shell?.pager else { return true }
        return !pager.moving
    }

    private var row: some View {
        stack {
            ZStack(alignment: .topTrailing) {
                if opensMenu(.assistant) {
                    Menu { chatMenu() } label: { segment(String(localized: "shell.assistant", defaultValue: "Assistant", comment: "The chat side of the top switch (72 pt wide)"), .assistant, selected: true) }
                        .menuStyle(.button)
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("shell-assistant")
                        .accessibilityHint("Chat menu")
                } else {
                    button(String(localized: "shell.assistant", defaultValue: "Assistant", comment: "The chat side of the top switch (72 pt wide)"), .assistant, id: "shell-assistant")
                }
                if pageTabSwitch { AssistantWaitingBadge() }
            }
            ZStack(alignment: .topTrailing) {
                if opensMenu(.chats) {
                    Menu { workMenu() } label: { segment("Agents", .chats, selected: true) }
                        .menuStyle(.button)
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("shell-agents")
                        .accessibilityHint("List options")
                } else {
                    button("Agents", .chats, id: "shell-agents")
                }
                WaitingBadge(axis: axis)
            }
            #if os(iOS) || os(visionOS)
            if LarkHomePreference.shared.shows {
                button(String(localized: "Messages [Quote Views 4da199]"), .feishu, id: "shell-feishu")
            }
            #endif
        }
        .background(alignment: axis == .vertical ? .top : .leading) {
            if let shell { PagerPill(shell: shell, follows: follows, axis: axis) }
        }
    }

    /// The segments side by side, or one above the other on the side rail.
    @ViewBuilder private func stack<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        if axis == .vertical { VStack(spacing: .zero, content: content) } else { HStack(spacing: .zero, content: content) }
    }

    private func button(_ title: String, _ value: AppTab, id: String) -> some View {
        Button {
            #if DEBUG
            ShellTrace.shared.note("switch \(value) chosen=\(chosen)")
            #endif
            if let shell {
                // Also while the strip slides: the last tap is where it ends.
                if chosen != value { Haptics.tap() }
                shell.select(ShellState.index(for: value))
            } else if tab != value {
                Haptics.tap()
                tab = value
            }
        } label: {
            segment(title, value, selected: chosen == value)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(id)
        .accessibilityAddTraits(chosen == value ? .isSelected : [])
    }

    @ViewBuilder private func segment(_ title: String, _ value: AppTab, selected: Bool) -> some View {
        if axis == .vertical {
            railSegment(title, symbol: Self.symbol(value), selected: selected)
        } else if AppStyle.current.usesBeautifulUI {
            beautifulSegment(title, selected: selected)
        } else {
            plainSegment(title, selected: selected)
        }
    }

    /// The page's symbol on the rail, matching its shared tab.
    private static func symbol(_ value: AppTab) -> String {
        #if os(iOS) || os(visionOS)
        if value == .feishu { return Theme.Icon.feishuChats } // a phone's tab only (no such tab on the Mac)
        #endif
        return value == .chats ? Theme.Icon.agents : Theme.Icon.ai
    }

    /// A segment of the side rail's switch: the page's symbol over its name, square, the pill (`PagerPill`) under the
    /// chosen one.
    private func railSegment(_ title: String, symbol: String, selected: Bool) -> some View {
        VStack(spacing: Theme.Space.xxs) {
            ThemedIcon(symbol).font(Theme.Font.heading)
            Text(title).font(Theme.Font.caption).lineLimit(1).minimumScaleFactor(0.7)
        }
        .foregroundStyle(selected ? Color.primary : Color.secondary)
        .scaledFrame(width: Theme.Shell.railSegment, height: Theme.Shell.railSegment)
        .contentShape(.rect(cornerRadius: Theme.Radius.card))
    }

    private func beautifulSegment(_ title: String, selected: Bool) -> some View {
        Text(title)
            .buiFont(BUITextStyle(size: 13, weight: selected ? .semibold : .medium, lineHeight: 1.19))
            .foregroundStyle(selected ? theme.colors.ink : theme.colors.ink3)
            .lineLimit(1)
            .minimumScaleFactor(0.8)
            .scaledFrame(width: Theme.Shell.segmentWidth, height: Theme.Shell.switchHeight - Theme.Space.s)
            .background {
                if selected, shell == nil {
                    Color.clear.buiSurface(Capsule(), fill: theme.colors.surface, elevation: .button)
                        .matchedGeometryEffect(id: "pill", in: pill)
                }
            }
            .contentShape(.capsule)
    }

    private func plainSegment(_ title: String, selected: Bool) -> some View {
        Text(title)
            .font(selected ? Theme.Font.bodyStrong : Theme.Font.body)
            .foregroundStyle(selected ? Color.primary : Color.secondary)
            .lineLimit(1)
            .minimumScaleFactor(0.8)
            .scaledFrame(width: Theme.Shell.segmentWidth, height: Theme.Shell.switchHeight - Theme.Space.s)
            .background {
                if selected, shell == nil {
                    Capsule().fill(Theme.Shell.segment).matchedGeometryEffect(id: "pill", in: pill)
                }
            }
            .contentShape(.capsule)
    }
}

/// The switch's pill in the shell: under Assistant or Agents as the strip's position says, gliding with the finger. Its own
/// view: only it reads the position, the switch (and its menus) are not drawn again on every frame of a drag.
///
/// Only the moving bar's pill follows the strip. A page's bar can be out of the window (a page pushed over it, even
/// one pushed while the strip still slides), and a view there that took the slide's animation never runs it: the
/// slide's end waited for it, both pages out of reach and the moving bar's switch over the chat (2026-11-03, opening an
/// agent from the inbox, `ShellInboxAgentTests`).
private struct PagerPill: View {
    let shell: ShellState
    var follows: Bool
    /// Along the switch: across it, or down it on the side rail.
    var axis: Axis = .horizontal
    @Environment(\.buiTheme) private var theme

    var body: some View {
        GeometryReader { g in
            let n = CGFloat(shell.pager.pageCount)
            let at = follows ? min(n - 1, max(0, shell.pager.position)) : CGFloat(shell.page)
            let down = axis == .vertical
            let size = down ? CGSize(width: g.size.width, height: g.size.height / n) : CGSize(width: g.size.width / n, height: g.size.height)
            let offset = down ? CGSize(width: 0, height: at * size.height) : CGSize(width: at * size.width, height: 0)
            if AppStyle.current.usesBeautifulUI {
                // BeautifulUI's segmented control: the chosen segment is a raised capsule.
                Color.clear.buiSurface(Capsule(), fill: theme.colors.surface, elevation: .button)
                    .frame(width: size.width, height: size.height)
                    .offset(offset)
            } else {
                Capsule().fill(Theme.Shell.segment)
                    .frame(width: size.width, height: size.height)
                    .offset(offset)
            }
        }
    }
}

/// How many agents wait on the user, over the switch's Agents. Its own view: the count changes with nearly every push
/// (an agent asks, another is answered), and only this badge is drawn again.
private struct WaitingBadge: View {
    @Environment(AppModel.self) private var model
    var axis: Axis = .horizontal
    private var offset: CGSize {
        // On the side rail, inside the segment's corner: past it is the screen's edge.
        if axis == .vertical { return CGSize(width: -Theme.Space.xs, height: Theme.Space.xs) }
        #if os(iOS) || os(visionOS)
        if LarkHomePreference.shared.shows { return CGSize(width: -Theme.Space.s, height: -Theme.Space.l) }
        #endif
        return CGSize(width: Theme.Space.s, height: -Theme.Space.sm)
    }

    var body: some View {
        let waiting = model.agentsWaiting
        if AppStyle.current.usesBeautifulUI, let text = CountBadge.text(waiting) {
            BUIPill(text, tone: .custom(ink: StatusPalette.needsYou.text, fill: StatusPalette.needsYou.tint))
                .offset(offset)
                .allowsHitTesting(false)
                .accessibilityLabel(StateName.count(.needsYou, waiting))
        } else if waiting > 0 {
            CountBadge(count: waiting, look: .needs, label: StateName.count(.needsYou, waiting))
                .offset(offset)
                .allowsHitTesting(false)
        }
    }
}

/// Persona's own badge on the switch where it stands in for the tab bar (iPhone Duo open, #2623): what the Persona tab
/// counted (`AppModel.badges`), read here as `WaitingBadge` reads the agents, so the switch is not built again for it.
private struct AssistantWaitingBadge: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        let waiting = model.badges.assistant
        if waiting > 0 {
            CountBadge(count: waiting, look: .needs, label: StateName.count(.needsYou, waiting))
                .offset(x: Theme.Space.s, y: -Theme.Space.sm)
                .allowsHitTesting(false)
        }
    }
}

/// A round glass button of the shell's top bar (New Chat, New Session): the same size as the system toolbar's, so the Assistant
/// page's floating bar and the Agents page's navigation bar line up.
struct ShellCircleButton: View {
    var symbol: String
    var label: String
    var id: String
    var action: () -> Void
    @Environment(\.buiTheme) private var theme

    var body: some View {
        Button {
            Haptics.tap()
            action()
        } label: {
            ThemedIcon(symbol)
                .font(Theme.Font.heading)
                .fontWeight(.medium) // a touch heavier and rounder, like ChatGPT's line icons
                .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
                .foregroundStyle(AppStyle.current.usesBeautifulUI ? theme.colors.ink : Color.primary)
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .modifier(CircleSurface())
        .accessibilityLabel(label)
        .accessibilityIdentifier(id)
    }
}

/// The top bar's left button on both pages (2026-11-01 user request, in place of ☰): the persona's face in Persona's
/// state, shared with the platform bar's centered `PersonaDot` (docs/apple/persona.md「States」), opening the drawer
/// (Chat History, Inbox…). The state is that of Persona's conversation on Assistant (`assistantCurrent`), on Agents too, so the
/// face stays the same while the strip slides. While the inbox has something for the user a small soft red dot sits
/// on the face's lower right (2026-11-04 user request, in place of a red tray beside it: smaller, paler, overlapping
/// the face so it takes no room and does not draw the eye), inside the round face so nothing reaches past it (it once
/// hung below the frame over Agents' search field), away from the corner the packages draw in (the thought bubble and
/// the z are top right). A ring in the page's color sets it apart from the face. The button reads all of it itself:
/// the bars around it are not drawn again with each push (#178).
struct ShellPersonaButton: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dynamicTypeSize) private var typeSize
    var id: String
    var action: () -> Void

    /// How far up and left of the face frame's lower right corner the dot goes so that it lies on the face's rim at
    /// 45°, just inside the round face: its center `face / 2 - dot / 2` from the face's, down and right.
    nonisolated static func dotShift(face: CGFloat, dot: CGFloat) -> CGFloat {
        let r = face / 2, d = dot / 2
        let along = (r - d) / 2.squareRoot() // the dot's center right of (and below) the face's
        return along - (r - d) // from bottomTrailing, where the dot's center is r - d right of the face's; negative
    }

    var body: some View {
        let key = model.assistantCurrent
        let state = model.personaState(key, machineID: model.assistantMachineID(key))
        let inbox = InboxStore.shared.actionCount > 0
        // As big as the bar's round buttons (ShellCircleButton's frame): the tap target, growing with the text size.
        let k = Theme.sizeScale(typeSize)
        let dot = Theme.Size.dotXL * k
        Button {
            Haptics.tap()
            action()
        } label: {
            PersonaDot(size: Theme.Size.tapTarget * k, state: state)
                .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .overlay(alignment: .bottomTrailing) {
            if inbox {
                let shift = Self.dotShift(face: Theme.Size.tapTarget * k, dot: dot)
                InboxDot(size: dot).offset(x: shift, y: shift)
            }
        }
        .accessibilityLabel("Chat History [Shell Components 60636b]")
        .accessibilityValue([String(localized: "\(model.persona.name), \(state.title)"), inbox ? String(localized: "Something in the Inbox needs you") : nil]
            .compactMap { $0 }.joined(separator: String(localized: ", ", comment: "List separator")))
        .accessibilityHint("Opens the sidebar")
        .accessibilityIdentifier(id)
    }
}

/// The inbox's dot on the persona's face: soft red, no number or symbol, ringed inside its size in the page's color so
/// it stands apart from the face without growing past it.
private struct InboxDot: View {
    var size: CGFloat

    var body: some View {
        Circle()
            .fill(Theme.inboxDot)
            .overlay(Circle().strokeBorder(Theme.pageBackground, lineWidth: Theme.Size.lineMedium))
            .frame(width: size, height: size)
            .allowsHitTesting(false)
            .accessibilityHidden(true)
    }
}

/// A round button's surface: glass, or in the BeautifulUI style its raised white circle.
private struct CircleSurface: ViewModifier {
    func body(content: Content) -> some View { content.chromeSurface(in: Circle()) }
}

extension View {
    /// The floating chrome's button surface (the shell's round buttons, the image viewer's bars): interactive glass,
    /// or BeautifulUI's raised button in that style.
    @ViewBuilder func chromeSurface<S: InsettableShape>(in shape: S) -> some View {
        if AppStyle.current.usesBeautifulUI {
            buiSurface(shape, elevation: .button)
        } else {
            surface(interactive: true, in: shape)
        }
    }
}

extension View {
    /// The shell's chrome a size up (docs/apple/ios.md「New shell」): one Dynamic Type step, about 12 %, so its text, icons, buttons
    /// and the composer grow together and keep their proportions, ChatGPT's roomier scale. The conversation's messages
    /// keep the reading size. Small / Standard / Large and the system's size still apply under it.
    func shellScaled() -> some View { modifier(ShellScaled()) }

    /// A top bar of the shell: a size up like the rest of its chrome, but no larger than
    /// `Theme.Shell.topBarLargestTypeSize`, so it stays clear of Agents' search field at the accessibility sizes.
    func shellTopBarScaled() -> some View { dynamicTypeSize(...Theme.Shell.topBarLargestTypeSize).shellScaled() }
}

/// Room for the shell's floating top bar (`AgentsTopBar`) on a page that keeps the system navigation bar for its search
/// field (Agents): a blank prompt line on the page's own navigation item, which makes that page's navigation bar taller
/// by the line, so the search field the bar holds and the list under it start below the top bar. While searching the
/// line goes, and the field rises into the top row.
///
/// The room is the page's, not the navigation container's: it used to be padding around the container, taken away for
/// a chat pushed on the list and given back on the way back, and UIKit does not lay out its navigation bar again in the
/// middle of a push or pop. Coming back, the bar sat where it had been for the chat (52 pt low), the list's inset with
/// it, which unfolded the search field, and everything sprang up once the pop was over (#340). A page's own navigation
/// item is carried through the transition by UIKit itself; the container keeps its frame and its safe area (which the
/// shell's status row changes, ShellStatus).
///
/// The line's height is UIKit's, the same at every text size; the top bar stops growing at
/// `Theme.Shell.topBarLargestTypeSize` to stay within it (`needed(at:)`, ShellNavigationClearanceTests).
struct ShellNavigationClearance: View {
    var active: Bool

    /// How far below the status bar the search field must start at this text size: the top bar's height (its buttons
    /// a size up, no larger than the top bars' largest; the inbox mark hanging off the face; the bar's top padding) and
    /// a step of space. The navigation bar with the prompt line must be at least this tall.
    static func needed(at size: DynamicTypeSize) -> CGFloat {
        let all = DynamicTypeSize.allCases
        let up = all[min((all.firstIndex(of: size) ?? 0) + 1, all.count - 1)]
        let k = Theme.sizeScale(min(up, Theme.Shell.topBarLargestTypeSize))
        return Theme.Size.tapTarget * k + Theme.Size.badge * k / 2 + Theme.Space.xxs * 2 + Theme.Space.s
    }

    /// The blank prompt line.
    static let line = " "

    var body: some View {
        #if os(iOS) || os(visionOS)
        PromptLine(active: active)
        #endif
    }
}

#if os(iOS) || os(visionOS)
private struct PromptLine: UIViewControllerRepresentable {
    var active: Bool

    func makeUIViewController(context: Context) -> Line { Line() }
    func updateUIViewController(_ line: Line, context: Context) { line.active = active }

    /// A child of the page's controller (the representable's), which owns the navigation item.
    final class Line: UIViewController {
        var active = false { didSet { apply() } }

        override func loadView() {
            view = UIView()
            view.isUserInteractionEnabled = false
        }

        override func didMove(toParent parent: UIViewController?) {
            super.didMove(toParent: parent)
            apply()
        }

        private func apply() {
            guard let item = parent?.navigationItem else { return }
            let prompt = active ? ShellNavigationClearance.line : nil
            if item.prompt != prompt { item.prompt = prompt }
        }
    }
}
#endif

/// The text size the app settled on (the system's, moved by Small / Standard / Large), set once at the root. `shellScaled` goes one
/// step up from this, not from the environment's own size: that one is not always what the page has. The drawer's
/// sidebar lives in a hosting controller whose environment follows its UIKit trait collection, which takes up the
/// step set inside it, so reading the environment there added a step on every refresh (Inbox's row 59 → 78 → 80 pt).
private struct ShellBaseTypeSizeKey: EnvironmentKey {
    static let defaultValue: DynamicTypeSize? = nil
}

extension EnvironmentValues {
    var shellBaseTypeSize: DynamicTypeSize? {
        get { self[ShellBaseTypeSizeKey.self] }
        set { self[ShellBaseTypeSizeKey.self] = newValue }
    }
}

private struct ShellScaled: ViewModifier {
    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.shellBaseTypeSize) private var base

    func body(content: Content) -> some View {
        let all = DynamicTypeSize.allCases
        let i = all.firstIndex(of: base ?? size) ?? 0
        content.dynamicTypeSize(all[min(i + 1, all.count - 1)])
    }
}

extension ShellSwitch where WorkMenu == EmptyView {
    init(tab: Binding<AppTab>, axis: Axis = .horizontal, @ViewBuilder chatMenu: @escaping () -> ChatMenu) {
        self.init(tab: tab, axis: axis, chatMenu: chatMenu, workMenu: { EmptyView() })
    }
}
