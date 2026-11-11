import CoreGraphics
import Foundation
import Testing
@testable import Persona

/// The Mac's pointer over its picture (#3075, VNCPointerArt): drawn at a size that reads, with rings only where the
/// pointer lacks a contrasting edge, so it reads on black, white, gray, a dark terminal and a busy picture.
struct VNCPointerArtTests {
    /// A cursor from rows, as RFBClient hands it over (BGRA, alpha from the mask): `#` black, `o` white, `+` gray,
    /// `r` red, `.` clear.
    static func cursor(_ rows: [String], hot: (Int, Int)) -> RFBClient.Cursor {
        let w = rows[0].count, h = rows.count
        var px = [UInt8](repeating: 0, count: w * h * 4)
        for (y, row) in rows.enumerated() {
            for (x, ch) in row.enumerated() {
                let i = (y * w + x) * 4
                let bgr: [UInt8]? = switch ch {
                case "#": [0, 0, 0]
                case "o": [255, 255, 255]
                case "+": [128, 128, 128]
                case "r": [30, 30, 220]
                default: nil
                }
                guard let bgr else { continue }
                px[i] = bgr[0]; px[i + 1] = bgr[1]; px[i + 2] = bgr[2]; px[i + 3] = 255
            }
        }
        return RFBClient.Cursor(width: w, height: h, hotX: hot.0, hotY: hot.1, pixels: px)
    }

    /// macOS's arrow at 1x as Screen Sharing may send it: its soft shadow cut off by the mask…
    static let arrow = cursor([
        "oo........", "o#o.......", "o##o......", "o###o.....", "o####o....", "o#####o...", "o######o..", "o#######o.",
        "o########o", "o########o", "o#####oooo", "o##o##+o..", "o+ooo##o..", ".o+.o##+o.", "....o+##o.", ".....o++o.",
        "......oo..",
    ], hot: (1, 1))
    /// …or kept, hardened into a black rim.
    static let arrowRimmed = cursor([
        "..##...........", "##oo##.........", "##ooo##........", "##o#oo##.......", "##o##oo##......", "##o###oo##.....",
        "##o####oo##....", "##o#####oo##...", "##o######oo##..", "##o#######oo##.", "##o########oo#.", "##o########oo##",
        "##o#####oooo###", "##o##o##+o#####", "##o+ooo##o+###.", "##+o+#o##+o##..", "######o+##o##..", ".#####+o++o##..",
        "..#####+oo###..", ".....########..", ".....#######...", ".......####....",
    ], hot: (3, 3))
    /// macOS's I-beam (over text: a terminal).
    static let iBeam = cursor([
        "ooooooo", "o##o##o", ".o+#+o.", "..o#o..", "..o#o..", "..o#o..", "..o#o..", "..o#o..", "..o#o..", "..o#o..",
        "..o#o..", "..o#o..", "..o#o..", "..o#o..", "..o#o..", ".o+#+o.", "o##o##o", "ooooooo",
    ], hot: (3, 9))
    static func cross(_ ink: Character) -> RFBClient.Cursor {
        cursor((0..<15).map { y in String((0..<15).map { x in x == 7 || y == 7 ? ink : "." }) }, hot: (7, 7))
    }

    /// A 1920-pixel-wide Mac on a 402-point-wide iPhone at 3×: 4.8 of its pixels a point.
    static let natural: CGFloat = 402.0 / 1920
    static let scale: CGFloat = 3

    static func render(_ c: RFBClient.Cursor, natural: CGFloat = natural) throws -> VNCPointerArt.Art {
        try #require(VNCPointerArt.render(c, natural: natural, minHeight: Theme.Size.pointer, scale: scale))
    }

    // MARK: Backgrounds, and what reads on them

    /// A background as rows of luminance (0…1), as big as the art, in device pixels.
    typealias Background = @Sendable (Int, Int) -> CGFloat
    static let black: Background = { _, _ in 0 }
    static let white: Background = { _, _ in 1 }
    static let gray: Background = { _, _ in 0.5 }
    /// A dark terminal: near black, with lines of light-gray text every few rows.
    static let terminal: Background = { x, y in y % 18 < 9 && x % 7 < 5 ? 0.8 : 0.12 }
    /// A busy picture (a photo, a video): every pixel anything.
    static let busy: Background = { x, y in
        var h = UInt32(truncatingIfNeeded: x &* 73_856_093 ^ y &* 19_349_663)
        h ^= h >> 13; h = h &* 0x5bd1e995; h ^= h >> 15
        return CGFloat(h % 1000) / 999
    }

