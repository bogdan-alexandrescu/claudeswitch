import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The sentences of a text, split after each full stop.
func sentences(_ s: String) -> [String] {
    s.components(separatedBy: ". ").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
}

/// Settings → Profiles in words (QA 2026-10-28): a name ran into the words
/// around it ("Remove profile review", "Pools default uses"), so names are
/// quoted, kept whole, and never start a sentence.
@Suite struct ProfilesTextTests {
    let legacy = PoolList(data: (try? fixture("cli-pool-list.json")) ?? Data())!
    var review: ProfileInfo { ProfileList(data: (try? fixture("cli-profile-list.json")) ?? Data())!.profile("review")! }

    @Test func testANameIsQuotedAndKeptWhole() {
        #expect(ProfilesText.q("review") == "“review”")
        #expect(ProfilesText.q("client-x") == "“client\u{2011}x”", "its hyphens do not break the line")
    }

    // Remove profile: the sheet says what removing does first, points to
    // where the guard is lifted, and is itself the confirmation.

    @Test func testRemovingSaysWhatItDoesFirst() {
        #expect(ProfilesText.removeTitle("review") == "Remove the profile “review”?")
        #expect(ProfilesText.removeButton("review") == "Remove “review”")
        let s = ProfileRemoval.summary(review, to: "default", pools: legacy)
        #expect(s == "The profile “review” is removed from claudeswitch: it gets no card and is not rotated. "
                + "Its accounts (“personal”) move to “default”. "
                + "Its folder ~/.claude-review and its saved login stay on disk; nothing is deleted. "
                + "The account “personal” may still be signed in there, so it stays guarded: no other profile "
                + "uses it until that login is gone, or until you release it with Forget… in Profiles.")
        #expect(ProfileRemoval.summary(review, to: nil, pools: legacy).contains("move to the profile you choose."),
                "no destination chosen yet: none quoted")
        for line in sentences(s) {
            #expect(!line.hasPrefix("review") && !line.hasPrefix("personal") && !line.hasPrefix("“"),
                    "no sentence starts with a name: \(line)")
        }
    }

    @Test func testTheNoteOnceRemovedQuotesItsNames() throws {
        let j = try #require(JSON(data: try fixture("cli-profile-remove.json")))
        let r = try #require(ProfileRemoved(j))
        #expect(ProfilesText.removed(r)
                == "Removed the profile “review”; its accounts are in “default”; “personal” stays guarded until that login is gone")
    }

    // Change…: whose pools, and sharing in plain words.

    @Test func testChangePoolsSaysWhoseAndHowTheyAreShared() {
        #expect(ProfilesText.changeTitle("default") == "Choose the pools “default” uses")
        #expect(ProfilesText.changeHelp("default") == "Choose the pools “default” uses, and their order")
        #expect(ProfilesText.sharing == "Profiles that use the same pool share its accounts; each account is live "
                + "in one profile at a time. No account moves.")
        #expect(ProfilesText.changeIntro("default") == "The profile “default” uses the checked pools in this order: "
                + "an account in a later pool only when nothing in an earlier one is usable. " + ProfilesText.sharing)
        #expect(ProfilesText.thenUnlisted(["loose-1"], profile: "default")
                == "Then the account in no pool (“loose\u{2011}1”), as always for “default”.")
        #expect(ProfilesText.thenUnlisted(["a", "b"], profile: "default")
                == "Then the 2 accounts in no pool (“a”, “b”), as always for “default”.")
        #expect(ProfilesText.poolsSaved("work", ["ops", "team"]) == "The profile “work” now uses “ops”, then “team”")
    }

    @Test func testEachPoolSaysWhoseItIsAndWhoUsesIt() throws {
        let shared = try sharedPools()
        #expect(ProfilesText.poolCaption("team", pools: shared, owner: nil) == "used by “default”, “work” · team-1, team-2, team-3")
        #expect(ProfilesText.poolCaption("spare", pools: shared, owner: nil) == "used by no profile · no accounts")
        #expect(ProfilesText.poolCaption("review", pools: legacy, owner: "review") == "its own pool · personal")
        #expect(ProfilesText.poolCaption("default", pools: legacy, owner: "review") == "a profile’s own pool · work-1, work-2")
        #expect(ProfilesText.poolCaption("gone", pools: legacy, owner: nil) == "")
    }

    // New profile: the order numbers are explained once something is
    // checked; the example name is not one the example data uses.

    @Test func testNewProfileExplainsTheNumbersAndSharing() {
        #expect(ProfilesText.newProfilePools(numbered: false, ownFirst: false) == ProfilesText.sharing)
        #expect(ProfilesText.newProfilePools(numbered: true, ownFirst: false)
                == "Used in the order numbered. " + ProfilesText.sharing)
        #expect(ProfilesText.newProfilePools(numbered: true, ownFirst: true)
                == "Used in the order numbered; its own accounts come first. " + ProfilesText.sharing)
        #expect(ProfilesText.newProfilePools(numbered: false, ownFirst: true)
                == "Its own accounts come first. " + ProfilesText.sharing)
        #expect(ProfilesText.newProfileOwn
                == "Accounts in no pool, which “default” uses now: the ones checked become this profile’s own.")
        #expect(ProfilesText.inUseBy("work") == "in use by “work”")
        #expect(ProfilesText.namePrompt == "e.g. client-x")
    }

    /// The numbers show once a pool is checked; ↑ and ↓ only with two to order.
    @Test func testTheOrderShowsOnlyWhenThereIsOne() {
        var c = PoolChoice(all: ["a", "b", "c"], chosen: [])
        #expect(!c.numbered && !c.reorderable)
        c.toggle("b")
        #expect(c.numbered && !c.reorderable, "one checked: numbered, nothing to move")
        c.toggle("a")
        #expect(c.numbered && c.reorderable)
    }
}
