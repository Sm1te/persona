import BeautifulUI
import SwiftUI

extension EnvironmentValues {
    /// Opens a space's page (its agents) from a chat; set on the dev tab's NavigationStack.
    @Entry var openSpace: ((String) -> Void)? = nil
}

/// The space's tabs (its agents) in the chat's header, one capsule each in its state's color (no dots: the pulsing ones
/// were too busy in a row of them; kept over a neutral fill at the user's call, docs/archive/apple/plan-chat-topbar.md Q2), the
/// current one bold with a ring. The agents of one tab sit together in the order of their
/// panes, each named by its agent, the starter marked "Starter" (#2320). A name is one line, cut at `Theme.Size.tabName`,
/// so one long name cannot push the current tab away. As many as the width holds show around the current one; a swipe
/// scrolls through the rest, and an end fades while there is more that way. The space's mark and the tab count at the
/// end (not ⋯: that is the page's menu) lists every tab with its state, and the space's page, for a quick pick.
struct SiblingTabs: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openSpace) private var openSpace
    var current: SessionKey
    var select: (SessionKey) -> Void
    var closed: (SessionKey, [SessionKey]) -> Void = { _, _ in }
    /// The strip's own scroll position (not ScrollViewReader: its scrollTo drags the page above along).
    @State private var shown: SessionKey?
    @State private var showAll = false
    @State private var closing: Session?
    @State private var stopping: SessionKey?
    @State private var closeOrder: [SessionKey] = []

    var body: some View {
        let siblings = model.siblings(of: current)
        HStack(spacing: Theme.Space.sm) {
            if !siblings.isEmpty {
                strip(siblings)
                allButton(siblings)
            }
        }
        .confirmClose($closing) { key in closed(key, closeOrder) }
        .sheet(isPresented: $showAll) {
            SiblingList(current: current, space: model.session(current)) { key in
                showAll = false
                if key != current { select(key) }
            } openSpace: { space in
                showAll = false
                openSpace?(space)
            } closed: { key, order in
                if key == current { showAll = false }
                closed(key, order)
            }
            .presentationDetents([.medium, .large])
            .presentationDragIndicator(.visible)
            #if os(macOS)
            // A Mac sheet ignores detents and opens at its content's minimum size: the list came up as its header and
            // one row (#906).
            .macSheet()
            #endif
        }
    }

    private func requestClose(_ session: Session) {
        guard stopping == nil, let latest = model.latest(session.key), latest.canTerminal else { return }
        closeOrder = model.siblings(of: current).map(\.key)
        if AgentCloseRules.needsConfirmation(latest) {
            closing = latest
        } else {
            stopping = latest.key
            Task {
                if await model.close(latest.key) { closed(latest.key, closeOrder) }
                stopping = nil
            }
        }
    }

    private func strip(_ siblings: [Session]) -> some View {
        let roles = TabPanes.roles(siblings)
        return ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: Theme.Space.sm) {
                ForEach(siblings) { s in tab(s, role: roles[s.key]) }
            }
            .scrollTargetLayout()
            .padding(.horizontal, Theme.Space.xxs)
        }
        .scrollTargetBehavior(.viewAligned)
        // A space with many tabs opens on the current one and its neighbours, not the first few.
        .scrollPosition(id: $shown, anchor: .center)
        .onAppear { shown = current }
        .onChange(of: current) { _, k in withAnimation(Theme.Motion.standard) { shown = k } }
        .edgeFade()
        .frame(maxWidth: .infinity)
    }

    private func tab(_ s: Session, role: TabPanes.Role?) -> some View {
        let on = s.key == current
        let tone = s.stateTone
        let quiet = s.state == .idle || s.state == .unknown || s.state == .offline
        let label = TabPanes.tabLabel(s, role: role)
        let launcher = role?.launcher == true
        return Group {
            if AppStyle.current.usesBeautifulUI {
                // Its filter chip: the state's dot (none for a quiet, seen or idle one) and the tab's name; the current
                // one is the raised capsule. The chip sizes itself (`fixedSize`), so its name is cut here, not by a frame.
                let name = Self.capped(label)
                BUIFilterChip(launcher ? String(localized: "\(name) · Starter", comment: "Chip of the agent that started the tab") : name, dot: quiet ? nil : tone.dot, isActive: on) {
                    if !on { Haptics.tap(); select(s.key) }
                }
            } else {
                plainTab(s, label: label, launcher: launcher, on: on, tone: tone, quiet: quiet)
            }
        }
        .accessibilityLabel(String(localized: "\(label)\(launcher ? String(localized: " (starter)") : ""), \(s.stateLabel(muted: model.isMuted(s)))"))
        .accessibilityAddTraits(on ? .isSelected : [])
        .accessibilityIdentifier("chat.sibling")
        .contextMenu {
            if s.canTerminal {
                Button(role: .destructive) { requestClose(s) } label: {
                    Label("Stop & Close Agent", systemImage: Theme.Icon.end).lineLimit(1)
                }
                .disabled(stopping != nil)
            }
        }
        .disabled(stopping == s.key)
        .id(s.key)
    }

    private func plainTab(_ s: Session, label: String, launcher: Bool, on: Bool, tone: StatusTone, quiet: Bool) -> some View {
        Button { if !on { Haptics.tap(); select(s.key) } } label: {
            HStack(spacing: Theme.Space.xs) {
                Text(label)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .font(on ? Theme.Font.labelHeavy : Theme.Font.labelMedium)
                    // A width cap that holds in the strip's open offer, where a flexible frame would not (#3010).
                    .scaledWidthCap(Theme.Size.tabName)
                // The starter's mark, quieter than the name (the web's "Starter").
                if launcher { Text("Starter").font(Theme.Font.caption).opacity(0.75).lineLimit(1).fixedSize() }
            }
                .padding(.horizontal, Theme.Space.m)
                .scaledFrame(height: Theme.Size.wellS)
                // The state is the capsule's color; a quiet (seen, idle) one is the plain gray.
                .background(quiet ? AnyShapeStyle(Theme.Fill.chip) : AnyShapeStyle(tone.dot.opacity(on ? 0.26 : 0.16)), in: .capsule)
                .overlay(Capsule().strokeBorder(on ? (quiet ? Color.primary.opacity(0.45) : tone.dot.opacity(0.8)) : .clear, lineWidth: Theme.Size.lineMedium))
                .foregroundStyle(quiet ? Color.primary : tone.text)
                .contentShape(.capsule)
        }
        .buttonStyle(.plain)
    }

    /// A tab's name for BeautifulUI's chip, which cannot be capped from outside: about `Theme.Size.tabName` at its
    /// 12 pt, counting a wide (CJK) character as two narrow ones; the full name is in the VoiceOver label and the list.
    nonisolated static func capped(_ name: String, budget: Int = 20) -> String {
        var used = 0
        var out = ""
        for c in name {
            let w = c.unicodeScalars.contains { $0.properties.isIdeographic || (0xAC00...0xD7A3).contains($0.value) || (0x3040...0x30FF).contains($0.value) } ? 2 : 1
            if used + w > budget { return out.trimmingCharacters(in: .whitespaces) + "…" }
            used += w
            out.append(c)
        }
        return out
    }

    /// The space's mark and how many tabs it has: a sheet with every tab, its state and the space's page. Not ⋯, which
    /// is the page's menu in the same header.
    private func allButton(_ siblings: [Session]) -> some View {
        let waiting = siblings.contains { $0.key != current && $0.state == .needsYou }
        return Button { Haptics.tap(); showAll = true } label: {
            HStack(spacing: Theme.Space.xs) {
                ThemedIcon(Theme.Icon.space)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(.secondary)
                Text("\(siblings.count)")
                    .font(Theme.Font.labelStrong.monospacedDigit())
                    .foregroundStyle(Color.primary)
                    .lineLimit(1)
            }
                .padding(.horizontal, Theme.Space.s)
                .scaledFrame(height: Theme.Size.wellS)
                .background(Theme.Fill.chip, in: .capsule)
                .overlay(alignment: .topTrailing) {
                    if waiting { Circle().fill(Theme.needsYou).scaledFrame(width: Theme.Size.dot, height: Theme.Size.dot).offset(x: 1, y: -1) } // still, not pulsing
                }
                .contentShape(.capsule)
        }
        .buttonStyle(.plain)
        // Its count is never cut: where the row runs out of width (the largest text sizes, with the agent's pills and mode
        // after it, #3719) the strip of tabs before it scrolls in less.
        .fixedSize()
        .accessibilityLabel(waiting ? String(localized: "\(siblings.count) agents in this space, one needs you")
                                    : String(localized: "\(siblings.count) agents in this space [Space View 359a45]"))
        #if DEBUG
        // `-showSpaceTabs YES`: open this sheet (screenshots); `new` opens its new-tab form on top.
        .task { if ["YES", "new"].contains(UserDefaults.standard.string(forKey: "showSpaceTabs")) { try? await Task.sleep(for: .seconds(2)); showAll = true } }
        #endif
    }
}

