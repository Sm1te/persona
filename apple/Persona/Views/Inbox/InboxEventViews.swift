import BeautifulUI
import SwiftUI

// Inbox phase 1 (docs/assistant/inbox.md): what an event adds to the inbox's screens — the row's mark, and in the detail
// recap, the suggestion card with its buttons, the chain and related items. The iPhone draws them as a page of their own
// (`InboxEventPage`, the source behind 'view original'); the Mac in the detail's action column (`MacInboxActions`), beside
// the source. An item without any of the new fields (an older hub) never reaches these.

// MARK: - Row

/// What an open row says under its summary: the suggestion's kind and first line, In Progress's state, the 'preparing a suggestion' line
/// for an urgent one, or that the AI failed.
struct InboxEventMarkLine: View {
    var mark: InboxEventText.Mark

    var body: some View {
        switch mark {
        case .preparing:
            HStack(spacing: Theme.Space.sm) {
                InboxSpinner()
                Text("AI is preparing a suggestion").font(Theme.Font.labelStrong).foregroundStyle(Theme.ai).lineLimit(1)
            }
        case .working(let word):
            (Text(Image(systemName: Theme.Icon.inProgress)) + Text(" " + word))
                .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
        case .suggestion(let kind, let line, let symbol):
            (Text(Image(systemName: symbol)) + Text(" " + kind).fontWeight(.semibold) + Text("  " + line).foregroundStyle(.secondary))
                .font(Theme.Font.label).foregroundStyle(Theme.ai).lineLimit(1)
        case .failed(let words):
            (Text(Image(systemName: Theme.Icon.error)) + Text(" " + words))
                .font(Theme.Font.label).foregroundStyle(Theme.danger).lineLimit(1)
        }
    }
}

/// The small spinner of the inbox's rows, in the current style.
struct InboxSpinner: View {
    var body: some View {
        if AppStyle.current.usesBeautifulUI { BUISpinner(size: Theme.Size.dotXL) } else { ProgressView().controlSize(.mini) }
    }
}

// MARK: - Blocks

/// recap, the suggestion (or why there is none yet), the chain and related items, one under another: the iPhone's event page and
/// the Mac's action column.
struct InboxEventBlocks: View {
    var item: InboxItem
    var model: InboxSuggestionModel
    /// The source is on screen beside them (the Mac): a link to just the source opens nothing more.
    var sourceShown = false
    /// Narrow (the Mac's strip over the item): recap cut to a few lines.
    var compact = false
    var edit: () -> Void
    /// How another item / the source at a message opens; nil pushes it on the surrounding stack (the Mac's column is
    /// beside the detail's stack, not in it).
    var openItem: ((InboxItem) -> Void)?
    var openSource: ((InboxSourceRoute) -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.l) {
            status
            if let recap = item.recap, !recap.isEmpty {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    InboxBlockTitle(title: String(localized: "Recap"))
                    Text(recap)
                        .font(Theme.Font.secondary)
                        .lineHeight(.secondary)
                        .lineLimit(compact ? 3 : nil)
                        .fixedSize(horizontal: false, vertical: !compact)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .accessibilityElement(children: .combine)
            }
            if !item.isDone, let s = item.currentSuggestion {
                InboxSuggestionCard(item: item, suggestion: s, model: model, edit: edit)
            } else if let notice = model.notice {
                Label(notice, systemImage: Theme.Icon.info).font(Theme.Font.captionMedium).foregroundStyle(Theme.caution).lineLimit(1)
            }
            InboxChainSection(item: item, sourceShown: sourceShown, openItem: openItem, openSource: openSource)
            InboxRelatedSection(item: item, openItem: openItem)
        }
        #if DEBUG
        // `-showInboxSuggestionStale YES`: confirm the suggestion as first shown after 6 s (bump it on the mock hub
        // meanwhile, `/mock/inbox_events/bump`), for 「建议已更新」 screenshots.
        .task {
            guard UserDefaults.standard.bool(forKey: "showInboxSuggestionStale"), !compact, let s = item.currentSuggestion else { return }
            try? await Task.sleep(for: .seconds(6))
            await model.confirm(s)
        }
        #endif
    }

    /// The processing conversation's state, when it has something to say: still at it, waiting, or failed (with why).
    @ViewBuilder private var status: some View {
        if let filed = InboxEventText.doneBy(item) {
            Label(filed, systemImage: Theme.Icon.done).font(Theme.Font.secondaryStrong).foregroundStyle(Theme.positive).lineLimit(1)
        } else if !item.isDone, let mark = InboxEventText.mark(item) {
            switch mark {
            case .suggestion: EmptyView() // the card says it
            case .failed(let words):
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Label(words, systemImage: Theme.Icon.error).font(Theme.Font.secondaryStrong).foregroundStyle(Theme.danger).lineLimit(1)
                    if let why = item.handling?.error, !why.isEmpty {
                        Text(why).font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                }
            default:
                InboxEventMarkLine(mark: mark)
            }
        }
    }
}

