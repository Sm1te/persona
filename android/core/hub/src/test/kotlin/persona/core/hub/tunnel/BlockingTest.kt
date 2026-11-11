package persona.core.hub.tunnel

import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.Executor

/** #3844: work handed to another thread for a caller that is gone by then is not started (a superseded start does not dial). */
class BlockingTest {
    /** Runs what it is given only when [drain] is called, so the order of cancel and pickup is fixed. */
    private class Queue : Executor {
        val tasks = ArrayDeque<Runnable>()
        override fun execute(command: Runnable) { tasks += command }
        fun drain() { while (tasks.isNotEmpty()) tasks.removeFirst().run() }
    }

    @Test fun aCallerCancelledBeforeThePickupNeverStartsTheWork() = runBlocking(Dispatchers.Unconfined) {
        val queue = Queue()
        var dialled = 0
        var interrupted = 0
        val call = async(start = CoroutineStart.UNDISPATCHED) { blocking({ interrupted++ }, executor = queue) { dialled++ } }
        call.cancel()
        queue.drain()
        assertEquals("the dial never ran", 0, dialled)
        assertEquals("the interrupt still runs", 1, interrupted)
        assertTrue(call.isCancelled)
    }

    @Test fun aWaitingCallerGetsTheResult() = runBlocking(Dispatchers.Unconfined) {
        val queue = Queue()
        val call = async(start = CoroutineStart.UNDISPATCHED) { blocking({}, executor = queue) { 42 } }
        queue.drain()
        yield()
        assertEquals(42, call.await())
    }
}
