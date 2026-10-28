import Foundation
import Testing
@testable import ClaudeSwitchCore

// QA of the update flow (2026-10-28). With Advanced → Check for updates off,
// the Overview's Updates tile ("Check is off") opened the window on
// "Checking for updates…", but off makes no call, so nothing ever answered
// and the window offered only Cancel. Off now says so in the window, with
// Turn On, and the tile opens Advanced instead.

@Suite struct UpdateFlowTests {
    @Test func testCheckForUpdatesWithTheCheckOffMakesNoCallAndSaysSo() {
        #expect(UpdateDialog.checkNow(enabled: true, showing: nil) == .checking)
        #expect(UpdateDialog.checkNow(enabled: true, showing: .checkFailed("offline")) == .checking,
                "Try Again checks again")
        #expect(UpdateDialog.checkNow(enabled: false, showing: nil) == .checkOff, "off makes no call at all")
        #expect(UpdateDialog.checkNow(enabled: false, showing: .checkFailed("offline")) == .checkOff,
                "Try Again after the check was turned off")
        let installing = UpdateDialog.installing(version: "0.5.6", step: .installingApp)
        #expect(UpdateDialog.checkNow(enabled: true, showing: installing) == installing, "an install stays in view")
        #expect(UpdateDialog.checkNow(enabled: false, showing: installing) == installing)

        let off = UpdateDialog.checkOff
        #expect(off.title == "Checking for updates is off")
        #expect(off.message.contains("never contacts GitHub"))
        #expect(off.message.contains("Turn On"))
    }

    @Test func testTheUpdatesTileOpensAdvancedWhenTheCheckIsOff() {
        var u = OverviewUpdate(current: "0.8.12", available: nil, lastCheck: nil, enabled: false)
        #expect(u.action == .advanced, "the setting, not a check that is never made")
        u.enabled = true
        #expect(u.action == .check)
        u.available = "0.9.0"
        #expect(u.action == .offer)
        u.enabled = false
        #expect(u.action == .advanced)
    }
}

/// A failed check stored its time as the last check, so the Overview's
/// Updates tile said "Up to date" and Advanced "checked just now" (QA
/// 2026-10-28). Only a check that read the release is the last check; the
/// last failure is kept apart, with when and why.
@Suite struct UpdateFailedCheckTests {
    let now = fixtureNow
    var offline: UpdateCheckFailure {
        UpdateCheckFailure(at: now.addingTimeInterval(-300),
                           message: UpdateError.noRelease("The Internet connection appears to be offline").message)
    }

    @Test func testAdvancedSaysTheLastCheckFailedNotThatItChecked() {
        #expect(UpdateStatus.line(current: "0.8.12", available: nil, lastCheck: now.addingTimeInterval(-3600),
                                  failure: nil, now: now) == "This is 0.8.12 · checked 1h ago")
        #expect(UpdateStatus.line(current: "0.8.12", available: nil, lastCheck: now.addingTimeInterval(-3 * 86_400),
                                  failure: offline, now: now) == "This is 0.8.12 · last check failed 5m ago")
        #expect(UpdateStatus.line(current: "0.8.12", available: "0.9.0", lastCheck: nil, failure: offline, now: now)
            == "This is 0.8.12 · 0.9.0 available · last check failed 5m ago")
        #expect(UpdateStatus.line(current: nil, available: nil, lastCheck: nil, failure: nil, now: now)
            == "Version unknown")
    }

    /// A failed check is the day's call: it is tried again the next day,
    /// not every hour.
    @Test func testAFailedCheckIsTriedAgainTheNextDay() {
        let old = now.addingTimeInterval(-3 * 86_400)
        #expect(!UpdateSchedule.due(enabled: true, lastCheck: old, failed: now.addingTimeInterval(-3600), now: now))
        #expect(UpdateSchedule.due(enabled: true, lastCheck: old, failed: now.addingTimeInterval(-86_400), now: now))
        #expect(!UpdateSchedule.due(enabled: true, lastCheck: nil, failed: now.addingTimeInterval(-3600), now: now))
        #expect(UpdateSchedule.due(enabled: true, lastCheck: nil, failed: nil, now: now))
        #expect(!UpdateSchedule.due(enabled: false, lastCheck: old, failed: nil, now: now), "off means no call at all")
    }
}

/// A copy without a version (swift run) has nothing to compare the release
/// with, but the window said "ClaudeSwitch is up to date" (QA 2026-10-28).
@Suite struct UpdateUnknownVersionTests {
    @Test func testAnUnknownVersionIsNotCalledUpToDate() throws {
        let r = try fixtureRelease()
        for current in [nil, "dev"] as [String?] {
            let d = UpdateDialog.checked(.success(r), current: current, arch: .arm64)
            #expect(d == .versionUnknown(latest: "0.5.6"), "\(current ?? "nil")")
            #expect(d.title == "This copy’s version is unknown")
            #expect(d.message.contains("0.5.6") && !d.message.contains("up to date"))
        }
        #expect(UpdateDialog.checked(.success(r), current: "0.5.6", arch: .arm64).title == "ClaudeSwitch is up to date")
    }
}

extension OverviewTests {
    /// The tile said "Up to date" beside "Version unknown".
    @Test func testTheUpdatesTileDoesNotClaimUpToDateWithoutAVersion() throws {
        var i = input(try snapshot())
        i.update.current = nil
        let tile = try #require(Overview.build(i).tiles.first { $0.title == "Updates" })
        #expect(tile.primary == "Version unknown" && tile.secondary == "checked 1h ago")
        i.update.current = "0.8.12"
        #expect(Overview.build(i).tiles.first { $0.title == "Updates" }?.primary == "Up to date")
    }

    @Test func testTheUpdatesTileSaysTheLastCheckFailed() throws {
        var i = input(try snapshot())
        i.update.failure = UpdateCheckFailure(at: fixtureNow.addingTimeInterval(-7200), message: "offline")
        func tile() throws -> OverviewTile { try #require(Overview.build(i).tiles.first { $0.title == "Updates" }) }
        #expect(try tile().primary == "Last check failed", "a check that failed is not up to date")
        #expect(try tile().secondary == "This is 0.8.12 · failed 2h ago")
        #expect(try !tile().attention)
        i.update.lastCheck = nil
        #expect(try tile().primary == "Last check failed", "nor is it not checked yet")
        i.update.available = "0.9.0"
        #expect(try tile().primary == "0.9.0 available", "what an earlier check found still stands")
        i.update.enabled = false
        #expect(try tile().primary == "Check is off" && tile().secondary == "This is 0.8.12")
        #expect(OverviewUpdate(current: "0.8.12", available: nil, lastCheck: nil, enabled: true).failure == nil)
    }
}
