import SwiftUI

/// A Mac's VNC on the whole screen (#1343), where it can be operated: its picture, the touches as the mode says (Mouse /
/// Trackpad), the clipboard both ways, the keyboard with a row of the Mac's shortcuts, Help, and Picture in Picture back to picture in
/// picture. When the agent whose chat opened it waits on the user, 「我完成了」 hands the Mac back (closing does too).
struct VNCPage: View {
    @Environment(AppModel.self) private var model
    let ref: MachineRef
    var unlock = false
    var from: SessionKey?
    var onClose: () -> Void
    @State private var session: RFBSession?
    @State private var mode: VNCMode = .mouse
    @State private var help = false
    @State private var typing = false
    @State private var unlocked = false
    #if os(iOS) || os(visionOS)
    @State private var screenView: VNCScreenUIView?
    @State private var modifiers = VNCModifiers()
    /// The display shown alone (`VNCDisplay.id`, kept per Mac, #80); nil: all of them.
    @State private var display: String?
    @State private var pipLog = false
    /// The app went to the background with the page up (not just Control Center or an alert over it).
    @State private var wentBackground = false
    /// When the app came back from the background with the connection not (yet) known to be down: a drop reported
    /// within `returnWindow` is the suspension's, so it reconnects once by itself (#4243).
    @State private var returnedAt: Date?
    private static let returnWindow: TimeInterval = 5
    /// When the app came back with the connection still saying live, and which connection was asked: the whole screen
    /// is asked for, and no frame within `frameWait` means it died quietly while suspended, so it reconnects once (#4380).
    @State private var askedFrameAt: Date?
    @State private var askedConnection = 0
    /// Long enough for a whole Retina frame over the hub's relay on a slow network: a healthy connection is not dropped.
    private static let frameWait: Duration = .seconds(10)
    #endif

    private var name: String { model.machine(ref)?.name ?? ref.id }
    /// The agent that opened it waits on the user: there is something to hand back.
    private var waiting: Bool { from.flatMap { model.session($0) }?.state == .needsYou }

    var body: some View {
        VStack(spacing: 0) {
            topBar
            TimelineView(.periodic(from: .now, by: 1)) { ctx in
                if let a = model.computerActivities[ref], a.shown(at: ctx.date) { stepLine(a) }
            }
            ZStack {
                Color.black
                if let s = session { content(s) }
            }
            #if os(iOS) || os(visionOS)
            // Over the keyboard while it is up (the bars ride above it).
            if typing, let s = session { VNCShortcutBar(session: s, modifiers: modifiers) }
            #endif
            bottomBar
        }
        .background(Color.black.ignoresSafeArea())
        .environment(\.colorScheme, .dark)
        .onAppear {
            session = VNC.hold(ref)
            mode = VNCMode.of(ref)
            #if os(iOS) || os(visionOS)
            display = VNCDisplayChoice.of(ref)
            #endif
        }
        .onDisappear {
            #if os(iOS)
            VNCPiP.shared.pageGone(ref)
            #endif
            VNC.release(ref)
        }
        .onChange(of: session?.phase) { _, phase in
            #if os(iOS)
            // The small window ready (#1839): Picture in Picture starts it in the tap, and going home starts it by itself.
            if phase == .live { VNCPiP.shared.prepare(ref, from: from, automatic: true) }
            #endif
            if phase == .live, unlock, !unlocked {
                unlocked = true
                session?.unlockWithKeptPassword()
            }
            #if os(iOS) || os(visionOS)
            if case .closed? = phase, let at = returnedAt, Date.now.timeIntervalSince(at) < Self.returnWindow {
                returnedAt = nil
                session?.start()
            }
            #endif
        }
        #if os(iOS) || os(visionOS)
        // Back from the background: the suspended app's connection is usually gone. Reconnect instead of waiting on
        // Reconnect being tapped; a sign-in asked for, or a connection still up, is left as it is.
        .onReceive(NotificationCenter.default.publisher(for: UIApplication.didEnterBackgroundNotification)) { _ in wentBackground = true }
        .onReceive(NotificationCenter.default.publisher(for: UIApplication.didBecomeActiveNotification)) { _ in
            guard wentBackground, let s = session else { return }
            wentBackground = false
            if case .closed = s.phase {
                returnedAt = nil
                s.start()
            } else {
                returnedAt = .now
                if case .live = s.phase {
                    askedConnection = s.connection
                    askedFrameAt = .now
                    s.refresh()
                }
            }
        }
        .task(id: askedFrameAt) {
            guard let at = askedFrameAt else { return }
            try? await Task.sleep(for: Self.frameWait)
            guard !Task.isCancelled else { return }
            defer { askedFrameAt = nil }
            // Only the connection that was asked, still saying live: one started since (a reconnect, a sign-in) is new.
            guard let s = session, s.connection == askedConnection, case .live = s.phase else { return }
            if (s.lastFrame ?? .distantPast) < at { s.reconnect() }
        }
        #endif
        .sheet(isPresented: $help) { VNCHelp() }
        #if os(iOS)
        .sheet(isPresented: $pipLog) { VNCPiPDiagnostics() }
        #endif
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen-drawer")
    }

