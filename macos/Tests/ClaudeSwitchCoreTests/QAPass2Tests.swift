import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Fixes from the second QA pass, 2026-10-28.
@Suite struct QAPass2Tests {
    /// A recorded hotkey takes two of ⌘, ⌥ and ⌃ (or one with a function
    /// key): ⌘Q or ⌘C alone would be taken from every app, ⌥S types ß, ⌃C
    /// interrupts a terminal. Keys saved before keep working.
    @Test func testARecordedHotkeyNeedsTwoModifiers() {
        let s: UInt32 = 1, q: UInt32 = 12, c: UInt32 = 8, f5: UInt32 = 96
        #expect(HotKey(keyCode: q, modifiers: [.command]).recordRefusal != nil)
        #expect(HotKey(keyCode: s, modifiers: [.option]).recordRefusal != nil)
        #expect(HotKey(keyCode: c, modifiers: [.control]).recordRefusal != nil)
        #expect(HotKey(keyCode: s, modifiers: [.option, .command]).recordRefusal == nil)
        #expect(HotKey(keyCode: f5, modifiers: [.control]).recordRefusal == nil)
        #expect(HotKey(keyCode: q, modifiers: [.command]).isUsable, "a key saved before still applies")
    }

    /// A daemon that stopped polling marks the menu bar, as trouble does.
    @Test func testAStaleDaemonMarksTheMenuBar() throws {
        var s = try cardsSnapshot()
        s.daemon.health = .stale(since: fixtureNow.addingTimeInterval(-21 * 60))
        let t = MenuTitle.of(s, compact: false, profile: nil)
        #expect(t.text.hasSuffix("!"))
        #expect(GlyphReading.of(s, profile: nil).state == .needsYou)
        #expect(GlyphReading.of(s, profile: nil).accessibility.contains("the daemon is not polling"))
    }

    /// A description quoting a choice's raw value reads its words.
    @Test func testADescriptionReadsChoicesAsWords() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let d = SettingNames.description(try #require(schema.setting("switch_when")))
        #expect(d.contains("“Between turns”") && d.contains("“At once”") && !d.contains("\"idle\""))
    }
}
