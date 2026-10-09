import Foundation
import Testing
@testable import ClaudeSwitchCore

/// F9: the first-run setup window. Shown on launch when there is no config;
/// four steps, each skippable, each done by the CLI's JSON forms.
@Suite struct FirstRunTests {
    @Test func testOfferedOnlyWhenThereIsNoConfig() {
        #expect(FirstRun.shouldOffer(configPath: "/h/.config/claudeswitch/config.toml", exists: { _ in false }))
        #expect(!FirstRun.shouldOffer(configPath: "/h/.config/claudeswitch/config.toml", exists: { _ in true }))
        var asked: [String] = []
        _ = FirstRun.shouldOffer(configPath: nil, home: "/h", exists: { asked.append($0); return true })
        #expect(asked == ["/h/.config/claudeswitch/config.toml"], "without the CLI's answer, the default path")
    }

    @Test func testTheStepsInOrder() {
        #expect(SetupStep.allCases == [.saveLogin, .addAccounts, .workProfile, .daemon])
        for s in SetupStep.allCases {
            #expect(!s.title.isEmpty && !s.detail.isEmpty)
        }
        #expect(SetupStep.daemon.title.contains("dry run"))
    }

    @Test func testEachStepCanBeSkipped() {
        var f = SetupFlow()
        #expect(f.current == .saveLogin)
        for s in SetupStep.allCases {
            #expect(f.current == s)
            f.skip(s)
            #expect(f.status(s) == .skipped)
        }
        #expect(f.current == nil)
        #expect(f.finished)
    }

    @Test func testWhatIsAlreadyTrueCountsAsDone() {
        var f = SetupFlow()
        f.facts = SetupFacts(accounts: 1, profiles: 0, daemonInstalled: false)
        #expect(f.status(.saveLogin) == .done, "an account is saved")
        #expect(f.current == .addAccounts, "adding more is never assumed finished")
        f.complete(.addAccounts)
        #expect(f.current == .workProfile)
        f.facts.profiles = 1
        #expect(f.current == .daemon)
        f.facts.daemonInstalled = true
        #expect(f.current == nil)
        #expect(f.finished)
    }

    @Test func testASkipIsNotOverriddenAndCanBeRevisited() {
        var f = SetupFlow()
        f.skip(.saveLogin)
        f.facts.accounts = 1
        #expect(f.status(.saveLogin) == .done, "done once it is true, even after a skip")
        f.skip(.addAccounts)
        f.reopen(.addAccounts)
        #expect(f.current == .addAccounts)
    }

    @Test func testTheSeedConfigIsWrittenOnlyWhenMissing() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("cs-firstrun-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let path = dir.path + "/claudeswitch/config.toml"
        #expect(try FirstRun.seedConfig(at: path).get() == true)
        let attrs = try FileManager.default.attributesOfItem(atPath: path)
        #expect((attrs[.posixPermissions] as? NSNumber)?.intValue == 0o600)
        let dirAttrs = try FileManager.default.attributesOfItem(atPath: dir.path + "/claudeswitch")
        #expect((dirAttrs[.posixPermissions] as? NSNumber)?.intValue == 0o700)
        let text = try String(contentsOfFile: path, encoding: .utf8)
        #expect(text == FirstRun.seed)
        try "keep = 1\n".write(toFile: path, atomically: true, encoding: .utf8)
        #expect(try FirstRun.seedConfig(at: path).get() == false, "an existing config is never overwritten")
        #expect(try String(contentsOfFile: path, encoding: .utf8) == "keep = 1\n")
    }

    @Test func testTheSeedHoldsNoAccountsAndNoSettings() {
        let lines = FirstRun.seed.split(separator: "\n")
        #expect(!lines.isEmpty)
        #expect(lines.allSatisfy { $0.hasPrefix("#") }, "comments only: every setting stays at its default")
    }
}
