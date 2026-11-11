import Foundation
import SwiftUI
import Testing
import UIKit
@testable import Persona

/// One count rule and one row state mark (#4538, docs/design/proposals/marks-and-badges.md): a count shows nothing at
/// zero and "99+" past 99 wherever it is drawn, and a dense row's Unread is green, never the Needs You blue.
@MainActor
struct RowMarksTests {
    private static let apple = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()

    private func rgb(_ color: Color, dark: Bool) -> [Double] {
        let c = UIColor(color).resolvedColor(with: UITraitCollection(userInterfaceStyle: dark ? .dark : .light))
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        c.getRed(&r, green: &g, blue: &b, alpha: &a)
        return [Double(r), Double(g), Double(b)]
    }

    @Test func countTextHidesZeroAndCapsPast99() {
        #expect(CountBadge.text(0) == nil)
        #expect(CountBadge.text(-3) == nil)
        #expect(CountBadge.text(1) == "1")
        #expect(CountBadge.text(99) == "99")
        #expect(CountBadge.text(100) == "99+")
        #expect(CountBadge.text(12_345) == "99+")
    }

    @Test func rowMarkColorsFollowTheStatusPalette() {
        #expect(RowStateMark.dot(.needsYou) == StatusPalette.needsYou.dot)
        #expect(RowStateMark.dot(.unread) == StatusPalette.unread.dot)
        for state in [AgentState.working, .idle, .offline, .unknown] {
            #expect(RowStateMark.dot(state) == nil, "\(state)")
        }
    }

    /// MB-03: the sidebar drew Unread in the accent, which in the default style is the Needs You blue.
    @Test func unreadIsGreenNotTheNeedsYouBlue() throws {
        let unread = try #require(RowStateMark.dot(.unread))
        let needs = try #require(RowStateMark.dot(.needsYou))
        for dark in [false, true] {
            let u = rgb(unread, dark: dark), n = rgb(needs, dark: dark)
            #expect(u[1] > u[2], "unread is not green, dark: \(dark)")
            #expect(n[2] > n[1], "needs you is not blue, dark: \(dark)")
        }
    }

    /// The rows that drew their own state with private `mark()` copies now go through `RowStateMark`.
    @Test func sidebarRowsUseTheSharedMark() throws {
        for file in ["Persona/Views/Assistant/AssistantSidebar.swift", "Persona/Views/Assistant/AutoConversationsView.swift"] {
            let source = try String(contentsOf: Self.apple.appending(path: file), encoding: .utf8)
            #expect(source.contains("RowStateMark("), "\(file)")
            #expect(!source.contains("private func mark("), "\(file) draws its own state mark again")
            #expect(!source.contains("Circle().fill(Theme.accent)"), "\(file) marks a state in the accent")
            #expect(!source.contains(".solid(Theme.needsYou)"), "\(file) draws a needs-you count solid; use .needs")
        }
    }
}
