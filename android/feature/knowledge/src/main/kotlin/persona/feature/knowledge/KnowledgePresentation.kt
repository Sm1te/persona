package persona.feature.knowledge

import persona.core.contracts.Session
import persona.core.contracts.contextBody
import persona.core.contracts.api.*
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.foldedSubtasks
import kotlinx.serialization.json.*
import androidx.annotation.StringRes
import java.time.Instant
import java.time.LocalDate
import java.time.OffsetDateTime
import kotlin.math.roundToLong

internal data class KnowledgeSection(
    @StringRes val title: Int,
    val entries: List<KnowledgeEntry>,
    val collapsed: Boolean = false,
    /** Said under a folded section: why its entries are there and how they come back. */
    @StringRes val note: Int? = null,
    /** A section named by a tag (#tag) instead of [title]. */
    val tag: String? = null,
) {
    /** Stable across languages, for list keys. */
    val key: String get() = tag?.let { "#$it" } ?: title.toString()
}

/** One entry found by a search, with up to all of its hits; [named] once a list told us the entry's name. */
internal data class KnowledgeSearchGroup(
    val kind: KnowledgeKind,
    val id: String,
    val entry: KnowledgeEntry,
    val named: Boolean,
    val hits: List<KnowledgeHit>,
) {
    val key: String get() = "${kind.wire}/$id"
}

/** Every kind the app knows: all conversations, private ones too (docs/archive/redesign/parity.md 3.1). */
internal fun KnowledgeEntry.inScope(): Boolean = KnowledgeKind.fromWire(kind) != null

/** Projects as iOS groups them: "Focus" (P0 / P1), "Other active", "Done", then "Not projects" folded with a word on why. */
internal fun projectSections(entries: List<KnowledgeEntry>): List<KnowledgeSection> {
    val (ignored, visible) = entries.partition { it.projectStatus == "ignored" }
    val (done, active) = visible.partition { it.projectStatus == "done" }
    val (focus, rest) = active.partition { it.importance in setOf("P0", "P1") }
    return listOf(
        KnowledgeSection(R.string.knowledge_section_focus, focus),
        KnowledgeSection(if (focus.isEmpty()) R.string.knowledge_section_active else R.string.knowledge_section_other_active, rest),
        KnowledgeSection(R.string.knowledge_section_done, done),
        KnowledgeSection(R.string.knowledge_section_not_projects, ignored, collapsed = true,
            note = R.string.knowledge_section_not_projects_note),
    ).filter { it.entries.isNotEmpty() }
}

/** The other kinds keep the hub's order (importance, then relevance); a tag names the section. */
internal fun kindSections(entries: List<KnowledgeEntry>, tag: String?): List<KnowledgeSection> =
    if (entries.isEmpty()) emptyList() else listOf(KnowledgeSection(R.string.knowledge_section_ranked, entries, tag = tag))

/** A project's `extra` is "short|long · active|paused|done|ignored"; missing halves read as short and active. */
internal val KnowledgeEntry.projectKind: String
    get() = extra.orEmpty().split(" · ").firstOrNull()?.trim()?.takeIf { it.isNotEmpty() } ?: "short"
internal val KnowledgeEntry.projectStatus: String
    get() = extra.orEmpty().split(" · ").getOrNull(1)?.trim()?.takeIf { it.isNotEmpty() } ?: "active"
internal val KnowledgeEntry.isProject: Boolean get() = kind == KnowledgeKind.Projects.wire

/** "Paused" / "Done" / "Not a project": a project's status when it is not the usual "Active". */
@get:StringRes
internal val KnowledgeEntry.projectStatusLabel: Int?
    get() = if (!isProject) null else when (projectStatus) {
        "paused" -> R.string.knowledge_status_paused
        "done" -> R.string.knowledge_status_done
        "ignored" -> R.string.knowledge_status_ignored
        else -> null
    }

/** The entry a hit is in: `chats/<id>/summary.md` → (chats, id), `docs/<id>.json` → (docs, id); null for anything else. */
internal fun KnowledgeHit.reference(): Pair<KnowledgeKind, String>? {
    val parts = path.split('/').filter { it.isNotEmpty() }
    if (parts.size < 2 || parts.any { it == "." || it == ".." }) return null
    val kind = KnowledgeKind.fromWire(parts[0]) ?: return null
    val id = if (parts.size == 2 && parts[1].endsWith(".json")) parts[1].removeSuffix(".json") else parts[1]
    return if (id.isEmpty()) null else kind to id
}

