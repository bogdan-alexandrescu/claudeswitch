import Foundation
import Testing
@testable import ClaudeSwitchCore

// Shared pools in the app (docs/PROFILES.md §10, D31-D36): accounts grouped
// into named pools, profiles listing the pools they use, in order.
//
// The fixtures: cli-pool-list.json is a config with no [[pool]] blocks (each
// profile its own pool, the base set); cli-pool-list-shared.json has team
// shared by default (2nd) and work (2nd), personal default's first, ops
// work's first, spare empty and listed by none, and loose-1 in no pool.

func sharedPools() throws -> PoolList { try #require(PoolList(data: try fixture("cli-pool-list-shared.json"))) }
func sharedProfiles() throws -> ProfileList { try #require(ProfileList(data: try fixture("cli-profile-list-pools.json"))) }

@Suite struct PoolDecodingTests {
    @Test func testPoolListDecodes() throws {
        let l = try sharedPools()
        #expect(l.pools.map(\.name) == ["personal", "team", "ops", "spare"])
        let team = try #require(l.pool("team"))
        #expect(team.accounts == ["team-1", "team-2", "team-3"])
        #expect(team.profiles == ["default", "work"])
        #expect(!team.own)
        #expect(l.pool("spare")?.accounts == [])
        #expect(l.pool("spare")?.profiles == [])
        #expect(l.unlisted == ["loose-1"])
        #expect(l.pool(of: "team-2")?.name == "team")
        #expect(l.pool(of: "loose-1") == nil)
        #expect(l.pool(of: "nobody") == nil)
    }

    @Test func testALegacyConfigIsOwnPools() throws {
        let l = try #require(PoolList(data: try fixture("cli-pool-list.json")))
        #expect(l.pools.map(\.name) == ["default", "review"])
        #expect(l.pools.allSatisfy { $0.own })
        #expect(l.unlisted.isEmpty)
    }

    @Test func testLenientShapes() throws {
        let nulls = try #require(PoolList(data: Data(#"{"pools": null, "unlisted": null}"#.utf8)))
        #expect(nulls.pools.isEmpty && nulls.unlisted.isEmpty, "Go writes a nil list as null")
        #expect(PoolList(data: Data(#"{"profiles": []}"#.utf8)) == nil, "not a pool list")
        #expect(PoolList(data: Data(#"{"error": {"code": "usage", "message": "x"}}"#.utf8)) == nil)
    }

    @Test func testWhoMayUseAnAccount() throws {
        let l = try sharedPools()
        #expect(l.profiles(of: "team-1", defaultExists: true) == ["default", "work"])
        #expect(l.profiles(of: "ops-1", defaultExists: true) == ["work"])
        #expect(l.profiles(of: "loose-1", defaultExists: true) == ["default"], "D6: default uses the accounts in no pool")
        #expect(l.profiles(of: "loose-1", defaultExists: false) == [])
    }

    @Test func testProfilesNameTheirPools() throws {
        let l = try sharedProfiles()
        #expect(l.profile("default")?.pools == ["personal", "team"])
        #expect(l.profile("work")?.pools == ["ops", "team"])
        #expect(l.pools?.map(\.name) == ["personal", "team", "ops", "spare"], "the top level is pool list's pools")
        let old = try #require(ProfileList(data: try encode(["profiles": [["name": "default", "pool": ["a1"]]]])))
        #expect(old.profile("default")?.pools == nil, "an older CLI does not say")
        #expect(old.pools == nil)
    }

    @Test func testAccountsNameTheirPoolAndEveryProfileThatMayUseThem() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list-pools.json")))
        let t2 = try #require(l.account("team-2"))
        #expect(t2.pool == "team")
        #expect(t2.profiles == ["default", "work"])
        #expect(t2.profile == "default", "profile stays: the first of them")
        #expect(t2.activeIn == "work")
        let loose = try #require(l.account("loose-1"))
        #expect(loose.pool == nil)
        #expect(loose.profiles == ["default"])
        let old = try #require(AccountList(data: Data(#"{"accounts": [{"id": "a1", "profile": "default"}]}"#.utf8)))
        #expect(old.account("a1")?.profiles == nil, "an older CLI does not say")
        #expect(old.account("a1")?.pool == nil)
    }

    @Test func testAddedAccountsSayWhoMayUseThem() throws {
        let a = try #require(AccountAdded(JSON(data: Data(#"""
            {"account": "n1", "profile": "default", "profiles": ["default", "work"], "pool": ["personal", "n1"]}
            """#.utf8))!))
        #expect(a.profiles == ["default", "work"])
        #expect(a.pool == ["personal", "n1"], "login's pool stays the profile's accounts (keys are never repurposed)")
        let old = try #require(AccountAdded(JSON(data: Data(#"{"account": "n1", "profile": "default"}"#.utf8))!))
        #expect(old.profiles == nil)
    }

    @Test func testPoolInUseIsAnErrorCode() throws {
        let e = try #require(AppError(data: try fixture("cli-error-pool-in-use.json")))
        #expect(e.code == "pool_in_use")
        #expect(e.message.contains("\"default\", \"work\""))
    }
}

/// The pool commands' argument vectors and answers, against the fake.
@Suite final class PoolCLITests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    @Test func testEveryPoolEditAnswersThePoolList() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-pool-list-shared")
        #expect(try cli.poolList().get().pools.count == 4)
        #expect(lastCall() == ["pool", "list", "--json"])

        #expect(try cli.poolCreate("spare", accounts: ["a1", "a2"]).get().pool("team") != nil)
        #expect(lastCall() == ["pool", "create", "spare", "a1", "a2", "--json"])
        _ = cli.poolCreate("spare", accounts: [])
        #expect(lastCall() == ["pool", "create", "spare", "--json"])

        _ = try cli.poolAdd("team", account: "loose-1").get()
        #expect(lastCall() == ["pool", "add", "team", "loose-1", "--json"])
        _ = try cli.poolRemove("team", account: "team-3").get()
        #expect(lastCall() == ["pool", "remove", "team", "team-3", "--json"])
        _ = try cli.poolMove("team-2", to: "ops").get()
        #expect(lastCall() == ["pool", "move", "team-2", "--to=ops", "--json"])
        _ = try cli.poolDelete("spare").get()
        #expect(lastCall() == ["pool", "delete", "spare", "--json"])
    }

    @Test func testProfilePoolsAnswersTheProfile() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-profile-pools")
        let p = try cli.profilePools("work", ["team", "ops"]).get()
        #expect(lastCall() == ["profile", "pools", "work", "team", "ops", "--json"])
        #expect(p.name == "work")
        #expect(p.pools == ["team", "ops"])
    }

    @Test func testNamesAreCheckedBeforeAnythingRuns() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-pool-list-shared")
        #expect(cli.poolMove("-x", to: "team").failureCode == "usage")
        #expect(cli.poolMove("a1", to: "--to=x").failureCode == "usage")
        #expect(cli.poolCreate("team", accounts: ["--json"]).failureCode == "usage")
        #expect(cli.profilePools("work", []).failureCode == "usage", "a profile uses at least one pool")
        #expect(FakeAppCLI.calls(dir).isEmpty, "nothing ran")
    }

    @Test func testRefusalsAreTheCLIs() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-error-pool-in-use", exit: 1)
        let r = cli.poolDelete("team")
        #expect(r.failureCode == "pool_in_use")
        #expect(r.failureHint?.contains("profile pools") == true)
    }

    /// An older binary has no `pool` command: it prints its usage and exits 2.
    @Test func testAnOlderCLIHasNoPools() throws {
        try FakeAppCLI.reply(dir, text: "", stderr: "claudeswitch 0.5.6 — keeps Claude Code on an account\n  history --usage\n",
                             exit: 2)
        let r = cli.poolList()
        let e = try #require(r.failure)
        #expect(PoolSupport.unsupported(e))
        #expect(PoolSupport.unsupported(.app(AppError(code: "usage", message: "unknown command"))))
        #expect(!PoolSupport.unsupported(.app(AppError(code: "config_invalid", message: "x"))))
    }
}

/// The Accounts pane: one section per pool, in pool list's order, then the
/// accounts in no pool.
@Suite struct PoolLayoutTests {
    let order = ["team-2", "personal", "ops-1", "team-1", "loose-1", "team-3"]

    @Test func testOneSectionPerPoolThenTheAccountsInNoPool() throws {
        let s = PoolLayout.sections(try sharedPools(), order: order, profiles: try sharedProfiles())
        #expect(s.map(\.title) == ["PERSONAL", "TEAM", "OPS", "SPARE", "In no pool"], "pool list's order")
        #expect(s.map(\.kind) == [.pool("personal"), .pool("team"), .pool("ops"), .pool("spare"), .unlisted])
        #expect(s[1].accounts == ["team-2", "team-1", "team-3"], "within a pool, the rotation order")
        #expect(s[1].subtitle == "used by default (2nd), work (2nd)")
        #expect(s[0].subtitle == "used by default (1st)", "default uses the accounts in no pool after it")
        #expect(s[2].subtitle == "used by work (1st)")
        #expect(s[3].subtitle == "no profile uses it")
        #expect(s[3].accounts.isEmpty && s[3].deletable)
        #expect(!s[1].deletable, "only an empty pool offers Delete")
        #expect(s[4].accounts == ["loose-1"])
        #expect(s[4].subtitle == "default uses them last")
        #expect(!s[4].deletable)
    }

    @Test func testALegacyConfigIsOneSectionPerProfile() throws {
        let pools = try #require(PoolList(data: try fixture("cli-pool-list.json")))
        let profiles = try #require(ProfileList(data: try fixture("cli-profile-list.json")))
        let s = PoolLayout.sections(pools, order: ["personal", "work-2", "work-1"], profiles: profiles)
        #expect(s.map(\.title) == ["DEFAULT", "REVIEW"], "no section for the accounts in no pool when there are none")
        #expect(s[0].accounts == ["work-2", "work-1"])
        #expect(s[0].subtitle == "used by default", "no ordinal for a profile's only pool")
        #expect(s[1].subtitle == "used by review")
        #expect(s.allSatisfy { $0.own && !$0.deletable })
        #expect(PoolLayout.grouped(pools, profiles: profiles))
    }

    @Test func testAnEmptyOwnPoolIsNoSection() throws {
        let pools = PoolList(pools: [PoolInfo(name: "default", accounts: ["a"], profiles: ["default"], own: true),
                                     PoolInfo(name: "lab", accounts: [], profiles: ["lab"], own: true)],
                             unlisted: [])
        #expect(PoolLayout.sections(pools, order: ["a"], profiles: nil).map(\.title) == ["DEFAULT"])
    }

    @Test func testAccountsTheOrderDoesNotNameComeLastInTheirPool() throws {
        let s = PoolLayout.sections(try sharedPools(), order: ["team-3"], profiles: try sharedProfiles())
        #expect(s[1].accounts == ["team-3", "team-1", "team-2"])
    }

    @Test func testNoProfilesYetIsOneList() throws {
        let pools = PoolList(pools: [], unlisted: ["a", "b"])
        let implicit = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "default", "declared": false, "pool": ["a", "b"], "pools": []}]}
            """#.utf8)))
        #expect(!PoolLayout.grouped(pools, profiles: implicit), "with no [[profile]] blocks there is nothing to group")
        #expect(!PoolLayout.grouped(nil, profiles: implicit), "nor with an older CLI")
        #expect(PoolLayout.grouped(try sharedPools(), profiles: try sharedProfiles()))
    }

    @Test func testTheAccountsInNoPoolWithoutDefault() throws {
        let pools = PoolList(pools: [PoolInfo(name: "team", accounts: ["a"], profiles: ["work"], own: false)],
                             unlisted: ["off"])
        let profiles = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "work", "declared": true, "pool": ["a"], "pools": ["team"]}]}
            """#.utf8)))
        let s = PoolLayout.sections(pools, order: ["a", "off"], profiles: profiles)
        #expect(s.last?.subtitle == "no profile uses them")
        let onlyD6 = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "default", "declared": true, "pool": ["off"], "pools": ["default"]},
                          {"name": "work", "declared": true, "pool": ["a"], "pools": ["team"]}]}
            """#.utf8)))
        #expect(PoolLayout.sections(pools, order: ["a", "off"], profiles: onlyD6).last?.subtitle == "default uses them",
                "default has no pool of its own with accounts: nothing comes before them")
    }

    @Test func testOrdinals() {
        #expect([1, 2, 3, 4, 11, 12, 13, 21, 22, 23, 101, 111].map(PoolLayout.ordinal)
                == ["1st", "2nd", "3rd", "4th", "11th", "12th", "13th", "21st", "22nd", "23rd", "101st", "111th"])
    }

    @Test func testDroppingOntoAnotherSection() throws {
        let pools = try sharedPools()
        let s = PoolLayout.sections(pools, order: order, profiles: try sharedProfiles())
        let (personal, team, ops, none) = (s[0], s[1], s[2], s[4])
        #expect(PoolLayout.drop("team-2", onto: ops, pools: pools) == .move(account: "team-2", from: "team", to: "ops"))
        #expect(PoolLayout.drop("team-2", onto: team, pools: pools) == nil, "within its own pool a drop is a reorder")
        #expect(PoolLayout.drop("team-2", onto: none, pools: pools) == .remove(account: "team-2", from: "team"))
        #expect(PoolLayout.drop("loose-1", onto: personal, pools: pools) == .move(account: "loose-1", from: nil, to: "personal"))
        #expect(PoolLayout.drop("loose-1", onto: none, pools: pools) == nil)
        #expect(PoolLayout.targets(for: "team-2", sections: s).map(\.title) == ["PERSONAL", "OPS", "SPARE"],
                "Move to pool: every other pool")
        #expect(PoolLayout.targets(for: "loose-1", sections: s).map(\.title) == ["PERSONAL", "TEAM", "OPS", "SPARE"])
    }

    @Test func testConfirmingADrop() throws {
        let pools = try sharedPools()
        let move = Confirm.poolDrop(.move(account: "team-2", from: "team", to: "ops"), pools: pools)
        #expect(move.title == "Move “team-2” to “ops”?")
        #expect(move.action == "Move")
        #expect(move.message == "The account “team-2” leaves the pool “team” and joins “ops”. From now on “work” may use it; “default” no longer can.")
        let toUnused = Confirm.poolDrop(.move(account: "ops-1", from: "ops", to: "spare"), pools: pools)
        #expect(toUnused.message == "The account “ops-1” leaves the pool “ops” and joins “spare”. No profile uses “spare”, so “ops-1” rotates nowhere until one does.")
        let intoTeam = Confirm.poolDrop(.move(account: "loose-1", from: nil, to: "team"), pools: pools)
        #expect(intoTeam.message == "The account “loose-1” joins the pool “team”. From now on “default” and “work” may use it.")
        let out = Confirm.poolDrop(.remove(account: "ops-1", from: "ops"), pools: pools)
        #expect(out.title == "Take “ops-1” out of “ops”?")
        #expect(out.action == "Take Out")
        #expect(out.message == "The account “ops-1” is then in no pool, where “default” uses it after its pools; “work” no longer can.")
        #expect(Notes.poolDrop(.move(account: "team-2", from: "team", to: "ops")) == "The account “team-2” is now in “ops”")
        #expect(Notes.poolDrop(.remove(account: "ops-1", from: "ops")) == "The account “ops-1” is in no pool now")
    }

    @Test func testReorderingWithinAPoolKeepsTheOthersInPlace() {
        let global = ["a", "x", "b", "y", "c"]
        #expect(RotationOrder.reorder(global, section: ["a", "b", "c"], from: IndexSet([2]), to: 0)
                == ["c", "x", "a", "y", "b"])
        #expect(RotationOrder.reorder(global, section: ["a", "b", "c"], from: IndexSet([0]), to: 3)
                == ["b", "x", "c", "y", "a"])
        #expect(RotationOrder.reorder(global, section: ["x", "y"], from: IndexSet([1]), to: 0)
                == ["a", "y", "b", "x", "c"])
    }
}

