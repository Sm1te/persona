#if DEBUG
// i18n-ignore-file: DEBUG-only made-up recording for screenshots
import Foundation

/// Demo and screenshots use made-up text only and never start the microphone; with a hub paired they sync into its Knowledge as usual (UI tests check this step with the mock hub).
enum RecordingSamples {
    /// Once per launch: the page appears again after its recording was deleted, and must not bring it back.
    nonisolated(unsafe) private static var installed = false

    static func installIfAsked() -> UUID? {
        guard UserDefaults.standard.bool(forKey: "showRecordingSample"), !installed else { return nil }
        installed = true
        let id = UUID(uuidString: "00000000-0000-0000-0000-000000000001")!
        RecordingLibrary.shared.restore(id)
        var entry = RecordingEntry(id: id, createdAt: Date(timeIntervalSince1970: 1_800_000_000), source: .watch,
                                   title: "项目讨论", active: false, expectedSegments: 1)
        entry.segments = [RecordingSegment(index: 0, start: 0, duration: 90, sentences: [
            RecordingSentence(start: 0, end: 12, text: "这次讨论确定三个重点：先验证核心流程，再补充测试，最后检查体验。"),
            RecordingSentence(start: 28, end: 40, text: "下周整理一次进展，把未解决的问题列出来。"),
            RecordingSentence(start: 65, end: 78, text: "需要把今天的结论变成待办，避免遗漏。"),
        ])]
        entry.marks = [28]
        try? RecordingLibrary.shared.save(entry)
        return id
    }

    /// The meeting being recorded (`-showMeetingSample recording|paused|interrupted`).
    static let liveMeeting = UUID(uuidString: "00000000-0000-0000-0000-000000000002")!
    /// A finished meeting, transcribed (`-showMeetingSample minutes|pending` opens it).
    static let pastMeeting = UUID(uuidString: "00000000-0000-0000-0000-000000000003")!

    #if os(iOS)
    /// The lines the `flow` sample hears, one segment of one sentence each, a few seconds apart.
    private static let flowLines = [
        "好，我们开始，今天先看录音入口的进展。",
        "新版入口已经合进主干，开始录音只要一步。",
        "锁屏和灵动岛可以看到时长，也能直接停止。",
        "决定：默认用中文转写，中英混说也要能用。",
        "张三负责周五前补上长录音的真机测试。",
        "还没定的是 Mac 上要不要也支持录音。",
        "下周同一时间再对一次进展，今天就到这里。",
    ]
    nonisolated(unsafe) private static var flowTask: Task<Void, Never>?

    /// `-showMeetingSample flow`: a meeting that records itself with no microphone. The clock and the level move, a line is
    /// heard every few seconds, and Stop, Pause and Bookmark work (`RecordingCapture`'s DEBUG sample paths), so the
    /// simulator can show the whole way from the live sheet through the wrap-up to the minutes (mock hub, `recording_minutes`).
    @MainActor private static func startFlow(_ meeting: RecordingMeeting) {
        RecordingLibrary.shared.restore(liveMeeting)
        var live = RecordingEntry(id: liveMeeting, createdAt: .now, source: .phone, title: "产品周会", active: true)
        live.meeting = meeting
        live.attendance = RecordingAttendance(role: .participant, session: nil, sessionTitle: "Persona", mainPicked: true)
        try? RecordingLibrary.shared.save(live)
        let capture = RecordingCapture.shared
        capture.showSample(liveMeeting, elapsed: 1)
        flowTask?.cancel()
        flowTask = Task { @MainActor in
            var elapsed = 1.0
            var heard = 0
            var lastLine = 0.0
            while !Task.isCancelled, capture.recordingID == liveMeeting {
                try? await Task.sleep(for: .milliseconds(100))
                guard capture.state == .recording else { capture.advanceSample(elapsed: elapsed, level: 0); continue }
                elapsed += 0.1
                // Speech in bursts: a slow swell and a quicker flutter.
                let swell = max(0, sin(elapsed * 0.9)), flutter = 0.5 + 0.5 * sin(elapsed * 9)
                capture.advanceSample(elapsed: elapsed, level: min(1, 0.08 + 0.7 * swell * flutter))
                if elapsed - lastLine >= 6, heard < flowLines.count {
                    lastLine = elapsed
                    let start = max(0, elapsed - 8), line = flowLines[heard], index = heard
                    heard += 1
                    RecordingLibrary.shared.change(liveMeeting) { entry in
                        entry.segments.append(RecordingSegment(index: index, start: start, duration: 6, sentences: [
                            RecordingSentence(start: start, end: start + 5, text: line)]))
                    }
                }
            }
        }
    }
    #endif

