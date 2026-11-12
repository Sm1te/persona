import BeautifulUI
import PhotosUI
import SwiftUI

/// The tabs of a dev session page: the conversation, its terminal, the project's git state and its files.
enum SessionPane: String, CaseIterable, Identifiable {
    case chat, terminal, git, files
    var id: String { rawValue }

    var title: String {
        switch self {
        case .chat: String(localized: "Chat")
        case .terminal: String(localized: "Terminal")
        case .git: "Git"
        case .files: String(localized: "Files")
        }
    }

    var symbol: String {
        switch self {
        case .chat: Theme.Icon.agentChat
        case .terminal: "terminal"
        case .git: Theme.Icon.gitBranch
        case .files: "folder"
        }
    }
}

/// The Mac's Terminal | Chat (#3183, the web's #2445): the chat and the agent's terminal take turns in the middle of the
/// page. The terminal is mounted the first time it shows and stays mounted (subscribed, same size) while the chat
/// shows, so coming back to it does not connect again.
nonisolated struct TerminalChatSwitch: Equatable {
    private(set) var mounted = false

    /// The pane to show for a pick of the switch (or ⇧⌘J's flip), from `pane`; nil when nothing changes.
    mutating func pick(_ p: SessionPane, from pane: SessionPane, canTerminal: Bool) -> SessionPane? {
        guard p != pane, p == .chat || p == .terminal else { return nil }
        if p == .terminal {
            guard canTerminal else { return nil }
            mounted = true
        }
        return p
    }

    /// ⇧⌘J: the terminal from the chat, Files or Git; the chat from the terminal.
    mutating func toggle(from pane: SessionPane, canTerminal: Bool) -> SessionPane? {
        pick(pane == .terminal ? .chat : .terminal, from: pane, canTerminal: canTerminal)
    }

    /// The pane changed some other way (a link, a launch argument): the terminal mounts once it shows.
    mutating func shown(_ pane: SessionPane) { if pane == .terminal { mounted = true } }

    /// The page switched to another agent of the space: its terminal connects only once it shows, and one that has
    /// none shows its chat. Returns the pane to show.
    mutating func switchedAgent(pane: SessionPane, canTerminal: Bool) -> SessionPane {
        let next = pane == .terminal && !canTerminal ? SessionPane.chat : pane
        if next != .terminal { mounted = false }
        return next
    }
}

/// One row of equal segments under the navigation bar; labels never wrap.
struct SessionPaneBar: View {
    @Binding var pane: SessionPane
    var panes: [SessionPane]
    var badges: [SessionPane: String] = [:]
    /// Terminal comes up from the bottom over the page instead of taking its place (TerminalCover).
    var openTerminal: (() -> Void)?
    @Namespace private var ns

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            ForEach(panes) { p in
                Button {
                    if p == .terminal, let openTerminal { Haptics.tap(); openTerminal(); return }
                    guard p != pane else { return }
                    Haptics.tap()
                    withAnimation(Theme.Motion.state) { pane = p }
                } label: {
                    PaneSegment(pane: p, on: p == pane, badge: badges[p], ns: ns)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(p.title)
                .accessibilityAddTraits(p == pane ? .isSelected : [])
            }
        }
        .padding(Theme.Space.xs)
        .background(Theme.Fill.well, in: .capsule)
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.sm)
    }
}

private struct PaneSegment: View {
    var pane: SessionPane
    var on: Bool
    var badge: String?
    var ns: Namespace.ID

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            ThemedIcon(pane.symbol).font(Theme.Font.labelStrong)
            Text(pane.title).lineLimit(1).minimumScaleFactor(0.8)
            if let badge { badgeView(badge) }
        }
        .font(on ? Theme.Font.captionStrong : Theme.Font.captionMedium)
        .foregroundStyle(on ? Theme.accent : Color.secondary)
        .frame(maxWidth: .infinity)
        .scaledFrame(height: Theme.Size.wellS)
        .background { if on { Capsule().fill(Theme.accent.opacity(0.13)).matchedGeometryEffect(id: "pane", in: ns) } }
        .contentShape(.capsule)
    }

    private func badgeView(_ text: String) -> some View {
        Text(text)
            .font(Theme.Font.labelHeavy.monospacedDigit())
            .padding(.horizontal, Theme.Space.sm)
            .padding(.vertical, 1)
            .background(on ? Theme.accent.opacity(0.18) : Theme.Fill.chip, in: .capsule)
    }
}

// MARK: - Terminal

