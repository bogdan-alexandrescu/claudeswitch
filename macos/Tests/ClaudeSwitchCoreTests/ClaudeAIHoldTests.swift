import Foundation
import Testing
@testable import ClaudeSwitchCore

// IMPROVEMENTS P4 and phase 1 H (owner decisions 2026-10-21):
// hold_for_claude_ai (hold_for_browser in 0.7.8–0.7.9), how long a rotation
// waits after a session's last claude.ai call: Claude in Chrome, an artifact,
// a Doc. It is a rotation setting, so Settings → Rotation draws its row from
// the schema.

@Suite struct ClaudeAIHoldSettingTests {
    func hold() throws -> SettingSchema {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        return try #require(schema.setting("hold_for_claude_ai"))
    }

    @Test func testHoldForClaudeAIIsARotationRowAfterMaxSwitchWait() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let s = try hold()
        #expect(SettingsPane.of(s) == .rotation)
        let rotation = schema.settings(in: .rotation).map(\.key)
        let wait = try #require(rotation.firstIndex(of: "max_switch_wait"))
        #expect(rotation.firstIndex(of: "hold_for_claude_ai") == wait + 1)
        #expect(SettingNames.label("hold_for_claude_ai") == "Hold for claude.ai")
        #expect(!s.profileScoped, "global only, like max_switch_wait")
    }

    @Test func testItIsADurationOfAtLeastZeroDefaultingToFiveMinutes() throws {
        let s = try hold()
        #expect(s.type == "duration")
        #expect(SettingNames.value(s, s.defaultValue) == "5m")
        #expect(SettingNames.editable(s, s.defaultValue) == "5m")
        #expect(s.validate("0s") == nil, "0 turns the hold off")
        #expect(s.validate("2m") == nil)
        #expect(s.validate("-1m") != nil)
        #expect(s.validate("soon") != nil)
        #expect(SettingNames.description(s).hasPrefix("Hold a rotation"))
    }

    @Test func testTheSchemaWithPreferListsItToo() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema-prefer.json")))
        let s = try #require(schema.setting("hold_for_claude_ai"))
        #expect(SettingsPane.of(s) == .rotation)
    }

    /// Phase 1 S: settle_after_swap follows it in Settings → Rotation, a
    /// duration from 0 to 5 minutes.
    @Test func testSettleAfterSwapIsTheRotationRowAfterIt() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let s = try #require(schema.setting("settle_after_swap"))
        #expect(SettingsPane.of(s) == .rotation)
        let rotation = schema.settings(in: .rotation).map(\.key)
        let hold = try #require(rotation.firstIndex(of: "hold_for_claude_ai"))
        #expect(rotation.firstIndex(of: "settle_after_swap") == hold + 1)
        #expect(SettingNames.label("settle_after_swap") == "Settle after swap")
        #expect(SettingNames.value(s, s.defaultValue) == "45s")
        #expect(s.validate("0s") == nil, "0 turns it off")
        #expect(s.validate("5m") == nil)
        #expect(s.validate("6m") != nil)
        #expect(s.validate("-1s") != nil)
    }

    /// release_idle_after (owner decision 2026-10-23) follows the settle in
    /// Settings → Rotation.
    @Test func testReleaseIdleAfterIsTheRotationRowAfterTheSettle() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let s = try #require(schema.setting("release_idle_after"))
        #expect(SettingsPane.of(s) == .rotation)
        let rotation = schema.settings(in: .rotation).map(\.key)
        let settle = try #require(rotation.firstIndex(of: "settle_after_swap"))
        #expect(rotation.firstIndex(of: "release_idle_after") == settle + 1)
        #expect(SettingNames.value(s, s.defaultValue) == "30m")
        #expect(s.validate("0s") == nil, "0 never releases")
        #expect(s.validate("-1m") != nil)
    }
}
