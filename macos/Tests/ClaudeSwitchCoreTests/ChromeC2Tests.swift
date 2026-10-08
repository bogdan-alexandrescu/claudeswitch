import Foundation
import Testing
@testable import ClaudeSwitchCore

/// IMPROVEMENTS C2: your own Chrome profiles, one per profile, overridable
/// per account (docs/APP_CLI.md, chrome).
@Suite struct ChromeC2DecodingTests {
    @Test func testChromeProfilesDecode() throws {
        let p = try #require(ChromeProfiles(data: try fixture("cli-chrome-profiles.json")))
        #expect(p.localState && p.supported && p.lastUsed == "Profile 2")
        #expect(p.profiles.map(\.folder) == ["Default", "Profile 1", "Profile 2", "claudeswitch-personal"])
        #expect(p.profiles[0].name == "Person 1" && p.profiles[0].usedByProfiles == ["default"])
        #expect(p.profiles[2].lastUsed && p.profiles[2].usedByAccounts == ["research"])
        #expect(p.profiles[3].name == nil && !p.profiles[3].inChrome)
        #expect(p.profiles[3].label == "claudeswitch-personal" && p.profiles[1].label == "Work")
        #expect(p.label("Profile 1") == "Work" && p.label("Profile 9") == "Profile 9")
        // The picker lists what Chrome lists.
        #expect(p.pickable.map(\.folder) == ["Default", "Profile 1", "Profile 2"])
        #expect(p.lastUsedLabel == "Work 2")
    }

    @Test func testChromeProfilesWithoutLocalState() throws {
        let p = try #require(ChromeProfiles(data: try encode(
            ["chrome_profiles": [Any](), "last_used": NSNull(), "local_state": false, "supported": true])))
        #expect(!p.localState && p.profiles.isEmpty && p.lastUsed == nil && p.lastUsedLabel == nil)
    }

    @Test func testChromeListCarriesExistingNamesAndTheLiveResolution() throws {
        let l = try #require(ChromeList(data: try fixture("cli-chrome-list-c2.json")))
        let research = try #require(l.mapping("research"))
        #expect(research.existing && research.name == "Work 2" && research.label == "Work 2")
        #expect(l.mapping("personal")?.existing == false && l.mapping("personal")?.label == "claudeswitch-personal")
        #expect(l.live.map(\.profile) == ["default", "work"])
        let work = try #require(l.live(in: "work"))
        #expect(work.account == "work-team" && work.profileDir == "Profile 1" && work.name == "Work")
        #expect(work.rule == "profile" && work.lastFrom == "work-1" && work.lastSwitch != nil)
        let def = try #require(l.live(in: "default"))
        #expect(def.rule == "account" && def.lastFrom == nil && def.lastSwitch == nil && def.label == "claudeswitch-personal")
        // Lane 13's shape, without the C2 keys, still reads.
        let old = try #require(ChromeList(data: try fixture("cli-chrome-list-some.json")))
        #expect(old.live.isEmpty && old.mapping("work-1")?.existing == false)
    }

    @Test func testTheAccountsChoice() throws {
        let l = try #require(ChromeList(data: try fixture("cli-chrome-list-c2.json")))
        #expect(l.choice(for: "research") == .existing(folder: "Profile 2", name: "Work 2"))
        #expect(l.choice(for: "personal") == .created(folder: "claudeswitch-personal"))
        #expect(l.choice(for: "work-1") == .sameAsProfile)
    }

    @Test func testProfileListCarriesTheProfilesChrome() throws {
        let l = try #require(ProfileList(data: try fixture("cli-profile-list.json")))
        #expect(l.profile("review")?.chrome == "Profile 1" && l.profile("review")?.chromeName == "Work")
        #expect(l.profile("default")?.chrome == nil && l.profile("default")?.chromeName == nil)
    }
}

/// The popover card's sign-in notice: only when the live account's Chrome
/// profile comes from its profile and the last rotation changed the account;
/// dismissable per rotation.
@Suite struct ChromeSignInNoticeTests {
    func live(rule: String = "profile", account: String = "work-team", from: String? = "work-1",
              at: Date? = Date(timeIntervalSince1970: 1_791_000_000), name: String? = "Work") -> ChromeLive {
        ChromeLive(profile: "work", account: account, profileDir: "Profile 1", name: name, rule: rule,
                   lastFrom: from, lastSwitch: at)
    }

    @Test func testShownWhenOnlyTheProfileApplies() throws {
        let n = try #require(live().signInNotice(dismissed: []))
        #expect(n.text == "Claude in Chrome in \"Work\" is still signed in as work-1")
        #expect(n.button == "Sign in as work-team")
        #expect(n.account == "work-team" && n.profile == "work")
    }

    @Test func testNotShownOtherwise() {
        #expect(live(rule: "account").signInNotice(dismissed: []) == nil, "an account mapping routes by itself")
        #expect(live(rule: "last_used").signInNotice(dismissed: []) == nil)
        #expect(live(from: nil).signInNotice(dismissed: []) == nil, "no rotation recorded")
        #expect(live(from: "work-team").signInNotice(dismissed: []) == nil, "the rotation did not change the account")
    }

    @Test func testDismissedPerRotation() throws {
        let n = try #require(live().signInNotice(dismissed: []))
        #expect(live().signInNotice(dismissed: [n.key]) == nil)
        let next = live(account: "work-1", from: "work-team", at: Date(timeIntervalSince1970: 1_791_003_600))
        #expect(next.signInNotice(dismissed: [n.key]) != nil, "the next rotation shows it again")
    }

    @Test func testFolderNamesItWithoutAName() throws {
        let n = try #require(live(name: nil).signInNotice(dismissed: []))
        #expect(n.text == "Claude in Chrome in \"Profile 1\" is still signed in as work-1")
    }
}

@Suite final class ChromeC2CLITests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    @Test func testProfiles() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-chrome-profiles")
        let p = try cli.chromeProfiles().get()
        #expect(lastCall() == ["chrome", "profiles", "--json"])
        #expect(p.profiles.count == 4)
    }

    @Test func testAddExisting() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-chrome-add-existing")
        let a = try cli.chromeAddExisting("work-team", "Profile 2").get()
        #expect(lastCall() == ["chrome", "add", "work-team", "--existing=Profile 2", "--json"])
        #expect(a.existing && !a.created && !a.opened && a.name == "Work 2" && a.email == nil)
    }

    @Test func testSignin() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-chrome-signin")
        let s = try cli.chromeSignin("work-team").get()
        #expect(lastCall() == ["chrome", "signin", "work-team", "--json"])
        #expect(s.opened && s.profileDir == "Profile 1" && s.name == "Work" && s.rule == "profile")
        #expect(s.email == "team@example.com" && s.label == "Work")
    }

    @Test func testProfileSetChrome() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-profile-set-chrome")
        let r = try cli.profileSet("work", "chrome", "Profile 1").get()
        #expect(lastCall() == ["profile", "set", "work", "chrome", "Profile 1", "--json"])
        #expect(r.override == "Profile 1")
        try FakeAppCLI.reply(dir, fixture: "cli-profile-set-chrome-inherit")
        let i = try cli.profileSet("work", "chrome", "").get()
        #expect(lastCall() == ["profile", "set", "work", "chrome", "inherit", "--json"])
        #expect(i.override == nil)
    }

    /// "Open Chrome" opens the resolved Chrome profile; it never creates
    /// one (C2: only "Create a new one" does).
    @Test func testOpenNeverCreates() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-chrome-open")
        _ = cli.chromeOpen("work-2")
        #expect(lastCall() == ["chrome", "open", "work-2", "--json"])
    }
}
