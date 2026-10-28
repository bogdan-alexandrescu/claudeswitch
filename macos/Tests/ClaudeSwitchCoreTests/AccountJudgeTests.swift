import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Settings → Accounts and Overview judge an account by the profile it is
/// live in (else the first profile that uses it), with that profile's
/// thresholds, not by the default profile's: review switches at 75, so its
/// live account at 80% of the session has no headroom though default's 90
/// would call it available.
@Suite struct AccountJudgeTests {
    @Test func testAnAccountIsJudgedByTheProfileItIsLiveIn() throws {
        var state = try #require(JSONSerialization.jsonObject(with: try fixture("state.json")) as? [String: Any])
        var accounts = try #require(state["accounts"] as? [String: Any])
        var personal = try #require(accounts["personal"] as? [String: Any])
        var usage = try #require(personal["last_usage"] as? [String: Any])
        var five = try #require(usage["five_hour"] as? [String: Any])
        five["utilization"] = 80
        usage["five_hour"] = five
        personal["last_usage"] = usage
        personal["last_error"] = ""
        accounts["personal"] = personal
        state["accounts"] = accounts
        let s = Snapshot.build(state: StateFile(data: try JSONSerialization.data(withJSONObject: state)),
                               why: WhyReport(data: try fixture("why-profiles.json")),
                               settings: ConfigSettings(data: try fixture("config.json")),
                               profiles: ProfileList(data: try fixture("cli-profile-list.json")), now: fixtureNow)
        #expect(s.card("review")?.switchAt == 75)
        #expect(s.accounts.first { $0.id == "personal" }?.status == .noHeadroom,
                "review, where personal is live, switches at 75")
    }
}