/// New pool: accounts in no pool join at once; one already in a pool is
/// moved in afterwards (`pool create` refuses it), confirmed first.
@Suite struct NewPoolTests {
    @Test func testAPoolStartsWithTheAccountsInNoPoolAndMovesTheOthersIn() throws {
        let pools = try sharedPools()
        let p = NewPoolPlan.make(name: "lab", selected: ["loose-1", "team-2"], pools: pools)
        #expect(p.create == ["loose-1"])
        #expect(p.moves == [.move(account: "team-2", from: "team", to: "lab")])
        let c = try #require(p.confirmation)
        #expect(c.title == "Create “lab” and move “team-2”?")
        #expect(c.message == "The account “team-2” leaves the pool “team”.")
        #expect(NewPoolPlan.make(name: "lab", selected: ["loose-1"], pools: pools).confirmation == nil)
        #expect(NewPoolPlan.note(for: "team-2", checked: true, pools: pools) == "leaves “team”")
        #expect(NewPoolPlan.note(for: "team-2", checked: false, pools: pools) == "in “team”", "not checked: where it is")
        #expect(NewPoolPlan.note(for: "loose-1", checked: true, pools: pools) == nil)
    }
}

/// Profiles: the POOLS row, and Change… (`profile pools`).
@Suite struct ProfilePoolsTests {
    @Test func testTheRowNamesThePoolsInOrder() throws {
        let (pools, list) = (try sharedPools(), try sharedProfiles())
        let d = try #require(list.profile("default"))
        let w = try #require(list.profile("work"))
        #expect(ProfilePools.shown(d, pools: pools) == ["personal", "team"])
        #expect(ProfilePools.shown(w, pools: pools) == ["ops", "team"])
        #expect(ProfilePools.usesUnlisted(d, pools: pools), "default uses the accounts in no pool, last")
        #expect(!ProfilePools.usesUnlisted(w, pools: pools))
        #expect(ProfilePools.summary(d, pools: pools) == "pools personal, team, then 1 in no pool")
        #expect(ProfilePools.summary(w, pools: pools) == "pools ops, team")
    }

