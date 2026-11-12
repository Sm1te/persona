package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import kotlin.math.abs

/**
 * The image editor's edits (#4992, iOS `ImageEditsTests` from #4963): turning, cropping and marks map points and pixels
 * where they belong, a crop handle keeps the crop inside the picture and in its shape, and Undo and Redo step through
 * whole edits.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35])
class ImageEditsTest {
    private val size = Size(40f, 30f)

    private fun close(a: Rect, b: Rect) = abs(a.left - b.left) < 1e-5 && abs(a.top - b.top) < 1e-5 &&
        abs(a.width - b.width) < 1e-5 && abs(a.height - b.height) < 1e-5

    // Turning

    @Test fun aPointComesBackFromEveryTurn() {
        val points = listOf(Offset(0f, 0f), Offset(40f, 0f), Offset(13f, 7f), Offset(40f, 30f))
        for (turns in 0..3) {
            val e = ImageEdits(turns = turns)
            for (p in points) {
                val back = e.unturn(e.turn(p, size), size)
                assertTrue("turns $turns: $p came back as $back", abs(back.x - p.x) < 1e-4 && abs(back.y - p.y) < 1e-4)
                val mapped = floatArrayOf(p.x, p.y).also { e.turnMatrix(size).mapPoints(it) }
                assertEquals("turns $turns: the matrix agrees", e.turn(p, size), Offset(mapped[0], mapped[1]))
            }
        }
    }

    @Test fun aQuarterClockwisePutsTheTopLeftAtTheTopRight() {
        val e = ImageEdits(turns = 1)
        assertEquals(Size(30f, 40f), e.turned(size))
        assertEquals(Offset(30f, 0f), e.turn(Offset(0f, 0f), size))
        assertEquals(Offset(30f, 40f), e.turn(Offset(40f, 0f), size))
    }

    @Test fun turningLeftTakesTheCropAndItsShapeAlong() {
        var e = ImageEdits(crop = Rect(0.1f, 0.2f, 0.6f, 0.5f), shape = CropShape.Wide4x3).turnLeft()
        assertEquals(3, e.turns)
        assertEquals(CropShape.Tall3x4, e.shape)
        assertTrue(close(e.crop, Rect(0.2f, 0.4f, 0.5f, 0.9f)))
        repeat(3) { e = e.turnLeft() }
        assertEquals(0, e.turns)
        assertEquals(CropShape.Wide4x3, e.shape)
        assertTrue("four turns left: back where it was", close(e.crop, Rect(0.1f, 0.2f, 0.6f, 0.5f)))
    }

    @Test fun resetKeepsTheMarks() {
        val mark = EditorMark(false, Color.Red, 2f, listOf(Offset.Zero, Offset(1f, 1f)))
        val e = ImageEdits(turns = 2, crop = Rect(0f, 0f, 0.5f, 0.5f), shape = CropShape.Square, marks = listOf(mark)).resetFrame()
        assertTrue(e.isFramedAsIs)
        assertEquals(1, e.marks.size)
        assertFalse(e.isEmpty)
    }

    // Shapes and handles

    @Test fun aSquareOnAWidePictureIsAsTallAsItAndInTheMiddle() {
        val e = ImageEdits().reshape(CropShape.Square, size) // 40 × 30
        assertTrue(close(e.crop, Rect(0.125f, 0f, 0.875f, 1f)))
        assertEquals(Rect(5f, 0f, 35f, 30f), e.cropRect(size))
        assertTrue("Freeform keeps the crop as it is", close(e.reshape(CropShape.Free, size).crop, Rect(0.125f, 0f, 0.875f, 1f)))
    }

    @Test fun aFreeCornerStaysInsideAndNoSmallerThanTheLeast() {
        val start = Rect(0.2f, 0.2f, 0.7f, 0.7f)
        val least = Size(0.1f, 0.1f)
        assertTrue("pulled past the edge: stops at it",
            close(ImageEdits.drag(start, CropHandle.TopLeft, Offset(-0.5f, -0.5f), least, null), Rect(0f, 0f, 0.7f, 0.7f)))
        assertTrue("pushed past the other side: stops at the least",
            close(ImageEdits.drag(start, CropHandle.BottomRight, Offset(-0.9f, -0.9f), least, null), Rect(0.2f, 0.2f, 0.3f, 0.3f)))
        assertTrue("an edge moves only its own side",
            close(ImageEdits.drag(start, CropHandle.Right, Offset(0.1f, 0.3f), least, null), Rect(0.2f, 0.2f, 0.8f, 0.7f)))
    }

    @Test fun aFixedShapeKeepsItsShapeAndTheOppositeCornerStays() {
        val start = Rect(0.3f, 0.3f, 0.7f, 0.7f)
        val least = Size(0.1f, 0.1f)
        assertTrue(close(ImageEdits.drag(start, CropHandle.BottomRight, Offset(0.2f, 0.05f), least, 1f), Rect(0.3f, 0.3f, 0.9f, 0.9f)))
        assertTrue("as large as fits from the corner that stays",
            close(ImageEdits.drag(start, CropHandle.TopLeft, Offset(-0.9f, -0.1f), least, 1f), Rect(0f, 0f, 0.7f, 0.7f)))
        val side = ImageEdits.drag(start, CropHandle.Right, Offset(0.2f, 0f), least, 1f)
        assertTrue("an edge grows round its middle", abs(side.width - side.height) < 1e-5 && side.left == 0.3f && abs(side.center.y - 0.5f) < 1e-5)
    }

    @Test fun insideMovesTheCropAndNotPastTheEdge() {
        val out = ImageEdits.drag(Rect(0.2f, 0.2f, 0.7f, 0.7f), CropHandle.Inside, Offset(0.6f, -0.1f), Size.Zero, null)
        assertTrue(close(out, Rect(0.5f, 0.1f, 1f, 0.6f)))
    }

    @Test fun aFingerTakesTheCornerTheEdgeOrTheInside() {
        val frame = Rect(100f, 100f, 300f, 200f)
        assertEquals(CropHandle.TopLeft, CropHandle.at(Offset(95f, 104f), frame, 22f))
        assertEquals(CropHandle.BottomRight, CropHandle.at(Offset(305f, 196f), frame, 22f))
        assertEquals(CropHandle.Top, CropHandle.at(Offset(200f, 92f), frame, 22f))
        assertEquals(CropHandle.Right, CropHandle.at(Offset(299f, 150f), frame, 22f))
        assertEquals(CropHandle.Inside, CropHandle.at(Offset(200f, 150f), frame, 22f))
        assertNull(CropHandle.at(Offset(20f, 20f), frame, 22f))
    }

    @Test fun cropLayoutShowsTheWholePictureAndMarkingShowsTheCrop() {
        val e = ImageEdits(crop = Rect(0.5f, 0f, 1f, 1f))
        val cropping = EditorLayout.of(e, size, Size(400f, 600f), cropping = true, inset = 20f)
        assertEquals(360f, cropping.picture.width, 0.01f) // the whole 40 × 30 picture in 360 wide
        assertEquals(cropping.picture.left + 180f, cropping.crop.left, 0.01f)
        val marking = EditorLayout.of(e, size, Size(400f, 600f), cropping = false, inset = 20f)
        assertEquals(400f, marking.visible.width, 0.01f) // the kept half fills the width
        assertEquals("the left edge on screen is the picture's middle", 20f, marking.pixel(Offset(0f, 300f)).x, 0.01f)
    }

    // Drawing

    private fun picture(width: Int, height: Int, vararg colors: Int) =
        Bitmap.createBitmap(colors, width, height, Bitmap.Config.ARGB_8888)

    private val red = android.graphics.Color.RED
    private val blue = android.graphics.Color.BLUE
    private val green = android.graphics.Color.GREEN
    private val white = android.graphics.Color.WHITE

    @Test fun aTurnDrawsThePixelsWhereTheyGo() {
        val image = picture(2, 1, red, blue)
        val clockwise = ImageEdits(turns = 1).render(image)!!
        assertEquals(1, clockwise.width)
        assertEquals(2, clockwise.height)
        assertEquals("turned clockwise, the left side is on top", red, clockwise.getPixel(0, 0))
        assertEquals(blue, clockwise.getPixel(0, 1))
        assertEquals("turned left, the right side is on top", blue, ImageEdits(turns = 3).render(image)!!.getPixel(0, 0))
    }

    @Test fun aCropKeepsItsOwnPixels() {
        val out = ImageEdits(crop = Rect(0.25f, 0f, 0.75f, 1f)).render(picture(4, 1, red, blue, green, red))!!
        assertEquals(2, out.width)
        assertEquals(blue, out.getPixel(0, 0))
        assertEquals(green, out.getPixel(1, 0))
    }

    @Test fun aMarkIsBurnedInWhereItWasDrawnAfterATurn() {
        val image = picture(10, 10, *IntArray(100) { white })
        val box = EditorMark(true, Color.Red, 2f, listOf(Offset(2f, 2f), Offset(7f, 7f)))
        fun ink(c: Int) = android.graphics.Color.red(c) > 200 && android.graphics.Color.green(c) < 120
        val plain = ImageEdits(marks = listOf(box)).render(image)!!
        assertTrue("the box's left side", ink(plain.getPixel(2, 4)))
        assertEquals("inside the box", white, plain.getPixel(5, 5))
        // Turned clockwise, the source's (2.5, 4.5) is at (10 - 4.5, 2.5).
        val turned = ImageEdits(turns = 1, marks = listOf(box)).render(image)!!
        assertTrue(ink(turned.getPixel(5, 2)))
        assertEquals(white, turned.getPixel(5, 5))
    }

    @Test fun aClearPictureGoesWhiteNotBlack() {
        val out = ImageEdits(turns = 2).render(picture(2, 2, 0, 0, 0, 0))!!
        assertEquals(white, out.getPixel(0, 0))
    }

    // Undo and Redo

    @Test fun undoAndRedoStepThroughWholeEdits() {
        val a = ImageEdits()
        val b = ImageEdits(turns = 3)
        val c = ImageEdits(turns = 2)
        var history = EditHistory().record(a).record(b)
        var now = c
        history.undo(now)!!.let { (h, e) -> history = h; now = e }
        assertEquals(b, now)
        assertTrue(history.canRedo)
        history.undo(now)!!.let { (h, e) -> history = h; now = e }
        assertEquals(a, now)
        assertFalse(history.canUndo)
        history.redo(now)!!.let { (h, e) -> history = h; now = e }
        assertEquals(b, now)
        history = history.record(now) // a new change: nothing left to redo
        assertFalse(history.canRedo)
        assertEquals(2, history.past.size)
    }
}
