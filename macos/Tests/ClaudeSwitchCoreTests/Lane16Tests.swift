import Foundation
import Testing
@testable import ClaudeSwitchCore

/// A fake claudeswitch with state: one account, "x", which a pool edit moves
/// from default (where it is by D6 alone) to work. `profile list` reads the
/// pools first and answers after `slow` seconds, so a read begun before the
/// edit answers with the pools from before it, after the edit has finished
/// (the owner's report, reproduced by QA as QARace).
enum StatefulCLI {
    static func make(in dir: URL) throws -> String {
        try fixture("why-profiles.json").write(to: dir.appendingPathComponent("why.json"))
        return try FakeBinary.write(in: dir, script: """
            D="$(dirname "$0")"
            POOLS="$D/pools"
            [ -f "$POOLS" ] || echo default > "$POOLS"
            case "$1 $2" in
              "version --json") echo '{"version": "0.5.1", "contract": 2}'; exit 0;;
              "version "*) echo "claudeswitch 0.5.1"; exit 0;;
              "why --json") cat "$D/why.json"; exit 0;;
              "config --json") echo '{"switch_at": "90"}'; exit 0;;
              "chrome list") echo '{"chrome_profiles": [], "supported": true}'; exit 0;;
              "account list") echo '{"accounts": []}'; exit 0;;
              "profile list")
                OWNER="$(cat "$POOLS")"
                [ -f "$D/slow" ] && sleep "$(cat "$D/slow")"
                if [ "$OWNER" = work ]; then DP='["a"]'; DL='["a"]'; WP='["w1","x"]'; WL='["w1","x"]'
                else DP='["a","x"]'; DL='["a"]'; WP='["w1"]'; WL='["w1"]'; fi
                printf '{"profiles":[{"name":"default","declared":true,"pool":%s,"listed":%s,"signed_in":"yes"},{"name":"work","declared":true,"dir":"~/.claude-work","pool":%s,"listed":%s,"signed_in":"yes"}],"ghosts":[]}\\n' "$DP" "$DL" "$WP" "$WL"
                exit 0;;
              "profile pool")
                if [ "$(cat "$POOLS")" = work ]; then
                  echo '{"account":"x","profile":"work","changed":false,"pools":{"default":["a"],"work":["w1","x"]}}'; exit 0
                fi
                echo work > "$POOLS"
                echo '{"account":"x","profile":"work","changed":true,"pools":{"default":["a"],"work":["w1","x"]}}'; exit 0;;
            esac
            echo '{"error":{"code":"failed","message":"fake: unhandled"}}'; exit 1
            """)
    }
}

/// Holds a value handed across threads in a test.
final class Box<T>: @unchecked Sendable {
    var value: T?
}

/// B1: a refresh applies only what it read, and never a read older than an
/// edit the app has already applied.
@Suite final class RefreshRaceTests {
    let dir: URL
    let path: String

