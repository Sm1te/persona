import SwiftUI

/// The composer's input line, shared by an agent's chat (`Composer`) and a new assistant conversation
/// (`StarterComposer`) so the two look alike: [+] with the caller's menu, the field (a glass capsule, at least 44 pt),
/// then send (faded while there is nothing to send) or, while the agent works and nothing is typed, a red stop.
/// The callers keep their own state: the chat's drafts, SendSafety and uploads stay in `Composer`.
struct ComposerRow<MenuItems: View>: View {
    var placeholder: String
    @Binding var text: String
    @Binding var selection: TextSelection?
    var focused: FocusState<Bool>.Binding
    var canSend: Bool
    /// Set while the agent works: shown instead of send when there is nothing to send.
    var stop: (() -> Void)?
    var send: () -> Void
    /// What goes with the message, quoted over the field inside its box (#986).
    var quotes: Binding<[ContextRef]>? = nil
    /// The conversation whose sent messages ↑ / ↓ and ⌃R bring back (the Mac, `MacSendHistory`); nil: none.
    var history: String? = nil
    /// The main conversation cannot answer yet: send says what to do first and goes there, the draft stays.
    var gate: SendGate? = nil
    @ViewBuilder var menu: () -> MenuItems

    /// The symbols' frame: the button styles pad it to about 44 pt, the field's height.
    private static var glyph: CGFloat { 31 }

    var body: some View {
        HStack(alignment: .bottom, spacing: Theme.Space.s) {
            Menu { menu() } label: {
                ThemedIcon(Theme.Icon.add).font(Theme.Font.heading).frame(width: Self.glyph, height: Self.glyph)
            }
            #if os(macOS)
            // A Mac menu draws as a pull-down button with its arrow beside the label: clipped to the circle, the +
            // was cut in half (2026-11-02). As a plain button with no arrow, in a round glass of its own.
            .menuStyle(.button)
            .menuIndicator(.hidden)
            .macRoundButton()
            #else
            .themedButton()
            .clipShape(.circle)
            #endif

            VStack(alignment: .leading, spacing: 0) {
                if let quotes, !quotes.wrappedValue.isEmpty {
                    QuoteBar(refs: quotes)
                        .padding(.leading, Theme.Space.l)
                        .padding(.trailing, Theme.Space.xs)
                        .padding(.top, Theme.Space.s)
                }
                TextField(placeholder, text: $text, selection: $selection, axis: .vertical)
                    .lineLimit(1...6)
                    #if os(macOS)
                    .textFieldStyle(.plain) // the surface below is the field's box; the Mac's own bezel drew a second one inside
                    .macComposerKeys(focused: focused.wrappedValue, canSend: canSend, // ⏎ sends, ⇧⏎ a new line
                                     history: history.map { MacComposerHistory(key: $0, text: $text, focus: { focused.wrappedValue = true }) }, send: send)
                    #endif
                    .focused(focused)
                    .padding(.horizontal, Theme.Space.l)
                    .padding(.vertical, Theme.Space.m)
                    .scaledFrame(minHeight: Theme.Size.tapTarget)
            }
            .surface(interactive: true, in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))

            if let gate {
                SendGateButton(gate: gate)
            } else if let stop, !canSend {
                Button(action: stop) {
                    ThemedIcon(Theme.Icon.interrupt).font(Theme.Font.heading).frame(width: Self.glyph, height: Self.glyph)
                }
                #if os(macOS)
                .macRoundButton(prominent: true)
                #else
                .themedButton(prominent: true)
                .clipShape(.circle)
                #endif
                .tint(Theme.danger)
                .accessibilityLabel("Interrupt [Composer Buttons f9d193]")
            } else {
                Button(action: sendTyped) {
                    ThemedIcon(Theme.Icon.send).font(Theme.Font.heading).frame(width: Self.glyph, height: Self.glyph)
                }
                #if os(macOS)
                .macRoundButton(prominent: true)
                #else
                .themedButton(prominent: true)
                .clipShape(.circle)
                #endif
                .disabled(!canSend)
                .keyboardShortcut(.return, modifiers: .command)
                .accessibilityLabel("Send")
            }
            #if os(iOS)
            if focused.wrappedValue {
                Button(action: dismissKeyboard) {
                    ThemedIcon(Theme.Icon.dismissKeyboard).font(Theme.Font.heading).frame(width: Self.glyph, height: Self.glyph)
                }
                .themedButton()
                .clipShape(.circle)
                .accessibilityLabel("Hide Keyboard")
                .accessibilityIdentifier("composer-row-dismiss-keyboard")
                .transition(.scale.combined(with: .opacity))
            }
            #endif
        }
    }
}

