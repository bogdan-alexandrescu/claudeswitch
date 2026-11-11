import Foundation
import Testing
@testable import ClaudeSwitchCore

// The drain forecast (docs/IMPROVEMENTS.md, "Drain forecast"): how many hours
// of use each account and each profile's pool has left before the switch
// point. Read from `account list --json`'s `drain` and `why --json`'s
// `pool_drain`; an older CLI hides every line, and unknown is never 0.

private func drainSnapshot() throws -> Snapshot {
    Snapshot.build(state: StateFile(data: try fixture("state.json")),
                   why: WhyReport(data: try fixture("why-drain.json")),
                   settings: ConfigSettings(data: try fixture("config.json")),
                   profiles: ProfileList(data: try fixture("cli-profile-list.json")),
                   accounts: AccountList(data: try fixture("cli-account-list-drain.json")),
                   now: fixtureNow)
}

private func drain(_ state: DrainState, _ hours: Double? = nil, speed: Double? = nil,
                   elsewhere: Bool = false) -> AccountDrain {
    AccountDrain(state: state, hoursLeft: hours, speedPerHour: speed, usedElsewhere: elsewhere)
}

private func pool(_ state: DrainState, _ hours: Double? = nil, accounts: Int = 6) -> PoolDrain {
    PoolDrain(state: state, accounts: accounts, hoursLeft: hours, speedPerHour: hours == nil ? nil : 2.6)
}

@Suite struct DrainReadingTests {
    @Test func testAccountListCarriesEachAccountsDrain() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list-drain.json")))
        #expect(l.account("work-1")?.drain == drain(.hours, 21.2, speed: 2.6))
        #expect(l.account("work-2")?.drain == drain(.hours, 11.3, speed: 6, elsewhere: true))
        #expect(l.account("personal")?.drain == drain(.unknown))
    }

    @Test func testAnOlderAccountListHasNoDrain() throws {
        let l = try #require(AccountList(data: try fixture("cli-account-list-next.json")))
        #expect(l.accounts.allSatisfy { $0.drain == nil })
    }

    @Test func testAWordANewerCLIAddsReadsAsUnknown() throws {
        let l = try #require(AccountList(data: Data(#"{"accounts": [{"id": "a", "drain": {"state": "later"}}]}"#.utf8)))
        #expect(l.account("a")?.drain?.state == .unknown)
    }

    @Test func testWhyCarriesEachPoolsDrain() throws {
        let w = try #require(WhyReport(data: try fixture("why-drain.json")))
        #expect(w.profiles.first { $0.profile == "default" }?.poolDrain
                == PoolDrain(state: .hours, accounts: 2, hoursLeft: 32.5, speedPerHour: 2.6))
        #expect(w.profiles.first { $0.profile == "review" }?.poolDrain?.state == .unknown)
        let old = try #require(WhyReport(data: try fixture("why-profiles.json")))
        #expect(old.profiles.allSatisfy { $0.poolDrain == nil })
    }

    @Test func testTheCardsCarryTheForecasts() throws {
        let s = try drainSnapshot()
        let def = try #require(s.card("default"))
        #expect(def.poolDrain?.hoursLeft == 32.5)
        #expect(def.active?.drain?.hoursLeft == 21.2)
        #expect(def.accounts.first { $0.id == "work-2" }?.drain?.usedElsewhere == true)
        #expect(s.accounts.first { $0.id == "work-1" }?.drain?.hoursLeft == 21.2, "Settings reads them too")
        let none = Snapshot.build(state: StateFile(data: try fixture("state.json")),
                                  why: WhyReport(data: try fixture("why-profiles.json")), settings: nil,
                                  profiles: ProfileList(data: try fixture("cli-profile-list.json")), now: fixtureNow)
        #expect(none.cards.allSatisfy { $0.poolDrain == nil && $0.accounts.allSatisfy { $0.drain == nil } })
    }

    /// The app's snapshot reads them from the account list it already loads.
    @Test func testTheAppsSnapshotUsesItsAccountList() throws {
        let d = AppData(state: StateFile(data: try fixture("state.json")),
                        why: WhyReport(data: try fixture("why-drain.json")),
                        profiles: ProfileList(data: try fixture("cli-profile-list.json")),
                        accountList: AccountList(data: try fixture("cli-account-list-drain.json")))
        #expect(d.snapshot(now: fixtureNow)?.card("default")?.active?.drain?.hoursLeft == 21.2)
    }
}

