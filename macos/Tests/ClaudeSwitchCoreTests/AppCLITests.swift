import Foundation
import Testing
@testable import ClaudeSwitchCore

/// A fake claudeswitch for the app's JSON CLI (docs/APP_CLI.md). It records
/// every argument vector (one line, arguments separated by a tab) and answers
/// with whatever the test put in reply.json, exiting with the status in
/// `exit` (0 when absent). Nothing real ever runs: no keychain, no launchd,
/// no config.
enum FakeAppCLI {
    static func make(in dir: URL, version: String = "0.5.1") throws -> String {
        try FakeBinary.write(in: dir, script: """
            D="$(dirname "$0")"
            for a in "$@"; do printf '%s\\t' "$a" >> "$D/args.log"; done
            printf '\\n' >> "$D/args.log"
            if [ "$1" = version ] && [ "$2" = --json ]; then echo '{"version": "\(version)", "contract": 2}'; exit 0; fi
            if [ "$1" = version ]; then echo "claudeswitch \(version)"; exit 0; fi
            [ -f "$D/stderr.txt" ] && cat "$D/stderr.txt" >&2
            [ -f "$D/reply.json" ] && cat "$D/reply.json"
            exit "$(cat "$D/exit" 2>/dev/null || echo 0)"
            """)
    }

    static func reply(_ dir: URL, fixture name: String, exit code: Int = 0) throws {
        try ClaudeSwitchCoreTests.fixture(name + ".json").write(to: dir.appendingPathComponent("reply.json"))
        try String(code).write(to: dir.appendingPathComponent("exit"), atomically: true, encoding: .utf8)
    }

    static func reply(_ dir: URL, text: String, stderr: String = "", exit code: Int) throws {
        try text.write(to: dir.appendingPathComponent("reply.json"), atomically: true, encoding: .utf8)
        try stderr.write(to: dir.appendingPathComponent("stderr.txt"), atomically: true, encoding: .utf8)
        try String(code).write(to: dir.appendingPathComponent("exit"), atomically: true, encoding: .utf8)
    }

    /// Every argument vector the fake was run with, in order.
    static func calls(_ dir: URL) -> [[String]] {
        ((try? String(contentsOf: dir.appendingPathComponent("args.log"), encoding: .utf8)) ?? "")
            .split(separator: "\n", omittingEmptySubsequences: true)
            .map { line in line.split(separator: "\t", omittingEmptySubsequences: false).map(String.init).filter { !$0.isEmpty } }
    }
}