    // MARK: Bars

    private var topBar: some View {
        HStack(spacing: Theme.Space.s) {
            Button { close() } label: { barIcon(Theme.Icon.close) }
            .themedButton()
            .accessibilityLabel("Close Screen")
            .accessibilityIdentifier("screen-close")
            Spacer(minLength: Theme.Space.s)
            HStack(spacing: Theme.Space.xs) {
                Circle().fill(dot).scaledFrame(width: Theme.Size.dot, height: Theme.Size.dot)
                Text(name).font(Theme.Font.bodyStrong).lineLimit(1)
                Text(phaseLabel).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("screen-phase")
            Spacer(minLength: Theme.Space.s)
            Button { help = true } label: { barIcon(Theme.Icon.help) }
                .themedButton()
                .accessibilityLabel("Help")
                .accessibilityIdentifier("vnc-help")
            moreMenu
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
    }

    /// A top-bar button's glyph in one square for every button: a themed button is sized by its label, and glyphs differ
    /// in height (⋯ is a few points tall).
    private func barIcon(_ name: String) -> some View {
        ThemedIcon(name).font(Theme.Font.labelStrong).scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
    }

    private var moreMenu: some View {
        Menu {
            #if os(iOS) || os(visionOS)
            // On the Mac updateNSView never reads the mode; the picker would only change the iPhone's setting (#3893).
            Picker("Mode", selection: Binding(get: { mode }, set: { mode = $0; VNCMode.set($0, for: ref) })) {
                Label("Mouse Mode", systemImage: Theme.Icon.pointer).tag(VNCMode.mouse)
                Label("Trackpad Mode", systemImage: Theme.Icon.trackpad).tag(VNCMode.trackpad)
            }
            let displays = offeredDisplays
            if displays.count > 1 {
                Picker(selection: Binding(get: { shownDisplay?.id }, set: { display = $0; VNCDisplayChoice.set($0, for: ref) })) {
                    Text("All Displays").tag(String?.none)
                    ForEach(Array(displays.enumerated()), id: \.element.id) { i, d in
                        Text("Display \(i + 1)").tag(Optional(d.id))
                    }
                } label: {
                    Label("Displays", systemImage: Theme.Icon.displays)
                }
                .pickerStyle(.menu)
                .accessibilityIdentifier("vnc-displays")
            }
            #endif
            Button { centerPointer() } label: { Label("Center Pointer", systemImage: Theme.Icon.pointerCenter) }
            #if os(iOS) || os(visionOS)
            if VNCPiP.supported {
                Button { toPictureInPicture() } label: { Label("Picture in Picture", systemImage: Theme.Icon.pictureInPicture) }
            }
            Button { pipLog = true } label: { Label("Picture in Picture Diagnostics", systemImage: Theme.Icon.help) }
            #else
            Button { toPictureInPicture() } label: { Label("Picture in Picture", systemImage: Theme.Icon.pictureInPicture) }
            #endif
            #if DEBUG
            if let s = session { Text(String(format: "%.0f 帧/秒 · %.1f Mbit/s", s.fps, s.mbps) + (s.latency.map { String(format: " · %.0f 毫秒", $0) } ?? "")) } // i18n-ignore
            #endif
        } label: {
            barIcon(Theme.Icon.more)
        }
        .themedButton()
        .accessibilityLabel("More")
        .accessibilityIdentifier("vnc-more")
    }

    private var bottomBar: some View {
        HStack(spacing: Theme.Space.m) {
            Menu {
                Button { Task { await pasteFromPhone(andPaste: true) } } label: { Label(Self.fromPhone, systemImage: Theme.Icon.paste) }
                Button { Task { await pasteFromPhone(andPaste: false) } } label: { Label("Copy to Mac's Clipboard Only", systemImage: Theme.Icon.clipboard) }
                Button { Task { await copyToPhone() } } label: { Label(Self.toPhone, systemImage: Theme.Icon.copy) }
            } label: {
                ThemedIcon(Theme.Icon.clipboard).font(Theme.Font.heading).scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
            }
            .themedButton()
            .accessibilityLabel("Clipboard")
            .accessibilityIdentifier("vnc-clipboard")
            Spacer(minLength: Theme.Space.s)
            if waiting {
                Button { handBack() } label: {
                    Label("I'm Done", systemImage: Theme.Icon.handBack).font(Theme.Font.labelStrong).lineLimit(1)
                }
                .themedButton(prominent: true)
                .accessibilityIdentifier("vnc-done")
                Spacer(minLength: Theme.Space.s)
            }
            #if os(iOS) || os(visionOS)
            Button { typing.toggle() } label: {
                ThemedIcon(Theme.Icon.keyboard).font(Theme.Font.heading).scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
            }
            .themedButton(prominent: typing)
            .accessibilityLabel("Keyboard")
            .accessibilityIdentifier("vnc-keyboard")
            #endif
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        #if os(iOS) || os(visionOS)
        .background { if let s = session { VNCKeyCatcher(session: s, modifiers: modifiers, typing: $typing).frame(width: Theme.Size.hairline, height: Theme.Size.hairline) } }
        #endif
    }

    #if os(iOS) || os(visionOS)
    static let fromPhone = String(localized: "Paste from iPhone"), toPhone = String(localized: "Copy to iPhone")
    #else
    static let fromPhone = String(localized: "Paste from This Mac"), toPhone = String(localized: "Copy to This Mac")
    #endif

    private func stepLine(_ a: ComputerActivity) -> some View {
        Text("Now: \(a.step)").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, Theme.Space.page)
            .padding(.bottom, Theme.Space.xs)
            .accessibilityIdentifier("screen-step")
    }

    // MARK: Content

    @ViewBuilder private func content(_ s: RFBSession) -> some View {
        switch s.phase {
        case .live:
            #if os(iOS) || os(visionOS)
            VNCScreen(session: s, mode: mode, interactive: true, region: shownDisplay?.frame) { v in DispatchQueue.main.async { screenView = v } }
            #else
            VNCScreen(session: s, mode: mode, interactive: true)
            #endif
        case .signIn(let failed):
            VNCSignIn(name: name, account: Self.account(model.machine(ref)?.home), failed: failed) { account, password, remember in
                s.signIn(account: account, password: password, remember: remember)
            }
        case .connecting:
            VStack(spacing: Theme.Space.s) {
                ProgressView()
                Text("Connecting to \(name)’s screen…").font(Theme.Font.secondary).foregroundStyle(.secondary)
            }
        case .closed(let why):
            VStack(spacing: Theme.Space.m) {
                Text(why ?? String(localized: "Disconnected.")).font(Theme.Font.secondary).foregroundStyle(.secondary).multilineTextAlignment(.center)
                Button("Reconnect") { s.start() }.themedButton(prominent: true).lineLimit(1)
            }
            .padding(Theme.Space.xl)
        }
    }

    #if os(iOS) || os(visionOS)
    /// The Mac's displays to choose from: the server's screens, else the node's arrangement, else none (VNCDisplays).
    private var offeredDisplays: [VNCDisplay] {
        guard let s = session else { return [] }
        return VNCDisplays.layout(server: s.screens, machine: model.screenDisplays(ref), framebuffer: s.size)
    }

    /// The chosen display while it is there; nil shows them all.
    private var shownDisplay: VNCDisplay? {
        guard let display else { return nil }
        return offeredDisplays.first { $0.id == display }
    }
    #endif

    /// The Mac account's short name, from its home directory (/Users/<name>).
    static func account(_ home: String?) -> String {
        guard let home, home.hasPrefix("/Users/") else { return "" }
        return String(home.dropFirst("/Users/".count).split(separator: "/").first ?? "")
    }

    private var dot: Color {
        switch session?.phase {
        case .live: Theme.positive
        case .closed: Theme.danger
        default: Theme.caution
        }
    }

    private var phaseLabel: String {
        switch session?.phase {
        case .live: String(localized: "Connected [V N C Page 5be032]")
        case .signIn: String(localized: "Sign-in needed")
        case .closed: String(localized: "Not connected")
        default: String(localized: "Connecting")
        }
    }

    // MARK: Actions

    private func centerPointer() {
        #if os(iOS) || os(visionOS)
        screenView?.centerPointer()
        #else
        if let s = session { s.move(to: CGPoint(x: s.size.width / 2, y: s.size.height / 2)) }
        #endif
    }

    private func toPictureInPicture() {
        VNCPiP.shared.start(ref, from: from)
        #if os(macOS)
        onClose()
        #endif
        // iOS: the page goes once the small window has started (VNCPiP), or stays, saying why it could not.
    }

    private func handBack() {
        guard let from else { return }
        Task { _ = await model.send(from, text: String(localized: "I'm done on the computer. Go ahead.")) }
        onClose()
    }

    private func close() {
        if waiting { handBack() } else { onClose() }
    }

    private func pasteFromPhone(andPaste: Bool) async {
        #if os(iOS) || os(visionOS)
        let text = UIPasteboard.general.string
        #else
        let text = NSPasteboard.general.string(forType: .string)
        #endif
        guard let text, !text.isEmpty, let s = session else {
            model.toast = Toast(String(localized: "No text on the clipboard"), style: .error)
            return
        }
        let ok = andPaste ? await s.paste(text) : await s.setMacClipboard(text)
        model.toast = ok ? Toast(andPaste ? String(localized: "Pasted on \(name)") : String(localized: "Copied to \(name)’s clipboard"))
            : Toast(andPaste ? String(localized: "Couldn't write to \(name)'s clipboard, so nothing was pasted") : String(localized: "Couldn't write to \(name)'s clipboard"), style: .error)
    }

    private func copyToPhone() async {
        guard let s = session, let text = await s.readMacClipboard(), !text.isEmpty else {
            model.toast = Toast(String(localized: "No text on \(name)’s clipboard"), style: .error)
            return
        }
        #if os(iOS) || os(visionOS)
        UIPasteboard.general.string = text
        #else
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
        model.toast = Toast(String(localized: "Copied \(name)’s clipboard"))
    }
}

/// The Mac's sign-in for its Screen Sharing, when this device keeps none (or the kept one was refused).
private struct VNCSignIn: View {
    let name: String
    @State var account: String
    let failed: String?
    let submit: (String, String, Bool) -> Void
    @State private var password = ""
    @State private var remember = true

    init(name: String, account: String, failed: String?, submit: @escaping (String, String, Bool) -> Void) {
        self.name = name
        _account = State(initialValue: account)
        self.failed = failed
        self.submit = submit
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text("Sign In to This Mac's Screen Sharing").font(Theme.Font.heading)
            Text(remember ? String(localized: "Use your macOS account and password on \(name). They go only to that Mac; if remembered, they stay in this device’s keychain and are never uploaded.")
                 : String(localized: "Use your macOS account and password on \(name). They go only to that Mac and aren’t saved."))
                .font(Theme.Font.label).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            if let failed {
                Text(failed).font(Theme.Font.label).foregroundStyle(Theme.danger).fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("vnc-signin-failed")
            }
            TextField(String(localized: "Accounts"), text: $account)
                .textContentType(.username)
                #if os(iOS) || os(visionOS)
                .textInputAutocapitalization(.never)
                #endif
                .autocorrectionDisabled()
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("vnc-account")
            SecureField(String(localized: "Password"), text: $password)
                .textContentType(.password)
                .textFieldStyle(.roundedBorder)
                .onSubmit(go)
                .accessibilityIdentifier("vnc-password")
            Toggle(ScreenKeep.label, isOn: $remember).font(Theme.Font.secondary).accessibilityIdentifier("vnc-remember")
            Button(action: go) { Text("Connect [V N C Page a55741]").font(Theme.Font.labelStrong).lineLimit(1).frame(maxWidth: .infinity) }
                .themedButton(prominent: true)
                .disabled(account.trimmingCharacters(in: .whitespaces).isEmpty || password.isEmpty)
                .accessibilityIdentifier("vnc-connect")
        }
        .padding(Theme.Space.l)
        .card()
        .padding(Theme.Space.l)
        .frame(maxWidth: Theme.Size.box380)
    }

    private func go() {
        let a = account.trimmingCharacters(in: .whitespaces)
        guard !a.isEmpty, !password.isEmpty else { return }
        submit(a, password, remember)
        password = ""
    }
}

/// 「在这台手机上记住」 on the phone, 「在这台 Mac 上记住」 on the Mac.
enum ScreenKeep {
    #if os(macOS)
    static let label = String(localized: "Remember on This Mac")
    #else
    static let label = String(localized: "Remember on This iPhone")
    #endif
}

/// Help: how the touches, the keyboard and the clipboard work, and handing the Mac back.
struct VNCHelp: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section("Moving Around") {
                    #if !os(macOS)
                    row(Theme.Icon.pointer, String(localized: "Mouse mode (default)"), String(localized: "Tap to click there. Drag with one finger to drag. Drag with two fingers to scroll. Two-finger tap or touch and hold to right-click."))
                    row(Theme.Icon.search, String(localized: "Zoom"), String(localized: "Pinch to zoom in and out. When zoomed in, dragging with two fingers moves the view instead of scrolling."))
                    row(Theme.Icon.displays, String(localized: "Displays"), String(localized: "When the Mac has more than one display, choose one in “…” › Displays to show only that display. Zooming, taps and the pointer then stay on it."))
                    #else
                    row(Theme.Icon.pointer, String(localized: "Mouse"), String(localized: "Click for the left mouse button; right-click (or Control-click) for the right button; press and hold the mouse button and move to drag."))
                    row(Theme.Icon.trackpad, String(localized: "Scroll"), String(localized: "Turn the mouse wheel, or swipe with two fingers on a Magic Mouse or trackpad to scroll the view."))
                    row(Theme.Icon.search, String(localized: "Zoom"), String(localized: "There is no zoom gesture on the Mac; the view always shows the whole screen."))
                    #endif
                }
                Section("Typing and Clipboard") {
                    #if !os(macOS)
                    row(Theme.Icon.keyboard, String(localized: "Typing"), String(localized: "Tap the keyboard in the bottom bar. The row above the keyboard has Mac shortcuts: tap ⌘ ⌥ ⌃ ⇧ to hold, tap again to release, plus Esc, Tab, arrow keys, and ⌘C ⌘V ⌘Z ⌘Tab ⌘Space. Input methods such as Chinese work as usual; the committed text is pasted over through the clipboard."))
                    #else
                    row(Theme.Icon.keyboard, String(localized: "Typing"), String(localized: "Type directly on this Mac's keyboard; text, ⌘ ⌥ ⌃ ⇧ and shortcuts are all sent as-is to the Mac shown in the view."))
                    #endif
                    row(Theme.Icon.clipboard, String(localized: "Copy and Paste"), String(localized: "Tap the clipboard in the bottom bar: “\(VNCPage.fromPhone)” puts your text on the Mac’s clipboard and pastes it; “\(VNCPage.toPhone)” brings over the text on the Mac’s clipboard."))
                }
                #if !os(macOS)
                Section("When to Use Trackpad Mode") {
                    row(Theme.Icon.trackpad, String(localized: "Trackpad Mode"), String(localized: "Turn it on from “…” in the top-right corner. Move your finger to move the pointer, tap to click at the pointer, and tap then drag right away to drag. If the pointer drifts off, use “Center Pointer”."))
                }
                #endif
                Section("Picture in Picture") {
                    row(Theme.Icon.pictureInPicture, String(localized: "Picture in Picture"), String(localized: "“Picture in Picture” in “…” floats the screen over other apps and the Home Screen; tap it to come back here. Picture in Picture is view-only, and closing it disconnects."))
                }
                Section("Handing Back the Computer") {
                    row(Theme.Icon.handBack, String(localized: "I'm Done"), String(localized: "When an agent is waiting for you to do something on the computer, tap “I'm Done” in the bottom bar when you're finished and it carries on. Closing the screen also hands it back."))
                }
            }
            .navigationTitle("Using VNC")
            #if os(iOS) || os(visionOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { dismiss() } label: { ThemedIcon(Theme.Icon.close) }.accessibilityIdentifier("vnc-help-close")
                }
            }
        }
        .accessibilityIdentifier("vnc-help-page")
    }

    private func row(_ icon: String, _ title: String, _ text: String) -> some View {
        Label {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(title).font(Theme.Font.body)
                Text(text).font(Theme.Font.label).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
        } icon: {
            ThemedIcon(icon).foregroundStyle(Theme.accent)
        }
    }
}

extension RFBSession {
    /// One-Tap Unlock (#1507 a-2): a Shift wakes the lock screen and puts the cursor in its password field, then the kept
    /// password and Return; the hub only notes that it was unlocked.
    func unlockWithKeptPassword() {
        guard let kept = ScreenCredentials.load(ref) else { return }
        press(Self.shift)
        Task {
            try? await Task.sleep(for: .milliseconds(800))
            for ch in kept.password.unicodeScalars { press(Self.keysym(ch)) }
            press(VNCKeys.enter)
            _ = try? await AppModel.shared.api(ref.group)?.computerJob(machine: ref.id, "unlocking")
        }
    }
}
