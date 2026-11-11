import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// The image editor, over the picture on black as Photos edits: Box (the tool it opens on, as it always has) and Pen in
/// six inks and three widths, Crop (free or a fixed shape, turn left, reset), and Undo / Redo over all of it. The source
/// is never changed: Done hands back an `EditedImage` (the source, the edits, their history and the picture drawn from
/// them), and one handed in again carries on where it stopped. From the + panel (`send`) a caption and Send sit under
/// the tools.
struct ImageEditor: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    /// Where it starts: a fresh `EditedImage(source:)`, or one edited before.
    let start: EditedImage
    /// Title of the done button (what happens with the result: "Add to Message", "Done", "Select").
    var doneTitle: String = String(localized: "Done [Extension System Presentation c0b3fb]")
    /// From the + panel: the caption and Send under the tools.
    var send: ImageEditorSend? = nil
    var onDone: (EditedImage) -> Void

    enum Tool: Hashable { case box, pen, crop }

    @State private var tool: Tool = .box
    @State private var ink = 0
    @State private var lineWidth = Theme.ImageEdit.defaultLineWidth
    @State private var edits: ImageEdits
    @State private var history: EditHistory
    @State private var current: ImageMark?
    @State private var cropping: (handle: CropHandle, start: CGRect)?
    /// The quarter turns as drawn: turning left takes a quarter off, so the picture animates the short way round.
    @State private var shownTurns: Int
    /// The source's pixels, upright and at most the size sent; decoded off the main thread once the editor is up.
    @State private var pixels: CGImage?
    @State private var caption: String
    @FocusState private var captionFocused: Bool
    @State private var finishing = false

    init(_ start: EditedImage, doneTitle: String = String(localized: "Done [Extension System Presentation c0b3fb]"),
         send: ImageEditorSend? = nil, onDone: @escaping (EditedImage) -> Void) {
        self.start = start
        self.doneTitle = doneTitle
        self.send = send
        self.onDone = onDone
        _edits = State(initialValue: start.edits)
        _history = State(initialValue: start.history)
        _shownTurns = State(initialValue: start.edits.turns)
        _caption = State(initialValue: send?.caption.wrappedValue ?? "")
    }

    private var pixelSize: CGSize { pixels.map { CGSize(width: $0.width, height: $0.height) } ?? .zero }

    var body: some View {
        NavigationStack {
            GeometryReader { geo in
                ZStack {
                    Color.black.ignoresSafeArea()
                    if pixels != nil {
                        canvas(area: geo.size)
                    } else {
                        ProgressView().tint(.white)
                    }
                }
            }
            .safeAreaInset(edge: .bottom) { toolbar }
            .navigationTitle("Edit Image")
            .navigationBarTitleDisplayMode(.inline)
            .toolbarBackground(.black, for: .navigationBar)
            .toolbarBackground(.visible, for: .navigationBar)
            .toolbarColorScheme(.dark, for: .navigationBar)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .accessibilityIdentifier("image-edit-cancel")
                }
                // Undo and Redo up top, as Photos has them: the tools' rows below stay one line on the smallest iPhone. One
                // item: the bar's middle shows only the first of a group.
                ToolbarItem(placement: .principal) {
                    HStack(spacing: Theme.Space.s) {
                        Button(action: undo) { ThemedIcon(Theme.Icon.undo).tapTargetFrame() }
                            .disabled(!history.canUndo || finishing)
                            .accessibilityLabel("Undo")
                            .accessibilityIdentifier("image-edit-undo")
                        Button(action: redo) { ThemedIcon(Theme.Icon.redo).tapTargetFrame() }
                            .disabled(!history.canRedo || finishing)
                            .accessibilityLabel("Redo")
                            .accessibilityIdentifier("image-edit-redo")
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(doneTitle) { finish(sending: false) }
                        .fontWeight(.semibold)
                        .lineLimit(1)
                        .disabled(pixels == nil || finishing)
                        .accessibilityIdentifier("image-edit-done")
                }
            }
        }
        // Dark whatever the appearance, as the image viewer. Through the environment, not `preferredColorScheme`: that
        // reaches past this cover to the one it was presented from (the + panel stayed dark after the editor closed).
        .environment(\.colorScheme, .dark)
        .task {
            guard pixels == nil else { return }
            pixels = await ImageEditWork.pixels(start.source)
        }
    }

    // MARK: The picture

    /// Where things are on screen. Marking, the crop fills the room; cropping, all of the turned picture shows, inset so
    /// the handles on its edges can be taken, the crop's frame over it.
    private struct Layout {
        /// The whole turned picture's frame (past the screen's room where it is cropped away), and points per pixel.
        var picture: CGRect
        var scale: CGFloat
        /// What shows of it.
        var visible: CGRect
        /// The crop's frame.
        var crop: CGRect

        /// A point on screen in the turned picture's pixels, kept inside what shows.
        func pixel(_ p: CGPoint) -> CGPoint {
            let q = CGPoint(x: min(max(p.x, visible.minX), visible.maxX), y: min(max(p.y, visible.minY), visible.maxY))
            return CGPoint(x: (q.x - picture.minX) / scale, y: (q.y - picture.minY) / scale)
        }

        func screen(_ p: CGPoint) -> CGPoint { CGPoint(x: picture.minX + p.x * scale, y: picture.minY + p.y * scale) }
    }

    private func layout(_ area: CGSize) -> Layout {
        let size = pixelSize
        let turned = edits.turned(size)
        guard turned.width > 0, turned.height > 0 else { return Layout(picture: .zero, scale: 1, visible: .zero, crop: .zero) }
        if tool == .crop {
            let inset = Theme.ImageEdit.cropInset
            let picture = Self.fit(turned, in: CGRect(origin: .zero, size: area).insetBy(dx: inset, dy: inset))
            let c = edits.crop
            let crop = CGRect(x: picture.minX + c.minX * picture.width, y: picture.minY + c.minY * picture.height,
                              width: c.width * picture.width, height: c.height * picture.height)
            return Layout(picture: picture, scale: picture.width / turned.width, visible: picture, crop: crop)
        }
        let kept = edits.cropRect(size)
        let shown = Self.fit(kept.size, in: CGRect(origin: .zero, size: area))
        let scale = shown.width / max(kept.width, 1)
        let picture = CGRect(x: shown.minX - kept.minX * scale, y: shown.minY - kept.minY * scale,
                             width: turned.width * scale, height: turned.height * scale)
        return Layout(picture: picture, scale: scale, visible: shown, crop: shown)
    }

    @ViewBuilder private func canvas(area: CGSize) -> some View {
        let l = layout(area)
        let size = pixelSize
        ZStack {
            // The source as it came, turned by a rotation and cropped by a mask: nothing is drawn again for either.
            Image(uiImage: start.source).resizable()
                .frame(width: size.width * l.scale, height: size.height * l.scale)
                .rotationEffect(.degrees(90 * Double(shownTurns)))
                .position(x: l.picture.midX, y: l.picture.midY)
                .frame(width: area.width, height: area.height)
                .mask { Rectangle().frame(width: l.visible.width, height: l.visible.height).position(x: l.visible.midX, y: l.visible.midY) }
                .accessibilityLabel("Photo")
                .accessibilityAddTraits(.isImage)
            Canvas { ctx, _ in
                ctx.clip(to: Path(l.visible))
                for m in edits.marks + (current.map { [$0] } ?? []) where m.points.count > 1 {
                    let path = Path(m.path { l.screen(edits.turn($0, in: size)) })
                    ctx.stroke(path, with: .color(Color(cgColor: ImageMark.color(m.ink))),
                               style: StrokeStyle(lineWidth: m.width * l.scale, lineCap: .round, lineJoin: .round))
                }
            }
            .allowsHitTesting(false)
            .accessibilityHidden(true)
            if tool == .crop { CropFrame(frame: l.crop, bounds: l.picture, dragging: cropping != nil) }
        }
        .frame(width: area.width, height: area.height)
        .contentShape(.rect)
        .gesture(tool == .crop ? nil : markGesture(l, size: size))
        .gesture(tool == .crop ? cropGesture(l) : nil)
        .animation(reduceMotion ? nil : Theme.Motion.state, value: tool)
        .animation(reduceMotion ? nil : Theme.Motion.state, value: shownTurns)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("image-edit-canvas")
    }

    private func markGesture(_ l: Layout, size: CGSize) -> some Gesture {
        DragGesture(minimumDistance: 0)
            .onChanged { v in
                let p = edits.unturn(l.pixel(v.location), in: size)
                if current == nil {
                    captionFocused = false // drawing on the picture puts the caption's keyboard away
                    let width = Theme.ImageEdit.lineWidths[lineWidth] / max(l.scale, .ulpOfOne)
                    current = ImageMark(kind: tool == .pen ? .pen : .box, ink: ink, width: width, points: [p])
                } else {
                    current?.points.append(p)
                }
            }
            .onEnded { _ in
                if let c = current, c.points.count > 1 { change { $0.marks.append(c) } }
                current = nil
            }
    }

    private func cropGesture(_ l: Layout) -> some Gesture {
        DragGesture(minimumDistance: 0)
            .onChanged { v in
                if cropping == nil {
                    captionFocused = false
                    guard let handle = Self.handle(at: v.startLocation, in: l.crop) else { return }
                    cropping = (handle, edits.crop)
                }
                guard let (handle, from) = cropping, l.picture.width > 0, l.picture.height > 0 else { return }
                let delta = CGSize(width: v.translation.width / l.picture.width, height: v.translation.height / l.picture.height)
                let minimum = CGSize(width: Theme.ImageEdit.cropMinimum / l.picture.width, height: Theme.ImageEdit.cropMinimum / l.picture.height)
                let ratio = edits.shape.ratio(edits.turned(pixelSize)).map { $0 * l.picture.height / l.picture.width }
                edits.crop = ImageEdits.drag(from, handle: handle, by: delta, minimum: minimum, ratio: ratio)
            }
            .onEnded { _ in
                guard let (_, from) = cropping else { return }
                cropping = nil
                if edits.crop != from {
                    var before = edits
                    before.crop = from
                    history.record(before)
                }
            }
    }

    /// What a finger at `p` takes of a crop framed at `frame`: a corner or an edge within reach, else inside it; nil
    /// outside it.
    static func handle(at p: CGPoint, in frame: CGRect) -> CropHandle? {
        let reach = Theme.ImageEdit.cropGrab
        guard frame.insetBy(dx: -reach, dy: -reach).contains(p) else { return nil }
        let left = abs(p.x - frame.minX) <= reach, right = !left && abs(p.x - frame.maxX) <= reach
        let top = abs(p.y - frame.minY) <= reach, bottom = !top && abs(p.y - frame.maxY) <= reach
        switch (left, right, top, bottom) {
        case (true, _, true, _): return .topLeading
        case (_, true, true, _): return .topTrailing
        case (true, _, _, true): return .bottomLeading
        case (_, true, _, true): return .bottomTrailing
        case (true, _, _, _): return .leading
        case (_, true, _, _): return .trailing
        case (_, _, true, _): return .top
        case (_, _, _, true): return .bottom
        default: return frame.contains(p) ? .inside : nil
        }
    }

    static func fit(_ size: CGSize, in box: CGRect) -> CGRect {
        guard size.width > 0, size.height > 0, box.width > 0, box.height > 0 else { return .zero }
        let s = min(box.width / size.width, box.height / size.height)
        let w = size.width * s, h = size.height * s
        return CGRect(x: box.midX - w / 2, y: box.midY - h / 2, width: w, height: h)
    }

    // MARK: Changes

    /// One step of the history: the edits changed by `make`, the ones before kept for Undo.
    private func change(_ make: (inout ImageEdits) -> Void) {
        var next = edits
        make(&next)
        guard next != edits else { return }
        history.record(edits)
        edits = next
        settleTurns()
    }

    private func undo() {
        guard let back = history.undo(edits) else { return }
        edits = back
        settleTurns()
    }

    private func redo() {
        guard let ahead = history.redo(edits) else { return }
        edits = ahead
        settleTurns()
    }

    /// The drawn turns brought to the edits' by the shortest way (a turn left has already taken its quarter off).
    private func settleTurns() {
        let off = ((edits.turns - shownTurns) % 4 + 4) % 4
        shownTurns += off == 3 ? -1 : off
    }

    private func turnLeft() {
        change { $0.turnLeft() }
    }

    private func finish(sending: Bool) {
        guard !finishing, let pixels else { return }
        finishing = true
        captionFocused = false
        let edits = edits, history = history
        Task {
            let result = edits.isEmpty ? start.source : await ImageEditWork.render(edits, over: pixels) ?? start.source
            let edited = EditedImage(source: start.source, edits: edits, history: history, result: result)
            if let send {
                send.caption.wrappedValue = caption
                if sending { send.send(edited) } else { onDone(edited) }
            } else {
                onDone(edited)
            }
            dismiss()
        }
    }

    // MARK: Tools

    private var toolbar: some View {
        VStack(spacing: Theme.Space.m) {
            options
            HStack(spacing: Theme.Space.s) {
                ViewThatFits(in: .horizontal) {
                    toolButtons(labeled: true)
                    toolButtons(labeled: false)
                }
                Spacer(minLength: 0)
                if tool != .crop {
                    iconButton(Theme.Icon.delete, String(localized: "Clear [Image Editor bce237]"), id: "image-edit-clear") {
                        change { $0.marks = [] }
                    }
                    .disabled(edits.marks.isEmpty)
                }
            }
            if send != nil { captionRow }
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
        .background(.black)
        .disabled(pixels == nil || finishing)
    }

    private func toolButtons(labeled: Bool) -> some View {
        HStack(spacing: Theme.Space.s) {
            toolButton(.box, String(localized: "Box"), Theme.Icon.markupBox, labeled: labeled)
            toolButton(.pen, String(localized: "Pen"), Theme.Icon.markupPen, labeled: labeled)
            toolButton(.crop, String(localized: "Crop"), Theme.Icon.crop, labeled: labeled)
        }
    }

    private func toolButton(_ t: Tool, _ title: String, _ symbol: String, labeled: Bool) -> some View {
        Button { tool = t } label: {
            if labeled {
                Label { Text(title).lineLimit(1) } icon: { ThemedIcon(symbol) }
                    .font(Theme.Font.secondaryStrong)
            } else {
                ThemedIcon(symbol).font(Theme.Font.secondaryStrong)
            }
        }
        .themedButton(prominent: tool == t)
        .accessibilityLabel(title)
        .accessibilityAddTraits(tool == t ? .isSelected : [])
        .accessibilityIdentifier("image-edit-tool-\(t)")
    }

    private func iconButton(_ symbol: String, _ label: String, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) { ThemedIcon(symbol).scaledFrame(width: Theme.Size.icon) }
            .themedButton()
            .accessibilityLabel(label)
            .accessibilityIdentifier(id)
    }

    /// What the tool shown can change: the ink and the width for Box and Pen, the turn, the shape and Reset for Crop. At
    /// large text sizes Crop's buttons drop their words and the inks scroll, the width staying in sight; nothing wraps.
    @ViewBuilder private var options: some View {
        if tool == .crop {
            ViewThatFits(in: .horizontal) {
                cropRow(labeled: true)
                cropRow(labeled: false)
                ScrollView(.horizontal, showsIndicators: false) { cropRow(labeled: false) }
            }
        } else {
            HStack(spacing: Theme.Space.s) {
                ViewThatFits(in: .horizontal) {
                    swatches
                    ScrollView(.horizontal, showsIndicators: false) { swatches }
                }
                widthMenu
                Spacer(minLength: 0)
            }
        }
    }

    private func cropRow(labeled: Bool) -> some View {
        HStack(spacing: Theme.Space.s) {
            Button(action: turnLeft) {
                if labeled {
                    Label { Text("Rotate").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.rotateLeft) }
                        .font(Theme.Font.secondaryStrong)
                } else {
                    ThemedIcon(Theme.Icon.rotateLeft).font(Theme.Font.secondaryStrong)
                }
            }
            .themedButton()
            .accessibilityLabel("Rotate")
            .accessibilityIdentifier("image-edit-rotate")
            shapeMenu(labeled: labeled)
            Spacer(minLength: 0)
            Button { change { $0.resetFrame() } } label: {
                Text("Reset [Image Editor 4b1e7c]").lineLimit(1).font(Theme.Font.secondaryStrong)
            }
            .themedButton()
            .disabled(edits.isFramedAsIs)
            .accessibilityIdentifier("image-edit-reset")
        }
    }

    private var swatches: some View {
        HStack(spacing: Theme.Space.flush) {
            ForEach(Theme.ImageEdit.inks.indices, id: \.self) { i in
                Button { ink = i } label: {
                    Circle().fill(Color(cgColor: ImageMark.color(i)))
                        .scaledFrame(width: Theme.Size.iconL, height: Theme.Size.iconL)
                        .overlay(Circle().stroke(.white, lineWidth: ink == i ? Theme.ImageEdit.swatchRing : Theme.ImageEdit.swatchRingIdle)
                            .opacity(ink == i ? 1 : Theme.ImageEdit.swatchRingIdleOpacity))
                        .tapTargetFrame()
                        .contentShape(.circle)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(Self.inkName(i))
                .accessibilityAddTraits(ink == i ? .isSelected : [])
            }
        }
    }

    private var widthMenu: some View {
        Menu {
            Picker(selection: $lineWidth) {
                ForEach(Theme.ImageEdit.lineWidths.indices, id: \.self) { i in Text(Self.widthName(i)).tag(i) }
            } label: {
                Text("Line Width")
            }
            .pickerStyle(.inline)
        } label: {
            ThemedIcon(Theme.Icon.lineWidth).font(Theme.Font.heading).foregroundStyle(.white).tapTargetFrame()
        }
        .accessibilityLabel("Line Width")
        .accessibilityValue(Self.widthName(lineWidth))
        .accessibilityIdentifier("image-edit-width")
    }

    private func shapeMenu(labeled: Bool) -> some View {
        Menu {
            Picker(selection: Binding(get: { edits.shape }, set: { s in change { $0.reshape(s, size: pixelSize) } })) {
                ForEach(CropShape.allCases) { s in Text(Self.shapeName(s)).tag(s) }
            } label: {
                Text("Aspect Ratio")
            }
            .pickerStyle(.inline)
        } label: {
            if labeled {
                Label { Text(Self.shapeName(edits.shape)).lineLimit(1) } icon: { ThemedIcon(Theme.Icon.cropRatio) }
                    .font(Theme.Font.secondaryStrong)
            } else {
                ThemedIcon(Theme.Icon.cropRatio).font(Theme.Font.secondaryStrong)
            }
        }
        .themedButton()
        .accessibilityLabel("Aspect Ratio")
        .accessibilityValue(Self.shapeName(edits.shape))
        .accessibilityIdentifier("image-edit-shape")
    }

    /// The panel's caption (the message's) and Send, as under the panel's grid.
    private var captionRow: some View {
        HStack(alignment: .bottom, spacing: Theme.Space.xs) {
            TextField("Add a caption…", text: $caption, axis: .vertical)
                .lineLimit(1...4)
                .font(Theme.Font.body)
                .focused($captionFocused)
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.s)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
                .modifier(ComposerSurface())
                .accessibilityIdentifier("image-edit-caption")
            ComposerRoundButton(symbol: Theme.Icon.send, label: String(localized: "Send"), id: "image-edit-send", pressScales: true) {
                finish(sending: true)
            }
        }
    }

    static func inkName(_ i: Int) -> String {
        switch i {
        case 0: String(localized: "Red")
        case 1: String(localized: "Yellow")
        case 2: String(localized: "Green")
        case 3: String(localized: "Blue")
        case 4: String(localized: "White")
        default: String(localized: "Black")
        }
    }

    static func widthName(_ i: Int) -> String {
        switch i {
        case 0: String(localized: "Thin")
        case 1: String(localized: "Medium [Image Editor 2c8d51]")
        default: String(localized: "Thick")
        }
    }

    static func shapeName(_ s: CropShape) -> String {
        switch s {
        case .free: String(localized: "Freeform")
        case .original: String(localized: "Original [Image Editor 9a4f22]")
        case .square: String(localized: "Square")
        case .wide4x3: "4:3"
        case .tall3x4: "3:4"
        case .wide16x9: "16:9"
        case .tall9x16: "9:16"
        }
    }
}

