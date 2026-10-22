import Foundation
import Testing
@testable import ClaudeSwitchCore

// IMPROVEMENTS P4 (owner decision 2026-10-21): hold_for_browser, how long a
// rotation waits after a session's last Claude in Chrome call. It is a
// rotation setting, so Settings → Rotation draws its row from the schema.

@Suite struct BrowserHoldSettingTests {
    func hold() throws -> SettingSchema {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        return try #require(schema.setting("hold_for_browser"))
    }

    @Test func testHoldForBrowserIsARotationRowAfterMaxSwitchWait() throws {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        let s = try hold()
        #expect(SettingsPane.of(s) == .rotation)
        let rotation = schema.settings(in: .rotation).map(\.key)
        let wait = try #require(rotation.firstIndex(of: "max_switch_wait"))
        #expect(rotation.firstIndex(of: "hold_for_browser") == wait + 1)
        #expect(SettingNames.label("hold_for_browser") == "Hold for browser")
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
        let s = try #require(schema.setting("hold_for_browser"))
        #expect(SettingsPane.of(s) == .rotation)
    }
}