/// The ⋯ sheet of a space's tabs: the space's page on top, then each tab with its state's dot and words.
private struct SiblingList: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var current: SessionKey
    var space: Session?
    var pick: (SessionKey) -> Void
    var openSpace: (String) -> Void
    var closed: (SessionKey, [SessionKey]) -> Void

    @State private var showNew = false
    @State private var closing: Session?
    @State private var stopping: SessionKey?
    @State private var closeOrder: [SessionKey] = []
    @State private var failure: String?

    private var siblings: [Session] { model.siblings(of: current) }

    private func requestClose(_ session: Session) {
        guard stopping == nil, let latest = model.latest(session.key), latest.canTerminal else { return }
        closeOrder = siblings.map(\.key)
        failure = nil
        if AgentCloseRules.needsConfirmation(latest) {
            closing = latest
        } else {
            stopping = latest.key
            Task {
                if await model.close(latest.key) {
                    closed(latest.key, closeOrder)
                } else {
                    failure = model.toast?.message ?? String(localized: "Couldn't stop it. The tab is still open; try again.")
                }
                stopping = nil
            }
        }
    }

    /// Where a new tab goes: the space this chat is in (its machine, directory and herdr workspace).
    private var preset: SpacePreset? {
        guard let s = space, let ws = s.spaceID else { return nil }
        return SpacePreset(machineID: s.machineRef.path, cwd: s.cwd, workspaceID: ws, spaceName: s.spaceName ?? "", host: s.hostKind ?? s.spaceKind)
    }

    var body: some View {
        List {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: Theme.Space.s) {
                    Text("\(siblings.count) agents").font(Theme.Font.heading).lineLimit(1)
                    Spacer(minLength: Theme.Space.s)
                    if preset != nil {
                        Button { Haptics.tap(); showNew = true } label: {
                            Label("New Tab", systemImage: Theme.Icon.add).font(Theme.Font.secondaryStrong).lineLimit(1)
                        }
                        .themedButton()
                        .accessibilityLabel("Open a new tab in this space")
                    }
                    #if os(macOS)
                    // A Mac sheet has no swipe down or tap outside to close it; Esc presses this.
                    Button("Done [Extension System Presentation c0b3fb]") { dismiss() }.lineLimit(1).keyboardShortcut(.cancelAction)
                    #endif
                }
                .padding(.horizontal, Theme.Space.l).padding(.top, Theme.Space.l).padding(.bottom, Theme.Space.s)
                if let failure {
                    Text(failure)
                        .font(Theme.Font.secondary)
                        .foregroundStyle(Theme.danger)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, Theme.Space.l)
                        .padding(.vertical, Theme.Space.s)
                        .accessibilityIdentifier("agent.tabs.close.error")
                }
                if let s = space, let name = s.spaceName, let k = s.spaceKey {
                    Button { openSpace(k) } label: {
                        HStack(spacing: Theme.Space.m) {
                            ThemedIcon(Theme.Icon.space).foregroundStyle(Theme.accent).scaledFrame(width: Theme.Size.iconS)
                            Text("All agents in space \(name)").font(Theme.Font.secondaryStrong).lineLimit(1)
                            Spacer(minLength: 8)
                            ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                        }
                        .padding(.horizontal, Theme.Space.l)
                        .scaledFrame(minHeight: Theme.Size.avatarL)
                        .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    Divider().padding(.leading, Theme.Space.l)
                }
            }
            .listRowInsets(EdgeInsets())
            .listRowSeparator(.hidden)
            .listRowBackground(Color.clear)
            let roles = TabPanes.roles(siblings)
            ForEach(siblings) { s in
                    let on = s.key == current
                    let role = roles[s.key]
                    let label = TabPanes.tabLabel(s, role: role)
                    Button { pick(s.key) } label: {
                        HStack(spacing: Theme.Space.m) {
                            Circle().fill(s.stateTone.dot).scaledFrame(width: Theme.Size.dotL, height: Theme.Size.dotL).scaledFrame(width: Theme.Size.iconS) // a still dot
                            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                Text(label).font(on ? Theme.Font.secondaryStrong : Theme.Font.secondary).lineLimit(1)
                                // The state in its color, then the model the tab runs on (2026-10-31: which tab is which model).
                                (Text(s.stateLabel(muted: model.isMuted(s))).foregroundStyle(s.stateTone(muted: model.isMuted(s)).text)
                                    + Text(role?.launcher == true ? String(localized: " · Started It") : "").foregroundStyle(.secondary)
                                    + Text(Fmt.model(s.model).map { " · " + $0 } ?? "").foregroundStyle(.secondary))
                                    .font(Theme.Font.label).lineLimit(1).truncationMode(.tail)
                            }
                            Spacer(minLength: 8)
                            if stopping == s.key { ProgressView().tint(Theme.danger) }
                            else if on { ThemedIcon(Theme.Icon.done).font(Theme.Font.captionStrong).foregroundStyle(Theme.accent) }
                        }
                        .padding(.horizontal, Theme.Space.l)
                        // A tab's other agents hang a level in under its starter.
                        .padding(.leading, role?.branch == true ? Theme.Space.l : 0)
                        .scaledFrame(minHeight: Theme.Size.box50)
                        .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(String(localized: "\(label)\(role?.launcher == true ? String(localized: " (starter)") : ""), \(s.stateLabel(muted: model.isMuted(s)))") + (Fmt.model(s.model).map { String(localized: ", \($0)") } ?? ""))
                    .accessibilityAddTraits(on ? .isSelected : [])
                    .listRowInsets(EdgeInsets())
                    .listRowSeparator(.hidden)
                    .listRowBackground(Color.clear)
                    .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                        if s.canTerminal {
                            Button { requestClose(s) } label: {
                                Label("Close", systemImage: Theme.Icon.end).lineLimit(1)
                            }
                            .tint(Theme.danger)
                            .accessibilityLabel("Stop and Close \(label)")
                        }
                    }
                    .contextMenu {
                        if s.canTerminal {
                            Button(role: .destructive) { requestClose(s) } label: {
                                Label("Stop & Close Agent", systemImage: Theme.Icon.end).lineLimit(1)
                            }
                        }
                    }
            }
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .scrollBounceBehavior(.basedOnSize)
        .disabled(stopping != nil)
        .interactiveDismissDisabled(stopping != nil)
        .confirmClose($closing) { key in closed(key, closeOrder) }
        // The new tab opens in place of this one, like picking a tab (pick closes this sheet and the form with it).
        .sheet(isPresented: $showNew) {
            if let preset {
                NewChatView(preset: preset) { s in
                    showNew = false
                    pick(s.key)
                }
            }
        }
        #if DEBUG
        // `-showSpaceTabs new`: open the new-tab form too (screenshots).
        .task { if UserDefaults.standard.string(forKey: "showSpaceTabs") == "new" { try? await Task.sleep(for: .seconds(1)); showNew = true } }
        #endif
    }
}

