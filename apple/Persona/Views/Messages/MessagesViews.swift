import SwiftUI

// Settings › Connectors › Messages: one Mac's Messages, read through the hub (docs/plans/plan-connectors.md「iMessage `connector`」). The user reads every
// conversation here; Hand to Persona (on by default) also gives the real ones to the knowledge base, the inbox and the
// assistant, and can be turned off, and what the hub kept deleted. Nothing is marked read, nothing is sent.

/// `manager`: the Messages of another manager's connector (docs/assistant/managers.md "Grouping"), nil the default's.
struct MessagesChatsRoute: Hashable { var manager: String? = nil }
struct MessagesThreadRoute: Hashable { var id: String; var name: String; var manager: String? = nil }

/// The connector's page: which Mac, whether it may read, and how to let it. `manager`: another manager's copy, every
/// request naming it; nil the default's.
struct MessagesConnectorView: View {
    var manager: String? = nil
    @Environment(AppModel.self) private var model
    @State private var status: MessagesStatus?
    @State private var loading = true
    @State private var failure: String?
    @State private var saving = false
    @State private var confirmDelete = false
    @State private var deleted: String?

    var body: some View {
        SettingsForm {
            Section {
                HStack(spacing: Theme.Space.m) {
                    Image(systemName: Theme.Icon.messagesApp).resizable().scaledToFit().foregroundStyle(Theme.positive)
                        .scaledFrame(width: Theme.Size.wellM, height: Theme.Size.wellM)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(status.map { String(localized: "Messages on \($0.machine)") } ?? String(localized: "Messages [Image Viewer e70286]")).font(Theme.Font.heading).lineLimit(1)
                        HStack(spacing: Theme.Space.sm) {
                            Circle().fill(tint).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                            Text(stateText).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                        }
                    }
                    Spacer(minLength: Theme.Space.s)
                    if loading { ProgressView() }
                }
                .padding(.vertical, Theme.Space.xs)
                if status?.status == "ok" {
                    NavigationLink(value: MessagesChatsRoute(manager: manager)) {
                        Label("Session", systemImage: Theme.Icon.feishuChats)
                    }
                    .accessibilityIdentifier("messages-chats")
                }
                if let e = (status?.status == "ok" ? nil : status?.error) ?? failure, !e.isEmpty {
                    Text(e).font(Theme.Font.label).foregroundStyle(Theme.danger).textSelection(.enabled)
                }
            }
            if status?.status == "no_permission", let guide = status?.guide, !guide.isEmpty {
                Section {
                    ForEach(Array(guide.enumerated()), id: \.offset) { i, step in
                        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.m) {
                            Text("\(i + 1)").font(Theme.Font.labelStrong).monospacedDigit().foregroundStyle(Theme.accent)
                            Text(step).font(Theme.Font.body).textSelection(.enabled)
                        }
                    }
                    Button { Task { await load() } } label: {
                        Label("Check Again [Lark Chat View c25fb8]", systemImage: Theme.Icon.retry).lineLimit(1)
                    }
                    .disabled(loading)
                } header: {
                    SettingsHeading(String(localized: "Grant Access on \(status?.machine ?? String(localized: "that Mac"))"))
                }
            }
            if let ai = status?.ai {
                Section {
                    Toggle(isOn: Binding(get: { ai }, set: { on in Task { await setAI(on) } })) {
                        Label("Hand to Persona", systemImage: Theme.Icon.ai).lineLimit(1)
                    }
                    .disabled(saving)
                    .accessibilityIdentifier("messages-ai")
                    Button(role: .destructive) { confirmDelete = true } label: {
                        Label("Delete Messages Stored on Hub", systemImage: Theme.Icon.delete).lineLimit(1)
                    }
                    .disabled(ai || saving)
                    .accessibilityIdentifier("messages-delete")
                    if let deleted {
                        Text(deleted).font(Theme.Font.label).foregroundStyle(.secondary)
                    }
                } footer: {
                    Text(ai ? String(localized: "Conversations with your contacts and ones you've replied to go into the knowledge base and Inbox (sorted like Lark), where the assistant can search and summarize them. Verification codes are masked. Turning this off stops it right away; to delete what's stored on the hub, turn it off first.")
                            : String(localized: "Not handed to Persona: nothing syncs or goes to the Inbox, and the assistant can't read it. Anything already stored on the hub is still there and can be deleted."))
                }
            }
            Section {
            } footer: {
                Text(status?.ai == nil ? String(localized: "Messages here are just for you: they aren't stored on the hub or handed to AI, and viewing them doesn't mark them as read. Persona is read-only and never sends messages for you.")
                                       : String(localized: "You can see every conversation here (read straight from that Mac; viewing doesn't mark anything as read). Persona is read-only and never sends messages for you."))
            }
        }
        .confirmationDialog("Delete messages stored on the hub?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Delete", role: .destructive) { Task { await deleteData() } }
        } message: {
            Text("Messages conversations and people in the knowledge base, and Messages items in the Inbox, will be deleted. Messages on that Mac aren't affected.")
        }
        .themedPage()
        .detailTitle(String(localized: "Messages [Image Viewer e70286]"))
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
    }

    private var tint: Color {
        switch status?.status {
        case nil: .secondary
        case "ok": Theme.positive
        case "not_configured": .secondary
        default: Theme.danger
        }
    }

    private var stateText: String {
        guard let status else { return loading ? String(localized: "Checking…") : String(localized: "Couldn't get status") }
        switch status.status {
        case "ok": return String(localized: "Readable")
        case "no_permission": return String(localized: "No Full Disk Access")
        case "not_configured": return String(localized: "That computer doesn't have Messages")
        default: return String(localized: "Can't read")
        }
    }

    private func load() async {
        loading = true
        defer { loading = false }
        do {
            status = try await model.api?.manager(manager).messagesStatus()
            failure = nil
        } catch {
            failure = error.shown.map { String(localized: "Couldn't read the Messages status: \($0)") }
        }
    }

    private func setAI(_ on: Bool) async {
        saving = true
        defer { saving = false }
        do {
            if let s = try await model.api?.manager(manager).setMessagesAI(on) { status = s }
            deleted = nil
            failure = nil
        } catch {
            failure = error.shown.map { String(localized: "Couldn't change it: \($0) [Messages Views 87cfe2]") }
        }
    }

    private func deleteData() async {
        saving = true
        defer { saving = false }
        do {
            if let d = try await model.api?.manager(manager).deleteMessagesData() {
                deleted = String(localized: "Deleted. Conversations: \(d.chats), people: \(d.people), Inbox items: \(d.inbox)")
            }
            failure = nil
        } catch {
            failure = error.shown.map { String(localized: "Couldn't delete: \($0) [Messages Views d07409]") }
        }
    }
}

