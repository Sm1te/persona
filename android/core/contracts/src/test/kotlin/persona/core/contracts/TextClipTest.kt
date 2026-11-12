package persona.core.contracts

import org.junit.Assert.assertEquals
import org.junit.Test

class TextClipTest {
    @Test fun aClipNeverSplitsACharacter() {
        assertEquals("abc", "abcdef".clipped(3))
        assertEquals("abcdef", "abcdef".clipped(6))
        assertEquals("ab", "ab😀c".clipped(3))
        assertEquals("ab😀", "ab😀c".clipped(4))
        assertEquals("", "😀".clipped(1))
        assertEquals("", "abc".clipped(0))
    }
}
