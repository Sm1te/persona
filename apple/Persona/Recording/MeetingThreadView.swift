#if os(iOS)
import SwiftUI

/// The meeting page's one thread (#3862, docs/assistant/meeting-minutes.md「One thread」). While the meeting records: the
/// room conversation (Persona joining, the lines it was fed folded to "N lines from the meeting", the questions and its
/// answers) with this phone's own questions the room does not hold yet, failed or answered from the meeting's material;
/// with Just Me on, the owner's private questions and the main chat's answers to them instead. After the meeting: the room
/// (with the asks answered from the meeting's material where they were asked), **After the Meeting**, then the follow-up
/// conversation (its first message's material folded) and the messages on their way to it. It subscribes to the
/// conversations it shows while it is on screen and derives its rows from their items only when those change
/// (`MeetingThread`); a reply being written redraws only its own row (`MeetingLiveAnswer`).
///
/// Photos (phase 3, `MeetingPhotos`): one the room got sits where its message is, with its state, the room's note under
/// it, and Photo removed once the hub no longer lists it; the others (not sent to the room, its message not among the
/// room's pages loaded, still on their way from this phone) follow the room's rows. A long press deletes one, a tap on
/// its thumbnail opens the app's image viewer. The page's `MeetingConversation.watch` reads the photos as it reads the
/// meeting. The thread reads only the order of its recording's photos (`MeetingPhotos.Album.order`); each photo row is its
/// own view that reads the rest, so a photo's progress never redraws the room's rows.
struct MeetingThreadView: View {
    /// What the thread shows, for the page around it: its rows' ids in order (Jump to Latest counts the new ones), and the
    /// exchanges it draws as `MeetingExchangeRow` cards, which carry their own Stop Speaking.
    struct Shown: Equatable {
        var ids: [String] = []
        var exchangeRows: Set<UUID> = []
    }

    var entry: RecordingEntry
    var mode: MeetingPage.Mode
    var justMe: Bool
    var exchanges: [MeetingAsk.Exchange]
    @Binding var shown: Shown

    init(entry: RecordingEntry, mode: MeetingPage.Mode, justMe: Bool, exchanges: [MeetingAsk.Exchange],
         shown: Binding<Shown> = .constant(Shown())) {
        self.entry = entry
        self.mode = mode
        self.justMe = justMe
        self.exchanges = exchanges
        _shown = shown
    }

    @Environment(AppModel.self) private var model
    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var conversation = MeetingConversation.shared
    /// This view's hold on the conversations it shows (`AppModel.subscribe`).
    @State private var owner = UUID()
    @State private var visible = false
    @State private var subscribed: Set<SessionKey> = []
    @State private var roomEntries: [MeetingThread.Entry] = []
    /// What the room's part of the thread draws: its entries, and after the meeting the asks answered from the
    /// meeting's material where they were asked (or every answered ask when the room never came up).
    @State private var roomRows: [MeetingThread.RoomRow] = []
    @State private var followupEntries: [MeetingThread.Entry] = []
    /// Each Just Me question's answer in the main chat, read from its items when they change.
    @State private var replies: [UUID: MeetingThread.JustMeAnswer] = [:]
    /// The folded rows opened, and the material shown whole.
    @State private var open: Set<String> = []
    @State private var whole: Set<String> = []
    /// The follow-up's answers already there: a new one is announced; nil until its items first came.
    @State private var followupAnswers: Int?
    /// The photos the room's thread holds (its `photo-<id>` messages), and those drawn after the room's rows, in order:
    /// derived when the room's entries or the recording's photos change, never in `body`.
    @State private var roomPhotos: Set<String> = []
    @State private var apart: [String] = []
    /// The photo whose Delete Photo waits for its confirmation.
    @State private var confirming: MeetingPhotoItem?
    /// The photo open in the viewer.
    @State private var viewing: MeetingPhotoItem?