/// The Mac's conversations, newest first.
struct MessagesChatListView: View {
    @State private var page: MessagesChatsModel
    @State private var query = ""

    init(manager: String? = nil) { _page = State(initialValue: MessagesChatsModel(manager: manager)) }

    var body: some View {
        let chats = page.filtered(query)
        List {
            if case .noPermission(let why) = page.failure, page.chats.isEmpty {
                Section {
                    Label(why, systemImage: Theme.Icon.error).font(Theme.Font.label).foregroundStyle(Theme.danger)
                }
            } else if let f = page.failure {
                Label(f.text, systemImage: Theme.Icon.error).font(Theme.Font.label).foregroundStyle(Theme.danger)
            }
            ForEach(chats) { chat in
                NavigationLink(value: MessagesThreadRoute(id: chat.id, name: chat.name, manager: page.manager)) {
                    MessagesChatRow(chat: chat)
                }
                .accessibilityIdentifier("messages-chat-\(chat.id)")
                .onAppear {
                    if chat.id == page.chats.last?.id { Task { await page.loadMore() } }
                }
            }
            if page.loading && page.loaded {
                ProgressView().frame(maxWidth: .infinity)
            }
        }
        .themedPage()
        .overlay {
            if !page.loaded {
                ProgressView()
            } else if chats.isEmpty && page.failure == nil {
                EmptyStateView(symbol: Theme.Icon.messagesApp, title: query.isEmpty ? String(localized: "No conversations") : String(localized: "No results"),
                               message: query.isEmpty ? String(localized: "Messages on that Mac has no conversations yet.") : String(localized: "Try a different name or number."))
            }
        }
        .detailTitle(String(localized: "Session"))
        .navigationBarTitleDisplayMode(.inline)
        .pageSearchable(text: $query, prompt: String(localized: "Search names or numbers"))
        .task { if !page.loaded { await page.load() } }
        .refreshable { await page.load() }
    }
}