/** Which file a line is from: a day's messages, the profile, the timeline, the details, or another file by name. */
internal sealed interface KnowledgeHitSource {
    data class Messages(val day: String) : KnowledgeHitSource
    data object Profile : KnowledgeHitSource
    data object Timeline : KnowledgeHitSource
    data object Details : KnowledgeHitSource
    /** A recording's transcript (`recordings/<id>/transcript.md`). */
    data object Transcript : KnowledgeHitSource
    data class File(val name: String) : KnowledgeHitSource
}

/** Which file the line is from, said plainly. */
internal val KnowledgeHit.source: KnowledgeHitSource
    get() {
        val file = path.substringAfterLast('/')
        if ("/messages/" in path) return KnowledgeHitSource.Messages(file.substringBeforeLast('.'))
        return when (file) {
            "summary.md" -> KnowledgeHitSource.Profile
            "log.md" -> KnowledgeHitSource.Timeline
            "meta.json" -> KnowledgeHitSource.Details
            "transcript.md" -> KnowledgeHitSource.Transcript
            else -> if (file.endsWith(".json")) KnowledgeHitSource.Details else KnowledgeHitSource.File(file)
        }
    }

/** The line without its markdown list marker or heading, JSON key and quotes. */
internal val KnowledgeHit.text: String
    get() {
        var text = line.trim()
        for (prefix in listOf("- ", "* ", "## ", "# ")) if (text.startsWith(prefix)) text = text.removePrefix(prefix)
        if (text.startsWith("\"")) text.indexOf("\": ").takeIf { it >= 0 }?.let { text = text.substring(it + 3) }
        return text.trim(' ', ',', '"')
    }

/**
 * Hits grouped by the entry they are in, in the order found, each line once; "Profile" and "Timeline" lines before "Details"
 * (iOS `KBSearchGroup`). Hits outside any entry (the directory's README) are no result. [index] names the entries
 * a list has shown; the others are named by their kind until one does.
 */
internal fun searchGroups(hits: List<KnowledgeHit>, index: Map<String, KnowledgeEntry>): List<KnowledgeSearchGroup> {
    val byEntry = LinkedHashMap<String, Pair<Pair<KnowledgeKind, String>, MutableList<KnowledgeHit>>>()
    for (hit in hits) {
        val reference = hit.reference() ?: continue
        val list = byEntry.getOrPut("${reference.first.wire}/${reference.second}") { reference to mutableListOf() }.second
        if (list.none { it.text == hit.text }) list += hit
    }
    return byEntry.map { (key, value) ->
        val (reference, list) = value
        val known = index[key]
        KnowledgeSearchGroup(reference.first, reference.second,
            known ?: KnowledgeEntry(reference.first.wire, reference.second, ""), known != null,
            list.filter { it.source != KnowledgeHitSource.Details } + list.filter { it.source == KnowledgeHitSource.Details })
    }
}

/** Search results by kind, in the order the list offers the kinds. */
internal fun searchSections(groups: List<KnowledgeSearchGroup>): List<Pair<KnowledgeKind, List<KnowledgeSearchGroup>>> =
    KnowledgeKind.entries.mapNotNull { kind -> groups.filter { it.kind == kind }.takeIf { it.isNotEmpty() }?.let { kind to it } }

/** Where every word of [query] (any case) occurs in [text], merged: the hub matches lines with all the words. */
internal fun queryRanges(text: String, query: String): List<IntRange> {
    val words = query.split(Regex("\\s+")).filter { it.isNotEmpty() }
    if (words.isEmpty() || text.isEmpty()) return emptyList()
    val on = BooleanArray(text.length)
    for (word in words) {
        var from = 0
        while (true) {
            val at = text.indexOf(word, from, ignoreCase = true)
            if (at < 0) break
            for (i in at until at + word.length) on[i] = true
            from = at + word.length
        }
    }
    val ranges = mutableListOf<IntRange>()
    var i = 0
    while (i < text.length) {
        if (!on[i]) { i++; continue }
        var j = i
        while (j < text.length && on[j]) j++
        ranges += i until j
        i = j
    }
    return ranges
}

