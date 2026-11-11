import CoreGraphics
import Foundation
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

// What the image editor (`ImageEditor`) did to a picture: turned it, cropped it, drew on it. Kept apart from the picture,
// so a photo edited in the composer opens again with its crop and marks as they were, each still undoable, and the
// source is never written. Pure values: the editor draws them on screen, `render` burns them into a new picture.

/// A box or a free line drawn on the picture.
nonisolated struct ImageMark: Equatable, Sendable, Identifiable {
    enum Kind: Sendable { case box, pen }
    var id = UUID()
    var kind: Kind
    /// Which of `Theme.ImageEdit.inks`.
    var ink: Int
    /// The line's width, in the source picture's pixels.
    var width: CGFloat
    /// In the source picture's pixels, before it was turned or cropped: where the finger went; a box is its first and
    /// last point.
    var points: [CGPoint]

    static func color(_ ink: Int) -> CGColor {
        let inks = Theme.ImageEdit.inks
        let c = inks[min(max(ink, 0), inks.count - 1)]
        return CGColor(srgbRed: c.red, green: c.green, blue: c.blue, alpha: 1)
    }

    /// The mark's outline with each point taken through `map` (onto the screen, or into the result).
    func path(_ map: (CGPoint) -> CGPoint) -> CGPath {
        let path = CGMutablePath()
        let pts = points.map(map)
        guard let first = pts.first, let last = pts.last else { return path }
        switch kind {
        case .pen: path.addLines(between: pts)
        case .box: path.addRect(CGRect(x: min(first.x, last.x), y: min(first.y, last.y), width: abs(last.x - first.x), height: abs(last.y - first.y)))
        }
        return path
    }
}

/// A crop's shape: free, the picture's own, or width : height.
nonisolated enum CropShape: String, CaseIterable, Identifiable, Sendable {
    case free, original, square, wide4x3, tall3x4, wide16x9, tall9x16
    var id: String { rawValue }

    /// Width over height in pixels, for a picture of `size` (as turned); nil: any.
    func ratio(_ size: CGSize) -> CGFloat? {
        switch self {
        case .free: nil
        case .original: size.height > 0 ? size.width / size.height : nil
        case .square: 1
        case .wide4x3: 4.0 / 3
        case .tall3x4: 3.0 / 4
        case .wide16x9: 16.0 / 9
        case .tall9x16: 9.0 / 16
        }
    }

    /// The same shape once the picture has turned a quarter: 4:3 becomes 3:4.
    var turned: CropShape {
        switch self {
        case .wide4x3: .tall3x4
        case .tall3x4: .wide4x3
        case .wide16x9: .tall9x16
        case .tall9x16: .wide16x9
        default: self
        }
    }
}

/// Where a crop is taken by the finger: a corner, an edge, or inside it (moves it).
nonisolated enum CropHandle: Sendable, Equatable {
    case topLeading, top, topTrailing, leading, trailing, bottomLeading, bottom, bottomTrailing, inside

    var left: Bool { [.topLeading, .leading, .bottomLeading].contains(self) }
    var right: Bool { [.topTrailing, .trailing, .bottomTrailing].contains(self) }
    var upper: Bool { [.topLeading, .top, .topTrailing].contains(self) }
    var lower: Bool { [.bottomLeading, .bottom, .bottomTrailing].contains(self) }
    var corner: Bool { (left || right) && (upper || lower) }
}