extension ComposerRow {
    /// Sends what the field shows: the keyboard's composition confirmed into the draft first (`TypedText`, #2203).
    fileprivate func sendTyped() {
        #if os(iOS) || os(visionOS)
        if focused.wrappedValue { TypedText.commitFocused(into: $text) }
        #endif
        #if os(macOS)
        if let history { MacSendHistory.shared.record(history, text) }
        #endif
        send()
    }

    #if os(iOS)
    fileprivate func dismissKeyboard() {
        Haptics.tap()
        if focused.wrappedValue { TypedText.commitFocused(into: $text) }
        focused.wrappedValue = false
        Keyboard.dismiss()
    }
    #endif
}

/// A chat's composer's photos, videos and file uploads (#2626): kept by `AppModel.composerAttachments(_:)` per
/// conversation, as the text is in `drafts`, not by the composer, which is built again with the whole tree when iOS
/// snapshots the app in another width on its way to the background (iPhone Duo open).
@MainActor @Observable
final class ComposerAttachments {
    var images: [(UIImage, ImagePayload)] = [] {
        didSet { edited.removeAll { e in !images.contains { $0.0 === e.result } } }
    }
    let uploads = FileUploads()
    let videos = VideoAttachments()
    /// The photos here that were edited (in the strip or the + panel): each one's source, edits and history beside the
    /// picture that goes, so the editor opens it again as it was left. Dropped with the photo.
    private(set) var edited: [EditedImage] = []

    /// How `image`, one of `images`, was edited; nil when it never was.
    func edits(of image: UIImage) -> EditedImage? { edited.first { $0.result === image } }

    /// `image` in the strip becomes `edit`'s result, in its place, and remembers how it was made; nothing when it is gone
    /// (another window on the same chat took it out).
    func replace(_ image: UIImage, with edit: EditedImage) {
        guard edit.result !== image, let i = images.firstIndex(where: { $0.0 === image }), let p = MessagePhotos.payload(edit.result) else { return }
        var kept = edit
        kept.result = p.0
        images[i] = p
        edited.append(kept)
    }

    /// An edited picture after the photos (at most `MessagePhotos.limit`, a toast says so).
    func add(_ edit: EditedImage, model: AppModel) {
        let before = images.count
        MessagePhotos.add(edit.result, to: &images, model: model)
        guard images.count > before, let added = images.last?.0 else { return }
        var kept = edit
        kept.result = added
        edited.append(kept)
    }
}

/// The new conversation's composer (#2626): its text and quotes as well, there being no conversation yet whose draft
/// could keep them; kept by `AppModel.starterDraft(_:)` per page (the assistant's, Health's).
@MainActor @Observable
final class StarterDraft {
    var text = ""
    var quotes: [ContextRef] = []
    let held = ComposerAttachments()
    /// One more for each message sent: a file still uploading for one that went puts its path in no later message.
    private(set) var generation = 0

    /// The message went: an empty box for the next one.
    func sent() {
        text = ""
        quotes = []
        held.images = []
        held.videos.sent()
        generation += 1
    }

