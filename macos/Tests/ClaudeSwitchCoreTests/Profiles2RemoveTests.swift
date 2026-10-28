import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Remove profile, second QA pass (2026-10-28): a pool only the removed
/// profile uses is left unused, and the sheet says so; `default` with
/// nothing of its own to move says why claudeswitch still asks where its
/// accounts go (the CLI needs --to for default, D6); every name is quoted.
@Suite struct Profiles2RemoveTests {
    /// The shared fixture without the account in no pool: default then has
    /// nothing of its own to move.
    func noLoose() throws -> PoolList { PoolList(pools: try sharedPools().pools, unlisted: []) }

    @Test func testAPoolOnlyItUsesIsSaidToBeLeftUnused() throws {
        let (pools, list) = (try sharedPools(), try sharedProfiles())
        let w = try #require(list.profile("work"))
        let kept = ProfileRemoval.keptPools(w, pools: pools)
        #expect(kept.shared == ["team"], "default uses team too")
        #expect(kept.alone == ["ops"], "only work uses ops")
        #expect(ProfileRemoval.summary(w, to: "default", pools: pools).contains(
            "is not rotated. It has no accounts of its own; the pool it shares (“team”) and the pool only it "
                + "uses (“ops”) stay. Its folder"))
        #expect(ProfileRemoval.unusedAfter(w, pools: pools)
                == "No profile uses “ops” then: its account “ops\u{2011}1” rotates nowhere until a profile lists it.")
    }

    @Test func testOnlySharedPoolsNeedNoWarning() throws {
        let pools = try sharedPools()
        var w = try #require(try sharedProfiles().profile("work"))
        w.pools = ["team"]
        #expect(ProfileRemoval.unusedAfter(w, pools: pools) == nil)
        #expect(ProfileRemoval.summary(w, to: "default", pools: pools)
            .contains("It has no accounts of its own; the pool it shares (“team”) stays."))
        w.pools = ["ops", "spare", "team"]
        let spare = PoolList(pools: pools.pools.map { p in
            var p = p
            if p.name == "spare" { p.profiles = ["work"] }
            return p
        }, unlisted: pools.unlisted)
        #expect(ProfileRemoval.keptPools(w, pools: spare).alone == ["ops", "spare"])
        #expect(ProfileRemoval.unusedAfter(w, pools: spare)
                == "No profile uses “ops” then: its account “ops\u{2011}1” rotates nowhere until a profile lists it.",
                "an empty pool left unused strands no account")
        let two = PoolList(pools: [PoolInfo(name: "a", accounts: ["a-1", "a-2"], profiles: ["work"], own: false)],
                           unlisted: [])
        w.pools = ["a"]
        #expect(ProfileRemoval.unusedAfter(w, pools: two)
                == "No profile uses “a” then: its accounts “a\u{2011}1” and “a\u{2011}2” rotate nowhere until a "
                + "profile lists it.")
        #expect(ProfileRemoval.summary(w, to: "default", pools: two).contains("the pool only it uses (“a”) stays."))
    }

    /// The CLI refuses `profile remove default` without --to, since the
    /// accounts in no pool are default's: the sheet asks, and says why,
    /// rather than "It has no accounts." beside "Its accounts go to".
    @Test func testDefaultWithNothingToMoveSaysWhyItStillAsks() throws {
        let (pools, list) = (try noLoose(), try sharedProfiles())
        let d = try #require(list.profile("default"))
        #expect(ProfileRemoval.ownAccounts(d, pools: pools) == [])
        #expect(ProfileRemoval.needsDestination(d, pools: pools), "the CLI needs --to for default")
        #expect(ProfileRemoval.destinationLabel(d, pools: pools) == "Accounts in no pool go to")
        let s = ProfileRemoval.summary(d, to: "work", pools: pools)
        #expect(!s.contains("It has no accounts."))
        #expect(s.contains("is not rotated. It has no accounts of its own, so none move; the pool it shares (“team”) "
                           + "and the pool only it uses (“personal”) stay. Accounts in no pool belong to “default”, so "
                           + "claudeswitch asks where they would go even when, as now, there are none."))
        var bare = d
        bare.pools = []
        let empty = PoolList(pools: [], unlisted: [])
        #expect(ProfileRemoval.summary(bare, to: nil, pools: empty).contains(
            "is not rotated. It has no accounts of its own, so none move. Accounts in no pool belong to “default”"))
        // With accounts of its own the label is the usual one.
        #expect(ProfileRemoval.destinationLabel(d, pools: try sharedPools()) == "Its accounts go to")
        let w = try #require(list.profile("work"))
        #expect(!ProfileRemoval.needsDestination(w, pools: pools), "work has none to move and the CLI needs none")
    }

    @Test func testItsAccountsAreQuoted() throws {
        let legacy = try #require(PoolList(data: try fixture("cli-pool-list.json")))
        let review = try #require(ProfileList(data: try fixture("cli-profile-list.json"))?.profile("review"))
        #expect(ProfileRemoval.summary(review, to: "default", pools: legacy).contains("Its accounts (“personal”) move to “default”."))
        let mixed = PoolList(pools: [PoolInfo(name: "lab", accounts: ["work-team", "work-2"], profiles: ["lab"], own: true),
                                     PoolInfo(name: "team", accounts: ["t"], profiles: ["lab", "default"], own: false)],
                             unlisted: [])
        var lab = review
        lab.name = "lab"
        lab.pools = ["lab", "team"]
        #expect(ProfileRemoval.summary(lab, to: "default", pools: mixed).contains(
            "Its own accounts (“work\u{2011}team”, “work\u{2011}2”) move to “default”; the pool it shares (“team”) stays."))
    }

    @Test func testTheNoteOnceRemovedQuotesTheGuardedAccount() throws {
        let j = try #require(JSON(data: try fixture("cli-profile-remove.json")))
        let r = try #require(ProfileRemoved(j))
        #expect(ProfilesText.removed(r)
                == "Removed the profile “review”; its accounts are in “default”; “personal” stays guarded until that login is gone")
    }
}
