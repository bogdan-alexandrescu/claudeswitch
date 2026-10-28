import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The setup window (QA 2026-10-28): mid-way, steps 1 and 2 both said "3
/// accounts saved", though saved accounts do not say the login in use is
/// one; step 1 asserted a sign-in on a first run; it pointed to the ⋯ menu,
/// gone since 2026-10-26; only a missing binary was flagged; "Again" was
/// vague.
@Suite struct WelcomeTextTests {
    @Test func testStepOneSaysOnlyWhatIsKnownOfTheLoginInUse() {
        var f = SetupFlow(facts: SetupFacts(accounts: 3))
        #expect(f.status(.saveLogin) == .done)
        #expect(f.note(.saveLogin) == nil, "3 saved accounts do not say the login in use is one of them")
        #expect(f.note(.addAccounts) == "3 accounts saved", "the count is step 2's")
        f.facts.currentLogin = "work-1"
        #expect(f.note(.saveLogin) == "Saved as " + ProfilesText.q("work-1"))
        f.skip(.addAccounts)
        #expect(f.note(.addAccounts) == "Skipped")
        #expect(SetupFlow(facts: SetupFacts(accounts: 1)).note(.addAccounts) == "1 account saved")
        #expect(SetupFlow().note(.addAccounts) == nil)
        #expect(SetupFlow().note(.saveLogin) == nil)
    }

    @Test func testTheOtherStepsNotes() {
        var f = SetupFlow(facts: SetupFacts(accounts: 1, profiles: 2, daemonInstalled: true))
        #expect(f.note(.workProfile) == "2 profiles besides “default”")
        f.facts.profiles = 1
        #expect(f.note(.workProfile) == "1 profile besides “default”")
        #expect(f.note(.daemon, daemonMode: "live") == "Installed, live")
        #expect(f.note(.daemon, daemonMode: "dry-run") == "Installed, dry run")
        #expect(f.note(.daemon) == "Installed")
        #expect(SetupFlow().note(.daemon) == nil)
    }

    /// The login in use is a saved account when default's live account,
    /// signed in, is one the config has.
    @Test func testTheLoginInUseIsDefaultsLiveAccountWhenSaved() throws {
        let list = try #require(ProfileList(data: try fixture("cli-profile-list.json")))
        #expect(SetupFacts.currentLogin(profiles: list, accounts: ["work-1", "work-2"]) == "work-1")
        #expect(SetupFacts.currentLogin(profiles: list, accounts: ["personal"]) == nil, "not a saved account")
        #expect(SetupFacts.currentLogin(profiles: nil, accounts: ["work-1"]) == nil, "not read yet")
        let out = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "default", "declared": false, "live": "work-1", "signed_in": "no"}]}
            """#.utf8)))
        #expect(SetupFacts.currentLogin(profiles: out, accounts: ["work-1"]) == nil, "its login is gone")
    }

    /// "Session" is the 5-hour window everywhere else in the app (second
    /// pass, 2026-10-28).
    @Test func testStepOneDoesNotAssertASignIn() {
        #expect(SetupStep.saveLogin.detail == "If Claude Code is signed in already, save that login under a name "
                + "you choose. The login Claude Code is using now is not touched.")
        #expect(!SetupStep.saveLogin.detail.contains("vault"))
        for s in SetupStep.allCases { #expect(!s.detail.contains("session"), "\(s.title)") }
    }

    @Test func testADoneStepReopensByWhatItDoes() {
        #expect(SetupStep.addAccounts.reopen(.done) == "Add another")
        #expect(SetupStep.workProfile.reopen(.done) == "Add another")
        #expect(SetupStep.saveLogin.reopen(.done) == "Open")
        #expect(SetupStep.daemon.reopen(.done) == "Open")
        for s in SetupStep.allCases { #expect(s.reopen(.skipped) == "Do it now") }
    }

    /// Second pass: the hint's "this again." wrapped onto a line of its own
    /// beside the window's button. Every step shows its Skip.
    @Test func testItReopensFromTheIconsMenu() {
        #expect(FirstRun.reopenHint == "Right-click the menu-bar icon → Set up… opens this again.")
        #expect(FirstRun.dontShowHelp
                == "Stop opening this window by itself at launch. Right-click the menu-bar icon → Set up… still opens it.")
    }

    @Test func testABinaryTooOldIsFlaggedLikeAMissingOne() {
        #expect(FirstRun.banner(nil) == nil)
        #expect(FirstRun.banner(.failed("x")) == nil, "a failed read is not the binary's")
        let missing = FirstRun.banner(.missing)
        #expect(missing?.title == "claudeswitch is not installed")
        #expect(missing?.message == CLIError.missing.message)
        #expect(missing?.hint == CLI.installSteps)
        let old = FirstRun.banner(.tooOld(version: "0.5.0"))
        #expect(old?.title == "claudeswitch is too old")
        #expect(old?.message == CLIError.tooOld(version: "0.5.0").message)
        #expect(old?.hint == CLI.installSteps)
        let newer = FirstRun.banner(.unsupportedContract(version: "0.9.0", contract: 3))
        #expect(newer?.title == "claudeswitch is newer than this app")
        #expect(newer?.message == CLIError.unsupportedContract(version: "0.9.0", contract: 3).message)
        #expect(newer?.hint == "", "the message says to update the app")
    }
}
