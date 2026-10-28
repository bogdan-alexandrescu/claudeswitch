import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The Add account sheet, second QA pass: one profile and no pools has no
/// Pool row and says where the account goes in plain words; signing in
/// again names the pool once; "session" is the 5-hour window, never the
/// login; the name is asked for only while it holds the button back; and
/// the spinner says what it waits for.
@Suite struct Accounts2AddTests {
    func profiles(_ json: String) throws -> ProfileList {
        try #require(ProfileList(data: Data(json.utf8)))
    }

    /// One profile and no pools: the only choice is no pool, which reads as
    /// the profile it is added to.
    @Test func testOneProfileAndNoPoolsHasNoPoolToChoose() throws {
        let one = try profiles(#"{"profiles": [{"name": "default", "declared": false, "pools": []}]}"#)
        let none = PoolList(pools: [], unlisted: ["a"])
        #expect(AddForm.onlyNoPool(pools: none, profiles: one))
        #expect(!AddForm.onlyNoPool(pools: try sharedPools(), profiles: try sharedProfiles()), "pools to choose from")
        #expect(!AddForm.onlyNoPool(pools: nil, profiles: one), "an older CLI: the Profile row")

        let place = AddForm.place(existing: false, pool: PoolPick.none, target: "default", profiles: one, pools: none)
        #expect(place == .added("“default”"))
        #expect(AddForm.signInNote(place)
                == "The login Claude Code is using now is not touched. The account is checked, stored and added to “default”.")
        #expect(AddForm.saveNote(source: "default", place)
                == "Saves the login Claude Code is using in “default” under this name, and adds it to “default”.")
    }

    @Test func testTheNotesSayWhereANewAccountGoes() throws {
        let (pools, list) = (try sharedPools(), try sharedProfiles())
        let team = AddForm.place(existing: false, pool: .pool("team"), target: "default", profiles: list, pools: pools)
        #expect(team == .pool("in the pool “team”, which the profiles “default” and “work” use"))
        #expect(AddForm.signInNote(team) == "The login Claude Code is using now is not touched. "
                + "The account is checked and stored in the pool “team”, which the profiles “default” and “work” use.")
        #expect(AddForm.saveNote(source: "work", team)
                == "Saves the login Claude Code is using in “work” under this name, in the pool “team”, which the profiles “default” and “work” use.")

        // An older CLI: no pools to choose, the profile it is added to.
        let old = AddForm.place(existing: false, pool: nil, target: "work", profiles: nil, pools: nil)
        #expect(old == .added("the profile “work”"))
        #expect(AddForm.signInNote(old)
                == "The login Claude Code is using now is not touched. The account is checked, stored and added to the profile “work”.")
        #expect(AddForm.saveNote(source: "default", old)
                == "Saves the login Claude Code is using in “default” under this name, and adds it to the profile “work”.")
    }

    /// Signing in again: Used by and its note already name the pool, so the
    /// note under the steps does not say it a third time.
    @Test func testSigningInAgainNamesThePoolOnce() throws {
        let place = AddForm.place(existing: true, pool: .pool("team"), target: "default",
                                  profiles: try sharedProfiles(), pools: try sharedPools())
        #expect(place == .existing)
        #expect(AddForm.signInNote(place) == "The login Claude Code is using now is not touched. The account is checked and stored.")
        #expect(AddForm.saveNote(source: "default", place) == "Saves the login Claude Code is using in “default” under this name.")
    }

    /// "Signed in in" read as a typo.
    @Test func testSavingAnotherProfilesLoginReadsCleanly() {
        #expect(AddForm.crossProfileNote(source: "default", target: "work", sourceUsesIt: false)
                == "it is still the login in “default”, which will move off it — “work” can use it after")
        #expect(AddForm.crossProfileNote(source: "default", target: "work", sourceUsesIt: true)
                == "it is still the login in “default”, which uses it too — “work” can use it once “default” rotates off it")
    }

    @Test func testAnInvalidOrganizationIdIsSaidWithACurlyApostrophe() {
        #expect(AddForm.orgProblem(.other, typed: "not an id") == "Paste the organization’s id: letters, digits and dashes.")
    }

    /// Save takes the suggested name when the field is empty, so the line
    /// asking for one would contradict an enabled button.
    @Test func testTheNameIsAskedForOnlyWhileItHoldsTheButtonBack() {
        #expect(AddForm.needsName("", suggested: nil, saving: false))
        #expect(AddForm.needsName("", suggested: "person4", saving: false), "a sign-in takes no suggestion")
        #expect(AddForm.needsName("", suggested: nil, saving: true))
        #expect(!AddForm.needsName("", suggested: "person4", saving: true), "Save uses the suggestion")
        #expect(!AddForm.needsName("work-3", suggested: nil, saving: false))
    }

    @Test func testTheSpinnerSaysWhatItWaitsFor() {
        #expect(AddForm.busyText(login: true, add: false, started: false, saving: false) == "Opening the sign-in page…")
        #expect(AddForm.busyText(login: true, add: false, started: true, saving: false) == "Checking the account…")
        #expect(AddForm.busyText(login: false, add: true, started: false, saving: true) == "Saving the login…")
        #expect(AddForm.busyText(login: false, add: true, started: false, saving: false) == "Reading the login…",
                "the name suggested for the source's login")
        #expect(AddForm.busyText(login: false, add: false, started: true, saving: false) == nil)
    }
}