    @Test func testAnEmptyOwnPoolIsNoPool() throws {
        let pools = PoolList(pools: [], unlisted: ["a", "b"])
        let list = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "default", "declared": true, "pool": ["a", "b"], "pools": ["default"]},
                          {"name": "lab", "declared": true, "pool": [], "pools": ["lab"]}]}
            """#.utf8)))
        #expect(ProfilePools.shown(list.profiles[0], pools: pools) == [])
        #expect(ProfilePools.summary(list.profiles[0], pools: pools) == "2 accounts in no pool")
        #expect(ProfilePools.summary(list.profiles[1], pools: pools) == "no pools")
    }

    @Test func testAnOlderCLISaysNothingOfPools() throws {
        let old = try #require(ProfileList(data: Data(#"{"profiles": [{"name": "default", "pool": ["a"]}]}"#.utf8)))
        #expect(ProfilePools.shown(old.profiles[0], pools: nil) == nil)
        #expect(ProfilePools.summary(old.profiles[0], pools: nil) == nil)
    }

    @Test func testChoosingAndOrderingPools() {
        var c = PoolChoice(all: ["personal", "team", "ops", "spare"], chosen: ["ops", "team"])
        #expect(c.order == ["ops", "team"])
        #expect(c.unchecked == ["personal", "spare"])
        c.toggle("personal")
        #expect(c.order == ["ops", "team", "personal"], "a pool checked joins last")
        let up = c.move("personal", by: -1)
        #expect(up)
        #expect(c.order == ["ops", "personal", "team"])
        let first = c.move("ops", by: -1)
        #expect(!first, "the first cannot go up")
        c.toggle("ops")
        #expect(c.order == ["personal", "team"])
        #expect(c.position(of: "team") == 2)
        #expect(c.position(of: "ops") == nil)
        #expect(c.unchecked == ["ops", "spare"], "in pool list's order")
        let chosenGone = PoolChoice(all: ["team"], chosen: ["gone", "team"])
        #expect(chosenGone.order == ["team"], "a pool the list no longer has is dropped")
    }

    /// `profile pools` converts a profile's own pool and keeps it first
    /// unless the list names it: dropping it takes a second call.
    @Test func testDroppingAProfilesOwnPoolTakesTwoCalls() throws {
        let legacy = try #require(PoolList(data: try fixture("cli-pool-list.json")))
        #expect(ProfilePools.steps(profile: "review", pools: legacy, chosen: ["default"])
                == [["review", "default"], ["default"]])
        #expect(ProfilePools.steps(profile: "review", pools: legacy, chosen: ["default", "review"])
                == [["default", "review"]])
        #expect(ProfilePools.steps(profile: "work", pools: try sharedPools(), chosen: ["team"]) == [["team"]])
        #expect(ProfilePools.note(profile: "review", pools: legacy, chosen: ["default"])
                == "Its own accounts (“personal”) become the pool “review”, which no profile uses then: "
                + "they rotate nowhere until a profile lists it or you move them. "
                + "The pool “default” belongs to the profile of the same name: checked, it becomes shared, "
                + "and that profile keeps using it.")
        #expect(ProfilePools.note(profile: "default", pools: legacy, chosen: ["default", "review"])
                == "The pool “review” belongs to the profile of the same name: checked, it becomes shared, "
                + "and that profile keeps using it.")
        #expect(ProfilePools.note(profile: "work", pools: try sharedPools(), chosen: ["team"]) == nil)
    }
}

/// The popover: an account live in another profile is not offered.
@Suite struct InUseElsewhereTests {
    func snapshot() throws -> Snapshot {
        let state = try #require(StateFile(data: try encode([
            "version": 1, "accounts": [String: Any](),
            "profiles": ["default": ["active_account": "personal"], "work": ["active_account": "team-2"]]])))
        return Snapshot.build(state: state, why: nil, settings: nil, profiles: try sharedProfiles(), now: fixtureNow)
    }

    @Test func testAnAccountLiveInAnotherProfile() throws {
        let s = try snapshot()
        #expect(s.liveElsewhere("team-2", notIn: "default") == "work")
        #expect(s.liveElsewhere("team-2", notIn: "work") == nil, "live here, not elsewhere")
        #expect(s.liveElsewhere("team-1", notIn: "default") == nil)
        #expect(s.liveIn("team-2") == "work")
        #expect(s.liveIn("team-1") == nil)
    }

    @Test func testItsPickerLine() throws {
        let s = try snapshot()
        let card = try #require(s.card("default"))
        let t2 = try #require(card.accounts.first { $0.id == "team-2" })
        #expect(Format.pickerLine(t2, inUseBy: "work", now: fixtureNow) == "team-2  – · in use by work")
        let p = try #require(card.accounts.first { $0.id == "personal" })
        #expect(Format.pickerLine(p, inUseBy: nil, now: fixtureNow).hasPrefix("personal  "),
                "the live account's check is the menu's own, in its gutter, so the ids line up")
    }
}

/// Add account: an account is in a pool, which several profiles may use.
@Suite struct AddFormPoolTests {
    @Test func testAnExistingAccountsPlace() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list-pools.json")))
        #expect(AddForm.lockedNote(account: "team-2", info: l.account("team-2"), profile: "default")
                == "The account “team-2” is in the pool “team”, which “default” and “work” use. "
                + "To change that, move it to another pool in Accounts.")
        #expect(AddForm.lockedNote(account: "loose-1", info: l.account("loose-1"), profile: "default")
                == "The account “loose-1” is in no pool: “default” uses it. To change that, move it into a pool in Accounts.")
        let old = try #require(AccountList(data: Data(#"{"accounts": [{"id": "a1", "profile": "work"}]}"#.utf8)))
        #expect(AddForm.lockedNote(account: "a1", info: old.account("a1"), profile: "work")
                == "The account “a1” is already in the profile “work”.")
        #expect(AddForm.lockedNote(account: "loose-1", info: l.account("loose-1"), profile: "default", grouped: false)
                == "The account “loose-1” is already in the profile “default”.",
                "with no [[profile]] blocks there are no pools to move it between")
        #expect(AddForm.usedBy(info: l.account("team-2"), profile: "default") == "default, work")
        #expect(AddForm.usedBy(info: old.account("a1"), profile: "work") == "work")
    }

    @Test func testTheTargetOfAnExistingAccountIsTheSourceWhenItMayUseIt() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list-pools.json")))
        #expect(AddForm.existingTarget(info: l.account("team-2"), source: "work", fallback: "default") == "work")
        #expect(AddForm.existingTarget(info: l.account("ops-1"), source: "default", fallback: "work") == "work")
    }

    @Test func testSavingALoginTheSourceSharesSaysSo() throws {
        let (pools, list) = (try sharedPools(), try sharedProfiles())
        #expect(AddForm.crossProfileNote(source: "default", target: "work", sourceUsesIt: false)
                == "it is still the login in “default”, which will move off it — “work” can use it after")
        #expect(AddForm.crossProfileNote(source: "default", target: "work", sourceUsesIt: true)
                == "it is still the login in “default”, which uses it too — “work” can use it once “default” rotates off it")
        #expect(AddForm.crossProfileNote(source: "work", target: "work", sourceUsesIt: true) == nil)
        // A new account joins the target's first pool: ops, which default
        // does not use; team, which it does.
        #expect(!AddForm.newAccountShared(source: "default", target: "work", list: list, pools: pools))
        let teamFirst = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "default", "declared": true, "pools": ["personal", "team"]},
                          {"name": "work", "declared": true, "pools": ["team", "ops"]}]}
            """#.utf8)))
        #expect(AddForm.newAccountShared(source: "default", target: "work", list: teamFirst, pools: pools))
    }

    @Test func testWhereANewAccountGoes() throws {
        let (pools, list) = (try sharedPools(), try sharedProfiles())
        #expect(AddForm.destination(target: "work", list: list, pools: pools) == "the pool “ops”, the first “work” uses")
        let legacy = try #require(ProfileList(data: try fixture("cli-profile-list.json")))
        #expect(AddForm.destination(target: "review", list: legacy, pools: nil) == "review")
    }

    @Test func testAnExistingAccountKeepsItsPool() throws {
        let st = try #require(LoginStart(JSON(data: Data(#"""
            {"url": "https://example.com/a", "pending": {"account": "team-2", "profile": "default", "new_account": false}}
            """#.utf8))!))
        #expect(AddForm.mismatch(chosen: "work", start: st)
                == "“team-2” already exists: it stays in its pool, which the profile “default” uses. "
                + "Move it to another pool in Accounts.")
        #expect(AddForm.mismatch(chosen: "default", start: st) == nil)
    }
}

/// New profile: it uses the pools chosen, in order, and signs in with an
/// account of theirs.
@Suite struct NewProfilePoolTests {
    @Test func testANewProfileUsesThePoolsChosen() throws {
        let pools = try sharedPools()
        let busy: (String) -> String? = { $0 == "team-2" ? "work" : nil }
        #expect(NewProfilePlan.seedCandidates(pools: ["team", "personal"], own: [], list: pools, liveElsewhere: busy)
                == ["team-1", "team-3", "personal"], "an account live in another profile cannot sign it in")
        let p = NewProfilePlan.make(name: "lab", pools: ["team"], own: [], seed: "team-1", candidates: ["team-1", "team-3"])
        #expect(p.pools == ["team"] && p.seed == "team-1" && p.own.isEmpty)
        let q = NewProfilePlan.make(name: "lab", pools: ["ops"], own: [], seed: "team-1", candidates: ["ops-1"])
        #expect(q.seed == nil, "a seed the pools chosen do not hold is dropped")
        #expect(p.notLoadedMessage("the daemon did not load the config in 10s")
                == "lab was created but not signed in: the daemon did not load the config in 10s")
    }

    /// Accounts in no pool (all of them, with no [[profile]] blocks yet) can
    /// be its own: `profile create --pool`, ahead of the pools chosen.
    @Test func testAccountsInNoPoolCanBeItsOwn() throws {
        let pools = try sharedPools()
        #expect(NewProfilePlan.seedCandidates(pools: ["ops"], own: ["loose-1"], list: pools, liveElsewhere: { _ in nil })
                == ["loose-1", "ops-1"], "its own accounts come first")
        let p = NewProfilePlan.make(name: "lab", pools: ["ops"], own: ["loose-1", "team-1"], seed: "loose-1",
                                    candidates: ["loose-1", "ops-1"], unlisted: pools.unlisted)
        #expect(p.own == ["loose-1"], "only an account in no pool can be taken at create")
        #expect(p.seed == "loose-1")
        let first = PoolList(pools: [], unlisted: ["a", "b"])
        let f = NewProfilePlan.make(name: "lab", pools: [], own: ["b"], seed: nil, candidates: ["b"], unlisted: first.unlisted)
        #expect(f.own == ["b"] && f.pools.isEmpty)
    }
}

/// Remove profile: only its own accounts move; the pools it lists stay.
@Suite struct RemoveProfilePoolTests {
    @Test func testOnlyItsOwnAccountsMove() throws {
        let (pools, list) = (try sharedPools(), try sharedProfiles())
        let w = try #require(list.profile("work"))
        #expect(ProfileRemoval.ownAccounts(w, pools: pools) == [])
        #expect(!ProfileRemoval.needsDestination(w, pools: pools))
        #expect(ProfileRemoval.summary(w, to: "default", pools: pools).contains(
            "is not rotated. It has no accounts of its own; the pool it shares (“team”) and the pool only it uses (“ops”) stay."))
        let d = try #require(list.profile("default"))
        #expect(ProfileRemoval.ownAccounts(d, pools: pools) == ["loose-1"], "default's own: the accounts in no pool")
        let legacy = try #require(PoolList(data: try fixture("cli-pool-list.json")))
        let lp = try #require(ProfileList(data: try fixture("cli-profile-list.json")))
        let review = try #require(lp.profile("review"))
        #expect(ProfileRemoval.ownAccounts(review, pools: legacy) == ["personal"])
        #expect(ProfileRemoval.needsDestination(review, pools: legacy))
        #expect(ProfileRemoval.summary(review, to: "default", pools: legacy).contains("Its accounts (“personal”) move to “default”."))
        let old = try #require(ProfileList(data: Data(#"{"profiles": [{"name": "x", "declared": true, "pool": ["a"]}]}"#.utf8)))
        #expect(ProfileRemoval.ownAccounts(old.profiles[0], pools: nil) == ["a"], "an older CLI: its pool")
    }
}

/// A pool edit's answer is applied at once, to the pools and to every
/// account's pool and profiles; an older answer never over a newer one.
@Suite struct ApplyPoolsTests {
    func data() throws -> AppData {
        var d = AppData()
        var r = Reading(token: d.begin())
        r.fetched = [.pools, .accounts, .profiles]
        r.pools = try sharedPools()
        r.accountList = try #require(AccountList(data: try fixture("cli-account-list-pools.json")))
        r.profiles = try sharedProfiles()
        d.apply(r)
        return d
    }

    func moved() -> PoolList {
        PoolList(pools: [PoolInfo(name: "personal", accounts: ["personal"], profiles: ["default"], own: false),
                         PoolInfo(name: "team", accounts: ["team-1", "team-3"], profiles: ["default", "work"], own: false),
                         PoolInfo(name: "ops", accounts: ["ops-1", "team-2"], profiles: ["work"], own: false),
                         PoolInfo(name: "spare", accounts: [], profiles: [], own: false)],
                 unlisted: ["loose-1"])
    }

    @Test func testAnEditsAnswerUpdatesThePoolsAndTheAccounts() throws {
        var d = try data()
        #expect(d.pools?.pool(of: "team-2")?.name == "team")
        let applied = d.applyPools(moved(), token: d.begin())
        #expect(applied)
        #expect(d.pools?.pool(of: "team-2")?.name == "ops")
        let t2 = try #require(d.accountList?.account("team-2"))
        #expect(t2.pool == "ops")
        #expect(t2.profiles == ["work"])
        #expect(t2.profile == "work")
        #expect(d.profile(of: "team-2") == "work")
        #expect(d.accountList?.account("loose-1")?.profiles == ["default"])
    }

    @Test func testAnOlderEditsAnswerIsDropped() throws {
        var d = try data()
        let first = d.begin()
        let second = d.begin()
        let newer = d.applyPools(moved(), token: second)
        let older = d.applyPools(try sharedPools(), token: first)
        #expect(newer)
        #expect(!older)
        #expect(d.pools?.pool(of: "team-2")?.name == "ops")
    }

    @Test func testAReadBegunBeforeAnEditIsNotAppliedOverIt() throws {
        var d = try data()
        let token = d.begin()
        let edited = d.applyPools(moved(), token: d.begin())
        #expect(edited)
        var stale = Reading(token: token)
        stale.fetched = [.pools, .accounts]
        stale.pools = try sharedPools()
        stale.accountList = try #require(AccountList(data: try fixture("cli-account-list-pools.json")))
        let applied = d.apply(stale)
        #expect(!applied.contains(.pools) && !applied.contains(.accounts))
        #expect(d.pools?.pool(of: "team-2")?.name == "ops")
    }

    @Test func testAProfilePoolsAnswerReplacesThatProfile() throws {
        var d = try data()
        let p = try #require(ProfileList(data: try fixture("cli-profile-pools.json").withProfilesWrapper())?.profiles.first)
        let applied = d.applyProfile(p, token: d.begin())
        #expect(applied)
        #expect(d.profiles?.profile("work")?.pools == ["team", "ops"])
        #expect(d.profiles?.profile("default")?.pools == ["personal", "team"])
    }

    @Test func testAnOlderCLIWithoutPoolsIsRemembered() throws {
        var d = AppData()
        var r = Reading(token: d.begin())
        r.located = true
        r.fetched = [.pools]
        r.poolsUnsupported = true
        d.apply(r)
        #expect(d.poolsUnsupported)
        #expect(d.pools == nil)
        var r2 = Reading(token: d.begin())
        r2.fetched = [.pools]
        r2.pools = try sharedPools()
        d.apply(r2)
        #expect(!d.poolsUnsupported, "a newer binary since")
    }
}

/// A fake claudeswitch for the refresh: `pool list` answers the shared
/// fixture until `pool move` has run, then the pools after the move. With
/// `old` it is a binary from before pools (usage text, exit 2); with `slow`
/// it reads the pools first and answers that many seconds later.
enum PoolsCLI {
    static let after = #"""
        {"pools": [
          {"name": "personal", "accounts": ["personal"], "profiles": ["default"], "own": false},
          {"name": "team", "accounts": ["team-1", "team-3"], "profiles": ["default", "work"], "own": false},
          {"name": "ops", "accounts": ["ops-1", "team-2"], "profiles": ["work"], "own": false},
          {"name": "spare", "accounts": [], "profiles": [], "own": false}],
         "unlisted": ["loose-1"]}
        """#

    static func make(in dir: URL) throws -> String {
        try fixture("why-profiles.json").write(to: dir.appendingPathComponent("why.json"))
        try fixture("cli-pool-list-shared.json").write(to: dir.appendingPathComponent("before.json"))
        try after.write(to: dir.appendingPathComponent("after.json"), atomically: true, encoding: .utf8)
        return try FakeBinary.write(in: dir, script: """
            D="$(dirname "$0")"
            case "$1 $2" in
              "version --json") echo '{"version": "0.6.4", "contract": 2}'; exit 0;;
              "version "*) echo "claudeswitch 0.6.4"; exit 0;;
              "why --json") cat "$D/why.json"; exit 0;;
              "config --json") echo '{"switch_at": "90"}'; exit 0;;
              "chrome list") echo '{"chrome_profiles": [], "supported": true}'; exit 0;;
              "account list") echo '{"accounts": []}'; exit 0;;
              "profile list") echo '{"profiles": [], "ghosts": []}'; exit 0;;
              "pool list")
                if [ -f "$D/old" ]; then
                  printf 'claudeswitch 0.5.6 — keeps Claude Code on an account\\n  history --usage\\n' >&2; exit 2
                fi
                F=before; [ -f "$D/moved" ] && F=after
                [ -f "$D/slow" ] && sleep "$(cat "$D/slow")"
                cat "$D/$F.json"; exit 0;;
              "pool move") touch "$D/moved"; cat "$D/after.json"; exit 0;;
            esac
            echo '{"error":{"code":"failed","message":"fake: unhandled"}}'; exit 1
            """)
    }
}

@Suite final class PoolRefreshTests {
    let dir: URL
    let path: String

    init() throws {
        dir = try FakeBinary.directory()
        path = try PoolsCLI.make(in: dir)
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func read(_ token: Int) -> Reading {
        RefreshReader.read(path: path, request: ReadRequest(full: true), token: token,
                           stateFile: dir.appendingPathComponent("none.json").path) { $0.checkVersion() }
    }

    @Test func testEveryFullRefreshReadsThePools() throws {
        var data = AppData()
        data.apply(read(data.begin()))
        #expect(data.pools?.pool(of: "team-2")?.name == "team")
        #expect(!data.poolsUnsupported)
        #expect(data.problem == nil)
    }

    @Test func testAnOlderBinaryWithoutPoolsIsNoProblem() throws {
        try "".write(to: dir.appendingPathComponent("old"), atomically: true, encoding: .utf8)
        var data = AppData()
        let r = read(data.begin())
        #expect(r.poolsUnsupported)
        #expect(r.problem == nil, "the rest of the app works with it")
        data.apply(r)
        #expect(data.poolsUnsupported && data.pools == nil)
        #expect(data.why != nil)
    }

    @Test func testAnOlderRefreshNeverUndoesAPoolMove() throws {
        var data = AppData()
        data.apply(read(data.begin()))
        try "1.5".write(to: dir.appendingPathComponent("slow"), atomically: true, encoding: .utf8)
        let token = data.begin()
        let slow = Box<Reading>()
        let done = DispatchSemaphore(value: 0)
        let (p, f) = (path, dir.appendingPathComponent("none.json").path)
        DispatchQueue.global().async {
            slow.value = RefreshReader.read(path: p, request: ReadRequest(full: true), token: token,
                                            stateFile: f) { $0.checkVersion() }
            done.signal()
        }
        Thread.sleep(forTimeInterval: 0.5)
        let editToken = data.begin()
        let moved = try CLI(path: path).poolMove("team-2", to: "ops").get()
        let edited = data.applyPools(moved, token: editToken)
        #expect(edited)
        #expect(data.pools?.pool(of: "team-2")?.name == "ops", "the move shows as soon as the CLI answers")
        done.wait()
        let applied = data.apply(try #require(slow.value))
        #expect(!applied.contains(.pools))
        #expect(data.pools?.pool(of: "team-2")?.name == "ops", "a read begun before the move is never applied over it")
        try FileManager.default.removeItem(at: dir.appendingPathComponent("slow"))
        data.apply(read(data.begin()))
        #expect(data.pools?.pool(of: "team-2")?.name == "ops")
    }
}

extension Data {
    /// A profile entry as `profile list` would carry it.
    func withProfilesWrapper() -> Data {
        guard let o = try? JSONSerialization.jsonObject(with: self) else { return self }
        return (try? JSONSerialization.data(withJSONObject: ["profiles": [o]])) ?? self
    }
}

/// A refused Delete pool names the sheet's own way out, not the CLI's.
@Suite struct PoolHintTests {
    @Test func testAPoolInUseSaysWhereToUncheckIt() {
        #expect(PoolHint.app(code: "pool_in_use")?.contains("Change…") == true)
        #expect(PoolHint.app(code: "failed") == nil)
    }
}
