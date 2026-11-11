import Compression
import Foundation

/// The Mac's screen as the native client holds it (RFBClient): BGRA, 4 bytes a pixel, rows packed, which is the
/// pixel format the client asks for (32 bits, depth 24, little-endian, red at bit 16) and what CVPixelBuffer's
/// 32BGRA takes as is. The encodings write into it: Raw, CopyRect, ZRLE.
nonisolated final class RFBFramebuffer: @unchecked Sendable {
    private(set) var width: Int
    private(set) var height: Int
    private(set) var pixels: UnsafeMutablePointer<UInt8>
    var bytesPerRow: Int { width * 4 }

    init(width: Int, height: Int) {
        self.width = width
        self.height = height
        pixels = .allocate(capacity: max(1, width * height * 4))
        pixels.initialize(repeating: 0, count: max(1, width * height * 4))
    }

    deinit { pixels.deallocate() }

    /// A new size (DesktopSize): black again.
    func resize(width: Int, height: Int) {
        pixels.deallocate()
        self.width = width
        self.height = height
        pixels = .allocate(capacity: max(1, width * height * 4))
        pixels.initialize(repeating: 0, count: max(1, width * height * 4))
    }

    private func inside(_ x: Int, _ y: Int, _ w: Int, _ h: Int) -> Bool {
        x >= 0 && y >= 0 && w >= 0 && h >= 0 && x + w <= width && y + h <= height
    }

    /// Raw: w·h pixels of 4 bytes, the client's format.
    func raw(x: Int, y: Int, w: Int, h: Int, _ data: UnsafeRawBufferPointer) {
        guard inside(x, y, w, h), data.count >= w * h * 4, let src = data.baseAddress else { return }
        for row in 0..<h {
            (pixels + ((y + row) * width + x) * 4).update(from: src.advanced(by: row * w * 4).assumingMemoryBound(to: UInt8.self), count: w * 4)
        }
    }

    /// CopyRect: from (sx, sy) to (x, y), overlapping or not.
    func copy(x: Int, y: Int, w: Int, h: Int, from sx: Int, _ sy: Int) {
        guard inside(x, y, w, h), inside(sx, sy, w, h) else { return }
        let rows = sy < y ? Array((0..<h).reversed()) : Array(0..<h)
        for row in rows {
            let to = pixels + ((y + row) * width + x) * 4, from = pixels + ((sy + row) * width + sx) * 4
            memmove(to, from, w * 4) // the rows may overlap
        }
    }

    func fill(x: Int, y: Int, w: Int, h: Int, b: UInt8, g: UInt8, r: UInt8) {
        guard inside(x, y, w, h) else { return }
        for row in 0..<h {
            var p = pixels + ((y + row) * width + x) * 4
            for _ in 0..<w {
                p[0] = b; p[1] = g; p[2] = r; p[3] = 255
                p += 4
            }
        }
    }

    func set(_ x: Int, _ y: Int, b: UInt8, g: UInt8, r: UInt8) {
        guard x >= 0, y >= 0, x < width, y < height else { return } // defense in depth: draw checks the rectangle
        let p = pixels + (y * width + x) * 4
        p[0] = b; p[1] = g; p[2] = r; p[3] = 255
    }

    /// The pixel at (x, y), BGRA (tests).
    func pixel(_ x: Int, _ y: Int) -> [UInt8] { Array(UnsafeBufferPointer(start: pixels + (y * width + x) * 4, count: 4)) }
}

/// One zlib stream that lasts the connection (ZRLE's): each rectangle's compressed part inflates to that
/// rectangle's bytes (the server flushes the stream at the end of each). Compression's ZLIB is raw DEFLATE, so the
/// stream's two-byte header is skipped first.
nonisolated final class RFBInflater {
    private var stream = compression_stream(dst_ptr: UnsafeMutablePointer<UInt8>(bitPattern: 1)!, dst_size: 0,
                                            src_ptr: UnsafePointer<UInt8>(bitPattern: 1)!, src_size: 0, state: nil)
    private var started = false
    private var headerLeft = 2
    private var out = [UInt8](repeating: 0, count: 1 << 16)

    deinit { if started { compression_stream_destroy(&stream) } }

    /// The inflated bytes of one rectangle's compressed part.
    func inflate(_ compressed: [UInt8]) throws -> [UInt8] {
        if !started {
            guard compression_stream_init(&stream, COMPRESSION_STREAM_DECODE, COMPRESSION_ZLIB) == COMPRESSION_STATUS_OK else { throw RFBZRLE.Failure.inflate }
            started = true
        }
        var input = compressed[...]
        let skip = min(headerLeft, input.count)
        input = input.dropFirst(skip)
        headerLeft -= skip
        var result: [UInt8] = []
        try input.withUnsafeBufferPointer { src in
            stream.src_ptr = src.baseAddress ?? UnsafePointer<UInt8>(bitPattern: 1)!
            stream.src_size = src.count
            repeat {
                let produced = try out.withUnsafeMutableBufferPointer { dst -> Int in
                    stream.dst_ptr = dst.baseAddress!
                    stream.dst_size = dst.count
                    let status = compression_stream_process(&stream, 0)
                    guard status != COMPRESSION_STATUS_ERROR else { throw RFBZRLE.Failure.inflate }
                    return dst.count - stream.dst_size
                }
                result.append(contentsOf: out[0..<produced])
                if produced < out.count && stream.src_size == 0 { break }
            } while true
        }
        return result
    }
}