    var body: some View {
        let meeting = conversation.meeting(entry.id)
        let roomKey = conversation.roomKey(entry.id)
        let followupKey = mode == .after ? conversation.followupKey(entry.id) : nil
        let local = localExchanges(hasRoom: roomKey != nil)
        let mine = showsJustMe ? conversation.justMe(for: entry.id) : []
        let sends = mode == .after ? conversation.followups(for: entry.id) : []
        let report = Shown(ids: rowIDs(local: local, mine: mine, sends: sends),
                           exchangeRows: showsJustMe ? [] : Set(local.map(\.id)))
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            if showsJustMe {
                justMeRows(mine)
            } else {
                roomPart(roomKey: roomKey, meeting: meeting, local: local)
                if mode == .after { followupRows(followupKey, sends: sends) }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        // Rows that land in one change arrive as one group.
        .animation(reduceMotion ? nil : Theme.Motion.reveal, value: report.ids)
        .onTabVisibility(appear: {
            visible = true
            resubscribe()
        }, disappear: {
            visible = false
            resubscribe()
        })
        .onChange(of: wanted) { resubscribe() }
        .onChange(of: RoomInput(items: signature(roomKey), asks: meeting.asks, ended: !entry.active), initial: true) { rebuildRoom() }
        .onChange(of: signature(followupKey), initial: true) { rebuildFollowup() }
        .onChange(of: sends) { markDelivered() }
        .onChange(of: JustMeInput(asks: mine, chats: chatKeys(mine).map { signature($0) }), initial: true) { rebuildReplies() }
        // None while Just Me shows instead: photos are the meeting's material.
        .onChange(of: PhotoInput(order: showsJustMe ? [] : album.order, inRoom: roomPhotos), initial: true) { _, input in
            let next = input.order.filter { !input.inRoom.contains($0) }
            if apart != next { apart = next }
        }
        .onChange(of: report, initial: true) { _, now in shown = now }
        .confirmationDialog("Delete This Photo?", isPresented: confirmingBinding, titleVisibility: .visible, presenting: confirming) { item in
            Button("Delete Photo", role: .destructive) { delete(item) }
                .accessibilityIdentifier("meeting-photo-delete-confirm")
        } message: { item in
            // What the room already saw stays with it; the hub tells it not to use the photo again. One that never
            // reached the hub as far as this phone knows promises nothing: the delete reads the hub's list first.
            if item.listed?.forwarded == true {
                Text("Persona already saw it. It is told not to use it again, and it leaves the minutes.")
            } else if item.listed != nil {
                Text("It leaves the meeting's material and the minutes.")
            }
        }
        .fullScreenCover(item: $viewing) { item in MeetingPhotoViewer(recording: entry.id, item: item) }
    }

    private var showsJustMe: Bool { mode == .live && justMe }

    /// The recording's photos as the pages draw them; the thread itself reads only their order.
    private var album: MeetingPhotos.Album { MeetingPhotos.shared.album(entry.id) }

    // MARK: Subscriptions

    /// The conversations to follow: the room (unless Just Me hides it while recording), the follow-up after the meeting,
    /// and the main chat of the Just Me questions sent from here.
    private var wanted: Set<SessionKey> {
        var keys: Set<SessionKey> = []
        if !showsJustMe, let room = conversation.roomKey(entry.id) { keys.insert(room) }
        if mode == .after, let followup = conversation.followupKey(entry.id) { keys.insert(followup) }
        if showsJustMe { keys.formUnion(chatKeys(conversation.justMe(for: entry.id))) }
        return keys
    }

    /// Subscribes to what is wanted while on screen and lets go of the rest (all of it once the page is gone), so the
    /// app's reconnect catch-up never carries a conversation nobody looks at.
    private func resubscribe() {
        let want: Set<SessionKey> = visible ? wanted : []
        for key in subscribed.subtracting(want) { model.unsubscribe(key, by: owner) }
        for key in want.subtracting(subscribed) { model.subscribe(key, by: owner) }
        subscribed = want
    }

    private func chatKeys(_ asks: [MeetingConversation.JustMe]) -> [SessionKey] {
        var keys: [SessionKey] = []
        for ask in asks {
            if case .sent(let key, _) = ask.state, !keys.contains(key) { keys.append(key) }
        }
        return keys
    }

    /// What a conversation's items are now, cheaply: rows are derived again only when it changes.
    private func signature(_ key: SessionKey?) -> String {
        guard let key else { return "" }
        let items = model.chat(key).items
        return "\(key.path)|\(items.count)|\(items.first?.id ?? "")|\(items.last?.id ?? "")"
    }

    // MARK: Deriving rows

    private func rebuildRoom() {
        let asks = conversation.meeting(entry.id).asks
        guard let key = conversation.roomKey(entry.id) else {
            roomEntries = []
            roomRows = mode == .after ? MeetingThread.pairs(asks, materialOnly: false).map { .ask($0) } : []
            if !roomPhotos.isEmpty { roomPhotos = [] }
            return
        }
        // While it records the meeting hasn't ended: a line from it with nothing after (a withdrawn photo's) is no end.
        roomEntries = MeetingThread.room(model.chat(key).items, recordingID: entry.id, asks: asks.map { MeetingThread.AskInfo($0) },
                                         ended: !entry.active)
        roomRows = mode == .after ? MeetingThread.merged(roomEntries, asks: asks) : roomEntries.map { .entry($0) }
        let photos = Set(MeetingThread.photoIDs(roomEntries))
        if roomPhotos != photos { roomPhotos = photos }
    }

    private func rebuildFollowup() {
        guard mode == .after, let key = conversation.followupKey(entry.id) else {
            followupEntries = []
            return
        }
        let store = model.chat(key)
        let built = MeetingThread.followup(store.items)
        // A new answer is announced (the microphone is closed after the meeting); the ones there when it first loads
        // are not news.
        let answers = built.compactMap { entry -> String? in
            if case .answer(_, _, let text) = entry { return text }
            return nil
        }
        if store.loadedOnce {
            if let before = followupAnswers, answers.count > before, let latest = answers.last {
                AccessibilityNotification.Announcement(latest).post()
            }
            followupAnswers = answers.count
        }
        followupEntries = built
        markDelivered()
    }

    /// A follow-up message on its way is shown until its own message is in the follow-up's transcript, matched by its
    /// id only (two messages may say the same words).
    private func markDelivered() {
        guard mode == .after, let key = conversation.followupKey(entry.id) else { return }
        let arrived = Set(model.chat(key).items.compactMap { $0.clientMsgID?.lowercased() })
        guard !arrived.isEmpty else { return }
        for send in conversation.followups(for: entry.id)
        where MeetingConversation.clientMsgIDs(for: send).contains(where: { arrived.contains($0.lowercased()) }) {
            conversation.delivered(send.id)
        }
    }

    /// Each sent Just Me question's answer: the main chat's assistant text after its message, up to the end of its turn
    /// (the next user text of any kind: what the hub puts in, a task's notice or a scheduled run, is another turn).
    private func rebuildReplies() {
        var out: [UUID: MeetingThread.JustMeAnswer] = [:]
        for ask in conversation.justMe(for: entry.id) {
            guard case .sent(let key, let clientMsgID) = ask.state,
                  let answer = MeetingThread.justMeAnswer(model.chat(key).items, after: clientMsgID) else { continue }
            out[ask.id] = answer
        }
        replies = out
    }

    /// This phone's questions the thread draws itself: while recording, with a room thread, those it does not hold yet,
    /// those that failed and those answered from the meeting's material; without one, all of them.
    private func localExchanges(hasRoom: Bool) -> [MeetingAsk.Exchange] {
        guard mode == .live else { return [] }
        guard hasRoom else { return exchanges }
        let held = MeetingThread.askIDs(roomEntries)
        return exchanges.filter { exchange in
            if case .failed = exchange.state { return true }
            if exchange.via == "material" { return true }
            return !held.contains(exchange.id.uuidString.lowercased())
        }
    }

    /// The rows' ids in order, for Jump to Latest's count.
    private func rowIDs(local: [MeetingAsk.Exchange], mine: [MeetingConversation.JustMe],
                        sends: [MeetingConversation.FollowupSend]) -> [String] {
        if showsJustMe {
            return mine.flatMap { ask in replies[ask.id]?.text == nil ? ["me-\(ask.id)"] : ["me-\(ask.id)", "me-\(ask.id)-answer"] }
        }
        var ids = roomRows.map(\.id)
        ids += apart.map { "photo-" + $0 }
        ids += local.map { "exchange-\($0.id)" }
        ids += followupEntries.map(\.id)
        ids += sends.map { "send-\($0.id)" }
        return ids
    }

    /// Whether the room thread holds a question or an answer yet (its header and fed lines don't count).
    private static func asked(_ entries: [MeetingThread.Entry]) -> Bool {
        entries.contains { entry in
            switch entry {
            case .question, .answer, .heard: true
            default: false
            }
        }
    }

    // MARK: The room

    @ViewBuilder private func roomPart(roomKey: SessionKey?, meeting: MeetingConversation.Meeting,
                                       local: [MeetingAsk.Exchange]) -> some View {
        let role = entry.attendance?.role ?? .off
        let ready = meeting.room?.known == .ready
        if let roomKey {
            let store = model.chat(roomKey)
            if store.hasMore { earlier(roomKey, identifier: "meeting-thread-earlier") }
            ForEach(roomRows) { item in roomRow(item, roomReady: ready) }
            ForEach(apart, id: \.self) { photoID in photoRow(photoID, inRoom: false, roomReady: ready) }
            MeetingLiveAnswer(store: store, markdown: false, identifier: "meeting-thread-answer-live")
        } else {
            if mode == .after { ForEach(roomRows) { item in roomRow(item, roomReady: ready) } }
            ForEach(apart, id: \.self) { photoID in photoRow(photoID, inRoom: false, roomReady: ready) }
        }
        if mode == .live {
            if roomKey == nil, role.attends, meeting.room?.known == .starting {
                HStack(spacing: Theme.Space.s) {
                    ProgressView()
                    Text("Persona is joining the meeting…").font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("meeting-thread-joining")
            }
            ForEach(local) { exchange in
                MeetingExchangeRow(exchange: exchange, entry: entry)
                    .padding(Theme.Space.m)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            }
            if roomKey != nil, meeting.room?.known == .ready, role.attends, local.isEmpty, !Self.asked(roomEntries) {
                Text(role.speaks ? String(localized: "Ask Persona about the meeting. Answers may be heard by the room.")
                     : String(localized: "Ask Persona about the meeting. Answers are shown on this screen."))
                    .font(Theme.Font.secondary).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("meeting-thread-empty")
            }
        }
    }

    @ViewBuilder private func roomRow(_ item: MeetingThread.RoomRow, roomReady: Bool) -> some View {
        switch item {
        case .entry(let entry): row(entry, markdown: false, roomReady: roomReady)
        case .ask(let pair): askRows(pair)
        }
    }

    /// One answered question of the hub's `asks`, as the thread draws a room question and its answer.
    @ViewBuilder private func askRows(_ asked: MeetingThread.AskPair) -> some View {
        question(asked.question.flatMap { $0.isEmpty ? nil : $0 } ?? String(localized: "What was just said"), spoken: false)
        answer(asked.answer, markdown: false)
    }

    // MARK: After the meeting

    @ViewBuilder private func followupRows(_ key: SessionKey?, sends: [MeetingConversation.FollowupSend]) -> some View {
        afterDivider
        if let key {
            let store = model.chat(key)
            if store.hasMore { earlier(key, identifier: "meeting-followup-earlier") }
            ForEach(followupEntries) { item in row(item, markdown: true) }
            MeetingLiveAnswer(store: store, markdown: true, identifier: "meeting-thread-answer-live")
        }
        ForEach(sends) { send in
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                question(send.text, spoken: false)
                switch send.state {
                case .sending:
                    sending
                case .sent:
                    EmptyView()
                case .failed(let message):
                    failure(message, identifier: "meeting-followup-retry") { conversation.retryFollowup(send.id) }
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("meeting-followup-pending")
        }
    }

    /// **After the Meeting**: a hairline across the thread with the words in the middle, as the chat marks a compaction
    /// (`CompactDivider`).
    private var afterDivider: some View {
        HStack(spacing: Theme.Space.m) {
            rule
            Text("After the Meeting").font(Theme.Font.captionMedium).foregroundStyle(.secondary)
                .lineLimit(1).minimumScaleFactor(0.7)
                .layoutPriority(1)
            rule
        }
        .padding(.vertical, Theme.Space.xs)
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
        .accessibilityIdentifier("meeting-after-divider")
    }

    private var rule: some View {
        let beautiful = AppStyle.current.usesBeautifulUI
        return Rectangle().fill(beautiful ? Theme.cardStroke : Color(uiColor: .separator))
            .frame(height: beautiful ? Theme.Size.line : Theme.Size.hairline)
            .frame(maxWidth: .infinity)
            .accessibilityHidden(true)
    }

    // MARK: Just Me

    @ViewBuilder private func justMeRows(_ mine: [MeetingConversation.JustMe]) -> some View {
        if mine.isEmpty {
            Text("Ask privately. Answers stay on this screen and in the main chat.")
                .font(Theme.Font.secondary).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("meeting-just-me-empty")
        }
        ForEach(mine) { ask in justMeRow(ask, last: ask.id == mine.last?.id) }
        // The turn's cards are answered in the main chat (their one home); here only that it waits.
        if waitsOnACard(mine) {
            Label { Text("Persona is waiting for your answer in the main chat.") } icon: {
                ThemedIcon(Theme.Icon.question).foregroundStyle(Theme.caution)
            }
            .font(Theme.Font.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("meeting-just-me-waiting")
        }
    }

    /// A Just Me turn that is still the main chat's newest raised a card: one raised since its message (not one another
    /// turn of the main chat raised before it).
    private func waitsOnACard(_ mine: [MeetingConversation.JustMe]) -> Bool {
        mine.contains { ask in
            guard case .sent(let key, _) = ask.state, let reply = replies[ask.id], reply.latest, let at = reply.at else { return false }
            return model.interactions(for: key).contains { ($0.createdAt ?? .distantPast) >= at }
        }
    }

    /// One private question: the words, then sending, what went wrong with Retry, or the main chat's answer (its text
    /// being written while its turn runs). Never announced: VoiceOver would say it to the room.
    private func justMeRow(_ ask: MeetingConversation.JustMe, last: Bool) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            question(ask.text, spoken: false, identifier: "meeting-just-me-question")
            switch ask.state {
            case .sending:
                sending
            case .failed(let message):
                failure(message, identifier: "meeting-just-me-retry") { conversation.retryJustMe(ask.id) }
            case .sent(let key, _):
                let reply = replies[ask.id]
                if let text = reply?.text {
                    answer(text, markdown: true, identifier: "meeting-just-me-answer")
                }
                // The chat's live text is this answer only while its message is in the chat and its turn the newest: a
                // message still queued behind another turn would show that turn's words.
                MeetingLiveAnswer(store: model.chat(key), markdown: true, identifier: "meeting-just-me-answer-live",
                                  shown: last && reply?.latest == true, looking: reply?.text == nil)
            }
        }
        .accessibilityElement(children: .contain)
    }

    // MARK: Rows

    @ViewBuilder private func row(_ item: MeetingThread.Entry, markdown: Bool, roomReady: Bool = false) -> some View {
        switch item {
        case .joined:
            quiet(String(localized: "Persona joined the meeting"))
                .accessibilityIdentifier("meeting-thread-joined")
        case .ended:
            quiet(String(localized: "The meeting ended"))
                .accessibilityIdentifier("meeting-thread-ended")
        case .lines(let id, _, let lines):
            let full = lines.count == 1 ? String(localized: "1 line from the meeting") : String(localized: "\(lines.count) lines from the meeting")
            let short = lines.count == 1 ? String(localized: "1 line") : String(localized: "\(lines.count) lines")
            fold(id, title: size.isAccessibilitySize ? short : full, spoken: full, symbol: Theme.Icon.quote) {
                linesList(lines)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("meeting-thread-lines")
        case .heard(let id, _, let clock, let lines, _):
            let full = clock.isEmpty ? String(localized: "What was just said") : String(localized: "What was just said (\(clock))")
            // At accessibility sizes the time steps aside (its lines carry theirs): the capsule stays one line.
            fold(id, title: size.isAccessibilitySize ? String(localized: "What was just said") : full, spoken: full,
                 symbol: Theme.Icon.voiceListening) {
                linesList(lines)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("meeting-thread-heard")
        case .question(_, _, let text, let via, _):
            question(text, spoken: via == .spoken)
        case .answer(_, _, let text):
            answer(text, markdown: markdown)
        case .image(_, _, let image):
            photo(image)
                .accessibilityIdentifier("meeting-thread-image")
        case .material(let id, _, let title, let text, let seed):
            // A fixed name on the capsule: the block's title is the hub's or the owner's words, of any length, so it heads
            // what opens instead, where it may wrap.
            let name = seed ? String(localized: "The meeting's material") : String(localized: "Context")
            fold(id, title: name, spoken: name, symbol: Theme.Icon.minutes) {
                material(id, title: title, text)
            }
            // Its Show All is a control of its own.
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("meeting-thread-material")
        case .photo(_, _, let photoID):
            photoRow(photoID, inRoom: true, roomReady: roomReady)
        }
    }

    /// The owner's own words ("You"); one spoken in the room says so.
    private func question(_ text: String, spoken: Bool, identifier: String = "meeting-thread-question") -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            HStack(spacing: Theme.Space.s) {
                Text("You").font(Theme.Font.captionStrong).foregroundStyle(.secondary)
                if spoken { Text("Spoken").font(Theme.Font.caption).foregroundStyle(.secondary) }
            }
            Text(text).font(Theme.Font.body).textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(identifier)
    }

    /// Persona's answer, in the AI's color: plain in the room (its replies are plain sentences), Markdown in the
    /// follow-up and the main chat.
    private func answer(_ text: String, markdown: Bool, identifier: String = "meeting-thread-answer") -> some View {
        MeetingAnswerRow(text: text, markdown: markdown, identifier: identifier)
    }

    /// A quiet line across the thread: Persona joined, the meeting ended.
    private func quiet(_ text: String) -> some View {
        Text(text).font(Theme.Font.caption).foregroundStyle(.secondary)
            .multilineTextAlignment(.center)
            .frame(maxWidth: .infinity)
            .accessibilityElement(children: .combine)
    }

    private var sending: some View {
        HStack(spacing: Theme.Space.s) {
            ProgressView()
            Text("Sending…").font(Theme.Font.caption).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }

    private func failure(_ message: String, identifier: String, retry: @escaping () -> Void) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Text(message).font(Theme.Font.secondary).foregroundStyle(Theme.danger)
                .fixedSize(horizontal: false, vertical: true)
            Button(action: retry) { Label("Retry", systemImage: Theme.Icon.retry).lineLimit(1) }
                .themedButton()
                .accessibilityIdentifier(identifier)
        }
    }

    private func earlier(_ key: SessionKey, identifier: String) -> some View {
        Group {
            if model.chat(key).isLoadingOlder {
                ProgressView().frame(maxWidth: .infinity).scaledFrame(minHeight: Theme.Size.tapTarget)
            } else {
                Button { Task { await model.loadOlder(key, turns: Theme.Recording.earlierTurns) } } label: {
                    Text("Show Earlier").font(Theme.Font.secondaryStrong).lineLimit(1)
                        .frame(maxWidth: .infinity)
                        .scaledFrame(minHeight: Theme.Size.tapTarget)
                        .contentShape(.rect)
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier(identifier)
            }
        }
    }

    /// A folded row: one line in a capsule, a tap opens what it holds under it. The capsule is a button, so its words
    /// stay on one line at every size (shorter ones at accessibility sizes, `spoken` saying them whole).
    private func fold<Content: View>(_ id: String, title: String, spoken: String, symbol: String,
                                     @ViewBuilder content: () -> Content) -> some View {
        let isOpen = open.contains(id)
        return VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Button { toggle(id) } label: {
                HStack(spacing: Theme.Space.s) {
                    ThemedIcon(symbol).accessibilityHidden(true)
                    Text(title)
                        .lineLimit(1)
                        .minimumScaleFactor(0.8)
                    ThemedIcon(isOpen ? Theme.Icon.collapse : Theme.Icon.expand).accessibilityHidden(true)
                }
                .font(Theme.Font.caption)
                .foregroundStyle(.secondary)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.xs)
                .background(Theme.Fill.well, in: .capsule)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(Text(spoken))
            .accessibilityValue(isOpen ? Text("Expanded") : Text("Collapsed"))
            if isOpen {
                content()
                    .padding(Theme.Space.m)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
                    .transition(.opacity)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func toggle(_ id: String) {
        var next = open
        if next.contains(id) { next.remove(id) } else { next.insert(id) }
        withAnimation(reduceMotion ? nil : Theme.Motion.state) { open = next }
    }

    /// The `[m:ss] text` lines of a batch or a heard question, each at its time in the meeting.
    private func linesList(_ lines: [MeetingThread.Line]) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            ForEach(lines.indices, id: \.self) { index in
                let stamp = Text(lines[index].clock).font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
                let words = Text(lines[index].text).font(Theme.Font.secondary).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                if size.isAccessibilitySize {
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) { stamp; words }
                } else {
                    HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) { stamp; words }
                }
            }
        }
    }

    /// The meeting's material (the follow-up's first message) or a context block the owner picked: its title, then a few
    /// lines and Show All, so a long transcript does not push the thread away.
    private func material(_ id: String, title: String, _ text: String) -> some View {
        let all = whole.contains(id)
        let long = text.count > Theme.Recording.materialPreview
            || text.split(separator: "\n", omittingEmptySubsequences: false).count > Theme.Recording.materialLines
        return VStack(alignment: .leading, spacing: Theme.Space.s) {
            if !title.isEmpty {
                Text(title).font(Theme.Font.secondaryStrong)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityAddTraits(.isHeader)
            }
            Text(text).font(Theme.Font.secondary).foregroundStyle(.secondary)
                .lineLimit(all ? nil : Theme.Recording.materialLines)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            if long, !all {
                Button { _ = whole.insert(id) } label: { Text("Show All").lineLimit(1) }
                    .font(Theme.Font.secondaryStrong)
                    .buttonStyle(.borderless)
                    .scaledFrame(minHeight: Theme.Size.tapTarget)
                    .accessibilityIdentifier("meeting-thread-material-all")
            }
        }
    }

    // MARK: Photos

    /// A photo of the thread: its own view, which reads the recording's photos (`MeetingPhotos.Album`). `inRoom`: the
    /// room's message of it, which says Photo removed once the hub's list no longer has it.
    private func photoRow(_ photoID: String, inRoom: Bool, roomReady: Bool) -> some View {
        MeetingPhotoRow(album: album, recording: entry.id, photoID: photoID, inRoom: inRoom, after: mode == .after,
                        roomReady: roomReady, confirming: $confirming, viewing: $viewing)
    }

    private var confirmingBinding: Binding<Bool> {
        Binding(get: { confirming != nil }, set: { if !$0 { confirming = nil } })
    }

    /// Delete Photo, confirmed: the store deletes a stored one at the hub (the room's message of it then reads Photo
    /// removed) and settles a failed one with the hub first (`MeetingPhotos.remove`); its row says Deleting… meanwhile.
    /// No haptic: the microphone may be open.
    private func delete(_ item: MeetingPhotoItem) {
        confirming = nil
        let recording = entry.id
        let current = album.items[item.photoID] ?? item
        Task { await MeetingPhotos.shared.remove(current, of: recording) }
    }

    /// A photo in the thread: its thumbnail, or a chip when the item carries no picture.
    @ViewBuilder private func photo(_ image: ImagePayload?) -> some View {
        if let image {
            ChatThumbnail(payload: image)
                .scaledFrame(width: Theme.Recording.photoSide, height: Theme.Recording.photoSide)
                .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(Text("Photo"))
        } else {
            Chip(text: String(localized: "Photo"), symbol: Theme.Icon.photo)
        }
    }
}

/// What the room's rows are derived from: its items as they are now (`signature`), the hub's asks (an ask answered
/// from the material, or answered at all, changes what shows without a new item), and whether the meeting ended (only
/// then may its last line say so).
private struct RoomInput: Equatable {
    var items: String
    var asks: [MeetingAskWire.Ask]
    var ended: Bool
}

// MARK: Photos

/// What the photos drawn after the room's rows are derived from: the recording's photos in order, and those the room's
/// thread holds.
private struct PhotoInput: Equatable {
    var order: [String]
    var inRoom: Set<String>
}

/// One photo (#3862 phase 3): its thumbnail (a tap opens it), "Photo" and when it was taken, its caption or the first
/// lines read in it, where it is in words (being prepared or uploaded, meeting material, why it couldn't go, being
/// deleted), whether the minutes cite it, and under it the room's note, or while the room looks at it, that it does. A
/// failed one shows Retry when Retry can help and Delete Photo; a long press offers Delete Photo on any it can delete.
///
/// VoiceOver reads the photo as one element (its label the time and words, its value the state; View Photo, Retry and
/// Delete Photo as its actions, so nothing needs the long press), the buttons and the note as their own. The photo
/// element's identifier names its state for the UI tests: `meeting-photo-preparing`, `-uploading`, `-material`,
/// `-failed`, `-deleting`, or `meeting-photo-unlisted` before the hub's list is read.
private struct MeetingPhotoRow: View {
    let album: MeetingPhotos.Album
    var recording: UUID
    var photoID: String
    var inRoom: Bool
    var after: Bool
    var roomReady: Bool
    @Binding var confirming: MeetingPhotoItem?
    @Binding var viewing: MeetingPhotoItem?

