import BeautifulUI
import SwiftUI

/// A session's Subtasks row (Android's "Subtasks (N)", `CodexTasks`): under the session's row, its Codex subagents or the
/// agents it created a level in when opened.
/// Laid out as the list's other fold lines: By Repository's tree line, Compact List's line, else the idle agents' row.
struct CodexSubtasksRow: View {
    /// `card`: a row of a quiet space's card (the capsules), `last` ending it.
    enum Style { case regular, compact, tree(level: Int), card(last: Bool) }
    var count: Int
    var open: Bool
    var style: Style
    @Environment(\.buiTheme) private var theme

    private var title: String { String(localized: "Subtasks (\(count))") }

    var body: some View {
        Group {
            switch style {
            case .tree(let level):
                #if os(iOS)
                RepoTreeLine(level: level) {
                    ThemedIcon(Theme.Icon.subagent).font(Theme.Font.caption).foregroundStyle(.tertiary)
                } content: {
                    HStack(spacing: Theme.Space.xs) {
                        Text(title).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                        Spacer(minLength: 0)
                        chevron.font(Theme.Font.captionStrong)
                    }
                }
                #else
                regular
                #endif
            case .compact:
                HStack(spacing: CompactList.gap) {
                    ThemedIcon(Theme.Icon.subagent)
                        .font(CompactList.metaStrong)
                        .foregroundStyle(.secondary)
                        .scaledFrame(width: CompactList.mark)
                    Text(title).font(CompactList.title).foregroundStyle(.secondary).lineLimit(1)
                    Spacer(minLength: 0)
                    chevron.font(CompactList.metaStrong)
                }
            case .regular:
                regular
            case .card(let last):
                regular
                    .padding(.horizontal, Theme.Space.m)
                    .modifier(TouchHeight()) // a finger's height with the card's color under all of it (#3753)
                    .background(UnevenRoundedRectangle(bottomLeadingRadius: last ? theme.radii.card : 0,
                                                       bottomTrailingRadius: last ? theme.radii.card : 0, style: .continuous)
                        .fill(theme.colors.surface))
            }
        }
        .contentShape(.rect)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
        .accessibilityValue(open ? String(localized: "Expanded") : String(localized: "Collapsed"))
        .accessibilityAddTraits(.isButton)
    }

    /// The idle agents' row's layout (`IdleFoldRow`).
    private var regular: some View {
        HStack(spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.subagent).foregroundStyle(.secondary)
            Text(title).font(Theme.Font.secondaryMedium).foregroundStyle(.secondary).lineLimit(2) // its count, at large sizes too
            Spacer(minLength: 0)
            chevron.font(Theme.Font.captionStrong)
        }
        .padding(.vertical, Theme.Space.xs)
    }

    private var chevron: some View {
        ThemedIcon(open ? Theme.Icon.collapse : Theme.Icon.expand).foregroundStyle(.tertiary)
    }
}
