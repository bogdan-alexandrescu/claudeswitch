import Foundation
import Testing
@testable import ClaudeSwitchCore

// Settings → Cleanup (owner decisions 2026-10-25): the answers of
// `claudeswitch clean`, the clean_* settings in their own pane, the bool
// type, and the confirmation Clean now asks.

@Suite struct CleanupModelTests {
    @Test func testAPlanGroupsWhatWouldGoByKindInTheCLIsOrder() throws {
        let p = try #require(CleanPlan(JSON(data: try fixture("cli-clean-plan.json"))!))
        #expect(p.items.count == 5)
        #expect(p.removable.count == 4 && p.kept == 1)
        #expect(p.groups.map(\.kind) == ["worktree", "prune", "branch", "scratch"])
        #expect(p.bytes == 536870912 + 3145728)
        #expect(p.groups.first?.title == "Worktrees")
        #expect(p.kinds["remote_branches"] == false && p.kinds["worktrees"] == true)
        let branch = try #require(p.items.first { $0.kind == "branch" })
        #expect(branch.place(home: "/Users/me") == "~/src/app: feat/x")
        let wt = p.items[0]
        #expect(wt.place(home: "/Users/me") == "~/src/app/.claude/worktrees/agent-a1 [lane/a1]")
    }

    @Test func testARunSaysWhatMovedWhatStayedAndWhatFailed() throws {
        let r = try #require(CleanRun(JSON(data: try fixture("cli-clean-apply.json"))!))
        #expect(r.id == "20261025-160212")
        #expect(r.done.count == 2 && r.undone == 1)
        #expect(r.skipped.count == 1 && r.failed.count == 1)
        #expect(r.summary.hasPrefix("Run 20261025-160212 moved 512 MB to the Trash: 1 worktrees, 1 local branches."))
        #expect(r.summary.contains("1 changed since the plan and stayed") && r.summary.contains("1 failed"))
        #expect(r.failed[0].contains("another volume"))
    }

    @Test func testTheRunsListSaysWhichCanBeUndone() throws {
        let runs = try #require(CleanRunSummary.list(JSON(data: try fixture("cli-clean-runs.json"))!))
        #expect(runs.map(\.id) == ["20261025-160212", "20261024-090000"])
        #expect(runs[0].undoable && !runs[1].undoable, "a run put back in full has nothing to undo")
    }

    @Test func testSizesReadAsTheCLIPrintsThem() {
        #expect(Format.bytes(536870912) == "512 MB")
        #expect(Format.bytes(83_400_000_000) == "77.7 GB")
        #expect(Format.bytes(3_000) == "3 KB")
        #expect(Format.bytes(12) == "12 B")
    }

    @Test func testCleanNowAsksWithTheTotalAndThatItCanBePutBack() throws {
        let p = try #require(CleanPlan(JSON(data: try fixture("cli-clean-plan.json"))!))
        let c = Confirm.cleanApply(p)
        #expect(c.title == "Move 4 items (515 MB) to the Trash?")
        #expect(c.message.hasPrefix("1 worktrees, 1 records of worktrees whose folder is gone, 1 local branches, "
                                    + "1 ended sessions' scratch."))
        #expect(c.message.contains("Undo") && c.destructive)
    }
}

@Suite struct CleanupSettingsTests {
    func schema() throws -> ConfigSchema {
        try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
    }

    @Test func testTheCleanSettingsHaveAPaneOfTheirOwn() throws {
        let sc = try schema()
        let cleanup = sc.settings(in: .cleanup).map(\.key)
        #expect(cleanup == SettingsPane.cleanupScheduleKeys + SettingsPane.cleanupKindKeys)
        #expect(!sc.settings(in: .advanced).contains { $0.key.hasPrefix("clean") })
    }

    @Test func testBoolSettingsTakeTrueOrFalse() throws {
        let on = try #require(try schema().setting("clean"))
        #expect(on.type == "bool" && on.defaultValue == "false")
        #expect(on.validate("true") == nil && on.validate("false") == nil)
        #expect(on.validate("maybe") == "true or false")
        #expect(SettingNames.value(on, "true") == "on" && SettingNames.value(on, "false") == "off")
        #expect(SettingNames.label("clean_remote_branches") == "Branches on GitHub")
        let remote = try #require(try schema().setting("clean_remote_branches"))
        #expect(remote.defaultValue == "false", "it acts on GitHub: off until turned on")
    }
}

@Suite final class CleanupCLITests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    @Test func testThePlanApplyRunsAndUndoCalls() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-clean-plan")
        _ = try cli.cleanPlan().get()
        #expect(lastCall() == ["clean", "--json"])
        try FakeAppCLI.reply(dir, fixture: "cli-clean-apply")
        _ = try cli.cleanApply().get()
        #expect(lastCall() == ["clean", "--apply", "--json"])
        try FakeAppCLI.reply(dir, fixture: "cli-clean-runs")
        #expect(try cli.cleanRuns().get().count == 2)
        #expect(lastCall() == ["clean", "runs", "--json"])
        try FakeAppCLI.reply(dir, fixture: "cli-clean-apply")
        _ = try cli.cleanUndo("20261025-160212").get()
        #expect(lastCall() == ["clean", "undo", "20261025-160212", "--json"])
    }

    @Test func testAnotherCleanupRunningIsTheCLIsOwnError() throws {
        try FakeAppCLI.reply(dir, text: #"{"error": {"code": "clean_running", "message": "another cleanup is running", "hint": "wait for it to finish, then run this again"}}"#, exit: 1)
        guard case .failure(.app(let e)) = cli.cleanApply() else {
            Issue.record("not an app error")
            return
        }
        #expect(e.code == "clean_running" && e.hint.hasPrefix("wait"))
    }
}