@Suite struct DrainTextTests {
    /// As the CLI says it: whole hours, and under an hour and a half minutes
    /// to the nearest five.
    @Test func testHoursAreRoundedToWhatTheyCanSay() {
        #expect(DrainText.hours(21.2) == "~21h")
        #expect(DrainText.hours(1.6) == "~2h")
        #expect(DrainText.hours(1.4) == "~85m")
        #expect(DrainText.hours(0.75) == "~45m")
        #expect(DrainText.hours(0.01) == "~5m")
    }

    @Test func testSpeedDropsAWholeFiguresDecimal() {
        #expect(DrainText.speed(2.6) == "2.6% an hour")
        #expect(DrainText.speed(10) == "10% an hour")
        #expect(DrainText.speed(4.04) == "4% an hour")
    }

    /// The switch menu and Settings → Accounts: brief, and in words where
    /// the CLI has a legend for ↑.
    @Test func testTheShortFormSaysEachForecast() {
        #expect(DrainText.short(drain(.hours, 21.2, speed: 2.6)) == "~21h left")
        #expect(DrainText.short(drain(.hours, 11.3, speed: 6, elsewhere: true)) == "~11h left · used elsewhere")
        #expect(DrainText.short(drain(.lastsToReset, 90)) == "lasts to reset")
        #expect(DrainText.short(drain(.idle, speed: 0)) == "∞")
        #expect(DrainText.short(drain(.drained)) == "drained")
        #expect(DrainText.short(drain(.unknown)) == nil, "unknown is hidden, never a guess")
        #expect(DrainText.short(nil) == nil)
    }

    /// Under the live account's dials.
    @Test func testTheLiveLineSaysHowLongAtWhatSpeed() {
        #expect(DrainText.live(drain(.hours, 21.2, speed: 2.6)) == "~21h of use left at 2.6% an hour")
        #expect(DrainText.live(drain(.lastsToReset, 90, speed: 0.5)) == "lasts until the week clears at 0.5% an hour")
        #expect(DrainText.live(drain(.idle, speed: 0)) == "∞, not being spent")
        #expect(DrainText.live(drain(.drained)) == "drained: at its switch point")
        #expect(DrainText.live(drain(.unknown)) == nil)
        #expect(DrainText.live(nil) == nil)
    }

    @Test func testThePoolLineSaysHowLongThePoolLasts() {
        #expect(DrainText.poolLine(profile: "work", pool(.hours, 83.1)) == "work pool: ~83h of use left across 6 accounts")
        #expect(DrainText.poolLine(profile: "work", pool(.hours, 4, accounts: 1)) == "work pool: ~4h of use left in 1 account")
        #expect(DrainText.poolLine(profile: "work", pool(.idle)) == "work pool: ∞, not being spent (6 accounts with room)")
        #expect(DrainText.poolLine(profile: "work", pool(.idle, accounts: 1)) == "work pool: ∞, not being spent (1 account with room)")
        #expect(DrainText.poolLine(profile: "work", pool(.drained, accounts: 0)) == "work pool: drained, no account under its switch point")
        #expect(DrainText.poolLine(profile: "work", pool(.unknown)) == nil)
        #expect(DrainText.poolLine(profile: "work", nil) == nil)
    }

    @Test func testUnderTwoHoursOfUseOrDrainedIsLow() {
        #expect(DrainText.low(pool(.hours, 1.9)))
        #expect(!DrainText.low(pool(.hours, 2)))
        #expect(DrainText.low(pool(.drained, accounts: 0)))
        #expect(!DrainText.low(pool(.idle)))
        #expect(!DrainText.low(pool(.unknown)))
        #expect(!DrainText.low(nil))
    }

    @Test func testOverviewSaysThePoolInBrief() {
        #expect(DrainText.poolShort(pool(.hours, 83.1)) == "~83h of use left")
        #expect(DrainText.poolShort(pool(.idle)) == "∞, not being spent")
        #expect(DrainText.poolShort(pool(.drained, accounts: 0)) == "drained")
        #expect(DrainText.poolShort(pool(.unknown)) == nil)
    }
}

@Suite struct DrainViewsTests {
    let now = GoTime.parse("2026-10-08T02:46:41Z")!

    func account(_ d: AccountDrain?) -> AccountView {
        AccountView(id: "w09", isActive: false, isPinned: false, status: .available,
                    fiveHour: nil, sevenDay: nil, bindingWindow: "seven_day", bindingPct: 56, level: .ok,
                    scopedWeekly: [], drain: d)
    }

