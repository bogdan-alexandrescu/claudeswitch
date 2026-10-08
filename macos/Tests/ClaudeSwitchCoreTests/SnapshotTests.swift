import Foundation
import Testing
@testable import ClaudeSwitchCore

/// When gen-fixtures.sh ran: every time in the fixtures is relative to this.
let fixtureNow = GoTime.parse("2026-10-08T02:46:41Z")!

func fixtureSnapshot(why name: String = "why.json", now: Date = fixtureNow) throws -> Snapshot {
    Snapshot.build(state: StateFile(data: try fixture("state.json")),
                   why: WhyReport(data: try fixture(name)),
                   settings: ConfigSettings(data: try fixture("config.json")),
                   now: now)
}

@Suite struct SnapshotTests {
    @Test func testAccountsInTheOrderThePolicyConsideredThem() throws {
        let s = try fixtureSnapshot()
        #expect(s.accounts.map(\.id) == ["work-1", "personal", "work-2"])
        #expect(s.activeID == "work-1")
        #expect(s.accounts.filter(\.isActive).map(\.id) == ["work-1"])
        #expect(s.decision?.kind == "stay")
    }

    @Test func testStates() throws {
        let s = try fixtureSnapshot()
        let byID = Dictionary(uniqueKeysWithValues: s.accounts.map { ($0.id, $0) })
        #expect(byID["work-1"]?.status == .available)
        #expect(byID["personal"]?.status == .needsLogin, "a 401 needs a sign-in, whatever the policy thinks of its old figures")
        guard case .refused(let w, let until)? = byID["work-2"]?.status else {
            Issue.record("work-2 should be refused, is \(String(describing: byID["work-2"]?.status))"); return
        }
        #expect(w == "five_hour")
        #expect(abs((until?.timeIntervalSince(fixtureNow) ?? 0) - 2400) < 0.01)
        #expect(byID["work-2"]?.level == .over)
        #expect(byID["work-2"]?.eligible == false)
    }

    @Test func testTheBindingWindowIsTheHigherOne() throws {
        let a = try #require(try fixtureSnapshot().active)
        #expect(a.bindingWindow == "seven_day")
        #expect(a.bindingPct == 41)
        #expect(a.level == .ok)
        #expect(a.scopedWeekly.map(\.percent) == [12])
        #expect(a.why == "at 42%, 53 points of room")
    }

    /// Liveness comes from state.json alone: the daemon saves it at least
    /// every two minutes, so a save older than three of those (or three
    /// active polls, if longer) means it has stopped.
    @Test func testTheDaemon() throws {
        let s = try fixtureSnapshot()
        #expect(s.daemon.health == .polling)
        #expect(s.daemon.mode == "dry-run")
        #expect(s.daemon.lastPoll.map { fixtureNow.timeIntervalSince($0) } == 60)
    }

    @Test func testADaemonThatStoppedSaving() throws {
        // The fixture was saved at 22:19:43.88; ten and a half minutes on, it is stale
        // (whole minutes are shown, rounded down).
        let s = try fixtureSnapshot(now: fixtureNow.addingTimeInterval(10 * 60 + 30))
        guard case .stale(let since)? = Optional(s.daemon.health) else {
            Issue.record("expected stale, got \(s.daemon.health)"); return
        }
        // saved_at trails the generation time by however long the binary took
        // to build and run (1.4s at the last regeneration).
        #expect(since == s.daemon.savedAt)
        #expect(abs(s.now.timeIntervalSince(since) - 630) < 3)
        #expect(s.daemon.mode == "not polling for 10m")
        // Five minutes is inside the 6-minute allowance.
        #expect(try fixtureSnapshot(now: fixtureNow.addingTimeInterval(5 * 60)).daemon.health == .polling)
    }

    @Test func testTheAllowanceFollowsPollActive() {
        #expect(DaemonView.staleAfter(pollActive: nil) == 360)
        #expect(DaemonView.staleAfter(pollActive: 60) == 360)
        #expect(DaemonView.staleAfter(pollActive: 300) == 900)
    }

