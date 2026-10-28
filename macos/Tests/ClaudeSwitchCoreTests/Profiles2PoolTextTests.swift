import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Second QA pass (2026-10-28): a profile's own pool does not repeat its
/// name, so its accounts show; every pool caption says whose it is or who
/// uses it, then its accounts; with one profile and no pools, Add account
/// has nothing to choose and says nothing about "last".
@Suite struct Profiles2PoolTextTests {
    let legacy = PoolList(data: (try? fixture("cli-pool-list.json")) ?? Data())!

    @Test func testEveryCaptionSaysWhoseFirstThenItsAccounts() throws {
        let shared = try sharedPools()
        #expect(ProfilesText.poolCaption("team", pools: shared, owner: nil) == "used by “default”, “work” · team-1, team-2, team-3")
        #expect(ProfilesText.poolCaption("spare", pools: shared, owner: nil) == "used by no profile · no accounts")
        #expect(ProfilesText.poolCaption("review", pools: legacy, owner: "review") == "its own pool · personal")
        #expect(ProfilesText.poolCaption("default", pools: legacy, owner: "review") == "a profile’s own pool · work-1, work-2",
                "the row above names it: the caption does not repeat it")
        #expect(ProfilesText.poolCaption("default", pools: legacy, owner: nil) == "a profile’s own pool · work-1, work-2",
                "New profile: no owner")
    }

    @Test func testOneSentenceForEveryOwnPoolChecked() throws {
        #expect(ProfilePools.note(profile: "lab", pools: legacy, chosen: ["default", "review"])
                == "The pools “default” and “review” belong to the profiles of the same name: checked, each becomes "
                + "shared, and its profile keeps using it.")
        #expect(ProfilePools.note(profile: "default", pools: legacy, chosen: ["default", "review"])
                == "The pool “review” belongs to the profile of the same name: checked, it becomes shared, "
                + "and that profile keeps using it.")
        #expect(ProfilePools.note(profile: "review", pools: legacy, chosen: ["default"])
                == "Its own accounts (“personal”) become the pool “review”, which no profile uses then: "
                + "they rotate nowhere until a profile lists it or you move them. "
                + "The pool “default” belongs to the profile of the same name: checked, it becomes shared, "
                + "and that profile keeps using it.")
    }

    @Test func testAddAccountNamesTheProfileThatUsesAPool() throws {
        let pools = try sharedPools()
        #expect(NewAccountPool.describe(.pool("team"), pools: pools) == "in the pool “team”, which the profiles “default” and “work” use")
        #expect(NewAccountPool.describe(.pool("ops"), pools: pools) == "in the pool “ops”, which the profile “work” uses")
        #expect(NewAccountPool.describe(.pool("default"), pools: legacy) == "in the pool “default”, which the profile “default” uses")
        #expect(NewAccountPool.options(pools: legacy, profiles: nil).first?.label == "“default”, a profile’s own pool")
    }

    /// One profile and no pools: "No pool, which “default” uses last" had
    /// nothing to be last after, and a pop-up of one choice.
    @Test func testWithOneProfileAndNoPoolsThereIsNothingToChoose() throws {
        let one = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "default", "declared": true, "pool": ["a"], "pools": []}]}
            """#.utf8)))
        let none = PoolList(pools: [], unlisted: ["a"])
        let options = NewAccountPool.options(pools: none, profiles: one)
        #expect(options == [PoolOption(pick: .none, label: "No pool, which “default” uses")])
        #expect(NewAccountPool.onlyChoice(pools: none, profiles: one) == PoolPick.none)
        #expect(NewAccountPool.describe(PoolPick.none, pools: none) == "in no pool, where “default” uses it")
        // With pools before it, No pool is last, and there is a choice.
        let (pools, profiles) = (try sharedPools(), try sharedProfiles())
        #expect(NewAccountPool.options(pools: pools, profiles: profiles).last?.label == "No pool, which “default” uses last")
        #expect(NewAccountPool.onlyChoice(pools: pools, profiles: profiles) == nil)
        // One pool and no default: that pool, said rather than offered.
        let lone = PoolList(pools: [PoolInfo(name: "team", accounts: ["t"], profiles: ["work"], own: false)], unlisted: [])
        let work = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "work", "declared": true, "pool": ["t"], "pools": ["team"]}]}
            """#.utf8)))
        #expect(NewAccountPool.onlyChoice(pools: lone, profiles: work) == .pool("team"))
    }
}
