import CoreGraphics
import Foundation
import Testing
import UIKit
@testable import Persona

/// The image editor's edits (`ImageEdits`): turning, cropping and marks map points and pixels where they belong, a crop
/// handle keeps the crop inside the picture and in its shape, Undo and Redo step through whole edits, and the composer
/// keeps an edited photo's edits beside it until the photo goes.
struct ImageEditsTests {
    private let size = CGSize(width: 40, height: 30)

    // MARK: Turning

    @Test func aPointComesBackFromEveryTurn() {
        let points = [CGPoint(x: 0, y: 0), CGPoint(x: 40, y: 0), CGPoint(x: 13, y: 7), CGPoint(x: 40, y: 30)]
        for turns in 0..<4 {
            let e = ImageEdits(turns: turns)
            for p in points {
                let back = e.unturn(e.turn(p, in: size), in: size)
                #expect(abs(back.x - p.x) < 1e-9 && abs(back.y - p.y) < 1e-9, "turns \(turns): \(p) came back as \(back)")
                #expect(e.turn(p, in: size) == p.applying(e.turnTransform(size)), "turns \(turns): the transform agrees")
            }
        }
    }

    @Test func aQuarterClockwisePutsTheTopLeftAtTheTopRight() {
        let e = ImageEdits(turns: 1)
        #expect(e.turned(size) == CGSize(width: 30, height: 40))
        #expect(e.turn(CGPoint(x: 0, y: 0), in: size) == CGPoint(x: 30, y: 0))
        #expect(e.turn(CGPoint(x: 40, y: 0), in: size) == CGPoint(x: 30, y: 40))
    }

    @Test func turningLeftTakesTheCropAndItsShapeAlong() {
        var e = ImageEdits()
        e.crop = CGRect(x: 0.1, y: 0.2, width: 0.5, height: 0.3)
        e.shape = .wide4x3
        e.turnLeft()
        #expect(e.turns == 3)
        #expect(e.shape == .tall3x4)
        #expect(close(e.crop, CGRect(x: 0.2, y: 0.4, width: 0.3, height: 0.5)))
        for _ in 0..<3 { e.turnLeft() }
        #expect(e.turns == 0 && e.shape == .wide4x3)
        #expect(close(e.crop, CGRect(x: 0.1, y: 0.2, width: 0.5, height: 0.3)), "four turns left: back where it was")
    }

    @Test func resetKeepsTheMarks() {
        var e = ImageEdits(turns: 2, crop: CGRect(x: 0, y: 0, width: 0.5, height: 0.5), shape: .square)
        e.marks = [ImageMark(kind: .pen, ink: 0, width: 2, points: [.zero, CGPoint(x: 1, y: 1)])]
        e.resetFrame()
        #expect(e.isFramedAsIs)
        #expect(e.marks.count == 1)
        #expect(!e.isEmpty)
    }

    // MARK: Shapes and handles

    @Test func aSquareOnAWidePictureIsAsTallAsItAndInTheMiddle() {
        var e = ImageEdits()
        e.reshape(.square, size: size) // 40 × 30
        #expect(close(e.crop, CGRect(x: 0.125, y: 0, width: 0.75, height: 1)))
        #expect(e.cropRect(size) == CGRect(x: 5, y: 0, width: 30, height: 30))
        e.reshape(.free, size: size)
        #expect(close(e.crop, CGRect(x: 0.125, y: 0, width: 0.75, height: 1)), "Freeform keeps the crop as it is")
    }

    @Test func aFreeCornerStaysInsideAndNoSmallerThanTheLeast() {
        let start = CGRect(x: 0.2, y: 0.2, width: 0.5, height: 0.5)
        let least = CGSize(width: 0.1, height: 0.1)
        let out = ImageEdits.drag(start, handle: .topLeading, by: CGSize(width: -0.5, height: -0.5), minimum: least, ratio: nil)
        #expect(close(out, CGRect(x: 0, y: 0, width: 0.7, height: 0.7)), "pulled past the edge: stops at it")
        let shrunk = ImageEdits.drag(start, handle: .bottomTrailing, by: CGSize(width: -0.9, height: -0.9), minimum: least, ratio: nil)
        #expect(close(shrunk, CGRect(x: 0.2, y: 0.2, width: 0.1, height: 0.1)), "pushed past the other side: stops at the least")
        let edge = ImageEdits.drag(start, handle: .trailing, by: CGSize(width: 0.1, height: 0.3), minimum: least, ratio: nil)
        #expect(close(edge, CGRect(x: 0.2, y: 0.2, width: 0.6, height: 0.5)), "an edge moves only its own side")
    }

