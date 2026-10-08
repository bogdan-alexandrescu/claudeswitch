import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Fixtures are real output of the Go binary, produced by
/// macos/scripts/gen-fixtures.sh against a throwaway HOME holding placeholder
/// accounts. why-profiles.json is the per-profile shape (SUFFIX=-profiles).
func fixture(_ name: String) throws -> Data {
    let parts = name.split(separator: ".")
    let url = try #require(Bundle.module.url(forResource: String(parts[0]), withExtension: String(parts[1]),
                                              subdirectory: "Fixtures"), "missing fixture \(name)")
    return try Data(contentsOf: url)
}

func fixtureObject(_ name: String) throws -> [String: Any] {
    try #require(JSONSerialization.jsonObject(with: try fixture(name)) as? [String: Any])
}

func encode(_ obj: Any) throws -> Data { try JSONSerialization.data(withJSONObject: obj) }

@Suite struct StateDecodingTests {
    @Test func testDecodesTheStateFileTheBinaryWrote() throws {
        let s = try #require(StateFile(data: try fixture("state.json")))
        #expect(Set(s.accounts.keys) == ["work-1", "work-2", "personal"], "the record the config does not name was discarded by the binary")
        #expect(s.profiles.map(\.name) == ["default", "review"])
        #expect(s.defaultProfile.active == "work-1")
        #expect(s.defaultProfile.lastSwitch != nil)
        let review = s.profiles[1]
        #expect(review.active == "personal")
        #expect(review.pinned == "personal")
        #expect(review.lastSwitch == nil, "Go's zero time means never")
        #expect(!(s.daemonLive))
        #expect(s.daemonSince != nil)
        #expect(s.savedAt != nil, "local offset and microseconds must parse")
    }

    @Test func testDecodesAnAccountsReading() throws {
        let s = try #require(StateFile(data: try fixture("state.json")))
        let w1 = try #require(s.accounts["work-1"])
        let last = try #require(w1.last)
        #expect(last.fiveHour.utilization == 3)
        #expect(last.sevenDay.utilization == 41)
        #expect(last.fiveHour.resetsAt != nil)
        #expect(last.limits.map(\.kind) == ["session", "weekly_all", "weekly_scoped"])
        #expect(last.scopedWeekly.map(\.percent) == [12])
        #expect(last.scopedWeekly[0].model == "Modelname", "Go stores the scope (IMPROVEMENTS I6)")
        #expect(last.limits[1].isActive)
        #expect(last.worst?.window == "seven_day")
        #expect(last.worst?.pct == 41)
        #expect(w1.burntUntil == nil)
        #expect(w1.lastError == nil)

        let w2 = try #require(s.accounts["work-2"])
        #expect(w2.burntUntil != nil)
        #expect(w2.burntWindow == "five_hour")

        let p = try #require(s.accounts["personal"])
        #expect(p.lastError == "usage API: 401 Unauthorized")
        #expect(p.last?.limits == [], "limits: null reads as none")
        #expect(p.burntUntil == nil)
    }

    /// A file from a binary before profiles has only the top-level fields,
    /// which are the default profile's.
    @Test func testReadsTheLegacyShape() throws {
        var obj = try fixtureObject("state.json")
        obj.removeValue(forKey: "profiles")
        // The binary no longer writes these (D10); a 0.4.x file has them.
        obj["active_account"] = "work-1"
        obj["last_switch"] = "2026-10-08T01:54:15Z"
        let s = try #require(StateFile(data: try encode(obj)))
        #expect(s.profiles.map(\.name) == ["default"])
        #expect(s.defaultProfile.active == "work-1")
    }

    /// Dev builds before D19 wrote the profiles map as "instances".
    @Test func testReadsADevBuildsInstancesKey() throws {
        var obj = try fixtureObject("state.json")
        let profiles = try #require(obj["profiles"])
        obj.removeValue(forKey: "profiles")
        obj["instances"] = profiles
        let s = try #require(StateFile(data: try encode(obj)))
        #expect(s.profiles.map(\.name) == ["default", "review"])
        #expect(s.profiles[1].pinned == "personal")
    }

    @Test func testRefusesWhatIsNotAStateFile() {
        #expect(StateFile(data: Data("not json".utf8)) == nil)
        #expect(StateFile(data: Data("[]".utf8)) == nil)
        #expect(StateFile(data: Data()) == nil)
    }