/// The session's terminal at any time (not only while Claude works), live: the hub streams herdr's frames of the pane
/// (docs/integrations/terminal.md) — colors, cursor, wide characters — or, where it cannot, the colored screen is read once a
/// second. Long rows fold at the phone's width unless No Wrap is on (for TUI boxes). Scrolling up loads the 200 rows
/// before, again and again, back to the start of what herdr keeps (about 1000 lines — older output lives in the chat);
/// each page is found by the lines it must end before, so output arriving meanwhile shifts nothing. Reading history
/// holds the screen still, Jump to Latest lets it follow again. A link that fails never leaves old output looking live: a
/// bar says how old it is and when the next try is, with a button to try now.
///
/// Typing is on the terminal itself: a tap brings the keyboard up, every key goes to herdr as it is pressed (batched
/// while a request is out), the key bar sits over the keyboard. Settings › Input Mode brings back a text field that sends
/// what is written in one go.
struct TerminalPanel: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openURL) private var openURL
    @Environment(\.colorScheme) private var scheme
    let key: SessionKey
    @State private var feed: TerminalFeed
    @State private var input = ""
    /// The key bar's ctrl / alt / shift: they also apply to what is typed (ctrl, then c → ctrl+c).
    @State private var mods = TerminalModifiers()
    /// Everything going to the terminal, in the order it was pressed.
    @State private var stream = KeyStream()
    @AppStorage(TerminalPrefs.wrap) private var wrap = true
    @AppStorage(TerminalPrefs.fontSize) private var fontSize = TerminalPrefs.defaultFontSize
    @AppStorage(TerminalPrefs.font) private var fontID = TerminalFont.jetbrains.rawValue
    @AppStorage(TerminalPrefs.theme) private var themeID = TerminalTheme.defaultID
    @AppStorage(TerminalPrefs.shortcuts) private var shortcutsJSON = ""
    @AppStorage(TerminalPrefs.keys) private var visibleKeys = TerminalPrefs.defaultKeys
    @AppStorage(TerminalPrefs.autoHide) private var autoHideKeys = false
    @AppStorage(TerminalPrefs.inputMode) private var inputMode = "direct"
    @AppStorage(TerminalPrefs.tipsSeen) private var tipsSeen = false
    /// The first-use tips are about touch (double-tap to paste, two-finger swipe, the keyboard button): not on the Mac.
    /// The key bar stays there: its ⚡ is how saved commands are reached.
    #if os(macOS)
    private static let touchTips = false
    #else
    private static let touchTips = true
    #endif
    @AppStorage(TerminalPrefs.size) private var sizeMode = "phone"
    #if os(macOS)
    @AppStorage(TerminalPrefs.mouseMode) private var mouseMode = MacTerminalMouse.Mode.program.rawValue
    #endif
    /// A grid change waiting to go out (the keyboard animates, a pinch goes on: the last size is what counts).
    @State private var gridTask: Task<Void, Never>?
    /// The keyboard is up on the terminal (typing straight in).
    @State private var keyboardUp = false
    @FocusState private var typing: Bool
    @State private var showShortcuts = false
    @State private var showSettings = false
    @State private var suggestions: [String] = []
    @State private var pasting: String?
    /// The size a pinch is at, shown while it changes.
    @State private var pinchHUD: Double?
    /// Files pasted, dropped or picked on the way to the pane's machine (TerminalFiles.swift).
    @State private var uploads = TerminalUploads()
    @State private var pickingPhotos = false
    @State private var photoPicks: [PhotosPickerItem] = []
    @State private var pickingFiles = false
    /// Files are being dragged over the terminal (Mac).
    @State private var dropping = false
    /// Resume Session is on its way (the node opens a tab and waits for Claude, up to a couple of minutes).
    @State private var resuming = false
    /// Switches the page to another session (two-finger swipes); nil where the panel cannot (the full-screen sheet).
    var switchTo: ((SessionKey) -> Void)?
    /// The keyboard came up on the terminal or went down (the page folds its header meanwhile).
    var onKeyboard: ((Bool) -> Void)?
    /// Shown from the bottom over the page (TerminalCover): its header is a slim bar to drag it down by.
    var grab: TerminalGrab?
    /// Kept mounted while another view takes its place (the Mac's Terminal | Chat, #3183): the keyboard comes to it when it
    /// shows and leaves it when it hides; its feed goes on either way. Nil: always on screen while mounted.
    var active: Bool?

    /// Older than this without a fresh screen counts as stale even before anything fails (a request may hang).
    static let staleAfter: TimeInterval = 4

    init(key: SessionKey, switchTo: ((SessionKey) -> Void)? = nil, onKeyboard: ((Bool) -> Void)? = nil, grab: TerminalGrab? = nil,
         active: Bool? = nil) {
        self.key = key
        self.switchTo = switchTo
        self.onKeyboard = onKeyboard
        self.grab = grab
        self.active = active
        _feed = State(initialValue: TerminalFeed(key: key))
    }

    private var theme: TerminalTheme { .named(themeID) }
    private var side: TerminalTheme.Side { theme.side(scheme == .dark) }
    private var direct: Bool { inputMode != "field" }
    private var font: TerminalFont { TerminalFont(rawValue: fontID) ?? .jetbrains }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("TerminalPanel")
        #endif
        VStack(spacing: 0) {
            // Over the page its bar is always there, whatever the terminal shows (#4328: with no terminal it went
            // with the screen, and nothing closed the cover).
            if let grab { grabBar(grab) }
            screen
            if !noTerminal { controls } // keys with nothing to type into
        }
        .background(Color(hex: side.background), in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        // The controls on the terminal take its look: dark keys on the dark theme whatever the app's appearance.
        .environment(\.colorScheme, side.isDark ? .dark : .light)
        .animation(Theme.Motion.standard, value: typing)
        .animation(Theme.Motion.standard, value: keyboardUp)
        .onChange(of: keyboardUp) { _, up in onKeyboard?(up) }
        .onChange(of: active, initial: true) { _, on in
            guard let on else { return }
            if on { if direct { keyboardUp = true } else { typing = true } } else { keyboardUp = false; typing = false }
        }
        .sheet(isPresented: $showSettings) {
            TerminalSettingsView().environment(\.colorScheme, scheme)
            #if os(macOS)
                .macSheet()
            #endif
        }
        .alert("Another program on the computer is using this terminal", isPresented: Binding(get: { feed.control == .busy }, set: { shown in
            if !shown, feed.control == .busy { feed.watchOnly(String(localized: "View only: another program on the computer is using this terminal")) }
        })) {
            Button("Take Over") { feed.takeOver(model) }
            Button("View Only", role: .cancel) { feed.watchOnly(String(localized: "View only: another program on the computer is using this terminal")) }
        } message: {
            Text(TerminalSizeText.takeOver)
        }
        .onChange(of: feed.controlNote) { _, note in
            if let note { model.toast = Toast(note, style: .info) }
        }
        .onChange(of: sizeMode) { _, mode in feed.setWantsControl(mode == "phone", model) }
        .photosPicker(isPresented: $pickingPhotos, selection: $photoPicks, maxSelectionCount: 10,
                      matching: .any(of: [.images, .videos]), photoLibrary: .shared())
        .onChange(of: photoPicks) { _, items in
            guard !items.isEmpty else { return }
            photoPicks = []
            Task { await takePhotos(items) }
        }
        .fileUploadPicker(isPresented: $pickingFiles) { takeFiles($0.map { .file($0) }) }
        .confirmationDialog(pasting.map { String(localized: "Paste \($0.split(separator: "\n", omittingEmptySubsequences: false).count) lines into the terminal?") } ?? "",
                            isPresented: Binding(get: { pasting != nil }, set: { if !$0 { pasting = nil } }),
                            titleVisibility: .visible) {
            Button("Paste") { if let t = pasting { paste(t) } }
        } message: {
            Text("The shell may run multi-line text one line at a time.")
        }
        #if DEBUG
        .onAppear {
            if UserDefaults.standard.bool(forKey: "focusTerminalInput") { if direct { keyboardUp = true } else { typing = true } }
            if UserDefaults.standard.bool(forKey: "openShortcutsPanel") { showShortcuts = true }
            if UserDefaults.standard.bool(forKey: "openTerminalSettings") { showSettings = true }
            if UserDefaults.standard.bool(forKey: "openTerminalTips") { tipsSeen = false }
        }
        #endif
        .task(id: key) {
            if feed.key != key { feed = TerminalFeed(key: key) }
            feed.wantsControl = sizeMode == "phone"
            #if DEBUG
            // `-terminalPages N`: load N pages of history once the screen is there (what scrolling up does).
            let pages = UserDefaults.standard.integer(forKey: "terminalPages")
            var paged = pages == 0, ticks = 0
            #endif
            while !Task.isCancelled {
                await feed.tick(model)
                #if DEBUG
                if !paged, feed.loaded {
                    ticks += 1
                    if ticks >= 8 { // after the hub's hello: the stream replaces the first reads
                        paged = true
                        for _ in 0..<pages { await feed.loadOlder(model) }
                    }
                }
                #endif
                try? await Task.sleep(for: .milliseconds(250))
            }
            feed.stop(model)
            uploads.leave() // an upload that ends now types nothing into whatever is open next
        }
    }

    // MARK: Controls

    /// The key bar (over the keyboard while it is up), and in Input Field mode the text field under it.
    private var controls: some View {
        VStack(spacing: Theme.Space.s) {
            if !autoHideKeys || typing || keyboardUp || mods.active {
                TerminalKeyBar(mods: $mods, visible: Set(visibleKeys.split(separator: ",").map(String.init)),
                               shortcuts: { withAnimation(.spring(duration: 0.3, bounce: 0.25)) { showShortcuts.toggle() } },
                               shortcutsOn: showShortcuts,
                               paste: pasteClipboard,
                               photos: { pickingPhotos = true },
                               files: { pickingFiles = true },
                               keyboard: direct ? $keyboardUp : nil) { keys in send(keys) }
                    .overlay(alignment: .bottomLeading) {
                        if showShortcuts {
                            ShortcutsPanel(items: TerminalPrefs.decode(shortcutsJSON),
                                           hint: direct ? String(localized: "Quick commands · tap to type, double-tap to run") : String(localized: "Quick commands · tap to fill in, double-tap to send"),
                                           fill: fillShortcut,
                                           send: { send(["enter"], text: $0); showShortcuts = false },
                                           edit: { showShortcuts = false; showSettings = true })
                                .padding(.bottom, 52)
                                .transition(.scale(scale: 0.6, anchor: .bottomLeading).combined(with: .opacity))
                        }
                    }
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if !direct {
                if typing, !suggestions.isEmpty {
                    SuggestionStrip(items: suggestions) { input = $0 }
                }
                field
            }
        }
        .padding(.horizontal, Theme.Space.sm)
        .padding(.top, Theme.Space.xs)
        .padding(.bottom, Theme.Space.sm)
    }

    /// Input Field mode: write, then send with return.
    private var field: some View {
        HStack(spacing: Theme.Space.s) {
            TextField(Self.placeholder(mods), text: $input)
                .focused($typing)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .padding(Theme.Space.m)
                .background(Theme.Fill.chip, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                    .stroke(mods.active ? Theme.accent.opacity(0.6) : .clear, lineWidth: 1.5))
                .onSubmit(sendInput)
                .onChange(of: input) { old, new in
                    typed(old: old, new: new)
                    suggestions = TerminalCompletion.suggestions(for: input, rows: feed.content.rows)
                }
            Button(action: sendInput) {
                ThemedIcon(Theme.Icon.enter).scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
            }
            .themedButton(prominent: true)
            .disabled(input.isEmpty)
            .accessibilityLabel("Send with Return")
        }
    }

    // MARK: Screen

    /// Nothing runs the session (its tab was closed: a finished task's Agent, #4328) and no screen came before.
    private var noTerminal: Bool { feed.offline && feed.screen.isEmpty }

    @ViewBuilder private var screen: some View {
        if noTerminal {
            notRunning
        } else {
            // The view is there while the first frame comes: its layout tells the phone's grid, which driving the
            // terminal waits for before it subscribes (a spinner in its place kept it loading forever).
            live.overlay {
                if !feed.loaded {
                    ProgressView().tint(Color(hex: side.foreground)).frame(maxWidth: .infinity, maxHeight: .infinity)
                        .allowsHitTesting(false)
                }
            }
        }
    }

    /// No terminal: says the session isn't running and, for a Claude Code session, offers to resume it (in a new tab,
    /// as a message to it would, node `session.start` with `resume_session_id`). Over the page it drags down like
    /// the bar: there is no output under the finger to scroll.
    private var notRunning: some View {
        notRunningContent
            .padding(Theme.Space.page)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .contentShape(.rect)
            .modifier(TerminalGrabDrag(grab: grab))
            .accessibilityElement(children: .contain) // one element to find, its text and Resume Session inside
            .accessibilityIdentifier("terminal-not-running")
    }

    @ViewBuilder private var notRunningContent: some View {
        if resuming {
            ProgressView { Text("Resuming…").lineLimit(1) }
                .tint(Color(hex: side.foreground))
        } else if canResume {
            EmptyStateView(symbol: Theme.Icon.terminal, title: String(localized: "No Terminal"),
                           message: String(localized: "This Agent has stopped. Resume runs it again in a terminal on its computer."),
                           action: (title: String(localized: "Resume Session"), run: resumeSession))
        } else {
            EmptyStateView(symbol: Theme.Icon.terminal, title: String(localized: "No Terminal"),
                           message: String(localized: "This session isn't running on its computer."))
        }
    }

    /// A session Resume Session can bring back, the one way a stopped Agent runs again (#4754): a stopped Agent (Claude
    /// Code, Codex or a compatible CLI), a Codex thread nothing has open, or a Claude Code session of the engine that is
    /// not being started.
    private var canResume: Bool {
        guard let s = model.session(key), !s.assistant, s.starting != true else { return false }
        return s.isStopped || (s.sourceKind == "codex_history" && s.canTerminal) || (!s.hasSource && s.isClaude)
    }

    /// Over the shell a stopped Agent left (#4754): Resume runs it again in that shell.
    @ViewBuilder private var stoppedResume: some View {
        if canResume, model.session(key)?.isStopped == true {
            Button(action: resumeSession) {
                Text(resuming ? String(localized: "Resuming…") : String(localized: "Resume Session"))
                    .font(Theme.Font.captionStrong).lineLimit(1)
            }
            .themedButton(prominent: true)
            .disabled(resuming)
            .padding(Theme.Space.m)
            .accessibilityIdentifier("terminal-stopped-resume")
        }
    }

    /// Resumes it with what it ran with (the node fills in what is left out) and connects as soon as it is up.
    private func resumeSession() {
        guard let s = model.session(key), let api = model.api(key.group), !resuming else { return }
        Haptics.tap()
        resuming = true
        Task {
            defer { resuming = false }
            do {
                let started = try await api.start(.init(machineID: key.machineID, cwd: s.cwd, launcher: s.launcher ?? "", prompt: nil,
                                                        resumeSessionID: key.sessionID, workspaceID: nil,
                                                        permissionMode: s.permissionMode, effort: s.effort))
                Haptics.success()
                model.adopt(started)
                feed.retryNow(model)
            } catch {
                guard !error.isCancellation else { return }
                Haptics.error()
                model.toast = Toast(error.shown ?? String(localized: "Couldn't resume the session"), style: .error)
            }
        }
    }

    private var live: some View {
        liveContent
            .clipShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(alignment: .bottom) { bottomOverlay }
            .overlay(alignment: .topTrailing) { stoppedResume }
            .overlay { pinchOverlay }
            .overlay(alignment: .top) {
                if let error = feed.error {
                    Text(error).font(Theme.Font.label).foregroundStyle(.white).padding(Theme.Space.s)
                        .background(Theme.danger.opacity(0.85), in: .capsule).padding(Theme.Space.s)
                }
            }
            .overlay {
                if dropping {
                    RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                        .strokeBorder(Theme.accent, lineWidth: Theme.Size.lineMedium)
                        .allowsHitTesting(false)
                }
            }
            .animation(Theme.Motion.standard, value: feed.paused)
            .animation(Theme.Motion.standard, value: pinchHUD == nil)
    }

    private var liveContent: some View {
        VStack(alignment: .leading, spacing: 0) {
            if !feed.paused {
                TerminalFeedLink(feed: feed)
            }
            if grab == nil { cardHeader } // over the page the bar is above the screen (body)
            TerminalFeedFrame(feed: feed) { content in
                TerminalView(content: content, wrap: wrap, fontSize: fontSize, font: font, theme: theme,
                             focused: direct ? $keyboardUp : nil,
                             onInput: inputHandler,
                             onNearTop: { Task { await feed.loadOlder(model) } },
                             onOpenURL: { openURL($0) },
                             onCopy: { model.toast = Toast(String(localized: "Copied [Mac Hover 07d6bb]"), style: .success) },
                             onFontSize: pinched,
                             onSwipe: swipe,
                             onDoubleTap: pasteClipboard,
                             onWheel: feed.drives && model.canScrollTerminal(key.group) ? { model.terminalScroll(key, lines: $0, at: $1) } : nil,
                             onClick: feed.drives && model.canClickTerminal(key.group) ? { model.terminalClick(key, column: $0, row: $1) } : nil,
                             onGrid: gridChanged)
                #if os(macOS)
                .files(takeFiles, dropping: { on in dropping = on && feed.drives })
                .mouse(feed.drives && model.canMouseTerminal(key.group) ? { e in
                    model.terminalMouse(key, action: e.action.rawValue, button: e.button.rawValue, column: e.column, row: e.row, mods: e.mods)
                } : nil, mode: MacTerminalMouse.mode(mouseMode))
                #endif
            }
            .overlay(alignment: .top) {
                if let status = uploads.status { uploadingBar(status).padding(Theme.Space.s) }
            }
        }
    }

    private var inputHandler: ((TerminalInputEvent) -> Void)? {
        guard direct else { return nil }
        return { e in typedOnTerminal(e) }
    }

    @ViewBuilder private var bottomOverlay: some View {
        if feed.paused {
            VStack(spacing: Theme.Space.s) {
                // Over the page (the slim bar has no room for the card header's history line): where the history
                // ends, once there is nothing older to load (#1133).
                if grab != nil, let end = historyEnd {
                    Text(end)
                        .lineLimit(1)
                        .minimumScaleFactor(0.7)
                        .font(Theme.Font.labelMedium)
                        .foregroundStyle(Color(hex: side.foreground).opacity(0.7))
                        .padding(.horizontal, Theme.Space.m)
                        .padding(.vertical, Theme.Space.xs)
                        .background(Color(hex: side.background).opacity(0.9), in: .capsule)
                        .allowsHitTesting(false) // the terminal under it keeps its scrolling and selection
                        .accessibilityIdentifier("terminal-history-end")
                }
                Button {
                    Haptics.tap()
                    feed.resume()
                } label: {
                    Label("Jump to Latest", systemImage: "arrow.down.to.line").lineLimit(1).font(Theme.Font.captionStrong)
                }
                .themedButton()
            }
            .padding(Theme.Space.m)
            .transition(.move(edge: .bottom).combined(with: .opacity))
        } else if Self.touchTips, !tipsSeen {
            TerminalTips(direct: direct, openSettings: { tipsSeen = true; showSettings = true }) {
                withAnimation(Theme.Motion.standard) { tipsSeen = true }
            }
            .padding(Theme.Space.m)
            .transition(.move(edge: .bottom).combined(with: .opacity))
        }
    }

    @ViewBuilder private var pinchOverlay: some View {
        if let size = pinchHUD {
            Text("Text Size \(size, specifier: "%.1f")")
                .font(Theme.Font.secondaryStrong.monospacedDigit())
                .lineLimit(1)
                .padding(.horizontal, Theme.Space.l).padding(.vertical, Theme.Space.s)
                .surface(in: .capsule)
                .transition(.opacity)
                .allowsHitTesting(false)
        }
    }

    /// Over the page (TerminalCover): a bar on top in every state — its size on the left, the handle, settings and Close
    /// on the right. Dragged down it takes the terminal with it (far or fast enough: closed); a tap on the handle or on
    /// Close closes it. With no terminal (#4328) only the handle and Close: size and settings have nothing to act on.
    private func grabBar(_ g: TerminalGrab) -> some View {
        let fg = Color(hex: side.foreground)
        return ZStack {
            Capsule().fill(fg.opacity(0.35)).scaledFrame(width: Theme.Size.wellXL, height: Theme.Size.grabber)
                .scaledFrame(width: Theme.Size.box110, height: Theme.Size.wellS)
                .contentShape(.rect)
                .onTapGesture { Haptics.tap(); g.close() }
                .accessibilityHidden(true) // Close says it
            HStack(spacing: Theme.Space.sm) {
                if !noTerminal {
                    if feed.wantsControl, feed.control != .off { sizeChip } else { wrapToggle }
                    if feed.loading { ProgressView().controlSize(.mini).tint(fg) }
                }
                Spacer(minLength: 0)
                if !noTerminal {
                    Button { Haptics.tap(); showSettings = true } label: {
                        ThemedIcon(Theme.Icon.options).font(Theme.Font.captionStrong).scaledFrame(width: Theme.Size.well, height: Theme.Size.iconL)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(fg.opacity(0.75))
                    .accessibilityLabel("Terminal Settings")
                }
                Button { Haptics.tap(); g.close() } label: {
                    ThemedIcon(Theme.Icon.clear).symbolRenderingMode(.hierarchical).font(Theme.Font.title)
                        .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .foregroundStyle(fg.opacity(0.75))
                .accessibilityLabel("Close Terminal")
                .accessibilityIdentifier("terminal-close")
            }
        }
        .padding(.leading, Theme.Space.m)
        .padding(.trailing, Theme.Space.xs)
        .scaledFrame(height: Theme.Size.tapTarget)
        .contentShape(.rect)
        .modifier(TerminalGrabDrag(grab: g))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("terminal-bar")
    }

    private var cardHeader: some View {
        HStack(spacing: Theme.Space.s) {
            historyStatus
            Spacer(minLength: 8)
            if feed.wantsControl, feed.control != .off { sizeChip } else { wrapToggle }
            Button { Haptics.tap(); showSettings = true } label: {
                Label("Settings", systemImage: Theme.Icon.options)
                    .font(Theme.Font.labelStrong)
                    .lineLimit(1)
                    .fixedSize()
                    .padding(.horizontal, Theme.Space.s)
                    .padding(.vertical, Theme.Space.xs)
                    .background(Color(hex: side.foreground).opacity(0.1), in: .capsule)
            }
            .buttonStyle(.plain)
            .foregroundStyle(Color(hex: side.foreground).opacity(0.75))
            .accessibilityLabel("Terminal Settings")
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.top, Theme.Space.s)
    }

    /// Where the history ends, once nothing older is left to load: herdr keeps about 1000 lines of a pane. Nothing
    /// while driving or taking the terminal over: scrolling up is the program's own history then.
    private var historyEnd: String? {
        if feed.drives || feed.control == .starting { return nil }
        if feed.atLimit { return String(localized: "— herdr keeps only the last ~1000 lines; earlier output is in Chat —") }
        if !feed.hasOlder { return String(localized: "— Beginning of output —") }
        return nil
    }

    @ViewBuilder private var historyStatus: some View {
        Group {
            if feed.loading {
                HStack(spacing: Theme.Space.sm) { ProgressView().controlSize(.mini).tint(Color(hex: side.foreground)); Text("Loading earlier output…").lineLimit(1) }
            } else if feed.drives || feed.control == .starting {
                EmptyView() // driving: scrolling up is the terminal's own history since the takeover, nothing to say
            } else if let end = historyEnd {
                Text(end).lineLimit(1).minimumScaleFactor(0.7)
            } else {
                Text(TerminalHelpText.loadOlder).lineLimit(1).minimumScaleFactor(0.8)
            }
        }
        .font(Theme.Font.labelMedium)
        .foregroundStyle(Color(hex: side.foreground).opacity(0.55))
    }

    /// Driving the terminal: its size (the computer's pane is that now). Watching although it wanted to drive: a
    /// button to take it.
    @ViewBuilder private var sizeChip: some View {
        let fg = Color(hex: side.foreground)
        switch feed.control {
        case .active, .starting:
            let g = feed.phoneGrid
            Label(feed.drives ? "\(TerminalSizeText.own) \(g?.cols ?? 0)×\(g?.rows ?? 0)" : String(localized: "Taking over…"), systemImage: TerminalSizeText.icon)
                .font(Theme.Font.labelStrong.monospacedDigit())
                .lineLimit(1)
                .fixedSize()
                .padding(.horizontal, Theme.Space.s)
                .padding(.vertical, Theme.Space.xs)
                .background(fg.opacity(0.1), in: .capsule)
                .foregroundStyle(fg.opacity(0.75))
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("terminal-size")
        default:
            Button {
                Haptics.tap()
                feed.takeOver(model)
            } label: {
                Label("View Only · Take Over", systemImage: TerminalSizeText.takeOverIcon)
                    .font(Theme.Font.labelStrong)
                    .lineLimit(1)
                    .fixedSize()
                    .padding(.horizontal, Theme.Space.s)
                    .padding(.vertical, Theme.Space.xs)
                    .background(fg.opacity(0.1), in: .capsule)
            }
            .buttonStyle(.plain)
            .foregroundStyle(fg.opacity(0.75))
            .accessibilityLabel("Take Over Terminal")
            .accessibilityIdentifier("terminal-size")
        }
    }

    /// The view's grid: the first one at once (the feed waits for it to subscribe), later ones once they settle.
    private func gridChanged(_ g: TerminalGridSize) {
        gridTask?.cancel()
        guard feed.phoneGrid != nil else { return feed.setGrid(g, model) }
        let feed = feed, model = model
        gridTask = Task {
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            feed.setGrid(g, model)
        }
    }

    /// Wrap (rows fold at the phone's width) or No Wrap · Swipe Sideways (the pane's width, for TUI boxes).
    private var wrapToggle: some View {
        Button {
            Haptics.tap()
            wrap.toggle()
        } label: {
            Label(wrap ? String(localized: "Wrap") : TerminalHelpText.noWrap, systemImage: wrap ? "arrow.turn.down.left" : "arrow.left.and.right")
                .font(Theme.Font.labelStrong)
                .lineLimit(1)
                .fixedSize()
                .padding(.horizontal, Theme.Space.s)
                .padding(.vertical, Theme.Space.xs)
                .background(Color(hex: side.foreground).opacity(0.1), in: .capsule)
        }
        .buttonStyle(.plain)
        .foregroundStyle(Color(hex: side.foreground).opacity(0.75))
        .accessibilityLabel("Wrap Long Lines")
        .accessibilityValue(wrap ? String(localized: "On [Session Panes 39eae6]", comment: "Long-line wrapping: on") : TerminalHelpText.noWrapValue)
    }

    /// Over the terminal while files go to its machine.
    private func uploadingBar(_ status: String) -> some View {
        HStack(spacing: Theme.Space.s) {
            ProgressView().controlSize(.mini).tint(Color(hex: side.foreground))
            Text(status).lineLimit(1).truncationMode(.middle)
        }
        .font(Theme.Font.labelMedium)
        .foregroundStyle(Color(hex: side.foreground))
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.xs)
        .background(Color(hex: side.background).opacity(0.95), in: .capsule)
        .overlay(Capsule().strokeBorder(Color(hex: side.foreground).opacity(0.15), lineWidth: Theme.Size.hairline))
        .allowsHitTesting(false)
        .accessibilityIdentifier("terminal-uploading")
    }

    // MARK: Actions

    /// Files pasted, dropped or picked: uploaded to the pane's machine, then their paths typed in (folders refused).
    private func takeFiles(_ sources: [TerminalFileSource]) {
        let urls = sources.compactMap { if case .file(let u) = $0 { u } else { nil } }
        let split = TerminalPaste.files(urls)
        if split.folders > 0 { model.toast = Toast(TerminalPaste.folders, style: .error) }
        let keep = Set(split.files)
        let sending = sources.filter { if case .file(let u) = $0 { keep.contains(u) } else { true } }
        uploads.start(sending, feed: feed, model: model)
    }

    /// Photos from the library: pictures the agents cannot read (HEIC) as JPEG, the rest (and videos) as they are.
    private func takePhotos(_ items: [PhotosPickerItem]) async {
        var sources: [TerminalFileSource] = []
        var failed = 0
        for item in items {
            guard let data = try? await item.loadTransferable(type: Data.self) else { failed += 1; continue }
            let type = item.supportedContentTypes.first
            if TerminalPaste.needsJPEG(type), let jpeg = UIImage(data: data)?.jpegData(compressionQuality: 0.9) {
                sources.append(.data(jpeg, name: TerminalPaste.pictureName(.jpeg)))
            } else {
                sources.append(.data(data, name: TerminalPaste.photoName(type)))
            }
        }
        if failed > 0 { model.toast = Toast(String(localized: "Couldn't read \(failed) photos (they may still be downloading from iCloud) [Session Panes 5cdc8b]"), style: .error) }
        takeFiles(sources)
    }

    private func pinched(_ size: CGFloat) {
        fontSize = Double(size)
        pinchHUD = Double(size)
        let shown = Double(size)
        Task {
            try? await Task.sleep(for: .seconds(1))
            if pinchHUD == shown { pinchHUD = nil }
        }
    }

    /// A shortcut: typed into the terminal (or put in the field), not run.
    private func fillShortcut(_ s: String) {
        showShortcuts = false
        if direct {
            send([], text: s)
            keyboardUp = true
        } else {
            input = s
            typing = true
        }
    }

    /// Double tap: the clipboard goes to the terminal as typed (no return); several lines ask first.
    private func pasteClipboard() {
        guard let text = UIPasteboard.general.string, !text.isEmpty else {
            model.toast = Toast(String(localized: "No text on the clipboard"), style: .error)
            return
        }
        if text.contains("\n") { pasting = text } else { paste(text) }
    }

    private func paste(_ text: String) {
        Haptics.tap()
        send([], text: text)
        model.toast = Toast(String(localized: "Pasted"), style: .success)
        pasting = nil
    }

    /// Two fingers: left / right go to the next / previous agent of this space (herdr's tabs), up / down to the
    /// first agent of the next / previous space on this machine.
    private func swipe(_ d: UISwipeGestureRecognizer.Direction) {
        guard let switchTo else { return }
        let target: SessionKey?
        switch d {
        case .left, .right:
            let list = model.siblings(of: key).map(\.key)
            guard let i = list.firstIndex(of: key), list.count > 1 else { return }
            target = list[(i + (d == .left ? 1 : list.count - 1)) % list.count]
        default:
            let live = model.sessionsNow.filter { $0.machineID == key.machineID && $0.state.isLive && $0.spaceKey != nil }
            let spaces = Array(Set(live.compactMap(\.spaceKey))).sorted { $0.localizedStandardCompare($1) == .orderedAscending }
            guard let here = model.session(key)?.spaceKey, let i = spaces.firstIndex(of: here), spaces.count > 1 else { return }
            let next = spaces[(i + (d == .up ? 1 : spaces.count - 1)) % spaces.count]
            guard let there = live.first(where: { $0.spaceKey == next }) else { return }
            target = model.siblings(of: there.key).first?.key
        }
        if let target, target != key { switchTo(target) }
    }

    /// A key on the keyboard: with ctrl / alt / shift down on the key bar it goes out as that combination.
    private func typedOnTerminal(_ e: TerminalInputEvent) {
        if feed.paused { feed.resume() }
        guard mods.active else { return push(e) }
        switch e {
        case .text(let t) where t.count == 1:
            let (k, text) = mods.typed(t.first!)
            mods.pressed()
            push(k.map { .key($0) } ?? .text(text ?? t))
        case .key(let k):
            let name = mods.apply(k)
            mods.pressed()
            push(.key(name))
        case .text:
            push(e)
        }
    }

    /// The return after the text takes the modifiers that are down (shift+enter: a new line in Claude's box).
    private func sendInput() {
        let t = input
        guard !t.isEmpty else { return }
        input = ""
        let enter = mods.apply("enter")
        mods.pressed()
        send([enter], text: t)
    }

    /// Text, then keys — in order with everything typed before.
    private func send(_ keys: [String], text: String = "") {
        if !text.isEmpty { push(.text(text)) }
        for k in keys { push(.key(k)) }
    }

    private func push(_ e: TerminalInputEvent) {
        if feed.drives { // straight to the terminal, as its bytes
            switch e {
            case .text(let t): feed.type(TerminalKeyBytes.text(t), model)
            case .key(let k): if let b = TerminalKeyBytes.encode(k) { feed.type(b, model) }
            }
            return
        }
        let model = model, key = key, feed = feed
        stream.push(e) { keys, text in
            await model.keys(key, keys, text: text)
            feed.nudge()
        }
    }

    /// With a modifier down, a character typed in the field goes out as that key at once (ctrl, c → ctrl+c) instead of
    /// joining the text; the rest of a paste stays in the field.
    private func typed(old: String, new: String) {
        guard mods.active, new.count > old.count, new.hasPrefix(old), let ch = new.dropFirst(old.count).first else { return }
        let (k, text) = mods.typed(ch)
        mods.pressed()
        let rest = String(new.dropFirst(old.count + 1))
        input = old + rest
        Haptics.tap()
        if let k { send([k]) } else if let text { send([], text: text) }
    }

    static func placeholder(_ mods: TerminalModifiers) -> String {
        guard mods.active else { return String(localized: "Send text to the terminal") }
        let down = mods.apply("").dropLast()
        return String(localized: "Next character is sent as \(down)")
    }
}

private struct TerminalFeedLink: View {
    @Environment(AppModel.self) private var model
    let feed: TerminalFeed

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("TerminalFeedLink")
        #endif
        TerminalLinkStatus(lastOK: feed.lastOK, failure: feed.failure, nextTry: feed.nextTry) {
            Haptics.tap()
            feed.retryNow(model)
        }
    }
}

