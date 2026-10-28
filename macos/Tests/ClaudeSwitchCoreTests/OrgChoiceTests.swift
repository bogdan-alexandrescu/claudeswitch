import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Signing in to a chosen organization (GROUND_TRUTH §48): the Add account
/// sheet asks for one with `--org`, from the organizations the accounts
/// already in it are in, or one pasted by id.
@Suite final class OrgChoiceTests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    @Test func testTheOrganizationIsPassedAsOrg() throws {
        try FakeAppCLI.reply(dir, text: #"""
            {"url": "https://example.com/a?orgUUID=org-2", "pending": {"account": "w3", "org": "org-2", "new_account": true}}
            """#, exit: 0)
        let s = try cli.loginStart(account: "w3", profile: nil, browser: nil, org: "org-2").get()
        #expect(lastCall() == ["login", "w3", "--direct", "--json", "--no-open", "--org=org-2"])
        #expect(s.org == "org-2")
        _ = cli.loginStart(account: "w3", profile: nil, browser: nil, org: nil)
        #expect(lastCall() == ["login", "w3", "--direct", "--json", "--no-open"])
    }

    @Test func testTheKnownOrganizationsAreTheAccountsOwn() throws {
        let list = try #require(AccountList(data: Data(#"""
            {"accounts": [
              {"id": "team-b", "seat": "p2@org-1"},
              {"id": "team-a", "seat": "p1@org-1"},
              {"id": "own", "seat": "p1@org-2"},
              {"id": "unread"}
            ]}
            """#.utf8)))
        #expect(AddForm.organizations(list) == [
            OrgChoice(id: "org-2", label: "Same organization as “own”"),
            OrgChoice(id: "org-1", label: "Same organization as “team-a”, “team-b”"),
        ], "by their first account's name")
        #expect(AddForm.organizations(nil).isEmpty)
    }

    @Test func testWhatIsAskedFor() {
        #expect(AddForm.org(.browser, typed: "org-9") == nil)
        #expect(AddForm.org(.known("org-1"), typed: "org-9") == "org-1")
        #expect(AddForm.org(.other, typed: "  0a1b-2c3d \n") == "0a1b-2c3d")
        #expect(AddForm.org(.other, typed: "") == nil)
        #expect(AddForm.org(.other, typed: "org 2&x=y") == nil, "nothing that could carry another parameter")
        #expect(AddForm.orgProblem(.other, typed: "org 2&x=y") != nil)
        #expect(AddForm.orgProblem(.other, typed: "") != nil)
        #expect(AddForm.orgProblem(.browser, typed: "") == nil)
    }

    /// A refusal after the code was exchanged spends the attempt, so the
    /// sheet opens again for another try: another organization, or another
    /// browser.
    @Test func testARefusedSignInCanBeTriedAgain() {
        #expect(AddForm.startsOver(after: "already_vaulted"))
        #expect(AddForm.startsOver(after: "wrong_account"))
        #expect(AddForm.startsOver(after: "no_pending_login"))
        #expect(!AddForm.startsOver(after: "failed"), "the code may only have been mistyped")
    }

    /// The CLI's hint names --org; the sheet names its own field.
    @Test func testADuplicateSaysToChooseTheOrganization() {
        #expect(AddForm.appHint(code: "already_vaulted", asked: nil)?.contains("under Organization") == true)
        #expect(AddForm.appHint(code: "already_vaulted", asked: "org-2") == nil, "it was asked for: the CLI's own")
        #expect(AddForm.appHint(code: "wrong_account", asked: nil) == nil)
    }
}