    @Environment(\.dynamicTypeSize) private var size

    var body: some View {
        if let item = album.items[photoID] {
            row(item)
        } else if inRoom, album.loaded {
            Text("Photo removed").font(Theme.Font.caption).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: .infinity)
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("meeting-photo-removed")
        } else if inRoom {
            // Before the hub's list is read: the photo as far as the room's message tells.
            row(MeetingPhotoItem(photoID: photoID))
        }
    }

    private func row(_ item: MeetingPhotoItem) -> some View {
        let busy = album.deleting.contains(item.photoID)
        let problem = album.problems[item.photoID]
        let layout = size.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: Theme.Space.s))
            : AnyLayout(HStackLayout(alignment: .top, spacing: Theme.Space.m))
        return VStack(alignment: .leading, spacing: Theme.Space.s) {
            layout {
                Button { viewing = item } label: {
                    MeetingPhotoThumb(recording: recording, photoID: item.photoID, n: item.n, ready: item.hasImage)
                }
                .buttonStyle(.plain)
                .disabled(!item.hasImage)
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    heading(item)
                    state(item, busy: busy)
                    if after, item.listed?.cited == true {
                        Label { Text("Cited in the minutes") } icon: { ThemedIcon(Theme.Icon.minutes).foregroundStyle(Theme.accent) }
                            .font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .opacity(busy ? Theme.Recording.dimmed : 1)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(Text(label(item)))
            .accessibilityValue(Text(value(item, busy: busy)))
            .accessibilityAddTraits(item.hasImage ? .isImage : [])
            .accessibilityAction { if item.hasImage { viewing = item } }
            .accessibilityActions {
                if item.hasImage { Button("View Photo") { viewing = item } }
                if item.retryable, !busy, let local = item.local { Button("Retry") { MeetingPhotos.shared.retry(local.id) } }
                if item.deletable, !busy { Button("Delete Photo") { confirming = item } }
            }
            .accessibilityIdentifier(stateIdentifier(item, busy: busy))
            if item.failed, !busy { failedControls(item) }
            if let problem {
                Text(problem).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("meeting-photo-delete-problem")
            }
            if let note = item.note {
                MeetingAnswerRow(text: note, markdown: false, identifier: "meeting-photo-note")
            } else if item.listed?.forwarded == true, roomReady, let added = item.addedAt {
                MeetingPhotoReading(until: added.addingTimeInterval(Theme.Recording.photoNoteWait))
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(.rect)
        .contextMenu {
            if item.deletable, !busy {
                Button(role: .destructive) { confirming = item } label: { Label("Delete Photo", systemImage: Theme.Icon.delete) }
                    .accessibilityIdentifier("meeting-photo-delete")
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("meeting-photo-row")
        // A failed delete is said, as any transient message is (DESIGN_SYSTEM 6.3, item 9).
        .onChange(of: problem) { _, now in
            if let now { AccessibilityNotification.Announcement(now).post() }
        }
    }

    /// "Photo" and its time in the meeting, then its caption or the first lines read in it.
    @ViewBuilder private func heading(_ item: MeetingPhotoItem) -> some View {
        HStack(spacing: Theme.Space.s) {
            Text("Photo").font(Theme.Font.captionStrong).foregroundStyle(.secondary)
            if let at = item.at {
                Text(RecordingFormat.clock(at)).font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
        }
        if let words = item.preview {
            Text(words).font(Theme.Font.secondary)
                .lineLimit(Theme.Recording.photoTextLines)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    /// Where the photo is, in words: being deleted, being prepared, being uploaded, stored as the meeting's material, or
    /// why it couldn't go.
    @ViewBuilder private func state(_ item: MeetingPhotoItem, busy: Bool) -> some View {
        if busy {
            progress(String(localized: "Deleting…"))
        } else {
            switch item.local?.state {
            case .some(.preparing):
                progress(String(localized: "Preparing…"))
            case .some(.uploading):
                progress(String(localized: "Uploading…"))
            case .some(.failed(let message, _, _)):
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    Text("Couldn't upload").font(Theme.Font.captionStrong).foregroundStyle(Theme.danger)
                    Text(message).font(Theme.Font.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            case .some(.stored), .none:
                if item.stored {
                    Label { Text("Used as material") } icon: { ThemedIcon(Theme.Icon.successFilled).foregroundStyle(Theme.positive) }
                        .font(Theme.Font.caption).foregroundStyle(.secondary)
                }
            }
        }
    }

    /// A failed photo's buttons: Retry when sending it again can help, and Delete Photo, so a refusal the hub would give
    /// again never leaves the row with nothing to do but a hidden long press.
    private func failedControls(_ item: MeetingPhotoItem) -> some View {
        let layout = size.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: Theme.Space.s))
            : AnyLayout(HStackLayout(spacing: Theme.Space.s))
        return layout {
            if item.retryable, let local = item.local {
                Button { MeetingPhotos.shared.retry(local.id) } label: { Label("Retry", systemImage: Theme.Icon.retry).lineLimit(1) }
                    .themedButton()
                    .accessibilityIdentifier("meeting-photo-retry")
            }
            Button(role: .destructive) { confirming = item } label: {
                Label("Delete Photo", systemImage: Theme.Icon.delete).lineLimit(1).minimumScaleFactor(0.8)
            }
            .themedButton()
            .accessibilityIdentifier("meeting-photo-remove")
        }
    }

    private func progress(_ words: String) -> some View {
        HStack(spacing: Theme.Space.s) {
            ProgressView()
            Text(words).font(Theme.Font.caption).foregroundStyle(.secondary)
        }
    }

    /// "Photo, 12:34, <its first lines>": what VoiceOver says first.
    private func label(_ item: MeetingPhotoItem) -> String {
        [String(localized: "Photo"), item.at.map { RecordingFormat.clock($0) }, item.preview].compactMap { $0 }.joined(separator: ", ")
    }

    /// The state in words, as the row shows it (never the dimming alone), and whether the minutes cite it.
    private func value(_ item: MeetingPhotoItem, busy: Bool) -> String {
        var parts: [String] = []
        if busy {
            parts.append(String(localized: "Deleting…"))
        } else {
            switch item.local?.state {
            case .some(.preparing): parts.append(String(localized: "Preparing…"))
            case .some(.uploading): parts.append(String(localized: "Uploading…"))
            case .some(.failed(let message, _, _)): parts += [String(localized: "Couldn't upload"), message]
            case .some(.stored), .none: if item.stored { parts.append(String(localized: "Used as material")) }
            }
        }
        if after, item.listed?.cited == true { parts.append(String(localized: "Cited in the minutes")) }
        return parts.joined(separator: ", ")
    }

    private func stateIdentifier(_ item: MeetingPhotoItem, busy: Bool) -> String {
        if busy { return "meeting-photo-deleting" }
        switch item.local?.state {
        case .some(.preparing): return "meeting-photo-preparing"
        case .some(.uploading): return "meeting-photo-uploading"
        case .some(.failed): return "meeting-photo-failed"
        case .some(.stored), .none: return item.stored ? "meeting-photo-material" : "meeting-photo-unlisted"
        }
    }
}

/// A photo's thumbnail: the prepared JPEG on this phone, else the hub's copy, decoded off the main actor at the pixels
/// it is drawn at (`MeetingPhotos.thumbnail`, cached there). `ready`: there is something to show yet (a photo still being
/// prepared has no picture), so the load runs again once there is. The minutes' cited photos use it too.
struct MeetingPhotoThumb: View {
    var recording: UUID
    var photoID: String
    var n: Int?
    var ready: Bool

    @Environment(\.displayScale) private var scale
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var image: UIImage?

    var body: some View {
        let side = Int((Theme.Recording.photoThumbSide * Theme.sizeScale(typeSize) * scale).rounded(.up))
        ZStack {
            if let image {
                // The whole photo, letterboxed on the well: a meeting's photos are boards and slides, whose edges hold words.
                Image(uiImage: image).resizable().scaledToFit()
            } else {
                ThemedIcon(Theme.Icon.photo).font(Theme.Font.heading).foregroundStyle(.secondary)
            }
        }
        .scaledFrame(width: Theme.Recording.photoThumbSide, height: Theme.Recording.photoThumbSide)
        .background(Theme.Fill.well)
        .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
        .contentShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
        .task(id: "\(photoID)|\(n ?? 0)|\(ready)|\(side)") {
            guard ready else { return }
            if let loaded = await MeetingPhotos.shared.thumbnail(recording, photoID: photoID, n: n, side: side) { image = loaded }
        }
    }
}

/// "Persona is looking at the photo…" while the room writes its note, until `until` (`Theme.Recording.photoNoteWait`
/// after the photo was added): a schedule of that one date draws it again then, so a note that never comes doesn't keep
/// it turning.
private struct MeetingPhotoReading: View {
    var until: Date

    var body: some View {
        TimelineView(.explicit([until])) { _ in
            if Date.now < until {
                HStack(spacing: Theme.Space.s) {
                    ProgressView()
                    Text("Persona is looking at the photo…").font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("meeting-photo-reading")
            }
        }
    }
}

/// A photo full screen: the app's one image viewer (`ImageViewer`) once its bytes are here (this phone's copy, else the
/// hub's), a spinner with Done until then, and what went wrong when neither has it. The minutes' cited photos open it too.
struct MeetingPhotoViewer: View {
    var recording: UUID
    var item: MeetingPhotoItem

    @Environment(\.dismiss) private var dismiss
    @State private var image: ViewerImage?
    @State private var failed = false

    var body: some View {
        Group {
            if let image {
                ImageViewer(items: [image], index: 0)
            } else {
                Group {
                    if failed {
                        EmptyStateView(symbol: Theme.Icon.error, title: String(localized: "Image didn't load"),
                                       message: String(localized: "Couldn't get this image. Tap Retry to try again."),
                                       action: (title: String(localized: "Retry"), run: { failed = false }), tint: Theme.caution)
                    } else {
                        ProgressView()
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .themedGroupedPage()
                .overlay(alignment: .topTrailing) {
                    RecordingSheetCloseButton(title: String(localized: "Done"), identifier: "meeting-photo-viewer-done") { dismiss() }
                        .padding(.horizontal, Theme.Space.page)
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("meeting-photo-viewer")
        .task(id: failed) {
            guard !failed, image == nil else { return }
            await load()
        }
    }

    private func load() async {
        guard let data = await MeetingPhotos.shared.data(recording, photoID: item.photoID, n: item.n) else {
            failed = true
            return
        }
        // A megabyte of base64, made off the main actor; the viewer decodes it there too.
        let encoded = await Task.detached(priority: .userInitiated) { data.base64EncodedString() }.value
        let clock = item.at.map { RecordingFormat.clock($0) }
        image = ViewerImage(payload: ImagePayload(mime: MeetingPhotoPipeline.mime, data: encoded),
                            title: String(localized: "Photo"), detail: clock)
    }
}

/// What the Just Me rows are derived from: the questions and the state of the chats they went to.
private struct JustMeInput: Equatable {
    var asks: [MeetingConversation.JustMe]
    var chats: [String]
}

/// Persona's answer, in the AI's color: plain in the room (its replies are plain sentences), Markdown in the follow-up
/// and the main chat.
private struct MeetingAnswerRow: View {
    var text: String
    var markdown: Bool
    var identifier: String

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Label { Text("Persona") } icon: { ThemedIcon(Theme.Icon.persona).accessibilityHidden(true) }
                .font(Theme.Font.captionStrong).foregroundStyle(Theme.ai)
            MeetingAnswerText(text: text, markdown: markdown)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(identifier)
    }
}

/// A reply being written in a conversation (`ChatStore.liveText`), as a pending answer. Its own view, so each streamed
/// piece redraws only this row, never the thread around it (P5). `shown`: the live text is this row's to show (a Just Me
/// answer's only while its turn is the chat's newest). `looking`: with no live text, "Persona is looking…" holds its
/// place (a Just Me question with no answer yet).
private struct MeetingLiveAnswer: View {
    let store: ChatStore
    var markdown: Bool
    var identifier: String
    var shown = true
    var looking = false

    var body: some View {
        let live = shown ? store.liveText : ""
        if !live.isEmpty {
            MeetingAnswerRow(text: live, markdown: markdown, identifier: identifier)
        } else if looking {
            HStack(spacing: Theme.Space.s) {
                ProgressView()
                Text("Persona is looking…").font(Theme.Font.secondary).foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
        }
    }
}

/// An answer's text: Markdown's inline styles where the conversation writes them, else as it is. Its own view, so a row
/// parses only when its text changes.
private struct MeetingAnswerText: View, Equatable {
    var text: String
    var markdown: Bool

    var body: some View {
        Group {
            if markdown, let rich = try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) {
                Text(rich)
            } else {
                Text(verbatim: text)
            }
        }
        .font(Theme.Font.body)
        .textSelection(.enabled)
        .fixedSize(horizontal: false, vertical: true)
    }
}
#endif
