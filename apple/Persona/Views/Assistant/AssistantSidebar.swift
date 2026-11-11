import BeautifulUI
import PersonaKit
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

#if os(iOS) || os(visionOS)
/// How far the Persona tab's sidebar is out (0 closed … 1 open). Its own object so following the finger only redraws
/// the page's motion modifier (offset, scale, opacities) and moves two UIKit views, never the conversation or the sidebar.
@Observable final class AssistantDrawer {
    var progress: CGFloat = 0 {
        didSet {
            if (progress > 0) != isOut { isOut = progress > 0 }
            applyToUIKit()
        }
    }
    /// Out at all: changes only when the sidebar starts to come out or is fully back, so views that only care
    /// whether it is out (the sidebar clearing its search) do not redraw on every frame of a drag.
    private(set) var isOut = false
    /// The sidebar's width, measured by `AssistantDrawerLayout`.
    @ObservationIgnored var width: CGFloat = 300 {
        didSet { if inPlace, width != oldValue { applyToUIKit() } }
    }
    /// Under iPhone Duo's side bars (`shellRailEdge`) the page stays where it is and the sidebar comes in over it from
    /// the leading edge (#3755): the shell's pages hold navigation controllers, and moved sideways under those bars they
    /// lay their pages out again on every frame. Set by `AssistantDrawerLayout`.
    @ObservationIgnored var inPlace = false {
        didSet { if inPlace != oldValue { applyToUIKit() } }
    }
    @ObservationIgnored var dragStart: CGFloat = 0
    /// The tab bar fades out as the sidebar comes out (it would sit over the sidebar's bottom row and repeat its Settings).
    /// Faded, not hidden: hiding it changes the page's safe area, and the whole conversation was laid out again on the
    /// first frame of every drag and the last of every close (20–40 ms each, sampled). Set by `DrawerTabBarProbe`.
    @ObservationIgnored weak var tabBar: UITabBar? {
        didSet { applyToUIKit() }
    }
    /// The sidebar's own hosting view (`DrawerSidebarHost`): lifted vertically here, hidden while closed.
    @ObservationIgnored weak var sidebarView: UIView? {
        didSet { applyToUIKit() }
    }
    /// The screen's own bottom inset (the home indicator) without the tab bar: the sidebar reaches down to it.
    var screenBottom: CGFloat = 0
    /// A settle's animation is running: the sidebar is hidden when it ends, not when it starts.
    @ObservationIgnored private var settling = false

    var isOpen: Bool { isOut }

    /// The UIKit side of the progress: the tab bar fades and the stationary sidebar lifts slightly. Inside `settle`
    /// they animate with UIKit, on the same spring as the page.
    func applyToUIKit() {
        if let tabBar {
            // The bar's own container, when it holds only the bar: the tab bar controller sets the bar's alpha back to
            // 1 whenever SwiftUI refreshes the tab bar's visibility (a launch with the sidebar out showed it again).
            let fading = tabBar.superview.flatMap { $0.subviews == [tabBar] ? $0 : nil } ?? tabBar
            let alpha = 1 - progress
            if fading.alpha != alpha { fading.alpha = alpha }
            tabBar.isUserInteractionEnabled = progress == 0
            tabBar.accessibilityElementsHidden = progress > 0
        }
        if let sidebarView {
            if inPlace {
                // Over the page, which stays (iPhone Duo's side bars): in from the leading edge, under the finger.
                sidebarView.transform = CGAffineTransform(translationX: -(1 - progress) * width, y: 0)
            } else {
                // The page is the upper sheet: sliding it away uncovers a sidebar that is already underneath.
                // Keep every horizontal position and the text size fixed; only a subtle vertical lift adds depth.
                sidebarView.transform = CGAffineTransform(translationX: 0, y: (1 - progress) * Theme.Motion.drawerLift)
            }
            let hidden = progress == 0 && !settling
            if inPlace {
                // Opaque all the way in: inside `settle` the alpha would fade in with the slide and show the page through.
                UIView.performWithoutAnimation { sidebarView.alpha = hidden ? 0 : 1 }
            } else {
                sidebarView.alpha = hidden ? 0 : 1
            }
            sidebarView.isHidden = hidden
            sidebarView.accessibilityElementsHidden = hidden
        }
    }

    /// `velocity`: the finger's, in points per second, when a drag lets go (the spring carries on at that speed).
    func open(velocity: CGFloat = 0) {
        UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
        settle(at: 1, velocity: velocity)
    }

    func close(velocity: CGFloat = 0) { settle(at: 0, velocity: velocity) }

    /// A drag took hold: the finger takes over where the page is (even mid-animation).
    func dragBegan() {
        dragStart = progress
        if progress == 0 {
            UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
        }
    }

    /// The finger is `x` points to the right of where the drag took hold. Straight to the value: no animation.
    func drag(_ x: CGFloat) {
        var t = Transaction()
        t.disablesAnimations = true
        withTransaction(t) { progress = min(1, max(0, dragStart + x / max(width, 1))) }
    }

    /// The finger let go at `velocity` points per second; returns whether it settles open.
    @discardableResult func letGo(velocity: CGFloat) -> Bool {
        let opens = Self.settlesOpen(progress: progress, velocity: velocity, width: max(width, 1))
        if opens {
            if dragStart == 0 { Haptics.tap() }
            open(velocity: velocity)
        } else {
            close(velocity: velocity)
        }
        return opens
    }

    private func settle(at target: CGFloat, velocity: CGFloat) {
        let way = (target - progress) * max(width, 1)
        let share = abs(way) > 1 ? max(0, Double(velocity / way)) : 0
        let motion = Theme.Motion.drawer(velocity: share)
        #if DEBUG && os(iOS)
        DrawerProbe.note("settle", ["from": Double(progress), "to": Double(target), "share": share])
        #endif
        // The same spring for the tab bar and the sidebar (UIKit) as for the page (SwiftUI).
        settling = true
        UIView.animate(motion) {
            withAnimation(motion) { progress = target }
        } completion: { [weak self] in
            guard let self else { return }
            settling = false
            applyToUIKit()
        }
    }

    /// Where a drag lets go, like the system's back swipe: a flick goes its way, otherwise past half way (counting a
    /// little of where it is heading) opens. `velocity` in points per second.
    nonisolated static func settlesOpen(progress: CGFloat, velocity: CGFloat, width: CGFloat) -> Bool {
        SwipeRelease.goes(progress: progress, velocity: velocity, width: width)
    }

    /// Whether a touch that has moved `dx`, `dy` from where it went down is the drawer's: within 45° of horizontal
    /// (a thumb's slant; vertical scrolls are steeper), and while closed only to the right.
    nonisolated static func takes(dx: CGFloat, dy: CGFloat, isOpen: Bool) -> Bool {
        abs(dx) > abs(dy) && (isOpen || dx > 0)
    }
}

/// The sidebar under the page: the foreground page slides to the right, uncovering the stationary sidebar with a slight
/// vertical lift. The page stays as a rounded card at the edge (a tap on it, or a drag back, closes). Dragging to the right
/// anywhere on the page opens it (`DrawerPan`). Under iPhone Duo's side bars the page stays where it is, dimmed, and the
/// sidebar slides in over it instead (`AssistantDrawer.inPlace`, #3755).
///
/// The sidebar stays built while closed (hidden): building it on the first frame of a drag was most of that frame.
/// While the finger moves only `DrawerPageMotion` reads the progress (the sidebar and the tab bar are moved by UIKit,
/// `AssistantDrawer.applyToUIKit`), and it changes nothing but transforms and opacities: the corners, shadow and edge
/// line are fixed shapes faded in, not redrawn per frame. docs/operations/testing.md「Sidebar dragging」has the numbers.
struct AssistantDrawerLayout<Sidebar: View, Page: View>: View {
    let drawer: AssistantDrawer
    /// The page is at its root (nothing pushed): only then does a drag open the sidebar.
    var enabled: Bool
    /// Behind the sidebar: the system's grouped gray, or the shell's near-white (docs/apple/ios.md「New shell」).
    var backdrop: Color = Color(.secondarySystemBackground)
    /// The shell's Assistant | Agents strip: a drag sideways may page between them instead (`DrawerPan`).
    var pager: ShellPager? = nil
    @ViewBuilder var sidebar: Sidebar
    var page: Page
    /// iPhone Duo's bars down the side (the shell's `shellRailEdge`): the page stays and the sidebar comes over it.
    @Environment(\.shellRailEdge) private var railEdge

    var body: some View {
        GeometryReader { geo in
            let width = min(geo.size.width * 0.82, 380)
            let inPlace = railEdge != nil
            ZStack(alignment: .leading) {
                DrawerSidebarHost(drawer: drawer, content: sidebar.padding(.bottom, drawer.screenBottom)
                    .background { if inPlace { DrawerSidebarGround(backdrop: backdrop) } })
                    .frame(width: width)
                    // Down under the faded tab bar, to the screen's own edge.
                    .ignoresSafeArea(.container, edges: .bottom)
                    .zIndex(inPlace ? 1 : 0)
                page
                    .modifier(DrawerPageMotion(drawer: drawer, width: width, inPlace: inPlace))
            }
            .frame(width: geo.size.width, height: geo.size.height, alignment: .leading)
            .background { backdrop.ignoresSafeArea() }
            #if os(iOS)
            .gesture(DrawerPan(drawer: drawer, enabled: enabled, pager: pager))
            #endif
            .background { DrawerTabBarProbe(drawer: drawer) }
            .onAppear {
                drawer.width = width
                drawer.inPlace = inPlace
            }
            .onChange(of: width) { drawer.width = width }
            .onChange(of: inPlace) { drawer.inPlace = inPlace }
        }
    }
}

/// The sidebar's own ground while it lies over the page (iPhone Duo's side bars, `AssistantDrawer.inPlace`): the
/// backdrop it otherwise lies on, with the page card's shadow and edge line along its trailing side.
private struct DrawerSidebarGround: View {
    let backdrop: Color