/// ZRLE (encoding 16): one zlib stream for the whole connection, each rectangle a length and its part of it, which
/// inflates to 64×64 tiles of 3-byte pixels (CPIXEL: the low three bytes of the client's format, B G R) in one of
/// raw, solid, packed palette, plain RLE or palette RLE.
nonisolated final class RFBZRLE {
    enum Failure: Error { case inflate, tile }

    private let stream = RFBInflater()

    /// The inflated bytes of one rectangle's compressed part.
    func inflate(_ compressed: [UInt8]) throws -> [UInt8] { try stream.inflate(compressed) }

    /// Draws a rectangle's inflated tiles. A rectangle that does not fit the framebuffer is the server's error; so
    /// is data that ends inside a tile or a palette index past the palette. Pixels are written straight into the
    /// framebuffer, a row at a time (#3067: per-pixel calls took most of the decode).
    func draw(_ data: [UInt8], x: Int, y: Int, w: Int, h: Int, into fb: RFBFramebuffer) throws {
        guard x >= 0, y >= 0, w >= 0, h >= 0, x + w <= fb.width, y + h <= fb.height else { throw Failure.tile }
        try data.withUnsafeBufferPointer { buffer in
            guard let src = buffer.baseAddress else {
                if w > 0, h > 0 { throw Failure.tile }
                return
            }
            let count = buffer.count, stride = fb.width * 4
            var at = 0
            func byte() throws -> UInt8 {
                guard at < count else { throw Failure.tile }
                defer { at += 1 }
                return src[at]
            }
            func run() throws -> Int {
                var n = 1
                while true {
                    let b = Int(try byte())
                    n += b
                    if b != 255 { return n }
                }
            }
            /// Up to 127 palette colours as BGRA words.
            var palette = [UInt32](repeating: 0, count: 128)
            func readPalette(_ n: Int) throws {
                guard at + n * 3 <= count else { throw Failure.tile }
                for i in 0..<n {
                    palette[i] = UInt32(src[at]) | UInt32(src[at + 1]) << 8 | UInt32(src[at + 2]) << 16 | 0xff00_0000
                    at += 3
                }
            }
            /// n pixels of colour c from pixel k of the tile on, across its rows.
            func fill(_ tile: UnsafeMutablePointer<UInt8>, tw: Int, from k: Int, _ n: Int, _ c: UInt32) {
                var k = k, left = n
                while left > 0 {
                    let row = k / tw, col = k % tw, span = min(left, tw - col)
                    let p = UnsafeMutableRawPointer(tile + row * stride + col * 4).assumingMemoryBound(to: UInt32.self)
                    for i in 0..<span { p[i] = c }
                    k += span
                    left -= span
                }
            }
            var ty = y
            while ty < y + h {
                let th = min(64, y + h - ty)
                var tx = x
                while tx < x + w {
                    let tw = min(64, x + w - tx), area = tw * th
                    let tile = fb.pixels + ty * stride + tx * 4
                    let sub = try byte()
                    switch sub {
                    case 0:
                        guard at + area * 3 <= count else { throw Failure.tile }
                        for j in 0..<th {
                            var p = tile + j * stride, s = src + at + j * tw * 3
                            for _ in 0..<tw {
                                p[0] = s[0]; p[1] = s[1]; p[2] = s[2]; p[3] = 255
                                p += 4; s += 3
                            }
                        }
                        at += area * 3
                    case 1:
                        try readPalette(1)
                        fill(tile, tw: tw, from: 0, area, palette[0])
                    case 2...16:
                        let n = Int(sub), bits = sub == 2 ? 1 : sub <= 4 ? 2 : 4, mask = (1 << bits) - 1
                        try readPalette(n)
                        let rowBytes = (tw * bits + 7) / 8
                        guard at + rowBytes * th <= count else { throw Failure.tile }
                        for j in 0..<th {
                            let p = UnsafeMutableRawPointer(tile + j * stride).assumingMemoryBound(to: UInt32.self), r = src + at + j * rowBytes
                            for i in 0..<tw {
                                let bit = i * bits
                                let index = Int(r[bit >> 3] >> UInt8(8 - bits - bit & 7)) & mask
                                guard index < n else { throw Failure.tile }
                                p[i] = palette[index]
                            }
                        }
                        at += rowBytes * th
                    case 128:
                        var i = 0
                        while i < area {
                            try readPalette(1)
                            let c = palette[0], n = try run()
                            fill(tile, tw: tw, from: i, min(n, area - i), c)
                            i += n
                        }
                    case 130...255:
                        let n = Int(sub - 128)
                        try readPalette(n)
                        var i = 0
                        while i < area {
                            let v = try byte()
                            let length = v & 128 != 0 ? try run() : 1
                            let index = Int(v & 127)
                            guard index < n else { throw Failure.tile }
                            fill(tile, tw: tw, from: i, min(length, area - i), palette[index])
                            i += length
                        }
                    default:
                        throw Failure.tile
                    }
                    tx += 64
                }
                ty += 64
            }
        }
    }
}