/// A space (herdr workspace) at a glance: every agent with its state and latest line; start another tab in it.
struct SpaceView: View {
    @Environment(AppModel.self) private var model
    var spaceKey: String
    @State private var showNew = false
    @State private var started: SessionKey?
    @State private var renaming: SpaceGroup?

    /// The most urgent first; the agents of one tab together where the first of them is, in the order of their panes.
    private var sessions: [Session] {
        TabPanes.grouped(model.allSessions.filter { $0.spaceKey == spaceKey && $0.state.isLive }
            .sorted { ($0.state.priority, $0.slotIndex ?? 0, $0.sessionID) < ($1.state.priority, $1.slotIndex ?? 0, $1.sessionID) })
    }

    var body: some View {
        let sessions = sessions
        let roles = TabPanes.roles(sessions)
        let first = sessions.first
        ScrollView {
            VStack(alignment: .leading, spacing: Theme.Space.l) {
                if let first {
                    HStack(spacing: Theme.Space.s) {
                        // The machine opens its own list of projects.
                        NavigationLink(value: MachineRoute(first.machineRef)) {
                            HStack(spacing: Theme.Space.sm) {
                                ThemedIcon(model.machine(first.machineRef)?.symbol ?? Theme.Icon.machine)
                                Text(model.machine(first.machineRef)?.title ?? first.machineID).lineLimit(1)
                                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelHeavy)
                            }
                            .fontWeight(.semibold)
                            .foregroundStyle(Theme.accent)
                            .padding(.horizontal, Theme.Space.m)
                            .padding(.vertical, Theme.Space.sm)
                            .background(Theme.accent.opacity(0.1), in: .capsule)
                            .contentShape(.capsule)
                        }
                        .buttonStyle(.plain)
                        .layoutPriority(1)
                        .accessibilityLabel("All projects on \(model.machine(first.machineRef)?.title ?? first.machineID)")
                        Text(Fmt.path(first.cwd, home: model.machine(first.machineRef)?.home)).lineLimit(1).truncationMode(.head)
                    }
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                }
                LazyVGrid(columns: [GridItem(.flexible(), spacing: 12), GridItem(.flexible(), spacing: 12)], spacing: 12) {
                    ForEach(sessions) { s in
                        NavigationLink(value: s.key) { SlotCard(session: s, pending: model.interactions(for: s.key).count, muted: model.isMuted(s), pane: roles[s.key]) }
                            .buttonStyle(.plain)
                    }
                    Button { showNew = true } label: {
                        VStack(spacing: Theme.Space.s) {
                            ThemedIcon(Theme.Icon.add).font(Theme.Font.title)
                            Text("Open a new tab in this space").font(Theme.Font.captionMedium)
                        }
                        .foregroundStyle(Theme.accent)
                        // Same box as a SlotCard (min height + the card's 12pt padding), and it fills its grid row
                        // so it is as tall as the card next to it instead of floating centered and shorter.
                        .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.box150)
                        .padding(Theme.Space.m)
                        .frame(maxHeight: .infinity)
                        .background(Theme.accent.opacity(0.06), in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.accent.opacity(0.3), style: StrokeStyle(lineWidth: 1, dash: [5])))
                    }
                    .buttonStyle(.plain)
                    .disabled(first == nil)
                }
            }
            .padding(Theme.Space.l)
        }
        .themedGroupedPage()
        .navigationTitle(first?.spaceName ?? String(localized: "Space [Space View 3347e0]"))
        .navigationBarTitleDisplayMode(.large)
        .pageToolbar {
            if let g = SpaceGroup.build(sessions).first {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu { SpaceMenuItems(group: g) { renaming = g } } label: {
                        Label("Space Settings", systemImage: model.isMuted(g) ? Theme.Icon.mute : Theme.Icon.more)
                    }
                    .accessibilityLabel("Space actions")
                }
            }
        }
        .sheet(isPresented: $showNew) {
            if let first, let ws = first.spaceID {
                NewChatView(preset: SpacePreset(machineID: first.machineRef.path, cwd: first.cwd, workspaceID: ws, spaceName: first.spaceName ?? "", host: first.hostKind ?? first.spaceKind)) { s in
                    showNew = false
                    started = s.key
                }
            }
        }
        .navigationDestination(item: $started) { key in ChatView(key: key).id(key).splitDetailPage(ownsBack: true) }
        .renameSpaceAlert($renaming)
    }
}