    var body: some View {
        Rectangle()
            .fill(backdrop)
            .shadow(color: DrawerEdge.shadow, radius: DrawerEdge.shadowRadius)
            .overlay(alignment: .trailing) {
                Rectangle().fill(DrawerEdge.line).frame(width: Theme.Size.line)
            }
            .ignoresSafeArea()
    }
}

/// Where the page card (or, over the page, the sidebar) meets the other: a soft shadow, and in dark mode, where the
/// shadow is invisible, a hairline.
private enum DrawerEdge {
    static let shadow = Color.black.opacity(0.18)
    static let shadowRadius: CGFloat = 24
    static let line = Color.primary.opacity(0.12)
}

/// Finds the tab bar and the screen's bottom inset for `AssistantDrawer`.
private struct DrawerTabBarProbe: UIViewRepresentable {
    let drawer: AssistantDrawer

    func makeUIView(context: Context) -> Probe {
        let v = Probe()
        v.drawer = drawer
        v.isUserInteractionEnabled = false
        return v
    }

    func updateUIView(_ v: Probe, context: Context) {}

    final class Probe: UIView {
        weak var drawer: AssistantDrawer?

        override func didMoveToWindow() {
            super.didMoveToWindow()
            guard let drawer else { return }
            guard let window else {
                // Another tab (a notification can switch while the sidebar is out): the bar is that tab's again.
                // Without animation: the bar shows on the other tab, and the sidebar is in when the user comes back.
                drawer.progress = 0
                drawer.tabBar = nil
                return
            }
            var r: UIResponder? = self
            while let next = r?.next, !(next is UIViewController) { r = next }
            drawer.tabBar = (r?.next as? UIViewController)?.tabBarController?.tabBar
            drawer.screenBottom = window.safeAreaInsets.bottom
        }

        override func safeAreaInsetsDidChange() {
            super.safeAreaInsetsDidChange()
            if let window, let drawer, drawer.screenBottom != window.safeAreaInsets.bottom { drawer.screenBottom = window.safeAreaInsets.bottom }
        }
    }
}

/// The sidebar in a hosting controller of its own, with the page's whole environment. It stays built while closed
/// (building it on a drag's first frame cost that frame 15–20 ms on the simulator), and closed it has to be gone from
/// VoiceOver too: SwiftUI's `accessibilityHidden` left its search field, scroll view and rows in the tree. A UIKit view
/// can be hidden for real (`AssistantDrawer.sidebarView`), and moved and faded without SwiftUI updating anything.
///
/// The look (light or dark) is the one thing not taken from the page: it is the window's. The copied environment is only
/// as new as the last update SwiftUI sent here (and SwiftUI hands the same value down as the hosted controllers' traits),
/// while iOS draws the app-switcher snapshots in the other look as the app goes to the background: the sidebar was then
/// left with the other look's `Color.primary`, white text on the light backdrop, until the next update that did not
/// always come (#4496). The window is where Settings › Appearance › Light & Dark and the system set the look (`ColorSchemeChoice`).
private struct DrawerSidebarHost<Content: View>: UIViewControllerRepresentable {
    let drawer: AssistantDrawer
    let content: Content

    /// SwiftUI sets the frame, transform, alpha and hidden flag of the view it is given (every update put the
    /// sidebar back in place, shown): the hosting view inside it is the drawer's own.
    final class Container: UIViewController {
        let host: UIHostingController<AnyView>
        /// The page's content and environment, as last handed in (`set`): the look goes on top of it.
        private var content: AnyView
        private var environment: EnvironmentValues

        init(_ content: AnyView, _ environment: EnvironmentValues) {
            self.content = content
            self.environment = environment
            host = UIHostingController(rootView: AnyView(content.environment(\.self, environment)))
            super.init(nibName: nil, bundle: nil)
        }

        required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

        /// Touches only for the sidebar itself: closed (hidden), the container is clear room over the page's top bar and
        /// conversation, and took the Chat History button's tap on a 6.3" phone.
        final class PassThrough: UIView {
            var movedToWindow: () -> Void = {}

            override func hitTest(_ point: CGPoint, with event: UIEvent?) -> UIView? {
                let hit = super.hitTest(point, with: event)
                return hit === self ? nil : hit
            }

            override func didMoveToWindow() {
                super.didMoveToWindow()
                movedToWindow()
            }
        }

        /// The window whose look changes are followed, and the registration for them.
        private weak var watched: UIWindow?
        private var watching: UITraitChangeRegistration?

        /// In a window (or another one): follow its look.
        private func watchWindow() {
            let window = view.window
            if window !== watched {
                if let watching, let watched { watched.unregisterForTraitChanges(watching) }
                watched = window
                watching = window?.registerForTraitChanges([UITraitUserInterfaceStyle.self]) { [weak self] (_: UIWindow, _) in
                    self?.render()
                }
            }
            render()
        }

        override func loadView() {
            let v = PassThrough()
            v.movedToWindow = { [weak self] in self?.watchWindow() }
            view = v
        }

        override func viewDidLoad() {
            super.viewDidLoad()
            view.backgroundColor = .clear
            // The frame given here is the sidebar's whole room; only the keyboard still insets it.
            host.safeAreaRegions = .keyboard
            host.sizingOptions = []
            host.view.backgroundColor = .clear
            addChild(host)
            view.addSubview(host.view)
            host.didMove(toParent: self)
        }

        override func viewDidLayoutSubviews() {
            super.viewDidLayoutSubviews()
            // Bounds and center, not frame: the drawer keeps a transform on it.
            host.view.bounds = view.bounds
            host.view.center = CGPoint(x: view.bounds.midX, y: view.bounds.midY)
        }

        func set(_ content: AnyView, _ environment: EnvironmentValues) {
            self.content = content
            self.environment = environment
            if isViewLoaded { render() }
        }

        /// The page's environment with the window's look in it; the hosting controller's traits on the same look, for
        /// what UIKit and the glass draw in the sidebar. Not in a window yet: the page's.
        private func render() {
            var look = environment
            let style = view.window?.traitCollection.userInterfaceStyle ?? .unspecified
            if style == .dark { look.colorScheme = .dark } else if style == .light { look.colorScheme = .light }
            if host.overrideUserInterfaceStyle != style { host.overrideUserInterfaceStyle = style }
            host.rootView = AnyView(content.environment(\.self, look))
        }
    }

    func makeUIViewController(context: Context) -> Container {
        let c = Container(root, context.environment)
        c.loadViewIfNeeded()
        drawer.sidebarView = c.host.view
        return c
    }

    func updateUIViewController(_ c: Container, context: Context) {
        c.set(root, context.environment)
        if drawer.sidebarView !== c.host.view { drawer.sidebarView = c.host.view }
    }

    private var root: AnyView { AnyView(content.accessibilityIdentifier("assistant-sidebar")) }
}

/// The page pushed aside: a card with rounded corners, a shadow and (dark mode) an edge line, dimmed; a tap on
/// it closes the sidebar.
private struct DrawerPageMotion: ViewModifier {
    let drawer: AssistantDrawer
    let width: CGFloat
    /// iPhone Duo's side bars: the page stays where it is, only dimmed, and the sidebar comes over it
    /// (`AssistantDrawer.inPlace`).
    var inPlace = false

    func body(content: Content) -> some View {
        let p = drawer.progress
        let out = p > 0
        // The corners only change when the page leaves or comes back to full screen (where the screen's own, rounder
        // corners hide them), so the mask and the shadow are not redrawn on every frame. A page that stays is no card.
        let card = RoundedRectangle(cornerRadius: out && !inPlace ? Theme.Radius.page : 0, style: .continuous)
        let edge = inPlace ? 0 : p
        content
            // While the sidebar is out the page is only a card to tap back to (the overlay below says so).
            .accessibilityHidden(out)
            .mask { card.ignoresSafeArea() }
            .background {
                ZStack {
                    card.fill(Color.black).shadow(color: DrawerEdge.shadow, radius: DrawerEdge.shadowRadius).opacity(edge)
                    card.fill(Theme.pageBackground)
                }
                .ignoresSafeArea()
            }
            .overlay {
                if out {
                    // In dark mode the shadow is invisible: a hairline keeps the card apart from the sidebar.
                    card.strokeBorder(DrawerEdge.line, lineWidth: Theme.Size.line)
                        .opacity(edge)
                        .ignoresSafeArea()
                        .allowsHitTesting(false)
                    // The page at the edge: dimmed as the sidebar comes out (#334). The dimming is the card's shape: the
                    // overlay is not under the mask, and a plain rectangle grayed the sidebar's color outside the card's
                    // rounded corners (#1247).
                    card.fill(Theme.Fill.scrim)
                        .opacity(p)
                        .ignoresSafeArea()
                        .allowsHitTesting(false)
                    // A tap on it closes the sidebar: the whole card, or the part beside the sidebar where the page stays.
                    Color.clear
                        .contentShape(.rect)
                        .onTapGesture { drawer.close() }
                        .accessibilityElement()
                        .accessibilityLabel("Close Sidebar")
                        .accessibilityAddTraits(.isButton)
                        .accessibilityAction { drawer.close() }
                        .accessibilityIdentifier("assistant-drawer-close")
                        .padding(.leading, inPlace ? width : 0)
                        .ignoresSafeArea()
                }
            }
            // Slid aside whole, like ChatGPT (2026-11-01 user request): it used to shrink 6% too, which left gaps above
            // and below the card and dropped its top bar below the sidebar's, and moved every point of it each frame.
            .offset(x: inPlace ? 0 : p * width)
    }
}

#if os(iOS)
/// The drag that opens and closes the sidebar. A UIKit pan so it can decide when the finger has moved a little: it takes
/// the touch when that move is within 45° of horizontal (steeper drags stay the list's), only to the right while closed,
/// and never on something that scrolls sideways itself (a wide code block or table that is not at its left edge) —
/// those scroll first. The direction comes from where the touch went down (`Recognizer.start`): the pan's own
/// translation is still zero when it asks, and its velocity then is a guess from the last two samples (a 30° drag
/// read as almost straight down, so a slanted swipe did nothing).
struct DrawerPan: UIGestureRecognizerRepresentable {
    let drawer: AssistantDrawer
    var enabled: Bool
    /// In the shell: the Assistant | Agents strip. On Assistant a drag to the left pages to Agents (to the right is still the
    /// drawer); on Agents a drag to the right pages back to Assistant (the drawer opens from ☰ there).
    var pager: ShellPager? = nil

