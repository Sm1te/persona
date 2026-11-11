package persona.core.designsystem

import androidx.compose.ui.res.stringResource
import androidx.annotation.StringRes
import android.graphics.Bitmap
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.border
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.drag
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcons
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** One colour of the image editor's marks. */
@Immutable
class MarkupColor internal constructor(@StringRes val nameRes: Int, val color: Color) {
    val name: String @Composable get() = stringResource(nameRes)
}

/**
 * The image editor's six mark colours. Fixed on purpose, not taken from a style: a mark has to read the same in every
 * style, on the dark editor and in the picture that leaves the phone (the red, yellow, green and blue of iOS's system
 * colours, white, black — as the iOS editor).
 */
object MarkupColors {
    val Red = MarkupColor(R.string.designsystem_markup_red, Color(0xFFFF3B30))
    val Yellow = MarkupColor(R.string.designsystem_markup_yellow, Color(0xFFFFCC00))
    val Green = MarkupColor(R.string.designsystem_markup_green, Color(0xFF34C759))
    val Blue = MarkupColor(R.string.designsystem_markup_blue, Color(0xFF007AFF))
    val White = MarkupColor(R.string.designsystem_markup_white, Color(0xFFFFFFFF))
    val Black = MarkupColor(R.string.designsystem_markup_black, Color(0xFF000000))
    val all: List<MarkupColor> = listOf(Red, Yellow, Green, Blue, White, Black)
}

/**
 * One mark in picture pixels, before the picture is turned or cropped. A box is its first and last point; a pen stroke is
 * all of them. [width] is the stroke as it was on screen, in picture pixels, so the result looks like what the user drew.
 */
@Immutable
data class EditorMark(val box: Boolean, val color: Color, val width: Float, val points: List<Offset>)

internal enum class EditorTool(@StringRes val labelRes: Int) {
    Box(R.string.designsystem_markup_box), Pen(R.string.designsystem_markup_pen), Crop(R.string.designsystem_markup_crop)
}

/** A mark's line on screen: thin, medium (the one Box and Pen drew with before there was a choice; the default) and thick, as iOS. */
private val MarkStroke = 3.5.dp
private val MarkWidths = listOf(MarkStroke * (4f / 7f), MarkStroke, MarkStroke * 2f)
private val WidthNames = listOf(R.string.designsystem_editor_width_thin, R.string.designsystem_editor_width_medium, R.string.designsystem_editor_width_thick)
/** Room round the picture while cropping, so the handles on its edges can be taken; how far round an edge a finger takes it. */
private val CropInset get() = Tokens.Space.xl
private val CropGrab get() = Tokens.Size.iconS
/** The smallest crop, a side on screen; the frame's line and its corner marks. */
private val CropMinimum get() = Tokens.Space.xxl * 2
private val CropLine get() = Tokens.Space.xxs / 2
private val CropCorner get() = Tokens.Size.iconXS
private val CropCornerWidth get() = Tokens.Space.xxs * 1.5f

/**
 * Edits [source] before it goes to an Agent, full screen in the dark appearance (#4992, as the iOS editor from #4963):
 * Box or Pen in six colours and three widths, Crop (corners and edges drag, inside moves it; a shape; Rotate a quarter to
 * the left; Reset), Undo and Redo over every change, and Clear. [onDone] gets the picture with the edits burned in at
 * [source]'s own resolution ([source] itself when nothing was done); after that the editor waits, "Done" spinning, until
 * its caller removes it. Back and "Cancel" call [onCancel]. Pass a software bitmap ([decodePicture] with `software = true`).
 * [start]: the edits a picture edited before carries on from, still undoable; [onEdited] gets the edits and their history
 * just before [onDone], so the caller can keep them beside the result (#4992, iOS `EditedImage`).
 */
@Composable
fun ImageEditor(source: Bitmap, onCancel: () -> Unit, onDone: (Bitmap) -> Unit, start: ImageEditState = ImageEditState(),
                onEdited: (ImageEditState) -> Unit = {}) {
    var finishing by remember(source) { mutableStateOf(false) }
    AlwaysDark {
        JFullScreen({ if (!finishing) onCancel() }) {
            CompositionLocalProvider(LocalReadingInset provides 0.dp) {
                EditorBody(source, start, finishing, { finishing = it }, onCancel, onDone, onEdited)
            }
        }
    }
}

