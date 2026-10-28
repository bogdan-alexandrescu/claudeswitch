import Foundation
import Testing
@testable import ClaudeSwitchCore

/// New profile runs `profile create`, then `profile pools`, then `profile
/// seed`. QA 2026-10-28: when create worked and a later step failed, the
/// sheet stayed as it was with Create on, and Create again failed with
/// "already exists". Once the profile exists only what is left is retried.
@Suite struct NewProfileRunTests {
    let plan = NewProfilePlan.make(name: "client-x", pools: ["team"], seed: "team-1", candidates: ["team-1"])
    var x: String { ProfilesText.q("client-x") }

    @Test func testTheStepsInOrder() {
        var r = NewProfileRun(plan: plan)
        #expect(r.steps == [.create, .pools, .seed])
        #expect(r.remaining == [.create, .pools, .seed])
        #expect(!r.created && !r.interrupted && !r.finished)
        r.succeeded(.create)
        r.succeeded(.pools)
        r.succeeded(.seed)
        #expect(r.finished && !r.interrupted)
        #expect(r.note == "Created the profile \(x); it uses “team”")
        let bare = NewProfileRun(plan: NewProfilePlan.make(name: "lab", pools: [], seed: nil, candidates: []))
        #expect(bare.steps == [.create], "no pools chosen, no account to sign in with")
        #expect(bare.note == "Created the profile “lab”")
    }

    @Test func testAFailedCreateKeepsTheForm() {
        var r = NewProfileRun(plan: plan)
        r.failed(.create, code: "exists", message: "a profile named client-x exists", hint: "")
        #expect(!r.created && !r.interrupted && r.headline == nil, "nothing was made: the form stays and Create tries again")
        #expect(r.remaining.first == .create)
    }

    @Test func testOnceCreatedOnlyWhatIsLeftIsRetried() {
        var r = NewProfileRun(plan: plan)
        r.succeeded(.create)
        r.failed(.pools, code: "live", message: "team-1 is live in work", hint: "")
        #expect(r.interrupted, "the sheet says what was done, with Retry and Close")
        #expect(r.headline == "The profile \(x) was created, but giving it its pools failed.")
        #expect(r.failure?.message == "team-1 is live in work")
        #expect(r.failure?.hint == "Retry, or Close and choose them later from its card in Profiles (Change…).")
        #expect(r.remaining == [.pools, .seed], "never profile create again")

        r.succeeded(.pools)
        #expect(r.interrupted && r.failure == nil)
        #expect(r.headline == "The profile \(x) was created; signing it in…", "while the rest runs")
        r.failed(.seed, code: "daemon_not_loaded", message: "the daemon did not load it in time", hint: "cs daemon status")
        #expect(r.headline == "The profile \(x) was created, but signing it in failed.")
        #expect(r.failure?.hint == "Retry once the daemon runs, or Close and sign it in later from its card in "
                + "Profiles (Sign in…).")
        #expect(r.remaining == [.seed])
        r.succeeded(.seed)
        #expect(r.finished)
    }

    @Test func testTheCLIsOwnHintIsKept() {
        var r = NewProfileRun(plan: plan)
        r.succeeded(.create)
        r.failed(.seed, code: "live", message: "team-1 is live in work", hint: "Sign work out of team-1 first.")
        #expect(r.failure?.hint == "Sign work out of team-1 first.")
    }
}