private struct TerminalFeedFrame<Frame: View>: View {
    let feed: TerminalFeed
    @ViewBuilder var frame: (TerminalContent) -> Frame

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("TerminalFeedFrame")
        #endif
        frame(feed.content)
    }
}

/// Sends what goes to the terminal in order, one request at a time; what is typed while one is out goes in the next
/// (KeyBatcher), so fast typing costs a few requests, not one per key, and nothing overtakes anything.
@MainActor final class KeyStream {
    private var batch = KeyBatcher()
    private var running = false

    func push(_ e: TerminalInputEvent, post: @escaping (_ keys: [String], _ text: String) async -> Void) {
        batch.push(e)
        guard !running else { return }
        running = true
        Task {
            while let next = batch.take() { await post(next.keys, next.text) }
            running = false
        }
    }
}

/// Shown on the terminal until dismissed: what can be done here that nothing on the screen says.
struct TerminalTips: View {
    var direct: Bool
    var openSettings: () -> Void
    var dismiss: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text("Terminal Tips").font(Theme.Font.secondaryStrong)
            VStack(alignment: .leading, spacing: Theme.Space.sm) {
                if direct { tip("keyboard", "Keyboard button: type directly; taps go to the terminal") }
                tip(Theme.Icon.enlarge, "Pinch to change text size")
                tip(Theme.Icon.tap, "Double-tap the terminal to paste")
                tip("bolt", "Bolt: quick commands; tap to type, double-tap to run")
                tip(Theme.Icon.switchAgents, "Two-finger swipe: switch agents in this space")
            }
            .font(Theme.Font.caption)
            HStack(spacing: Theme.Space.m) {
                Button("Colors, Font, Key Bar", action: openSettings).lineLimit(1).themedButton()
                Spacer(minLength: 0)
                Button("OK [Recordings Section de32e2]", action: dismiss).lineLimit(1).themedButton(prominent: true)
            }
            .font(Theme.Font.captionStrong)
        }
        .padding(Theme.Space.l)
        .scaledFrame(maxWidth: Theme.Size.box380, alignment: .leading)
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous)) // no shadow over the surface
    }

    private func tip(_ symbol: String, _ text: LocalizedStringKey) -> some View {
        Label { Text(text).lineLimit(1).minimumScaleFactor(0.8) } icon: {
            ThemedIcon(symbol).foregroundStyle(Theme.accent).scaledFrame(width: Theme.Size.iconXS)
        }
    }
}

