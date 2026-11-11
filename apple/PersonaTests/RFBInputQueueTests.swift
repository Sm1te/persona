import Testing
@testable import Persona

/// Keys and pastes to the Mac's Screen Sharing in the order they were made (#2374, RFBInputQueue): a paste's clipboard
/// write takes a while (the hub), and nothing made after it may overtake its ⌘V; a failed write sends no ⌘V.
@MainActor struct RFBInputQueueTests {
    /// What went out, as text: a key down as its character (⌘V as "⌘V"), key ups left out; clipboard writes as [text]
    /// when they are answered. Writes wait until `answer()`, one at a time in the order made.
    @MainActor final class Wire {
        var out: [String] = []
        /// Writes that fail (by their text).
        var failing: Set<String> = []
        /// Writes started and not answered yet: more than one means two pastes overlap.
        private(set) var waiting: [(String, CheckedContinuation<Bool, Never>)] = []
        private(set) var mostWaiting = 0
        private var command = false
        /// How long a new session drops keys (none unless a test signs in).
        var settle: Duration = .zero

        lazy var queue = RFBInputQueue(settle: settle, send: { [unowned self] e in self.sent(e) }, write: { [unowned self] text in
            await withCheckedContinuation { k in
                self.waiting.append((text, k))
                self.mostWaiting = max(self.mostWaiting, self.waiting.count)
            }
        })

        private func sent(_ e: RFBInputQueue.Event) {
            if e.keysym == RFBSession.command { command = e.down; return }
            guard e.down else { return }
            if command, e.keysym == 0x76 { out.append("⌘V"); return }
            out.append(String(Character(Unicode.Scalar(e.keysym)!)))
        }

        /// Lets the queue run until a write is waiting (or nothing is left), then answers the oldest.
        func answer() async {
            for _ in 0..<1000 {
                if !waiting.isEmpty || queue.idle { break }
                await Task.yield()
            }
            // A few more turns: a second write started meanwhile (overlap) shows up in mostWaiting.
            for _ in 0..<20 { await Task.yield() }
            guard !waiting.isEmpty else { return }
            let (text, k) = waiting.removeFirst()
            out.append("[\(text)]")
            k.resume(returning: !failing.contains(text))
        }

        func drain() async {
            for _ in 0..<100 where !queue.idle { await answer() }
        }
    }

    /// Polls (on the main actor, so the queue runs meanwhile) until `done` or two seconds pass.
    private func eventually(_ done: () -> Bool) async {
        for _ in 0..<100 where !done() { try? await Task.sleep(for: .milliseconds(20)) }
    }

    /// Signed in a moment ago (#3075): the Mac would drop keys now, so they wait out the session's first seconds, in
    /// order, and keys after that go out at once.
    @Test func keysWaitOutANewSessionsFirstSeconds() async {
        let w = Wire()
        w.settle = .milliseconds(300)
        w.queue.signedIn()
        w.queue.type("ab") {}
        w.queue.press(0x63)
        #expect(w.out.isEmpty)
        #expect(!w.queue.idle)
        await eventually { w.out.count == 3 }
        #expect(w.out == ["a", "b", "c"])
        await eventually { w.queue.idle }
        w.queue.press(0x64)
        #expect(w.out == ["a", "b", "c", "d"])
    }

    /// A paste just after signing in: its clipboard write starts at once, its ⌘V only once the Mac takes keys.
    @Test func aPastesCommandVWaitsOutANewSessionButItsWriteDoesNot() async {
        let w = Wire()
        w.settle = .milliseconds(300)
        w.queue.signedIn()
        w.queue.paste("x")
        await w.answer()
        #expect(w.out == ["[x]"])
        await eventually { w.out.count == 2 }
        #expect(w.out == ["[x]", "⌘V"])
    }

    /// Signed in again (a reconnect) while keys wait: they wait for the new session, not the old one.
    @Test func aNewSignInWhileWaitingWaitsForTheNewSession() async {
        let w = Wire()
        w.settle = .milliseconds(600)
        w.queue.signedIn()
        w.queue.type("a") {}
        try? await Task.sleep(for: .milliseconds(300))
        w.queue.signedIn()
        try? await Task.sleep(for: .milliseconds(450))
        #expect(w.out.isEmpty, "the first session's 600 ms are over, the new one's are not")
        await eventually { w.out == ["a"] }
        #expect(w.out == ["a"])
    }