/// One conversation in the list: who, the newest message, when, how many not read.
struct MessagesChatRow: View {
    var chat: MessagesChat

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.m) {
            avatar
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
                    Text(chat.name).font(Theme.Font.bodyStrong).lineLimit(1)
                    Spacer(minLength: Theme.Space.xs)
                    Text(LarkTime.short(chat.last)).font(Theme.Font.caption).foregroundStyle(.secondary).fixedSize()
                }
                HStack(alignment: .top, spacing: Theme.Space.sm) {
                    Text(preview).font(Theme.Font.secondary).lineHeight(.secondary).foregroundStyle(.secondary).lineLimit(2)
                    Spacer(minLength: 0)
                    if let n = chat.unread, n > 0 {
                        CountBadge(count: n, look: .accent)
                    }
                }
            }
        }
        .padding(.vertical, Theme.Space.xs)
        .contentShape(.rect)
    }

    private var preview: String {
        let text = chat.preview ?? ""
        return chat.fromMe == true && !text.isEmpty ? String(localized: "You: \(text)") : text
    }

    @ViewBuilder private var avatar: some View {
        if chat.isGroup {
            Circle().fill(Theme.monogramFill)
                .scaledFrame(width: Theme.Size.avatar, height: Theme.Size.avatar)
                .overlay(ThemedIcon(Theme.Icon.groupChat).font(Theme.Font.body).foregroundStyle(.secondary))
        } else {
            Monogram(name: chat.name, size: Theme.Size.avatar, fill: Theme.monogramFill)
        }
    }
}

/// One conversation's messages, oldest at the top; older pages load from the top.
struct MessagesThreadView: View {
    @State private var page: MessagesThreadModel
    private let title: String

    init(id: String, name: String, manager: String? = nil) {
        _page = State(initialValue: MessagesThreadModel(chatID: id, manager: manager))
        title = name
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: Theme.Space.xs) {
                    if page.more {
                        Button {
                            let first = page.messages.first?.id
                            Task {
                                await page.loadOlder()
                                if let first { proxy.scrollTo(first, anchor: .top) }
                            }
                        } label: {
                            Label("Earlier Messages", systemImage: Theme.Icon.recent).font(Theme.Font.label).lineLimit(1)
                        }
                        .buttonStyle(.borderless)
                        .disabled(page.loading)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, Theme.Space.s)
                    }
                    if let f = page.failure {
                        Label(f.text, systemImage: Theme.Icon.error).font(Theme.Font.label).foregroundStyle(Theme.danger)
                            .frame(maxWidth: .infinity)
                    }
                    let lines = MessagesLine.lines(page.messages, group: page.chat?.isGroup ?? false,
                                                   services: (page.chat?.services.count ?? 0) > 1)
                    ForEach(lines) { line in
                        MessagesLineView(line: line).id(line.message.id)
                    }
                }
                .padding(.horizontal, Theme.Space.page)
                .padding(.vertical, Theme.Space.m)
            }
            .defaultScrollAnchor(.bottom)
        }
        .themedPage()
        .overlay {
            if !page.loaded {
                ProgressView()
            } else if page.messages.isEmpty && page.failure == nil {
                EmptyStateView(symbol: Theme.Icon.messagesApp, title: String(localized: "No Messages"), message: String(localized: "There are no messages to show in this conversation yet."))
            }
        }
        .navigationTitle(page.chat?.name ?? title)
        .navigationBarTitleDisplayMode(.inline)
        .task { if !page.loaded { await page.load() } }
    }
}

/// A message with what goes above it: the time when there was a pause, who wrote it in a group, the service when it
/// changes (a person's iMessage and SMS chats are one conversation).
struct MessagesLine: Identifiable {
    var message: MessagesMessage
    var time: String?
    var sender: String?
    var service: String?

    var id: String { message.id }

    /// A gap this long (or a new day) shows the time again.
    static let pause: TimeInterval = 3600

    static func lines(_ messages: [MessagesMessage], group: Bool, services: Bool, now: Date = .now) -> [MessagesLine] {
        var out: [MessagesLine] = []
        var previous: MessagesMessage?
        var service: String?
        for m in messages {
            var line = MessagesLine(message: m)
            if let p = previous, m.at.timeIntervalSince(p.at) < pause, Calendar.current.isDate(m.at, inSameDayAs: p.at) {
                if group, !m.isMine, !m.isEvent, let who = m.sender?.shown, p.isMine || p.isEvent || p.sender?.handle != m.sender?.handle {
                    line.sender = who
                }
            } else {
                line.time = MessagesTime.separator(m.at, now: now)
                if group, !m.isMine, !m.isEvent { line.sender = m.sender?.shown }
            }
            if services, !m.isEvent, let s = m.service, !s.isEmpty, s != service {
                line.service = s == "SMS" ? String(localized: "SMS") : s
                service = s
            }
            out.append(line)
            previous = m
        }
        return out
    }
}