/// Atop the terminal while what it shows may be old: how long since the last fresh screen, why, when the
/// next try is — and a button to try now. Hidden while refreshes keep arriving.
struct TerminalLinkStatus: View {
    var lastOK: Date?
    var failure: String?
    var nextTry: Date
    var retry: () -> Void

    private var tint: Color { failure != nil ? Theme.danger : Theme.caution }

    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { ctx in
            let stale = lastOK.map { ctx.date.timeIntervalSince($0) > TerminalPanel.staleAfter } ?? false
            if failure != nil || stale {
                HStack(spacing: Theme.Space.s) {
                    ThemedIcon(failure != nil ? Theme.Icon.offline : Theme.Icon.recent)
                        .font(Theme.Font.labelStrong)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(Self.headline(lastOK: lastOK, now: ctx.date)).font(Theme.Font.labelStrong).lineLimit(1)
                        Text(Self.detail(failure: failure, nextTry: nextTry, now: ctx.date))
                            .font(Theme.Font.label).opacity(0.8).lineLimit(1)
                    }
                    Spacer(minLength: 4)
                    Button(action: retry) {
                        Text("Retry [Components b8784c]").font(Theme.Font.captionStrong).lineLimit(1)
                            .foregroundStyle(.black)
                            .padding(.horizontal, Theme.Space.l)
                            .scaledFrame(height: Theme.Size.wellS)
                            .background(.white, in: .capsule)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Retry [Components b8784c]")
                }
                .foregroundStyle(.black)
                .padding(.leading, Theme.Space.m)
                .padding(.trailing, Theme.Space.s)
                .padding(.vertical, Theme.Space.s)
                .background(tint, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
                .padding([.horizontal, .top], Theme.Space.sm)
                .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
    }

    /// "Updated 12 seconds ago" / "No terminal yet".
    static func headline(lastOK: Date?, now: Date) -> String {
        guard let lastOK else { return String(localized: "No terminal yet") }
        let s = max(0, Int(now.timeIntervalSince(lastOK)))
        if s < 60 { return String(localized: "Updated \(s) seconds ago") }
        if s < 3600 { return String(localized: "Updated \(s / 60) minutes ago") }
        return String(localized: "Updated at \(lastOK.formatted(date: .omitted, time: .shortened))")
    }

    /// When it tries again, then why (the reason may be cut off, the countdown never): "Retrying in 4 seconds · Can't reach the hub",
    /// or "Waiting for the computer…" while a request hangs.
    static func detail(failure: String?, nextTry: Date, now: Date) -> String {
        guard let failure else { return String(localized: "Waiting for the computer…") }
        let s = Int(ceil(nextTry.timeIntervalSince(now)))
        return s > 0 ? String(localized: "Retrying in \(s) seconds · \(failure)") : String(localized: "Retrying… · \(failure)")
    }
}

// MARK: - Background tasks

/// Commands and subagents Claude left running in the background ("2 shells still running" in the TUI), shown above
/// the composer even after the turn ended. Tap one to read its output file.
struct BackgroundTasksStrip: View {
    var tasks: [BackgroundTask]
    var openOutput: (BackgroundTask) -> Void
    @State var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Button {
                Haptics.tap()
                withAnimation(Theme.Motion.standard) { expanded.toggle() }
            } label: {
                if AppStyle.current.usesBeautifulUI {
                    // Its tool run header over its task rows.
                    ToolRunTitle(isOpen: expanded) {
                        Text(!expanded && tasks.first != nil ? String(localized: "\(tasks.count) background tasks · \(tasks[0].title)") : String(localized: "\(tasks.count) background tasks"))
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(.rect)
                } else {
                    HStack(spacing: Theme.Space.s) {
                        ThemedIcon(Theme.Icon.backgroundTask)
                            .font(Theme.Font.captionStrong)
                            .foregroundStyle(Theme.busy)
                        Text("\(tasks.count) background tasks").font(Theme.Font.captionStrong).lineLimit(1)
                        if !expanded, let first = tasks.first {
                            Text(first.title).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                        }
                        Spacer(minLength: 4)
                        ThemedIcon(Theme.Icon.collapse)
                            .font(Theme.Font.labelHeavy)
                            .foregroundStyle(.secondary)
                            .rotationEffect(.degrees(expanded ? 0 : 180))
                    }
                    .contentShape(.rect)
                }
            }
            .buttonStyle(.plain)
            .accessibilityLabel("\(tasks.count) background tasks, \(expanded ? String(localized: "Collapse") : String(localized: "Expand"))")
            if expanded {
                if AppStyle.current.usesBeautifulUI {
                    VStack(spacing: 0) {
                        ForEach(Array(tasks.enumerated()), id: \.element.id) { n, t in taskRow(t, n: n) }
                    }
                } else {
                    ForEach(tasks) { t in row(t) }
                }
            }
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
    }

    private func row(_ t: BackgroundTask) -> some View {
        Button { if t.outputFile != nil { openOutput(t) } } label: {
            HStack(alignment: .top, spacing: Theme.Space.m) {
                ThemedIcon(t.isAgent ? Theme.Icon.subagent : Theme.Icon.terminal)
                    .font(Theme.Font.labelStrong).dynamicTypeSize(...DynamicTypeSize.xxLarge)
                    .foregroundStyle(.secondary)
                    .scaledFrame(width: Theme.Size.badge, height: Theme.Size.badge)
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    Text(t.title).font(Theme.Font.secondaryMedium).foregroundStyle(Color.primary).lineLimit(1)
                    if let cmd = t.command, !cmd.isEmpty, cmd != t.title {
                        Text(cmd).font(Theme.Font.label.monospaced()).foregroundStyle(.secondary).lineLimit(2)
                    }
                }
                Spacer(minLength: 4)
                VStack(alignment: .trailing, spacing: Theme.Space.xs) {
                    if let start = t.startedAt {
                        TimelineView(.periodic(from: .now, by: 1)) { ctx in
                            Text(Self.elapsed(from: start, to: ctx.date))
                                .font(Theme.Font.label.monospacedDigit())
                                .foregroundStyle(.secondary)
                        }
                    }
                    if t.outputFile != nil {
                        Text("View Output [Session Panes c63585]").font(Theme.Font.labelStrong).foregroundStyle(Theme.accent).lineLimit(1)
                    }
                }
            }
            .padding(Theme.Space.m)
            .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .contextMenu {
            if let cmd = t.command { Button { UIPasteboard.general.string = cmd } label: { Label("Copy Command", systemImage: Theme.Icon.copy) } }
        }
    }

    /// The BeautifulUI style: its task row, a numbered ring turning while it runs, its command under it, how long it
    /// has been running on the right; a tap opens its output.
    private func taskRow(_ t: BackgroundTask, n: Int) -> some View {
        let cmd = t.command.flatMap { $0.isEmpty || $0 == t.title ? nil : $0 }
        return TaskRow(TaskItem(id: t.id, title: t.title, subtitle: cmd, status: .running(step: n + 1)),
                       shape: .list(first: n == 0, last: n == tasks.count - 1),
                       accessory: t.outputFile != nil ? .disclosure : .none, subtitleLines: 2,
                       action: t.outputFile != nil ? { openOutput(t) } : nil) {
            TaskBadge(.running(step: n + 1))
        } trailing: {
            if let start = t.startedAt {
                TimelineView(.periodic(from: .now, by: 1)) { ctx in
                    Text(Self.elapsed(from: start, to: ctx.date))
                        .font(Theme.Font.label.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        } detail: {
            EmptyView()
        }
        .accessibilityHint(t.outputFile != nil ? String(localized: "View Output [Session Panes c63585]") : "")
        .contextMenu {
            if let cmd = t.command { Button { UIPasteboard.general.string = cmd } label: { Label("Copy Command", systemImage: Theme.Icon.copy) } }
        }
    }

    static func elapsed(from start: Date, to now: Date) -> String {
        let s = max(0, Int(now.timeIntervalSince(start)))
        if s < 60 { return String(localized: "\(s) s") }
        if s < 3600 { return String(localized: "\(s / 60)m \(s % 60)s") }
        return String(localized: "\(s / 3600)h \(s % 3600 / 60)m [Session Panes c667a6]")
    }
}

// MARK: - Activity chips

/// "⟳ 13:24 │ ⚙ 2" at the end of the chat header's second row: what keeps an agent going, in one neutral pill as tall
/// as the tabs beside it. The background work is a count by a neutral gear: whether it works or only waits is the
/// agent's state, which the title's line says once (#3719; it used to draw the state's clock or amber gear here too).
/// The loop's arrows keep their color; the words are in its VoiceOver label and its sheet (`ActivitySheet`). At the
/// largest text sizes, with both there, the loop is only its arrows so the pill leaves the tabs some room.
struct ActivityChips: View {
    var loop: LoopInfo?
    var background: [BackgroundTask]
    var open: () -> Void
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        Button {
            Haptics.tap()
            open()
        } label: {
            HStack(spacing: Theme.Space.s) {
                if let loop {
                    HStack(spacing: Theme.Space.xxs) {
                        ThemedIcon(Self.loopSymbol).font(Theme.Font.labelHeavy).foregroundStyle(Theme.loop)
                        if background.isEmpty || typeSize < .xxxLarge {
                            TimelineView(.periodic(from: .now, by: 30)) { ctx in
                                Text(Self.shortLoopLabel(loop, now: ctx.date)).lineLimit(1)
                            }
                        }
                    }
                }
                if loop != nil, !background.isEmpty {
                    Divider().scaledFrame(height: Theme.Size.markXS) // in an HStack: the hairline between the two
                }
                if !background.isEmpty {
                    HStack(spacing: Theme.Space.xxs) {
                        ThemedIcon(Self.backgroundSymbol).font(Theme.Font.labelHeavy).foregroundStyle(.secondary)
                        Text("\(background.count)").monospacedDigit().lineLimit(1)
                    }
                }
            }
            .font(Theme.Font.labelStrong)
            .foregroundStyle(Color.primary)
            .padding(.horizontal, Theme.Space.m)
            .scaledFrame(height: Theme.Size.wellS)
            .background(Theme.Fill.chip, in: .capsule)
            .contentShape(.capsule)
        }
        .buttonStyle(.plain)
        .fixedSize()
        .accessibilityLabel(a11yLabel)
    }

    /// The loop's and the background work's symbols, shared with the folded bar's mark.
    static let loopSymbol = Theme.Icon.inProgress
    static let backgroundSymbol = Theme.Icon.backgroundTask

    private var a11yLabel: String {
        let parts = [loop.map { String(localized: "Looping, \(Self.loopLabel($0, now: .now))") },
                     background.isEmpty ? nil : String(localized: "\(background.count) background tasks")].compactMap { $0 }
        let list = parts.joined(separator: String(localized: ", ", comment: "Separator between items of a spoken list"))
        return String(localized: "\(list), tap to view")
    }

    /// The loop beside the arrows when room is short: the next time, or that it is running.
    static func shortLoopLabel(_ loop: LoopInfo, now: Date) -> String {
        if let next = loop.nextAt, next > now { return next.formatted(date: .omitted, time: .shortened) }
        return loop.nextAt != nil ? String(localized: "Running [Session Panes 5026a6]") : String(localized: "Loop")
    }

    static func loopLabel(_ loop: LoopInfo, now: Date) -> String {
        if let next = loop.nextAt, next > now { return String(localized: "Next \(next.formatted(date: .omitted, time: .shortened)) [Session Panes 0881ca]") }
        if loop.nextAt != nil { return String(localized: "Loop running") }
        return loop.cadence ?? String(localized: "Looping")
    }
}

/// The chips opened: the loop (with its stop button) and every background task (with its output).
struct ActivitySheet: View {
    @Environment(\.dismiss) private var dismiss
    var loop: LoopInfo?
    var background: [BackgroundTask]
    var stopLoop: () -> Void
    var openOutput: (BackgroundTask) -> Void

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: Theme.Space.m) {
                    if let loop { LoopStrip(loop: loop, stop: stopLoop, expanded: true) }
                    if !background.isEmpty { BackgroundTasksStrip(tasks: background, openOutput: openOutput, expanded: true) }
                }
                .padding(Theme.Space.l)
            }
            .background(Theme.pageBackground)
            .navigationTitle(loop != nil && background.isEmpty ? String(localized: "Loop") : loop == nil ? String(localized: "Background Task") : String(localized: "Loop & Background Tasks"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done [Extension System Presentation c0b3fb]") { dismiss() } } }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }
}

// MARK: - Loops

/// The /loop driving this agent: what it repeats, how often, when it wakes next — and a way to stop it.
struct LoopStrip: View {
    var loop: LoopInfo
    var stop: () -> Void
    @State var expanded = false
    @State private var confirming = false

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.inProgress)
                    .font(Theme.Font.captionHeavy)
                    .foregroundStyle(Theme.loop)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(loop.cadence.map { String(localized: "Looping · \($0)") } ?? String(localized: "Looping")).font(Theme.Font.captionStrong).lineLimit(1)
                    if let next = loop.nextAt {
                        TimelineView(.periodic(from: .now, by: 30)) { ctx in
                            Text(next > ctx.date ? String(localized: "Next wake-up \(next.formatted(date: .omitted, time: .shortened)) (\(Self.until(next, now: ctx.date)))")
                                                 : String(localized: "Running this round"))
                                .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                        }
                    }
                }
                Spacer(minLength: 4)
                Button { confirming = true } label: {
                    Text("Stop Loop").font(Theme.Font.captionStrong).lineLimit(1)
                }
                .themedButton()
                .tint(Theme.danger)
            }
            .contentShape(.rect)
            .onTapGesture { withAnimation(Theme.Motion.standard) { expanded.toggle() } }
            if expanded {
                Text(loop.task).font(Theme.Font.secondary).foregroundStyle(.secondary).textSelection(.enabled)
                if let r = loop.reason, !r.isEmpty {
                    Label(r, systemImage: Theme.Icon.conversation).font(Theme.Font.label).foregroundStyle(.tertiary)
                }
            }
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
        .confirmationDialog("Stop this loop?", isPresented: $confirming, titleVisibility: .visible) {
            Button("Stop Loop", role: .destructive, action: stop)
        } message: {
            Text("This sends the agent a message asking it not to schedule another wake-up. The current round finishes first.")
        }
    }
}

