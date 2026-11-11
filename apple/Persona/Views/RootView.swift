import MarkdownUI
import BeautifulUI
import SwiftUI

struct RootView: View {
    @Environment(AppModel.self) private var model
    /// The system's text size, before Small / Standard / Large moves it.
    @Environment(\.dynamicTypeSize) private var systemTypeSize
    /// How far down the toasts outside the shell's window start: below the navigation bar's buttons, as the shell's do,
    /// not on them (the owner asked, #138: a toast covered Back). The Mac's stay at the top of the window.
    private var toastClearance: CGFloat {
        #if os(iOS)
        ShellNavigationClearance.needed(at: model.textSize.applied(to: systemTypeSize))
        #else
        0
        #endif
    }
    @Environment(\.horizontalSizeClass) private var widthClass
    #if os(iOS)
    /// The Agents page's New Session sheet (`\.newAgentShown`), here so a fold or an unfold keeps it (#2836).
    @State private var newAgentShown = false
    /// The tree this window shows now (`\.shownTree`).
    @State private var shownTree = ShownTree()
    @Environment(\.scenePhase) private var phase
    /// The window's width class when the app was last the active one: what its pages keep while it is away (#2836,
    /// `heldWidthClass`).
    @State private var activeWidthClass: UserInterfaceSizeClass?
    #endif
    #if DEBUG
    /// `-swapTreeAt <seconds>`: the shell for a moment that long after launch and back, the tree swap a fold and an
    /// unfold make, on any simulator; only this view's choice flips, as before #2836 (a real fold changes the pages' own
    /// width class too). With `-swapTreeAway YES` the whole app's width class flips instead, from above this view
    /// (`TreeSwapFlip`), while the app counts as away, as while iOS takes its snapshots: nothing is built again
    /// (`heldWidthClass`, `probe.tree-inner`). With `-swapTreeWhole YES` the whole app's flips while it is active: a real
    /// fold and unfold, the pages' own width class with it (#2836).
    @State private var flipped = false
    /// The flips it made, and the swaps between the shell and the tabs (`probe.tree`, `-probes YES`): a test that needs
    /// the tree built again checks it was (#2626), one that needs it kept checks it was not (#2836).
    @State private var swaps = TreeSwaps()
    /// `-swapTreeAway YES`: the app counts as away while the width is flipped.
    @State private var away = false
    #else
    private let flipped = false
    private let away = false
    #endif

    /// The iPhone-width shell; regular-width and other platforms keep their own navigation.
    private var usesShell: Bool {
        #if os(visionOS)
        // Apple Vision Pro keeps the tabs, as the system's ornament (VisionShell), whatever the window's width.
        return false
        #elseif os(iOS)
        return (heldWidthClass ?? widthClass) == .compact || flipped
        #else
        return false
        #endif
    }

    #if os(iOS)
    /// The width class the app's pages are given instead of the window's (#2836); nil, the window's. iOS snapshots an app
    /// on its way to the background in other widths (iPhone Duo open: the outer screen's), and the whole tree was built
    /// again for it (the shell for the tabs, a stack for a split, a drawer for a column), losing what each part kept (an
    /// open sheet such as New Agent with what was typed in it, uploads under way, what was picked); and again on the way
    /// back. While the app is away, its pages keep the width class it had while it was the active one; back, they take
    /// the window's (folded meanwhile, the shell). Read in the same update as the phase: a width that changes with it
    /// does not get a turn.
    private var heldWidthClass: UserInterfaceSizeClass? { isAway ? activeWidthClass : nil }

    /// Not the active app (inactive, in the background), or a test says so (`-swapTreeAway`).
    private var isAway: Bool { phase != .active || away }
    #endif

    /// The shell or the tabs are on screen: not the welcome page, the engine step, the checklist after pairing (the same
    /// branches as `body`) or an extension's page in their place.
    private var pagesShown: Bool {
        model.isPaired && model.pairingEngineMachine == nil && !model.showsChecklist && model.extensionOpen == nil
    }

    /// The shell is on screen, and with it its toast window (`ToastWindow`): not on the welcome page, the engine step or
    /// the checklist after pairing (the same branches as `body`).
    private var shellShown: Bool { usesShell && model.isPaired && model.pairingEngineMachine == nil && !model.showsChecklist }