    @Test func aFixedShapeKeepsItsShapeAndTheOppositeCornerStays() {
        let k: CGFloat = 1 // a square crop of a square picture, in fractions
        let start = CGRect(x: 0.3, y: 0.3, width: 0.4, height: 0.4)
        let least = CGSize(width: 0.1, height: 0.1)
        let out = ImageEdits.drag(start, handle: .bottomTrailing, by: CGSize(width: 0.2, height: 0.05), minimum: least, ratio: k)
        #expect(close(out, CGRect(x: 0.3, y: 0.3, width: 0.6, height: 0.6)))
        let big = ImageEdits.drag(start, handle: .topLeading, by: CGSize(width: -0.9, height: -0.1), minimum: least, ratio: k)
        #expect(close(big, CGRect(x: 0, y: 0, width: 0.7, height: 0.7)), "as large as fits from the corner that stays")
        let side = ImageEdits.drag(start, handle: .trailing, by: CGSize(width: 0.2, height: 0), minimum: least, ratio: k)
        #expect(abs(side.width - side.height) < 1e-9 && side.minX == 0.3 && abs(side.midY - 0.5) < 1e-9, "an edge grows round its middle")
    }

    @Test func insideMovesTheCropAndNotPastTheEdge() {
        let start = CGRect(x: 0.2, y: 0.2, width: 0.5, height: 0.5)
        let out = ImageEdits.drag(start, handle: .inside, by: CGSize(width: 0.6, height: -0.1), minimum: .zero, ratio: nil)
        #expect(close(out, CGRect(x: 0.5, y: 0.1, width: 0.5, height: 0.5)))
    }

    @MainActor @Test func aFingerTakesTheCornerTheEdgeOrTheInside() {
        let frame = CGRect(x: 100, y: 100, width: 200, height: 100)
        #expect(ImageEditor.handle(at: CGPoint(x: 95, y: 104), in: frame) == .topLeading)
        #expect(ImageEditor.handle(at: CGPoint(x: 305, y: 196), in: frame) == .bottomTrailing)
        #expect(ImageEditor.handle(at: CGPoint(x: 200, y: 92), in: frame) == .top)
        #expect(ImageEditor.handle(at: CGPoint(x: 299, y: 150), in: frame) == .trailing)
        #expect(ImageEditor.handle(at: CGPoint(x: 200, y: 150), in: frame) == .inside)
        #expect(ImageEditor.handle(at: CGPoint(x: 20, y: 20), in: frame) == nil)
    }

    // MARK: Drawing

    @Test func aTurnDrawsThePixelsWhereTheyGo() throws {
        let image = try Self.picture(width: 2, height: 1, pixels: [Self.red, Self.blue])
        let clockwise = try #require(ImageEdits(turns: 1).render(image))
        #expect(clockwise.width == 1 && clockwise.height == 2)
        #expect(Self.color(clockwise, x: 0, y: 0) == Self.red, "turned clockwise, the left side is on top")
        #expect(Self.color(clockwise, x: 0, y: 1) == Self.blue)
        let left = try #require(ImageEdits(turns: 3).render(image))
        #expect(Self.color(left, x: 0, y: 0) == Self.blue, "turned left, the right side is on top")
    }

    @Test func aCropKeepsItsOwnPixels() throws {
        let image = try Self.picture(width: 4, height: 1, pixels: [Self.red, Self.blue, Self.green, Self.red])
        let out = try #require(ImageEdits(crop: CGRect(x: 0.25, y: 0, width: 0.5, height: 1)).render(image))
        #expect(out.width == 2 && out.height == 1)
        #expect(Self.color(out, x: 0, y: 0) == Self.blue)
        #expect(Self.color(out, x: 1, y: 0) == Self.green)
    }

    @Test func aMarkIsBurnedInWhereItWasDrawnAfterATurn() throws {
        let image = try Self.picture(width: 10, height: 10, pixels: Array(repeating: Self.white, count: 100))
        let box = ImageMark(kind: .box, ink: 0, width: 2, points: [CGPoint(x: 2, y: 2), CGPoint(x: 7, y: 7)])
        let plain = try #require(ImageEdits(marks: [box]).render(image))
        #expect(Self.isInk(Self.color(plain, x: 2, y: 4)), "the box's left side")
        #expect(Self.color(plain, x: 5, y: 5) == Self.white, "inside the box")
        // Turned clockwise, the source's (2.5, 4.5) is at (10 - 4.5, 2.5).
        let turned = try #require(ImageEdits(turns: 1, marks: [box]).render(image))
        #expect(Self.isInk(Self.color(turned, x: 5, y: 2)))
        #expect(Self.color(turned, x: 5, y: 5) == Self.white)
    }