    /// The first message as it goes, from what was typed (`typed`, trimmed) and the box: the quoted sessions as
    /// <persona-context> tags first, as in a chat; with nothing typed, a line saying what to look at; then the videos.
    func message(_ typed: String, files: [AttachedFile]) -> String {
        let said = typed.isEmpty
            ? (held.images.isEmpty ? (files.isEmpty ? String(localized: "See the context above.") : String(localized: "Please look at the image [Assistant View a3e0a0]"))
                                   : String(localized: "Please look at the image [Assistant View 5ad878]"))
            : typed
        let body = AttachedFile.message(said, files: files)
        return quotes.isEmpty ? body : quotes.map(\.xml).joined(separator: "\n") + "\n\n" + body
    }
}

/// The composer's Take Photo and Glasses Photo, in a conversation and a new one alike: the system camera over the page,
/// the glasses' viewfinder in a sheet (iOS); each picture taken goes to `add`.
struct ComposerCaptureSheets: ViewModifier {
    @Binding var camera: Bool
    @Binding var glasses: Bool
    var add: (UIImage) -> Void

    func body(content: Content) -> some View {
        content
            .fullScreenCover(isPresented: $camera) {
                CameraPicker { img in add(img) }.ignoresSafeArea()
            }
            #if os(iOS)
            .sheet(isPresented: $glasses) {
                GlassesCaptureSheet { img in add(img) }
            }
            #endif
    }
}

/// Photos picked for the next message, above the composer: × takes one out; with `edit`, a tap marks it up first.
struct ComposerImages: View {
    @Binding var images: [(UIImage, ImagePayload)]
    var edit: ((Int) -> Void)?

    /// A conversation's photos (`held`): a tap opens one in the image editor, which picks up where an earlier edit (here
    /// or in the + panel) left off; Done puts the result in its place.
    static func editable(_ held: ComposerAttachments) -> some View { ComposerEditableImages(held: held) }

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack {
                ForEach(Array(images.enumerated()), id: \.offset) { idx, pair in
                    #if os(macOS)
                    MacComposerThumb(image: pair.0, index: idx, editable: edit != nil,
                                     edit: { edit?(idx) }, remove: { images.remove(at: idx) })
                    #else
                    Image(uiImage: pair.0).resizable().scaledToFill().scaledFrame(width: Theme.Size.box56, height: Theme.Size.box56)
                        .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
                        .overlay(alignment: .bottomLeading) {
                            if edit != nil {
                                ThemedIcon("pencil.tip.crop.circle.fill").font(Theme.Font.label).foregroundStyle(.white)
                                    .shadow(radius: 2).padding(Theme.Space.xs)
                            }
                        }
                        .onTapGesture { edit?(idx) }
                        .accessibilityLabel(edit == nil ? String(localized: "Image \(idx + 1) [Composer Parts 0d2e3a]") : String(localized: "Image \(idx + 1), tap to edit"))
                        .overlay(alignment: .topTrailing) {
                            Button { images.remove(at: idx) } label: {
                                ThemedIcon(Theme.Icon.clear).symbolRenderingMode(.palette)
                                    .foregroundStyle(.white, .black.opacity(0.6))
                            }
                            .offset(x: 5, y: -5)
                            .accessibilityLabel("Remove Image \(idx + 1)") // same key as the Mac thumbnail
                        }
                    #endif
                }
            }
            .padding(.top, Theme.Space.sm)
        }
    }
}

/// `ComposerImages` with the image editor behind a tap, for the composers that keep their photos in a
/// `ComposerAttachments` (a conversation's, a new conversation's).
private struct ComposerEditableImages: View {
    let held: ComposerAttachments
    @State private var editing: EditingPhoto?

    private struct EditingPhoto: Identifiable {
        let image: UIImage
        var id: ObjectIdentifier { ObjectIdentifier(image) }
    }

    var body: some View {
        ComposerImages(images: Binding(get: { held.images }, set: { held.images = $0 })) { i in
            if held.images.indices.contains(i) { editing = EditingPhoto(image: held.images[i].0) }
        }
        .fullScreenCover(item: $editing) { e in
            ImageEditor(held.edits(of: e.image) ?? EditedImage(source: e.image)) { edited in
                held.replace(e.image, with: edited)
            }
        }
    }
}
