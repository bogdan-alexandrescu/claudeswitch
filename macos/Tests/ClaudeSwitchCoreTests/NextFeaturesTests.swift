import Foundation
import Testing
@testable import ClaudeSwitchCore

// F1, F2, F3, F4, F5, F7 and F12 (docs/IMPROVEMENTS.md, "Next features"),
// built against the spec's JSON. Every new read degrades: a key or command
// an older binary lacks hides the feature, and an unknown value is never 0.

let utc = TimeZone(identifier: "UTC")!

private func utcCalendar() -> Calendar {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = utc
    return c
}

private func at(_ s: String) -> Date { GoTime.parse(s)! }

// MARK: F1 spend expiring quota first

@Suite struct PreferSettingTests {
    @Test func testPreferIsARotationSettingNamedSpendFirst() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema-prefer.json")))
        let prefer = try #require(schema.setting("prefer"))
        #expect(SettingsPane.of(prefer) == .rotation)
        #expect(schema.settings(in: .rotation).map(\.key).contains("prefer"))
        #expect(SettingNames.label("prefer") == "Spend first")
        #expect(prefer.profileScoped, "the spec: global and per profile")
    }

    @Test func testItsChoicesReadAsMostRoomAndExpiringQuota() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema-prefer.json")))
        let prefer = try #require(schema.setting("prefer"))
        #expect(SettingNames.value(prefer, "room") == "Most room")
        #expect(SettingNames.value(prefer, "expiring") == "Expiring quota")
        #expect(SettingNames.value(prefer, "later") == "later", "a choice a newer CLI adds reads as itself")
        #expect(RotationPrefer(rawValue: "expiring")?.label == "Expiring quota")
    }

    @Test func testAnOlderSchemaHasNoSpendFirstRow() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        #expect(!schema.settings(in: .rotation).map(\.key).contains("prefer"))
    }

    @Test func testOtherEnumsStillReadAsTheirValues() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let when = try #require(schema.setting("switch_when"))
        #expect(SettingNames.value(when, "idle") == "idle")
    }

    @Test func testAProfileChoiceOffersInheritThenEveryValue() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema-prefer.json")))
        let prefer = try #require(schema.setting("prefer"))
        let opts = OverrideChoices.options(prefer, global: "room")
        #expect(opts.map(\.value) == ["", "room", "expiring"])
        #expect(opts.first?.label == "Inherit (Most room)")
    }
}

// MARK: F2 pool runway

@Suite struct RunwayTests {
    @Test func testWhyCarriesPoolDryAtAndTriggerAt() throws {
        let w = try #require(WhyReport(data: try fixture("why-runway.json")))
        let def = try #require(w.profiles.first { $0.profile == "default" })
        #expect(def.poolDryAt == at("2026-10-08T14:00:00Z"))
        #expect(def.accounts.first { $0.id == "work-1" }?.triggerAt == at("2026-10-08T09:30:00Z"))
        #expect(def.accounts.first { $0.id == "personal" }?.triggerAt == nil, "null is unknown")
        #expect(w.profiles.first { $0.profile == "review" }?.poolDryAt == nil)
    }

    @Test func testAnOlderWhyHasNoRunway() throws {
        let w = try #require(WhyReport(data: try fixture("why-profiles.json")))
        #expect(w.profiles.allSatisfy { $0.poolDryAt == nil })
        #expect(w.profiles.flatMap(\.accounts).allSatisfy { $0.triggerAt == nil })
    }

    @Test func testTheCardsCarryTheRunway() throws {
        let s = Snapshot.build(state: StateFile(data: try fixture("state.json")),
                               why: WhyReport(data: try fixture("why-runway.json")),
                               settings: nil, profiles: ProfileList(data: try fixture("cli-profile-list.json")),
                               now: fixtureNow)
        #expect(s.card("default")?.poolDryAt == at("2026-10-08T14:00:00Z"))
        #expect(s.card("review")?.poolDryAt == nil)
        #expect(s.card("default")?.accounts.first { $0.id == "work-2" }?.triggerAt == at("2026-10-10T18:15:00Z"))
    }