    /// The switch menu says how long each account lasts where it used to
    /// say when it reaches its trigger.
    @Test func testThePickerSaysHowLongEachAccountLasts() {
        #expect(Format.pickerLine(account(drain(.hours, 21.2, speed: 2.6)), inUseBy: nil, now: now)
                == "w09  56% week · available · ~21h left")
        #expect(Format.pickerLine(account(drain(.idle, speed: 0)), inUseBy: nil, now: now)
                == "w09  56% week · available · ∞")
        #expect(Format.pickerLine(account(drain(.hours, 11.3, speed: 6, elsewhere: true)), inUseBy: nil, now: now)
                == "w09  56% week · available · ~11h left · used elsewhere")
        #expect(Format.pickerLine(account(nil), inUseBy: nil, now: now) == "w09  56% week · available")
    }

    /// The menu-bar title names the live account's hours only when they run
    /// low, so it stays short and the hours read as a warning.
    @Test func testTheMenuBarNamesTheHoursOnlyWhenLow() throws {
        var s = try drainSnapshot()
        #expect(MenuTitle.of(s, compact: false, profile: "default").text == "default · work-1 41%")
        s.cards[0].active?.drain = drain(.hours, 0.75, speed: 9)
        #expect(MenuTitle.of(s, compact: false, profile: "default").text == "default · work-1 41% · ~45m")
        #expect(MenuTitle.of(s, compact: true, profile: "default").text == "", "the icon alone stays as it was")
        s.cards[0].active?.drain = drain(.lastsToReset, 1, speed: 9)
        #expect(MenuTitle.of(s, compact: false, profile: "default").text == "default · work-1 41%",
                "lasting to the reset is not low")
    }

    /// Settings → Accounts: after the figure; it gives way before the
    /// figure does, and after when each window clears.
    @Test func testTheAccountsRowSaysHowLongItLasts() {
        let v = AccountLine.variants(usedBy: ["work"], figure: "44% week", lasts: "~21h left",
                                     clears: "week clears in 6d", chrome: nil, seat: nil, login: nil)
        #expect(v.first == "used by work · 44% week · ~21h left · week clears in 6d")
        #expect(v.contains("used by work · 44% week · ~21h left"))
        #expect(v.last == "44% week")
        #expect(AccountLine.variants(usedBy: ["work"], figure: "44% week", chrome: nil, seat: nil, login: nil).first
                == "used by work · 44% week", "no forecast, no gap")
    }
}

@Suite struct DrainOverviewTests {
    func input() throws -> OverviewInput {
        var s = try drainSnapshot()
        for k in s.accounts.indices { s.accounts[k].status = .available }
        for k in s.cards.indices { s.cards[k].pinned = nil }
        return OverviewInput(daemon: s.daemon, cards: s.cards, accounts: s.accounts, pools: nil, chrome: [],
                             clean: nil,
                             update: OverviewUpdate(current: "0.8.18", available: nil, lastCheck: nil, enabled: false),
                             switches: [], now: fixtureNow)
    }

    @Test func testTheRotationTileSaysHowLongThePoolLasts() throws {
        let t = try #require(Overview.build(try input()).tiles.first { $0.title == "Rotation" })
        #expect(t.secondary == "default pool: ~33h of use left", "\(t.secondary)")
        #expect(!t.attention)
        var one = try input()
        one.cards = [one.cards[0]]
        #expect(Overview.build(one).tiles.first { $0.title == "Rotation" }?.secondary == "pool: ~33h of use left")
    }

    /// Under two hours of use the pool is the person's to act on, and the
    /// tile speaks for it.
    @Test func testALowPoolIsANeed() throws {
        var i = try input()
        i.cards[1].poolDrain = PoolDrain(state: .hours, accounts: 1, hoursLeft: 1.2, speedPerHour: 9)
        let o = Overview.build(i)
        #expect(o.needs.contains { $0.text == "“review”’s pool has ~70m of use left" && $0.target == .rotation },
                "\(o.needs.map(\.text))")
        let t = try #require(o.tiles.first { $0.title == "Rotation" })
        #expect(t.secondary == "review pool: ~70m of use left" && t.attention, "\(t.secondary)")
        i.cards[1].poolDrain = PoolDrain(state: .drained, accounts: 0, hoursLeft: nil, speedPerHour: nil)
        #expect(Overview.build(i).needs.contains { $0.text == "“review”’s pool is drained" })
    }

    @Test func testAnUnknownPoolKeepsTheThresholds() throws {
        var i = try input()
        for k in i.cards.indices { i.cards[k].poolDrain = nil }
        let t = try #require(Overview.build(i).tiles.first { $0.title == "Rotation" })
        #expect(t.secondary.hasPrefix("default: session "))
    }
}