/** Where things are on screen: the whole turned picture's frame and screen pixels per picture pixel, what shows of it, and the crop's frame. */
internal data class EditorLayout(val picture: Rect, val scale: Float, val visible: Rect, val crop: Rect) {
    /** A point on screen in the turned picture's pixels, kept inside what shows. */
    fun pixel(p: Offset): Offset {
        val q = Offset(p.x.coerceIn(visible.left, visible.right), p.y.coerceIn(visible.top, visible.bottom))
        return Offset((q.x - picture.left) / scale, (q.y - picture.top) / scale)
    }

    companion object {
        /**
         * Marking, the crop fills [area]; cropping, all of the turned picture shows inside [inset], the crop's frame over it
         * ([image]: the source's pixels).
         */
        fun of(edits: ImageEdits, image: Size, area: Size, cropping: Boolean, inset: Float): EditorLayout {
            val turned = edits.turned(image)
            if (turned.width <= 0f || turned.height <= 0f || area.width <= 0f) return EditorLayout(Rect.Zero, 1f, Rect.Zero, Rect.Zero)
            if (cropping) {
                val room = Size((area.width - 2 * inset).coerceAtLeast(1f), (area.height - 2 * inset).coerceAtLeast(1f))
                val picture = ImageMath.fitRect(turned, room).translate(inset, inset)
                val c = edits.crop
                val crop = Rect(picture.left + c.left * picture.width, picture.top + c.top * picture.height,
                    picture.left + c.right * picture.width, picture.top + c.bottom * picture.height)
                return EditorLayout(picture, picture.width / turned.width, picture, crop)
            }
            val kept = edits.cropRect(image)
            val shown = ImageMath.fitRect(kept.size, area)
            val scale = shown.width / kept.width.coerceAtLeast(1f)
            val picture = Rect(Offset(shown.left - kept.left * scale, shown.top - kept.top * scale), Size(turned.width * scale, turned.height * scale))
            return EditorLayout(picture, scale, shown, shown)
        }
    }
}