extension LoopStrip {
    /// "4 min left" / "1 h 5 min left".
    static func until(_ date: Date, now: Date) -> String {
        let m = max(1, Int(ceil(date.timeIntervalSince(now) / 60)))
        if m < 60 { return String(localized: "\(m) minutes left") }
        return m % 60 > 0 ? String(localized: "\(m / 60) h \(m % 60) min left") : String(localized: "\(m / 60) hours left")
    }
}

/// A wakeup of a /loop (the transcript records each as the "/loop …" prompt again): one quiet line, not a bubble.
struct LoopTickRow: View {
    var item: Item

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            ThemedIcon(Theme.Icon.inProgress).font(Theme.Font.labelHeavy)
            Text("Loop Wake-Up").fontWeight(.semibold)
            if let ts = item.ts { Text(ts.formatted(date: .omitted, time: .shortened)) }
            Text(String((item.text ?? "").dropFirst(5)).trimmingCharacters(in: .whitespaces)).lineLimit(1).foregroundStyle(.tertiary)
        }
        .font(Theme.Font.label)
        .foregroundStyle(.secondary)
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.sm)
        .background(Theme.Fill.well, in: .capsule)
        .frame(maxWidth: .infinity, alignment: .trailing)
    }
}


// MARK: - Subagents

extension EnvironmentValues {
    /// Opens the subagent launched by an Agent tool call (by its tool_use id; set by ChatView).
    @Entry var openSubagent: ((String) -> Void)? = nil
}