    @Test func testThePoolLineNamesTheProfileAndTheTime() {
        let now = at("2026-10-08T02:46:41Z") // a Thursday
        #expect(Runway.poolLine(profile: "work", dryAt: at("2026-10-08T14:00:00Z"), now: now, timeZone: utc)
                == "work pool: runs dry Thu 14:00 at this pace")
        #expect(Runway.poolLine(profile: "work", dryAt: at("2026-10-10T09:05:00Z"), now: now, timeZone: utc)
                == "work pool: runs dry Sat 09:05 at this pace")
    }

    @Test func testNullHidesThePoolLine() {
        #expect(Runway.poolLine(profile: "work", dryAt: nil, now: Date(), timeZone: utc) == nil)
    }

    @Test func testAWeekOrMoreAwayNamesTheDate() {
        let now = at("2026-10-08T02:46:41Z")
        #expect(Runway.when(at("2026-10-16T08:00:00Z"), now: now, timeZone: utc) == "16 Oct 08:00")
    }

    @Test func testAPassedTimeIsNow() {
        let now = at("2026-10-08T02:46:41Z")
        #expect(Runway.poolLine(profile: "work", dryAt: at("2026-10-08T02:00:00Z"), now: now, timeZone: utc)
                == "work pool: runs dry now at this pace")
    }

    @Test func testWithinTwoHoursIsUrgent() {
        let now = at("2026-10-08T02:46:41Z")
        #expect(Runway.urgent(dryAt: at("2026-10-08T04:00:00Z"), now: now))
        #expect(!Runway.urgent(dryAt: at("2026-10-08T05:00:00Z"), now: now))
        #expect(!Runway.urgent(dryAt: nil, now: now))
    }

    @Test func testThePickerShowsEachAccountsTriggerETA() {
        let now = at("2026-10-08T02:46:41Z")
        #expect(Runway.accountETA(at("2026-10-08T09:30:00Z"), now: now, timeZone: utc) == "trigger Thu 09:30")
        #expect(Runway.accountETA(nil, now: now, timeZone: utc) == nil, "unknown is hidden, never a guess")
    }
}

// MARK: F3 pin safety valve

@Suite struct PinValveTests {
    @Test func testAccountListCarriesPinHard() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list-next.json")))
        #expect(l.account("personal")?.pinHard == true)
        #expect(l.account("work-1")?.pinHard == false)
        #expect(l.knowsHardPin)
    }

    @Test func testAnOlderListHidesHardPin() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list.json")))
        #expect(l.account("personal")?.pinHard == nil)
        #expect(!l.knowsHardPin)
    }

    @Test func testTheLatestUnpinPerProfileFromTheAuditLog() throws {
        let data = try fixture("audit-unpin.jsonl")
        let lifts = PinLift.latest(jsonl: data)
        #expect(lifts.count == 2, "a line that is not JSON is skipped")
        let d = try #require(lifts["default"])
        #expect(d.account == "work-2")
        #expect(d.at == at("2026-10-08T02:15:00Z"))
        #expect(d.message == "Pin on work-2 lifted: it was refused")
        #expect(lifts["review"]?.message == "Pin on personal lifted: it needs signing in")
    }

    @Test func testAnUnpinWithFromAndNoReason() throws {
        let line = #"{"at":"2026-10-08T02:15:00Z","kind":"unpin","profile":"work","from":"w1"}"#
        let lifts = PinLift.latest(jsonl: Data(line.utf8))
        #expect(lifts["work"]?.message == "Pin on w1 lifted by the daemon")
    }

    @Test func testTheNoticeShowsOnAnUnpinnedCardForADay() throws {
        let lifts = PinLift.latest(jsonl: try fixture("audit-unpin.jsonl"))
        let now = at("2026-10-08T02:46:41Z")
        #expect(PinLift.notice(profile: "default", lifts: lifts, pinned: false, dismissed: [], now: now) != nil)
        #expect(PinLift.notice(profile: "default", lifts: lifts, pinned: true, dismissed: [], now: now) == nil,
                "pinned again: the lift is over")
        let later = now.addingTimeInterval(25 * 3600)
        #expect(PinLift.notice(profile: "default", lifts: lifts, pinned: false, dismissed: [], now: later) == nil)
        let key = try #require(lifts["default"]).key
        #expect(PinLift.notice(profile: "default", lifts: lifts, pinned: false, dismissed: [key], now: now) == nil)
    }

    @Test func testHardPinArgv() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: """
            echo "$@" > "$(dirname "$0")/args"
            echo '{"account":"work-1","profile":"default","pinned":true,"hard":true}'
            """)
        _ = CLI(path: p).pin("work-1", hard: true)
        let args = try String(contentsOf: dir.appendingPathComponent("args"), encoding: .utf8)
        #expect(args.trimmingCharacters(in: .whitespacesAndNewlines) == "account pin work-1 --hard --json")
    }
}