    @Test func testWrongTypesDegradeToUnknown() throws {
        let odd = """
        {"accounts": {"x": {"last_usage": {"five_hour": {"utilization": "high", "resets_at": 7},
                                            "limits": {"not": "an array"}},
                             "last_at": "yesterday", "burnt_until": true},
                      "y": 5},
         "profiles": [1, 2], "daemon_live": "yes", "saved_at": null,
         "some_future_field": {"deep": [1, 2, 3]}}
        """
        let s = try #require(StateFile(data: Data(odd.utf8)))
        let x = try #require(s.accounts["x"])
        #expect(x.last?.fiveHour.utilization == nil)
        #expect(x.last?.fiveHour.resetsAt == nil)
        #expect(x.last?.limits == [])
        #expect(x.lastAt == nil)
        #expect(x.burntUntil == nil)
        #expect(s.accounts["y"]?.id == "y")
        #expect(s.profiles.map(\.name) == ["default"])
        #expect(!(s.daemonLive))
    }

    /// Removing any one field anywhere in the real file must still decode.
    @Test func testEveryFieldIsOptional() throws {
        let obj = try fixtureObject("state.json")
        for path in keyPaths(obj) {
            let cut = removing(path, from: obj)
            #expect(StateFile(data: try encode(cut)) != nil, "without \(path.joined(separator: "."))")
        }
    }
}

@Suite struct WhyDecodingTests {
    @Test func testDecodesTheSingleProfileShape() throws {
        let w = try #require(WhyReport(data: try fixture("why.json")))
        #expect(w.profiles.count == 1)
        let p = try #require(w.primary)
        #expect(p.profile == "default")
        #expect(p.decision?.kind == "stay")
        #expect(p.decision?.reason == "active account at 42%, under the 95% trigger")
        #expect(p.decision?.target == nil)
        #expect(p.accounts.map(\.id) == ["work-1", "personal", "work-2"])
        #expect(p.accounts[0].active)
        #expect(p.accounts[0].window == "seven_day")
        #expect(abs((p.accounts[0].utilization ?? 0) - (41.63)) <= 0.01)
        #expect(!(p.accounts[2].eligible))
        #expect(p.accounts[2].why == "refused on its five_hour window")
        #expect(p.accounts[2].clearsAt != nil)
    }

    /// IMPROVEMENTS I8: each account's weekly pace, as `why --json` reports it.
    @Test func testDecodesTheWeeklyPace() throws {
        let p = try #require(WhyReport(data: try fixture("why.json"))?.primary)
        let pace = try #require(p.accounts[0].pace, "work-1 is four days into its week")
        #expect(abs(pace.expected - 400.0 / 7) < 0.5)
        #expect(abs(pace.actual - 41) < 1)
        let unused = try #require(pace.unusedAtReset)
        #expect(unused > 20 && unused < 35, "41% four days in reaches about 72% by the reset")
        #expect(pace.resetsAt != nil)
    }

    @Test func testAPaceWithoutAnEstimateHasNoExpiry() throws {
        let j = #"{"decision": {"kind": "stay", "reason": ""}, "accounts": [{"id": "a", "weekly_pace": "#
            + #"{"expected": 5, "actual": 2, "resets_at": "2026-10-11T01:57:15Z"}}]}"#
        let pace = try #require(WhyReport(data: Data(j.utf8))?.primary?.accounts.first?.pace)
        #expect(pace.unusedAtReset == nil)
        #expect(pace.atReset == nil)
    }

    @Test func testDecodesThePerProfileShape() throws {
        let w = try #require(WhyReport(data: try fixture("why-profiles.json")))
        #expect(w.profiles.map(\.profile) == ["default", "review"])
        let p = try #require(w.primary)
        #expect(p.profile == "default")
        #expect(p.current)
        #expect(p.switchAt == 90)
        #expect(p.switchAtWeekly == 95)
        #expect(p.accounts.map(\.id) == ["work-1", "personal", "work-2"])
        #expect(w.profiles[1].decision?.reason == "pinned to personal; automatic rotation is off")
        #expect(w.profiles[1].accounts == [])
    }

    @Test func testDecodesASwitch() throws {
        let j = #"{"decision": {"kind": "switch", "target": "work-2", "reason": "r", "forced": true,"#
            + #" "recovers_account": "work-1", "recovers_at": "2026-10-07T22:58:51Z"}, "accounts": []}"#
        let d = try #require(WhyReport(data: Data(j.utf8))?.primary?.decision)
        #expect(d.target == "work-2")
        #expect(d.forced)
        #expect(d.recoversAccount == "work-1")
        #expect(d.recoversAt != nil)
    }

