package persona.core.contracts

/**
 * The first [max] UTF-16 units of this text, one fewer when the cut would split a surrogate pair (an emoji or another
 * character beyond the BMP), so no half character is left to show as a box (#5284). Shorter text comes back as it is.
 */
fun String.clipped(max: Int): String {
    if (length <= max) return this
    if (max <= 0) return ""
    val end = if (this[max - 1].isHighSurrogate() && this[max].isLowSurrogate()) max - 1 else max
    return substring(0, end)
}
