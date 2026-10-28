import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Second QA pass (2026-10-28): names in a sentence are quoted, in the
/// confirmations and the notes after them too, and no sentence starts with
/// one; Forget… says it worked.
@Suite struct Profiles2QuotesTests {
    @Test func testMovingAnAccountBetweenPoolsQuotesItsNames() throws {
        let pools = try sharedPools()
        let move = Confirm.poolDrop(.move(account: "team-2", from: "team", to: "ops"), pools: pools)
        #expect(move.title == "Move “team-2” to “ops”?")
        #expect(move.message == "The account “team-2” leaves the pool “team” and joins “ops”. "
                + "From now on “work” may use it; “default” no longer can.")
        let toUnused = Confirm.poolDrop(.move(account: "ops-1", from: "ops", to: "spare"), pools: pools)
        #expect(toUnused.message == "The account “ops-1” leaves the pool “ops” and joins “spare”. "
                + "No profile uses “spare”, so “ops-1” rotates nowhere until one does.")
        let intoTeam = Confirm.poolDrop(.move(account: "loose-1", from: nil, to: "team"), pools: pools)
        #expect(intoTeam.message == "The account “loose-1” joins the pool “team”. From now on “default” and “work” may use it.")
        let out = Confirm.poolDrop(.remove(account: "ops-1", from: "ops"), pools: pools)
        #expect(out.title == "Take “ops-1” out of “ops”?")
        #expect(out.message == "The account “ops-1” is then in no pool, where “default” uses it after its pools; "
                + "“work” no longer can.")
        let none = Confirm.poolDrop(.remove(account: "ops-1", from: "ops"), pools: pools, defaultExists: false)
        #expect(none.message == "The account “ops-1” is then in no pool, where no profile uses it; “work” no longer can.")
        let mine = Confirm.poolDrop(.remove(account: "personal", from: "personal"), pools: pools)
        #expect(mine.message == "The account “personal” is then in no pool, where “default” uses it after its pools.")
        #expect(Notes.poolDrop(.move(account: "team-2", from: "team", to: "ops")) == "The account “team-2” is now in “ops”")
        #expect(Notes.poolDrop(.remove(account: "ops-1", from: "ops")) == "The account “ops-1” is in no pool now")
        for c in [move, toUnused, intoTeam, out, none, mine] {
            for line in sentences(c.message) { #expect(!line.hasPrefix("“"), "no sentence starts with a name: \(line)") }
        }
    }

    @Test func testForgettingAnOldLoginQuotesItsNamesAndSaysItWorked() {
        let c = Confirm.forgetGhost(profile: "old", account: "work-2")
        #expect(c.title == "Forget “old”?")
        #expect(c.message == "The last login of the old profile “old” (“work-2”) is no longer guarded: "
                + "claudeswitch stops treating “work-2” as possibly live there.")
        #expect(ProfilesText.forgot(profile: "review", account: "old-sso")
                == "Forgot the old login of “old\u{2011}sso” in “review”")
    }

    @Test func testRecoveryCopiesQuoteTheirNames() {
        let clear = Confirm.recoveryClear(slot: "r1", account: "personal")
        #expect(clear.title == "Clear the recovery copy “r1”?")
        #expect(clear.message == "The kept credential in “r1” (“personal”) is deleted. It cannot be restored afterwards.")
        #expect(Confirm.recoveryClear(slot: "r2", account: nil).message
                == "The kept credential in “r2” is deleted. It cannot be restored afterwards.")
        let same = Confirm.recoveryRestore(slot: "r1", to: "personal", itsAccount: "personal")
        #expect(same.title == "Restore “r1” to “personal”?")
        #expect(same.message == "The credential kept in “r1” replaces the stored credential of “personal”.")
        let other = Confirm.recoveryRestore(slot: "r1", to: "work-1", itsAccount: "personal")
        #expect(other.message == "The credential kept in “r1” replaces the stored credential of “work-1”. "
                + "The copy belongs to “personal”: restoring it to another account stores the login of “personal” "
                + "under “work-1”.")
        for c in [clear, same, other] { #expect(!c.message.contains("'"), "no straight apostrophe") }
    }

    @Test func testAProfilesPoolsAreQuoted() {
        #expect(ProfilesText.thenUnlisted(["loose-1"], profile: "default")
                == "Then the account in no pool (“loose\u{2011}1”), as always for “default”.")
        #expect(ProfilesText.thenUnlisted(["a", "b"], profile: "default")
                == "Then the 2 accounts in no pool (“a”, “b”), as always for “default”.")
        #expect(ProfilesText.poolsSaved("work", ["ops", "team"]) == "The profile “work” now uses “ops”, then “team”")
        #expect(ProfilesText.poolsSaved("work", ["ops"]) == "The profile “work” now uses “ops”")
        var r = NewProfileRun(plan: NewProfilePlan.make(name: "lab", pools: ["ops", "team"], seed: nil, candidates: []))
        r.succeeded(.create)
        r.succeeded(.pools)
        #expect(r.note == "Created the profile “lab”; it uses “ops”, then “team”")
    }
}