    init() throws {
        dir = try FakeBinary.directory()
        path = try StatefulCLI.make(in: dir)
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func read(_ data: inout AppData, profiles: Bool = true) -> Reading {
        let t = data.begin()
        return RefreshReader.read(path: path, request: ReadRequest(full: true, settings: true, profiles: profiles),
                                  token: t, stateFile: dir.appendingPathComponent("none.json").path) { $0.checkVersion() }
    }

    func pool(_ data: AppData, _ p: String) -> [String] { data.profiles?.profile(p)?.pool ?? [] }

    @Test func testAnOlderRefreshNeverUndoesAPoolEdit() throws {
        var data = AppData()
        data.apply(read(&data))
        #expect(pool(data, "work") == ["w1"])
        #expect(data.profiles?.step(adding: "x", to: "work") == .add(account: "x", to: "work"))

        // A refresh starts (the directory watcher, the timer) and reads the
        // pools from before the edit…
        try "1.5".write(to: dir.appendingPathComponent("slow"), atomically: true, encoding: .utf8)
        let token = data.begin()
        let slow = Box<Reading>()
        let done = DispatchSemaphore(value: 0)
        let (p, f) = (path, dir.appendingPathComponent("none.json").path)
        DispatchQueue.global().async {
            slow.value = RefreshReader.read(path: p, request: ReadRequest(full: true, profiles: true),
                                            token: token, stateFile: f) { $0.checkVersion() }
            done.signal()
        }
        Thread.sleep(forTimeInterval: 0.5)

        // …the edit succeeds and its answer is applied at once…
        let r = try CLI(path: path).profilePool("work", .add, account: "x", to: nil).get()
        data.applyPools(r)
        #expect(pool(data, "work") == ["w1", "x"], "the edit shows as soon as the CLI answers")
        #expect(pool(data, "default") == ["a"])
        #expect(data.profiles?.step(adding: "x", to: "work") == .already)

        // …and the older read, answering last, does not put it back.
        done.wait()
        let applied = data.apply(try #require(slow.value))
        #expect(!applied.contains(.profiles))
        #expect(pool(data, "work") == ["w1", "x"], "a read begun before the edit is never applied over it")

        // A read begun after the edit is.
        try FileManager.default.removeItem(at: dir.appendingPathComponent("slow"))
        data.apply(read(&data))
        #expect(pool(data, "work") == ["w1", "x"])
        #expect(data.profiles?.profile("work")?.listed == ["w1", "x"])
    }

    @Test func testANewerReadWinsWhicheverAnswersFirst() throws {
        var data = AppData()
        let first = data.begin()
        let second = data.begin()
        var older = Reading(token: first)
        older.fetched = [.profiles]
        older.profiles = ProfileList(data: Data(#"{"profiles":[{"name":"old"}]}"#.utf8))
        var newer = Reading(token: second)
        newer.fetched = [.profiles]
        newer.profiles = ProfileList(data: Data(#"{"profiles":[{"name":"new"}]}"#.utf8))
        data.apply(newer)
        data.apply(older)
        #expect(data.profiles?.profiles.map(\.name) == ["new"])
    }

    /// The state.json watcher's refresh reads state alone: it must leave
    /// everything else as it is now, not as it was when it started.
    @Test func testAStateOnlyRefreshWritesNothingElseBack() throws {
        var data = AppData()
        data.apply(read(&data))
        let before = data.profiles
        let t = data.begin()
        var r = Reading(token: t)
        r.fetched = [.state]
        r.profiles = nil
        r.why = nil
        data.apply(r)
        #expect(data.profiles == before)
        #expect(data.why != nil)
    }

    /// B5: a `profile list` failure that is the CLI's answer (not a missing
    /// binary) is kept to show, and the last good list stays.
    @Test func testAProfileListFailureIsSurfacedNotSwallowed() throws {
        var data = AppData()
        data.apply(read(&data))
        let t = data.begin()
        var r = Reading(token: t)
        r.fetched = [.profiles]
        r.profilesError = .app(AppError(code: "config_invalid", message: "config.toml: bad pool"))
        data.apply(r)
        #expect(data.profilesError?.message == "config.toml: bad pool")
        #expect(data.profiles != nil, "the last good list stays")

        data.apply(read(&data))
        #expect(data.profilesError == nil)
    }
}

/// B6: the rotation order the app shows.
@Suite struct RotationOrderTests {
    func list(_ ids: [String]) -> AccountList {
        let items = ids.map { #"{"id": "\#($0)"}"# }.joined(separator: ",")
        return AccountList(data: Data(#"{"accounts": [\#(items)]}"#.utf8))!
    }

    @Test func testAFullRefreshClearsTheRememberedOrder() {
        var data = AppData()
        var r = Reading(token: data.begin())
        r.fetched = [.accounts]
        r.accountList = list(["a", "b", "c"])
        data.apply(r)
        data.reorder(["c", "a", "b"])
        #expect(data.accountIDs(snapshot: nil) == ["c", "a", "b"], "the drag shows at once")

        // The account was renamed meanwhile: the list read after it has the new id.
        var r2 = Reading(token: data.begin())
        r2.fetched = [.accounts]
        r2.accountList = list(["c", "a", "b2"])
        data.apply(r2)
        #expect(data.priorityOrder == nil)
        #expect(data.accountIDs(snapshot: nil) == ["c", "a", "b2"], "a renamed or deleted id never comes back")
    }

    @Test func testAListReadBeforeTheReorderDoesNotUndoIt() {
        var data = AppData()
        let early = data.begin()
        data.reorder(["b", "a"])
        var r = Reading(token: early)
        r.fetched = [.accounts]
        r.accountList = list(["a", "b"])
        data.apply(r)
        #expect(data.accountIDs(snapshot: nil) == ["b", "a"])
    }

    @Test func testMoveUpAndDown() {
        #expect(RotationOrder.move(["a", "b", "c"], "b", by: -1) == ["b", "a", "c"])
        #expect(RotationOrder.move(["a", "b", "c"], "b", by: 1) == ["a", "c", "b"])
        #expect(RotationOrder.move(["a", "b", "c"], "a", by: -1) == nil, "already first")
        #expect(RotationOrder.move(["a", "b", "c"], "c", by: 1) == nil, "already last")
        #expect(RotationOrder.move(["a"], "z", by: 1) == nil)
    }
}

/// B2, B3, B9, B10: the add-account sheet.
@Suite final class AddAccountTests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    var profiles: ProfileList { ProfileList(data: try! fixture("cli-profile-list.json"))! }

    @Test func testSaveCurrentLoginNamesItsSourceAndItsTarget() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-login-code")
        _ = try cli.add(account: "person4", from: "review", profile: "default").get()
        #expect(FakeAppCLI.calls(dir).last == ["add", "person4", "--from=review", "--profile=default", "--json"])
        _ = cli.add(account: nil, from: nil, profile: nil)
        #expect(FakeAppCLI.calls(dir).last == ["add", "--json"])
        guard case .failure(.app(let e)) = cli.add(account: nil, from: "-x", profile: nil) else {
            Issue.record("a source that could be a flag is refused"); return
        }
        #expect(e.code == "usage")
    }

    @Test func testTheSuggestedNameFollowsTheSource() {
        var s = NameSuggestion()
        let a = s.probe("default")
        #expect(s.answer(token: a, hint: "person4", name: "") == "person4")
        #expect(s.suggested == "person4")

        // Another source: the field follows while the person has not typed.
        let b = s.probe("review")
        #expect(s.suggested == nil, "the old suggestion is not offered for the new source")
        #expect(s.answer(token: b, hint: "person5", name: "person4") == "person5")

        // A late answer for an earlier source is dropped.
        let c = s.probe("default")
        let d = s.probe("review")
        #expect(s.answer(token: c, hint: "person4", name: "person5") == "person5")
        #expect(s.suggested == nil)
        #expect(s.answer(token: d, hint: "person5", name: "person5") == "person5")

        // A typed name is kept.
        let e = s.probe("default")
        #expect(s.answer(token: e, hint: "person4", name: "mine") == "mine")
        #expect(s.suggested == "person4")
    }

    /// Owner decision: saving one profile's login into another's accounts is
    /// allowed, and the sheet says what happens next, before and after.
    @Test func testAcrossProfilesTheSheetSaysWhatHappens() throws {
        #expect(AddForm.crossProfileNote(source: "default", target: "work")
                == "still signed in in default — default will move off it; work can use it after")
        #expect(AddForm.crossProfileNote(source: "work", target: "work") == nil)
        try FakeAppCLI.reply(dir, text: #"""
            {"account": "x", "profile": "work", "from": "default",
             "note": "still signed in in default — default will move off it; work can use it after"}
            """#, exit: 0)
        let a = try cli.add(account: "x", from: "default", profile: "work").get()
        #expect(a.from == "default")
        #expect(a.note == "still signed in in default — default will move off it; work can use it after")
    }

    @Test func testTheSheetStartsOnADeclaredProfile() throws {
        #expect(AddForm.initialTarget(requested: nil, list: profiles) == "default")
        let noDefault = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "work", "declared": true}, {"name": "review", "declared": true}]}
            """#.utf8)))
        #expect(AddForm.initialTarget(requested: nil, list: noDefault) == "work", "never a profile that does not exist")
        #expect(AddForm.initialTarget(requested: "review", list: noDefault) == "review")
        #expect(AddForm.initialTarget(requested: "gone", list: noDefault) == "work")
        #expect(AddForm.initialTarget(requested: nil, list: nil) == "default")
        #expect(AddForm.targets(noDefault) == ["work", "review"])
    }

    @Test func testAnExistingAccountKeepsItsProfile() throws {
        let accounts = try #require(AccountList(data: try fixture("cli-account-list.json")))
        #expect(AddForm.lockedProfile(account: "personal", accounts: accounts, list: profiles) == "review")
        #expect(AddForm.lockedProfile(account: "work-2", accounts: nil, list: profiles) == "default",
                "from the pools when the account list is not read yet")
        #expect(AddForm.lockedProfile(account: "brand-new", accounts: accounts, list: profiles) == nil)
        #expect(AddForm.lockedNote(account: "personal", profile: "review")
                == "personal is already in review. Move it in Profiles.")
    }

    @Test func testAProfileTheCLIDidNotApplyIsNeverReported() throws {
        try FakeAppCLI.reply(dir, text: #"""
            {"url": "https://example.com/a", "pending": {"account": "personal", "profile": "review", "new_account": false}}
            """#, exit: 0)
        let st = try cli.loginStart(account: "personal", profile: "default", browser: nil).get()
        #expect(AddForm.mismatch(chosen: "default", start: st)
                == "personal stays in review: an account that exists keeps its profile. Move it in Profiles.")
        #expect(AddForm.mismatch(chosen: "review", start: st) == nil)
        #expect(!FakeAppCLI.calls(dir).joined().joined().contains("scope"), "S1: no --scope")
    }
}

/// B4: the new-profile sheet's accounts.
@Suite struct NewProfileTests {
    let list = ProfileList(data: Data(#"""
        {"profiles": [
          {"name": "default", "declared": true, "pool": ["a", "d6"], "listed": ["a"]},
          {"name": "work", "declared": true, "pool": ["w1", "w2"], "listed": ["w1", "w2"]}]}
        """#.utf8))!

    @Test func testListedAccountsMoveAfterCreatingAndD6OnesJoinAtOnce() {
        let p = NewProfilePlan.make(name: "review", selected: ["d6", "w1"], seed: "w1", list: list)
        #expect(p.pool == ["d6"], "an account no pool lists can be taken at create")
        #expect(p.moves == [.move(account: "w1", from: "work", to: "review")])
        #expect(p.seedAtCreate == nil)
        #expect(p.seedAfter == "w1", "seeded once it has moved in")
        let c = p.confirmation
        #expect(c?.title == "Create review and move w1?")
        #expect(c?.message.contains("w1 leaves work") == true)

        let q = NewProfilePlan.make(name: "review", selected: ["d6"], seed: "d6", list: list)
        #expect(q.moves.isEmpty && q.confirmation == nil)
        #expect(q.seedAtCreate == "d6")
    }

    @Test func testWhereEachAccountIs() {
        #expect(NewProfilePlan.note(for: "w1", list: list) == "in work — moves here when created")
        #expect(NewProfilePlan.note(for: "a", list: list) == "in default — moves here when created")
        #expect(NewProfilePlan.note(for: "d6", list: list) == nil, "in default by D6 alone: free to take")
    }
}

/// B5, B7, C: pool edits and profiles.
@Suite struct PoolEditTests {
    let list = ProfileList(data: Data(#"""
        {"profiles": [
          {"name": "default", "declared": true, "pool": ["a", "d6"], "listed": ["a"]},
          {"name": "work", "declared": true, "pool": ["w1"], "listed": ["w1"]}]}
        """#.utf8))!

    @Test func testSuccessNotes() throws {
        let moved = try #require(PoolResult(JSON(data: Data(#"{"account":"lab-1","profile":"work","changed":true,"pools":{}}"#.utf8))!))
        #expect(Notes.pool(moved) == "lab-1 is now in work")
        let same = try #require(PoolResult(JSON(data: Data(#"{"account":"lab-1","profile":"work","changed":false,"pools":{}}"#.utf8))!))
        #expect(Notes.pool(same) == "lab-1 is already in work")
    }

    @Test func testOneVerbForListedAndD6Accounts() throws {
        let move = try #require(Confirm.poolStep(list.step(adding: "a", to: "work")))
        #expect(move.title == "Move a to work?" && move.action == "Move")
        let add = try #require(Confirm.poolStep(list.step(adding: "d6", to: "work")))
        #expect(add.title == "Move d6 to work?", "a D6 account is moved too, and confirmed")
        #expect(add.action == "Move")
        #expect(add.message.contains("default"))
        #expect(Confirm.poolStep(list.step(adding: "w1", to: "work")) == nil)
        #expect(list.moveTargets(for: "d6") == ["work"])
        #expect(list.moveTargets(for: "w1") == ["default"])
    }

    @Test func testImplicitMembers() {
        let d = list.profile("default")!
        #expect(d.isImplicitMember("d6"))
        #expect(!d.isImplicitMember("a"))
    }

    @Test func testNoProfilesYet() throws {
        let one = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "default", "declared": false, "from_env": true, "pool": ["a"]}]}
            """#.utf8)))
        let p = one.profiles[0]
        #expect(p.implicit)
        #expect(ProfileText.badge(p) == "No profiles yet", "never \"from CLAUDE_CONFIG_DIR\" for the implicit profile")
        #expect(ProfileText.createHint == "Create a profile to split accounts")
        let declared = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "work", "declared": true, "from_env": true}]}
            """#.utf8)))
        #expect(ProfileText.badge(declared.profiles[0]) == "from CLAUDE_CONFIG_DIR")
        #expect(ProfileText.badge(list.profiles[0]) == nil)
    }

    /// Review: pool edits run on a concurrent queue, so an older edit's
    /// answer can arrive after a newer one's; it is stamped like a read and
    /// dropped.
    @Test func testAnOlderPoolEditAnswerIsDropped() throws {
        var data = AppData()
        var r = Reading(token: data.begin())
        r.fetched = [.profiles]
        r.profiles = list
        data.apply(r)
        let first = data.begin()
        let second = data.begin()
        let toWork = try #require(PoolResult(JSON(data: Data(#"{"account":"d6","profile":"work","changed":true,"pools":{"default":["a"],"work":["w1","d6"]}}"#.utf8))!))
        let back = try #require(PoolResult(JSON(data: Data(#"{"account":"d6","profile":"default","changed":true,"pools":{"default":["a","d6"],"work":["w1"]}}"#.utf8))!))
        let newer = data.applyPools(back, token: second)
        let older = data.applyPools(toWork, token: first)
        #expect(newer)
        #expect(!older, "the older edit's answer is not applied over the newer")
        #expect(data.profiles?.profile("work")?.pool == ["w1"])
    }

    /// Review: with accounts to move, a create that timed out waiting for
    /// the daemon says the moves did not run.
    @Test func testACreateThatTimedOutSaysTheMovesDidNotRun() {
        let p = NewProfilePlan.make(name: "review", selected: ["d6", "w1"], seed: "d6", list: list)
        let m = p.notLoadedMessage("the daemon did not load the config in 10s")
        #expect(m.contains("review was created but not signed in"))
        #expect(m.contains("w1 was not moved in"))
        let q = NewProfilePlan.make(name: "review", selected: ["d6"], seed: "d6", list: list)
        #expect(!q.notLoadedMessage("x").contains("moved"))
    }

    @Test func testAppliedPoolsUpdateTheList() throws {
        var data = AppData()
        var r = Reading(token: data.begin())
        r.fetched = [.profiles]
        r.profiles = list
        data.apply(r)
        let res = try #require(PoolResult(JSON(data: Data(#"{"account":"d6","profile":"work","changed":true,"pools":{"default":["a"],"work":["w1","d6"]}}"#.utf8))!))
        data.applyPools(res)
        #expect(data.profiles?.profile("work")?.pool == ["w1", "d6"])
        #expect(data.profiles?.profile("work")?.listed == ["w1", "d6"])
        #expect(data.profiles?.profile("default")?.pool == ["a"])
        #expect(data.pool(of: "d6") == "work")
    }
}

/// B8: the app needs the CLI contract it was built against.
@Suite final class ContractTests {
    let dir: URL

    init() throws { dir = try FakeBinary.directory() }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func binary(version: String, json: String?) throws -> CLI {
        let answer = json.map { "echo '\($0)'; exit 0" } ?? "echo \"claudeswitch \(version)\"; exit 0"
        return CLI(path: try FakeBinary.write(in: dir, script: """
            if [ "$1" = version ] && [ "$2" = --json ]; then \(answer); fi
            if [ "$1" = version ]; then echo "claudeswitch \(version)"; exit 0; fi
            exit 2
            """))
    }

    @Test func testABuildThatSpeaksTheContract() throws {
        let cli = try binary(version: "dev", json: #"{"version": "dev", "contract": 2}"#)
        #expect(cli.contract() == 2)
        #expect(try cli.checkVersion().get() == "dev")
    }

    @Test func testAnOlderDevBuildIsRejected() throws {
        // A dev build from before lane 16 prints its plain version for any flag.
        let cli = try binary(version: "dev", json: nil)
        #expect(cli.contract() == nil)
        #expect(cli.checkVersion() == .failure(.tooOld(version: "dev")))
        #expect(CLIError.tooOld(version: "dev").message.contains("Update claudeswitch"))
    }

    @Test func testAReleaseWithoutTheContractIsRejected() throws {
        let cli = try binary(version: "0.5.1", json: nil)
        #expect(cli.checkVersion() == .failure(.tooOld(version: "0.5.1")))
        let older = try binary(version: "0.5.1", json: #"{"version": "0.5.1", "contract": 1}"#)
        #expect(older.checkVersion() == .failure(.tooOld(version: "0.5.1")))
        #expect(CLI.requiredContract == 2)
    }

    /// Review: the app speaks contract 2 exactly; a newer contract may have
    /// changed what it reads, so it is refused too, with its own message.
    @Test func testANewerContractIsRefused() throws {
        let cli = try binary(version: "0.6.0", json: #"{"version": "0.6.0", "contract": 3}"#)
        #expect(cli.checkVersion() == .failure(.unsupportedContract(version: "0.6.0", contract: 3)))
        #expect(CLIError.unsupportedContract(version: "0.6.0", contract: 3).message.contains("Update ClaudeSwitch"))
        #expect(CLI.supportedContracts == 2...2)
    }

    @Test func testTheMissingBinaryHintNamesWhereToChooseIt() {
        #expect(CLI.installSteps.contains("Settings → Advanced → Choose…"))
        #expect(!CLI.installSteps.contains("Binary…"))
    }
}

/// C: "Switch to best" on each card.
@Suite struct SwitchToBestTests {
    @Test func testTheBestAccountComesFromWhy() throws {
        let w = try #require(WhyReport(data: try fixture("why-profiles.json")))
        #expect(w.profiles.first { $0.profile == "default" }?.best == "personal")
        let r = try #require(w.profiles.first { $0.profile == "review" })
        #expect(r.best == nil)
        #expect(r.bestWhy == "review has no other account")

        let s = try cardsSnapshot()
        let d = try #require(s.card("default"))
        #expect(d.best?.id == "personal")
        #expect(d.bestLabel == "Switch to best: personal 12%")
        #expect(d.bestUnavailable == nil)
        let rv = try #require(s.card("review"))
        #expect(rv.best == nil)
        #expect(rv.bestLabel == "Switch to best")
        #expect(rv.bestUnavailable == "review has no other account")
    }

    /// Owner decision: with nothing clear of the landing margin, why still
    /// names the best account and best_why warns; the button shows it.
    @Test func testABestInsideTheMarginCarriesItsWarning() throws {
        var c = try #require(try cardsSnapshot().card("default"))
        #expect(c.bestWarning == nil)
        c.bestWhy = "personal is only 5 points below its 90% 5-hour trigger, short of the 10-point landing margin"
        #expect(c.bestWarning == c.bestWhy)
        #expect(c.bestUnavailable == nil, "it can still be switched to")
        c.best = nil
        #expect(c.bestWarning == nil)
    }

    @Test func testAnOlderWhyWithoutBestSaysSo() throws {
        let w = try #require(WhyReport(data: try fixture("why.json")))
        #expect(w.primary?.best == nil && w.primary?.bestWhy == nil)
        let s = try fixtureSnapshot()
        let c = try #require(s.cards.first)
        #expect(c.bestUnavailable == "No account to switch to")
    }
}

/// D: the popover's wording and states.
@Suite struct PopoverTextTests {
    func card(status: AccountStatus, five: Double? = 3, week: Double? = 41) -> ProfileCard {
        let a = AccountView(id: "a1", isActive: true, isPinned: false, status: status,
                            fiveHour: WindowReading(utilization: five, resetsAt: nil),
                            sevenDay: WindowReading(utilization: week, resetsAt: nil),
                            bindingWindow: "seven_day", bindingPct: week, level: .ok, scopedWeekly: [])
        return ProfileCard(name: "work", dir: nil, signedIn: "yes", active: a, pinned: nil, decision: nil,
                           switchAt: 90, switchAtWeekly: 95, pool: ["a1"], accounts: [a])
    }

    @Test func testTheCollapsedSummaryLeadsWithTrouble() {
        #expect(Format.cardSummary(card(status: .needsLogin)) == "needs login · a1")
        #expect(Format.cardSummary(card(status: .refused(window: "five_hour", until: nil))) == "refused · a1")
        #expect(Format.cardSummary(card(status: .available)) == "a1 · session 3% · week 41%")
    }

    @Test func testNextIsHiddenWhenWhyIsAboutAnotherAccount() throws {
        let s = try cardsSnapshot()
        #expect(s.card("default")?.nextVisible == true)
        var c = try #require(s.card("default"))
        c.whyActive = "work-2"
        #expect(!c.nextVisible, "why's active account is not the one state records: its decision is stale")
    }

    @Test func testAnUnsignedProfileCannotOpenClaudeCode() {
        var c = card(status: .available)
        #expect(c.canOpen)
        c.signedIn = "no"
        #expect(!c.canOpen)
        #expect(c.openBlocked == "work is not signed in. Sign it in from Settings → Profiles.")
        c.signedIn = "unknown"
        #expect(c.canOpen)
    }

    @Test func testModelLimitsAreColouredByLevel() throws {
        let s = try cardsSnapshot()
        let d = try #require(s.card("default"))
        let l = try #require(d.active?.scopedWeekly.first)
        #expect(d.level(of: l) == .ok)
        var hot = l
        hot.percent = 90
        #expect(d.level(of: hot) == .near)
        hot.percent = 99
        #expect(d.level(of: hot) == .over)
        hot.percent = nil
        #expect(d.level(of: hot) == nil)
    }

    @Test func testChipsShowStatus() {
        #expect(Chip.text("w03", status: .refused(window: nil, until: nil), pct: 100, error: nil) == "w03 · refused")
        #expect(Chip.text("w04", status: .needsLogin, pct: 3, error: "401") == "w04 · needs login")
        #expect(Chip.text("w05", status: .available, pct: 12, error: "usage API: timeout") == "w05 · error")
        #expect(Chip.text("w06", status: .available, pct: 12, error: nil) == "w06 · 12%")
        #expect(Chip.text("w07", status: nil, pct: nil, error: nil) == "w07")
    }

    @Test func testThePopoverFitsTheScreen() {
        #expect(PopoverLayout.maxHeight(visible: 900) == 860)
        #expect(PopoverLayout.maxHeight(visible: 300) == 320, "never below a usable height")
        #expect(PopoverLayout.scrolls(content: 500, visible: 900) == false)
        #expect(PopoverLayout.scrolls(content: 1200, visible: 900))
    }
}

/// D: Settings' wording.
@Suite struct SettingsTextTests {
    @Test func testHumanLabels() {
        #expect(SettingNames.label("switch_at") == "Switch at session")
        #expect(SettingNames.label("switch_at_weekly") == "Switch at week")
        #expect(SettingNames.label("hard_floor") == "Hard floor")
        #expect(SettingNames.label("landing_margin") == "Landing margin")
        #expect(SettingNames.label("models") == "Count model limits")
        #expect(SettingNames.label("poll_hot") == "Poll hot", "an unknown key still reads as words")
    }

    @Test func testDescriptionsAndValues() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let hot = try #require(schema.setting("hot_reserve"))
        #expect(SettingNames.description(hot) == "Usage calls per account held back for hot polling")
        let poll = try #require(schema.setting("poll_active"))
        #expect(SettingNames.value(poll, "3m0s") == "3m")
        #expect(SettingNames.value(poll, "1h0m0s") == "1h")
        #expect(SettingNames.value(poll, "24h0m0s") == "1d")
        let models = try #require(schema.setting("models"))
        #expect(SettingNames.value(models, "") == "none", "empty counts no model (the CLI: empty = none)")
        #expect(SettingNames.value(models, "Modelname") == "Modelname")
        let pct = try #require(schema.setting("switch_at"))
        #expect(SettingNames.value(pct, "85") == "85%")
    }

    @Test func testCopySaysAccountsNotPool() throws {
        let p = try #require(ProfileList(data: try fixture("cli-profile-list.json"))?.profile("review"))
        var empty = p
        empty.pool = []
        #expect(!ProfileRemoval.summary(empty, to: "default").lowercased().contains("pool"))
        #expect(ProfileRemoval.summary(empty, to: "default").hasPrefix("It has no accounts."))
        #expect(!ProfileRemoval.needsDestination(empty))
        #expect(ProfileRemoval.needsDestination(p))
        #expect(!Confirm.poolRemove(account: "a", from: "work").message.contains("pool"))
        #expect(!Confirm.poolMove(account: "a", from: "work", to: "review").message.contains("pool"))
    }
}