nonisolated struct ImageEdits: Equatable, Sendable {
    /// Quarter turns clockwise, 0 to 3.
    var turns = 0
    /// What is kept, in fractions of the picture as turned.
    var crop = ImageEdits.whole
    /// The crop's shape, kept with it so a turn or an undo brings it back as it was.
    var shape: CropShape = .free
    var marks: [ImageMark] = []

    static let whole = CGRect(x: 0, y: 0, width: 1, height: 1)

    /// Nothing done: the picture goes as it is.
    var isEmpty: Bool { turns == 0 && crop == Self.whole && marks.isEmpty }
    /// Neither turned nor cropped (Reset has nothing to do).
    var isFramedAsIs: Bool { turns == 0 && crop == Self.whole && shape == .free }

    // MARK: Geometry, in pixels

    /// The picture's size (`size`, its pixels) once turned.
    func turned(_ size: CGSize) -> CGSize { turns % 2 == 0 ? size : CGSize(width: size.height, height: size.width) }

    /// A point of the picture where it is once turned.
    func turn(_ p: CGPoint, in size: CGSize) -> CGPoint {
        switch turns {
        case 1: CGPoint(x: size.height - p.y, y: p.x)
        case 2: CGPoint(x: size.width - p.x, y: size.height - p.y)
        case 3: CGPoint(x: p.y, y: size.width - p.x)
        default: p
        }
    }

    /// A point of the turned picture back where it is in the picture.
    func unturn(_ p: CGPoint, in size: CGSize) -> CGPoint {
        switch turns {
        case 1: CGPoint(x: p.y, y: size.height - p.x)
        case 2: CGPoint(x: size.width - p.x, y: size.height - p.y)
        case 3: CGPoint(x: size.width - p.y, y: p.x)
        default: p
        }
    }

    /// What `turn` does, as a transform (top-left coordinates).
    func turnTransform(_ size: CGSize) -> CGAffineTransform {
        switch turns {
        case 1: CGAffineTransform(a: 0, b: 1, c: -1, d: 0, tx: size.height, ty: 0)
        case 2: CGAffineTransform(a: -1, b: 0, c: 0, d: -1, tx: size.width, ty: size.height)
        case 3: CGAffineTransform(a: 0, b: -1, c: 1, d: 0, tx: 0, ty: size.width)
        default: .identity
        }
    }

    /// The crop in the turned picture's pixels, whole pixels, inside it.
    func cropRect(_ size: CGSize) -> CGRect {
        let t = turned(size)
        let r = CGRect(x: crop.minX * t.width, y: crop.minY * t.height, width: crop.width * t.width, height: crop.height * t.height)
        return r.integral.intersection(CGRect(origin: .zero, size: t))
    }

    /// Turned a quarter to the left (Photos' button), the crop and its shape with the picture.
    mutating func turnLeft() {
        turns = (turns + 3) % 4
        crop = CGRect(x: crop.minY, y: 1 - crop.maxX, width: crop.height, height: crop.width)
        shape = shape.turned
    }

    /// Back to the picture as it came: not turned, not cropped (the marks stay).
    mutating func resetFrame() {
        turns = 0
        crop = Self.whole
        shape = .free
    }

    /// `shape` from now on, the crop made the largest of that shape there is room for round its middle (`size`: the
    /// picture's pixels).
    mutating func reshape(_ shape: CropShape, size: CGSize) {
        self.shape = shape
        guard let ratio = shape.ratio(turned(size)) else { return }
        crop = Self.fit(crop, ratio: ratio, in: turned(size))
    }

    /// The largest crop of `ratio` (width / height, pixels) in a picture of `size` (as turned), round `crop`'s middle
    /// and moved inside the picture.
    static func fit(_ crop: CGRect, ratio: CGFloat, in size: CGSize) -> CGRect {
        guard size.width > 0, size.height > 0, ratio > 0 else { return crop }
        let k = ratio * size.height / size.width // the ratio in fractions
        var w: CGFloat = 1, h = 1 / k
        if h > 1 { h = 1; w = k }
        let x = min(max(crop.midX - w / 2, 0), 1 - w), y = min(max(crop.midY - h / 2, 0), 1 - h)
        return CGRect(x: x, y: y, width: w, height: h)
    }

    /// `start` dragged by `handle` by `delta` (fractions of the turned picture), kept inside it and no smaller than
    /// `minimum` (fractions); with `ratio` (width / height in fractions, not pixels) it keeps that shape, a corner
    /// pinned opposite the one dragged and an edge's middle opposite the edge dragged.
    static func drag(_ start: CGRect, handle: CropHandle, by delta: CGSize, minimum: CGSize, ratio k: CGFloat?) -> CGRect {
        let minW = min(minimum.width, 1), minH = min(minimum.height, 1)
        if handle == .inside {
            let x = min(max(start.minX + delta.width, 0), 1 - start.width)
            let y = min(max(start.minY + delta.height, 0), 1 - start.height)
            return CGRect(x: x, y: y, width: start.width, height: start.height)
        }
        guard let k, k > 0 else {
            var minX = start.minX, maxX = start.maxX, minY = start.minY, maxY = start.maxY
            if handle.left { minX = min(max(minX + delta.width, 0), maxX - minW) }
            if handle.right { maxX = max(min(maxX + delta.width, 1), minX + minW) }
            if handle.upper { minY = min(max(minY + delta.height, 0), maxY - minH) }
            if handle.lower { maxY = max(min(maxY + delta.height, 1), minY + minH) }
            return CGRect(x: minX, y: minY, width: maxX - minX, height: maxY - minY)
        }
        // A fixed shape: the size the finger asks for, then what fits from what stays put.
        let leastW = min(max(minW, minH * k), 1, k)
        if handle.corner {
            let anchorX = handle.left ? start.maxX : start.minX, anchorY = handle.upper ? start.maxY : start.minY
            let askedW = handle.left ? start.width - delta.width : start.width + delta.width
            let askedH = handle.upper ? start.height - delta.height : start.height + delta.height
            let roomW = handle.left ? anchorX : 1 - anchorX, roomH = handle.upper ? anchorY : 1 - anchorY
            let w = max(min(max(askedW, askedH * k), roomW, roomH * k), min(leastW, roomW, roomH * k))
            let h = w / k
            return CGRect(x: handle.left ? anchorX - w : anchorX, y: handle.upper ? anchorY - h : anchorY, width: w, height: h)
        }
        if handle.left || handle.right {
            let anchorX = handle.left ? start.maxX : start.minX
            let askedW = handle.left ? start.width - delta.width : start.width + delta.width
            let roomW = handle.left ? anchorX : 1 - anchorX
            let w = max(min(askedW, roomW, k), min(leastW, roomW))
            let h = w / k
            let y = min(max(start.midY - h / 2, 0), 1 - h)
            return CGRect(x: handle.left ? anchorX - w : anchorX, y: y, width: w, height: h)
        }
        let anchorY = handle.upper ? start.maxY : start.minY
        let askedH = handle.upper ? start.height - delta.height : start.height + delta.height
        let roomH = handle.upper ? anchorY : 1 - anchorY
        let h = max(min(askedH, roomH, 1 / k), min(leastW / k, roomH))
        let w = h * k
        let x = min(max(start.midX - w / 2, 0), 1 - w)
        return CGRect(x: x, y: handle.upper ? anchorY - h : anchorY, width: w, height: h)
    }

    // MARK: Drawing

    /// The picture with these edits burned in: turned, drawn on, cropped, at its own pixels; nil when it can't be drawn.
    /// Plain CoreGraphics, so it runs off the main actor and draws the same on every platform.
    func render(_ image: CGImage) -> CGImage? {
        let size = CGSize(width: image.width, height: image.height)
        let out = cropRect(size)
        guard out.width >= 1, out.height >= 1 else { return nil }
        let space = image.colorSpace.flatMap { $0.model == .rgb ? $0 : nil } ?? CGColorSpace(name: CGColorSpace.sRGB)!
        guard let ctx = CGContext(data: nil, width: Int(out.width), height: Int(out.height), bitsPerComponent: 8, bytesPerRow: 0,
                                  space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return nil }
        // A transparent picture sent as JPEG would go black where it is clear: white under it, as Android does.
        ctx.setFillColor(CGColor(srgbRed: 1, green: 1, blue: 1, alpha: 1))
        ctx.fill(CGRect(origin: .zero, size: out.size))
        // Top-left coordinates, as the marks are kept: the result's, the turned picture's, then the picture's.
        ctx.translateBy(x: 0, y: out.height)
        ctx.scaleBy(x: 1, y: -1)
        ctx.translateBy(x: -out.minX, y: -out.minY)
        ctx.concatenate(turnTransform(size))
        // CoreGraphics draws a picture from its bottom up: flipped back for the picture alone.
        ctx.saveGState()
        ctx.translateBy(x: 0, y: size.height)
        ctx.scaleBy(x: 1, y: -1)
        ctx.interpolationQuality = .high
        ctx.draw(image, in: CGRect(origin: .zero, size: size))
        ctx.restoreGState()
        ctx.setLineCap(.round)
        ctx.setLineJoin(.round)
        for m in marks where m.points.count > 1 {
            ctx.setStrokeColor(ImageMark.color(m.ink))
            ctx.setLineWidth(m.width)
            ctx.addPath(m.path { $0 })
            ctx.strokePath()
        }
        return ctx.makeImage()
    }
}