    /// The tabs are on screen at regular width (iPad, iPhone Duo open), with a toast window of
    /// their own (`ToastWindowHook`): the same branches as `body`, with no extension page in the app's place.
    private var tabsToastWindow: Bool {
        #if os(iOS)
        !usesShell && model.isPaired && model.pairingEngineMachine == nil && !model.showsChecklist && model.extensionOpen == nil
        #else
        false
        #endif
    }

    /// The toasts are drawn in this view's overlay: where no toast window is up (the welcome page, the engine step, the
    /// checklist, an extension page in the app's place, the Mac, Apple Vision Pro).
    private var overlayToasts: Bool { model.extensionOpen != nil || !(shellShown || tabsToastWindow) }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("RootView")
        #endif
        Group {
            if let ref = model.pairingEngineMachine {
                PairingEngineView(ref: ref)
            } else if model.isPaired, model.showsChecklist {
                // Just paired: what the main conversation still needs, before the app (#4195).
                OnboardingChecklistView()
            } else if model.isPaired {
                #if os(visionOS)
                RootTabs().visionShell()
                #elseif os(iOS)
                if usesShell { ShellView().modifier(BuiltTreeRoot()) } else { RootTabs().background { ToastWindowHook() }.modifier(BuiltTreeRoot()) }
                #else
                RootTabs()
                #endif
            } else {
                WelcomeView()
            }
        }
        #if DEBUG && os(iOS)
        .background { InnerWidthProbe() } // below the held width class, across the branches
        #endif
        .task(id: model.checklistPending && model.hubFeatures.contains(AssistantReadiness.feature)) {
            if model.checklistPending { await model.refreshReadiness() }
        }
        // The plugins' main-navigation entries and cards, for Settings' badge (docs/extensions/extensions.md「UI slots」).
        .task(id: ExtensionSlots.source(model)) {
            let hub = ExtensionSlots.source(model).hub
            await ExtensionSlots.shared.reload(for: hub)
            // Which inbox sources a plugin draws (placement inbox_item): known before an item is opened.
            await InboxItemSlots.shared.reload(for: hub)
        }
        .extensionThemes()
        .extensionSystemPresentation()
        // A new style, Theme Color or persona rebuilds the whole tree (tokens are static); the open tab and chat are remembered.
        .id(model.look)
        #if os(iOS)
        // The Agents page's New Session, presented here, above the shell / tabs swap: a fold or an unfold while it is open
        // builds the page again, and the sheet went with it, what was typed in it too (#2836). It still goes when something
        // else takes the pages' place (the welcome page after unpairing, an extension's page), as it went with the page.
        .environment(\.newAgentShown, $newAgentShown)
        .sheet(isPresented: $newAgentShown) {
            NewChatView { session in
                newAgentShown = false
                model.selectedTab = .chats
                model.openChat = session.key
            }
            .id(model.look) // outlives RootView's rebuild for a new look, as Settings' sheet does
        }
        .onChange(of: pagesShown) { _, shown in if !shown { newAgentShown = false } }
        // Which tree is shown: one built before, still alive a moment after a swap, leaves the window to it.
        .environment(\.shownTree, shownTree)
        // While the app is away its pages keep the width class it had while it was the active one (#2836): the widths iOS
        // snapshots it in on its way to the background build nothing again.
        .transformEnvironment(\.horizontalSizeClass) { width in if let held = heldWidthClass { width = held } }
        .onChange(of: widthClass, initial: true) { _, now in if !isAway { activeWidthClass = now } }
        .onChange(of: isAway) { _, nowAway in if !nowAway { activeWidthClass = widthClass } }
        #endif
        #if DEBUG
        .task {
            let at = UserDefaults.standard.double(forKey: "swapTreeAt")
            guard at > 0 else { return }
            try? await Task.sleep(for: .seconds(at))
            if UserDefaults.standard.bool(forKey: "swapTreeAway") {
                away = true
                try? await Task.sleep(for: .milliseconds(200)) // away first, as the snapshots come after the phase
                TreeSwapDebug.shared.compact = true
                try? await Task.sleep(for: .seconds(1))
                TreeSwapDebug.shared.compact = false
                try? await Task.sleep(for: .milliseconds(200))
                away = false
            } else if UserDefaults.standard.bool(forKey: "swapTreeWhole") {
                TreeSwapDebug.shared.compact = true
                try? await Task.sleep(for: .seconds(1))
                TreeSwapDebug.shared.compact = false
            } else {
                flipped = true
                try? await Task.sleep(for: .seconds(1))
                flipped = false
            }
            swaps.count += 1
        }
        .onChange(of: usesShell) { swaps.rebuilt += 1 }
        .screenProbe("tree", swaps)
        // LookBlink drives UIWindowScene overrides, so it only exists on iOS and visionOS.
        #if os(iOS) || os(visionOS)
        .task { await LookBlink.run() }
        #endif
        #endif
        .tint(Theme.accent)
        .fontDesign(model.style.fontDesign)
        .dynamicTypeSize(model.textSize.applied(to: systemTypeSize))
        .environment(\.shellBaseTypeSize, model.textSize.applied(to: systemTypeSize))
        .buiTheme(.persona())
        // No scroll bars anywhere (2026-10-31, the user: they look bad); the environment reaches every ScrollView,
        // List and sheet below. UIKit scroll views (MonoTextView, the terminal, web views) turn theirs off themselves.
        .scrollIndicators(.hidden)
        .markdownInlineImageProvider(MathInlineImages()).markdownImageProvider(WebMarkdownImages()) // inline formulas in every Markdown (MathMarkdown)
        // On iOS the shell and the tabs show the toasts in a window of their own (`ToastWindow`), over their covers and
        // sheets too: drawn here, the tabs' Saved to Photos was under the image viewer and the share card (#1492). Where
        // there is no such window they are drawn here: a pairing link that failed on the welcome page said nothing (#4273).
        .overlay(alignment: .top) { if overlayToasts { ToastOverlay().padding(.top, toastClearance) } }
        #if os(iOS) || os(visionOS)
        // Settings › Appearance › Light & Dark on this scene's window (and any other open one) as it first shows.
        .onAppear { model.colorScheme.apply() }
        // The Theme Color on the windows too, for what UIKit draws in the window's tint (Settings' row icons, `WindowTint`).
        .onChange(of: model.look, initial: true) { WindowTint.apply() }
        #endif
        #if DEBUG || PERF_SIGNPOST
        .task { MainThreadWatchdog.shared.start() }
        #endif
        #if DEBUG
        .overlay(alignment: .bottomTrailing) { PerfCountProbe() }
        // UI tests see the small window (VNC in picture in picture, #1343), which is the system's.
        .overlay(alignment: .topLeading) {
            if model.vncPiP != nil {
                Color.clear.frame(width: Theme.Size.hairline, height: Theme.Size.hairline)
                    .accessibilityElement().accessibilityLabel("VNC 小窗开着").accessibilityIdentifier("vnc-pip-on") // i18n-ignore
            }
        }
        .screenProbe("connection", ConnectionShown(shown: "\(model.connection.shown)", network: model.connection.networkUp))
        .htmlCardSample()
        .voiceOrbSample()
        .healthTrendsSample()
        #endif
        .alert("Claude Wants to Act on Your iPhone", isPresented: Binding(
            get: { model.pendingConfirmation != nil },
            // Gone without an answer: Deny for the one this alert showed. Its own button may have answered it already,
            // and the next one may be on screen by the time this comes: that one is not touched (#4456).
            set: { [shown = model.pendingConfirmation?.id] in if !$0, let shown { model.phoneConfirmations.answer(shown, false) } }
        ), presenting: model.pendingConfirmation) { c in
            Button("Allow [Notification Actions ce7ef2]") { model.phoneConfirmations.answer(c.id, true) }
            Button("Deny", role: .cancel) { model.phoneConfirmations.answer(c.id, false) }
        } message: { c in
            Text(c.summary)
        }
        .sendRefusalAlert(model)
        // Add Computer / Confirm Computer from anywhere (the readiness card's add_computer / confirm_computer, the Agents list).
        .addComputerPresentation()
        .loadsChannels() // the names under a message that came in from a channel (「来自 微信」)
        #if os(iOS) || os(visionOS)
        .onChange(of: LarkHomePreference.shared.shows) { _, enabled in
            if !enabled, model.selectedTab == .feishu { model.selectedTab = .assistant }
        }
        #endif
    }
}

