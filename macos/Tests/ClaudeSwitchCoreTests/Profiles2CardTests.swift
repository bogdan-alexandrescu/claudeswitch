import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Second QA pass (2026-10-28) on the cards and New profile: a closed card
/// says its thresholds as every other view does; a profile that uses no
/// pool says so, not "No pools yet"; a global "none" reads as a value; New
/// profile says why Create is off, and when the profile would start with
/// no accounts.
@Suite struct Profiles2CardTests {
    @Test func testAClosedCardSaysItsThresholdsAsEveryViewDoes() throws {
        let (pools, list) = (try sharedPools(), try sharedProfiles())
        let w = try #require(list.profile("work"))
        #expect(ProfilesText.closedLine(w, pools: pools) == "~/.claude-work · pools ops, team · session 90% · week 95%")
        #expect(ProfilesText.closedLine(w, pools: nil) == "~/.claude-work · ops-1, team-1, team-2, team-3 · session 90% · week 95%",
                "an older CLI: its accounts")
        var bare = w
        bare.pool = []
        bare.thresholds.switchAt = nil
        #expect(ProfilesText.closedLine(bare, pools: nil) == "~/.claude-work · no accounts", "no thresholds read")
        #expect(ProfilesText.usesNoPool == "Uses no pool")
    }

    @Test func testAGlobalNoneReadsAsTheGlobalValue() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let models = try #require(schema.setting("models"))
        #expect(ProfilesText.overrideCaption(models, overridden: true, global: "") == "overrides the global value (none)",
                "not “overrides the global none”")
        #expect(ProfilesText.overrideCaption(models, overridden: true, global: "Modelname") == "overrides the global Modelname")
        let pct = try #require(schema.setting("switch_at"))
        #expect(ProfilesText.overrideCaption(pct, overridden: true, global: "90") == "overrides the global 90%")
    }

    @Test func testNewProfileSaysWhyCreateIsOff() {
        #expect(ProfilesText.enterName == "Enter a name to continue.")
        #expect(ProfilesText.newProfileOwn
                == "Accounts in no pool, which “default” uses now: the ones checked become this profile’s own.")
    }

    /// Nothing gives it an account: no account of its own checked, and no
    /// checked pool with one. Allowed, but said.
    @Test func testAnEmptyNewProfileIsSaid() throws {
        let pools = try sharedPools()
        let empty = "It starts with no accounts: check a pool, or add one later."
        #expect(ProfilesText.startsEmpty(own: [], chosen: [], pools: pools, offersAccounts: false, offersPools: true) == empty)
        #expect(ProfilesText.startsEmpty(own: [], chosen: ["spare"], pools: pools, offersAccounts: false, offersPools: true)
                == empty, "spare has no accounts")
        #expect(ProfilesText.startsEmpty(own: [], chosen: ["ops"], pools: pools, offersAccounts: false, offersPools: true) == nil)
        #expect(ProfilesText.startsEmpty(own: ["loose-1"], chosen: [], pools: pools, offersAccounts: true, offersPools: true) == nil)
        #expect(ProfilesText.startsEmpty(own: [], chosen: [], pools: pools, offersAccounts: true, offersPools: true)
                == "It starts with no accounts: check one of its accounts or a pool, or add one later.")
        #expect(ProfilesText.startsEmpty(own: [], chosen: [], pools: nil, offersAccounts: true, offersPools: false)
                == "It starts with no accounts: check one of its accounts, or add one later.")
        #expect(ProfilesText.startsEmpty(own: [], chosen: [], pools: nil, offersAccounts: false, offersPools: false)
                == "It starts with no accounts: add one later.")
    }
}