    @Test func testNoDaemonHasRun() throws {
        var obj = try fixtureObject("state.json")
        obj.removeValue(forKey: "daemon_since")
        let s = Snapshot.build(state: StateFile(data: try encode(obj)), why: nil, settings: nil, now: fixtureNow)
        #expect(s.daemon.health == .never)
        #expect(s.daemon.mode == "no daemon")
        let none = Snapshot.build(state: nil, why: nil, settings: nil, now: fixtureNow)
        #expect(none.daemon.health == .never)
    }

    @Test func testWithoutSavedAtTheNewestReadingIsUsed() throws {
        var obj = try fixtureObject("state.json")
        obj.removeValue(forKey: "saved_at")
        let s = Snapshot.build(state: StateFile(data: try encode(obj)), why: nil, settings: nil,
                               now: fixtureNow.addingTimeInterval(8 * 60))
        #expect(s.daemon.mode == "not polling for 9m")
    }

    @Test func testProfilesFromStateAndWhy() throws {
        let s = try fixtureSnapshot(why: "why-profiles.json")
        #expect(s.profiles.map(\.name) == ["default", "review"])
        #expect(s.profiles[1].pinned == "personal")
        #expect(s.profiles[1].decision?.reason == "pinned to personal; automatic rotation is off")
        #expect(s.accounts.map(\.id) == ["work-1", "personal", "work-2"])
        #expect(s.settings.switchAtWeekly == 95)
    }

    @Test func testAWindowThatHasResetIsNotTrusted() throws {
        let later = fixtureNow.addingTimeInterval(3 * 3600) // past work-1's 5h reset, not its 7d
        let s = try fixtureSnapshot(now: later)
        #expect(s.active?.status == .available, "the binding window is 7d, which has not reset")
        let muchLater = fixtureNow.addingTimeInterval(4 * 86_400)
        #expect(try fixtureSnapshot(now: muchLater).active?.status == .windowReset)
    }

    @Test func testStateAloneWhenTheBinaryCannotAnswer() throws {
        let s = Snapshot.build(state: StateFile(data: try fixture("state.json")), why: nil,
                               settings: nil, now: fixtureNow)
        #expect(s.activeID == "work-1")
        #expect(s.accounts.map(\.id) == ["personal", "work-1", "work-2"])
        #expect(s.decision == nil)
        #expect(s.daemon.mode == "dry-run")
    }

    @Test func testTheUnattributedRecordIsNotAnAccount() throws {
        var obj = try fixtureObject("state.json")
        var accts = try #require(obj["accounts"] as? [String: Any])
        accts["active"] = ["id": "active"]
        obj["accounts"] = accts
        let s = Snapshot.build(state: StateFile(data: try encode(obj)), why: nil, settings: nil,
                               now: fixtureNow)
        #expect(!(s.accounts.map(\.id).contains("active")))
    }

    @Test func testNoHeadroomAtTheTrigger() {
        let s = Snapshot.statusOf(record: nil, trigger: 90, now: fixtureNow)
        #expect(s == .unknown)
    }

    @Test func testLevels() {
        #expect(Level.of(79.9, trigger: 90) == .ok)
        #expect(Level.of(80, trigger: 90) == .near)
        #expect(Level.of(90, trigger: 90) == .over)
    }

    @Test func testNeedsLoginMatchesGo() {
        #expect(Snapshot.needsLogin("refresh: invalid_grant"))
        #expect(Snapshot.needsLogin("account \"x\" is not in the vault"))
        #expect(!(Snapshot.needsLogin("usage API: 429 Too Many Requests")))
    }
}

@Suite struct FormatTests {
    @Test func testPercent() {
        #expect(Format.pct(3) == "3%")
        #expect(Format.pct(41.46) == "41%")
        #expect(Format.pct(0.4) == "<1%")
        #expect(Format.pct(0) == "0%")
        #expect(Format.pct(nil) == "–")
        #expect(Format.pct(.nan) == "–")
    }

    @Test func testDurations() {
        #expect(Format.duration(30) == "30s")
        #expect(Format.duration(45 * 60) == "45m")
        #expect(Format.duration(2 * 3600 + 14 * 60) == "2h 14m")
        #expect(Format.duration(3 * 3600) == "3h")
        #expect(Format.duration(3 * 86_400 + 4 * 3600 + 59) == "3d 4h")
        #expect(Format.duration(-5) == "0s")
    }