    /// The art over a background, as luminance, and where it covers (half or more).
    static func composite(_ art: VNCPointerArt.Art, over bg: Background) -> (lum: [CGFloat], cover: [Bool], back: [CGFloat], w: Int, h: Int) {
        let w = art.image.width, h = art.image.height
        var px = [UInt8](repeating: 0, count: w * h * 4)
        px.withUnsafeMutableBytes { buf in
            let ctx = CGContext(data: buf.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            ctx.draw(art.image, in: CGRect(x: 0, y: 0, width: w, height: h))
        }
        var lum = [CGFloat](repeating: 0, count: w * h), cover = [Bool](repeating: false, count: w * h), back = lum
        for y in 0..<h {
            for x in 0..<w {
                let i = y * w + x
                let a = CGFloat(px[i * 4 + 3]) / 255
                let own = (0.2126 * CGFloat(px[i * 4]) + 0.7152 * CGFloat(px[i * 4 + 1]) + 0.0722 * CGFloat(px[i * 4 + 2])) / 255
                back[i] = bg(x, y)
                lum[i] = own + (1 - a) * back[i]
                cover[i] = a >= 0.5
            }
        }
        return (lum, cover, back, w, h)
    }

    /// Reads: every pixel of the pointer's outline has, within 2 points, a pixel of the pointer that differs from the
    /// picture under it by at least 0.4 in luminance: its whole shape stands out, not a few pixels of it.
    static func reads(_ art: VNCPointerArt.Art, over bg: Background) -> Bool {
        let c = composite(art, over: bg)
        let reach = Int(2 * scale)
        var outline = 0, seen = 0
        for y in 0..<c.h {
            for x in 0..<c.w where c.cover[y * c.w + x] {
                let edge = x == 0 || y == 0 || x == c.w - 1 || y == c.h - 1 || !c.cover[y * c.w + x - 1] || !c.cover[y * c.w + x + 1]
                    || !c.cover[(y - 1) * c.w + x] || !c.cover[(y + 1) * c.w + x]
                guard edge else { continue }
                outline += 1
                search: for dy in -reach...reach {
                    for dx in -reach...reach where dx * dx + dy * dy <= reach * reach {
                        let qx = x + dx, qy = y + dy
                        guard qx >= 0, qy >= 0, qx < c.w, qy < c.h, c.cover[qy * c.w + qx] else { continue }
                        if abs(c.lum[qy * c.w + qx] - c.back[qy * c.w + qx]) >= 0.4 { seen += 1; break search }
                    }
                }
            }
        }
        return outline > 0 && Double(seen) >= Double(outline) * 0.95
    }

    // MARK: Tests

    @Test(arguments: ["arrow", "arrow with a rim", "I-beam", "black cross", "white cross", "red cross"])
    func readsOnEveryBackground(_ name: String) throws {
        let c = switch name {
        case "arrow": Self.arrow
        case "arrow with a rim": Self.arrowRimmed
        case "I-beam": Self.iBeam
        case "black cross": Self.cross("#")
        case "white cross": Self.cross("o")
        default: Self.cross("r")
        }
        let art = try Self.render(c)
        for (bg, back) in [("black", Self.black), ("white", Self.white), ("gray", Self.gray), ("terminal", Self.terminal), ("busy", Self.busy)] {
            #expect(Self.reads(art, over: back), "\(name) over \(bg)")
        }
    }

    /// At the picture's own scale macOS's arrow would be about 4 points tall (the bug); it is drawn at least as tall as
    /// the token says, a whole number of device pixels to a pixel of it.
    @Test func atLeastPointerTall() throws {
        let v = try #require(VNCPointerArt.visible(Self.arrow))
        #expect(v.height * Self.natural < 4)
        let k = VNCPointerArt.devicePixelsPerPixel(Self.arrow, natural: Self.natural, minHeight: Theme.Size.pointer, scale: Self.scale)
        #expect(k == k.rounded() && v.height * k / Self.scale >= Theme.Size.pointer - 1)
        let art = try Self.render(Self.arrow)
        #expect(art.size.height >= Theme.Size.pointer)
        #expect(art.size.height < Theme.Size.pointer * 1.5)
    }

    /// Zoomed in far enough, the Mac's own size is larger, and it is drawn at that.
    @Test func zoomedInItIsTheMacsSize() throws {
        let art = try Self.render(Self.arrow, natural: 3)
        let v = try #require(VNCPointerArt.visible(Self.arrow))
        let pad = (2 * (VNCPointerArt.ring * Self.scale).rounded() + 1) / Self.scale // room for the rings, each side
        #expect(abs(art.size.height - (v.height * 3 + 2 * pad)) < 0.01)
    }

    /// macOS's own pointers already read (black with a white outline): they are only enlarged. A pointer without a light
    /// edge gets the light ring, one without a dark edge the dark ring.
    @Test func ringsOnlyWhereNeeded() throws {
        for c in [Self.arrow, Self.iBeam] {
            let art = try Self.render(c)
            #expect(!art.light && !art.dark)
        }
        let black = try Self.render(Self.cross("#")), white = try Self.render(Self.cross("o")), red = try Self.render(Self.cross("r"))
        #expect(black.light && !black.dark)
        #expect(!white.light && white.dark)
        #expect(red.light && !red.dark)
    }

    /// The hot spot is where the Mac's pointer is: the art's hot spot lands on the scaled hot pixel.
    @Test func theHotSpotStaysOnItsPixel() throws {
        var c = Self.cross("#")
        let i = (c.hotY * c.width + c.hotX) * 4
        c.pixels[i] = 0; c.pixels[i + 1] = 255; c.pixels[i + 2] = 0 // the hot pixel green
        let art = try Self.render(c)
        let w = art.image.width, h = art.image.height
        var px = [UInt8](repeating: 0, count: w * h * 4)
        px.withUnsafeMutableBytes { buf in
            let ctx = CGContext(data: buf.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            ctx.draw(art.image, in: CGRect(x: 0, y: 0, width: w, height: h))
        }
        // Just inside the hot spot (it is the hot pixel's top-left corner, scaled).
        let x = Int(art.hot.x * Self.scale) + 1, y = Int(art.hot.y * Self.scale) + 1
        let j = (y * w + x) * 4
        #expect(px[j + 1] > 200 && px[j] < 60 && px[j + 2] < 60, "\(px[j..<(j + 4)])")
    }

    /// The app's own arrow, the one that shows over a Mac (macOS's Screen Sharing sends no pointer): black with a white
    /// ring, as the Mac's own, and readable everywhere. It came out solid black with nothing around it (the bug).
    @MainActor @Test func theAppsOwnArrowIsTheMacsLook() throws {
        let c = VNCScreenUIView.arrow(Self.scale)
        var sum: CGFloat = 0, n: CGFloat = 0
        for i in stride(from: 0, to: c.pixels.count, by: 4) where c.pixels[i + 3] == 255 {
            sum += (CGFloat(c.pixels[i]) + CGFloat(c.pixels[i + 1]) + CGFloat(c.pixels[i + 2])) / 765
            n += 1
        }
        #expect(n > 50 && sum / n < 0.1, "black: \(sum / max(n, 1)) over \(n) pixels")
        let art = try #require(VNCPointerArt.render(c, natural: 1 / Self.scale, minHeight: 0, scale: Self.scale))
        #expect(art.light && !art.dark)
        #expect(abs(art.size.height - Theme.Size.icon) < 3, "\(art.size)")
        for (bg, back) in [("black", Self.black), ("white", Self.white), ("gray", Self.gray), ("terminal", Self.terminal), ("busy", Self.busy)] {
            #expect(Self.reads(art, over: back), "the app's arrow over \(bg)")
        }
    }

    @Test func nothingToDrawIsNil() {
        let clear = RFBClient.Cursor(width: 4, height: 4, hotX: 0, hotY: 0, pixels: [UInt8](repeating: 0, count: 64))
        #expect(VNCPointerArt.render(clear, natural: Self.natural, minHeight: Theme.Size.pointer, scale: Self.scale) == nil)
        let short = RFBClient.Cursor(width: 4, height: 4, hotX: 0, hotY: 0, pixels: [1, 2, 3])
        #expect(VNCPointerArt.render(short, natural: Self.natural, minHeight: Theme.Size.pointer, scale: Self.scale) == nil)
    }
}
