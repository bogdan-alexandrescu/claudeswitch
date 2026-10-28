import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The Add account sheet's own words (visual QA): names in quotes, a
/// duplicate said in the app's terms, and where a new account goes said
/// from its pool.
@Suite struct AccountsAddTextTests {
    /// already_vaulted as `login --code` words it, and as `add` does: a
    /// terminal's text, and "quota pool" is not the app's pool.
    @Test func testADuplicateIsSaidInTheAppsWords() {
        let login = """
            this is the same account AND organization already vaulted as "work-1" — person1@example.com in Example.
              Nothing was stored. A quota pool is one person in one organization, so to
              add a different pool you need a different person, a different organization,
              or both.
            """
        #expect(AddForm.duplicate(login)
                == "This person in this organization is already saved as “work-1” (person1@example.com, Example). Nothing was stored.")
        let add = """
            the credential live right now is already vaulted as "work-1" (person1@example.com in ).
              Nothing was stored. `add` saves whatever Claude Code is signed in to.
              To add a different account, sign in to it:
                  cs login work-2
            """
        #expect(AddForm.duplicate(add)
                == "This person in this organization is already saved as “work-1” (person1@example.com). Nothing was stored.")
        #expect(AddForm.duplicate("some newer wording\n  wrapped over\n  two lines")
                == "Some newer wording wrapped over two lines.", "else the lines joined, the indents gone, a capital")
    }

    @Test func testADuplicatesHintFitsTheRoute() {
        #expect(AddForm.duplicateHint(saving: true, asked: nil)
                == "Save current login saves the account Claude Code is signed in to. To add another, choose Sign in with browser.")
        #expect(AddForm.duplicateHint(saving: false, asked: nil) == AddForm.appHint(code: "already_vaulted", asked: nil))
        #expect(AddForm.duplicateHint(saving: false, asked: "org-2") == nil, "it was asked for: the CLI's own")
    }

    @Test func testWhereANewAccountGoes() throws {
        let pools = try sharedPools()
        #expect(NewAccountPool.describe(.pool("team"), pools: pools) == "in the pool “team”, which the profiles “default” and “work” use")
        #expect(NewAccountPool.describe(.pool("ops"), pools: pools) == "in the pool “ops”, which the profile “work” uses")
        #expect(NewAccountPool.describe(.pool("spare"), pools: pools) == "in the pool “spare”, which no profile uses yet")
        #expect(NewAccountPool.describe(PoolPick.none, pools: pools) == "in no pool, where “default” uses it after its pools")
        let one = PoolList(pools: [], unlisted: ["a"])
        #expect(NewAccountPool.describe(PoolPick.none, pools: one) == "in no pool, where “default” uses it",
                "one profile and no pools: nothing to be after")
    }

    @Test func testAnOrganizationIsNamedByAFewOfItsAccounts() throws {
        let list = try #require(AccountList(data: Data(#"""
            {"accounts": [
              {"id": "work-team", "seat": "p4@org-1"},
              {"id": "work-2", "seat": "p3@org-1"},
              {"id": "work-1", "seat": "p2@org-1"},
              {"id": "personal", "seat": "p1@org-1"},
              {"id": "own", "seat": "p1@org-2"}
            ]}
            """#.utf8)))
        #expect(AddForm.organizations(list).map(\.label) == [
            "Same organization as “own”",
            "Same organization as “personal”, “work-1” +2",
        ])
    }
}