    @Test func testResetsAndAge() {
        #expect(Format.resets(fixtureNow.addingTimeInterval(7200), now: fixtureNow) == "resets in 2h")
        #expect(Format.resets(fixtureNow.addingTimeInterval(-1), now: fixtureNow) == "reset")
        #expect(Format.resets(nil, now: fixtureNow) == "")
        #expect(Format.age(fixtureNow.addingTimeInterval(-90), now: fixtureNow) == "1m ago")
        #expect(Format.age(fixtureNow, now: fixtureNow) == "just now")
        #expect(Format.age(nil, now: fixtureNow) == "never")
    }

    @Test func testRowsFromTheFixture() throws {
        let s = try fixtureSnapshot()
        let a = try #require(s.active)
        #expect(Format.resets(a.fiveHour?.resetsAt, now: s.now) == "resets in 2h")
        #expect(Format.resets(a.sevenDay?.resetsAt, now: s.now) == "resets in 3d")
        #expect(Format.status(s.accounts[2].status, now: s.now) == "refused · 5h · clears in 40m")
        #expect(Format.status(s.accounts[1].status, now: s.now) == "needs login")
        #expect(Format.limitName(a.scopedWeekly[0]) == "Modelname weekly")
        #expect(a.pace != nil, "the popover shows the active account's weekly pace")
        #expect(Format.age(s.daemon.lastPoll, now: s.now) == "1m ago")
    }

    @Test func testPace() {
        let behind = WeeklyPace(expected: 57.1, actual: 20, atReset: 35, unusedAtReset: 65, resetsAt: nil)
        #expect(Format.pace(behind) == "20% used · 57% of the week gone")
        #expect(Format.paceExpiry(behind) == "~65% would expire unused at reset")
        let full = WeeklyPace(expected: 57.1, actual: 90, atReset: 100, unusedAtReset: 0, resetsAt: nil)
        #expect(Format.paceExpiry(full) == "on pace to use it all")
        let early = WeeklyPace(expected: 3, actual: 1, atReset: nil, unusedAtReset: nil, resetsAt: nil)
        #expect(Format.paceExpiry(early) == nil)
    }

    @Test func testNext() throws {
        #expect(Format.next(try fixtureSnapshot().decision) == "stay")
        let j = #"{"decision": {"kind": "switch", "target": "work-2", "reason": ""}}"#
        #expect(Format.next(WhyReport(data: Data(j.utf8))?.primary?.decision) == "switch → work-2")
        #expect(Format.next(nil) == "unknown")
    }

    @Test func testMenuTitle() throws {
        let s = try fixtureSnapshot()
        #expect(MenuTitle.of(s, compact: false) == MenuTitle(text: "work-1 41%", level: .ok, problem: false))
        #expect(MenuTitle.of(s, compact: true).text == "")
        #expect(MenuTitle.of(nil, compact: false).problem)

        // Near and over the 95% weekly trigger.
        var near = s
        near.accounts[0].bindingPct = 88
        near.accounts[0].level = .near
        #expect(MenuTitle.of(near, compact: false) == MenuTitle(text: "work-1 88%", level: .near, problem: false))
        var over = s
        over.accounts[0].bindingPct = 96
        over.accounts[0].level = .over
        #expect(MenuTitle.of(over, compact: false).text == "work-1 96%!")
        #expect(MenuTitle.of(over, compact: true).text == "!")

        var none = s
        none.activeID = nil
        #expect(MenuTitle.of(none, compact: false).text == "cs –")
    }

    @Test func testAnActiveAccountNearItsTriggerIsMarked() throws {
        var obj = try fixtureObject("state.json")
        var accts = try #require(obj["accounts"] as? [String: Any])
        var w1 = try #require(accts["work-1"] as? [String: Any])
        var usage = try #require(w1["last_usage"] as? [String: Any])
        usage["five_hour"] = ["utilization": 84.0, "resets_at": "2026-10-08T00:19:43Z"]
        w1["last_usage"] = usage
        accts["work-1"] = w1
        obj["accounts"] = accts
        let s = Snapshot.build(state: StateFile(data: try encode(obj)), why: nil,
                               settings: ConfigSettings(data: try fixture("config.json")),
                               now: fixtureNow)
        #expect(s.active?.bindingWindow == "five_hour")
        #expect(s.active?.level == .near, "84% is within 10 points of the 90% trigger")
        #expect(MenuTitle.of(s, compact: false).text == "work-1 84%")
    }
}