    /// A drag to the left that starts on a row that swipes left (a drawer conversation's Archive, `SwipeRowMark`) is the
    /// row's, unless it starts this close to the screen's right edge, where the strip still pages (#2822). Agents' rows
    /// do not swipe (#1244).
    static let edge: CGFloat = Theme.Space.xxl

    /// What a drag moves, decided when it starts.
    enum Mode { case drawer, pager }

    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        var drawer: AssistantDrawer
        var enabled = true
        var pager: ShellPager?
        var mode: Mode = .drawer
        init(drawer: AssistantDrawer) { self.drawer = drawer }

        func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
            guard enabled, let pan = g as? Recognizer, let view = pan.view, let start = pan.start else { return false }
            let now = pan.location(in: view)
            let dx = now.x - start.x, dy = now.y - start.y
            #if DEBUG
            pan.asked = true
            func refused(_ why: String) -> Bool {
                DrawerProbe.note("refused", ["why": why, "dx": Int(dx), "dy": Int(dy)])
                return false
            }
            #else
            func refused(_ why: String) -> Bool { false }
            #endif
            if drawer.isOpen {
                mode = .drawer
                // A conversation in the sidebar swipes left to Archive: from its row the drag is the row's.
                if dx < 0, abs(dx) > abs(dy), Self.onSwipeRow(at: start, in: view) { return refused("a row's swipe") }
                return AssistantDrawer.takes(dx: dx, dy: dy, isOpen: true) ? true : refused("direction")
            }
            // Within 45° of horizontal: steeper is the page's own scrolling.
            guard abs(dx) > abs(dy) else { return refused("direction") }
            if Self.pushed(at: start, in: view) {
                #if DEBUG
                ShellTrace.shared.note("pan refused: pushed")
                #endif
                return refused("pushed")
            }
            if let pager, Self.pages(pager: pager, dx: dx) {
                if Self.sidewaysScroller(at: start, in: view, towardsRight: dx < 0) { return refused("sideways scroller") }
                if Self.typing(at: start, in: view) { return refused("typing") }
                // Agents' rows do not swipe (#1244: on the way to a chat the drag opened a row's buttons): along them a
                // drag pages, to Assistant or on to Messages (#392). A row that does swipe left (a conversation's Archive
                // in the drawer, `SwipeRowMark`) keeps that drag, except from the band at the right edge (#2822).
                if dx < 0, start.x < view.bounds.width - DrawerPan.edge, Self.onSwipeRow(at: start, in: view) {
                    return refused("a row's swipe")
                }
                mode = .pager
                return true
            }
            // The drawer: only to the right while closed, and not where the strip has a page to the left.
            guard dx > 0, pager.map({ $0.page == 0 }) ?? true else { return refused("direction") }
            mode = .drawer
            return Self.sidewaysScroller(at: start, in: view, towardsRight: false) ? refused("sideways scroller") : true
        }

        /// Whether a drag `dx` points toward an existing neighbor on the page strip.
        static func pages(pager: ShellPager, dx: CGFloat) -> Bool {
            (dx < 0 && pager.page < pager.pageCount - 1) || (dx > 0 && pager.page > 0)
        }

        /// Scroll views under the page wait for the drawer to decide, which it does within the same few points they
        /// would start in (so scrolling starts no later). Without this the conversation's pan won the race on a
        /// thumb's 40° swipe (its vertical part passed the list's threshold first) and the drawer was never asked:
        /// the swipe did nothing. A code block at its left edge likewise took a drag to the right to bounce.
        /// In the shell a list's own swipe (a drawer conversation's Archive) waits too: refused at once on a row that
        /// swipes, the row's swipe goes ahead.
        func gestureRecognizer(_ g: UIGestureRecognizer, shouldBeRequiredToFailBy other: UIGestureRecognizer) -> Bool {
            guard let s = other.view as? UIScrollView else { return false }
            if other === s.panGestureRecognizer { return true }
            // The open drawer's own list (a conversation's Archive) waits too.
            return (pager != nil || drawer.isOpen) && other is UIPanGestureRecognizer && (s is UICollectionView || s is UITableView)
        }

        /// Something under the finger scrolls sideways and has room to go the way the finger pulls it: back towards its
        /// left edge for a drag to the right, on towards its right edge (`towardsRight`) for a drag to the left.
        /// Against the width it shows content in: on an iPad a scroll view in the chat runs on under the sidebar,
        /// with that much inset on its left (#5737: a wide table 751 wide in bounds 788, inset 372, scrolls).
        static func sidewaysScroller(at point: CGPoint, in view: UIView, towardsRight: Bool) -> Bool {
            var hit = view.hitTest(point, with: nil)
            while let v = hit, v !== view {
                if let s = v as? UIScrollView, s.isScrollEnabled,
                   s.contentSize.width > s.bounds.width - s.adjustedContentInset.left - s.adjustedContentInset.right + 1 {
                    let room = towardsRight
                        ? s.contentOffset.x < s.contentSize.width - s.bounds.width + s.adjustedContentInset.right - 1
                        : s.contentOffset.x > -s.adjustedContentInset.left + 1
                    if room { return true }
                }
                hit = v.superview
            }
            return false
        }

        /// A navigation stack under the finger has a page pushed: a drag from there is that stack's back swipe, whatever
        /// the page last said about being at its root (`enabled`). That word comes through SwiftUI state and was stale
        /// now and then: on a chat opened on Agents the strip took the edge swipe and slid to Assistant, the chat still pushed
        /// (`ShellPagerTests.testPushedPagesSwipeBack`, 3 in 24 runs). UIKit's stack is what the swipe acts on.
        static func pushed(at point: CGPoint, in view: UIView) -> Bool {
            var r: UIResponder? = view.hitTest(point, with: nil)
            while let x = r, x !== view {
                if let vc = x as? UIViewController, let nav = (vc as? UINavigationController) ?? vc.navigationController,
                   nav.viewControllers.count > 1 {
                    return true
                }
                r = x.next
            }
            return false
        }

        /// The finger is on the text being typed (moving the cursor, selecting): not a page turn.
        static func typing(at point: CGPoint, in view: UIView) -> Bool {
            var hit = view.hitTest(point, with: nil)
            while let v = hit, v !== view {
                if v.isFirstResponder, v is UITextInput { return true }
                hit = v.superview
            }
            return false
        }

        /// The finger is on a row that swipes to the left (`SwipeRowMark`): the sidebar's conversations (Archive).
        static func onSwipeRow(at point: CGPoint, in view: UIView) -> Bool {
            var hit = view.hitTest(point, with: nil)
            while let v = hit, v !== view {
                if v is UICollectionViewCell || v is UITableViewCell { return SwipeRowMark.marks(v) }
                hit = v.superview
            }
            return false
        }
    }

    /// The pan, remembering where the touch went down.
    final class Recognizer: UIPanGestureRecognizer {
        private(set) var start: CGPoint?
        #if DEBUG
        var asked = false
        private var moved: CGPoint = .zero
        #endif

        override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent) {
            if start == nil { start = touches.first?.location(in: view) }
            #if DEBUG
            asked = false
            moved = .zero
            #endif
            super.touchesBegan(touches, with: event)
        }

        #if DEBUG
        override func touchesMoved(_ touches: Set<UITouch>, with event: UIEvent) {
            super.touchesMoved(touches, with: event)
            if let s = start, let p = touches.first?.location(in: view) { moved = CGPoint(x: p.x - s.x, y: p.y - s.y) }
        }
        #endif

        override func reset() {
            #if DEBUG
            // Another gesture took the touch before this one was asked (`DrawerProbe`).
            if !asked, hypot(moved.x, moved.y) > 10 { DrawerProbe.note("lost", ["dx": Int(moved.x), "dy": Int(moved.y)]) }
            #endif
            start = nil
            super.reset()
        }
    }

    func makeCoordinator(converter: CoordinateSpaceConverter) -> Coordinator { Coordinator(drawer: drawer) }

    func makeUIGestureRecognizer(context: Context) -> Recognizer {
        let g = Recognizer()
        g.delegate = context.coordinator
        g.maximumNumberOfTouches = 1
        return g
    }

    func updateUIGestureRecognizer(_ recognizer: Recognizer, context: Context) {
        context.coordinator.enabled = enabled
        context.coordinator.pager = pager
    }

    func handleUIGestureRecognizerAction(_ recognizer: Recognizer, context: Context) {
        if context.coordinator.mode == .pager, let pager = context.coordinator.pager {
            page(recognizer, pager)
            return
        }
        switch recognizer.state {
        case .began:
            #if DEBUG
            DrawerProbe.note("began", ["from": Double(drawer.progress)])
            DrawerProbe.shared.dragBegan()
            #endif
            drawer.dragBegan()
            drawer.drag(recognizer.translation(in: recognizer.view).x)
        case .changed:
            drawer.drag(recognizer.translation(in: recognizer.view).x)
        case .ended:
            let v = recognizer.velocity(in: recognizer.view).x
            let at = drawer.progress
            let opens = drawer.letGo(velocity: v)
            #if DEBUG
            DrawerProbe.note("ended", ["at": Double(at), "velocity": Int(v), "open": opens])
            DrawerProbe.shared.dragEnded(opens ? "open" : "closed")
            #endif
        case .cancelled, .failed:
            drawer.close()
            #if DEBUG
            DrawerProbe.shared.dragEnded("cancelled")
            #endif
        default: break
        }
    }

    /// A drag along the strip: the pages follow the finger, and settle on one of them when it lets go.
    private func page(_ recognizer: Recognizer, _ pager: ShellPager) {
        switch recognizer.state {
        case .began:
            UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
            #if DEBUG
            DrawerProbe.note("page began", ["from": Double(pager.position)])
            #endif
            pager.dragBegan()
            pager.drag(recognizer.translation(in: recognizer.view).x)
        case .changed:
            pager.drag(recognizer.translation(in: recognizer.view).x)
        case .ended:
            let v = recognizer.velocity(in: recognizer.view).x
            #if DEBUG
            DrawerProbe.note("page ended", ["at": Double(pager.position), "velocity": Int(v)])
            #endif
            pager.letGo(velocity: v)
        case .cancelled, .failed:
            pager.cancel()
        default: break
        }
    }
}
#else
/// visionOS: a pinch-and-drag scrolls, and the sidebar opens from its ☰ button; nothing takes a sideways drag.
struct DrawerPan {}
#endif