/** A timeline line, `- 2026-10-30 23:16 · Agent · title · machine · what was said`: when, from where, what. */
internal data class KnowledgeLogLine(val whenText: String, val source: String, val text: String) {
    /** "2026-10-30 23:16" → "10-30 23:16"; the year only when it is not this one. */
    fun shortWhen(today: LocalDate = LocalDate.now()): String {
        val year = "${today.year}-"
        return if (whenText.startsWith(year)) whenText.removePrefix(year) else whenText
    }

    companion object {
        fun parse(raw: String): KnowledgeLogLine {
            var text = raw.trim()
            if (text.startsWith("- ")) text = text.removePrefix("- ")
            val parts = text.split(" · ")
            return if (parts.size >= 2 && parts[0].firstOrNull()?.isDigit() == true)
                KnowledgeLogLine(parts[0], if (parts.size > 2) parts.subList(1, parts.size - 1).joinToString(" · ") else "", parts.last())
            else KnowledgeLogLine("", "", text)
        }
    }
}

/** Newest first, as the page shows the timeline. */
internal fun timeline(log: List<String>?): List<KnowledgeLogLine> = log.orEmpty().asReversed().map(KnowledgeLogLine::parse)

/** A summary's leading `# Name` line is the page title already. */
internal fun dropTitle(markdown: String): String = if (!markdown.startsWith("# ")) markdown else markdown.substringAfter('\n', "")

/**
 * The profile without what the header already shows: its title, the one line, and the line of a person's generated
 * profile that only repeats the tagging ("importance P0 · relevance to me 95 · ..."). Blank when nothing else is left.
 */
internal fun profile(markdown: String?, oneLine: String?): String {
    val one = oneLine?.trim().orEmpty()
    return dropTitle(markdown.orEmpty()).lines().filter { line ->
        val text = line.trim()
        !(text.startsWith("重要级 ") && "相关性" in text) && (one.isEmpty() || text != one) // i18n-ignore: the hub's generated profile line
    }.joinToString("\n").trim()
}

/** Related entries by kind, in the list's order. */
internal fun linkGroups(links: List<KnowledgeEntry>?): List<Pair<KnowledgeKind, List<KnowledgeEntry>>> {
    val byKind = links.orEmpty().distinctBy { it.key }.groupBy { it.kind }
    return KnowledgeKind.entries.mapNotNull { kind -> byKind[kind.wire]?.takeIf { it.isNotEmpty() }?.let { kind to it } }
}

/** A project's first code directory (`<machine>:<path>`) as (machine, path), to open an Agent there. */
internal fun firstDir(dirs: List<String>): Pair<String, String>? = dirs.firstNotNullOfOrNull { dir ->
    dir.indexOf(':').takeIf { it > 0 }?.let { dir.substring(0, it) to dir.substring(it + 1) }?.takeIf { it.second.isNotEmpty() }
}

/** `<machine>:<path>` split for display: the machine, and the path's last folders (which tell projects apart). */
internal fun dirParts(dir: String): Pair<String?, String> {
    val colon = dir.indexOf(':')
    return if (colon <= 0) null to pathTail(dir) else dir.substring(0, colon) to pathTail(dir.substring(colon + 1))
}

internal fun pathTail(path: String, keep: Int = 3): String {
    val parts = path.trimEnd('/').split('/')
    return if (parts.size <= keep + 1) path else "…/" + parts.takeLast(keep).joinToString("/")
}

/** The tag editor's text as tags: split on any whitespace, empty ones dropped. */
internal fun parseTags(text: String): List<String> = text.split(Regex("\\s+")).filter { it.isNotEmpty() }

/**
 * Only live sessions that are not Persona itself can work on a project. A subtask the Agents list folds under its parent
 * is no row of its own while that parent is listed here too, as in the main chat's dock (#4769, `foldedSubtasks`).
 */