/// A block's small title (recap, chain, related items).
struct InboxBlockTitle: View {
    var title: String
    var body: some View {
        Text(title).font(Theme.Font.labelStrong).foregroundStyle(.secondary).lineLimit(1)
    }
}

// MARK: - Suggestion

/// The suggestion with its buttons right under it: send · edit · reject (a reply), start · edit · reject (an Agent),
/// got it · reject (a conclusion), confirm · edit · reject (one that needs judgment). edit opens `InboxSuggestionEditor`.
struct InboxSuggestionCard: View {
    var item: InboxItem
    var suggestion: InboxItem.Suggestion
    var model: InboxSuggestionModel
    var edit: () -> Void

    var body: some View {
        let s = suggestion
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(spacing: Theme.Space.sm) {
                Label(InboxEventText.kindTitle(s), systemImage: InboxEventText.symbol(s))
                    .font(Theme.Font.labelStrong).foregroundStyle(Theme.ai).lineLimit(1)
                Spacer(minLength: 0)
                if let at = s.at {
                    Text(LarkTime.short(at)).font(Theme.Font.caption).foregroundStyle(.secondary).fixedSize()
                }
            }
            if let notice = model.notice {
                Label(notice, systemImage: Theme.Icon.info).font(Theme.Font.captionMedium).foregroundStyle(Theme.caution).lineLimit(1)
                    .accessibilityIdentifier("inbox.suggestion.stale")
            }
            Text(s.text)
                .font(Theme.Font.secondary)
                .foregroundStyle(.primary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
            if let note = s.note, !note.isEmpty {
                Text(note).font(Theme.Font.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            ForEach(s.changes ?? [], id: \.self) { c in
                Label(DocChangeText.line(c, in: item.doc), systemImage: Theme.Icon.inProgress)
                    .font(Theme.Font.captionMedium).foregroundStyle(.secondary).lineLimit(1)
            }
            if let error = model.error {
                Label(error, systemImage: Theme.Icon.error).font(Theme.Font.caption).foregroundStyle(Theme.danger)
            }
            buttons(s)
        }
        .padding(Theme.Space.m)
        .modifier(DraftSurface())
        .accessibilityIdentifier("inbox.suggestion")
    }

    /// Side by side, the main one last at the trailing edge (as the draft card had them); where that does not fit (the
    /// Mac's narrow action column) one under another, full width, the main one first.
    private func buttons(_ s: InboxItem.Suggestion) -> some View {
        let actions = InboxEventText.actions(s)
        return ViewThatFits(in: .horizontal) {
            HStack(spacing: Theme.Space.s) {
                Spacer(minLength: 0)
                ForEach(Array(actions.reversed().enumerated()), id: \.offset) { _, a in button(a, s, fill: false) }
            }
            VStack(spacing: Theme.Space.s) {
                ForEach(Array(actions.enumerated()), id: \.offset) { _, a in button(a, s, fill: true) }
            }
        }
        .disabled(model.busy != nil)
    }

    @ViewBuilder private func button(_ a: InboxEventText.Action, _ s: InboxItem.Suggestion, fill: Bool) -> some View {
        switch a {
        case .reject:
            InboxSuggestionButton(title: String(localized: "Reject"), prominent: false, spinning: model.busy == "reject", fill: fill) {
                Task { await model.reject(s) }
            }
            .accessibilityIdentifier("inbox.suggestion.reject")
        case .edit:
            InboxSuggestionButton(title: String(localized: "Edit [Inbox Event Views 37090f]"), prominent: false, spinning: false, fill: fill, perform: edit)
                .accessibilityIdentifier("inbox.suggestion.edit")
        case .confirm(let title):
            InboxSuggestionButton(title: title, prominent: true, spinning: model.busy == "confirm", fill: fill) {
                Task { await model.confirm(s) }
            }
            .accessibilityIdentifier("inbox.suggestion.confirm")
        }
    }
}

/// One of the card's buttons: one line always, in the current style's buttons.
struct InboxSuggestionButton: View {
    var title: String
    var prominent: Bool
    var spinning: Bool
    /// As wide as the column (stacked buttons).
    var fill = false
    var perform: () -> Void

    var body: some View {
        let button = Button(action: perform) {
            HStack(spacing: Theme.Space.xs) {
                if spinning { InboxSpinner() }
                Text(title).lineLimit(1).fixedSize()
            }
            .frame(maxWidth: fill ? .infinity : nil)
        }
        if AppStyle.current.usesBeautifulUI {
            button.buttonStyle(.bui(prominent ? .accent : .secondary))
        } else {
            button.themedButton(prominent: prominent)
        }
    }
}

/// edit: the suggestion's words to change and confirm (send / start / confirm the changed text), or 'say a word' for the
/// manager instead (don't reply like that, ask about the time first), after which it suggests again.
struct InboxSuggestionEditor: View {
    @Environment(\.dismiss) private var dismiss
    var item: InboxItem
    var suggestion: InboxItem.Suggestion
    var model: InboxSuggestionModel
    @State private var text: String
    @State private var words = ""
    @FocusState private var editing: Bool

    init(item: InboxItem, suggestion: InboxItem.Suggestion, model: InboxSuggestionModel) {
        self.item = item
        self.suggestion = suggestion
        self.model = model
        _text = State(initialValue: suggestion.text)
    }

    /// send / start / confirm: the card's main button.
    private var confirmTitle: String {
        for a in InboxEventText.actions(suggestion) { if case .confirm(let t) = a { return t } }
        return String(localized: "Confirm")
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                if let note = suggestion.note, !note.isEmpty {
                    (Text(Image(systemName: Theme.Icon.ai)).foregroundStyle(Theme.ai) + Text(" " + note))
                        .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(4)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, Theme.Space.l)
                        .padding(.vertical, Theme.Space.m)
                }
                TextEditor(text: $text)
                    .focused($editing)
                    .scrollContentBackground(.hidden)
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.top, Theme.Space.s)
                    .disabled(model.busy != nil)
                    .accessibilityIdentifier("inbox.suggestion.text")
                if let error = model.error {
                    Label(error, systemImage: Theme.Icon.error).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                        .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, Theme.Space.l).padding(.bottom, Theme.Space.sm)
                }
                Divider()
                HStack(spacing: Theme.Space.s) {
                    TextField(String(localized: "Say a word, e.g. “Don't reply like that, ask about the time first”"), text: $words, axis: .vertical)
                        .lineLimit(1...3)
                        .padding(.horizontal, Theme.Space.m).padding(.vertical, Theme.Space.s)
                        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card))
                        .onSubmit { instruct() }
                        .accessibilityIdentifier("inbox.suggestion.words")
                    Button(action: instruct) {
                        HStack(spacing: Theme.Space.xs) {
                            if model.busy == "instruct" { ProgressView().controlSize(.mini) }
                            Text("Tell AI").lineLimit(1).fixedSize()
                        }
                    }
                    .buttonStyle(.bordered)
                    .disabled(model.busy != nil || words.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("inbox.suggestion.instruct")
                }
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.m)
            }
            .themedPage()
            .navigationTitle(InboxEventText.kindTitle(suggestion))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        let changed = text
                        Task { if await model.confirm(suggestion, text: changed).map(Self.closes) == true { dismiss() } }
                    } label: { Text(confirmTitle).lineLimit(1) }
                    .disabled(model.busy != nil || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("inbox.suggestion.editConfirm")
                }
            }
        }
        .presentationDetents([.medium, .large])
    }

    private func instruct() {
        let w = words
        Task { if await model.instruct(suggestion, w).map(Self.closes) == true { dismiss() } }
    }

    /// Done, or the suggestion moved on (the card says 「建议已更新」 and shows the new one): the editor closes. A failure
    /// keeps it open with the words, to try again.
    private static func closes(_ o: InboxSuggestionOutcome) -> Bool {
        if case .failed = o { return false }
        return true
    }
}

