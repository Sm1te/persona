import SwiftUI

struct MachineRoute: Hashable {
    var ref: MachineRef
    init(_ ref: MachineRef) { self.ref = ref }
    /// A machine of the default group, or "group|id" (launch arguments).
    init(id: String) { ref = MachineRef(path: id) }
}

/// One machine's projects (herdr spaces): the busy ones with their agents' counts and latest line, the idle ones
/// below; tap one for its page. Opened from a project page's machine name.
struct MachineSpacesView: View {
    @Environment(AppModel.self) private var model
    var ref: MachineRef

    var body: some View {
        let machine = model.machine(ref)
        // A subtask is its parent's, as the Agents list folds it (`Session.isNestedSubtask`).
        let sessions = model.allSessions.filter { $0.machineRef == ref && $0.state.isLive && !$0.assistant && !$0.isNestedSubtask }
        let groups = SpaceGroup.build(sessions)
        let pinned = groups.filter { model.isPinned($0) }
        // A space with an agent waiting on the user's reply (Awaiting Reply) is not idle (`AgentListOrder.isQuiet`).
        let order = AgentListOrder(isPinned: model.isPinned, isMuted: model.isMuted)
        let active = pinned + groups.filter { !order.isQuiet($0) && !model.isPinned($0) }
        let quiet = groups.filter { order.isQuiet($0) && !model.isPinned($0) }

        List {
            if let machine {
                Section {
                    summary(machine, sessions: sessions, spaces: groups.count)
                }
            }
            if !active.isEmpty {
                Section {
                    ForEach(active) { g in row(g, busy: true) }
                } header: {
                    SectionHeader(title: StateName.working.text, count: active.count, tone: StatusPalette.working)
                }
            }
            if !quiet.isEmpty {
                Section {
                    ForEach(quiet) { g in row(g, busy: false) }
                } header: {
                    SectionHeader(title: String(localized: "Idle"), count: quiet.count, tone: StatusPalette.idle)
                }
            }
            if groups.isEmpty {
                Section {
                    EmptyStateView(symbol: machine?.symbol ?? Theme.Icon.machine, title: String(localized: "No agents running [Machine Spaces View e5db75]"),
                                   message: machine?.online == false ? String(localized: "This machine is offline.") : String(localized: "Start Claude, Codex, or TRAEX in herdr on this machine and it shows up here."))
                }
                .listRowBackground(Color.clear)
            }
        }
        .listStyle(.insetGrouped)
        .themedPage()
        .navigationTitle(machine?.title ?? ref.id)
        .navigationBarTitleDisplayMode(.large)
        .pageToolbar {
            ToolbarItem(placement: .topBarTrailing) {
                // By value, like the chats the details page opens (MachineDetailRoute).
                NavigationLink(value: MachineDetailRoute(ref)) { Label("Machine Details", systemImage: Theme.Icon.info) }
            }
        }
    }

    private func summary(_ m: Machine, sessions: [Session], spaces: Int) -> some View {
        CountsHeading(spacing: Theme.Space.m) {
            ThemedIcon(m.symbol)
                .font(Theme.Font.title)
                .foregroundStyle(m.online ? Theme.accent : .secondary)
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                .background((m.online ? Theme.accent : Color.secondary).opacity(0.12), in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                HStack(spacing: Theme.Space.sm) {
                    Circle().fill(m.online ? Theme.positive : Color.secondary).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                    Text(m.online ? String(localized: "Online") : String(localized: "Offline [Machine Entity be1b4f]")).font(Theme.Font.secondaryStrong)
                    if let h = m.hostname, !h.isEmpty { Text(h).font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(1) }
                }
                // Two counts, two plural rules: localize each piece, join with a neutral separator.
                Text(verbatim: "\(String(localized: "\(spaces) projects")) · \(String(localized: "\(sessions.count) agents"))").font(Theme.Font.label).foregroundStyle(.secondary)
            }
        } counts: {
            StateCounts(group: SpaceGroup(id: "m-" + m.id, machineID: m.id, spaceID: "-", name: m.name, index: 0, sessions: sessions))
        }
        .padding(.vertical, Theme.Space.xxs)
    }

    /// A project: opens its page. Agents outside any herdr space have no page: the row opens the agent itself.
    @ViewBuilder private func row(_ g: SpaceGroup, busy: Bool) -> some View {
        if g.spaceID == "-" {
            ForEach(g.sessions) { s in
                NavigationLink(value: s.key) { SlotRow(session: s, muted: model.isMuted(s.key)).equatable() }
            }
        } else {
            NavigationLink(value: SpaceRoute(id: g.id)) {
                if busy { BusySpaceRow(group: g, pinned: model.isPinned(g), muted: model.isMuted(g)) }
                else { QuietSpaceRow(group: g, machine: nil, showMachine: false) }
            }
        }
    }
}

/// A project with something going on: its name and counts, then the line of the agent that most wants attention.
private struct BusySpaceRow: View {
    var group: SpaceGroup
    var pinned: Bool
    var muted: Bool

    var body: some View {
        let lead = group.sessions.min { ($0.state.priority, -($0.updatedAt ?? .distantPast).timeIntervalSince1970) < ($1.state.priority, -($1.updatedAt ?? .distantPast).timeIntervalSince1970) }
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            CountsHeading {
                ThemedIcon(Theme.Icon.spaceFilled).foregroundStyle(muted ? Color.secondary : group.state.color)
                Text(group.name).font(Theme.Font.heading).lineLimit(1)
                if pinned { ThemedIcon(Theme.Icon.pinned).font(Theme.Font.label).foregroundStyle(Theme.accent).accessibilityLabel("Pinned [Personal Agent List View fb47db]") }
                if muted { ThemedIcon(Theme.Icon.muted).font(Theme.Font.label).foregroundStyle(.secondary).accessibilityLabel("Muted [Chats View b66705]") }
            } counts: {
                StateCounts(group: group).opacity(muted ? 0.6 : 1)
            }
            if let lead {
                HStack(spacing: Theme.Space.sm) {
                    Text(lead.slotLabel ?? lead.displayTitle).fontWeight(.medium).lineLimit(1).layoutPriority(1)
                    if let t = lead.lastText, !t.isEmpty { Text(t).foregroundStyle(.secondary).lineLimit(1) }
                    Spacer(minLength: 4)
                    Text(Fmt.relative(group.updatedAt)).foregroundStyle(.tertiary).lineLimit(1).fixedSize()
                }
                .font(Theme.Font.label)
                .padding(.leading, 28)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        .opacity(muted ? 0.8 : 1)
    }
}
