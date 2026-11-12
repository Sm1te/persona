#if DEBUG
// i18n-ignore-file: DEBUG-only made-up recording for screenshots
import Foundation
#if os(iOS)
import UIKit
#endif

/// Demo and screenshots use made-up text only and never start the microphone; with a hub paired they sync into its Knowledge as usual (UI tests check this step with the mock hub).
enum RecordingSamples {
    /// Once per launch: the page appears again after its recording was deleted, and must not bring it back.
    nonisolated(unsafe) private static var installed = false

    /// `-showRecordingSample second`: the same recording under another id, for a test that deletes one (the hub keeps the first's tombstone).
    static let secondSample = UUID(uuidString: "00000000-0000-0000-0000-000000000004")!

    static func installIfAsked() -> UUID? {
        let asked = UserDefaults.standard.string(forKey: "showRecordingSample")
        guard !installed, UserDefaults.standard.bool(forKey: "showRecordingSample") || asked == "second" else { return nil }
        installed = true
        let id = asked == "second" ? secondSample : UUID(uuidString: "00000000-0000-0000-0000-000000000001")!
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
    /// `room-whiteboard`: `room-taking-part` started with photos, which adds a made-up whiteboard photo as the camera
    /// would (#3862 phase 3); `minutes` / `pending`: a finished meeting whose page opens (its minutes from the hub, or
    /// still being written). Both recordings are made up.
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
        // attends instead (#3862), not private; `room-whiteboard` takes part and takes photos.
        let room = ["room-listening", "room-taking-part", "room-whiteboard"].contains(mode)
        let photos = mode == "room-whiteboard"
        if ["listening", "taking-part", "answered"].contains(mode) || room {
            if room {
                live.attendance = RecordingAttendance.room(mode == "room-listening" ? .listener : .participant, private: false, photos: photos)
            } else {
                live.attendance = RecordingAttendance(role: mode == "listening" ? .listener : .participant, session: nil,
                                                      sessionTitle: "Persona", mainPicked: true)
            }
            live.segments.append(RecordingSegment(index: 1, start: 720, duration: 30, sentences: [
                RecordingSentence(start: 741, end: 748, text: "上周定的长录音测试谁负责？"),
            ]))
        }
        if photos {
            // An earlier run's photos not sent yet stay behind: this run's whiteboard is the only one.
            try? FileManager.default.removeItem(at: RecordingLibrary.shared.folder(liveMeeting).appending(path: MeetingPhotos.listFile))
        }
        try? RecordingLibrary.shared.save(live)
        RecordingCapture.shared.showSample(liveMeeting, elapsed: 754, paused: mode == "paused", interrupted: mode == "interrupted")
        if photos { Task { await addWhiteboard() } }
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

    #if os(iOS)
    /// `room-whiteboard`: once the hub has the meeting with its room (the first upload's `200`, 10 s at most), a made-up
    /// whiteboard goes the way a camera photo goes (`MeetingPhotos.add`), so the real pipeline runs: downscaled, read on
    /// the phone, uploaded, sent into the room, and the room's note.
    private static func addWhiteboard() async {
        for _ in 0..<50 {
            if let entry = RecordingLibrary.shared.entry(liveMeeting), RecordingSync.shared.attendanceSettled(entry) { break }
            try? await Task.sleep(for: .milliseconds(200))
        }
        let at = RecordingCapture.shared.timeline
        guard let jpeg = await Task.detached(priority: .userInitiated, operation: { RecordingSamples.whiteboard() }).value,
              let entry = RecordingLibrary.shared.entry(liveMeeting) else { return }
        MeetingPhotos.shared.add(.camera(jpeg), to: entry, at: at)
    }

    /// A whiteboard as a phone camera would hand it over: 2000 × 1500 px, white, a few lines in a dark marker, a box drawn
    /// around the decision. Drawn off the main actor (`UIGraphicsImageRenderer` is safe there).
    nonisolated private static func whiteboard() -> Data? {
        let size = CGSize(width: 2000, height: 1500)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        let renderer = UIGraphicsImageRenderer(size: size, format: format)
        let ink = UIColor(red: 0.1, green: 0.14, blue: 0.32, alpha: 1)
        let red = UIColor(red: 0.72, green: 0.12, blue: 0.12, alpha: 1)
        let lines: [(text: String, size: CGFloat, color: UIColor)] = [
            ("Q3 预算", 150, ink),
            ("导出上限 4 MB", 120, red),
            ("回归测试：张三 周五", 120, ink),
            ("Mac 录音：下周再定", 110, ink),
        ]
        return renderer.jpegData(withCompressionQuality: 0.9) { context in
            UIColor.white.setFill()
            context.fill(CGRect(origin: .zero, size: size))
            var y: CGFloat = 170
            for line in lines {
                let attributes: [NSAttributedString.Key: Any] = [.font: UIFont.systemFont(ofSize: line.size, weight: .bold),
                                                                 .foregroundColor: line.color]
                (line.text as NSString).draw(at: CGPoint(x: 180, y: y), withAttributes: attributes)
                y += line.size * 1.9
            }
            // The decision boxed in red, as people mark one on a board.
            let box = UIBezierPath(rect: CGRect(x: 140, y: 170 + 150 * 1.9 - 30, width: 1120, height: 200))
            box.lineWidth = 12
            red.setStroke()
            box.stroke()
        }
    }
    #endif
}
#endif