/// The time between messages: Today 08:00, Yesterday 21:30, Wednesday 09:12, Oct 21 18:00, Oct 21, 2025 18:00.
nonisolated enum MessagesTime {
    static func separator(_ d: Date, now: Date = .now) -> String {
        let cal = Calendar.current
        let time = d.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits))
        if cal.isDateInToday(d) { return String(localized: "Today \(time)") }
        if cal.isDateInYesterday(d) { return String(localized: "Yesterday \(time)") }
        if now.timeIntervalSince(d) < 6 * 86400, d < now { return d.formatted(.dateTime.weekday(.wide)) + " " + time }
        if cal.isDate(d, equalTo: now, toGranularity: .year) { return d.formatted(.dateTime.month().day()) + " " + time }
        return d.formatted(.dateTime.year().month().day()) + " " + time
    }
}

struct MessagesLineView: View {
    var line: MessagesLine

    private var m: MessagesMessage { line.message }

    var body: some View {
        VStack(alignment: m.isMine ? .trailing : .leading, spacing: Theme.Space.xxs) {
            if let time = line.time {
                caption([time, line.service].compactMap { $0 }.joined(separator: " · ")).padding(.top, Theme.Space.m)
            } else if let service = line.service {
                caption(service).padding(.top, Theme.Space.s)
            }
            if m.isEvent || m.unsent == true {
                caption(eventText).padding(.vertical, Theme.Space.xs)
            } else {
                if let who = line.sender {
                    Text(who).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                        .padding(.horizontal, Theme.Space.m).padding(.top, Theme.Space.xs)
                }
                if let quote = m.replyTo, !quote.isEmpty {
                    HStack(spacing: Theme.Space.sm) {
                        Capsule().fill(.tertiary).frame(width: Theme.Size.rule)
                        Text(quote).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(2)
                    }
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, Theme.Space.m)
                }
                HStack {
                    if m.isMine { Spacer(minLength: Theme.Size.avatarXL) }
                    bubble
                    if !m.isMine { Spacer(minLength: Theme.Size.avatarXL) }
                }
                if m.edited == true {
                    Text("Edited").font(Theme.Font.caption).foregroundStyle(.tertiary).padding(.horizontal, Theme.Space.m)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: m.isEvent || m.unsent == true ? .center : m.isMine ? .trailing : .leading)
    }

    private func caption(_ s: String) -> some View {
        Text(s).font(Theme.Font.caption).foregroundStyle(.secondary).multilineTextAlignment(.center)
            .frame(maxWidth: .infinity)
    }

    private var eventText: String {
        guard m.unsent == true, !m.isEvent else { return m.text ?? "" }
        return m.isMine ? String(localized: "You recalled a message") : String(localized: "\(m.sender?.shown ?? String(localized: "Someone [Messages Views ddfdac]")) recalled a message [Messages Views bb81d6]")
    }

    private var bubble: some View {
        VStack(alignment: .leading, spacing: Theme.Space.sm) {
            ForEach(Array((m.attachments ?? []).enumerated()), id: \.offset) { _, a in
                MessagesAttachmentRow(attachment: a)
            }
            if m.undecoded == true {
                Text("Can't read this message's format").font(Theme.Font.body).italic().foregroundStyle(.secondary)
            } else if let text = m.text, !text.isEmpty {
                Text(text).font(Theme.Font.body).lineHeight(.body).textSelection(.enabled)
            }
        }
        .foregroundStyle(m.isMine ? AnyShapeStyle(Theme.userBubbleText) : AnyShapeStyle(.primary))
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.s)
        .background(m.isMine ? Theme.userBubbleFill : AnyShapeStyle(Theme.Fill.chip),
                    in: .rect(cornerRadius: Theme.Radius.bubble, style: .continuous))
    }
}

/// An attachment as the Mac describes it: its file stays there.
struct MessagesAttachmentRow: View {
    var attachment: MessagesAttachment

    var body: some View {
        Label {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(title).font(Theme.Font.labelStrong).lineLimit(1)
                if let size = attachment.size, size > 0 {
                    Text(size.storageText).font(Theme.Font.caption).opacity(0.8)
                }
            }
        } icon: {
            ThemedIcon(symbol)
        }
    }

    private var title: String {
        switch attachment.kind {
        case "image": attachment.name.map { String(localized: "Photo · \($0)") } ?? String(localized: "images")
        case "video": attachment.name.map { String(localized: "Video · \($0)") } ?? String(localized: "Video")
        case "audio": String(localized: "voice")
        default: attachment.name ?? String(localized: "Files")
        }
    }

    private var symbol: String {
        switch attachment.kind {
        case "image": Theme.Icon.photo
        case "video": Theme.Icon.video
        case "audio": Theme.Icon.voiceMessage
        default: Theme.Icon.attachment
        }
    }
}