/// Each test has its own directory and fake binary.
@Suite final class AppCLITests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    // MARK: errors

    @Test func testAnErrorObjectIsTheCLIsCodeMessageAndHint() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-error-invalid-value", exit: 1)
        let r = cli.configSet("switch_at", "200")
        guard case .failure(.app(let e)) = r else { Issue.record("expected the error object, got \(r)"); return }
        #expect(e.code == "invalid_value")
        #expect(e.message == "switch_at would make the config invalid: switch_at must be between 0 and 100, got 200")
        #expect(e.hint == "switch_at is a percent in (0, 100]")
        #expect(r.failureMessage == e.message)
        #expect(r.failureHint == e.hint)
    }

    @Test func testEveryGoErrorFixtureDecodes() throws {
        for (name, code) in [("cli-error-not-found", "not_found"), ("cli-error-usage", "usage"),
                             ("cli-error-live", "live"), ("cli-error-in-other-pool", "in_other_pool"),
                             ("cli-error-confirmation", "confirmation_required"),
                             ("cli-error-daemon-not-loaded", "daemon_not_loaded"),
                             ("cli-error-config-changed", "config_changed"),
                             ("cli-error-outside-pool", "outside_pool"),
                             ("cli-error-no-pending", "no_pending_login"),
                             ("cli-error-name-required", "name_required")] {
            let e = try #require(AppError(data: try fixture(name + ".json")), "\(name)")
            #expect(e.code == code)
            #expect(!e.message.isEmpty)
        }
        #expect(AppError(data: try fixture("cli-use.json")) == nil, "a success is not an error")
    }

    @Test func testAnErrorObjectOnExitZeroIsStillAnError() throws {
        try FakeAppCLI.reply(dir, text: #"{"error": {"code": "failed", "message": "boom", "hint": ""}}"#, exit: 0)
        guard case .failure(.app(let e)) = cli.profileList() else { Issue.record("expected an error"); return }
        #expect(e.code == "failed")
        #expect(e.hint == "")
    }

    @Test func testHumanTextFromAnOldBinaryIsTooOld() throws {
        try FakeAppCLI.reply(dir, text: "", stderr: "flag provided but not defined: -json\nusage: …", exit: 2)
        guard case .failure(.cli(.tooOld)) = cli.profileList() else { Issue.record("expected tooOld"); return }
    }

    @Test func testUnreadableOutputIsReported() throws {
        try FakeAppCLI.reply(dir, text: "all good!\n", exit: 0)
        guard case .failure(.cli(.unreadable)) = cli.profileList() else { Issue.record("expected unreadable"); return }
    }

    @Test func testAFailureWithoutAnErrorObjectSaysWhy() throws {
        try FakeAppCLI.reply(dir, text: "", stderr: "claudeswitch: something broke\n", exit: 1)
        #expect(cli.profileList().failureMessage == "something broke")
    }

    // MARK: argument vectors are refused before anything runs

    @Test func testValuesThatCouldBeReadAsFlagsAreRefused() throws {
        #expect(cli.use("--dry-run", profile: "default").failureCode == "usage")
        #expect(cli.use("work-1", profile: "-x").failureCode == "usage")
        #expect(cli.configSet("switch_at", "-5").failureCode == "invalid_value")
        #expect(cli.configSet("switch_at", "8\n5").failureCode == "invalid_value")
        #expect(cli.profileSet("review", "switch_at", "--json").failureCode == "invalid_value")
        #expect(cli.priority([]).failureCode == "usage")
        #expect(cli.loginCode(account: "work-3", code: "  ").failureCode == "usage")
        #expect(cli.profileCreate(name: "lab", dir: "/tmp/x\ny", pool: [], seed: nil).failureCode == "usage")
        #expect(FakeAppCLI.calls(dir).isEmpty, "nothing was run")
    }

    // MARK: config

    @Test func testConfigSchema() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-config-schema")
        let s = try cli.configSchema().get()
        #expect(lastCall() == ["config", "schema", "--json"])
        #expect(s.settings.count == 18)
        #expect(s.setting("hot_reserve") != nil && s.setting("unseen_calls_per_hour") != nil)
        let sw = try #require(s.setting("switch_at"))
        #expect(sw.type == "percent")
        #expect(sw.defaultValue == "85")
        #expect(sw.min == 0 && sw.max == 100 && sw.minExclusive)
        #expect(sw.profileScoped)
        #expect(sw.description == "rotate away at this much of the 5-hour window")
        let when = try #require(s.setting("switch_when"))
        #expect(when.type == "enum")
        #expect(!when.enumValues.isEmpty)
        #expect(!when.profileScoped)
        let poll = try #require(s.setting("poll_hot"))
        #expect(poll.type == "duration")
    }

    @Test func testConfigValuesAndGetAndSet() throws {
        try FakeAppCLI.reply(dir, fixture: "config")
        let v = try cli.configValues().get()
        #expect(lastCall() == ["config", "--json"])
        #expect(v.values["switch_at"] == "90")
        #expect(v.values["path"] == nil, "path is kept apart")
        #expect(v.path == "/Users/example/.config/claudeswitch/config.toml")

        try FakeAppCLI.reply(dir, fixture: "cli-config-get")
        let g = try cli.configGet("switch_at", profile: nil).get()
        #expect(lastCall() == ["config", "get", "switch_at", "--json"])
        #expect(g.value == "90" && g.scope == "global" && g.override == nil)
        _ = cli.configGet("switch_at", profile: "review")
        #expect(lastCall() == ["config", "get", "switch_at", "--profile=review", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-config-set")
        let s = try cli.configSet("switch_at", "85").get()
        #expect(lastCall() == ["config", "set", "switch_at", "85", "--json"])
        #expect(s.key == "switch_at" && s.previous == "90" && s.value == "85")
        #expect(!s.daemonRunning)
        #expect(s.warnings.isEmpty)
    }

    @Test func testWarningsAreReadWhenTheCLIGivesThem() throws {
        try FakeAppCLI.reply(dir, text: #"{"key": "poll_hot", "previous": "1m0s", "value": "20s", "path": "/p", "daemon_running": true, "warnings": ["polling this often may hit the usage limit"]}"#, exit: 0)
        let s = try cli.configSet("poll_hot", "20s").get()
        #expect(s.warnings == ["polling this often may hit the usage limit"])
        #expect(s.daemonRunning)
    }

    // MARK: profiles

    @Test func testProfileList() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-profile-list")
        let l = try cli.profileList().get()
        #expect(lastCall() == ["profile", "list", "--json"])
        #expect(l.profiles.map(\.name) == ["default", "review"])
        let d = l.profiles[0]
        #expect(d.dir == nil, "null: Claude Code's default directory")
        #expect(d.pool == ["work-1", "work-2"])
        #expect(d.live == "work-1" && d.pinned == nil)
        #expect(d.signedIn == "yes")
        #expect(d.overrides.isEmpty)
        #expect(d.thresholds.switchAt == 90 && d.thresholds.switchAtWeekly == 95)
        let r = l.profiles[1]
        #expect(r.dir == "~/.claude-review")
        #expect(r.pinned == "personal")
        #expect(r.overrides == ["switch_at": "75", "models": "Modelname"])
        #expect(r.thresholds.switchAt == 75)
        #expect(l.ghosts.count == 1)
        #expect(l.ghosts[0].profile == "old" && l.ghosts[0].account == "work-2" && l.ghosts[0].why == "removed")
        #expect(l.ghosts[0].since != nil)
    }

    @Test func testProfileCreate() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-profile-create")
        let c = try cli.profileCreate(name: "lab", dir: "~/.claude-lab", pool: ["work-2", "personal"], seed: "work-2").get()
        #expect(lastCall() == ["profile", "create", "lab", "--dir=~/.claude-lab", "--pool=work-2,personal",
                               "--seed=work-2", "--json"])
        #expect(c.profile.name == "lab")
        #expect(c.seeded == "work-2")
        _ = cli.profileCreate(name: "lab", dir: nil, pool: [], seed: nil)
        #expect(lastCall() == ["profile", "create", "lab", "--json"])
    }

    @Test func testProfileSeedForgetPoolAndSet() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-profile-seed")
        #expect(try cli.profileSeed("lab", account: "work-2").get().seeded == "work-2")
        #expect(lastCall() == ["profile", "seed", "lab", "work-2", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-profile-forget")
        #expect(try cli.profileForget("old").get().released == 1)
        #expect(lastCall() == ["profile", "forget", "old", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-profile-pool")
        let p = try cli.profilePool("default", .remove, account: "work-2", to: "review").get()
        #expect(lastCall() == ["profile", "pool", "default", "remove", "work-2", "--to=review", "--json"])
        #expect(p.profile == "review" && p.changed)
        #expect(p.pools["review"] == ["personal", "work-2"])
        _ = cli.profilePool("review", .add, account: "work-2", to: nil)
        #expect(lastCall() == ["profile", "pool", "review", "add", "work-2", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-error-in-other-pool", exit: 1)
        let refused = cli.profilePool("review", .add, account: "work-2", to: nil)
        #expect(refused.failureCode == "in_other_pool")
        #expect(refused.failureHint == "move it with: claudeswitch profile pool default remove work-2 --to review")

        try FakeAppCLI.reply(dir, fixture: "cli-profile-set")
        let s = try cli.profileSet("review", "switch_at", "70").get()
        #expect(lastCall() == ["profile", "set", "review", "switch_at", "70", "--json"])
        #expect(s.override == "70" && s.effective == "70")
        try FakeAppCLI.reply(dir, fixture: "cli-profile-set-inherit")
        let i = try cli.profileSet("review", "switch_at", "").get()
        #expect(lastCall() == ["profile", "set", "review", "switch_at", "inherit", "--json"], "blank means inherit")
        #expect(i.override == nil && i.effective == "90")
    }

    // MARK: accounts

    @Test func testAccountRenameAndPriority() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-account-rename")
        #expect(try cli.accountRename("work-2", to: "work-3").get().account == "work-3")
        #expect(lastCall() == ["account", "rename", "work-2", "work-3", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-priority")
        let p = try cli.priority(["personal", "work-1"]).get()
        #expect(lastCall() == ["priority", "personal", "work-1", "--json"])
        #expect(p.priority == ["personal", "work-1"])
        #expect(p.order == ["personal", "work-1", "work-2"])
    }

    @Test func testAccountDeleteAsksTheCLIFirstThenConfirms() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-error-confirmation", exit: 1)
        let ask = cli.accountDelete("work-2", confirmed: false)
        #expect(lastCall() == ["account", "delete", "work-2", "--json"])
        #expect(ask.failureCode == "confirmation_required")
        #expect(ask.failureMessage?.contains("bbbbbbbb-bbbb") == true, "the message names the seat")

        try FakeAppCLI.reply(dir, fixture: "cli-account-delete")
        let d = try cli.accountDelete("work-2", confirmed: true).get()
        #expect(lastCall() == ["account", "delete", "work-2", "--yes", "--json"])
        #expect(d.account == "work-2")
        #expect(d.seat?.hasPrefix("bbbbbbbb") == true)
        #expect(d.email == "person2@example.com")
        #expect(d.removedCredential && d.removedConfig && !d.removedPriority && d.removedState)
        #expect(d.removedPool == "default")
        #expect(d.twin == nil)
        #expect(d.daemonRunning)

        for (f, code) in [("cli-error-live", "live"), ("cli-error-config-changed", "config_changed"),
                          ("cli-error-daemon-not-loaded", "daemon_not_loaded")] {
            try FakeAppCLI.reply(dir, fixture: f, exit: 1)
            #expect(cli.accountDelete("work-2", confirmed: true).failureCode == code)
        }
    }

    @Test func testPinAndUnpin() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-account-pin")
        let p = try cli.pin("work-1").get()
        #expect(lastCall() == ["account", "pin", "work-1", "--json"])
        #expect(p.profile == "default" && p.pinned == "work-1")

        try FakeAppCLI.reply(dir, fixture: "cli-account-unpin")
        #expect(try cli.unpin(profile: "default").get() == ["default"])
        #expect(lastCall() == ["account", "unpin", "--profile=default", "--json"])
    }

    // MARK: recovery

    @Test func testRecovery() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-recovery")
        let items = try cli.recovery(identify: false).get()
        #expect(lastCall() == ["recovery", "--json"])
        #expect(items.map(\.slot) == ["r1", "r2"])
        #expect(items[0].account == "personal" && items[0].renewable && items[0].keptAt != nil)
        #expect(items[0].accessExpiresAt != nil && items[0].refreshExpiresAt == nil)
        #expect(items[1].error == "unreadable item" && items[1].account == nil && !items[1].renewable)
        _ = cli.recovery(identify: true)
        #expect(lastCall() == ["recovery", "--identify", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-recovery-restore")
        let r = try cli.recoveryRestore("r1", account: "personal", force: false).get()
        #expect(lastCall() == ["recovery", "restore", "r1", "personal", "--json"])
        #expect(r.cleared && r.account == "personal")
        _ = cli.recoveryRestore("r1", account: "personal", force: true)
        #expect(lastCall() == ["recovery", "restore", "r1", "personal", "--force", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-recovery-clear")
        #expect(try cli.recoveryClear("r2").get())
        #expect(lastCall() == ["recovery", "clear", "r2", "--yes", "--json"])
    }

    // MARK: use

    @Test func testUse() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-use")
        let u = try cli.use("personal", profile: "default").get()
        #expect(lastCall() == ["use", "personal", "--profile=default", "--json"])
        #expect(u.account == "personal" && u.profile == "default" && u.from == "work-1")
        #expect(u.verified && !u.dryRun)
        #expect(u.fiveHour == 12.5 && u.sevenDay == 40)
        _ = cli.use("personal", profile: nil)
        #expect(lastCall() == ["use", "personal", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-error-outside-pool", exit: 1)
        #expect(cli.use("personal", profile: "default").failureCode == "outside_pool")
    }

    // MARK: adding an account

    @Test func testBrowserLogin() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-login-start")
        let s = try cli.loginStart(account: "work-3", profile: "default", browser: "Safari").get()
        #expect(lastCall() == ["login", "work-3", "--direct", "--json", "--no-open", "--profile=default",
                               "--browser=Safari"], "S1: no --scope")
        #expect(s.url.absoluteString == "https://example.com/oauth/authorize?code=true&state=abc")
        #expect(s.account == "work-3" && s.profile == "default" && s.newAccount)
        #expect(s.expiresAt != nil)
        _ = cli.loginStart(account: "work-3", profile: nil, browser: nil)
        #expect(lastCall() == ["login", "work-3", "--direct", "--json", "--no-open"])

        try FakeAppCLI.reply(dir, fixture: "cli-login-code")
        let a = try cli.loginCode(account: "work-3", code: " abc#def \n").get()
        #expect(lastCall() == ["login", "work-3", "--code=abc#def", "--json"], "trimmed, and one argument")
        #expect(a.account == "work-3" && a.email == "person4@example.com" && a.plan == "team")
        #expect(a.seat?.hasPrefix("cccccccc") == true && a.profile == "default")
        #expect(a.pool == ["work-1", "work-2", "work-3"])
        #expect(a.configured && a.newAccount && a.renewable)

        try FakeAppCLI.reply(dir, fixture: "cli-error-no-pending", exit: 1)
        #expect(cli.loginCode(account: "work-3", code: "abc").failureCode == "no_pending_login")
    }

    @Test func testALoginURLThatIsNotHTTPSIsRefused() throws {
        try FakeAppCLI.reply(dir, text: #"{"url": "file:///etc/passwd", "expires_at": null, "pending": {"account": "x"}}"#, exit: 0)
        guard case .failure(.cli(.unreadable)) = cli.loginStart(account: "x", profile: nil, browser: nil) else {
            Issue.record("a non-https URL must not be opened"); return
        }
    }

    @Test func testSaveCurrentLogin() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-error-name-required", exit: 1)
        let ask = cli.add(account: nil, from: nil, profile: "default")
        #expect(lastCall() == ["add", "--profile=default", "--json"])
        #expect(ask.failureCode == "name_required")
        #expect(ask.failureHint == "person4", "the hint is the suggested name alone")

        try FakeAppCLI.reply(dir, fixture: "cli-login-code")
        let a = try cli.add(account: "person4", from: nil, profile: "default").get()
        #expect(lastCall() == ["add", "person4", "--profile=default", "--json"])
        #expect(a.account == "work-3")
    }

    // MARK: daemon

    @Test func testDaemon() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-daemon-status")
        let s = try cli.daemon(.status).get()
        #expect(lastCall() == ["daemon", "status", "--json"])
        #expect(s.installed && s.loaded && s.running && s.binaryIsThis)
        #expect(s.mode == "live" && s.daemonLive == true && s.daemonVersion == "0.5.0")
        #expect(s.platform == "launchd" && s.since != nil)

        for (v, word) in [(DaemonVerb.start, "start"), (.stop, "stop"), (.restart, "restart"),
                          (.live, "live"), (.dryRun, "dry-run"), (.uninstall, "uninstall")] {
            _ = cli.daemon(v)
            #expect(lastCall() == ["daemon", word, "--json"])
        }
        _ = cli.daemon(.install(live: true))
        #expect(lastCall() == ["daemon", "install", "--live", "--json"])
        _ = cli.daemon(.install(live: false))
        #expect(lastCall() == ["daemon", "install", "--dry-run", "--json"])
        _ = cli.daemon(.install(live: nil))
        #expect(lastCall() == ["daemon", "install", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-daemon-not-installed")
        let n = try cli.daemon(.status).get()
        #expect(!n.installed && n.mode == nil && n.binary == nil && n.daemonLive == nil)
    }
}