// MARK: - chain

/// The chain: collapsed to the newest entry with 'show all (N)'; each row time · icon · words, and a tap goes
/// where its link says (the source at that message, a conversation, another item) or opens its detail in place.
struct InboxChainSection: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openURL) private var openURL
    @Environment(\.pushInboxChat) private var pushChat
    var item: InboxItem
    var sourceShown = false
    var openItem: ((InboxItem) -> Void)?
    var openSource: ((InboxSourceRoute) -> Void)?
    @State private var all = false
    @State private var opened: Set<String> = []
    /// The whole chain of an item listed with part of it (`InboxItem.chainCut`, #4742), read when the section appears.
    @State private var whole: [InboxItem.ChainEntry]?

    var body: some View {
        let chain = item.chainCut ? whole ?? item.chain ?? [] : item.chain ?? []
        let count = max(chain.count, item.chainCount ?? 0)
        if !chain.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                HStack(spacing: Theme.Space.s) {
                    InboxBlockTitle(title: String(localized: "Timeline [Inbox Event Views 49991a]"))
                    Spacer(minLength: 0)
                    if count > 1 {
                        Button(all ? String(localized: "Collapse") : String(localized: "Show all (\(count))")) {
                            withAnimation(Theme.Motion.layout) { all.toggle() }
                        }
                        .font(Theme.Font.captionMedium)
                        .lineLimit(1)
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("inbox.chain.all")
                    }
                }
                ForEach(all ? chain : Array(chain.suffix(1))) { e in
                    entry(e)
                }
            }
            .accessibilityIdentifier("inbox.chain")
            .task(id: CutChain(key: item.key, count: item.chainCount)) { await readWhole() }
        }
    }

    private struct CutChain: Hashable {
        var key: String
        var count: Int?
    }

    /// A cut chain's entries from the item itself; on failure the newest entry stays, as listed.
    private func readWhole() async {
        guard item.chainCut, let api = model.api?.manager(model.itemManager(item.manager)) else { return }
        if let full = try? await api.inboxItem(item.id).chain, !Task.isCancelled { whole = full }
    }

    @ViewBuilder private func entry(_ e: InboxItem.ChainEntry) -> some View {
        let target = InboxChainTarget.of(e, sourceShown: sourceShown)
        let open = opened.contains(e.id)
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                tappable(e, target) { line(e, target: target, open: open) }
                // An entry that goes somewhere and has messages too: they open here with the chevron.
                if target.navigates && e.expandable {
                    Button { toggle(e) } label: {
                        ThemedIcon(open ? Theme.Icon.collapse : Theme.Icon.expand)
                            .font(Theme.Font.labelHeavy).foregroundStyle(.secondary)
                            .scaledFrame(width: Theme.Size.tapTarget, alignment: .trailing)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(open ? String(localized: "Collapse") : String(localized: "Expand"))
                }
            }
            if open { expansion(e).transition(.opacity) }
        }
        .padding(.vertical, Theme.Space.xs)
    }

    private func line(_ e: InboxItem.ChainEntry, target: InboxChainTarget, open: Bool) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            Text(LarkTime.short(e.at)).font(Theme.Font.caption).foregroundStyle(.secondary).monospacedDigit().lineLimit(1).fixedSize()
            ThemedIcon(InboxEventText.symbol(e))
                .font(Theme.Font.label)
                .foregroundStyle(tint(e))
                .scaledFrame(width: Theme.Size.iconXS)
            Text(e.text.isEmpty ? e.kind : e.text)
                .font(Theme.Font.secondary)
                .foregroundStyle(.primary)
                .lineLimit(open ? nil : 2)
                .frame(maxWidth: .infinity, alignment: .leading)
            switch target {
            case .inline:
                ThemedIcon(open ? Theme.Icon.collapse : Theme.Icon.expand).font(Theme.Font.labelHeavy).foregroundStyle(.tertiary)
            case .nothing:
                EmptyView()
            default:
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelHeavy).foregroundStyle(.tertiary)
            }
        }
        .contentShape(.rect)
    }

    /// The row as what it opens: a page pushed on the stack, a conversation, a link; or itself expanding.
    @ViewBuilder private func tappable<L: View>(_ e: InboxItem.ChainEntry, _ target: InboxChainTarget, @ViewBuilder label: () -> L) -> some View {
        switch target {
        case .source(let position, let uid):
            let route = InboxSourceRoute(item: item, position: position, uid: uid)
            if let openSource {
                Button { openSource(route) } label: { label() }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("inbox.chain.source")
            } else {
                NavigationLink(value: route) { label() }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("inbox.chain.source")
            }
        case .url(let u):
            Button { openURL(u) } label: { label() }.buttonStyle(.plain)
        case .session(let key, let manager):
            Button { InboxEventOpen.session(key, manager: manager, owner: item.manager, model: model, push: pushChat) } label: { label() }
                .buttonStyle(.plain)
                .accessibilityIdentifier("inbox.chain.session")
        case .item(let id):
            if let it = InboxStore.shared.items.first(where: { $0.key == InboxItem.key(id: id, manager: item.manager) }) {
                if let openItem {
                    Button { openItem(it) } label: { label() }.buttonStyle(.plain)
                } else {
                    NavigationLink(value: it) { label() }.buttonStyle(.plain)
                }
            } else {
                label() // not in the inbox any more
            }
        case .inline:
            Button { toggle(e) } label: { label() }.buttonStyle(.plain)
                .accessibilityIdentifier("inbox.chain.expand")
        case .nothing:
            label()
        }
    }

    private func toggle(_ e: InboxItem.ChainEntry) {
        withAnimation(opened.contains(e.id) ? Theme.Motion.conceal : Theme.Motion.reveal) {
            if opened.contains(e.id) { opened.remove(e.id) } else { opened.insert(e.id) }
        }
    }

    /// What opens in place: the batch's messages (who: what), or the detail (the tier and reason, the words).
    private func expansion(_ e: InboxItem.ChainEntry) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            ForEach(Array((e.messages ?? []).enumerated()), id: \.offset) { _, m in
                Text(m.sender.map { String(localized: "\($0): \(m.text)", comment: "Who wrote it: what they wrote") } ?? m.text)
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let d = e.detail, !d.isEmpty {
                Text(d).font(Theme.Font.caption).foregroundStyle(.secondary).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(Theme.Space.s)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control))
    }

    private func tint(_ e: InboxItem.ChainEntry) -> Color {
        switch e.kind {
        case "failed", "rejected": Theme.danger
        case "confirmed", "sent", "concluded", "done": Theme.positive
        case "handling", "agent", "instructed": Theme.ai
        default: .secondary
        }
    }
}