// MARK: F4 profile folders

@Suite struct FoldersTests {
    @Test func testProfileListCarriesPaths() throws {
        let l = try #require(ProfileList(data: try fixture("cli-profile-list-paths.json")))
        #expect(l.profile("review")?.paths == ["~/review/**", "~/clients/acme/**"])
        #expect(l.profile("default")?.paths == [])
    }

    @Test func testAnOlderListHasNoFoldersField() throws {
        let l = try #require(ProfileList(data: try fixture("cli-profile-list.json")))
        #expect(l.profiles.allSatisfy { $0.paths == nil })
    }

    @Test func testFoldersAreCommaOrLineSeparated() {
        #expect(FolderList.parse(" ~/work/** , ~/clients/**\n~/work/** ,, ") == ["~/work/**", "~/clients/**"])
        #expect(FolderList.parse("") == [])
        #expect(FolderList.text(["~/a/**", "~/b"]) == "~/a/**, ~/b")
    }

    @Test func testAFolderThatLooksLikeAFlagIsRefused() {
        #expect(FolderList.validate("~/work/**, -x") != nil)
        #expect(FolderList.validate("~/work/**") == nil)
        #expect(FolderList.validate("") == nil, "empty clears the folders")
    }

    @Test func testFoldersAreSavedAsOneValue() {
        #expect(FolderList.value(["~/a/**", "~/b"]) == "~/a/**,~/b")
        #expect(FolderList.value([]) == "")
    }
}

// MARK: F5 re-login reminders

@Suite struct ReloginReminderTests {
    let now = at("2026-10-08T02:46:41Z")

    @Test func testAnAccountExpiringWithinFiveDaysIsDue() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list-next.json")))
        let due = ReloginReminders.due(l, now: now, sent: [:], calendar: utcCalendar())
        #expect(due.map(\.account) == ["personal"], "work-1 is 43 days out; work-2's expiry is unknown")
        #expect(due.first?.body == "personal needs signing in within 2 days")
    }

    @Test func testOncePerAccountPerDay() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list-next.json")))
        let cal = utcCalendar()
        let sent = ReloginReminders.record([:], accounts: ["personal"], now: now, calendar: cal)
        #expect(ReloginReminders.due(l, now: now.addingTimeInterval(3600), sent: sent, calendar: cal).isEmpty)
        let tomorrow = now.addingTimeInterval(24 * 3600)
        #expect(ReloginReminders.due(l, now: tomorrow, sent: sent, calendar: cal).map(\.account) == ["personal"])
    }

    @Test func testTheRecordKeepsOnlyRecentDays() {
        let cal = utcCalendar()
        let old = ["gone": "2026-09-01", "personal": "2026-10-07"]
        let sent = ReloginReminders.record(old, accounts: ["work-1"], now: now, calendar: cal)
        #expect(sent == ["personal": "2026-10-07", "work-1": "2026-10-08"])
    }

    @Test func testAnExpiredLoginSaysSo() throws {
        let json = #"{"accounts":[{"id":"a","enabled":true,"refresh_expires_at":"2026-10-07T00:00:00Z"}]}"#
        let l = try #require(AccountList(data: Data(json.utf8)))
        let due = ReloginReminders.due(l, now: now, sent: [:], calendar: utcCalendar())
        #expect(due.first?.body == "a needs signing in: its login has expired")
    }

    @Test func testUnknownOrDisabledIsNeverReminded() throws {
        let json = #"{"accounts":[{"id":"a","enabled":false,"refresh_expires_at":"2026-10-09T00:00:00Z"},{"id":"b"}]}"#
        let l = try #require(AccountList(data: Data(json.utf8)))
        #expect(ReloginReminders.due(l, now: now, sent: [:], calendar: utcCalendar()).isEmpty)
        #expect(ReloginReminders.due(nil, now: now, sent: [:], calendar: utcCalendar()).isEmpty)
    }

    @Test func testTheNotificationNamesItsAccount() {
        let r = ReloginReminder(account: "personal", expiresAt: now, days: 5)
        #expect(ReloginReminder.account(fromIdentifier: r.identifier) == "personal")
        #expect(ReloginReminder.account(fromIdentifier: "other.thing") == nil)
    }
}