/// From the + panel: the message's caption under the editor's tools, and Send. The caption typed goes back only with
/// Done or Send; Cancel leaves it as it was.
struct ImageEditorSend {
    var caption: Binding<String>
    var send: (EditedImage) -> Void
}

/// The crop's frame while cropping: the rest of the picture dimmed, a line round the crop, its corners marked, and the
/// thirds inside it while it is dragged.
private struct CropFrame: View {
    var frame: CGRect
    var bounds: CGRect
    var dragging: Bool

    var body: some View {
        Canvas { ctx, size in
            var outside = Path(CGRect(origin: .zero, size: size))
            outside.addRect(frame)
            ctx.fill(outside, with: .color(.black.opacity(Theme.ImageEdit.cropDim)), style: FillStyle(eoFill: true))
            ctx.stroke(Path(frame), with: .color(.white), lineWidth: Theme.ImageEdit.cropLine)
            if dragging {
                var thirds = Path()
                for i in 1...2 {
                    let x = frame.minX + frame.width * CGFloat(i) / 3, y = frame.minY + frame.height * CGFloat(i) / 3
                    thirds.move(to: CGPoint(x: x, y: frame.minY))
                    thirds.addLine(to: CGPoint(x: x, y: frame.maxY))
                    thirds.move(to: CGPoint(x: frame.minX, y: y))
                    thirds.addLine(to: CGPoint(x: frame.maxX, y: y))
                }
                ctx.stroke(thirds, with: .color(.white.opacity(Theme.ImageEdit.cropThirdsOpacity)), lineWidth: Theme.ImageEdit.cropLine)
            }
            let c = min(Theme.ImageEdit.cropCorner, frame.width / 2, frame.height / 2)
            var corners = Path()
            for (x, y, dx, dy) in [(frame.minX, frame.minY, c, c), (frame.maxX, frame.minY, -c, c),
                                   (frame.minX, frame.maxY, c, -c), (frame.maxX, frame.maxY, -c, -c)] {
                corners.move(to: CGPoint(x: x + dx, y: y))
                corners.addLine(to: CGPoint(x: x, y: y))
                corners.addLine(to: CGPoint(x: x, y: y + dy))
            }
            ctx.stroke(corners, with: .color(.white), style: StrokeStyle(lineWidth: Theme.ImageEdit.cropCornerWidth, lineCap: .square))
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}
