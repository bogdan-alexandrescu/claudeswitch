import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The grouped Accounts pane is one list: on macOS a row dragged in a List
/// only drops within the ForEach it came from, so with one ForEach per pool
/// no other pool ever offered a drop spot (owner, 2026-10-20). Headers are
/// rows; a move across one is a move to that pool, a move within a pool a
/// reorder.
@Suite struct PoolRowsTests {
    func sections() throws -> [PoolSection] {
        PoolLayout.sections(try sharedPools(),
                            order: ["personal", "team-1", "team-2", "team-3", "ops-1", "loose-1"],
                            profiles: try sharedProfiles())
    }

    @Test func testEachSectionIsAHeaderThenItsAccountsOrItsEmptyRow() throws {
        let rows = PoolLayout.rows(try sections())
        #expect(rows.map(\.id) == [
            "h:pool:personal", "a:personal",
            "h:pool:team", "a:team-1", "a:team-2", "a:team-3",
            "h:pool:ops", "a:ops-1",
            "h:pool:spare", "e:pool:spare",
            "h:unlisted", "a:loose-1",
        ])
        #expect(rows.allSatisfy { $0.movable == ($0.account != nil) })
    }

    @Test func testAMoveWithinAPoolIsAReorder() throws {
        let rows = PoolLayout.rows(try sections())
        // team-3 (row 5) to before team-1 (row 3).
        #expect(PoolLayout.move(rows, from: [5], to: 3) == .reorder(section: "pool:team", from: [2], to: 0))
        // team-1 to after team-3: the end of team, just before ops's header.
        #expect(PoolLayout.move(rows, from: [3], to: 6) == .reorder(section: "pool:team", from: [0], to: 3))
    }

    @Test func testAMoveAcrossAHeaderIsAMoveToThatPool() throws {
        let rows = PoolLayout.rows(try sections())
        // team-2 (row 4) to just under ops's header (row 6 → before row 7).
        #expect(PoolLayout.move(rows, from: [4], to: 7) == .drop(account: "team-2", onto: "pool:ops"))
        // personal to the empty spare pool, before or after its empty row.
        #expect(PoolLayout.move(rows, from: [1], to: 9) == .drop(account: "personal", onto: "pool:spare"))
        #expect(PoolLayout.move(rows, from: [1], to: 10) == .drop(account: "personal", onto: "pool:spare"))
        // ops-1 to the end, into the accounts in no pool.
        #expect(PoolLayout.move(rows, from: [7], to: rows.count) == .drop(account: "ops-1", onto: "unlisted"))
    }

    @Test func testNothingElseMoves() throws {
        let rows = PoolLayout.rows(try sections())
        #expect(PoolLayout.move(rows, from: [1], to: 0) == PoolRowMove.none, "above the first header is no pool")
        #expect(PoolLayout.move(rows, from: [2], to: 6) == PoolRowMove.none, "a header does not move")
        #expect(PoolLayout.move(rows, from: [9], to: 1) == PoolRowMove.none, "nor an empty row")
        #expect(PoolLayout.move(rows, from: [3], to: 4) == PoolRowMove.none, "back to where it was")
    }
}
