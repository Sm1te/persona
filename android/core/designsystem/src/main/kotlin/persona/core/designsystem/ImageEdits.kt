package persona.core.designsystem

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Matrix
import android.graphics.Paint
import androidx.compose.runtime.Immutable
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.toArgb
import kotlin.math.abs
import kotlin.math.ceil
import kotlin.math.floor
import kotlin.math.max
import kotlin.math.min

// What the image editor did to a picture (#4992, iOS `ImageEdits` from #4963): turned it, cropped it, drew on it. Kept
// apart from the picture, so an edited photo opens again with its crop and marks as they were, each still undoable, and
// the source is never written. Pure values: the editor draws them on screen, [ImageEdits.render] burns them in.

/** A crop's shape: free, the picture's own, or width : height. */
enum class CropShape {
    Free, Original, Square, Wide4x3, Tall3x4, Wide16x9, Tall9x16;

    /** Width over height in pixels, for a picture of [size] (as turned); null: any. */
    fun ratio(size: Size): Float? = when (this) {
        Free -> null
        Original -> if (size.height > 0f) size.width / size.height else null
        Square -> 1f
        Wide4x3 -> 4f / 3
        Tall3x4 -> 3f / 4
        Wide16x9 -> 16f / 9
        Tall9x16 -> 9f / 16
    }

    /** The same shape once the picture has turned a quarter: 4:3 becomes 3:4. */
    val turned: CropShape get() = when (this) {
        Wide4x3 -> Tall3x4
        Tall3x4 -> Wide4x3
        Wide16x9 -> Tall9x16
        Tall9x16 -> Wide16x9
        else -> this
    }
}

/** Where a crop is taken by the finger: a corner, an edge, or inside it (moves it). */
enum class CropHandle {
    TopLeft, Top, TopRight, Left, Right, BottomLeft, Bottom, BottomRight, Inside;

    val left get() = this == TopLeft || this == Left || this == BottomLeft
    val right get() = this == TopRight || this == Right || this == BottomRight
    val upper get() = this == TopLeft || this == Top || this == TopRight
    val lower get() = this == BottomLeft || this == Bottom || this == BottomRight
    val corner get() = (left || right) && (upper || lower)

    companion object {
        /** What a finger at [p] takes of a crop framed at [frame] within [reach]: a corner or an edge, else inside it; null outside. */
        fun at(p: Offset, frame: Rect, reach: Float): CropHandle? {
            if (!frame.inflate(reach).contains(p)) return null
            val l = abs(p.x - frame.left) <= reach
            val r = !l && abs(p.x - frame.right) <= reach
            val t = abs(p.y - frame.top) <= reach
            val b = !t && abs(p.y - frame.bottom) <= reach
            return when {
                l && t -> TopLeft
                r && t -> TopRight
                l && b -> BottomLeft
                r && b -> BottomRight
                l -> Left
                r -> Right
                t -> Top
                b -> Bottom
                else -> if (frame.contains(p)) Inside else null
            }
        }
    }
}