/// related items: other items this one has to do with, each going its own way; a tap opens that item.
struct InboxRelatedSection: View {
    var item: InboxItem
    var openItem: ((InboxItem) -> Void)?

    var body: some View {
        let related = item.related ?? []
        if !related.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                InboxBlockTitle(title: String(localized: "Related"))
                ForEach(related) { r in
                    let label = HStack(spacing: Theme.Space.s) {
                        ThemedIcon(Theme.Icon.link).font(Theme.Font.label).foregroundStyle(.secondary)
                            .scaledFrame(width: Theme.Size.iconXS)
                        Text(r.title).font(Theme.Font.secondary).foregroundStyle(.primary).lineLimit(1)
                        Spacer(minLength: 0)
                        ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelHeavy).foregroundStyle(.tertiary)
                    }
                    .padding(.vertical, Theme.Space.xs)
                    .contentShape(.rect)
                    if let it = InboxStore.shared.items.first(where: { $0.key == InboxItem.key(id: r.id, manager: item.manager) }) {
                        if let openItem {
                            Button { openItem(it) } label: { label }.buttonStyle(.plain)
                        } else {
                            NavigationLink(value: it) { label }.buttonStyle(.plain)
                        }
                    } else {
                        label.opacity(0.6)
                    }
                }
            }
        }
    }
}