// MARK: F7 usage history

@Suite struct UsageHistoryTests {
    func history() throws -> UsageHistory {
        try #require(UsageHistory(data: try fixture("cli-history-usage.json")))
    }

    @Test func testDecodesASeriesPerAccountAndTheSwitches() throws {
        let h = try history()
        #expect(h.accountIDs == ["work-1", "work-2", "personal"])
        #expect(h.series["work-1"]?.count == 355)
        #expect(h.switches.count == 11)
        #expect(h.switches.first?.from == "work-1")
        #expect(h.switches.first?.to == "work-2")
        #expect(h.series["personal"]?.contains { $0.fiveHour == nil } == true, "null stays unknown")
    }

    @Test func testAMapOfSeriesDecodesToo() throws {
        let json = #"{"accounts":{"b":[{"at":"2026-10-08T01:00:00Z","five_hour":3,"seven_day":4}],"a":[]},"switches":null}"#
        let h = try #require(UsageHistory(data: Data(json.utf8)))
        #expect(h.accountIDs == ["a", "b"])
        #expect(h.series["b"]?.first?.sevenDay == 4)
        #expect(h.switches.isEmpty)
    }

    @Test func testAnotherAnswerIsNotAHistory() {
        #expect(UsageHistory(data: Data(#"{"rejections":[]}"#.utf8)) == nil)
    }

    @Test func testChartPointsAreBucketedSortedAndClamped() throws {
        let json = """
            {"accounts":[{"id":"a","series":[
              {"at":"2026-10-08T01:40:00Z","five_hour":30,"seven_day":10},
              {"at":"2026-10-08T01:10:00Z","five_hour":50,"seven_day":12},
              {"at":"2026-10-08T00:10:00Z","five_hour":120,"seven_day":null},
              {"at":"2026-08-01T00:00:00Z","five_hour":5,"seven_day":5}
            ]}]}
            """
        let h = try #require(UsageHistory(data: Data(json.utf8)))
        let now = at("2026-10-08T02:00:00Z")
        let session = HistoryChart.points(h, account: "a", window: .session, now: now, days: 30)
        #expect(session.map(\.value) == [100, 50], "the hour's highest reading; 120 clamps to 100; older than 30 days dropped")
        let week = HistoryChart.points(h, account: "a", window: .week, now: now, days: 30)
        #expect(week.map(\.value) == [12], "the null hour has no point, never 0")
    }

    @Test func testAGapInReadingsBreaksTheLine() throws {
        let h = try history()
        let pts = HistoryChart.points(h, account: "work-1", window: .week, now: fixtureNow, days: 30)
        let segments = Set(pts.map(\.segment))
        #expect(segments.count == 2, "the half-day without readings splits the line in two")
    }

    @Test func testSwitchesAreMarkedOnTheAccountsTheyTouch() throws {
        let h = try history()
        let marks = HistoryChart.switches(h, account: "personal", now: fixtureNow, days: 30)
        #expect(!marks.isEmpty)
        #expect(marks.allSatisfy { $0.from == "personal" || $0.to == "personal" })
        #expect(marks.contains { $0.into }, "a switch to it")
        #expect(marks.contains { !$0.into }, "and away from it")
    }

