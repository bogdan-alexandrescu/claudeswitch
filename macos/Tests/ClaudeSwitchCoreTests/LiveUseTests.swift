import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Lane 15, the owner's live use: what a pool lists, and alerts shown twice.
@Suite struct LiveUseTests {
    func list() throws -> ProfileList {
        try #require(ProfileList(data: try encode([
            "profiles": [["name": "default", "declared": true, "pool": ["a1", "lab-1"], "listed": ["a1"]],
                         ["name": "work", "declared": true, "pool": ["w1"], "listed": ["w1"]],
                         ["name": "lab", "declared": true, "pool": [String](), "listed": [String]()]],
            "ghosts": [Any]()])))
    }

    @Test func testListedIsRead() throws {
        let l = try list()
        #expect(l.profile("default")?.listed == ["a1"])
        let old = try #require(ProfileList(data: try encode(["profiles": [["name": "default", "pool": ["a1"]]]])))
        #expect(old.profile("default")?.listed == nil, "an older CLI does not say")
    }

    // MARK: one action, one alert

    @Test func testAPostShowsOnTheSurfaceThatActedOnly() {
        var inbox = Inbox<String>()
        inbox.post("default now uses work-team", to: .settings)
        #expect(inbox.item(for: .settings) == "default now uses work-team")
        #expect(inbox.item(for: .popover) == nil, "the other window does not show it again")
        #expect(inbox.count == 1)
        inbox.post(nil, to: .settings)
        #expect(inbox.item(for: .settings) == nil && inbox.count == 0)
    }

    @Test func testEachSurfaceKeepsItsOwn() {
        var inbox = Inbox<String>()
        inbox.post("a", to: .popover)
        inbox.post("b", to: .settings)
        #expect(inbox.item(for: .popover) == "a" && inbox.item(for: .settings) == "b")
    }
}
