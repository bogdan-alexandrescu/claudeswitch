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

    // 0.6.1: "Don't show this again". Set, the window no longer opens by
    // itself at launch (⋯ → Set up… still opens it); kept in UserDefaults.

    @Test func testDontShowAgainStopsTheLaunchOfferOnly() throws {
        let suite = "cs-firstrun-\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let noConfig: (String) -> Bool = { _ in false }
        #expect(!FirstRun.dontShowAgain(defaults), "off until the person ticks it")
        #expect(FirstRun.offerAtLaunch(configPath: "/h/c.toml", defaults: defaults, exists: noConfig))
        FirstRun.setDontShowAgain(true, defaults)
        #expect(FirstRun.dontShowAgain(defaults))
        #expect(defaults.bool(forKey: FirstRun.dontShowAgainKey), "persisted in UserDefaults")
        #expect(!FirstRun.offerAtLaunch(configPath: "/h/c.toml", defaults: defaults, exists: noConfig))
        #expect(FirstRun.shouldOffer(configPath: "/h/c.toml", exists: noConfig),
                "the config is still missing: only the launch offer is suppressed")
        FirstRun.setDontShowAgain(false, defaults)
        #expect(FirstRun.offerAtLaunch(configPath: "/h/c.toml", defaults: defaults, exists: noConfig))
        #expect(!FirstRun.offerAtLaunch(configPath: "/h/c.toml", defaults: defaults, exists: { _ in true }),
                "never with a config, ticked or not")
    }

    // 0.6.1: the window asks the CLI for the empty config (`cs init --empty
    // --json`) instead of writing it: the app never writes claudeswitch's files.

    @Test func testTheEmptyConfigIsTheCLIsToWrite() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let cli = CLI(path: try FakeAppCLI.make(in: dir))
        try FakeAppCLI.reply(dir, text: #"{"path": "/h/cfg/config.toml"}"#, exit: 0)
        let r = FirstRun.ensureConfig(path: "/h/cfg/config.toml", cli: cli, exists: { _ in false })
        #expect(r == .success(true))
        #expect(FakeAppCLI.calls(dir).last == ["init", "--empty", "--json", "--config=/h/cfg/config.toml"])
        #expect(!FileManager.default.fileExists(atPath: "/h/cfg/config.toml"), "the app wrote nothing")
    }

    @Test func testAnExistingConfigIsLeftAlone() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let cli = CLI(path: try FakeAppCLI.make(in: dir))
        #expect(FirstRun.ensureConfig(path: "/h/c.toml", cli: cli, exists: { _ in true }) == .success(false))
        #expect(FakeAppCLI.calls(dir).isEmpty, "no call when the config is there")
        // Written between the check and the call: the CLI's `exists` is not a failure.
        try FakeAppCLI.reply(dir, text: #"{"error": {"code": "exists", "message": "there", "hint": ""}}"#, exit: 1)
        #expect(FirstRun.ensureConfig(path: "/h/c.toml", cli: cli, exists: { _ in false }) == .success(false))
    }

    @Test func testABinaryTooOldForInitEmptySaysWhatToDo() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let cli = CLI(path: try FakeAppCLI.make(in: dir, version: "0.6.0"))
        try FakeAppCLI.reply(dir, text: "", stderr: "flag provided but not defined: -empty\nUsage of init:\n", exit: 2)
        let r = FirstRun.ensureConfig(path: "/h/c.toml", cli: cli, exists: { _ in false })
        guard case .failure(let e) = r else { Issue.record("expected a refusal"); return }
        #expect(e.message == FirstRun.initTooOld)
        #expect(e.message.contains("cs init --empty") && e.message.contains("cs setup"))
        #expect(!e.hint.isEmpty, "how to update")
    }
}