    @Test func testTheDomainIsTheLastThirtyDays() {
        let now = at("2026-10-08T02:00:00Z")
        let d = HistoryChart.domain(now: now, days: 30)
        #expect(d.upperBound == now)
        #expect(d.lowerBound == now.addingTimeInterval(-30 * 86400))
    }

    @Test func testHistoryArgv() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: """
            echo "$@" > "$(dirname "$0")/args"
            echo '{"accounts":[],"switches":[]}'
            """)
        let r = CLI(path: p).usageHistory(days: 30)
        #expect(r.failure == nil)
        let args = try String(contentsOf: dir.appendingPathComponent("args"), encoding: .utf8)
        #expect(args.trimmingCharacters(in: .whitespacesAndNewlines) == "history --usage --days=30 --json")
    }

    @Test func testAnOlderBinarySaysItNeedsANewerOne() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: """
            case "$1" in version) echo "claudeswitch 0.5.5"; exit 0;; esac
            echo "flag provided but not defined: -usage" >&2; exit 2
            """)
        let r = CLI(path: p).usageHistory(days: 30)
        #expect(FeatureSupport.needsNewerCLI(r.failure))
    }
}

// MARK: F12 health

@Suite struct HealthTests {
    @Test func testDecodesEveryCheck() throws {
        let d = try #require(DoctorReport(data: try fixture("cli-doctor.json")))
        #expect(d.checks.count == 9)
        #expect(d.checks.first?.status == .ok)
        #expect(d.checks.filter { $0.status == .fail }.count == 4)
        #expect(d.summary == "4 failing · 2 warnings · 3 ok")
    }

    @Test func testKnownFixesAreActions() throws {
        let d = try #require(DoctorReport(data: try fixture("cli-doctor.json")))
        func fix(_ name: String) -> FixAction? { d.checks.first { $0.name == name }?.fix }
        #expect(fix("keychain") == .keychainAllow)
        #expect(fix("login personal") == .signin("personal"))
        #expect(fix("daemon") == .daemonRestart)
        #expect(fix("status line") == .statuslineInstall)
        #expect(fix("plugin") == nil, "an action this app does not know gets no button")
        #expect(fix("config") == nil)
    }

    @Test func testOnlyAFailureWithAKnownFixGetsAButton() throws {
        let d = try #require(DoctorReport(data: try fixture("cli-doctor.json")))
        let buttons = d.checks.filter(\.offersFix).map(\.name)
        #expect(buttons == ["keychain", "login personal", "daemon", "status line"])
    }

    @Test func testFixArgv() {
        #expect(FixAction.daemonRestart.argv == ["daemon", "restart", "--json"])
        #expect(FixAction.statuslineInstall.argv == ["statusline", "install", "--json"])
        #expect(FixAction.keychainAllow.argv == ["keychain", "allow", "--json"])
        #expect(FixAction.signin("personal").argv == nil, "sign-in opens Add account")
        #expect(FixAction("signin -rf") == nil, "a name that is not an id is refused")
        #expect(FixAction("signin") == nil)
    }

    @Test func testAnUnknownStatusIsNotOk() throws {
        let json = #"{"checks":[{"name":"x","status":"later","message":"m"}]}"#
        let d = try #require(DoctorReport(data: Data(json.utf8)))
        #expect(d.checks.first?.status == .unknown)
    }

    @Test func testDoctorExitingNonZeroWithChecksIsAnAnswer() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: """
            echo "$@" > "$(dirname "$0")/args"
            echo '{"checks":[{"name":"daemon","status":"fail","message":"not running","fix":"daemon restart"}]}'
            exit 1
            """)
        let r = CLI(path: p).doctor()
        #expect(try r.get().checks.first?.fix == .daemonRestart)
        let args = try String(contentsOf: dir.appendingPathComponent("args"), encoding: .utf8)
        #expect(args.trimmingCharacters(in: .whitespacesAndNewlines) == "doctor --json")
    }

    @Test func testAnOlderDoctorSaysItNeedsANewerOne() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: """
            case "$1" in version) echo "claudeswitch 0.5.5"; exit 0;; esac
            echo "flag provided but not defined: -json" >&2; exit 2
            """)
        #expect(FeatureSupport.needsNewerCLI(CLI(path: p).doctor().failure))
    }
}
