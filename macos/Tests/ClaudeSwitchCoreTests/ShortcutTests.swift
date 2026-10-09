import Foundation
import Testing
@testable import ClaudeSwitchCore

/// F6: the URL scheme (the fallback for App Intents, which the Command Line
/// Tools cannot register) and the global hotkey.
@Suite struct ShortcutURLTests {
    func parse(_ s: String) -> ShortcutCommand? { URL(string: s).flatMap(ShortcutURL.parse) }

    @Test func testEachCommandParses() {
        #expect(parse("claudeswitch://switch-best?profile=work") == .switchBest(profile: "work"))
        #expect(parse("claudeswitch://switch-best") == .switchBest(profile: nil))
        #expect(parse("claudeswitch://pin?profile=work") == .pin(profile: "work"))
        #expect(parse("claudeswitch://unpin?profile=default") == .unpin(profile: "default"))
        #expect(parse("claudeswitch://status") == .status(profile: nil))
        #expect(parse("claudeswitch://status?profile=review") == .status(profile: "review"))
        #expect(parse("CLAUDESWITCH://Switch-Best?profile=work") == .switchBest(profile: "work"), "case-insensitive verb")
        #expect(parse("claudeswitch://switch-best/?profile=work") == .switchBest(profile: "work"), "a trailing slash")
    }

    @Test func testAnythingElseIsRefused() {
        #expect(parse("https://switch-best?profile=work") == nil)
        #expect(parse("claudeswitch://delete?profile=work") == nil)
        #expect(parse("claudeswitch://switch-best?profile=--rf") == nil, "a profile that is not a name")
        #expect(parse("claudeswitch://switch-best?profile=a%20b") == nil)
        #expect(parse("claudeswitch://switch-best?profile=") == .switchBest(profile: nil), "empty is the followed one")
    }

    @Test func testURLsRoundTrip() {
        for c in [ShortcutCommand.switchBest(profile: "work"), .switchBest(profile: nil), .pin(profile: "work"),
                  .unpin(profile: nil), .status(profile: "default")] {
            #expect(ShortcutURL.parse(ShortcutURL.url(c)) == c, "\(c)")
        }
        #expect(ShortcutURL.url(.switchBest(profile: "work")).absoluteString == "claudeswitch://switch-best?profile=work")
    }
}

@Suite struct HotKeyTests {
    @Test func testTheSuggestedKeyIsOptionCommandS() {
        let k = HotKey.suggested
        #expect(k.display == "⌥⌘S")
        #expect(k.keyCode == 1)
        #expect(k.carbonModifiers == 0x0100 | 0x0800, "cmdKey | optionKey")
    }

    @Test func testItIsStoredAsText() {
        let k = HotKey(keyCode: 18, modifiers: [.control, .shift, .command])
        #expect(k.display == "⌃⇧⌘1")
        #expect(HotKey(stored: k.stored) == k)
        #expect(HotKey(stored: "") == nil)
        #expect(HotKey(stored: "x:y") == nil)
    }

    @Test func testAHotKeyNeedsAModifierBeyondShift() {
        #expect(HotKey(keyCode: 1, modifiers: []).isUsable == false)
        #expect(HotKey(keyCode: 1, modifiers: [.shift]).isUsable == false)
        #expect(HotKey(keyCode: 1, modifiers: [.option]).isUsable)
        #expect(HotKey(keyCode: 0xFFFF, modifiers: [.command]).isUsable == false, "a key with no name")
    }

    @Test func testKeyNames() {
        #expect(KeyNames.name(0) == "A")
        #expect(KeyNames.name(1) == "S")
        #expect(KeyNames.name(29) == "0")
        #expect(KeyNames.name(122) == "F1")
        #expect(KeyNames.name(49) == "Space")
        #expect(KeyNames.name(0xFFFF) == nil)
    }
}

@Suite struct StatusLineTests {
    @Test func testAProfilesStatusInOneLine() throws {
        let s = try fixtureSnapshot()
        let c = try #require(s.cards.first)
        let line = StatusLine.text(c)
        #expect(line.hasPrefix(c.name + " · "))
        if let a = c.active { #expect(line.contains(a.id)) }
        #expect(StatusLine.text(nil) == "No profile to report on yet.")
    }
}
