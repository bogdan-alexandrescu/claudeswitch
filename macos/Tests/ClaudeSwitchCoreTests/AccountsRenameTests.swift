import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The Rename sheets' words (visual QA): what renaming a profile's own pool
/// does, with the names in quotes.
@Suite struct AccountsRenameTests {
    @Test func testRenamingAProfilesOwnPoolSaysWhatItBecomes() {
        #expect(PoolRename.note(name: "default", own: true, to: "laptop")
                == "Renamed to “laptop”, it becomes a separate pool with the same accounts, which “default” keeps using.")
        #expect(PoolRename.note(name: "default", own: true, to: "")
                == "Renamed, it becomes a separate pool with the same accounts, which “default” keeps using.",
                "before a name is typed")
        #expect(PoolRename.note(name: "team", own: false, to: "crew")
                == "Every profile that uses it keeps using it, in the same place in its order.")
    }
}