@MainActor private struct RootTabs: View {
    @Environment(AppModel.self) private var model
    #if os(iOS)
    @State private var pad = PadRouter()
    #endif

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("RootTabs")
        #endif
        @Bindable var model = model
        TabView(selection: $model.selectedTab) {
            AssistantRootTab()
            AgentsRootTab()
            #if os(iOS) || os(visionOS)
            if LarkHomePreference.shared.shows { FeishuRootTab() }
            #endif
            #if !os(iOS)
            InboxRootTab()
            SettingsRootTab()
            #endif
        }
        #if os(iOS)
        .modifier(PadRoutes(router: pad))
        #endif
    }
}

#if os(iOS)
/// Where the iPad's routes to Inbox and Settings land without their tabs: every route in the app still
/// sets `AppModel.selectedTab` (notifications, Siri, the inbox's rows…). `.mail` opens the inbox on the Persona tab's page,
/// beside its sidebar; `.settings` the sheet, over the tab that was in front. As the iPhone's shell does (`ShellState.route`).
@MainActor @Observable final class PadRouter: TabRouter {
    /// What the Persona tab is asked to show; `serial` makes the same request count twice.
    private(set) var request: (page: ShellPage, serial: Int)?
    /// The tab in front, for Settings to go back to.
    @ObservationIgnored private var front = AppTab.assistant