internal fun projectAgents(agents: List<Session>?): List<Session> {
    val live = agents.orEmpty().filter { !it.assistant && it.isLive }
    val listed = live.mapTo(HashSet()) { it.key.path }
    val folded = foldedSubtasks(agents.orEmpty().map(::AgentSession)) { it.id in listed }
    return live.filterNot { it.key.path in folded }
}

internal fun KnowledgeDetail.entry(): KnowledgeEntry {
    val recording = kind == KnowledgeKind.Recordings.wire
    // A recording's title (when the summary gave it one) reads better than its generated name.
    val name = (if (recording) meta.string("title").ifBlank { meta.string("name") } else meta.string("name").ifBlank { meta.string("title") })
        .ifBlank { id }
    val extra = when (kind) {
        KnowledgeKind.Projects.wire -> "${meta.string("kind").ifBlank { "short" }} · ${meta.string("status").ifBlank { "active" }}"
        KnowledgeKind.Recordings.wire -> meta.string("device")
        else -> meta.string("kind")
    }
    // A recording lists as when it was made, as the hub's list does.
    val updated = if (recording) meta.string("started_at")
        else meta.string("updated").ifBlank { meta.string("last_at").ifBlank { meta.string("last_seen") } }
    return KnowledgeEntry(kind, id, name,
        meta.string("one_line").ifBlank { meta.string("note") }, meta.string("importance"),
        (meta?.get("relevance") as? JsonPrimitive)?.intOrNull ?: 0, meta.strings("tags"), updated, extra)
}

internal val KnowledgeEntry.isRecording: Boolean get() = kind == KnowledgeKind.Recordings.wire

/** Where a recording was made: its `extra` on a list, `device` in its meta. */
internal enum class RecordingDevice { Phone, Watch }

internal fun recordingDevice(value: String?): RecordingDevice? = when (value?.trim()) {
    "phone" -> RecordingDevice.Phone
    "watch" -> RecordingDevice.Watch
    else -> null
}

/** A mark set while recording, [at] seconds from the start; [label] when one was written. */
internal data class RecordingBookmark(val at: Double, val label: String?)

/** A recording's length split for display: hours and minutes past an hour, minutes and seconds past a minute. */
internal sealed interface RecordingLength {
    data class Hours(val hours: Long, val minutes: Long) : RecordingLength
    data class Minutes(val minutes: Long, val seconds: Long) : RecordingLength
    data class Seconds(val seconds: Long) : RecordingLength

    companion object {
        fun of(seconds: Double): RecordingLength {
            val total = seconds.coerceAtLeast(0.0).roundToLong()
            return when {
                total >= 3600 -> Hours(total / 3600, total % 3600 / 60)
                total >= 60 -> Minutes(total / 60, total % 60)
                else -> Seconds(total)
            }
        }
    }
}

/**
 * What a recording's page shows above its transcript, read from its meta.json (docs/assistant/knowledge.md "Recordings"). A meta
 * without `complete` is from a finished recording; a missing `duration` is measured from `ended_at`.
 */
internal data class RecordingFacts(
    val startedAt: Instant?,
    val duration: Double?,
    val device: String,
    val language: String,
    val complete: Boolean,
    val bookmarks: List<RecordingBookmark>,
) {
    val length: RecordingLength? get() = duration?.takeIf { it > 0 }?.let(RecordingLength::of)

    /** The wall-clock moment [offset] seconds into the recording; null without a start time. */
    fun clock(offset: Double): Instant? = startedAt?.plusMillis((offset.coerceAtLeast(0.0) * 1000).roundToLong())

    companion object {
        fun of(meta: JsonObject?): RecordingFacts {
            val started = instant(meta.string("started_at"))
            val duration = (meta?.get("duration") as? JsonPrimitive)?.doubleOrNull
                ?: instant(meta.string("ended_at"))?.let { end -> started?.let { (end.toEpochMilli() - it.toEpochMilli()) / 1000.0 } }
            val bookmarks = (meta?.get("bookmarks") as? JsonArray).orEmpty().mapNotNull { item ->
                val mark = item as? JsonObject ?: return@mapNotNull null
                val at = (mark["at"] as? JsonPrimitive)?.doubleOrNull ?: return@mapNotNull null
                RecordingBookmark(at, mark.string("label").trim().takeIf { it.isNotEmpty() })
            }.sortedBy { it.at }
            return RecordingFacts(started, duration, meta.string("device"), meta.string("language"),
                (meta?.get("complete") as? JsonPrimitive)?.booleanOrNull ?: true, bookmarks)
        }

        private fun instant(value: String): Instant? =
            value.takeIf { it.isNotBlank() }?.let { runCatching { OffsetDateTime.parse(it).toInstant() }.getOrNull() }
    }
}