/// Opening a chain's conversation: the processing one is the assistant's (Persona's own conversations), a sub-session
/// an Agent's. On the iPhone's shell either is pushed over the item instead (`pushInboxChat`).
@MainActor enum InboxEventOpen {
    /// `owner`: the item's manager, which a conversation not listed yet is read through (`InboxChatRoute`, or the
    /// Persona page's `assistantItemManagers`).
    static func session(_ key: SessionKey, manager: Bool, owner: String?, model: AppModel, push: ((InboxChatRoute) -> Void)? = nil) {
        if let push {
            push(InboxChatRoute(key: model.inHub(key), manager: owner))
        } else if manager && model.session(key) == nil {
            model.openAssistantConversation(model.inHub(key), manager: owner)
        } else {
            model.open(key)
        }
    }
}

/// A conversation an inbox item links to (the AI's handling, an Agent it started), pushed over the item. `manager`: the
/// item's, which a conversation not listed yet is read for.
struct InboxChatRoute: Hashable {
    var key: SessionKey
    var manager: String?
}

extension EnvironmentValues {
    /// The iPhone shell's Assistant stack: a conversation an inbox item links to goes on top of the item, so Back comes
    /// back to it (#3114). Nil elsewhere, where the conversation opens on its own page.
    @Entry var pushInboxChat: ((InboxChatRoute) -> Void)? = nil
}