@Immutable
data class ImageEdits(
    /** Quarter turns clockwise, 0 to 3. */
    val turns: Int = 0,
    /** What is kept, in fractions of the picture as turned. */
    val crop: Rect = Whole,
    /** The crop's shape, kept with it so a turn or an undo brings it back as it was. */
    val shape: CropShape = CropShape.Free,
    val marks: List<EditorMark> = emptyList(),
) {
    /** Nothing done: the picture goes as it is. */
    val isEmpty get() = turns == 0 && crop == Whole && marks.isEmpty()
    /** Neither turned nor cropped (Reset has nothing to do). */
    val isFramedAsIs get() = turns == 0 && crop == Whole && shape == CropShape.Free

    /** The picture's size ([size], its pixels) once turned. */
    fun turned(size: Size): Size = if (turns % 2 == 0) size else Size(size.height, size.width)

    /** A point of the picture where it is once turned. */
    fun turn(p: Offset, size: Size): Offset = when (turns) {
        1 -> Offset(size.height - p.y, p.x)
        2 -> Offset(size.width - p.x, size.height - p.y)
        3 -> Offset(p.y, size.width - p.x)
        else -> p
    }

    /** A point of the turned picture back where it is in the picture. */
    fun unturn(p: Offset, size: Size): Offset = when (turns) {
        1 -> Offset(p.y, size.height - p.x)
        2 -> Offset(size.width - p.x, size.height - p.y)
        3 -> Offset(size.width - p.y, p.x)
        else -> p
    }

    /** What [turn] does, as a matrix (picture pixels to turned pixels). */
    fun turnMatrix(size: Size): Matrix = Matrix().apply {
        when (turns) {
            1 -> setValues(floatArrayOf(0f, -1f, size.height, 1f, 0f, 0f, 0f, 0f, 1f))
            2 -> setValues(floatArrayOf(-1f, 0f, size.width, 0f, -1f, size.height, 0f, 0f, 1f))
            3 -> setValues(floatArrayOf(0f, 1f, 0f, -1f, 0f, size.width, 0f, 0f, 1f))
        }
    }

    /** The crop in the turned picture's pixels, whole pixels, inside it. */
    fun cropRect(size: Size): Rect {
        val t = turned(size)
        return Rect(floor(crop.left * t.width), floor(crop.top * t.height), ceil(crop.right * t.width), ceil(crop.bottom * t.height))
            .intersect(Rect(0f, 0f, t.width, t.height))
    }

    /** Turned a quarter to the left, the crop and its shape with the picture. */
    fun turnLeft(): ImageEdits = copy(turns = (turns + 3) % 4,
        crop = Rect(crop.top, 1 - crop.right, crop.top + crop.height, 1 - crop.right + crop.width), shape = shape.turned)

    /** Back to the picture as it came: not turned, not cropped (the marks stay). */
    fun resetFrame(): ImageEdits = copy(turns = 0, crop = Whole, shape = CropShape.Free)

    /** [shape] from now on, the crop made the largest of that shape there is room for round its middle ([size]: the picture's pixels). */
    fun reshape(shape: CropShape, size: Size): ImageEdits {
        val ratio = shape.ratio(turned(size)) ?: return copy(shape = shape)
        return copy(shape = shape, crop = fit(crop, ratio, turned(size)))
    }

    /** The picture with these edits burned in: turned, drawn on, cropped, at its own pixels; null when it can't be drawn. Blocking. */
    fun render(source: Bitmap): Bitmap? {
        val size = Size(source.width.toFloat(), source.height.toFloat())
        val out = cropRect(size)
        if (out.width < 1f || out.height < 1f) return null
        val result = Bitmap.createBitmap(out.width.toInt(), out.height.toInt(), Bitmap.Config.ARGB_8888)
        val canvas = Canvas(result)
        // A transparent picture sent as JPEG would go black where it is clear: white under it.
        canvas.drawColor(android.graphics.Color.WHITE)
        canvas.translate(-out.left, -out.top)
        canvas.concat(turnMatrix(size))
        canvas.drawBitmap(source, 0f, 0f, Paint(Paint.FILTER_BITMAP_FLAG))
        val paint = markPaint()
        for (mark in marks) drawMark(canvas, mark, paint)
        return result
    }

    companion object {
        val Whole = Rect(0f, 0f, 1f, 1f)

        /** The largest crop of [ratio] (width / height, pixels) in a picture of [size] (as turned), round [crop]'s middle and inside it. */
        fun fit(crop: Rect, ratio: Float, size: Size): Rect {
            if (size.width <= 0f || size.height <= 0f || ratio <= 0f) return crop
            val k = ratio * size.height / size.width // the ratio in fractions
            var w = 1f
            var h = 1 / k
            if (h > 1f) { h = 1f; w = k }
            val x = (crop.center.x - w / 2).coerceIn(0f, 1 - w)
            val y = (crop.center.y - h / 2).coerceIn(0f, 1 - h)
            return Rect(x, y, x + w, y + h)
        }

        /**
         * [start] dragged by [handle] by [delta] (fractions of the turned picture), kept inside it and no smaller than
         * [minimum] (fractions); with [k] (width / height in fractions, not pixels) it keeps that shape, a corner pinned
         * opposite the one dragged and an edge's middle opposite the edge dragged.
         */
        fun drag(start: Rect, handle: CropHandle, delta: Offset, minimum: Size, k: Float?): Rect {
            val minW = min(minimum.width, 1f)
            val minH = min(minimum.height, 1f)
            if (handle == CropHandle.Inside) {
                val x = (start.left + delta.x).coerceIn(0f, 1 - start.width)
                val y = (start.top + delta.y).coerceIn(0f, 1 - start.height)
                return Rect(x, y, x + start.width, y + start.height)
            }
            if (k == null || k <= 0f) {
                var l = start.left; var r = start.right; var t = start.top; var b = start.bottom
                if (handle.left) l = (l + delta.x).coerceAtLeast(0f).coerceAtMost(r - minW)
                if (handle.right) r = (r + delta.x).coerceAtMost(1f).coerceAtLeast(l + minW)
                if (handle.upper) t = (t + delta.y).coerceAtLeast(0f).coerceAtMost(b - minH)
                if (handle.lower) b = (b + delta.y).coerceAtMost(1f).coerceAtLeast(t + minH)
                return Rect(l, t, r, b)
            }
            // A fixed shape: the size the finger asks for, then what fits from what stays put.
            val leastW = min(min(max(minW, minH * k), 1f), k)
            if (handle.corner) {
                val anchorX = if (handle.left) start.right else start.left
                val anchorY = if (handle.upper) start.bottom else start.top
                val askedW = if (handle.left) start.width - delta.x else start.width + delta.x
                val askedH = if (handle.upper) start.height - delta.y else start.height + delta.y
                val roomW = if (handle.left) anchorX else 1 - anchorX
                val roomH = if (handle.upper) anchorY else 1 - anchorY
                val w = max(min(min(max(askedW, askedH * k), roomW), roomH * k), min(min(leastW, roomW), roomH * k))
                val h = w / k
                val x = if (handle.left) anchorX - w else anchorX
                val y = if (handle.upper) anchorY - h else anchorY
                return Rect(x, y, x + w, y + h)
            }
            if (handle.left || handle.right) {
                val anchorX = if (handle.left) start.right else start.left
                val askedW = if (handle.left) start.width - delta.x else start.width + delta.x
                val roomW = if (handle.left) anchorX else 1 - anchorX
                val w = max(min(min(askedW, roomW), k), min(leastW, roomW))
                val h = w / k
                val y = (start.center.y - h / 2).coerceIn(0f, 1 - h)
                val x = if (handle.left) anchorX - w else anchorX
                return Rect(x, y, x + w, y + h)
            }
            val anchorY = if (handle.upper) start.bottom else start.top
            val askedH = if (handle.upper) start.height - delta.y else start.height + delta.y
            val roomH = if (handle.upper) anchorY else 1 - anchorY
            val h = max(min(min(askedH, roomH), 1 / k), min(leastW / k, roomH))
            val w = h * k
            val x = (start.center.x - w / 2).coerceIn(0f, 1 - w)
            val y = if (handle.upper) anchorY - h else anchorY
            return Rect(x, y, x + w, y + h)
        }

        internal fun markPaint() = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            style = Paint.Style.STROKE
            strokeCap = Paint.Cap.ROUND
            strokeJoin = Paint.Join.ROUND
        }

        /** [mark] on [canvas] in picture pixels (the canvas already maps them where they go). */
        internal fun drawMark(canvas: Canvas, mark: EditorMark, paint: Paint) {
            val first = mark.points.firstOrNull() ?: return
            if (mark.points.size < 2) return
            val last = mark.points.last()
            paint.color = mark.color.toArgb()
            paint.strokeWidth = mark.width
            if (mark.box) canvas.drawRect(min(first.x, last.x), min(first.y, last.y), max(first.x, last.x), max(first.y, last.y), paint)
            else canvas.drawPath(android.graphics.Path().apply {
                moveTo(first.x, first.y)
                mark.points.drop(1).forEach { lineTo(it.x, it.y) }
            }, paint)
        }
    }
}

/** What the editor left (iOS `EditedImage` without the pictures): the edits and their history, to carry on where it stopped. */
@Immutable
data class ImageEditState(val edits: ImageEdits = ImageEdits(), val history: EditHistory = EditHistory())

/** Undo and Redo over whole edits: each finished change (a mark drawn, a crop let go, a turn, a shape, a reset, Clear) is a step. */
@Immutable
data class EditHistory(val past: List<ImageEdits> = emptyList(), val future: List<ImageEdits> = emptyList()) {
    val canUndo get() = past.isNotEmpty()
    val canRedo get() = future.isNotEmpty()

    /** A change was made to [before]: a step back to it, and nothing left to redo. */
    fun record(before: ImageEdits) = EditHistory(past + before, emptyList())

    /** The edits a step back from [now], and the history with [now] kept for Redo; null with nothing to undo. */
    fun undo(now: ImageEdits): Pair<EditHistory, ImageEdits>? =
        past.lastOrNull()?.let { EditHistory(past.dropLast(1), future + now) to it }

    fun redo(now: ImageEdits): Pair<EditHistory, ImageEdits>? =
        future.lastOrNull()?.let { EditHistory(past + now, future.dropLast(1)) to it }
}
