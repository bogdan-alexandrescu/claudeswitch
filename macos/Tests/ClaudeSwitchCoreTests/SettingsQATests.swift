import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Settings rows after the 2026-10-28 QA.
@Suite struct SettingsQATests {
    func setting(_ key: String) throws -> SettingSchema {
        let schema = try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
        return try #require(schema.setting(key))
    }

    @Test func testNamesAndChoicesReadAsWords() throws {
        #expect(SettingNames.label("api_budget") == "API budget")
        #expect(SettingNames.value(try setting("switch_when"), "idle") == "Between turns")
        #expect(SettingNames.value(try setting("switch_when"), "immediate") == "At once")
    }

    /// The field and its default speak one unit: "72h" over "Default 72h",
    /// not over "Default 3d" (config set takes no days).
    @Test func testADurationReadsAsItIsTyped() throws {
        let s = try setting("clean_after")
        #expect(SettingNames.value(s, "72h0m0s") == SettingNames.editable(s, "72h0m0s"))
        #expect(SettingNames.value(s, "72h0m0s") == "72h")
    }

    /// A description names other settings as their labels, not config keys.
    @Test func testDescriptionsNameSettingsAsPeopleSeeThem() throws {
        let d = SettingNames.description(try setting("hot_threshold"))
        #expect(!d.contains("poll_hot") && !d.contains("poll_active"))
        #expect(d.contains("“Poll hot”") && d.contains("“Poll active”"))
    }

    /// A current value its own range rules out is said, not shown silently.
    @Test func testAValueOutsideItsRangeIsSaid() throws {
        let s = try setting("poll_active")
        #expect(SettingNames.outOfRange(s, "1m0s") != nil)
        #expect(SettingNames.outOfRange(s, "3m0s") == nil)
    }
}