    /// `-showMeetingSample recording|paused|interrupted`: a meeting 12:34 into its recording, as the recording page and its
    /// Live Activity show it (no microphone); `listening|taking-part|answered`: the same with Persona attending (#2981);
    /// `room-listening|room-taking-part`: the same as a room meeting (#3862), whose sheet is the meeting page;
    /// `minutes` / `pending`: a finished meeting whose page opens (its minutes from the hub, or still being written). Both
    /// recordings are made up.
    static func showMeetingIfAsked() {
        #if os(iOS)
        guard let mode = UserDefaults.standard.string(forKey: "showMeetingSample") else {
            // A run without them: an earlier run's made-up meetings go, so other tests see only their own recordings.
            for id in [liveMeeting, pastMeeting] where RecordingLibrary.shared.entry(id) != nil { try? RecordingLibrary.shared.remove(id) }
            return
        }
        let start = Date(timeIntervalSince1970: 1_800_003_600)
        let meeting = RecordingMeeting(eventID: "sample-weekly", title: "产品周会", start: start, end: start.addingTimeInterval(3600),
                                       location: "3 楼会议室", attendees: ["张三", "李四", "王五"])
        RecordingLibrary.shared.restore(pastMeeting)
        var past = RecordingEntry(id: pastMeeting, createdAt: start, source: .phone, title: "产品周会", active: false, expectedSegments: 2)
        past.meeting = meeting
        past.segments = [
            RecordingSegment(index: 0, start: 0, duration: 60, sentences: [
                RecordingSentence(start: 2, end: 14, text: "今天先过一下新版录音入口，目标是一步就能开始录。"),
                RecordingSentence(start: 21, end: 38, text: "锁屏和灵动岛要能看到时长，也能直接停止。"),
                RecordingSentence(start: 44, end: 58, text: "决定：默认用中文转写，中英混说也要能用。"),
            ]),
            RecordingSegment(index: 1, start: 60, duration: 60, sentences: [
                RecordingSentence(start: 63, end: 80, text: "张三负责周五前补上长录音的真机测试。"),
                RecordingSentence(start: 86, end: 101, text: "还没定的是 Mac 上要不要也支持录音。"),
                RecordingSentence(start: 105, end: 118, text: "李四负责下周同一时间约大家再对一次进展。"),
            ]),
        ]
        past.marks = [44]
        try? RecordingLibrary.shared.save(past)
        if mode == "minutes" || mode == "pending" { return }
        if mode == "flow" { startFlow(meeting); return }

        RecordingLibrary.shared.restore(liveMeeting)
        var live = RecordingEntry(id: liveMeeting, createdAt: .now.addingTimeInterval(-754), source: .phone, title: "产品周会", active: true)
        live.meeting = meeting
        live.segments = [RecordingSegment(index: 0, start: 0, duration: 30, sentences: [
            RecordingSentence(start: 1, end: 9, text: "好，我们开始，今天先看录音入口的进展。"),
        ])]
        live.marks = [65, 300]
        // `listening` / `taking-part` / `answered`: Persona attends (#2981), listening in, taking part, or taking part with two
        // answers given. The main chat attends, picked for it (no session named, `mainPicked`: never the default, #3862);
        // the made-up meeting's upload tells the hub, as a real one would, and its lines go to the hub's live log, the last
        // one just before the tap (12:34). `room-listening` / `room-taking-part`: the meeting's own room conversation
        // attends instead (#3862), not private.
        let room = ["room-listening", "room-taking-part"].contains(mode)
        if ["listening", "taking-part", "answered"].contains(mode) || room {
            if room {
                live.attendance = RecordingAttendance.room(mode == "room-listening" ? .listener : .participant, private: false, photos: false)
            } else {
                live.attendance = RecordingAttendance(role: mode == "listening" ? .listener : .participant, session: nil,
                                                      sessionTitle: "Persona", mainPicked: true)
            }
            live.segments.append(RecordingSegment(index: 1, start: 720, duration: 30, sentences: [
                RecordingSentence(start: 741, end: 748, text: "上周定的长录音测试谁负责？"),
            ]))
        }
        try? RecordingLibrary.shared.save(live)
        RecordingCapture.shared.showSample(liveMeeting, elapsed: 754, paused: mode == "paused", interrupted: mode == "interrupted")
        if mode == "answered" {
            let first = MeetingAsk.Exchange(recordingID: liveMeeting, at: 612, question: "Mac 上的录音现在谁在跟？", typed: true,
                                            state: .answered("还没有定负责人。上次周会把它列为待定问题，计划下周同一时间再对。"),
                                            sources: ["产品周会 · 2026-11-03 14:00–15:00"])
            let second = MeetingAsk.Exchange(recordingID: liveMeeting, at: 731, question: "上周定的长录音测试谁负责？", typed: false,
                                             state: .answered("上周的周会定了：录音入口只做一步开始，默认用中文转写。长录音的真机测试由张三负责，周五前完成。"),
                                             sources: ["产品周会 · 2026-11-03 14:00–15:00"])
            MeetingAsk.shared.showSample([first, second])
            MeetingSpeaker.shared.showSample(second.id)
        }
        #endif
    }
}
#endif
