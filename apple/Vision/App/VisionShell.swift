import SwiftUI

#if os(visionOS)
extension View {
    /// The main window's shell on Apple Vision Pro, around the app's TabView (RootView's `RootTabs`, which visionOS
    /// draws as the system's tab ornament at the window's leading edge): the Needs You ornament below the window.
    func visionShell() -> some View { modifier(VisionShell()) }
}

private struct VisionShell: ViewModifier {
    @Environment(AppModel.self) private var model

    func body(content: Content) -> some View {
        // The tab badges' numbers (AppModel.badges): published only when one changes, so a push does not redraw this.
        let waiting = model.badges.assistant + model.badges.agents
        content
            .ornament(visibility: waiting > 0 ? .visible : .hidden, attachmentAnchor: .scene(.bottom), contentAlignment: .top) {
                NeedsYouOrnament(waiting: waiting)
            }
            #if DEBUG
            .modifier(VisionDebugWindowOpener())
            #endif
    }
}

/// How many questions, plans and approvals wait on the user (the same count as the tab badges: Persona's and the
/// Agents', muted agents left out), and a button to the one waiting longest. Again: the next one after the one open.
private struct NeedsYouOrnament: View {
    @Environment(AppModel.self) private var model
    var waiting: Int

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            Image(systemName: Theme.Icon.needsYou)
                .foregroundStyle(Theme.needsYou)
                .accessibilityHidden(true)
            Text(StateName.needsYou.text)
                .font(Theme.Font.bodyStrong)
                .lineLimit(1)
            CountBadge(count: waiting, look: .needs)
            Button(action: openNext) {
                Label("Go [Vision Shell a7e9f6]", systemImage: Theme.Icon.forward)
                    .labelStyle(.titleAndIcon)
                    .lineLimit(1)
            }
            .themedButton(prominent: true)
            .accessibilityIdentifier("vision-needs-you-open")
        }
        .fixedSize()
        .visionOrnamentPanel()
        .accessibilityElement(children: .contain)
        .accessibilityLabel(StateName.count(.needsYou, waiting))
    }

    /// The waiting ones as the badges count them, longest waiting first; the one after the conversation on screen.
    private func openNext() {
        let keys = model.pendingInteractions.map(\.sessionKey).filter { k in
            model.latest(k)?.assistant == true || model.isQuiet(k) || !model.isMuted(k)
        }
        var order: [SessionKey] = []
        for k in keys where !order.contains(k) { order.append(k) }
        guard !order.isEmpty else { return }
        let next = model.visibleSession.flatMap { order.firstIndex(of: $0) }.map { order[($0 + 1) % order.count] } ?? order[0]
        model.open(next)
    }
}
#endif