/// The agents in this conversation, on the Mac's row over the chat and in a hosted header (a task's sheet, #3255):
/// Main Agent and each subagent it launched (spinning while it runs, a check when done). The iPhone's and iPad's Agents
/// header has one status line instead (`HeaderStatusLine`). Tap one to read its conversation in the same chat UI. When some of `all` are not chips
/// (finished, nested), 「全部子 Agent N」 at the end opens every one by who launched whom.
struct AgentStrip: View {
    var agents: [Subagent]
    var all: [Subagent]
    var viewing: String?
    var select: (Subagent?) -> Void
    @State private var showAll = false

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: Theme.Space.sm) {
                    chip(title: String(localized: "Main Agent"), symbol: Theme.Icon.user, running: false, done: false, on: viewing == nil) { select(nil) }
                        .id("main")
                    ForEach(agents) { a in
                        chip(title: a.title, symbol: nil, running: a.running, done: !a.running, on: viewing == a.id) { select(a) }
                            .id(a.id)
                    }
                    if all.count > agents.count { allButton }
                }
                .padding(.horizontal, Theme.Space.l)
                .padding(.bottom, Theme.Space.sm)
            }
            // The one being read stays in sight — only when it changes, and only along the row (an initial scrollTo
            // also scrolled the page around it and shifted the whole header).
            .onChange(of: viewing) { _, id in
                guard let id else { return }
                withAnimation(Theme.Motion.standard) { proxy.scrollTo(id, anchor: .trailing) }
            }
        }
        .sheet(isPresented: $showAll) {
            SubagentTreeSheet(rows: Subagent.tree(all), viewing: viewing) { a in
                showAll = false
                select(a)
            }
        }
    }

    @ViewBuilder private var allButton: some View {
        let open = { Haptics.tap(); showAll = true }
        Group {
            if AppStyle.current.usesBeautifulUI {
                BUIFilterChip(String(localized: "All Subagents"), count: all.count, isActive: false, action: open)
            } else {
                Button(action: open) {
                    HStack(spacing: Theme.Space.sm) {
                        ThemedIcon(Theme.Icon.list).font(Theme.Font.labelStrong)
                        Text("All Subagents").lineLimit(1)
                        Text("\(all.count)").monospacedDigit().foregroundStyle(.secondary)
                    }
                    .font(Theme.Font.labelMedium)
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.vertical, Theme.Space.sm)
                    .background(Theme.Fill.well, in: .capsule)
                    .foregroundStyle(Color.primary)
                }
                .buttonStyle(.plain)
            }
        }
        .accessibilityLabel("All subagents, \(all.count)")
        .accessibilityHint("Lists every subagent under the agent that started it")
    }

    @ViewBuilder private func chip(title: String, symbol: String?, running: Bool, done: Bool, on: Bool, action: @escaping () -> Void) -> some View {
        if AppStyle.current.usesBeautifulUI {
            // Its filter chip: a dot in the working color while the subagent runs, green once it is done.
            BUIFilterChip(title, dot: running ? Theme.busy : done ? Theme.positive : nil, isActive: on, action: action)
                .accessibilityLabel(running ? String(localized: "\(title), running") : done ? String(localized: "\(title), done") : title)
        } else {
            plainChip(title: title, symbol: symbol, running: running, done: done, on: on, action: action)
        }
    }

    private func plainChip(title: String, symbol: String?, running: Bool, done: Bool, on: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: Theme.Space.sm) {
                if running {
                    ProgressView().controlSize(.mini)
                } else if done {
                    ThemedIcon(Theme.Icon.successFilled).font(Theme.Font.label).foregroundStyle(Theme.positive)
                } else if let symbol {
                    ThemedIcon(symbol).font(Theme.Font.labelStrong)
                }
                Text(title).lineLimit(1).frame(maxWidth: 160)
            }
            .font(on ? Theme.Font.labelStrong : Theme.Font.labelMedium)
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.sm)
            .background(on ? Theme.accent.opacity(0.15) : Theme.Fill.well, in: .capsule)
            .overlay(Capsule().stroke(on ? Theme.accent.opacity(0.45) : .clear, lineWidth: 1))
            .foregroundStyle(on ? Theme.accent : Color.primary)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(running ? String(localized: "\(title), running") : done ? String(localized: "\(title), done") : title)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

