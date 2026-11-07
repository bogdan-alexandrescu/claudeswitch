import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Next refill (owner's picks, 2026-11-06): what counts, the popover's line,
/// and the Refills timeline.
@Suite struct RefillsTests {
    let now = GoTime.parse("2026-10-08T02:46:41Z")!
    let utc = TimeZone(identifier: "UTC")!

    func at(_ hours: Double) -> Date { now.addingTimeInterval(hours * 3600) }

    func window(_ used: Double?, _ hours: Double?) -> WindowReading {
        WindowReading(utilization: used, resetsAt: hours.map(at))
    }

    func account(_ id: String, session: WindowReading? = nil, week: WindowReading? = nil,
                 status: AccountStatus = .available, read: Double = 0) -> AccountView {
        AccountView(id: id, isActive: false, isPinned: false, status: status, fiveHour: session, sevenDay: week,
                    bindingWindow: nil, bindingPct: nil, level: .ok, scopedWeekly: [], lastAt: at(read))
    }

    func refills(_ a: AccountView) -> AccountRefills {
        Refills.of(a, sessionTrigger: 85, weekTrigger: 98, now: now)
    }

    func card(_ accounts: [AccountView], live: String? = nil, week: Double = 98) -> ProfileCard {
        ProfileCard(name: "default", dir: nil, signedIn: "yes", active: accounts.first { $0.id == live }, pinned: nil,
                    decision: nil, switchAt: 85, switchAtWeekly: week, pool: accounts.map(\.id), accounts: accounts)
    }

    // MARK: what counts

    /// The usual case in a hard-worked pool: its session resets in an hour,
    /// but its week stays shut for days, so only the week's refill can be
    /// spent.
    @Test func testASessionResetOnAWeekShutAccountDoesNotCount() {
        let r = refills(account("dune-max", session: window(4, 1), week: window(98, 71)))
        #expect(!r.hasRoom)
        #expect(r.refills.map(\.window) == ["seven_day"])
        #expect(r.next?.frees == 98)
        #expect(r.next?.at == at(71))
    }

    @Test func testAnAccountWithRoomCountsBothSoonestFirst() {
        let r = refills(account("cedar-max", session: window(62, 0.8), week: window(41, 122)))
        #expect(r.hasRoom)
        #expect(r.refills.map(\.window) == ["five_hour", "seven_day"])
        #expect(r.next?.account == "cedar-max")
    }

    @Test func testAResetUnderFivePointsIsNotWorthSaying() {
        let r = refills(account("low", session: window(4.9, 1), week: window(5, 100)))
        #expect(r.refills.map(\.window) == ["seven_day"], "exactly five points counts, just under does not")
        #expect(refills(account("idle", session: window(0, nil), week: window(3, 100))).next == nil)
        #expect(refills(account("idle", session: window(0, nil), week: window(3, 100))).hasRoom)
    }

    @Test func testAResetAlreadyPastIsAnEmptyWindow() {
        let r = refills(account("harbor", week: window(98, -0.3), read: -1))
        #expect(r.hasRoom, "its week reset after the reading: it has room, though no new figure says so")
        #expect(r.next == nil, "and nothing is left to come back")
    }

    @Test func testOnlyASignInBringsItBackSoNoResetCounts() {
        let r = refills(account("gale", session: window(40, 2), week: window(40, 72), status: .needsLogin))
        #expect(!r.hasRoom)
        #expect(r.refills.isEmpty)
    }

    @Test func testNoReadingClaimsNothing() {
        let r = refills(account("new", status: .unknown))
        #expect(!r.hasRoom)
        #expect(r.refills.isEmpty)
    }

    @Test func testARefusalCountsWhenItClears() {
        let refused = AccountStatus.refused(window: "seven_day", until: at(56))
        let r = refills(account("ember", session: window(30, 2), week: window(100, 56), status: refused))
        #expect(!r.hasRoom)
        #expect(r.refills.map(\.window) == ["seven_day"], "the earlier session reset gives nothing to spend")
        #expect(r.next?.at == at(56))
    }

    /// The API's retry time and the window's reset can differ a little: the
    /// account gives quota back only once the refusal has cleared.
    @Test func testARefusalLaterThanItsWindowsResetCountsWhenItClears() {
        let refused = AccountStatus.refused(window: "five_hour", until: at(2.1))
        let r = refills(account("atlas", session: window(100, 2), week: window(16, 38), status: refused))
        #expect(r.next?.window == "five_hour")
        #expect(r.next?.at == at(2.1))
    }

    @Test func testARefusalWithNoTimeSaysNothingAboutWhen() {
        let r = refills(account("x", session: window(100, 2), week: window(16, 38),
                                status: .refused(window: nil, until: nil)))
        #expect(!r.hasRoom)
        #expect(r.refills.isEmpty)
    }

    @Test func testOverATriggerWithNoResetTimeSaysNothingAboutWhen() {
        let r = refills(account("x", session: window(10, 2), week: window(99, nil)))
        #expect(!r.hasRoom)
        #expect(r.refills.isEmpty)
    }

    @Test func testShutOnBothWindowsOnlyTheLaterResetCounts() {
        let weekLast = refills(account("x", session: window(90, 1), week: window(99, 40)))
        #expect(weekLast.refills.map(\.window) == ["seven_day"])
        let sessionLast = refills(account("y", session: window(90, 4), week: window(99, 1)))
        #expect(sessionLast.refills.map(\.window) == ["five_hour"])
    }

    @Test func testAnIdleSessionWithNoWindowOpenIsNoRefill() {
        let r = refills(account("x", session: window(nil, nil), week: window(98, 43)))
        #expect(!r.hasRoom)
        #expect(r.refills.map(\.window) == ["seven_day"])
    }

    /// Settings judges an account by the profile it is live in, else the
    /// first that uses it, as Snapshot.build does.
    @Test func testEveryAccountIsJudgedByItsOwnProfilesTriggers() throws {
        let state = try #require(StateFile(data: Data("""
        {"accounts": {
            "a": {"last_usage": {"five_hour": {"utilization": 0}, "seven_day": {"utilization": 90, "resets_at": "2026-10-09T02:46:41Z"}}, "last_at": "2026-10-08T02:45:00Z"}
        }, "active_account": "a"}
        """.utf8)))
        let strict = Snapshot.build(state: state, why: nil, settings: ConfigSettings(switchAt: 85, switchAtWeekly: 80),
                                    now: now)
        #expect(Refills.all(strict).first?.hasRoom == false)
        let loose = Snapshot.build(state: state, why: nil, settings: ConfigSettings(switchAt: 85, switchAtWeekly: 98),
                                   now: now)
        #expect(Refills.all(loose).first?.hasRoom == true)
    }

    // MARK: the popover's line

    @Test func testTheLineIsTheSoonestOfTheOtherAccounts() {
        let c = card([account("atlas-max", session: window(80, 0.5), week: window(16, 38)),
                      account("boreal-team", week: window(98, 43)),
                      account("cedar-max", session: window(62, 0.8), week: window(41, 122))], live: "atlas-max")
        #expect(RefillText.nextLine(c, now: now) == "Next refill: cedar-max in 48m · 62% of session",
                "the live account's own resets are on its dials, just above, so they do not lead the line")
    }

    @Test func testALongNameIsCutAndTheFiguresNeverAre() {
        let long = "research-team-extremely-long-account-name"
        let c = card([account(long, session: window(92, 0.2), week: window(30, 96))])
        #expect(RefillText.nextLine(c, now: now) == "Next refill: research-t…ount-name in 12m · 92% of session")
    }

    @Test func testNoLineWithNothingToComeBack() {
        #expect(RefillText.nextLine(card([account("a", session: window(0, nil), week: window(0, nil))]), now: now) == nil)
        #expect(RefillText.nextLine(card([account("a", session: window(90, 1), week: window(9, 90))], live: "a"),
                                    now: now) == nil, "only the live account, whose dials say it")
    }

    @Test func testThePoolIsJudgedByTheCardsOwnTriggers() {
        let a = account("a", session: window(10, 1), week: window(98, 40))
        #expect(RefillText.nextLine(card([a], week: 98), now: now)?.contains("of week") == true)
        #expect(RefillText.nextLine(card([a], week: 99), now: now)?.contains("of session") == true,
                "under a 99% trigger its week still has room, so its session reset can be spent")
    }

    @Test func testTheTooltipNamesTheNextFourInFull() {
        let long = "research-team-extremely-long-account-name"
        let c = card(["a", "b", "c", "d", "e"].enumerated().map { i, id in
            account(id, week: window(98, 10 + Double(i)))
        } + [account(long, session: window(62, 0.8), week: window(10, 20))])
        let lines = RefillText.tooltip(c, now: now, timeZone: utc)?.components(separatedBy: "\n") ?? []
        #expect(lines.count == 4)
        #expect(lines[0] == long + ": 62% of session in 48m (Thu 03:34)")
        #expect(lines[1] == "a: 98% of week in 10h (Thu 12:46)")
        #expect(RefillText.tooltip(card([]), now: now) == nil)
    }

    // MARK: the pane's words

    @Test func testTheSummarySaysHowManyHaveRoomAndWhatIsNext() {
        let room = refills(account("a", session: window(62, 0.8), week: window(41, 122)))
        let shut = refills(account("b", week: window(98, 43)))
        #expect(RefillText.summary([room, shut], now: now) == "1 of 2 has room now · next refill in 48m")
        #expect(RefillText.summary([room, room], now: now) == "2 of 2 have room now · next refill in 48m")
        #expect(RefillText.summary([refills(account("c", week: window(0, nil)))], now: now) == "1 of 1 has room now")
        #expect(RefillText.summary([], now: now) == "No accounts yet")
    }

    // MARK: the timeline

    func pool() -> [AccountRefills] {
        [refills(account("cedar-max", session: window(62, 0.8), week: window(41, 122))),
         refills(account("atlas-max", session: window(80, 1.6), week: window(16, 38))),
         refills(account("boreal-team", week: window(98, 43))),
         refills(account("gale-max", session: window(30, 1), status: .needsLogin))]
    }

    @Test func testAWeekShowsEveryRefillWithALanePerAccountInOrderOfTheirFirst() {
        let t = RefillTimeline.build(pool(), zoom: .week, atLeast: .any, now: now)
        #expect(t.lanes == ["cedar-max", "atlas-max", "boreal-team"], "one needing sign-in has no refill, so no lane")
        #expect(t.events.map(\.id) == ["cedar-max/five_hour", "atlas-max/five_hour", "atlas-max/seven_day",
                                       "boreal-team/seven_day", "cedar-max/seven_day"])
    }

    @Test func testAZoomKeepsOnlyWhatComesWithinIt() {
        let t = RefillTimeline.build(pool(), zoom: .day, atLeast: .any, now: now)
        #expect(t.lanes == ["cedar-max", "atlas-max"])
        #expect(t.events.allSatisfy { !$0.isWeek })
    }

    @Test func testTheAmountFilterKeepsOnlyWhatFreesThatMuch() {
        let t = RefillTimeline.build(pool(), zoom: .week, atLeast: .most, now: now)
        #expect(t.events.map(\.id) == ["atlas-max/five_hour", "boreal-team/seven_day"])
    }

    @Test func testAPositionIsHowFarAlongTheZoomItComes() {
        let e = Refill(account: "a", window: "five_hour", at: at(2.5), frees: 50)
        #expect(RefillZoom.hours.position(e, now: now) == 0.5)
        #expect(RefillZoom.hours.ticks.map(\.label) == ["1h", "2h", "3h", "4h", "5h"])
        #expect(RefillZoom.day.ticks.map(\.label) == ["6h", "12h", "18h", "1d"])
        #expect(RefillZoom.week.ticks.map(\.label) == ["1d", "2d", "3d", "4d", "5d", "6d", "7d"])
        #expect(RefillZoom.week.ticks.last?.at == 1)
    }

    @Test func testTheEmptyTimelineSaysWhichEmptyItIs() {
        #expect(RefillTimeline.build([], zoom: .week, atLeast: .any, now: now).emptyLine == "No refill coming")
        #expect(RefillTimeline.build(pool(), zoom: .hours, atLeast: .most, now: now).emptyLine == nil,
                "atlas-max's 80% session comes in under five hours")
        let t = RefillTimeline.build([refills(account("b", week: window(98, 43)))], zoom: .day, atLeast: .any, now: now)
        #expect(t.emptyLine == "No refill matches")
        #expect(t.canWiden)
        #expect(!RefillTimeline.build([], zoom: .week, atLeast: .any, now: now).canWiden)
    }
}