@Suite struct SchemaValidationTests {
    func schema() throws -> ConfigSchema {
        try #require(ConfigSchema(data: try fixture("cli-config-schema.json")))
    }

    @Test func testPercents() throws {
        let s = try #require(try schema().setting("switch_at"))
        #expect(s.validate("85") == nil)
        #expect(s.validate("100") == nil)
        #expect(s.validate("0") != nil, "min is exclusive")
        #expect(s.validate("101") != nil)
        #expect(s.validate("abc") != nil)
        #expect(s.validate("NaN") != nil)
        #expect(s.validate("") != nil)
    }

    @Test func testDurationsEnumsAndLists() throws {
        let sc = try schema()
        let poll = try #require(sc.setting("poll_hot"))
        #expect(poll.validate("90s") == nil)
        #expect(poll.validate("5m") == nil)
        #expect(poll.validate("soon") != nil)
        let when = try #require(sc.setting("switch_when"))
        #expect(when.validate(when.enumValues[0]) == nil)
        #expect(when.validate("whenever") != nil)
        let models = try #require(sc.setting("models"))
        #expect(models.type == "list")
        #expect(models.validate("") == nil, "an empty list is a value")
        #expect(models.validate("a,b") == nil)
    }

    @Test func testSettingsAreGroupedForThePanes() throws {
        let sc = try schema()
        let rotation = sc.settings(in: .rotation).map(\.key)
        let polling = sc.settings(in: .polling).map(\.key)
        let advanced = sc.settings(in: .advanced).map(\.key)
        #expect(rotation.first == "switch_at")
        #expect(rotation.contains("models"))
        #expect(polling.contains("poll_hot") && polling.contains("api_budget"))
        #expect(advanced.contains("refresh_window"))
        #expect(advanced.contains("hot_reserve") && advanced.contains("unseen_calls_per_hour"),
                "lane 11's advanced polling budget settings")
        let reserve = try #require(sc.setting("hot_reserve"))
        #expect(reserve.validate("10") == nil)
        #expect(reserve.validate("16") != nil, "max 15")
        #expect(reserve.validate("1.5") != nil, "an int")
        let unseen = try #require(sc.setting("unseen_calls_per_hour"))
        #expect(unseen.validate("2.5") == nil)
        #expect(unseen.validate("21") != nil)
        #expect(Set(rotation + polling + advanced).count == sc.settings.count, "every setting is somewhere, once")
        let unknown = SettingSchema(key: "brand_new", type: "int", defaultValue: "1")
        #expect(SettingsPane.of(unknown) == .advanced, "a setting this app does not know lands in Advanced")
    }
}