// MARK: - view original

/// The item's source page (the Feishu chat, the mail, what a plugin draws), at a chain entry's message.
struct InboxSourceRoute: Hashable {
    var item: InboxItem
    var position: String?
    var uid: UInt32?
}

/// The source as the inbox detail drew it before events: 'view original', a chain entry's link.
struct InboxSourcePage: View {
    var route: InboxSourceRoute
    var accounts: [MailAccount]
    @Environment(AppModel.self) private var model
    @State private var slot: InboxItemSlotModel

    init(route: InboxSourceRoute, accounts: [MailAccount]) {
        self.route = route
        self.accounts = accounts
        _slot = State(initialValue: InboxItemSlotModel(item: route.item))
    }

    private var item: InboxItem { route.item }

    var body: some View {
        InboxItemSlotContent(item: item, slot: slot) { content }
            .navigationTitle(item.title)
    }

    @ViewBuilder private var content: some View {
        if item.doc != nil {
            InboxDocDetailView(item: item)
        } else if item.connector != nil {
            InboxConnectorDetailView(item: item)
        } else if let m = item.moment {
            LarkMomentsPostView(postID: m.postID, pushed: true, manager: model.linkManager(item.manager))
        } else if let c = conversation {
            LarkChatView(conversation: c, manager: model.linkManager(item.manager))
        } else if let m = mail {
            MailDetailView(summary: m, accounts: accounts)
        } else {
            EmptyStateView(symbol: Theme.Icon.info, title: item.title, message: item.preview)
        }
    }