/** The transcript's lines that say something, in time order. */
internal fun transcript(segments: List<KnowledgeSegment>?): List<KnowledgeSegment> =
    segments.orEmpty().filter { it.text.isNotBlank() }.sortedBy { it.start }

internal fun JsonObject?.string(key: String): String = (this?.get(key) as? JsonPrimitive)?.contentOrNull.orEmpty()
internal fun JsonObject?.strings(key: String): List<String> = (this?.get(key) as? JsonArray)
    ?.mapNotNull { (it as? JsonPrimitive)?.contentOrNull }.orEmpty()

/** The tagging a form starts with: the entry's own, or P2 for an entry the hub has not ranked yet. */
internal data class KnowledgeTagForm(
    val importance: String,
    val relevance: Int,
    val tags: String,
    val projectKind: String,
    val status: String,
) {
    fun update(isProject: Boolean): KnowledgeUpdate = KnowledgeUpdate(importance, relevance.coerceIn(0, 100), parseTags(tags),
        status = if (isProject) status else null, projectKind = if (isProject) projectKind else null)

    companion object {
        fun of(entry: KnowledgeEntry) = KnowledgeTagForm(
            importance = entry.importance?.takeIf { it in IMPORTANCE } ?: "P2",
            // Kept as it is until the slider moves (then in steps of 5), so saving other fields does not change it.
            relevance = entry.relevance.coerceIn(0, 100),
            tags = entry.tags.orEmpty().joinToString(" "),
            projectKind = entry.projectKind,
            status = entry.projectStatus,
        )
        val IMPORTANCE = listOf("P0", "P1", "P2", "P3")
        val PROJECT_KINDS = listOf("short" to R.string.knowledge_kind_short, "long" to R.string.knowledge_kind_long)
        val PROJECT_STATUSES = listOf("active" to R.string.knowledge_status_active, "paused" to R.string.knowledge_status_paused,
            "done" to R.string.knowledge_status_done, "ignored" to R.string.knowledge_status_ignored)
    }
}

/**
 * The entry as context for the main chat: [kindName] (the kind in the app's language) and the name title it; the body
 * is for the model and keeps the knowledge base's own words.
 */
internal fun knowledgeContext(detail: KnowledgeDetail, kindName: String): String {
    fun attribute(value: String) = value.replace("&", "&amp;").replace("\"", "&quot;")
        .replace("<", "&lt;").replace(">", "&gt;")
    val title = "$kindName · ${detail.entry().name}"
    val text = buildString {
        append(detail.summary.orEmpty().ifBlank { detail.entry().oneLine.orEmpty() })
        detail.log.orEmpty().takeLast(10).takeIf { it.isNotEmpty() }?.let {
            append("\n\n最近时间线：\n").append(it.joinToString("\n")) // i18n-ignore: context for the model
        }
        detail.meta.strings("dirs").takeIf { it.isNotEmpty() }?.let {
            append("\n\n代码目录：\n").append(it.joinToString("\n")) // i18n-ignore: context for the model
        }
        append("\n\n知识库目录：").append(detail.path) // i18n-ignore: context for the model
    }.let(::contextBody)
        .replace("<persona-context", "&lt;persona-context")
    return "<persona-context kind=\"context\" title=\"${attribute(title)}\" url=\"\">\n$text\n</persona-context>"
}

/** What "Start agent" hands the new-Agent page: where to start (a project's first code directory) and the first message. */
data class KnowledgeAgentStart(val machineId: String?, val cwd: String?, val prompt: String)

internal fun agentStart(detail: KnowledgeDetail, kindName: String): KnowledgeAgentStart {
    val dir = firstDir(detail.meta.strings("dirs"))
    return KnowledgeAgentStart(dir?.first, dir?.second, knowledgeContext(detail, kindName) + "\n\n")
}
