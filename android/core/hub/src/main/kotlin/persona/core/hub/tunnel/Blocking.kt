package persona.core.hub.tunnel

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.asExecutor
import kotlinx.coroutines.suspendCancellableCoroutine
import java.util.concurrent.Executor

/**
 * Runs [block] (a dial, a login, a listen) on another thread, so the caller can be cancelled while it waits: [onCancel]
 * then interrupts it, and a result that comes after is handed to [onLate]. A caller cancelled before the thread picks the
 * block up never starts it (#3844): a superseded tunnel start must not dial the server, least of all one whose key
 * changed and is waiting for the user.
 */
internal suspend fun <T> blocking(
    onCancel: () -> Unit,
    onLate: (T) -> Unit = { (it as? AutoCloseable)?.close() },
    executor: Executor = Dispatchers.IO.asExecutor(),
    block: () -> T,
): T = suspendCancellableCoroutine { continuation ->
    continuation.invokeOnCancellation { executor.execute(onCancel) }
    executor.execute {
        if (!continuation.isActive) return@execute
        val result = runCatching(block)
        result.fold(
            { value -> continuation.resume(value) { _, late, _ -> onLate(late) } },
            { error -> if (continuation.isActive) continuation.resumeWith(Result.failure(error)) },
        )
    }
}
