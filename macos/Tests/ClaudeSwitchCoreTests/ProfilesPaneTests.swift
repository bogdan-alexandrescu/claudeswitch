import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Settings → Profiles' cards (QA 2026-10-28): a profile that needs sign-in
/// says so once, closed or open, and names its account; the only profile is
/// not "No profiles yet"; a directory reads with ~; the menu-bar checkbox
/// says why it cannot be unchecked; an override keeps its unit and states
/// the global value once.
@Suite struct ProfilesPaneTests {
    @Test func testADirectoryReadsWithTheHomeFolderAsTilde() {
        let home = "/Users/example"
        #expect(ProfilesText.dir(nil, home: home) == "~/.claude", "Claude Code's own")
        #expect(ProfilesText.dir("/Users/example/Projects/.claude-enterprise", home: home) == "~/Projects/.claude-enterprise")
        #expect(ProfilesText.dir("~/.claude-review", home: home) == "~/.claude-review")
        #expect(ProfilesText.dir("/Users/examples/.claude", home: home) == "/Users/examples/.claude", "only the home folder")
        #expect(ProfilesText.dir("/opt/claude", home: home) == "/opt/claude")
    }

    @Test func testTheSignInStateNamesItsAccount() {
        #expect(ProfilesText.signIn("yes", live: "work-1") == "signed in · work-1")
        #expect(ProfilesText.signIn("no", live: "old-sso") == "needs sign-in · old-sso")
        #expect(ProfilesText.signIn("no", live: nil) == "needs sign-in")
        #expect(ProfilesText.signIn("unknown", live: "x") == "sign-in unknown")
    }

    @Test func testSigningInSaysWhoAndWhere() {
        #expect(ProfilesText.signInAction == "Sign in…")
        #expect(ProfilesText.signInWith("work-1", elsewhere: "work") == "work-1 — in use by “work”")
        #expect(ProfilesText.signInWith("work-1", elsewhere: nil) == "work-1")
        #expect(ProfilesText.noAccountToSignIn("review") == "Add an account to “review” first")
        #expect(ProfilesText.signedIn(profile: "review", account: "personal")
                == "The profile “review” is signed in with “personal”")
        #expect(ProfilesText.signInFailed("review") == "Could not sign “review” in")
        #expect(ProfilesText.openHelp("review", signedIn: "no") == "The profile “review” needs sign-in: Sign in… first")
        #expect(ProfilesText.openHelp("review", signedIn: "yes") == "Open Claude Code in “review”")
    }

    @Test func testTheOnlyProfileIsNotCalledNoProfiles() throws {
        let one = try #require(ProfileList(data: Data(#"""
            {"profiles": [{"name": "default", "declared": false, "pool": ["a"]}]}
            """#.utf8)))
        #expect(ProfileText.badge(one.profiles[0]) == "the only profile")
        #expect(ProfilesText.implicitNote
                == "Every account belongs to this one setup. To split them, create a profile with New profile above.")
    }

    @Test func testTheMenuBarCheckboxSaysWhatItFollows() {
        #expect(ProfilesText.menuBarHelp(followed: true, name: "default")
                == "The menu bar follows this profile. To follow another, check Shown in menu bar on its card.")
        #expect(ProfilesText.menuBarHelp(followed: true, name: "work")
                == "The menu bar follows this profile. Uncheck to follow “default” again.")
        #expect(ProfilesText.menuBarHelp(followed: false, name: "work") == "Make the menu bar follow “work”")
    }

    @Test func testAnOverrideKeepsItsUnitAndSaysTheGlobalOnce() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let pct = try #require(schema.setting("switch_at"))
        #expect(ProfilesText.unit(pct) == "%", "75 reads as 75%, as the global 90% does")
        #expect(ProfilesText.placeholder(pct, global: "90") == "90", "the unit is beside the field, not in it")
        #expect(ProfilesText.placeholder(pct, global: nil) == "", "not read yet")
        #expect(ProfilesText.overrideCaption(pct, overridden: false, global: "90") == "inherits the global value",
                "the placeholder shows it")
        #expect(ProfilesText.overrideCaption(pct, overridden: true, global: "90") == "overrides the global 90%")
        #expect(ProfilesText.overrideCaption(pct, overridden: true, global: nil) == "overrides the global value")
        #expect(ProfilesText.inheritHelp(pct, global: "90") == "Inherit the global value (90%)")
        #expect(ProfilesText.inheritHelp(pct, global: nil) == "Inherit the global value")
        let models = try #require(schema.setting("models"))
        #expect(ProfilesText.unit(models) == nil)
        #expect(ProfilesText.placeholder(models, global: "") == "none")
    }
}
