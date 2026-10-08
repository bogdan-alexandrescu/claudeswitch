import Foundation
import Testing
@testable import ClaudeSwitchCore

func cardsSnapshot(profiles: Bool = true) throws -> Snapshot {
    Snapshot.build(state: StateFile(data: try fixture("state.json")),
                   why: WhyReport(data: try fixture("why-profiles.json")),
                   settings: ConfigSettings(data: try fixture("config.json")),
                   profiles: profiles ? ProfileList(data: try fixture("cli-profile-list.json")) : nil,
                   now: fixtureNow)
}

/// The popover's profile cards (IMPROVEMENTS M7, mockup 1).
@Suite struct ProfileCardTests {
    @Test func testACardPerProfileInTheCLIsOrder() throws {
        let s = try cardsSnapshot()
        #expect(s.cards.map(\.name) == ["default", "review"])
        let d = try #require(s.card("default"))
        #expect(d.dir == nil)
        #expect(d.dirLabel == "~/.claude", "Claude Code's default directory, shown as such")
        #expect(d.active?.id == "work-1")
        #expect(d.pool == ["work-1", "work-2"])
        #expect(d.accounts.map(\.id) == ["work-1", "work-2"], "the account picker offers the profile's pool")
        #expect(d.decision?.kind == "stay")
        #expect(d.switchAt == 90 && d.switchAtWeekly == 95)
        #expect(!d.isPinned)
        #expect(d.signedIn == "yes")
    }

    @Test func testAProfilesOwnThresholdsAndPin() throws {
        let s = try cardsSnapshot()
        let r = try #require(s.card("review"))
        #expect(r.dirLabel == "~/.claude-review")
        #expect(r.switchAt == 75, "the profile's override, from profile list")
        #expect(r.isPinned && r.pinned == "personal")
        #expect(r.active?.id == "personal")
        #expect(r.active?.isPinned == true)
        #expect(r.decision?.reason == "pinned to personal; automatic rotation is off")
    }

    @Test func testWithoutProfileListTheCardsComeFromWhyAndState() throws {
        let s = try cardsSnapshot(profiles: false)
        #expect(s.cards.map(\.name) == ["default", "review"])
        let d = try #require(s.card("default"))
        #expect(d.pool == ["work-1", "work-2", "personal"], "why's pool")
        #expect(d.dir == nil)
        #expect(s.card("review")?.active?.id == "personal")
    }

    @Test func testASingleProfileStillHasACard() throws {
        let s = try fixtureSnapshot()
        #expect(s.cards.map(\.name) == ["default", "review"], "state.json names a second profile")
        #expect(s.card("default")?.active?.id == "work-1")
    }

    @Test func testTheMenuBarFollowsTheChosenProfile() throws {
        let s = try cardsSnapshot()
        #expect(MenuTitle.of(s, compact: false, profile: "review").text == "review · personal 12%!", "a 401 is marked")
        #expect(MenuTitle.of(s, compact: false, profile: "default").text == "default · work-1 41%")
        #expect(MenuTitle.of(s, compact: false, profile: "gone").text == "default · work-1 41%",
                "a profile that went away falls back to default")
        #expect(MenuTitle.of(s, compact: false, profile: nil).text == "default · work-1 41%")
        #expect(MenuTitle.of(s, compact: true, profile: "review").text == "!", "icon only, still marked")
        #expect(MenuTitle.of(s, compact: true, profile: "default").text == "")
    }

    @Test func testOneProfileHasNoPrefix() throws {
        var obj = try fixtureObject("state.json")
        obj["profiles"] = ["default": ["active_account": "work-1"]]
        let s = Snapshot.build(state: StateFile(data: try encode(obj)), why: WhyReport(data: try fixture("why.json")),
                               settings: ConfigSettings(data: try fixture("config.json")), now: fixtureNow)
        #expect(s.cards.count == 1)
        #expect(MenuTitle.of(s, compact: false, profile: "default").text == "work-1 41%")
    }

    @Test func testTheFollowedProfileIsResolved() throws {
        let s = try cardsSnapshot()
        #expect(s.followed("review") == "review")
        #expect(s.followed("gone") == "default")
        #expect(s.followed(nil) == "default")
    }

    @Test func testCompactLines() throws {
        let s = try cardsSnapshot()
        let d = try #require(s.card("default"))
        #expect(Format.cardSummary(d) == "work-1 · session 3% · week 41%")
        let empty = ProfileCard(name: "x", dir: nil, signedIn: nil, active: nil, pinned: nil, decision: nil,
                                switchAt: 90, switchAtWeekly: 95, pool: [], accounts: [])
        #expect(Format.cardSummary(empty) == "no account live")
    }
}

@Suite struct JSONCLIVersionTests {
    @Test func testTheAppNeedsTheJSONCLI() {
        #expect(CLI.minimumVersion == "0.5.1")
        #expect(!CLI.supports(version: "0.5.0"), "0.5.0 shipped before the JSON CLI")
        #expect(!CLI.supports(version: "0.4.8"))
        #expect(CLI.supports(version: "0.5.1"))
        #expect(CLI.supports(version: "0.6.0"))
        #expect(CLI.supports(version: "dev"))
        #expect(CLIError.tooOld(version: "0.5.0").message.contains("Update claudeswitch"))
    }
}