/// Undo and Redo over whole edits: each finished change (a mark drawn, a crop let go, a turn, a shape, a reset) is a step.
nonisolated struct EditHistory: Equatable, Sendable {
    private(set) var past: [ImageEdits] = []
    private(set) var future: [ImageEdits] = []
    var canUndo: Bool { !past.isEmpty }
    var canRedo: Bool { !future.isEmpty }

    /// A change was made to `before`: a step back to it, and nothing left to redo.
    mutating func record(_ before: ImageEdits) {
        past.append(before)
        future = []
    }

    /// The edits a step back from `now`, and `now` kept for Redo.
    mutating func undo(_ now: ImageEdits) -> ImageEdits? {
        guard let last = past.popLast() else { return nil }
        future.append(now)
        return last
    }

    mutating func redo(_ now: ImageEdits) -> ImageEdits? {
        guard let next = future.popLast() else { return nil }
        past.append(now)
        return next
    }
}

/// A picture as the editor left it: the source as it came, the edits and their history, and the picture drawn from them
/// (`result`, what is sent). The composer keeps it beside an edited photo, so the editor carries on where it stopped.
struct EditedImage {
    let source: UIImage
    var edits = ImageEdits()
    var history = EditHistory()
    var result: UIImage

    init(source: UIImage) {
        self.source = source
        result = source
    }