#endif

#if os(iOS) || os(visionOS)
/// Marks a list row whose swipe to the left the drawer and the strip leave alone (a sidebar conversation's Archive):
/// they look for it in the cell under the finger.
struct SwipeRowMark: UIViewRepresentable {
    final class Marker: UIView {}

    func makeUIView(context: Context) -> Marker {
        let v = Marker()
        v.isUserInteractionEnabled = false
        return v
    }

    func updateUIView(_ uiView: Marker, context: Context) {}

    /// Whether a cell has the mark somewhere inside.
    static func marks(_ cell: UIView) -> Bool {
        var stack = [cell]
        while let v = stack.popLast() {
            if v is Marker { return true }
            stack.append(contentsOf: v.subviews)
        }
        return false
    }
}
#endif

/// Calls `closed` when the drawer is back in: only this empty view reads whether it is out, so the sidebar itself is
/// not redrawn when it comes out.
private struct SidebarClosed: View {
    let drawer: AssistantDrawer
    var closed: () -> Void

    var body: some View {
        Color.clear.onChange(of: drawer.isOut) { _, out in if !out { closed() } }
    }
}

/// What the sidebar lists (2026-10-31, laid out like Doubao's): Persona's name with a search button; the things to start
/// First the main conversation, as Persona with the app's logo, and a line (#2004, in every layout); the things to
/// start (New Chat, Scheduled Tasks, Knowledge; the shell's drawer: Inbox … Machines); a line; Pin and Recent, one line each with a round
/// mark; Persona's face, where it runs and Settings at the foot.
struct AssistantSidebar: View {
    /// The open conversation's row. On the Mac, the shade the shell's other lists select with (`macSelected`): the
    /// phone's `well` on the Mac's lighter fills (#1169) was hard to find there.
    #if os(macOS)
    private static let selectedFill = Theme.Fill.macSelected
    #else
    private static let selectedFill = Theme.Fill.well
    #endif

    @Environment(AppModel.self) private var model
    var current: SessionKey?
    var open: (SessionKey) -> Void
    var newConversation: () -> Void
    var openProfile: () -> Void
    var openRoutines: () -> Void
    var openKnowledge: () -> Void
    /// nil when the tab bar is beside the sidebar (a column on a wide display): no second way to Settings.
    var openSettings: (() -> Void)?
    /// The drawer it is in (nil as a column). The drawer's sidebar stays built while closed: closing it clears the search.
    var drawer: AssistantDrawer?
    /// The shell's drawer (docs/apple/ios.md「New shell」) also lists Inbox and Machines, and ends in New Chat + Settings like ChatGPT's;
    /// so does the iPad's column (docs/apple/ios.md「iPad」), whose tab bar has no Inbox or Settings.
    var openInbox: (() -> Void)? = nil
    var openTodos: (() -> Void)? = nil
    var onMachines: (() -> Void)? = nil
    /// Opens an extension's main-navigation page (nav_page, #2961): listed after the places to go, with its badge.
    /// nil where they are listed elsewhere (the Mac's sections column).
    // Not `openExtension`: Swift keeps property names in the binary, and that is a DEBUG switch release.sh rejects.
    var onOpenExtension: ((ExtensionNavRoute) -> Void)? = nil
    /// Health: the health space's page (docs/assistant/health.md); nil leaves the row out.
    var openHealth: (() -> Void)? = nil
    /// Meeting Recording (docs/apple/recording.md「Meeting recordings」): starts recording a meeting and opens Recordings;
    /// while something records, opens it. nil leaves the row out (the Mac records nothing).
    var startMeeting: (() -> Void)? = nil
    /// The column's own button that folds it away, at its top trailing corner, as ChatGPT's on iPhone Duo open (#2623).
    /// Search goes to its foot then, beside Persona's name and where it runs, and the main conversation's row holds what
    /// the face's menu at the top of the page did (the switch has that place). nil elsewhere.
    var collapse: (() -> Void)? = nil
    private var isShell: Bool { openInbox != nil }
    @State private var query = ""
    @State private var searchOpen = false
    #if os(iOS)
    /// The open iPhone Duo's search, over the whole window (`footerSearch`).
    @State private var fullSearch = false
    #endif
    #if os(macOS)
    /// Its header is in the title bar's row (#830): past the traffic lights when the window's sections column is hidden.
    @Environment(\.macTitleRowLeading) private var titleRowLeading
    #endif
    @State private var ending: Session?
    @State private var renaming: Session?
    @State private var showArchived = false
    @State private var autoList: AutoConversationsRoute?
    @State private var feed = SidebarFeed()
    private let taskChats = AssistantTaskChatsStore.shared
    @FocusState private var searching: Bool

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AssistantSidebar")
        #endif
        // Closed in its drawer, the sidebar keeps the list it last showed: `assistantConversations` reads every session
        // on every machine, so each hub push re-ran this body (three times a push) while nobody could see it. It
        // reads the live list again as soon as it starts to come out (`isOut`).
        let out = drawer?.isOut ?? true
        let all = feed.conversations(out ? model.assistantConversations : nil)
        // The same for Archived's count: it reads every session too (all of them redrew this while it was closed).
        let archived = feed.archived(out ? model.archivedConversations.count : nil)
        VStack(spacing: 0) {
            topBar
                #if os(macOS)
                // Starts where the other sections' titles do (`MacPageTitle`: `l`, past the traffic lights when the
                // sections column is hidden), #3179.
                .padding(.leading, max(Theme.Space.l, titleRowLeading))
                .padding(.trailing, Theme.Space.xl)
                // The title bar's row: centred on it as the other sections' titles are, so it does not jump (#830).
                .frame(height: Theme.Size.macToolbarHeight)
                #else
                .padding(.horizontal, Theme.Space.xl)
                .padding(.top, besideBar ? AssistantTopBar.topInset : Theme.Space.s)
                .padding(.bottom, Theme.Space.s)
                #endif
            // A list (not a stack in a scroll view) for its rows' own swipe: a conversation swipes left to Archive. On the
            // Mac it selects the open conversation: ↑ / ↓ open the next, Delete archives (`MacListKeys`, #2113).
            MacSelectingList(selection: Binding(get: { current }, set: { key in if let key, key != current { open(key) } })) {
                Group {
                    if query.isEmpty {
                        // The main conversation first, once, in every layout (#2004): never again under Pin or Recent.
                        mainItem(all)
                        VStack { Divider() }.padding(.horizontal, Theme.Space.m).padding(.vertical, Theme.Space.s) // a row lays a bare Divider out upright
                        actions(all)
                        if !macPlacesGroup {
                            VStack { Divider() }.padding(.horizontal, Theme.Space.m).padding(.vertical, Theme.Space.s) // a row lays a bare Divider out upright
                        }
                        conversations(all, archived: archived)
                    } else {
                        results(AssistantSidebarSearch.filter(all, query, names: model.directionNames))
                    }
                }
                #if os(macOS)
                // The list's own margin is the shell's rows' `s` (`MacSidebarRow`, Inbox's Source): the shade and the rows'
                // words line up with the other sections' columns (#3179).
                .listRowInsets(EdgeInsets())
                #else
                .listRowInsets(EdgeInsets(top: 0, leading: Theme.Space.s, bottom: 0, trailing: Theme.Space.s))
                #endif
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            #if os(macOS)
            .modifier(MacListKeys(current: { current }, delete: { key in
                guard key != model.assistantMain else { return }
                Task { await model.archiveConversation(key, true) }
            }))
            #endif
            .environment(\.defaultMinListRowHeight, 0)
            .contentMargins(.bottom, Theme.Space.l, for: .scrollContent)
            .scrollDismissesKeyboard(.interactively)
            footer
        }
        #if os(macOS)
        // Conversation rows cover the system selection with this color (`macListRow`).
        // Paint the entire column with it too, including the title, actions and unused space.
        .background(Theme.pageBackground)
        #endif
        // Which conversations the tasks started: read again as the sidebar comes out, when a conversation came or went, and
        // for another manager's page.
        .task(id: out ? "\(all.count)|\(model.assistantManagerID ?? "")" : "") { if out { await taskChats.refresh(model.api) } }
        .confirmClose($ending)
        .renameSessionAlert($renaming)
        .sheet(item: $autoList) { route in
            AutoConversationsView(source: route.source) { key in
                autoList = nil
                open(key)
            }
        }
        .sheet(isPresented: $showArchived) {
            ArchivedConversationsView { key in
                showArchived = false
                open(key)
            }
        }
        #if os(iOS)
        .fullScreenCover(isPresented: $fullSearch) {
            AssistantFullSearch { key in
                fullSearch = false
                open(key)
            }
        }
        #endif
        .background {
            if let drawer {
                SidebarClosed(drawer: drawer) {
                    query = ""
                    searchOpen = false
                    searching = false
                }
            }
        }
    }

    // MARK: Top: the name, search