    func show(_ page: ShellPage) {
        request = (page, (request?.serial ?? 0) + 1)
    }

    func attach(to model: AppModel) {
        model.tabRouter = self
        route(model.selectedTab, in: model)
    }

    func detach(from model: AppModel) {
        guard model.tabRouter === self else { return }
        model.tabRouter = nil
    }

    func route(_ t: AppTab, in model: AppModel) {
        switch t {
        case .mail:
            show(.inbox)
            front = .assistant
            model.selectedTab = .assistant
        case .settings:
            model.shellSettings = true
            model.selectedTab = front
        default:
            front = t
        }
    }
}

/// The iPad's routes (`PadRouter`) and its Settings, on the tabs: a sheet on the iPad; on a phone (iPhone Duo open) the
/// whole window, one column across, as ChatGPT shows its account page there, not a sheet over half of it (#3701).
private struct PadRoutes: ViewModifier {
    @Environment(AppModel.self) private var model
    let router: PadRouter
    private let phone = UIDevice.current.userInterfaceIdiom == .phone

    func body(content: Content) -> some View {
        @Bindable var model = model
        content
            .environment(\.padRouter, router)
            .sheet(isPresented: phone ? .constant(false) : $model.shellSettings) { settings.presentationSizing(.page) }
            .fullScreenCover(isPresented: phone ? $model.shellSettings : .constant(false)) {
                settings
                    .environment(\.horizontalSizeClass, .compact)
                    .accessibilityAction(.escape) { model.shellSettings = false } // VoiceOver's scrub, as on a sheet
            }
            .onAppear { router.attach(to: model) }
            // Not for Settings' cover, which takes the tabs off the window: a route asked meanwhile (Settings itself, from
            // an extension's link) waited for it to close and opened it again.
            .onDisappear { if !model.shellSettings { router.detach(from: model) } }
    }

    /// It outlives RootView's rebuild for a new look, so its identity follows the look too.
    private var settings: some View {
        SettingsView()
            .environment(\.settingsSheet, true)
            .id(model.look)
    }
}

extension EnvironmentValues {
    /// Set on the iPad's tabs, where Inbox and Settings live in Persona's sidebar; nil elsewhere.
    @Entry var padRouter: PadRouter?
}

/// The Assistant | Agents switch as a row of its own over a page of a phone's tabs (`pageTabSwitch`): Agents' page
/// column and Messages, over the chats opened there too. Assistant has it in its floating bar (`AssistantTopBar`), at the
/// same height. Agents' list options stay on the list's own ⋯ beside it.
struct PageTabSwitchRow: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        @Bindable var model = model
        ViewThatFits(in: .horizontal) {
            ShellSwitch(tab: $model.selectedTab) { EmptyView() } workMenu: { EmptyView() }
                .dynamicTypeSize(...Theme.Shell.topBarLargestTypeSize)
            ShellSwitch(tab: $model.selectedTab) { EmptyView() } workMenu: { EmptyView() }
                .dynamicTypeSize(...Theme.Shell.compactSwitchLargestTypeSize)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, AssistantTopBar.topInset)
        .padding(.bottom, Theme.Space.xs)
        .background(Theme.pageBackground)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("page-tab-switch")
    }
}