@Suite struct TerminalLaunchTests {
    let argv = ["/Users/example/.local/bin/claudeswitch", "run", "review"]

    @Test func testShellQuoting() {
        #expect(TerminalLauncher.shellQuote("plain") == "'plain'")
        #expect(TerminalLauncher.shellQuote("it's") == #"'it'\''s'"#)
        #expect(TerminalLauncher.shellQuote("$(rm -rf ~); `x`") == "'$(rm -rf ~); `x`'")
        #expect(TerminalLauncher.commandLine(["/a b/cs", "run", "x"]) == "'/a b/cs' 'run' 'x'")
    }

    @Test func testTerminalAndITermPassTheCommandAsAnArgumentNotAsScriptText() throws {
        for app in [TerminalApp.terminal, .iterm] {
            let p = TerminalLauncher.plan(app, argv: argv, scriptPath: "/tmp/x.command")
            #expect(p.executable == "/usr/bin/osascript")
            #expect(p.arguments.last == "'/Users/example/.local/bin/claudeswitch' 'run' 'review'")
            let script = p.arguments.dropLast().joined(separator: "\n")
            #expect(script.contains("on run argv"))
            #expect(!script.contains("review"), "nothing from the command line is spliced into AppleScript")
            #expect(p.script == nil)
        }
    }