/// The one thing about the conversation's subagents the chat header's status line says (docs/archive/apple/plan-chat-topbar.md ③),
/// the first that holds: one is being read; one runs; several run. None while none runs: finished and nested ones are
/// reached from their Agent call ("View subagent") and ⋯ › All subagents.
nonisolated enum HeaderStatus: Equatable, Sendable {
    case viewing(Subagent)
    case running(Subagent)
    /// How many run and the one started last.
    case severalRunning(count: Int, latest: Subagent)

    static func pick(_ all: [Subagent], viewing: Subagent?) -> HeaderStatus? {
        if let viewing { return .viewing(all.first { $0.id == viewing.id } ?? viewing) }
        let running = all.filter(\.running)
        guard let first = running.first else { return nil }
        if running.count == 1 { return .running(first) }
        // Started last; one without a start time counts as oldest, and of equals the first listed wins.
        let latest = running.reduce(first) { a, b in (b.startedAt ?? .distantPast) > (a.startedAt ?? .distantPast) ? b : a }
        return .severalRunning(count: running.count, latest: latest)
    }
}

/// The chat header's third row, at most one line across the page's margins (`HeaderStatus`): no chip for the main agent
/// (while a subagent is read, `SubagentFooter` at the composer has the way back, in reach of a thumb), the subagent's
/// title cut at its end instead of running off the screen. A tap opens the one running, or every subagent when several run.
struct HeaderStatusLine: View {
    var status: HeaderStatus
    var open: (Subagent) -> Void
    var openAll: () -> Void