    init(source: UIImage, edits: ImageEdits, history: EditHistory, result: UIImage) {
        self.source = source
        self.edits = edits
        self.history = history
        self.result = result
    }
}

/// Getting a picture ready for the editor and drawing its result, off the main actor where the platform allows.
enum ImageEditWork {
    /// An immutable picture handed to a background task (UIImage and CGImage are safe to read from any thread).
    nonisolated private struct Sent<T>: @unchecked Sendable { let value: T }

    /// The picture's pixels as the editor works on them: upright, at most `MessagePhotos.maxSide` on the long side
    /// (what is sent is no larger).
    static func pixels(_ image: UIImage) async -> CGImage? {
        let side = MessagePhotos.maxSide, sent = Sent(value: image)
        #if canImport(UIKit)
        return await Task.detached(priority: .userInitiated) { Sent(value: upright(sent.value, maxSide: side)) }.value.value
        #else
        return upright(sent.value, maxSide: side)
        #endif
    }

    /// `edits` drawn over `pixels` into a new picture.
    static func render(_ edits: ImageEdits, over pixels: CGImage) async -> UIImage? {
        let sent = Sent(value: pixels)
        #if canImport(UIKit)
        let drawn = await Task.detached(priority: .userInitiated) { Sent(value: edits.render(sent.value)) }.value.value
        #else
        let drawn = edits.render(sent.value)
        #endif
        return drawn.map { UIImage(cgImage: $0) }
    }

    nonisolated private static func upright(_ image: UIImage, maxSide: CGFloat) -> CGImage? {
        #if canImport(UIKit)
        let pixels = CGSize(width: image.size.width * image.scale, height: image.size.height * image.scale)
        if image.imageOrientation == .up, let cg = image.cgImage, CGFloat(max(cg.width, cg.height)) <= maxSide { return cg }
        let fit = min(1, maxSide / max(pixels.width, pixels.height, 1))
        let target = CGSize(width: (pixels.width * fit).rounded(), height: (pixels.height * fit).rounded())
        let format = UIGraphicsImageRendererFormat.default()
        format.scale = 1
        return UIGraphicsImageRenderer(size: target, format: format).image { _ in image.draw(in: CGRect(origin: .zero, size: target)) }.cgImage
        #else
        guard let cg = image.cgImage else { return nil }
        let side = CGFloat(max(cg.width, cg.height))
        guard side > maxSide else { return cg }
        let fit = maxSide / side
        let w = Int((CGFloat(cg.width) * fit).rounded()), h = Int((CGFloat(cg.height) * fit).rounded())
        guard let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return cg }
        ctx.interpolationQuality = .high
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        return ctx.makeImage()
        #endif
    }
}