    @Test func testRefusesOutputWithNothingInIt() {
        #expect(WhyReport(data: Data("{}".utf8)) == nil)
        #expect(WhyReport(data: Data("usage: claudeswitch".utf8)) == nil)
    }

    @Test func testEveryFieldIsOptional() throws {
        for name in ["why.json", "why-profiles.json"] {
            let obj = try fixtureObject(name)
            for path in keyPaths(obj) {
                _ = WhyReport(data: try encode(removing(path, from: obj))) // must not crash
            }
        }
    }
}

@Suite struct ConfigDecodingTests {
    @Test func testDecodesConfigJSON() throws {
        let c = try #require(ConfigSettings(data: try fixture("config.json")))
        #expect(c.switchAt == 90)
        #expect(c.switchAtWeekly == 95)
        #expect(c.hardFloor == 98)
        #expect(c.pollActive == 60)
        #expect(c.path == "/Users/example/.config/claudeswitch/config.toml")
        #expect(c.trigger(for: "five_hour") == 90)
        #expect(c.trigger(for: "seven_day") == 95)
    }

    @Test func testAWeeklyTriggerOfZeroFallsBackLikeGo() {
        let c = ConfigSettings(switchAt: 85, switchAtWeekly: 0)
        #expect(c.trigger(for: "seven_day") == 85)
        #expect(ConfigSettings().trigger(for: "five_hour") == 90)
    }

    @Test func testGoDurations() {
        #expect(GoDuration.parse("1m0s") == 60)
        #expect(GoDuration.parse("24h0m0s") == 86_400)
        #expect(GoDuration.parse("1h2m3.5s") == 3723.5)
        #expect(GoDuration.parse("250ms") == 0.25)
        #expect(GoDuration.parse("0s") == 0)
        #expect(GoDuration.parse("") == nil)
        #expect(GoDuration.parse("soon") == nil)
        #expect(GoDuration.parse("5x") == nil)
    }
}

@Suite struct GoTimeTests {
    @Test func testParsesGoEncodings() throws {
        let base = try #require(GoTime.parse("2026-10-07T22:18:51Z"))
        #expect(base.timeIntervalSince1970 == 1_791_411_531)
        #expect(abs((try #require(GoTime.parse("2026-10-07T22:18:51.5Z")).timeIntervalSince(base)) - (0.5)) <= 1e-6)
        #expect(abs((try #require(GoTime.parse("2026-10-07T15:18:51.702372-07:00")).timeIntervalSince(base)) - (0.702372)) <= 1e-5)
        #expect(abs((try #require(GoTime.parse("2026-10-07T22:18:51.123456789+00:00")).timeIntervalSince(base)) - (0.123456789)) <= 1e-6)
    }

    @Test func testZeroAndGarbageAreNil() {
        #expect(GoTime.parse("0001-01-01T00:00:00Z") == nil)
        #expect(GoTime.parse("") == nil)
        #expect(GoTime.parse("yesterday") == nil)
        #expect(GoTime.parse("2026-13-45T99:00:00Z") == nil)
    }
}

// MARK: - helpers

/// Every key path in a JSON object, objects only (arrays are walked into).
func keyPaths(_ obj: Any, prefix: [String] = []) -> [[String]] {
    var out: [[String]] = []
    if let d = obj as? [String: Any] {
        for (k, v) in d {
            out.append(prefix + [k])
            out += keyPaths(v, prefix: prefix + [k])
        }
    } else if let a = obj as? [Any] {
        for (i, v) in a.enumerated() { out += keyPaths(v, prefix: prefix + ["#\(i)"]) }
    }
    return out
}

func removing(_ path: [String], from obj: Any) -> Any {
    guard let head = path.first else { return obj }
    if head.hasPrefix("#"), var a = obj as? [Any], let i = Int(head.dropFirst()), i < a.count {
        a[i] = removing(Array(path.dropFirst()), from: a[i])
        return a
    }
    guard var d = obj as? [String: Any] else { return obj }
    if path.count == 1 {
        d.removeValue(forKey: head)
    } else if let v = d[head] {
        d[head] = removing(Array(path.dropFirst()), from: v)
    }
    return d
}