    var body: some View {
        switch status {
        case .viewing(let a):
            line(label: String(localized: "Viewing Subagent"), title: String(localized: "\(a.title) (read-only)"), chevron: false) {
                ThemedIcon(Theme.Icon.view).font(Theme.Font.labelStrong).foregroundStyle(Theme.accent)
            }
            .accessibilityElement(children: .combine)
            .accessibilityLabel(String(localized: "Viewing subagent: \(a.title), read-only"))
            .accessibilityIdentifier("chat.status-line")
        case .running(let a):
            Button { open(a) } label: {
                line(label: String(localized: "Subagent [Session Panes 4b81d0]"), title: a.title, chevron: true) { spinner }
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String(localized: "Subagent running: \(a.title)"))
            .accessibilityHint(String(localized: "Opens its conversation"))
            .accessibilityIdentifier("chat.status-line")
        case .severalRunning(let n, let latest):
            Button { openAll() } label: {
                line(label: String(localized: "\(n) subagents running ·"), title: latest.title, titleSecondary: true, chevron: true) { spinner }
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String(localized: "\(n) subagents running, the newest is \(latest.title)"))
            .accessibilityHint(String(localized: "Lists every subagent"))
            .accessibilityIdentifier("chat.status-line")
        }
    }

    /// Not a third orange dot under the subtitle's and the tab's: the work spinner.
    private var spinner: some View { ProgressView().controlSize(.mini) }

    private func line(label: String, title: String, titleSecondary: Bool = false, chevron: Bool,
                      @ViewBuilder icon: () -> some View) -> some View {
        HStack(spacing: Theme.Space.s) {
            icon()
            Text(label)
                .font(Theme.Font.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize()
            Text(title)
                .font(Theme.Font.captionMedium)
                .foregroundStyle(titleSecondary ? AnyShapeStyle(.secondary) : AnyShapeStyle(Color.primary))
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: .infinity, alignment: .leading)
            if chevron {
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
            }
        }
        .scaledFrame(minHeight: Theme.Size.wellS)
        .contentShape(.rect)
    }
}

/// "All subagents": every subagent under the one that launched it (`Subagent.tree`), with how it is going, what it is
/// and when it started; a tap reads it, like its chip.
struct SubagentTreeSheet: View {
    @Environment(\.dismiss) private var dismiss
    var rows: [Subagent.TreeRow]
    var viewing: String?
    var select: (Subagent) -> Void

    /// Past this many levels deeper ones line up with the last.
    private static let maxIndent = 4

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: Theme.Space.sm) {
                    ForEach(rows, id: \.agent.id) { row($0) }
                }
                .padding(Theme.Space.l)
            }
            .background(Theme.pageBackground)
            .navigationTitle("All Subagents")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done [Extension System Presentation c0b3fb]") { dismiss() } } }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }

    /// VoiceOver: its title, how it is going, what it is, how deep.
    private static func label(_ r: Subagent.TreeRow) -> String {
        let a = r.agent
        var parts = [a.title, a.running ? String(localized: "Running [Session Panes 1f0eb9]") : String(localized: "Ended")]
        if a.fork == true { parts.append(String(localized: "Fork")) } else if let t = a.type { parts.append(t) }
        if r.depth > 0 { parts.append(String(localized: "Level \(r.depth + 1)")) }
        return parts.joined(separator: String(localized: ", ", comment: "Separator between items of a spoken list"))
    }

    private func row(_ r: Subagent.TreeRow) -> some View {
        let a = r.agent, on = viewing == a.id
        return Button { select(a) } label: {
            HStack(alignment: .top, spacing: Theme.Space.s) {
                if r.depth > 0 {
                    ThemedIcon(Theme.Icon.followOn)
                        .font(Theme.Font.label)
                        .foregroundStyle(.tertiary)
                }
                StatusDot(state: a.running ? .working : .unread, size: Theme.Size.dotXS)
                    .scaledFrame(width: Theme.Size.dot, height: Theme.Size.badge)
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    Text(a.title).font(Theme.Font.secondaryMedium).foregroundStyle(Color.primary).lineLimit(1)
                    HStack(spacing: Theme.Space.sm) {
                        Text(a.running ? String(localized: "Running [Session Panes 1f0eb9]") : String(localized: "Ended")).foregroundStyle(a.running ? Theme.busy : .secondary)
                        if a.fork == true {
                            Chip(text: String(localized: "Fork"), symbol: Theme.Icon.gitBranch)
                        } else if let t = a.type {
                            Text("· " + t).foregroundStyle(.secondary).lineLimit(1)
                        }
                    }
                    .font(Theme.Font.label)
                    .lineLimit(1)
                }
                Spacer(minLength: Theme.Space.xs)
                if let start = a.startedAt ?? a.updatedAt {
                    TimelineView(.periodic(from: .now, by: 30)) { ctx in
                        Text(Fmt.relative(start, now: ctx.date))
                            .font(Theme.Font.label.monospacedDigit())
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
            }
            .padding(Theme.Space.m)
            .background(on ? Theme.accent.opacity(0.12) : Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .padding(.leading, CGFloat(min(r.depth, Self.maxIndent)) * Theme.Space.l)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(Self.label(r))
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

/// In place of the composer while a subagent is shown: what it is and the way back.
struct SubagentFooter: View {
    var agent: Subagent
    var back: () -> Void

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            ThemedIcon(agent.running ? Theme.Icon.subagentWorking : Theme.Icon.subagent)
                .font(Theme.Font.secondaryStrong)
                .foregroundStyle(Theme.accent)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text("Subagent · \(agent.type ?? "Agent")").font(Theme.Font.captionStrong).lineLimit(1)
                Text(agent.running ? String(localized: "Running, read-only") : String(localized: "Done, read-only")).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer(minLength: 6)
            Button(action: back) {
                Label("Back to Main Agent", systemImage: Theme.Icon.undo).font(Theme.Font.captionStrong).lineLimit(1)
            }
            .themedButton()
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
    }
}

/// How the terminal shown over the page (TerminalCover) is dragged down by its bar, and closed.
struct TerminalGrab {
    var dragged: (CGFloat) -> Void
    var released: (_ distance: CGFloat, _ predicted: CGFloat) -> Void
    var close: () -> Void
}

/// The cover's drag down, on its bar and on the no-terminal state (#4328): the terminal follows the finger, and goes
/// when let go far or fast enough. Nothing where there is no cover to drag (the panel in place).
private struct TerminalGrabDrag: ViewModifier {
    var grab: TerminalGrab?

    @ViewBuilder func body(content: Content) -> some View {
        if let grab {
            content.gesture(DragGesture(minimumDistance: 4, coordinateSpace: .global)
                .onChanged { grab.dragged($0.translation.height) }
                .onEnded { grab.released($0.translation.height, $0.predictedEndTranslation.height) })
        } else {
            content
        }
    }
}