    @ViewBuilder private var topBar: some View {
        if searchOpen {
            HStack(spacing: Theme.Space.m) {
                searchField
                Button("Cancel") {
                    withAnimation(Theme.Motion.state) {
                        query = ""
                        searchOpen = false
                        searching = false
                    }
                }
                .font(Theme.Font.body)
                .lineLimit(1)
            }
        } else {
            HStack {
                if model.managerRoster?.isMulti == true, let current = model.currentAssistant {
                    // Keep the current multi-assistant switcher; a sole assistant keeps the persona title.
                    AssistantSwitcherHeader(manager: current, nameFont: Theme.Font.title, compact: macTitleRow, openSettings: showSettings)
                } else {
                    #if os(macOS)
                    Text(model.persona.name).font(Theme.Font.title).foregroundStyle(Theme.macLabel).lineLimit(1)
                    #else
                    Text(model.persona.name).font(isShell && !besideBar ? Theme.Font.display : Theme.Font.title).foregroundStyle(Color.primary).lineLimit(1)
                    #endif
                }
                Spacer(minLength: Theme.Space.s)
                if let collapse {
                    AssistantTopBar.circle(Theme.Icon.sidebar, label: String(localized: "Hide Sidebar"), id: "assistant-sidebar-collapse") {
                        Haptics.tap()
                        collapse()
                    }
                } else if besideBar {
                    // The same button as the top bar's beside it (2026-11-04: on iPhone Duo open, this one stood lower
                    // and larger than Chat History / New Chat in the next column).
                    AssistantTopBar.circle(Theme.Icon.search, label: String(localized: "Search Chats [Assistant Sidebar 55c9e4]"), id: "assistant-sidebar-search", action: openSearch)
                } else {
                    Button(action: openSearch) {
                        ThemedIcon(Theme.Icon.search)
                            .font(Theme.Font.heading)
                            .foregroundStyle(Color.primary)
                            .scaledFrame(width: Theme.Size.avatarL, height: Theme.Size.avatarL)
                            .surface(interactive: true, in: .circle)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Search Chats [Assistant Sidebar 55c9e4]")
                }
            }
        }
    }

    /// Search at the foot, beside Persona's name, where the column has its fold at the top instead (iPhone Duo open): it
    /// opens over the whole window, its field at the foot (`AssistantFullSearch`, #3705).
    private var footerSearch: (() -> Void)? {
        guard collapse != nil else { return nil }
        #if os(iOS)
        return { fullSearch = true }
        #else
        return { openSearch() }
        #endif
    }

    private func openSearch() {
        withAnimation(Theme.Motion.state) { searchOpen = true }
        searching = true
    }

    /// The header sits in the Mac's title bar row: one line.
    private var macTitleRow: Bool {
        #if os(macOS)
        true
        #else
        false
        #endif
    }

    /// Settings on the page `AppModel.settingsPath` names (the switcher's Manage Assistants…): the way this sidebar has
    /// to Settings, else the app's own.
    private func showSettings() {
        if let openSettings { openSettings() } else { model.showSettings() }
    }

    /// The split's list column (a wide display), beside the conversation's top bar: the header is that bar's row, with
    /// the shell's places or without. The drawer keeps its own; on the Mac both sit in the title bar's row already (#830).
    private var besideBar: Bool {
        #if os(macOS)
        false
        #else
        drawer == nil
        #endif
    }

    /// The open search field; in the BeautifulUI style SidebarNav's.
    @ViewBuilder private var searchField: some View {
        if AppStyle.current.usesBeautifulUI {
            BUISearchField(String(localized: "Search Chats [Assistant Sidebar 55c9e4]"), text: $query, focused: $searching, clearLabel: String(localized: "Clear Search"))
                .autocorrectionDisabled()
        } else {
            HStack(spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.search).font(Theme.Font.secondaryMedium).foregroundStyle(.secondary)
                TextField(String(localized: "Search Chats [Assistant Sidebar 55c9e4]"), text: $query)
                    .focused($searching)
                    .submitLabel(.search)
                    .autocorrectionDisabled()
                if !query.isEmpty {
                    Button { query = "" } label: { ThemedIcon(Theme.Icon.clear).foregroundStyle(.tertiary) }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Clear Search")
                }
            }
            .padding(.horizontal, Theme.Space.m)
            .scaledFrame(height: Theme.Size.avatar)
            .background(Theme.Fill.chip, in: .capsule)
        }
    }

    // MARK: What to start

    @ViewBuilder private func actions(_ all: [Session]) -> some View {
        if let openInbox, let onMachines {
            // The shell's: the places to go (the main conversation is the row above them, `mainItem`).
            VStack(alignment: .leading, spacing: 0) {
                meetingRow
                action(String(localized: "Inbox"), Theme.Icon.inbox, id: "sidebar-inbox", count: InboxStore.shared.unreadCount, run: openInbox)
                if TodoStore.shared.isSupported {
                    action(String(localized: "To-dos"), Theme.Icon.todo, id: "sidebar-todos", count: TodoStore.shared.badgeCount, run: { (openTodos ?? openInbox)() })
                }
                action(String(localized: "Scheduled Tasks"), Theme.Icon.schedule, run: openRoutines)
                action(String(localized: "Knowledge"), Theme.Icon.knowledge, run: openKnowledge)
                if let openHealth { action(String(localized: "Health"), Theme.Icon.health, id: "sidebar-health", run: openHealth) }
                action(String(localized: "Machines"), Theme.Icon.machines, id: "sidebar-machines", run: onMachines)
                extensionRows
            }
        } else {
            #if os(macOS)
            // One row as the conversations' (#2113): New Chat is the title row's button and ⇧⌘N, Knowledge the window's
            // sections column (2026-11-05 user: not twice). Headed by nothing: a heading over one row is noise.
            if !AppStyle.current.usesBeautifulUI {
                macPlace(String(localized: "Scheduled Tasks"), Theme.Icon.schedule, run: openRoutines)
                if let openHealth { macPlace(String(localized: "Health"), Theme.Icon.health, run: openHealth) }
            } else {
                phoneActions
            }
            #else
            phoneActions
            #endif
        }
    }

    private var phoneActions: some View {
        VStack(alignment: .leading, spacing: 0) {
            action(String(localized: "New Chat [Quick Actions ea4b29]"), Theme.Icon.conversation, id: "sidebar-new", run: newConversation)
            meetingRow
            action(String(localized: "Scheduled Tasks"), Theme.Icon.schedule, run: openRoutines)
            action(String(localized: "Knowledge"), Theme.Icon.knowledge, run: openKnowledge)
            if let openHealth { action(String(localized: "Health"), Theme.Icon.health, id: "sidebar-health", run: openHealth) }
            extensionRows
        }
    }

    /// First among the places: one tap from the drawer records the meeting in the room; while a recording runs, the row
    /// says so and opens it.
    @ViewBuilder private var meetingRow: some View {
        #if os(iOS)
        if let startMeeting {
            let recording = RecordingCapture.shared.state != .idle
            action(recording ? String(localized: "Recording… Open") : String(localized: "Meeting Recording"),
                   Theme.Icon.recording, id: "sidebar-meeting-recording", run: startMeeting)
        }
        #endif
    }

    /// The extensions' pages on the main navigation (nav_page, docs/extensions/extensions.md「UI slots」): one row each, its icon,
    /// title and badge, in the hub's order. Nothing with a hub without slots.
    @ViewBuilder private var extensionRows: some View {
        if let onOpenExtension, ExtensionSlots.shared.available {
            ForEach(ExtensionSlots.shared.pages) { p in
                extensionRow(p) { onOpenExtension(ExtensionNavRoute(p)) }
            }
        }
    }