@Composable
private fun EditorBody(source: Bitmap, start: ImageEditState, finishing: Boolean, onFinishing: (Boolean) -> Unit, onCancel: () -> Unit,
                       onDone: (Bitmap) -> Unit, onEdited: (ImageEditState) -> Unit) {
    val palette = PersonaTheme.palette
    val density = LocalDensity.current
    var tool by remember { mutableStateOf(EditorTool.Box) }
    var color by remember { mutableStateOf(MarkupColors.Red) }
    var width by remember { mutableStateOf(1) }
    var edits by remember(source) { mutableStateOf(start.edits) }
    var history by remember(source) { mutableStateOf(start.history) }
    var current by remember(source) { mutableStateOf<EditorMark?>(null) }
    var dragging by remember { mutableStateOf(false) }
    var box by remember { mutableStateOf(IntSize.Zero) }
    var failed by remember { mutableStateOf(false) }
    var choosing by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val imageSize = remember(source) { Size(source.width.toFloat(), source.height.toFloat()) }
    val px = with(density) { object { val inset = CropInset.toPx(); val grab = CropGrab.toPx(); val minimum = CropMinimum.toPx()
        val line = CropLine.toPx(); val corner = CropCorner.toPx(); val cornerWidth = CropCornerWidth.toPx() } }
    val layout = EditorLayout.of(edits, imageSize, Size(box.width.toFloat(), box.height.toFloat()), tool == EditorTool.Crop, px.inset)
    val strokes = MarkWidths.map { with(density) { it.toPx() } }
    val latest by rememberUpdatedState(Triple(layout, edits, tool))
    val latestStyle by rememberUpdatedState(color to strokes[width])
    val latestFinishing by rememberUpdatedState(finishing)
    val done by rememberUpdatedState(onDone)

    // One step of the history: the edits [make] changes, the ones before kept for Undo.
    fun change(make: (ImageEdits) -> ImageEdits) {
        val next = make(edits)
        if (next == edits) return
        history = history.record(edits)
        edits = next
    }

    fun finish() {
        if (finishing) return
        onFinishing(true)
        failed = false
        val final = edits
        onEdited(ImageEditState(final, history))
        scope.launch {
            val result = try {
                if (final.isEmpty) source else withContext(Dispatchers.Default) { final.render(source) }
            } catch (e: CancellationException) {
                throw e
            } catch (_: Throwable) {
                null
            }
            if (result == null) { failed = true; onFinishing(false) } else done(result)
        }
    }

    Column(Modifier.fillMaxSize()) {
        Box(Modifier.fillMaxWidth().statusBarsPadding().height(56.dp).padding(horizontal = Tokens.Space.xs)) {
            JTextButton(stringResource(R.string.designsystem_cancel), onCancel, Modifier.align(Alignment.CenterStart), enabled = !finishing)
            Text(
                stringResource(if (failed) R.string.designsystem_editor_failed else R.string.designsystem_editor_title),
                Modifier.align(Alignment.Center).widthIn(max = 220.dp),
                style = PersonaTheme.type.title, color = if (failed) palette.danger.text else palette.ink,
                maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
            JButton(
                stringResource(R.string.designsystem_done), ::finish, Modifier.align(Alignment.CenterEnd).padding(end = Tokens.Space.s),
                size = JButtonSize.Small, loading = finishing,
            )
        }
        Box(
            Modifier.weight(1f).fillMaxWidth().onSizeChanged { box = it }
                .pointerInput(source) {
                    awaitEachGesture {
                        val down = awaitFirstDown()
                        val (l, start, t) = latest
                        if (l.picture.width <= 0f || latestFinishing) return@awaitEachGesture
                        if (t == EditorTool.Crop) {
                            val handle = CropHandle.at(down.position, l.crop, px.grab) ?: return@awaitEachGesture
                            down.consume()
                            dragging = true
                            val from = start.crop
                            val minimum = Size(px.minimum / l.picture.width, px.minimum / l.picture.height)
                            val k = start.shape.ratio(start.turned(imageSize))?.let { it * l.picture.height / l.picture.width }
                            drag(down.id) { change ->
                                val d = change.position - down.position
                                edits = edits.copy(crop = ImageEdits.drag(from, handle, Offset(d.x / l.picture.width, d.y / l.picture.height), minimum, k))
                                change.consume()
                            }
                            dragging = false
                            if (edits.crop != from) history = history.record(edits.copy(crop = from))
                            return@awaitEachGesture
                        }
                        down.consume()
                        val (ink, stroke) = latestStyle
                        fun toSource(p: Offset) = start.unturn(l.pixel(p), imageSize)
                        current = EditorMark(t == EditorTool.Box, ink.color, stroke / l.scale, listOf(toSource(down.position)))
                        val lifted = drag(down.id) { change ->
                            val point = toSource(change.position)
                            current = current?.let { mark -> if (mark.box) mark.copy(points = listOf(mark.points.first(), point)) else mark.copy(points = mark.points + point) }
                            change.consume()
                        }
                        val mark = current
                        current = null
                        if (lifted && mark != null && mark.points.size > 1 && (!mark.box || mark.points.first() != mark.points.last()))
                            change { it.copy(marks = it.marks + mark) }
                    }
                },
        ) {
            val description = stringResource(R.string.designsystem_editor_picture)
            Canvas(Modifier.fillMaxSize().semantics { contentDescription = description }) {
                if (layout.picture.width <= 0f) return@Canvas
                clipRect(layout.visible.left, layout.visible.top, layout.visible.right, layout.visible.bottom) {
                    drawIntoCanvas { canvas ->
                        val native = canvas.nativeCanvas
                        native.save()
                        native.translate(layout.picture.left, layout.picture.top)
                        native.scale(layout.scale, layout.scale)
                        native.concat(edits.turnMatrix(imageSize))
                        native.drawBitmap(source, 0f, 0f, android.graphics.Paint(android.graphics.Paint.FILTER_BITMAP_FLAG))
                        val paint = ImageEdits.markPaint()
                        edits.marks.forEach { ImageEdits.drawMark(native, it, paint) }
                        current?.let { ImageEdits.drawMark(native, it, paint) }
                        native.restore()
                    }
                }
                if (tool == EditorTool.Crop) drawCropFrame(layout.crop, dragging, px.line, px.corner, px.cornerWidth, palette.ink, palette.background)
            }
        }
        Column(
            Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.m),
            verticalArrangement = Arrangement.spacedBy(Tokens.Space.m),
        ) {
            if (tool == EditorTool.Crop) Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JTextButton(stringResource(R.string.designsystem_editor_rotate), { change { it.turnLeft() } }, enabled = !finishing,
                    icon = PhIcons.ArrowCounterClockwise)
                JTextButton(stringResource(shapeName(edits.shape)), { choosing = "shape" }, enabled = !finishing, icon = PhIcons.Crop)
                Spacer(Modifier.weight(1f))
                JTextButton(stringResource(R.string.designsystem_editor_reset), { change { it.resetFrame() } }, enabled = !finishing && !edits.isFramedAsIs)
            } else Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Row(Modifier.weight(1f), horizontalArrangement = Arrangement.Center) {
                    MarkupColors.all.forEach { option -> ColorSwatch(option, option == color) { color = option } }
                }
                val widthName = stringResource(WidthNames[width])
                JIconButton(PhIcons.PencilSimple, stringResource(R.string.designsystem_editor_width) + ", " + widthName, { choosing = "width" }, enabled = !finishing)
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                JSegmented(EditorTool.entries.map { JSegment(stringResource(it.labelRes)) }, tool.ordinal, { tool = EditorTool.entries[it] })
                Spacer(Modifier.weight(1f))
                JIconButton(PhIcons.ArrowUUpLeft, stringResource(R.string.designsystem_editor_undo), {
                    history.undo(edits)?.let { (h, back) -> history = h; edits = back }
                }, enabled = history.canUndo && !finishing)
                JIconButton(PhIcons.ArrowUUpRight, stringResource(R.string.designsystem_editor_redo), {
                    history.redo(edits)?.let { (h, ahead) -> history = h; edits = ahead }
                }, enabled = history.canRedo && !finishing)
                if (tool != EditorTool.Crop) JIconButton(PhIcons.Trash, stringResource(R.string.designsystem_editor_clear),
                    { change { it.copy(marks = emptyList()) } }, enabled = edits.marks.isNotEmpty() && !finishing)
            }
        }
    }
    when (choosing) {
        "shape" -> JActionSheet(CropShape.entries.map { s ->
            JAction(stringResource(shapeName(s)), checked = s == edits.shape, onClick = { change { it.reshape(s, imageSize) } })
        }, { choosing = null }, title = stringResource(R.string.designsystem_editor_shape))
        "width" -> JActionSheet(WidthNames.mapIndexed { i, name -> JAction(stringResource(name), checked = i == width, onClick = { width = i }) },
            { choosing = null }, title = stringResource(R.string.designsystem_editor_width))
    }
}