struct SlotCard: View {
    var session: Session
    var pending: Int
    /// Muted (itself or its space): plain Seen, never Awaiting Reply (`Session.awaitsYourReply(muted:)`).
    var muted = false
    /// Its place in a tab of several agents (#2320): the tab's chip names the agent too, the starter marked.
    var pane: TabPanes.Role? = nil

    var body: some View {
        let tone = session.stateTone(muted: muted)
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            // Chips don't shrink: in a narrow column (two cards a row on a phone) a long tab name pushed the card wider
            // than its column and the whole grid off the screen. Drop the agent chip first, then let the name truncate.
            ViewThatFits(in: .horizontal) {
                header(agent: true)
                header(agent: false)
                HStack(spacing: Theme.Space.xs) {
                    Text(tabChip)
                        .font(Theme.Font.labelMedium)
                        .foregroundStyle(tone.text)
                        .lineLimit(1)
                        .padding(.horizontal, Theme.Space.s)
                        .padding(.vertical, 2.5)
                        .background(tone.tint, in: .capsule)
                    Spacer(minLength: 4)
                    StatusDot(state: session.state, size: 9, waiting: session.waitsInBackground)
                }
            }
            Text(session.displayTitle).font(Theme.Font.secondaryStrong).lineLimit(2)
            // The starter says so beside its state: the header's chips are cut first in a narrow card.
            Text(session.stateLabel(muted: muted) + (pane?.launcher == true ? String(localized: " · Started It") : "") + (pending > 0 ? String(localized: " · \(pending) pending") : ""))
                .font(Theme.Font.labelStrong)
                .foregroundStyle(tone.text)
            if let t = session.listPreview(muted: muted) {
                Text(t).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(3)
            }
            Spacer(minLength: 0)
            if let m = Fmt.model(session.model) { Text(m).font(Theme.Font.label).foregroundStyle(.tertiary).lineLimit(1) }
        }
        .scaledFrame(minWidth: 0, maxWidth: .infinity, minHeight: Theme.Size.box150, alignment: .topLeading)
        .card(padding: 12)
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).stroke(session.state == .needsYou ? Theme.needsYou.opacity(0.5) : .clear, lineWidth: 1.5))
    }

    private var tabChip: String { pane.map { TabPanes.tabLabel(session, role: $0) } ?? session.slotLabel ?? "—" }

    private func header(agent: Bool) -> some View {
        HStack {
            Chip(text: tabChip, look: .tone(session.stateTone))
            // Beside its tab mates the tab's chip names the agent already.
            if agent, pane == nil { Chip(text: session.agentLabel, look: .outline) }
            Spacer(minLength: 4)
            StatusDot(state: session.state, size: 9, waiting: session.waitsInBackground)
        }
    }
}
