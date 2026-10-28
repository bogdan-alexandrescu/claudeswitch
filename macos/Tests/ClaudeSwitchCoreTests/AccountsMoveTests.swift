import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Move up / Move down in the grouped Accounts pane: within the account's
/// own section, as a drag there does (D33); every other account keeps its
/// place in the rotation order.
@Suite struct AccountsMoveTests {
    /// Two pools whose accounts take turns in the rotation order.
    let global = ["a", "x", "b", "y", "c"]
    let team = ["a", "b", "c"]
    let ops = ["x", "y"]

    @Test func testAMoveSwapsWithTheNextAccountOfItsOwnPool() {
        #expect(RotationOrder.move(global, section: team, "b", by: -1) == ["b", "x", "a", "y", "c"])
        #expect(RotationOrder.move(global, section: team, "b", by: 1) == ["a", "x", "c", "y", "b"])
        #expect(RotationOrder.move(global, section: ops, "x", by: 1) == ["a", "y", "b", "x", "c"])
        #expect(RotationOrder.move(global, section: ops, "y", by: -1) == ["a", "y", "b", "x", "c"])
    }

    @Test func testItCannotMovePastTheEndsOfItsPool() {
        #expect(RotationOrder.move(global, section: ops, "x", by: -1) == nil,
                "first of its pool, though a comes before it")
        #expect(RotationOrder.move(global, section: ops, "y", by: 1) == nil,
                "last of its pool, though c comes after it")
        #expect(RotationOrder.move(global, section: team, "a", by: -1) == nil)
        #expect(RotationOrder.move(global, section: team, "c", by: 1) == nil)
        #expect(RotationOrder.move(global, section: team, "x", by: 1) == nil, "not in the section")
    }

    @Test func testOneListIsOneSection() {
        let ids = ["a", "b", "c"]
        #expect(RotationOrder.move(ids, section: ids, "b", by: -1) == RotationOrder.move(ids, "b", by: -1))
        #expect(RotationOrder.move(ids, section: ids, "b", by: 1) == RotationOrder.move(ids, "b", by: 1))
        #expect(RotationOrder.move(ids, section: ids, "a", by: -1) == nil)
    }
}