/// The switch as a row over a page opened in Agents' split column on a phone's tabs. The column's stack has it as an
/// inset (`SplitNavigation`), and that reached only its root page: an opened page covers the whole column, so the switch
/// went as soon as an agent was opened, and nothing led back to Assistant (#3680). Only where it shows: everywhere else
/// the page is left exactly as it was.
struct PageTabSwitchInset: ViewModifier {
    var shown: Bool

    func body(content: Content) -> some View {
        if shown { content.safeAreaInset(edge: .top, spacing: 0) { PageTabSwitchRow() } } else { content }
    }
}

/// A phone's tabs (`pageTabSwitch`): no tab bar; the pages draw the switch.
private struct PageTabSwitch: ViewModifier {
    func body(content: Content) -> some View {
        if UIDevice.current.userInterfaceIdiom == .phone {
            content.toolbarVisibility(.hidden, for: .tabBar).environment(\.pageTabSwitch, true)
        } else {
            content
        }
    }
}
#endif

/// One tree RootView builds: the shell or the tabs, built again by a fold or an unfold, and by a new look.
@MainActor final class BuiltTree {}

/// The tree a window shows now (RootView's; nil: none yet). The trees built before it live on a moment after a fold or an
/// unfold swapped the shell and the tabs (seconds, on an iPad), or a new look built them again: they leave what is asked
/// of the window to the shown one (#2836: the old Agents page took the chat the new one opened again, and opened it where
/// no one saw it).
@MainActor final class ShownTree {
    var tree: BuiltTree?
}

#if os(iOS)
/// The tree under it as one `BuiltTree` (`\.builtTree`), the window's shown one from its appearing.
private struct BuiltTreeRoot: ViewModifier {
    @Environment(\.shownTree) private var shown
    @State private var tree = BuiltTree()

    func body(content: Content) -> some View {
        content
            .environment(\.builtTree, tree)
            .onAppear { shown?.tree = tree }
    }
}
#endif

extension EnvironmentValues {
    /// RootView's `ShownTree`, and the tree a page is in: whether it is the one shown.
    @Entry var shownTree: ShownTree? = nil
    @Entry var builtTree: BuiltTree? = nil
    /// The Agents page's New Session sheet, presented by RootView on iOS (it outlives the tree a fold or an unfold builds
    /// again, #2836); nil, the page presents its own.
    @Entry var newAgentShown: Binding<Bool>? = nil
    /// The Assistant | Agents switch is the pages' own, at their top, ChatGPT's Chat | Work, and the system's tab bar is
    /// hidden: a phone's tabs, at regular width (iPhone Duo open, where iOS draws the tab bar down the side of the screen,
    /// at its foot; #2623). The iPad keeps its tab bar along the top; false elsewhere.
    @Entry var pageTabSwitch = false
}

/// A tab's page, drawn again only by what it reads itself. Each tab's badge (the agents waiting, the inbox's Needs You) is read
/// by its `Tab`, and the hub changes them with nearly every push: without this the `Tab` built its page again each time,
/// the page ran its body (the Agents list's toolbar handed to UIKit again: the open ⋯ menu went back to its top while
/// choosing a machine; the open chat laid out again under the finger), TestFlight 70, docs/lessons/ios.md. The pages
/// take nothing from here, so nothing is lost.
private struct TabPage<Page: View>: View, Equatable {
    @ViewBuilder var page: () -> Page

    nonisolated static func == (a: Self, b: Self) -> Bool { true }

    var body: some View { page() }
}

@MainActor private struct AssistantRootTab: TabContent {
    @Environment(AppModel.self) private var model

    var body: some TabContent<AppTab> {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AssistantRootTab")
        #endif
        Tab("Persona", systemImage: Theme.Icon.ai, value: AppTab.assistant) {
            TabPage {
                AssistantView()
                    .modifier(TabActivityScope(tab: .assistant))
                #if os(iOS)
                    .modifier(PageTabSwitch())
                #endif
                #if PERF_SIGNPOST
                    .modifier(PerfTabMount("assistant"))
                #endif
            }
            .equatable()
        }
        .badge(model.badges.assistant)
    }
}

@MainActor private struct AgentsRootTab: TabContent {
    @Environment(AppModel.self) private var model