    @Test func testGhosttyGetsTheArgumentVector() {
        let p = TerminalLauncher.plan(.ghostty, argv: argv, scriptPath: "/tmp/x.command")
        #expect(p.executable == "/usr/bin/open")
        #expect(p.arguments == ["-na", "Ghostty", "--args", "-e"] + argv)
    }

    @Test func testWarpOpensAScriptFile() throws {
        let p = TerminalLauncher.plan(.warp, argv: argv, scriptPath: "/tmp/x.command")
        #expect(p.executable == "/usr/bin/open")
        #expect(p.arguments == ["-a", "Warp", "/tmp/x.command"])
        let s = try #require(p.script)
        #expect(s.hasPrefix("#!/bin/sh\n"))
        #expect(s.contains("exec '/Users/example/.local/bin/claudeswitch' 'run' 'review'"))
    }

    @Test func testTheDefaultIsTerminal() {
        #expect(TerminalApp(rawValue: "nonsense") == nil)
        #expect(TerminalApp.default == .terminal)
        #expect(TerminalApp.allCases.map(\.rawValue) == ["Terminal", "iTerm", "Ghostty", "Warp"])
    }

    @Test func testTheRunCommandRefusesAProfileThatCouldBeAFlag() {
        #expect(TerminalLauncher.runArgv(binary: "/b/cs", profile: "review") == ["/b/cs", "run", "review"])
        #expect(TerminalLauncher.runArgv(binary: "/b/cs", profile: "--help") == nil)
    }
}

