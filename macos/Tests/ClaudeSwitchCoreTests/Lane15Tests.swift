import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Lane 15: `account list --json` (the plan in the Accounts pane) and
/// `profile remove` (the Profiles pane's "Remove profile…").
@Suite final class Lane15Tests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    // MARK: account list

    @Test func testAccountList() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-account-list")
        let l = try cli.accountList().get()
        #expect(lastCall() == ["account", "list", "--json"])
        #expect(l.accounts.map(\.id) == ["personal", "work-1", "work-2"], "rotation order")

        let w = try #require(l.account("work-1"))
        #expect(w.plan == "Max 20x")
        #expect(w.email == "person1@example.com")
        #expect(w.seat?.hasPrefix("aaaaaaaa") == true)
        #expect(w.profile == "default")
        #expect(w.activeIn == "default")
        #expect(!w.pinned)
        #expect(w.enabled)
        #expect(w.state == "available")
        #expect(w.reading?.fiveHour == 3)
        #expect(w.reading?.sevenDay == 41)

        let p = try #require(l.account("personal"))
        #expect(p.plan == nil)
        #expect(p.profile == "review" && p.activeIn == "review" && p.pinned)
        #expect(p.state == "needs_login")
        #expect(p.reading?.error?.contains("401") == true)

        let r = try #require(l.account("work-2"))
        #expect(r.state == "refused")
        #expect(r.reading?.refusedUntil != nil)
    }

    @Test func testAccountColumns() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list.json")))
        #expect(l.account("work-1")?.planLabel == "Max 20x")
        #expect(l.account("personal")?.planLabel == "—", "an unknown plan is a dash, never a guess")
        #expect(l.account("nobody") == nil)
    }

    @Test func testAccountListIsLenient() throws {
        try FakeAppCLI.reply(dir, text: #"{"accounts": null}"#, exit: 0)
        #expect(try cli.accountList().get().accounts.isEmpty, "Go writes an empty list as null")
        try FakeAppCLI.reply(dir, text: #"{"accounts": [{"id": "x", "state": "something_new", "future": 1}]}"#, exit: 0)
        let x = try #require(try cli.accountList().get().account("x"))
        #expect(x.state == "something_new" && x.plan == nil && x.reading == nil && x.enabled)
    }

    // MARK: profile remove

    @Test func testProfileRemoveAsksTheCLIFirstThenConfirms() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-error-profile-remove-confirm", exit: 1)
        let ask = cli.profileRemove("review", to: nil, confirmed: false)
        #expect(lastCall() == ["profile", "remove", "review", "--json"])
        #expect(ask.failureCode == "confirmation_required")
        #expect(ask.failureMessage?.contains("guard") == true, "the CLI's message names the ghost behaviour")

        try FakeAppCLI.reply(dir, fixture: "cli-profile-remove")
        let r = try cli.profileRemove("review", to: "default", confirmed: true).get()
        #expect(lastCall() == ["profile", "remove", "review", "--to=default", "--yes", "--json"])
        #expect(r.profile == "review")
        #expect(r.to == "default")
        #expect(r.moved == ["personal"])
        #expect(r.ghost?.account == "personal")
        #expect(r.keptDir == "/Users/example/.claude-review")
        #expect(r.keptCredential == nil)
        #expect(!r.daemonRunning)
        #expect(r.daemonLoaded == nil)
        #expect(r.pools["default"]?.contains("personal") == true)

        try FakeAppCLI.reply(dir, fixture: "cli-error-last-profile", exit: 1)
        #expect(cli.profileRemove("default", to: nil, confirmed: true).failureCode == "last_profile")
        #expect(cli.profileRemove("--json", to: nil, confirmed: true).failureCode == "usage")
        #expect(cli.profileRemove("review", to: "-x", confirmed: true).failureCode == "usage")
    }

    @Test func testWhereARemovedProfilesAccountsCanGo() throws {
        let l = try #require(ProfileList(data: try encode([
            "profiles": [["name": "default", "declared": true, "pool": ["a1"]],
                         ["name": "work", "declared": true, "pool": ["w1", "w2"], "live": "w1"],
                         ["name": "lab", "declared": true, "pool": [String]()]],
            "ghosts": [Any]()])))
        #expect(ProfileRemoval.destinations(l, removing: "work") == ["default", "lab"])
        #expect(ProfileRemoval.suggested(l, removing: "work") == "default", "D6: default by default")
        #expect(ProfileRemoval.suggested(l, removing: "default") == nil, "default's accounts need a choice")
        #expect(ProfileRemoval.canRemove(l, "work"))

        let one = try #require(ProfileList(data: try encode([
            "profiles": [["name": "default", "declared": true, "pool": ["a1"]]], "ghosts": [Any]()])))
        #expect(!ProfileRemoval.canRemove(one, "default"), "the last profile stays")

        let work = try #require(l.profile("work"))
        let text = ProfileRemoval.summary(work, to: "default")
        #expect(text.contains("w1, w2") && text.contains("default"))
        #expect(text.contains("guarded"), "names the ghost behaviour")
        #expect(text.contains("stay"), "says the folder and the login stay")
        #expect(!ProfileRemoval.summary(try #require(l.profile("lab")), to: "default").contains("guarded"),
                "nothing live: no guard to describe")
    }
}