    var body: some TabContent<AppTab> {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AgentsRootTab")
        #endif
        Tab("Agents", systemImage: Theme.Icon.agents, value: AppTab.chats) {
            TabPage {
                ChatsView()
                    .modifier(TabActivityScope(tab: .chats))
                #if os(iOS)
                    .modifier(PageTabSwitch())
                #endif
                #if PERF_SIGNPOST
                    .modifier(PerfTabMount("agents"))
                #endif
            }
            .equatable()
        }
        .badge(model.badges.agents)
    }
}

@MainActor private struct InboxRootTab: TabContent {
    var body: some TabContent<AppTab> {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("InboxRootTab")
        #endif
        Tab(String(localized: "Inbox"), systemImage: Theme.Icon.inbox, value: AppTab.mail) {
            TabPage {
                InboxView()
                    .modifier(TabActivityScope(tab: .mail))
                #if PERF_SIGNPOST
                    .modifier(PerfTabMount("inbox"))
                #endif
            }
            .equatable()
        }
        .badge(InboxStore.shared.unreadCount)
    }
}

@MainActor private struct SettingsRootTab: TabContent {
    @Environment(AppModel.self) private var model

    var body: some TabContent<AppTab> {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("SettingsRootTab")
        #endif
        Tab(String(localized: "Settings"), systemImage: Theme.Icon.settings, value: AppTab.settings) {
            TabPage {
                SettingsView()
                    .modifier(TabActivityScope(tab: .settings))
                #if PERF_SIGNPOST
                    .modifier(PerfTabMount("settings"))
                #endif
            }
            .equatable()
        }
        .badge(badge) // Demo Mode is left from Settings; the plugins' entries are on it (docs/extensions/extensions.md「UI slots」, badges)
    }

    /// Demo; else what the plugins' main-navigation entries' badges add up to (only words: a dot).
    private var badge: Text? {
        if model.isDemo { return Text("Demo") }
        let total = ExtensionSlots.shared.total
        if let text = CountBadge.text(total.count) { return Text(text) }
        return total.dot ? Text("•") : nil
    }
}

#if DEBUG
/// What the titles say about the link (`probe.connection`): Waiting for Network / Connecting… / Updating…, or nothing (online).
nonisolated struct ConnectionShown: Encodable, Equatable {
    var shown: String
    var network: Bool
}
#endif

#if os(iOS) || os(visionOS)
@MainActor private struct FeishuRootTab: TabContent {
    var body: some TabContent<AppTab> {
        Tab(String(localized: "Messages [Quote Views 4da199]"), systemImage: Theme.Icon.feishuChats, value: AppTab.feishu) {
            TabPage {
                LarkConversationsView().modifier(TabActivityScope(tab: .feishu))
                #if os(iOS)
                    .modifier(PageTabSwitch())
                #endif
            }
            .equatable()
        }
    }
}
#endif

#if DEBUG
/// `probe.tree`: how many times `-swapTreeAt` flipped the width class (`count`), and how many times the tree went between
/// the shell and the tabs (`rebuilt`: twice for a flip while the app is active, not at all while it is away).
struct TreeSwaps: Encodable, Equatable {
    var count = 0
    var rebuilt = 0
}

/// `-swapTreeAway YES`'s flip, for the whole app (#2836): `TreeSwapFlip` gives RootView a compact width class while it is
/// set, as iOS does for its snapshots (iOS only; the Mac's RootView has no shell to swap).
@MainActor @Observable final class TreeSwapDebug {
    static let shared = TreeSwapDebug()
    var compact = false
}

#if os(iOS)
/// Above RootView (PersonaApp): `TreeSwapDebug`'s flip.
struct TreeSwapFlip: ViewModifier {
    @State private var debug = TreeSwapDebug.shared

    func body(content: Content) -> some View {
        let compact = debug.compact
        content.transformEnvironment(\.horizontalSizeClass) { width in if compact { width = .compact } }
    }
}

/// `probe.tree-inner`: how many times the pages below RootView saw their width class change; none while the app is away,
/// whatever RootView's own does (#2836).
private struct InnerWidthProbe: View {
    @Environment(\.horizontalSizeClass) private var width
    @State private var seen = InnerWidthChanges()

    var body: some View {
        Color.clear
            .frame(width: Theme.Size.line, height: Theme.Size.line)
            .allowsHitTesting(false)
            .onChange(of: width) { seen.count += 1 }
            .screenProbe("tree-inner", seen)
    }
}

struct InnerWidthChanges: Encodable, Equatable {
    var count = 0
}
#endif
#endif