    /// The Feishu chat, at the link's message when it names one.
    private var conversation: LarkConversation? {
        guard var c = item.larkConversation else { return nil }
        if let p = route.position { c.focus = p }
        return c
    }

    /// The mail the link names, else the thread's newest.
    private var mail: MailSummary? {
        guard let uid = route.uid, let m = item.mail else { return item.mailSummary }
        return MailSummary(accountID: m.accountID, mailbox: m.mailbox, uid: uid, messageID: uid == m.uids.last ? m.messageID : nil,
                           subject: item.title, from: m.from, date: item.updatedAt, snippet: item.preview, seen: true)
    }
}

// MARK: - iPhone

/// One event opened on the iPhone: the summary with 'view original', the processing state, recap, the suggestion and its buttons,
/// the chain, the thread line and related items; Hand to AI (with Draft Reply among its chips) is the bar along the bottom
/// (`InboxHandOffBar`). The source is a push away (view original, the received entry).
struct InboxEventPage: View {
    var item: InboxItem
    var accounts: [MailAccount]
    var draft: () -> Void
    @State private var suggestion: InboxSuggestionModel
    @State private var editing = false
    @State private var line: InboxTree?

    init(item: InboxItem, accounts: [MailAccount], draft: @escaping () -> Void) {
        self.item = item
        self.accounts = accounts
        self.draft = draft
        _suggestion = State(initialValue: InboxSuggestionModel.live(item.id, manager: item.manager))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Theme.Space.l) {
                header
                InboxEventBlocks(item: item, model: suggestion, edit: { editing = true })
                InboxLineSection(tree: line)
            }
            .padding(.horizontal, Theme.Space.page)
            .padding(.vertical, Theme.Space.l)
            .frame(maxWidth: Theme.Size.readable, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
        .scrollDismissesKeyboard(.interactively)
        .safeAreaInset(edge: .bottom, spacing: 0) {
            InboxHandOffBar(item: item, draft: canDraft ? draft : nil,
                            draftTitle: item.agentDraft == nil ? nil : String(localized: "View Draft"))
        }
        .themedPage()
        .inboxLine(item, tree: $line)
        .sheet(isPresented: $editing) {
            if let s = item.currentSuggestion {
                InboxSuggestionEditor(item: item, suggestion: s, model: suggestion)
            }
        }
        #if DEBUG
        // `-showInboxSuggestionEdit YES`: the editor of the suggestion (screenshots).
        .task { if UserDefaults.standard.bool(forKey: "showInboxSuggestionEdit"), item.currentSuggestion != nil { editing = true } }
        #endif
    }

    /// the summary, and 'view original' to the source.
    private var header: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if let s = item.summary, !s.isEmpty {
                (Text(Image(systemName: Theme.Icon.ai)).foregroundStyle(Theme.ai) + Text(" " + s))
                    .font(Theme.Font.heading)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            HStack(spacing: Theme.Space.s) {
                Text(InboxEventPage.from(item)).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                Spacer(minLength: 0)
                NavigationLink(value: InboxSourceRoute(item: item, position: nil, uid: nil)) {
                    Label("View original", systemImage: Theme.Icon.openDocument).font(Theme.Font.captionMedium).lineLimit(1).fixedSize()
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("inbox.viewSource")
            }
        }
    }

    /// Where it came from: Feishu · a name / the mail account.
    static func from(_ it: InboxItem) -> String {
        let source = it.isLark ? String(localized: "Lark [Inbox Event Views 7e4419]") : it.accountName
        return [source, it.title].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    /// Draft Reply is offered when the item can be answered from here and no suggested reply is there already.
    private var canDraft: Bool {
        let canReply = item.connector.map { $0.canReply == true } ?? true
        return canReply && item.currentSuggestion?.kind != "reply" && !item.isDone
    }
}