    @Test func aClearPictureGoesWhiteNotBlack() throws {
        let image = try Self.picture(width: 2, height: 2, pixels: Array(repeating: [0, 0, 0, 0], count: 4))
        let out = try #require(ImageEdits(turns: 2).render(image))
        #expect(Self.color(out, x: 0, y: 0) == Self.white)
    }

    // MARK: Undo and Redo

    @Test func undoAndRedoStepThroughWholeEdits() {
        var history = EditHistory()
        let a = ImageEdits(), b = ImageEdits(turns: 3), c = ImageEdits(turns: 2)
        history.record(a) // a → b
        history.record(b) // b → c
        var now = c
        now = history.undo(now) ?? now
        #expect(now == b && history.canRedo)
        now = history.undo(now) ?? now
        #expect(now == a && !history.canUndo)
        now = history.redo(now) ?? now
        #expect(now == b)
        history.record(now) // a new change: nothing left to redo
        #expect(!history.canRedo && history.past.count == 2)
    }

    // MARK: The composer

    @MainActor @Test func anEditedPhotoKeepsItsEditsUntilItGoes() throws {
        let held = ComposerAttachments()
        let source = UIGraphicsImageRenderer(size: CGSize(width: 8, height: 6)).image { ctx in
            UIColor.red.setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: 8, height: 6))
        }
        let first = try #require(MessagePhotos.payload(source))
        held.images = [first]
        var edit = EditedImage(source: source)
        edit.edits = ImageEdits(turns: 1)
        edit.result = UIGraphicsImageRenderer(size: CGSize(width: 6, height: 8)).image { _ in }
        held.replace(first.0, with: edit)
        let now = try #require(held.images.first?.0)
        #expect(now !== first.0, "the edit is in the photo's place")
        #expect(held.edits(of: now)?.edits.turns == 1, "and opens again as it was left")
        #expect(held.edits(of: now)?.source === source, "from the same source")
        held.images = []
        #expect(held.edits(of: now) == nil, "the photo gone, its edits go too")
    }

    // MARK: Helpers

    private static let red: [UInt8] = [255, 0, 0, 255]
    private static let blue: [UInt8] = [0, 0, 255, 255]
    private static let green: [UInt8] = [0, 255, 0, 255]
    private static let white: [UInt8] = [255, 255, 255, 255]

    /// The first ink (red) or near it at an anti-aliased edge.
    private static func isInk(_ c: [UInt8]) -> Bool { c[0] > 200 && c[1] < 120 && c[2] < 120 }

    private func close(_ a: CGRect, _ b: CGRect) -> Bool {
        abs(a.minX - b.minX) < 1e-9 && abs(a.minY - b.minY) < 1e-9 && abs(a.width - b.width) < 1e-9 && abs(a.height - b.height) < 1e-9
    }

    /// An sRGB picture of `pixels` (RGBA, rows from the top).
    private static func picture(width: Int, height: Int, pixels: [[UInt8]]) throws -> CGImage {
        let data = Data(pixels.flatMap { $0 }) as CFData
        let provider = try #require(CGDataProvider(data: data))
        return try #require(CGImage(width: width, height: height, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: width * 4,
                                    space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                    bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedLast.rawValue),
                                    provider: provider, decode: nil, shouldInterpolate: false, intent: .defaultIntent))
    }

    /// One pixel of `image` (RGBA, from the top left), each channel rounded to the nearest of 0 and 255 when within 24 of
    /// it (an edge drawn through a turn may blend a little).
    private static func color(_ image: CGImage, x: Int, y: Int) -> [UInt8] {
        var bytes = [UInt8](repeating: 0, count: image.width * image.height * 4)
        bytes.withUnsafeMutableBytes { buffer in
            let ctx = CGContext(data: buffer.baseAddress, width: image.width, height: image.height, bitsPerComponent: 8,
                                bytesPerRow: image.width * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            ctx.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
        }
        let i = (y * image.width + x) * 4
        return bytes[i..<i + 4].map { $0 < 24 ? 0 : $0 > 231 ? 255 : $0 }
    }
}
