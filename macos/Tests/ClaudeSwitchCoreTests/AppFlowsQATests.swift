import Foundation
import Testing
@testable import ClaudeSwitchCore

/// App flows after the 2026-10-28 QA.
@Suite struct AppFlowsQATests {
    /// A switch made from the app is recorded before the CLI answers (a
    /// refresh can land first); a refused one is forgotten, or it would hide
    /// the next real rotation notice to that account for two minutes.
    @Test func testARefusedSwitchIsForgotten() {
        var s = AppSwitches()
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        s.record(profile: "work", to: "w02", at: now)
        #expect(s.made(profile: "work", to: "w02", now: now))
        s.forget(profile: "work", to: "w02")
        #expect(!s.made(profile: "work", to: "w02", now: now))
    }

    /// The hotkey's answer says why there is nothing to switch to, not that
    /// the card is on its best account.
    @Test func testTheHotkeySaysWhyNothingWasSwitched() {
        #expect(ShortcutAnswer.noBest(profile: "work", alreadyOnBest: false, why: "every account is out")
            == "work has no account to switch to: every account is out")
        #expect(ShortcutAnswer.noBest(profile: "work", alreadyOnBest: true, why: nil)
            == "work is already on its best account.")
    }

    @Test func testAllowingKeychainAccessWarnsOfThePrompts() {
        #expect(FixAction.keychainAllow.warning?.contains("password") == true)
        #expect(FixAction.daemonRestart.warning == nil)
    }
}
