import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Lane 15, the owner's live use: moving an account default holds only by
/// D6, and alerts shown twice.
@Suite struct LiveUseTests {
    func list() throws -> ProfileList {
        try #require(ProfileList(data: try encode([
            "profiles": [["name": "default", "declared": true, "pool": ["a1", "aieng-claude1"], "listed": ["a1"]],
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

    @Test func testAnAccountDefaultHoldsOnlyByD6IsAddedNotMoved() throws {
        let l = try list()
        #expect(l.step(adding: "aieng-claude1", to: "work") == .add(account: "aieng-claude1", to: "work"),
                "no pool lists it: add it to work; never remove it from default")
        #expect(l.step(adding: "a1", to: "work") == .move(account: "a1", from: "default", to: "work"))
        #expect(l.step(adding: "w1", to: "lab") == .move(account: "w1", from: "work", to: "lab"))
        #expect(l.step(adding: "w1", to: "work") == .already)
        #expect(l.step(adding: "aieng-claude1", to: "default") == .already)
        #expect(l.step(adding: "nobody", to: "lab") == .add(account: "nobody", to: "lab"))
    }

    @Test func testAnOlderCLIFallsBackToTheEffectivePool() throws {
        let l = try #require(ProfileList(data: try encode([
            "profiles": [["name": "default", "pool": ["a1"]], ["name": "work", "pool": [String]()]]])))
        #expect(l.step(adding: "a1", to: "work") == .move(account: "a1", from: "default", to: "work"))
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
