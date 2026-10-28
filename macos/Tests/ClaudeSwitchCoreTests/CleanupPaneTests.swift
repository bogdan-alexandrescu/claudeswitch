import Foundation
import Testing
@testable import ClaudeSwitchCore

// Settings → Cleanup after QA (2026-10-28): Undo's answer says what came
// back rather than what moved, an undo that is running names its run, and
// a plan says what it did not judge: the folders macOS guards and the
// pull requests it could not read.

@Suite struct CleanupUndoTests {
    @Test func testAnUndoSaysWhatCameBackNotWhatMoved() throws {
        var r = try #require(CleanRun(JSON(data: try fixture("cli-clean-undo.json"))!))
        #expect(r.kind == .applied, "the answer alone does not say which call made it")
        r.kind = .undone
        #expect(r.summary == "Put back 2 of 2 from run 20261025-160212.")
        #expect(!r.summary.contains("Trash"))
        #expect(r.failures.isEmpty, "the run's own failures are not the undo's")
    }

    @Test func testAnUndoThatLeftSomeInTheTrashSaysHowMany() throws {
        var r = try #require(CleanRun(JSON(data: try fixture("cli-clean-apply.json"))!))
        r.kind = .undone
        #expect(r.summary == "Put back 1 of 2 from run 20261025-160212.")
    }

    @Test func testARunStillSaysWhatMovedAndWhatFailed() throws {
        let r = try #require(CleanRun(JSON(data: try fixture("cli-clean-apply.json"))!))
        #expect(r.kind == .applied)
        #expect(r.summary.hasPrefix("Run 20261025-160212 moved 512 MB to the Trash"))
        #expect(r.failures == r.failed && r.failures.count == 1)
    }

    @Test func testAnUndoRunningNamesItsRun() {
        let key = CleanUndo.key("20261025-160212")
        #expect(CleanUndo.running([key, "clean:plan"]) == "20261025-160212")
        #expect(CleanUndo.running(["clean:apply", "clean:plan"]) == nil)
        #expect(CleanUndo.progress("20261025-160212") == "Putting back run 20261025-160212…")
    }
}

@Suite struct CleanupPlanNotesTests {
    func plan() throws -> CleanPlan {
        try #require(CleanPlan(JSON(data: try fixture("cli-clean-plan-guarded.json"))!))
    }

    @Test func testAPlanSaysWhatItDidNotLookIn() throws {
        let p = try plan()
        #expect(p.guarded == ["/Users/me/Desktop/taxes", "/Users/me/Documents"])
        #expect(p.notLookedIn(home: "/Users/me") == "Not looked in (macOS asks first): ~/Desktop/taxes, ~/Documents")
    }

    @Test func testAPlanSaysWhenPullRequestsWereNotChecked() throws {
        let p = try plan()
        #expect(p.kept == 4 && p.removable.count == 1)
        #expect(p.warnings == ["Pull requests not checked: gh not found, so 3 kept may be merged already."])
    }

    @Test func testAPlanThatLookedEverywhereSaysNothingMore() throws {
        let p = try #require(CleanPlan(JSON(data: try fixture("cli-clean-plan.json"))!))
        #expect(p.guarded.isEmpty && p.notLookedIn() == nil && p.warnings.isEmpty)
    }

    @Test func testALongListOfGuardedFoldersIsCut() {
        let p = CleanPlan(items: [], guarded: (1...8).map { "/Users/me/Documents/p\($0)" })
        #expect(p.notLookedIn(home: "/Users/me") == "Not looked in (macOS asks first): ~/Documents/p1, "
            + "~/Documents/p2, ~/Documents/p3, ~/Documents/p4, ~/Documents/p5 and 3 more")
    }

    @Test func testEachReasonPullRequestsWentUncheckedIsItsOwnLine() {
        let p = CleanPlan(items: [
            CleanItem(kind: "branch", path: "/a", branch: "x", remove: false,
                      why: "1 commit not in main; pull requests not checked: gh: HTTP 401"),
            CleanItem(kind: "branch", path: "/b", branch: "y", remove: false,
                      why: "commits not in main; pull requests not checked: gh not found"),
            CleanItem(kind: "branch", path: "/a", branch: "z", remove: false,
                      why: "2 commits not in main; pull requests not checked: gh: HTTP 401"),
        ])
        #expect(p.warnings == ["Pull requests not checked: gh: HTTP 401, so 2 kept may be merged already.",
                               "Pull requests not checked: gh not found, so 1 kept may be merged already."])
    }
}