@StringRes
private fun shapeName(shape: CropShape): Int = when (shape) {
    CropShape.Free -> R.string.designsystem_editor_shape_free
    CropShape.Original -> R.string.designsystem_editor_shape_original
    CropShape.Square -> R.string.designsystem_editor_shape_square
    CropShape.Wide4x3 -> R.string.designsystem_editor_shape_4x3
    CropShape.Tall3x4 -> R.string.designsystem_editor_shape_3x4
    CropShape.Wide16x9 -> R.string.designsystem_editor_shape_16x9
    CropShape.Tall9x16 -> R.string.designsystem_editor_shape_9x16
}

/** The crop's frame: the rest dimmed, a line round it, its corners marked, the thirds inside it while it is dragged. */
/** [ink] the frame (white in the dark editor), [dim] what is cut away. */
private fun DrawScope.drawCropFrame(frame: Rect, dragging: Boolean, line: Float, corner: Float, cornerWidth: Float, ink: Color, dim: Color) {
    val outside = Path().apply {
        fillType = PathFillType.EvenOdd
        addRect(Rect(Offset.Zero, size))
        addRect(frame)
    }
    drawPath(outside, dim.copy(alpha = 0.6f))
    drawRect(ink, frame.topLeft, frame.size, style = Stroke(line))
    if (dragging) for (i in 1..2) {
        val x = frame.left + frame.width * i / 3
        val y = frame.top + frame.height * i / 3
        drawLine(ink.copy(alpha = 0.45f), Offset(x, frame.top), Offset(x, frame.bottom), line)
        drawLine(ink.copy(alpha = 0.45f), Offset(frame.left, y), Offset(frame.right, y), line)
    }
    val c = minOf(corner, frame.width / 2, frame.height / 2)
    for ((x, y, dx, dy) in listOf(listOf(frame.left, frame.top, c, c), listOf(frame.right, frame.top, -c, c),
            listOf(frame.left, frame.bottom, c, -c), listOf(frame.right, frame.bottom, -c, -c))) {
        drawPath(Path().apply { moveTo(x + dx, y); lineTo(x, y); lineTo(x, y + dy) }, ink,
            style = Stroke(cornerWidth, cap = StrokeCap.Square))
    }
}

/** A colour to pick: a ring around the chosen one, a thin edge on every one so black reads on the dark bar. */
@Composable
private fun ColorSwatch(option: MarkupColor, chosen: Boolean, onClick: () -> Unit) {
    val palette = PersonaTheme.palette
    val name = option.name
    Box(
        Modifier.size(44.dp).clip(CircleShape)
            .clickable(role = Role.RadioButton, onClick = onClick)
            .semantics { contentDescription = name; selected = chosen },
        contentAlignment = Alignment.Center,
    ) {
        Box(
            // A 0.dp border is a hairline in Compose, so the ring is added only when chosen.
            Modifier.size(34.dp).then(if (chosen) Modifier.border(2.5.dp, palette.ink, CircleShape) else Modifier).padding(4.dp)
                .clip(CircleShape).background(option.color).border(1.dp, palette.ink3, CircleShape),
        )
    }
}