    /// A pause counts from when the keys before it went out (One-Tap Unlock: a Shift, time for the lock screen to wake,
    /// then the password), just after signing in too, when those keys wait out the session's first seconds.
    @Test func aPauseCountsFromWhenTheKeysBeforeItWentOut() async {
        let w = Wire()
        w.settle = .milliseconds(300)
        w.queue.signedIn()
        let start = ContinuousClock.now
        w.queue.press(0x78)
        w.queue.pause(.milliseconds(400))
        w.queue.press(0x79)
        await eventually { w.out == ["x"] }
        let x = ContinuousClock.now - start
        await eventually { w.out == ["x", "y"] }
        let y = ContinuousClock.now - start
        #expect(x >= .milliseconds(300), "x waited out the session's first 300 ms: \(x)")
        #expect(y - x >= .milliseconds(350), "y came the pause after x went out: \(x) then \(y)")
    }

    @Test func keysGoOutAtOnceWithNothingPending() {
        let w = Wire()
        w.queue.type("ab") {}
        #expect(w.out == ["a", "b"])
        #expect(w.queue.idle)
    }

    /// 「a你b」: b waits for 「你」's ⌘V instead of going out before it; so does a key pressed after it.
    @Test func typedTextGoesOutInOrder() async {
        let w = Wire()
        w.queue.type("a你b") {}
        w.queue.press(0x63)
        #expect(w.out == ["a"])
        await w.drain()
        #expect(w.out == ["a", "[你]", "⌘V", "b", "c"])
        #expect(w.queue.idle)
    }

    /// 「你好」 then 「，」: the second write waits for the first one's ⌘V (no pasting 「，」 twice, no swapping).
    @Test func pastesInARowDoNotOverlap() async {
        let w = Wire()
        w.queue.type("你好") {}
        w.queue.type("，") {}
        let third = w.queue.paste("吗")
        await w.drain()
        #expect(w.out == ["[你好]", "⌘V", "[，]", "⌘V", "[吗]", "⌘V"])
        #expect(w.mostWaiting == 1)
        #expect(await third.value)
    }

    /// A failed write: no ⌘V (it would paste the Mac's old clipboard), what follows still goes out, and done says so
    /// before it does.
    @Test func aFailedWriteSendsNoCommandV() async {
        let w = Wire()
        w.failing = ["你"]
        let failed = w.queue.paste("你") { ok in w.out.append(ok ? "ok" : "failed") }
        w.queue.type(" 好") {}
        await w.drain()
        #expect(w.out == ["[你]", "failed", " ", "[好]", "⌘V"])
        #expect(await failed.value == false)
    }

    /// A run of failed pastes in typed text tells once; a paste that goes through ends the run, and so does
    /// everything having gone out before the next text.
    @Test func aRunOfFailuresTellsOnce() async {
        let w = Wire()
        w.failing = ["你", "好", "吗"]
        var told = 0
        w.queue.type("你a好") { told += 1 }
        w.queue.type("b吗") { told += 1 }
        await w.drain()
        #expect(w.out == ["[你]", "a", "[好]", "b", "[吗]"])
        #expect(told == 1)

        w.queue.type("你") { told += 1 }
        await w.drain()
        #expect(told == 2)

        w.queue.type("你x吗") { told += 1 }
        w.queue.type("好") { told += 1 }
        await w.drain()
        #expect(told == 3)
    }

    @Test func aPasteThroughEndsTheRun() async {
        let w = Wire()
        w.failing = ["你", "吗"]
        var told = 0
        w.queue.type("你a世b吗") { told += 1 }
        await w.drain()
        #expect(w.out == ["[你]", "a", "[世]", "⌘V", "b", "[吗]"])
        #expect(told == 2)
    }

    /// Once all of it has gone out, keys go out at once again.
    @Test func idleAgainAfterThePasteGoesOut() async {
        let w = Wire()
        w.queue.paste("你")
        await w.drain()
        w.queue.press(0x78)
        #expect(w.out == ["[你]", "⌘V", "x"])
    }
}
