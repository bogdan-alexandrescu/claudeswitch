import CoreGraphics
import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The owner's live test (lane 15): on a 14" MacBook the status item landed
/// under the notch, and the label read as an error while it loaded.
@Suite struct MenuBarPlacementTests {
    func scratchDefaults() throws -> UserDefaults {
        let name = "claudeswitch-test-\(UUID().uuidString)"
        let d = try #require(UserDefaults(suiteName: name))
        d.removePersistentDomain(forName: name)
        return d
    }

    @Test func testFirstLaunchSeedsAPositionNearTheRightEdge() throws {
        let d = try scratchDefaults()
        #expect(StatusItemPlacement.seed(d), "nothing stored: seeded")
        #expect(d.double(forKey: StatusItemPlacement.preferredPositionKey) == StatusItemPlacement.defaultPosition)
        #expect(StatusItemPlacement.preferredPositionKey == "NSStatusItem Preferred Position Item-0")
        #expect(StatusItemPlacement.defaultPosition == 300)
    }

    @Test func testAUsersOwnPositionIsNeverOverwritten() throws {
        let d = try scratchDefaults()
        d.set(812.0, forKey: StatusItemPlacement.preferredPositionKey) // a ⌘-drag
        #expect(!StatusItemPlacement.seed(d))
        #expect(d.double(forKey: StatusItemPlacement.preferredPositionKey) == 812)
    }

    // A 1512-pt menu bar with a notch between x=662 and x=850 (the owner's
    // item sat at 685–840).
    let screen = CGRect(x: 0, y: 0, width: 1512, height: 982)
    let left = CGRect(x: 0, y: 950, width: 662, height: 32)
    let right = CGRect(x: 850, y: 950, width: 662, height: 32)

    @Test func testAnItemInTheNotchGapIsHidden() {
        let item = CGRect(x: 685, y: 950, width: 155, height: 32)
        #expect(StatusItemPlacement.hiddenByNotch(item: item, safeAreaTop: 32, left: left, right: right))
        let straddling = CGRect(x: 600, y: 950, width: 100, height: 32)
        #expect(StatusItemPlacement.hiddenByNotch(item: straddling, safeAreaTop: 32, left: left, right: right))
    }

    @Test func testAnItemBesideTheNotchOrOnAScreenWithoutOneIsFine() {
        let visible = CGRect(x: 1140, y: 950, width: 155, height: 32)
        #expect(!StatusItemPlacement.hiddenByNotch(item: visible, safeAreaTop: 32, left: left, right: right))
        let item = CGRect(x: 685, y: 950, width: 155, height: 32)
        #expect(!StatusItemPlacement.hiddenByNotch(item: item, safeAreaTop: 0, left: nil, right: nil), "no notch")
        #expect(!StatusItemPlacement.hiddenByNotch(item: item, safeAreaTop: 32, left: nil, right: right),
                "no auxiliary areas: the gap is unknown, so nothing is claimed")
    }

    // MARK: the label while loading

    @Test func testLoadingIsNotAProblem() {
        let t = MenuTitle.of(nil, compact: false, profile: nil, problem: nil)
        #expect(!t.problem, "no data yet is loading, not an error")
        #expect(t.loading)
        #expect(t.text == "cs …")
        #expect(MenuTitle.of(nil, compact: true, profile: nil, problem: nil).text == "")
    }

    @Test func testOnlyABinaryProblemIsAProblem() {
        #expect(MenuTitle.of(nil, compact: false, profile: nil, problem: .missing).problem)
        #expect(MenuTitle.of(nil, compact: false, profile: nil, problem: .tooOld(version: "0.4.0")).problem)
        let failed = MenuTitle.of(nil, compact: false, profile: nil, problem: .failed("why failed"))
        #expect(!failed.problem && !failed.loading && failed.text == "cs")
    }

    @Test func testDataWinsOverAStaleProblem() throws {
        let s = try fixtureSnapshot()
        let t = MenuTitle.of(s, compact: false, profile: nil, problem: .failed("a later read failed"))
        #expect(t == MenuTitle.of(s, compact: false, profile: nil))
    }
}
