import Foundation

/// The same component renderer serves extension pages and assistant cards; only their control transport differs.
@MainActor
protocol ExtensionViewControlling: AnyObject {
    var values: [String: String] { get set }
    var query: String { get set }
    var running: String? { get }
    var loadingMore: String? { get }
    var fieldsEnabled: Bool { get }
    var managesConfirmation: Bool { get }
    func isEnabled(_ action: ExtensionAction) -> Bool
    func selected(_ action: String) -> Bool
    func prepared(_ action: ExtensionAction, with chosen: [String: String], displayed: ExtensionDisplayedRevision?) -> @MainActor () async -> ExtensionViewModel.Outcome
    func perform(_ action: ExtensionAction, with chosen: [String: String]) async -> ExtensionViewModel.Outcome
    func choose(_ name: String, _ value: String, action: ExtensionAction) async -> ExtensionViewModel.Outcome
    func filter(_ value: String) async -> ExtensionViewModel.Outcome
    func search() async -> ExtensionViewModel.Outcome
    func more(_ more: ExtensionMore) async -> ExtensionViewModel.Outcome

    // UI version 6's controls in rows (docs/extensions/extensions.md "UI version 6", "Controls").
    /// It sends what a control is changed to; false (an assistant card): controls are drawn as they are, not usable.
    var sendsControls: Bool { get }
    /// What a control shows, by its name, while the user's value is asked about or on its way (else the extension's).
    var controlValues: [String: String] { get }
    /// Controls whose change has been on its way for over a second (a spinner shows beside them).
    var slowControls: Set<String> { get }
    /// Shows `value` for the control `name` before it is sent (a slider dragged, a stepper tapped, a confirm asked);
    /// nil: the extension's value again, unless a change of it is on its way.
    func preview(_ name: String, _ value: String?)
    /// Sends a control's new value (after its confirm): shown at once, one request a control at a time.
    func change(_ control: ExtensionRowControl, to value: String) async -> ExtensionViewModel.Outcome
}

extension ExtensionViewControlling {
    var fieldsEnabled: Bool { true }
    var managesConfirmation: Bool { false }
    func isEnabled(_ action: ExtensionAction) -> Bool { running == nil }
    func selected(_ action: String) -> Bool { false }
    func prepared(_ action: ExtensionAction, with chosen: [String: String] = [:], displayed: ExtensionDisplayedRevision? = nil) -> @MainActor () async -> ExtensionViewModel.Outcome {
        { await self.perform(action, with: chosen) }
    }
    func perform(_ action: ExtensionAction) async -> ExtensionViewModel.Outcome { await perform(action, with: [:]) }
    var sendsControls: Bool { false }
    var controlValues: [String: String] { [:] }
    var slowControls: Set<String> { [] }
    func preview(_ name: String, _ value: String?) {}
    func change(_ control: ExtensionRowControl, to value: String) async -> ExtensionViewModel.Outcome { .init() }
}

extension ExtensionViewModel: ExtensionViewControlling {}
