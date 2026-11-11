import CoreGraphics
import Foundation

/// The Mac's pointer as the app draws it over the Mac's picture (#3075).
///
/// Drawn at the framebuffer's scale it was a speck: a 1920-pixel-wide Mac on a 402-point-wide phone puts 4.8 of its
/// pixels in a point, so macOS's 28 × 40 arrow came out 4 points tall, and its one-pixel white outline averaged away into
/// the black body. It disappeared on a black window or a dark terminal. So the pointer is drawn:
/// - at least `minHeight` points tall (its visible part; larger when zoomed in far enough that the Mac's own size is),
///   scaled about its hot spot, which stays where the Mac's pointer is, and by whole device pixels while it is
///   enlarged, so a Mac's one-pixel outline stays a crisp line;
/// - with the rings it needs to read on any background: one that already does (macOS's own arrow and I-beam: black with
///   a white outline) is left as it is; one with no light edge (a black pointer) gets a light ring, and one with no dark
///   edge (a white one) a dark ring around it. Any background is far from white or from black, so a pointer that reads
///   on both reads on all.
nonisolated enum VNCPointerArt {
    struct Art {
        let image: CGImage
        /// Its size and its hot spot (from its top-left corner), in points.
        let size: CGSize
        let hot: CGPoint
        /// The rings it was given (light inside dark when both).
        let light: Bool, dark: Bool
    }

    /// Each ring's width, in points.
    static let ring: CGFloat = 1
    /// How far in from its edge a pointer may have its contrasting band and still read (a light line inside a thin dark
    /// rim reads on black), in points.
    static let reach: CGFloat = 2
    /// The least luminance difference that reads at a glance.
    static let contrast: CGFloat = 0.5

    /// The pointer's visible part: the pixels its mask keeps, in its own pixels (nil: none).
    static func visible(_ c: RFBClient.Cursor) -> CGRect? {
        guard c.pixels.count >= c.width * c.height * 4 else { return nil }
        var minX = c.width, minY = c.height, maxX = -1, maxY = -1
        for y in 0..<c.height {
            for x in 0..<c.width where c.pixels[(y * c.width + x) * 4 + 3] > 0 {
                minX = min(minX, x); maxX = max(maxX, x)
                minY = min(minY, y); maxY = max(maxY, y)
            }
        }
        guard maxX >= 0 else { return nil }
        return CGRect(x: minX, y: minY, width: maxX - minX + 1, height: maxY - minY + 1)
    }

    /// Device pixels a pixel of the pointer is drawn at: the picture's own (`natural` points a pixel), or more so its
    /// visible part is at least `minHeight` points tall, then whole device pixels when enlarged past 1.5.
    static func devicePixelsPerPixel(_ c: RFBClient.Cursor, natural: CGFloat, minHeight: CGFloat, scale: CGFloat) -> CGFloat {
        guard let v = visible(c) else { return natural * scale }
        let k = max(natural, minHeight / v.height) * scale
        return k >= 1.5 ? k.rounded() : k
    }

    /// The pointer as drawn, `scale` device pixels to a point.
    static func render(_ c: RFBClient.Cursor, natural: CGFloat, minHeight: CGFloat, scale: CGFloat) -> Art? {
        guard c.width > 0, c.height > 0, let v = visible(c), scale > 0, let source = image(c, crop: v) else { return nil }
        let k = devicePixelsPerPixel(c, natural: natural, minHeight: minHeight, scale: scale)
        let band = max(1, (ring * scale).rounded())
        let pad = Int(2 * band) + 1
        let w = Int((v.width * k).rounded(.up)) + 2 * pad, h = Int((v.height * k).rounded(.up)) + 2 * pad
        guard w > 0, h > 0, w * h <= 1 << 22 else { return nil }

        // The pointer alone, scaled, in a premultiplied RGBA buffer, its top row first.
        var top = [UInt8](repeating: 0, count: w * h * 4)
        let drew = top.withUnsafeMutableBytes { buf -> Bool in
            guard let ctx = CGContext(data: buf.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                      space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: rgba.rawValue) else { return false }
            ctx.interpolationQuality = k == k.rounded() ? .none : .medium
            ctx.draw(source, in: CGRect(x: CGFloat(pad), y: CGFloat(pad), width: v.width * k, height: v.height * k).integral)
            return true
        }
        guard drew else { return nil }

        let reachPx = reach * scale
        let light = !readable(top, width: w, height: h, over: 0, reach: reachPx)
        let dark = !readable(top, width: w, height: h, over: 1, reach: reachPx)
        var out = top
        if light || dark {
            let shape = (0..<(w * h)).map { top[$0 * 4 + 3] >= 128 }
            let d = distances(from: shape, width: w, height: h)
            let lightEdge = light ? band : 0, darkEdge = lightEdge + (dark ? band : 0)
            for i in 0..<(w * h) {
                // Under the pointer: the dark ring (black), the light one (white) over it, each with a soft half-pixel
                // edge; premultiplied, the rings' red, green and blue are the light ring's cover.
                let la = min(max(lightEdge + 0.5 - d[i], 0), 1)
                let da = min(max(darkEdge + 0.5 - d[i], 0), 1)
                let ringA = la + da * (1 - la)
                let pa = CGFloat(top[i * 4 + 3]) / 255
                for ch in 0..<3 {
                    out[i * 4 + ch] = UInt8((min(CGFloat(top[i * 4 + ch]) / 255 + la * (1 - pa), 1) * 255).rounded())
                }
                out[i * 4 + 3] = UInt8((min(pa + ringA * (1 - pa), 1) * 255).rounded())
            }
        }
        guard let provider = CGDataProvider(data: Data(out) as CFData),
              let art = CGImage(width: w, height: h, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                                bitmapInfo: rgba, provider: provider, decode: nil, shouldInterpolate: false, intent: .defaultIntent)
        else { return nil }
        let hot = CGPoint(x: ((CGFloat(c.hotX) - v.minX) * k + CGFloat(pad)) / scale, y: ((CGFloat(c.hotY) - v.minY) * k + CGFloat(pad)) / scale)
        return Art(image: art, size: CGSize(width: CGFloat(w) / scale, height: CGFloat(h) / scale), hot: hot, light: light, dark: dark)
    }

    /// RGBA, premultiplied, red first in memory.
    private static let rgba = CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedLast.rawValue | CGBitmapInfo.byteOrder32Big.rawValue)

    /// Whether a pointer (premultiplied RGBA, top row first) reads over a plain background of this luminance (0 black,
    /// 1 white): every pixel on its outline has one within `reach` pixels that stands out from the background.
    static func readable(_ rgba: [UInt8], width w: Int, height h: Int, over background: CGFloat, reach: CGFloat) -> Bool {
        let n = w * h
        var shape = [Bool](repeating: false, count: n), stands = [Bool](repeating: false, count: n)
        for i in 0..<n {
            let a = CGFloat(rgba[i * 4 + 3]) / 255
            shape[i] = a >= 0.5
            let l = (0.2126 * CGFloat(rgba[i * 4]) + 0.7152 * CGFloat(rgba[i * 4 + 1]) + 0.0722 * CGFloat(rgba[i * 4 + 2])) / 255
            stands[i] = shape[i] && abs(l + (1 - a) * background - background) >= contrast
        }
        let near = distances(from: stands, width: w, height: h)
        var edge = 0, seen = 0
        for y in 0..<h {
            for x in 0..<w where shape[y * w + x] {
                let outline = x == 0 || y == 0 || x == w - 1 || y == h - 1 || !shape[y * w + x - 1] || !shape[y * w + x + 1]
                    || !shape[(y - 1) * w + x] || !shape[(y + 1) * w + x]
                guard outline else { continue }
                edge += 1
                if near[y * w + x] <= reach { seen += 1 }
            }
        }
        return edge > 0 && CGFloat(seen) >= CGFloat(edge) * 0.9
    }

    /// The pointer's pixels (BGRA, its mask in the alpha) as an image, cut to `crop`; colors where it is see-through
    /// are dropped, so premultiplied they add nothing.
    static func image(_ c: RFBClient.Cursor, crop: CGRect? = nil) -> CGImage? {
        guard c.width > 0, c.height > 0, c.pixels.count >= c.width * c.height * 4 else { return nil }
        var px = c.pixels
        for i in stride(from: 0, to: c.width * c.height * 4, by: 4) where px[i + 3] == 0 {
            px[i] = 0; px[i + 1] = 0; px[i + 2] = 0
        }
        guard let provider = CGDataProvider(data: Data(px) as CFData),
              let whole = CGImage(width: c.width, height: c.height, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: c.width * 4,
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue),
                                  provider: provider, decode: nil, shouldInterpolate: false, intent: .defaultIntent)
        else { return nil }
        guard let crop else { return whole }
        return whole.cropping(to: crop)
    }

    /// Each pixel's distance from the nearest one marked (0 on it), in pixels: a two-pass chamfer transform (steps of 1
    /// and √2), close enough for rings a few pixels wide.
    static func distances(from marked: [Bool], width w: Int, height h: Int) -> [CGFloat] {
        let far = CGFloat(w + h)
        var d = marked.map { $0 ? 0 : far }
        let diagonal: CGFloat = 2.0.squareRoot()
        for y in 0..<h {
            for x in 0..<w {
                let i = y * w + x
                var v = d[i]
                if x > 0 { v = min(v, d[i - 1] + 1) }
                if y > 0 {
                    v = min(v, d[i - w] + 1)
                    if x > 0 { v = min(v, d[i - w - 1] + diagonal) }
                    if x < w - 1 { v = min(v, d[i - w + 1] + diagonal) }
                }
                d[i] = v
            }
        }
        for y in stride(from: h - 1, through: 0, by: -1) {
            for x in stride(from: w - 1, through: 0, by: -1) {
                let i = y * w + x
                var v = d[i]
                if x < w - 1 { v = min(v, d[i + 1] + 1) }
                if y < h - 1 {
                    v = min(v, d[i + w] + 1)
                    if x < w - 1 { v = min(v, d[i + w + 1] + diagonal) }
                    if x > 0 { v = min(v, d[i + w - 1] + diagonal) }
                }
                d[i] = v
            }
        }
        return d
    }
}