/// Claude in Chrome (lane 13): a Chrome profile per account.
@Suite final class ChromeCLITests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    @Test func testList() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-chrome-list")
        let empty = try cli.chromeList().get()
        #expect(lastCall() == ["chrome", "list", "--json"])
        #expect(empty.supported && empty.profiles.isEmpty)

        try FakeAppCLI.reply(dir, fixture: "cli-chrome-list-some")
        let l = try cli.chromeList().get()
        #expect(l.profiles.map(\.account) == ["work-1", "personal"])
        #expect(l.profiles[0].profileDir == "claudeswitch-work-1")
        #expect(l.profiles[0].added != nil && l.profiles[1].added == nil)
        #expect(l.profiles[0].liveIn == ["default"] && l.profiles[1].liveIn.isEmpty)
        #expect(l.has("work-1") && !l.has("work-2"))
    }

    @Test func testAddOpenForget() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-chrome-add")
        let a = try cli.chromeAdd("work-2").get()
        #expect(lastCall() == ["chrome", "add", "work-2", "--json"])
        #expect(a.account == "work-2" && a.created && a.opened && a.email == "person2@example.com")

        try FakeAppCLI.reply(dir, fixture: "cli-chrome-open")
        let o = try cli.chromeOpen("work-1").get()
        #expect(lastCall() == ["chrome", "open", "work-1", "--json"])
        #expect(o.opened && o.profileDir == "claudeswitch-work-1")

        try FakeAppCLI.reply(dir, fixture: "cli-chrome-forget")
        #expect(try cli.chromeForget("work-1").get())
        #expect(lastCall() == ["chrome", "forget", "work-1", "--json"])

        try FakeAppCLI.reply(dir, fixture: "cli-error-chrome-not-set-up", exit: 1)
        let r = cli.chromeOpen("work-2")
        #expect(r.failureCode == "not_found")
        #expect(r.failureHint == "set one up with: cs chrome add work-2")
        #expect(cli.chromeOpen("--all").failureCode == "usage")
    }

    /// Opens the account's Chrome profile, setting it up first when it has none.
    @Test func testOpenOrAdd() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-chrome-add")
        _ = cli.chromeOpenOrAdd("work-2", known: ChromeList(data: try fixture("cli-chrome-list.json")))
        #expect(lastCall() == ["chrome", "add", "work-2", "--json"])
        try FakeAppCLI.reply(dir, fixture: "cli-chrome-open")
        _ = cli.chromeOpenOrAdd("work-1", known: ChromeList(data: try fixture("cli-chrome-list-some.json")))
        #expect(lastCall() == ["chrome", "open", "work-1", "--json"])
    }
}

@Suite struct EmailsFromStateTests {
    /// Lane 13 records each account's email in state.json, so the app can
    /// name accounts without the keychain.
    @Test func testEmailsAreRead() throws {
        var obj = try fixtureObject("state.json")
        obj["emails"] = ["work-1": "person1@example.com"]
        let s = try #require(StateFile(data: try encode(obj)))
        #expect(s.emails["work-1"] == "person1@example.com")
        #expect(StateFile(data: try fixture("state.json"))?.emails.isEmpty == true)
    }
}