    @ViewBuilder private func extensionRow(_ p: ExtensionUIPlace, run: @escaping () -> Void) -> some View {
        let symbol = ExtensionSlots.icon(p.icon)
        Group {
            if AppStyle.current.usesBeautifulUI {
                BUISidebarRow(p.title, symbol: Theme.Icon.styled(symbol), action: run) {
                    if let b = p.badge { ExtensionBadgeView(badge: b) }
                }
            } else {
                Button(action: run) {
                    HStack(spacing: Theme.Sidebar.gap) {
                        Image(systemName: symbol).resizable().scaledToFit().fontWeight(isShell ? .medium : .regular)
                            .foregroundStyle(Color.primary)
                            .scaledFrame(width: Theme.Sidebar.glyph, height: Theme.Sidebar.glyph)
                            .scaledFrame(width: Theme.Sidebar.glyphColumn)
                        Text(p.title).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
                        Spacer(minLength: 0)
                        if let b = p.badge { ExtensionBadgeView(badge: b) }
                    }
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.vertical, Theme.Sidebar.rowPadding)
                    .scaledFrame(minHeight: Theme.Sidebar.row)
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(p.title)
        .accessibilityValue(p.badge?.text ?? "")
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { run() }
        .accessibilityIdentifier("sidebar-extension-\(p.owner)-\(p.id)")
    }

    #if os(macOS)
    /// Whether the places to go are one row of the list (the Mac's plain styles) rather than the phone's big rows.
    private var macPlaces: Bool { !isShell && !AppStyle.current.usesBeautifulUI }

    /// A place to go at the top of the Mac's list: a conversation row's size, its symbol where a conversation has its mark.
    private func macPlace(_ title: String, _ symbol: String, run: @escaping () -> Void) -> some View {
        Button(action: run) {
            HStack(spacing: Theme.Sidebar.gap) {
                Image(systemName: symbol).resizable().scaledToFit().foregroundStyle(.secondary)
                    .scaledFrame(width: Theme.Sidebar.glyph, height: Theme.Sidebar.glyph)
                    .scaledFrame(width: Theme.Sidebar.mark)
                Text(title).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
                Spacer(minLength: 0)
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Sidebar.rowPadding)
            .scaledFrame(minHeight: Theme.Sidebar.row)
            .macHoverShade(in: RoundedRectangle(cornerRadius: Theme.Sidebar.radius, style: .continuous))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("sidebar-" + title)
    }
    #endif

    /// The sidebar's first row in every layout: the main conversation as Persona with the current persona's logo (#2004, #3203).
    /// Only the name shown is Persona's: it is the same conversation, its key and its data unchanged. Its state
    /// (working, waiting, unread) on the right, as on a conversation's row. It shows while its session is not in the
    /// list too (not loaded yet, or listed by no machine now, #4606): it still opens it, or with no main conversation
    /// yet the new-conversation page, whose first one becomes it. Off until the hub's assistant config came: a
    /// conversation started before would not become the main one. No swipe: it cannot be archived.
    private func mainItem(_ all: [Session]) -> some View {
        let key = model.assistantMain
        let main = all.first { $0.key == key }
        let selected = key != nil && key == current
        let radius = Theme.Sidebar.radius
        return Button { if let key { open(key) } else { newConversation() } } label: {
            HStack(spacing: Theme.Sidebar.gap) {
                // Where the places below have their symbol, the same width (on the Mac's plain list, `macPlace`'s).
                AppGlyph(size: macPlacesGroup ? Theme.Sidebar.mark : Theme.Sidebar.glyphColumn, scaled: true)
                // The small "Main Chat" under the name keeps the row readable as a conversation, not a heading (#6328, as the web, #6293).
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(Self.mainTitle).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
                    Text("Main Chat").font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 0)
                if let main { RowStateMark(state: main.state) }
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Sidebar.rowPadding)
            .scaledFrame(minHeight: Theme.Sidebar.row)
            .background {
                if selected { RoundedRectangle(cornerRadius: radius, style: .continuous).fill(Self.selectedFill) }
            }
            .macHoverShade(in: RoundedRectangle(cornerRadius: radius, style: .continuous))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .macListRow(selected: selected, in: Rectangle(), drawsShade: false) // the row draws its own (`selectedFill`)
        .accessibilityLabel([Self.mainTitle, String(localized: "Main Chat"), main.flatMap(stateWord)].compactMap { $0 }.joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label")))
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("sidebar-main")
        .disabled(key == nil && model.assistantConfig == nil)
        .contextMenu {
            if let key {
                if collapse != nil { MainChatPlaceItems(key: key, openKnowledge: openKnowledge, openProfile: openProfile) }
                MainConversationMenu(key: key) // Compact context, New Main Chat… (#1762)
                Button {
                    UIPasteboard.general.string = key.sessionID
                    model.toast = Toast(String(localized: "Session ID Copied"), style: .success)
                } label: { Label("Copy Session ID [Assistant Sidebar f24fb3]", systemImage: Theme.Icon.id) }
                Button {
                    UIPasteboard.general.string = ChatTrace.shared.text(key) // structure only, to paste into a report
                    model.toast = Toast(String(localized: "Diagnostics copied"), style: .success)
                } label: { Label("Copy Diagnostics", systemImage: Theme.Icon.diagnostics) }
                if let main, main.status.isLive {
                    Button(role: .destructive) { ending = main } label: { Label("End…", systemImage: Theme.Icon.end) }
                }
            }
        }
    }

    /// The main conversation's name in the sidebar (#2004; it was Main Chat before, and again in #4606).
    static let mainTitle = "Persona"

    @ViewBuilder private func action(_ title: String, _ symbol: String, id: String? = nil, count: Int = 0, run: @escaping () -> Void) -> some View {
        if AppStyle.current.usesBeautifulUI {
            // SidebarNav's primary items.
            BUISidebarRow(title, symbol: Theme.Icon.styled(symbol), action: run) {
                if count > 0 { CountBadge(count: count, look: .needs) }
            }
            .accessibilityIdentifier(id ?? "sidebar-" + title)
        } else {
            plainAction(title, symbol, id: id, count: count, run: run)
        }
    }

    private func plainAction(_ title: String, _ symbol: String, id: String? = nil, count: Int = 0, run: @escaping () -> Void) -> some View {
        Button(action: run) {
            HStack(spacing: Theme.Sidebar.gap) {
                // One box for every symbol: a wide one (Knowledge's books) scales down into it instead of pushing its words.
                Image(systemName: symbol).resizable().scaledToFit().fontWeight(isShell ? .medium : .regular).foregroundStyle(Color.primary)
                    .scaledFrame(width: Theme.Sidebar.glyph, height: Theme.Sidebar.glyph)
                    .scaledFrame(width: Theme.Sidebar.glyphColumn)
                Text(title).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
                Spacer(minLength: 0)
                if count > 0 { CountBadge(count: count, look: .needs) }
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Sidebar.rowPadding)
            .scaledFrame(minHeight: Theme.Sidebar.row)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(id ?? "sidebar-" + title)
    }

    // MARK: Conversations

    @ViewBuilder private func conversations(_ all: [Session], archived: Int) -> some View {
        let main = model.assistantMain
        // The leader conversations are the Directions group, in the hub's order, under each direction's name
        // (docs/assistant/unified-entry.md "Leader conversations"); then Recent, the newest few of the user's other
        // chats (#4873); everything else is one tap away in All Chats (#4331).
        let directions = AssistantSidebarGroups.directions(all, leaders: model.assistantLeaders, main: main)
        if !directions.isEmpty {
            heading(String(localized: "Directions")) { EmptyView() }
            ForEach(directions) { row($0, named: model.directionNames[$0.sessionID]) }
        }
        let recent = AssistantSidebarGroups.recent(all, leaders: model.assistantLeaders, main: main, tasks: taskChats.known(on: model.api),
                                                   hubSays: model.hubFeatures.contains(AutoConversationSource.feature))
        if !recent.isEmpty {
            heading(String(localized: "Recent")) { EmptyView() }
            ForEach(recent) { row($0) }
        }
        allChatsRow
        archivedRow(archived)
    }

    /// All Chats (#4331): the full list with its filters and search (`AutoConversationsView`). It works on every hub
    /// (its rows come from the conversations the app already has); a hub that predates the origins only lacks the
    /// source filters there.
    private var allChatsRow: some View {
        Button { autoList = AutoConversationsRoute(source: nil) } label: {
            HStack(spacing: Theme.Space.m) {
                if !isShell {
                    ThemedIcon(Theme.Icon.list).font(Theme.Font.label).foregroundStyle(.secondary)
                        .scaledFrame(width: Theme.Sidebar.mark, height: Theme.Sidebar.mark)
                }
                Text("All Chats").font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
                Spacer(minLength: Theme.Space.xs)
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Sidebar.rowPadding)
            .scaledFrame(minHeight: Theme.Sidebar.row)
            .macHoverShade(in: RoundedRectangle(cornerRadius: Theme.Sidebar.radius, style: .continuous))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("sidebar-all-chats")
    }

    /// Archived (N) at the end of the list when there are any: where swiped-away conversations are.
    @ViewBuilder private func archivedRow(_ n: Int) -> some View {
        if n > 0 {
            Button { showArchived = true } label: {
                Label("Archived (\(n))", systemImage: Theme.Icon.archive)
                    .font(Theme.Font.secondary)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.vertical, Theme.Space.m)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("sidebar-archived")
        }
    }

    @ViewBuilder private func results(_ hits: [Session]) -> some View {
        if hits.isEmpty {
            VStack(spacing: Theme.Space.sm) {
                ThemedIcon(Theme.Icon.search).font(Theme.Font.title).foregroundStyle(.tertiary)
                Text("No Results for “\(query)”").font(Theme.Font.secondary).foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity)
            .padding(.top, 40)
        } else {
            heading(String(localized: "\(hits.count) Chats [Assistant Sidebar a72a2a]")) { EmptyView() }
            ForEach(hits) { row($0, named: model.directionNames[$0.sessionID]) }
        }
    }

    /// A section's name, small and gray, with its ⋯ menu on the right when it has one.
    @ViewBuilder private func heading<Menu: View>(_ text: String, @ViewBuilder menu: () -> Menu) -> some View {
        if AppStyle.current.usesBeautifulUI {
            let items = menu()
            BUISidebarHeading(text) {
                if !HeadingMenu.isEmpty(items) {
                    SwiftUI.Menu { items } label: {
                        ThemedIcon(Theme.Icon.more).font(Theme.Font.captionStrong)
                            .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.wellXS)
                            .contentShape(.rect)
                    }
                    .tint(Color.secondary)
                    .labelOnlyMenu()
                    .accessibilityLabel(String(localized: "More actions for \(text) [Assistant Sidebar f357af]"))
                }
            }
            .scaledFrame(minHeight: HeadingMenu.minHeight(Menu.self))
            .padding(.top, Theme.Space.l)
            .padding(.bottom, Theme.Space.xs)
        } else {
            plainHeading(text, menu: menu)
        }
    }

    private func plainHeading<Menu: View>(_ text: String, @ViewBuilder menu: () -> Menu) -> some View {
        let items = menu()
        return HStack {
            Text(text).font(Theme.Font.caption).foregroundStyle(Color.secondary).accessibilityAddTraits(.isHeader)
            Spacer(minLength: 0)
            if !HeadingMenu.isEmpty(items) {
                SwiftUI.Menu { items } label: {
                    ThemedIcon(Theme.Icon.more).font(Theme.Font.captionStrong).foregroundStyle(.tertiary)
                        .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.wellXS)
                        .contentShape(.rect)
                }
                .tint(Color.secondary) // a gray ⋯, not the accent
                .labelOnlyMenu()
                .accessibilityLabel(String(localized: "More actions for \(text) [Assistant Sidebar f357af]"))
            }
        }
        .scaledFrame(minHeight: HeadingMenu.minHeight(Menu.self))
        .padding(.leading, Theme.Space.m)
        .padding(.top, Theme.Space.l)
        .padding(.bottom, Theme.Space.xs)
    }

    /// A conversation's title on one line. A 「1101 | 修复 | 主题」 title (docs/assistant/assistant.md「Chat titles」) has its day and
    /// kind in the secondary color: the topic is what the eye finds, and what a narrow drawer cuts at the end.
    @ViewBuilder private func titleText(_ title: String, named: Bool) -> some View {
        if named, let t = ConversationTitle(title) {
            Text("\(Text(t.head).foregroundStyle(Color.secondary))\(t.topic)")
                .font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
        } else {
            Text(title).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
        }
    }

    /// A conversation: a round mark (Persona's face for the main one), its title on one line, its state on the right.
    /// `named`: a direction's name, shown instead of its leader conversation's title.
    private func row(_ s: Session, named: String? = nil) -> some View {
        let selected = s.key == current
        let isMain = s.key == model.assistantMain
        let title = isMain ? Self.mainTitle : named ?? (s.title.isEmpty || s.title == "Claude Code" ? String(localized: "New Chat [Quick Actions ea4b29]") : s.title)
        let preview = s.lastText.map { Fmt.plain($0).replacingOccurrences(of: "\n", with: " ") } ?? ""
        let counts: String? = model.assistantLeaders.contains(s.sessionID)
            ? model.directionCounts[s.sessionID].flatMap { DirectionCountsText.label(working: $0.working, needsYou: $0.needsYou) }
            : nil
        return Button { open(s.key) } label: {
            HStack(spacing: Theme.Space.m) {
                // The shell's list is ChatGPT's: titles only, all in one column (the marks made it busy); the main
                // conversation (only in search results here) keeps the app's logo, as its first row has it (#2004).
                if isMain {
                    AppGlyph(size: isShell ? Theme.Sidebar.glyphColumn : Theme.Sidebar.mark, scaled: true)
                } else if isShell {
                    EmptyView()
                } else {
                    ConversationMark(id: s.sessionID)
                }
                titleText(title, named: !isMain)
                ConversationSourceMark(origin: s.origin, channels: s.channels) // the extension that opened it, or its latest message's channel
                Spacer(minLength: Theme.Space.xs)
                // A direction's work at this moment ("N working · N need you"), never a zero part (#4331).
                if let counts {
                    Text(counts).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                }
                RowStateMark(state: s.state)
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Sidebar.rowPadding)
            .scaledFrame(minHeight: Theme.Sidebar.row)
            .background {
                if selected {
                    RoundedRectangle(cornerRadius: Theme.Sidebar.radius, style: .continuous).fill(Self.selectedFill)
                }
            }
            .macHoverShade(in: RoundedRectangle(cornerRadius: Theme.Sidebar.radius, style: .continuous))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .tag(s.key)
        .macListRow(selected: selected, in: Rectangle(), drawsShade: false) // the row draws its own (`selectedFill`)
        .accessibilityLabel([title, counts, isMain && s.title != title ? s.title : nil, AutoConversationSource.external(s.origin)?.from, stateWord(s), preview.isEmpty ? nil : preview]
            .compactMap { $0 }.joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label")))
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier(isMain ? "sidebar-main" : "sidebar-conversation")
        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
            if !isMain {
                Button { Task { await model.archiveConversation(s.key, true) } } label: {
                    Label("Archive", systemImage: Theme.Icon.archive)
                }
                .tint(Theme.accent)
            }
        }
        #if os(iOS) || os(visionOS)
        .background { if !isMain { SwipeRowMark() } } // the drawer leaves a drag to the left on it to the row
        #endif
        .contextMenu {
            if isMain { MainConversationMenu(key: s.key) } // Compact context, New Main Chat… (#1762)
            if !isMain {
                // A direction is the hub's, for every device (#4331): the long press makes a conversation one, or ends
                // the direction there (#4331, `make` / `end`).
                let lead = model.assistantLeaders.contains(s.sessionID)
                Button {
                    Task { await model.setDirectionLead(s.sessionID, !lead) }
                } label: {
                    lead ? Label("Unpin [Assistant Sidebar b7c40e]", systemImage: Theme.Icon.unpin)
                        : Label("Pin [Assistant Sidebar 6d1f2a]", systemImage: Theme.Icon.pin)
                }
            }
            if !isMain {
                Button { renaming = s } label: { Label("Rename… [Assistant Sidebar ec4cd0]", systemImage: Theme.Icon.edit) }
                Button { Task { await model.archiveConversation(s.key, true) } } label: { Label("Archive", systemImage: Theme.Icon.archive) }
            }
            Button {
                UIPasteboard.general.string = s.sessionID
                model.toast = Toast(String(localized: "Session ID Copied"), style: .success)
            } label: { Label("Copy Session ID [Assistant Sidebar f24fb3]", systemImage: Theme.Icon.id) }
            Button {
                UIPasteboard.general.string = ChatTrace.shared.text(s.key) // structure only, to paste into a report
                model.toast = Toast(String(localized: "Diagnostics copied"), style: .success)
            } label: { Label("Copy Diagnostics", systemImage: Theme.Icon.diagnostics) }
            if s.status.isLive {
                Button(role: .destructive) { ending = s } label: { Label("End…", systemImage: Theme.Icon.end) }
            }
        } preview: {
            // The long-press preview: what it last said (the row itself shows only the title).
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                Text(title).font(Theme.Font.heading).lineLimit(2)
                if !preview.isEmpty { Text(preview).font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(6) }
                Text(Fmt.relative(s.updatedAt)).font(Theme.Font.label).foregroundStyle(.tertiary)
            }
            .padding(Theme.Space.l)
            .frame(width: Theme.Size.box280, alignment: .leading)
        }
    }

    private func stateWord(_ s: Session) -> String? {
        switch s.state {
        case .working, .needsYou, .unread: s.state.label
        default: s.updatedAt.map { Fmt.relative($0) }
        }
    }

    // MARK: Foot: Persona, Settings

    private var machineID: String { model.assistantConfig?.machineID ?? model.hubMachineID }

    /// The Mac's place is a row like the conversations': no line between it and them.
    private var macPlacesGroup: Bool {
        #if os(macOS)
        macPlaces
        #else
        false
        #endif
    }

    /// Persona's face and name with where it runs (a tap: its profile), Settings on the right (the tab bar is hidden while
    /// the drawer is out).
    @ViewBuilder private var footer: some View {
        if isShell {
            shellFooter
            // The iPad's column (the shell's places beside the conversation): Persona's face and where it runs stay
            // at the foot, under the shell's New Chat + Settings (2026-11-05 user: the bottom left is fine).
            if drawer == nil {
                AssistantProfileFooter(machineID: machineID, openProfile: openProfile, openSettings: nil, search: footerSearch)
            }
        } else {
            #if os(macOS)
            if macPlacesGroup {
                AssistantStatusLine(machineID: machineID, openProfile: openProfile)
            } else {
                AssistantProfileFooter(machineID: machineID, openProfile: openProfile, openSettings: openSettings)
            }
            #else
            AssistantProfileFooter(machineID: machineID, openProfile: openProfile, openSettings: openSettings)
            #endif
        }
    }

    /// The shell's foot, ChatGPT's: a New Chat pill in the Theme Color on the left (#1312), Settings in a glass circle on the right.
    private var shellFooter: some View {
        HStack(spacing: Theme.Space.m) {
            Button(action: newConversation) {
                Label("New Chat [Quick Actions ea4b29]", systemImage: Theme.Icon.compose)
                    .font(Theme.Font.bodyStrong)
                    .foregroundStyle(.white)
                    .lineLimit(1)
                    .padding(.horizontal, Theme.Space.xl)
                    .scaledFrame(height: Theme.Size.avatarL)
                    .background(Theme.accent, in: .capsule)
                    .contentShape(.capsule)
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("sidebar-new")
            Spacer(minLength: Theme.Space.s)
            if let openSettings {
                Button(action: openSettings) {
                    ThemedIcon(Theme.Icon.settings)
                        .font(Theme.Font.heading)
                        .foregroundStyle(Color.primary)
                        .scaledFrame(width: Theme.Size.avatarL, height: Theme.Size.avatarL)
                        .contentShape(.circle)
                }
                .buttonStyle(.plain)
                .surface(interactive: true, in: .circle)
                // The plugins' entries are in Settings (docs/extensions/extensions.md「UI slots」, badges): their badges add up here, unless the
                // sidebar lists the entries itself.
                .overlay(alignment: .topTrailing) { if onOpenExtension == nil { ExtensionSettingsBadge() } }
                .accessibilityLabel("Settings")
                .accessibilityIdentifier("sidebar-settings")
            }
        }
        .padding(.horizontal, Theme.Space.xl)
        .padding(.vertical, Theme.Space.m)
    }
}

/// What the face's menu at the top of the page held, on the main conversation's long press too (iPhone Duo open, where the
/// Assistant | Agents switch takes the face's place, #2623; the user asked for it there): whose Persona it is (several
/// hubs), Knowledge and Details, the directory and the facts to copy. The switch's Assistant, tapped while it is
/// selected, keeps the whole menu, as in the shell.
private struct MainChatPlaceItems: View {
    @Environment(AppModel.self) private var model
    let key: SessionKey
    let openKnowledge: () -> Void
    let openProfile: () -> Void

    var body: some View {
        if model.isMultiHub, let current = model.hub?.group {
            Section("hub") {
                Picker("hub", selection: Binding(get: { current }, set: { model.selectHub($0) })) {
                    ForEach(model.groupIDs, id: \.self) { g in
                        Label(model.groupName(g), systemImage: Theme.Icon.hubGroup).tag(g)
                    }
                }
            }
        }
        Section {
            Button(action: openKnowledge) { Label("Knowledge", systemImage: Theme.Icon.knowledge) }
            Button(action: openProfile) { Label("Details [Assistant Top Bar 1932da]", systemImage: Theme.Icon.person) }
        }
        Section {
            Button { copy(AssistantCopyText.cwd(key, in: model), String(localized: "Working Directory Copied")) } label: {
                Label("Copy Directory", systemImage: Theme.Icon.folder)
            }
            Button { copy(AssistantCopyText.info(key, in: model), String(localized: "Chat Info Copied")) } label: {
                Label("Copy Info", systemImage: Theme.Icon.copy)
            }
        }
    }

    private func copy(_ text: String, _ done: String) {
        UIPasteboard.general.string = text
        model.toast = Toast(done, style: .success)
    }
}

/// Persona's face and name with where it runs, Settings on the right: a view of its own, so the machines' heartbeats (and
/// the page coming to the front) draw this row again, not the conversations' list.
private struct AssistantProfileFooter: View {
    @Environment(AppModel.self) private var model
    @Environment(\.tabIsActive) private var inFront
    let machineID: String
    let openProfile: () -> Void
    let openSettings: (() -> Void)?
    /// Search on the right, where ChatGPT has it beside the account on iPhone Duo open (#2623); nil, none.
    var search: (() -> Void)? = nil

    var body: some View {
        // Behind another section (the Mac keeps the page): the machine as it is now, not each push's heartbeat.
        let machine = model.machine(model.hubRef(machineID), inFront: inFront)
        let online = machine?.online ?? false
        let state = online ? String(localized: "Online") : String(localized: "Offline [Machine Entity be1b4f]")
        return VStack(spacing: 0) {
            Divider()
            HStack(spacing: Theme.Space.m) {
                Button(action: openProfile) {
                    HStack(spacing: Theme.Space.m) {
                        PersonaAvatar(size: Theme.Size.avatarL)
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            HStack(spacing: Theme.Space.xs) {
                                Text(model.persona.name).font(Theme.Font.bodyStrong).foregroundStyle(Color.primary).lineLimit(1)
                                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                            }
                            HStack(spacing: Theme.Space.xs) {
                                Circle().fill(online ? Theme.positive : Color.secondary.opacity(0.5))
                                    .scaledFrame(width: Theme.Size.dotXS, height: Theme.Size.dotXS)
                                Text(online ? String(localized: "Online · \(machine?.name ?? machineID)") : String(localized: "Offline · \(machine?.name ?? machineID)")).lineLimit(1)
                            }
                            .font(Theme.Font.label)
                            .foregroundStyle(.secondary)
                        }
                    }
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityElement(children: .combine)
                .accessibilityLabel("\(model.persona.name), \(state), view profile")
                Spacer(minLength: Theme.Space.s)
                if let search {
                    // The shell's foot's round button (Settings beside New Chat), as the row above has it.
                    Button(action: search) {
                        ThemedIcon(Theme.Icon.search)
                            .font(Theme.Font.heading)
                            .foregroundStyle(Color.primary)
                            .scaledFrame(width: Theme.Size.avatarL, height: Theme.Size.avatarL)
                            .contentShape(.circle)
                    }
                    .buttonStyle(.plain)
                    .surface(interactive: true, in: .circle)
                    .accessibilityLabel("Search Chats [Assistant Sidebar 55c9e4]")
                    .accessibilityIdentifier("assistant-sidebar-search")
                }
                if let openSettings {
                    Button(action: openSettings) {
                        ThemedIcon(Theme.Icon.settings).font(Theme.Font.title).foregroundStyle(Color.primary)
                            .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Settings")
                }
            }
            .padding(.horizontal, Theme.Space.l)
            .padding(.vertical, Theme.Space.m)
        }
    }
}

#if os(macOS)
/// The Mac's foot of the list (#2113): one small line, where Persona runs and whether it is up, as a Mac app's status
/// at the bottom of its sidebar; a click opens its profile, as the phone's card does.
private struct AssistantStatusLine: View {
    @Environment(AppModel.self) private var model
    @Environment(\.tabIsActive) private var inFront
    let machineID: String
    let openProfile: () -> Void

    var body: some View {
        let machine = model.machine(model.hubRef(machineID), inFront: inFront)
        let online = machine?.online ?? false
        let place = machine?.name ?? machineID
        let state = online ? String(localized: "Online") : String(localized: "Offline [Machine Entity be1b4f]")
        VStack(spacing: 0) {
            Divider()
            Button(action: openProfile) {
                HStack(spacing: Theme.Space.s) {
                    Circle().fill(online ? Theme.positive : Color.secondary.opacity(0.5))
                        .scaledFrame(width: Theme.Size.dotXS, height: Theme.Size.dotXS)
                    Text("\(model.persona.name) \(state) · \(place)").lineLimit(1)
                    Spacer(minLength: 0)
                }
                .font(Theme.Font.caption)
                .foregroundStyle(.secondary)
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.s)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .help("View \(model.persona.name)'s Profile")
            .accessibilityLabel("\(model.persona.name), \(state), \(place), view profile")
        }
    }
}
#endif

/// A conversation's round mark in the sidebar: a chat bubble in a soft color picked by its id, so neighbours differ.
struct ConversationMark: View {
    var id: String

    private static let hues = [Theme.Hue.orange, Theme.Hue.purple, Theme.Hue.blue, Theme.Hue.green]

    var body: some View {
        let hue = Self.hues[Int(id.unicodeScalars.reduce(0) { ($0 &* 31 &+ Int($1.value)) & 0xFFFF }) % Self.hues.count]
        Circle()
            .fill(hue.opacity(0.14))
            .overlay(Circle().stroke(hue.opacity(0.25), lineWidth: Theme.Size.hairline))
            .scaledFrame(width: Theme.Sidebar.mark, height: Theme.Sidebar.mark)
            .overlay(ThemedIcon(Theme.Icon.conversation).font(Theme.Font.label).foregroundStyle(hue))
            .accessibilityHidden(true)
    }
}

/// The sidebar's conversations by day, newest first: Today, Yesterday, Previous 7 Days, Earlier (and those without a time).
nonisolated enum AssistantSidebarGroups {
    static let older = String(localized: "Earlier")

    struct Group: Equatable {
        var title: String
        var sessions: [Session]
    }

    /// The sidebar's Directions: the listed leader conversations in `leaders`' order (the hub's), never the main one;
    /// a leader the list does not hold (archived, another manager's) is left out.
    static func directions(_ sessions: [Session], leaders: [String], main: SessionKey?) -> [Session] {
        leaders.compactMap { id in sessions.first { $0.sessionID == id && $0.key != main } }
    }

    /// How many conversations the sidebar's Recent lists (#4873); the rest are in All Chats.
    static let recentLimit = 5

    /// The sidebar's Recent (#4873): the newest `recentLimit` conversations that are not the main one and not a direction
    /// (those are listed above it), and not ones Persona opened by itself (Auto Chats: the inbox's AI twin, scheduled tasks…,
    /// left in All Chats as Recent left them out before #4331). `leaders`: the directions' session ids.
    static func recent(_ sessions: [Session], leaders: [String], main: SessionKey?, tasks: AssistantTaskChats, hubSays: Bool) -> [Session] {
        let lead = Set(leaders)
        let rest = sessions.filter { $0.key != main && !lead.contains($0.sessionID) }
        let mine = AutoConversations(rest, tasks: tasks, current: nil, hubSays: hubSays).mine
        return Array(newestFirst(mine).prefix(recentLimit))
    }

    /// Newest first, a fixed order for equal times (the sidebar's Recent).
    static func newestFirst(_ sessions: [Session]) -> [Session] {
        sessions.sorted {
            let a = $0.updatedAt ?? .distantPast, b = $1.updatedAt ?? .distantPast
            return a != b ? a > b : $0.sessionID < $1.sessionID
        }
    }

    static func group(_ sessions: [Session], now: Date = .now, calendar: Calendar = .current) -> [Group] {
        let sorted = sessions.sorted {
            let a = $0.updatedAt ?? .distantPast, b = $1.updatedAt ?? .distantPast
            return a != b ? a > b : $0.sessionID < $1.sessionID // a fixed order for equal times
        }
        let today = calendar.startOfDay(for: now)
        let yesterday = calendar.date(byAdding: .day, value: -1, to: today) ?? today
        let week = calendar.date(byAdding: .day, value: -7, to: today) ?? today
        var out: [Group] = []
        for s in sorted {
            let title: String
            if let d = s.updatedAt, d.isMeaningful {
                title = d >= today ? String(localized: "Today") : d >= yesterday ? String(localized: "Yesterday") : d >= week ? String(localized: "Previous 7 Days") : older
            } else {
                title = older
            }
            if out.last?.title == title { out[out.count - 1].sessions.append(s) } else { out.append(Group(title: title, sessions: [s])) }
        }
        return out
    }
}

/// The sidebar's search: a conversation matches on its title or its last message, ignoring case and width.
nonisolated enum AssistantSidebarSearch {
    /// `names`: the directions' names by session id; a direction is found by its name too.
    static func filter(_ sessions: [Session], _ query: String, names: [String: String] = [:]) -> [Session] {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return sessions }
        let opts: String.CompareOptions = [.caseInsensitive, .widthInsensitive, .diacriticInsensitive]
        return sessions.filter {
            $0.title.range(of: q, options: opts) != nil || ($0.lastText ?? "").range(of: q, options: opts) != nil
                || (names[$0.sessionID] ?? "").range(of: q, options: opts) != nil
        }
    }
}

/// The conversations the sidebar showed last (not observed: keeping them changes nothing on screen).
final class SidebarFeed {
    private var last: [Session] = []

    /// `live` when the sidebar can be seen; nil while it is closed, for the list it had.
    func conversations(_ live: [Session]?) -> [Session] {
        if let live { last = live }
        return last
    }

    private var archivedCount = 0

    /// Archived's count the same way.
    func archived(_ live: Int?) -> Int {
        if let live { archivedCount = live }
        return archivedCount
    }
}

/// A sidebar heading's ⋯ menu (#778: Pin with only the main conversation pinned drew a ⋯ that opened nothing).
nonisolated enum HeadingMenu {
    /// Whether the menu has nothing in it now, so the heading draws no ⋯: no items, or items behind one `if` that is
    /// false, which a view builder makes a nil `Optional` rather than an `EmptyView`. Only those two shapes are looked
    /// into: several `if`s, an `if` / `else`, a `ForEach` or a `Group` count as items even when they draw nothing.
    static func isEmpty(_ items: some View) -> Bool {
        if items is EmptyView { return true }
        let mirror = Mirror(reflecting: items)
        return mirror.displayStyle == .optional && mirror.children.isEmpty
    }

    /// The heading's least height: one whose menu can empty keeps the ⋯'s height without it, so what is below does
    /// not jump; one that never has a menu (the search's 「N 个对话」) keeps its own.
    static func minHeight<Items: View>(_: Items.Type) -> CGFloat? {
        Items.self == EmptyView.self ? nil : Theme.Size.wellXS
    }
}

