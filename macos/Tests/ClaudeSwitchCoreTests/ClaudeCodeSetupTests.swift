import Foundation
import Testing
@testable import ClaudeSwitchCore

// OC1 (where Open Claude Code starts) and CC1-CC3 (Settings → Claude Code),
// the owner's picks of 2026-10-28: OC1 A, CC1 A, CC2 B, CC3 B.

private let home = "/Users/example"
private let now = fixtureNow

private func at(_ s: String) -> Date { GoTime.parse(s)! }

// MARK: OC1 the terminal starts in a folder

@Suite struct TerminalFolderTests {
    let argv = ["/Users/example/.local/bin/claudeswitch", "run", "review"]
    /// Spaces and a single quote: both must reach the shell as one word.
    let dir = "/Users/example/My Code/it's here"
    let quotedDir = #"'/Users/example/My Code/it'\''s here'"#
    let line = "'/Users/example/.local/bin/claudeswitch' 'run' 'review'"

    @Test func testTerminalAndITermChangeToTheFolderFirst() {
        for app in [TerminalApp.terminal, .iterm] {
            let p = TerminalLauncher.plan(app, argv: argv, scriptPath: "/tmp/x.command", dir: dir)
            #expect(p.executable == "/usr/bin/osascript")
            #expect(p.arguments.last == "cd \(quotedDir) && \(line)")
            let script = p.arguments.dropLast().joined(separator: "\n")
            #expect(!script.contains("My Code"), "the folder is an argument, never AppleScript text")
            #expect(p.script == nil)
        }
    }

    @Test func testGhosttyGetsTheWorkingDirectoryBeforeItsCommand() {
        let p = TerminalLauncher.plan(.ghostty, argv: argv, scriptPath: "/tmp/x.command", dir: dir)
        #expect(p.executable == "/usr/bin/open")
        #expect(p.arguments == ["-na", "Ghostty", "--args", "--working-directory=" + dir, "-e"] + argv)
    }

    @Test func testWarpsScriptChangesToTheFolderThenExecs() throws {
        let p = TerminalLauncher.plan(.warp, argv: argv, scriptPath: "/tmp/x.command", dir: dir)
        #expect(p.arguments == ["-a", "Warp", "/tmp/x.command"])
        #expect(try #require(p.script) == "#!/bin/sh\ncd \(quotedDir) && exec \(line)\n")
    }

    @Test func testNoFolderStartsWhereEachTerminalAlwaysDid() throws {
        #expect(TerminalLauncher.plan(.terminal, argv: argv, scriptPath: "/tmp/x").arguments.last == line)
        #expect(TerminalLauncher.plan(.iterm, argv: argv, scriptPath: "/tmp/x", dir: nil).arguments.last == line)
        #expect(TerminalLauncher.plan(.ghostty, argv: argv, scriptPath: "/tmp/x").arguments
                == ["-na", "Ghostty", "--args", "-e"] + argv)
        #expect(try #require(TerminalLauncher.plan(.warp, argv: argv, scriptPath: "/tmp/x").script)
                == "#!/bin/sh\nexec \(line)\n")
    }

    @Test func testOnlyAnAbsoluteFolderWithoutControlCharactersIsUsable() {
        #expect(TerminalLauncher.usableDir(dir))
        #expect(!TerminalLauncher.usableDir("code/website"))
        #expect(!TerminalLauncher.usableDir("/Users/example/a\nrm -rf ~"))
        #expect(!TerminalLauncher.usableDir(""))
    }
}

// MARK: OC1 recent folders and the last folder

@Suite struct RecentFoldersTests {
    @Test func testDecodesEachProfilesFoldersNewestFirst() throws {
        let r = try #require(RecentFolders(data: try fixture("cli-profile-recent.json")))
        let d = r.folders("default")
        #expect(d.count == 6)
        #expect(d.first == RecentFolder(path: "/Users/example/code/claudeswitch", lastUsed: at("2026-10-08T00:46:41Z")))
        #expect(d.last?.lastUsed == nil, "a folder with no transcript left has no time")
        #expect(r.folders("review").isEmpty)
        #expect(r.folders("nobody").isEmpty)
    }

    @Test func testARelativePathIsNoFolderAndAnotherShapeIsNoAnswer() throws {
        let j = #"{"profiles": [{"profile": "default", "folders": [{"path": "code/x"}, {"path": "/a/b"}]}]}"#
        #expect(RecentFolders(data: Data(j.utf8))?.folders("default").map(\.path) == ["/a/b"])
        #expect(RecentFolders(data: Data(#"{"folders": []}"#.utf8)) == nil)
        #expect(RecentFolders(data: try fixture("cli-error-usage.json")) == nil)
    }

    let recent = [RecentFolder(path: "/Users/example/code/website", lastUsed: at("2026-10-07T00:46:41Z")),
                  RecentFolder(path: "/Users/example/code/claudeswitch", lastUsed: at("2026-10-08T00:46:41Z")),
                  RecentFolder(path: "/Users/example/code/old", lastUsed: nil)]

    @Test func testTheNewestRecentFolderIsTheLastWithNothingChosen() {
        let p = LastFolder.pick(remembered: nil, recent: recent, exists: { _ in true })
        #expect(p == FolderPick(path: "/Users/example/code/claudeswitch", at: at("2026-10-08T00:46:41Z")))
    }

    @Test func testAFolderChosenSinceWinsAndAnOlderChoiceDoesNot() {
        let newer = RememberedFolder(path: "/Users/example/notes", at: at("2026-10-08T01:00:00Z"))
        #expect(LastFolder.pick(remembered: newer, recent: recent, exists: { _ in true })?.path == "/Users/example/notes")
        let older = RememberedFolder(path: "/Users/example/notes", at: at("2026-10-01T00:00:00Z"))
        #expect(LastFolder.pick(remembered: older, recent: recent, exists: { _ in true })?.path
                == "/Users/example/code/claudeswitch")
    }

    @Test func testAChosenFolderThatIsGoneIsPassedOver() {
        let gone = RememberedFolder(path: "/Users/example/deleted", at: at("2026-10-08T02:00:00Z"))
        let p = LastFolder.pick(remembered: gone, recent: recent, exists: { $0 != "/Users/example/deleted" })
        #expect(p?.path == "/Users/example/code/claudeswitch")
        #expect(LastFolder.pick(remembered: gone, recent: [], exists: { _ in false }) == nil, "then the home folder")
    }

    @Test func testAFolderWithNoRecordedUseIsNeverTheLast() {
        let untimed = [RecentFolder(path: "/Users/example/a", lastUsed: nil)]
        #expect(LastFolder.pick(remembered: nil, recent: untimed, exists: { _ in true }) == nil)
    }

    @Test func testAChoiceAsNewAsTheNewestRecentWins() {
        let same = RememberedFolder(path: "/Users/example/notes", at: at("2026-10-08T00:46:41Z"))
        #expect(LastFolder.pick(remembered: same, recent: recent, exists: { _ in true })?.path == "/Users/example/notes")
    }

    @Test func testRememberedFoldersSurviveTheDefaultsRoundTrip() {
        let m = ["work": RememberedFolder(path: "/Users/example/work/infra", at: at("2026-10-08T00:00:00Z"))]
        #expect(RememberedFolder.load(RememberedFolder.plist(m)) == m)
        let junk: [String: Any] = ["a": ["path": "relative", "at": 1.0], "b": ["path": "/x"], "c": "nonsense",
                                   "d": ["path": "/ok", "at": 2.0]]
        #expect(RememberedFolder.load(junk) == ["d": RememberedFolder(path: "/ok", at: Date(timeIntervalSince1970: 2))])
        #expect(RememberedFolder.load(nil).isEmpty)
    }

    @Test func testTheFoldersMacOSGuardsAreNotCheckedForExistence() {
        #expect(ProtectedFolders.contains(home + "/Documents/app", home: home))
        #expect(ProtectedFolders.contains(home + "/Desktop", home: home))
        #expect(ProtectedFolders.contains(home + "/Library/Mobile Documents/com~apple~CloudDocs/x", home: home))
        #expect(ProtectedFolders.contains("/Volumes/Backup/code", home: home))
        #expect(!ProtectedFolders.contains(home + "/code/website", home: home))
        #expect(!ProtectedFolders.contains(home + "/Documentsx", home: home))
    }
}

@Suite struct FolderTextTests {
    @Test func testPathsInTheHomeFolderReadWithATilde() {
        #expect(FolderText.abbreviate(home + "/code/website", home: home) == "~/code/website")
        #expect(FolderText.abbreviate(home, home: home) == "~")
        #expect(FolderText.abbreviate("/Users/examplex/code", home: home) == "/Users/examplex/code")
        #expect(FolderText.abbreviate("/opt/src", home: home) == "/opt/src")
    }

    @Test func testTheButtonNamesTheFolderOrTheHomeFolder() {
        #expect(FolderText.buttonLine(FolderPick(path: home + "/code/claudeswitch", at: nil), home: home)
                == "in claudeswitch")
        #expect(FolderText.buttonLine(nil, home: home) == "in your home folder")
        #expect(FolderText.buttonLine(FolderPick(path: home, at: nil), home: home) == "in your home folder")
    }

    @Test func testTheTooltipNamesTheWholePath() {
        #expect(FolderText.help(profile: "work", pick: FolderPick(path: home + "/work/infra", at: nil), home: home)
                == "Open Claude Code for work in ~/work/infra. The folder line, or a right-click, chooses another.")
        #expect(FolderText.help(profile: "work", pick: nil, home: home).contains("in your home folder."))
    }

    @Test func testAgesAreOneShortUnit() {
        func a(_ s: TimeInterval) -> String { FolderText.age(now.addingTimeInterval(-s), now: now) }
        #expect(a(20) == "now")
        #expect(a(5 * 60) == "5m")
        #expect(a(2 * 3600) == "2h")
        #expect(a(26 * 3600) == "1d")
        #expect(a(3 * 86_400) == "3d")
        #expect(a(8 * 86_400) == "1w")
        #expect(a(15 * 86_400) == "2w")
        #expect(a(-60) == "now", "a time after the clock is not a negative age")
        #expect(FolderText.age(nil, now: now) == "")
    }

    @Test func testAMenuLineIsTheNameThenWhereAndHowLongAgo() {
        #expect(FolderText.menuLine(home + "/code/claudeswitch", at: now.addingTimeInterval(-7200), home: home, now: now)
                == "claudeswitch  ~/code · 2h")
        #expect(FolderText.menuLine(home + "/code/old", at: nil, home: home, now: now) == "old  ~/code")
        let long = FolderText.menuLine(home + "/clients/northwind/enterprise-integration-service-for-the-warehouse",
                                       at: nil, home: home, now: now)
        #expect(long.hasPrefix("enterprise-integra…for-the-warehouse  ~/clients/northwind"), "cut in its middle")
    }

    @Test func testTheMenusFixedWords() {
        #expect(FolderText.header("default") == "Recent in default")
        #expect(FolderText.empty("work") == "No folders yet: work has not been opened in one")
        #expect(FolderText.chooseItem == "Choose Folder…")
        #expect(FolderText.homeItem == "Home Folder")
    }
}

@Suite struct FolderMenuTests {
    func recent() throws -> [RecentFolder] {
        try #require(RecentFolders(data: try fixture("cli-profile-recent.json"))).folders("default")
    }

    @Test func testTheCurrentFolderIsFirstAndChecked() throws {
        let r = try recent()
        let e = FolderMenu.entries(recent: r, current: LastFolder.pick(remembered: nil, recent: r, exists: { _ in true }),
                                   home: home, now: now)
        #expect(e.map(\.line).prefix(2) == ["claudeswitch  ~/code · 2h", "website  ~/code · 1d"])
        #expect(e.filter(\.checked).map(\.path) == [home + "/code/claudeswitch"])
        #expect(e.count == 6)
    }

    @Test func testAChosenFolderTheListLacksIsAddedChecked() throws {
        let chosen = FolderPick(path: home + "/scratch/new", at: now.addingTimeInterval(-300))
        let e = FolderMenu.entries(recent: try recent(), current: chosen, home: home, now: now)
        #expect(e.first == FolderMenuEntry(path: home + "/scratch/new", line: "new  ~/scratch · 5m", checked: true))
        #expect(e.count == 7)
    }

    @Test func testAChosenFolderFromLowerDownMovesUpWithItsNewerTime() throws {
        let chosen = FolderPick(path: home + "/code/notes", at: now.addingTimeInterval(-600))
        let e = FolderMenu.entries(recent: try recent(), current: chosen, home: home, now: now)
        #expect(e.first?.line == "notes  ~/code · 10m")
        #expect(e.filter { $0.path == home + "/code/notes" }.count == 1)
    }

    @Test func testTheHomeFolderHasItsOwnItem() {
        let r = [RecentFolder(path: home, lastUsed: now), RecentFolder(path: home + "/a", lastUsed: nil)]
        let e = FolderMenu.entries(recent: r, current: FolderPick(path: home, at: now), home: home, now: now)
        #expect(e.map(\.path) == [home + "/a"])
        #expect(e.allSatisfy { !$0.checked })
    }

    @Test func testNoFoldersIsAnEmptyList() {
        #expect(FolderMenu.entries(recent: [], current: nil, home: home, now: now).isEmpty)
    }
}

// MARK: CC1-CC3 what Claude Code reads

@Suite struct ClaudeSetupDecodingTests {
    func every() throws -> ClaudeSetup { try #require(ClaudeSetup(data: try fixture("cli-claude-show.json"))) }
    func review() throws -> ClaudeSetup { try #require(ClaudeSetup(data: try fixture("cli-claude-show-review.json"))) }

    @Test func testDecodesWhatClaudeCodeReadsFromTheBaseDir() throws {
        let s = try every()
        #expect(s.dir == "/Users/example/.claude")
        #expect(s.profile == nil && s.base)
        #expect(s.instructions?.lines == 42)
        #expect(s.instructions?.modified == at("2026-10-06T02:46:41Z"))
        #expect(s.settings?.model == "opus")
        #expect(s.settings?.hooks == ["PreToolUse", "Stop"])
        #expect(s.settings?.envNames == ["DISABLE_TELEMETRY", "HTTP_PROXY", "NODE_OPTIONS"])
        #expect(s.skills.count == 7)
        #expect(s.commands.map(\.name) == ["git/sync", "ship", "standup"])
        #expect(s.plugins.map(\.id) == ["cs@claudeswitch", "figma-tools@example-marketplace",
                                        "github@claude-plugins-official", "slack@claude-plugins-official"])
        #expect(s.plugins.last?.enabled == false)
        #expect(s.servers.map(\.target) == ["npx", "mcp.linear.example", "npx"])
    }

    @Test func testDecodesEachProfileAndHowItHasItsItems() throws {
        let s = try every()
        #expect(s.baseProfile?.name == "default")
        let r = try #require(s.profiles.first { $0.name == "review" })
        #expect(!r.base && r.dir == "/Users/example/.claude-review")
        #expect(r.items.map(\.state) == [.shared, .own, .elsewhere, .shared, .missing])
        #expect(r.state(.settings) == .own)
        #expect(r.plugins.map(\.name) == ["cs"])
        #expect(r.servers == 3)
    }

    @Test func testDecodesOneProfilesViewWithItsLinksTargets() throws {
        let s = try review()
        #expect(s.profile == "review" && !s.base)
        #expect(s.share(.instructions) == .shared)
        #expect(s.share(.agents) == .missing)
        #expect(s.instructions?.path == "/Users/example/.claude-review/CLAUDE.md")
        #expect(s.instructions?.target == "/Users/example/.claude/CLAUDE.md", "a shared file is edited where it lives")
        #expect(s.profiles.isEmpty)
        #expect(try every().share(.instructions) == nil, "~/.claude itself shares nothing")
    }

    @Test func testAnUnknownStateIsShownButNeverActedOn() throws {
        let j = #"{"name": "x", "items": [{"item": "skills", "state": "brand-new", "summary": "?"}, {"item": "nope"}]}"#
        let p = try #require(ProfileSetup(JSON(data: Data(j.utf8))!))
        #expect(p.items.count == 1)
        #expect(p.items[0].state == nil)
    }

    @Test func testAnErrorObjectOrANoDirAnswerIsNoSetup() throws {
        #expect(ClaudeSetup(data: try fixture("cli-error-usage.json")) == nil)
        #expect(ClaudeSetup(data: Data(#"{"skills": []}"#.utf8)) == nil)
    }
}

/// Never shown: settings.json `env` values, MCP servers' arguments, headers
/// and URL paths. The model has nowhere to keep them.
@Suite struct ClaudeSetupPrivacyTests {
    @Test func testAnEnvSentAsAnObjectKeepsOnlyItsNames() throws {
        let j = #"""
            {"dir": "/Users/example/.claude",
             "settings": {"path": "/s", "env": {"API_TOKEN": "sk-placeholder-secret-0000", "HTTP_PROXY": "http://proxy.example:3128"}}}
            """#
        let s = try #require(ClaudeSetup(data: Data(j.utf8)))
        #expect(s.settings?.envNames == ["API_TOKEN", "HTTP_PROXY"])
        let all = String(describing: s)
        #expect(!all.contains("sk-placeholder-secret-0000"))
        #expect(!all.contains("proxy.example"))
        // On screen, not even the names: a count.
        let rows = SetupRows.build(s, now: now)
        let shown = rows.flatMap { [$0.summary] + $0.items.flatMap { [$0.name, $0.detail] } }
        #expect(shown.contains("2 variables, values hidden"))
        #expect(!shown.contains { $0.contains("API_TOKEN") || $0.contains("secret") })
    }

    @Test func testAServerIsItsHostOrItsCommandsNameAlone() {
        #expect(ServerEntry.safeTarget("https://user:pw@mcp.linear.example/sse?token=abc") == "mcp.linear.example")
        #expect(ServerEntry.safeTarget("npx -y @example/postgres-mcp postgres://u:p@db/x") == "npx")
        #expect(ServerEntry.safeTarget("/usr/local/bin/node") == "node")
        #expect(ServerEntry.safeTarget("mcp.linear.example") == "mcp.linear.example")
        #expect(ServerEntry.safeTarget("tool?key=1") == "tool")
        #expect(ServerEntry.safeTarget("") == "")
    }

    @Test func testAServerTheCLISendsWithItsArgumentsShowsNone() throws {
        let j = #"{"dir": "/d", "servers": [{"name": "db", "type": "stdio", "target": "uvx run --password hunter2"}]}"#
        let s = try #require(ClaudeSetup(data: Data(j.utf8)))
        #expect(s.servers.first?.target == "uvx")
        #expect(!String(describing: s).contains("hunter2"))
    }
}

@Suite struct SetupRowsTests {
    @Test func testEveryProfilesRowsWithTheirSummariesAndActions() throws {
        let s = try #require(ClaudeSetup(data: try fixture("cli-claude-show.json")))
        let rows = SetupRows.build(s, now: now)
        #expect(rows.map(\.kind) == SetupKind.allCases)
        #expect(rows.map(\.summary) == ["CLAUDE.md · 42 lines · edited 2 days ago",
                                        "model opus · 12 allowed, 2 denied · 2 hooks", "7 skills", "2 agents",
                                        "3 commands", "4 installed, 3 on", "3 servers"])
        #expect(rows[0].file == .open("/Users/example/.claude/CLAUDE.md"))
        #expect(rows[0].edit == .instructions && rows[1].edit == .settings && rows[2].edit == nil)
        #expect(rows[2].file == .reveal("/Users/example/.claude/skills"))
        #expect(rows[5].file == .reveal("/Users/example/.claude/plugins"))
        #expect(rows[6].file == nil, "MCP servers live in ~/.claude.json, which the pane does not open")
        #expect(rows.allSatisfy { $0.share == nil })
        #expect(rows[0].items.isEmpty, "Instructions does not fold")
        #expect(rows[4].items.map(\.name) == ["/git/sync", "/ship", "/standup"])
        #expect(rows[4].items[0].detail == "No description")
        #expect(rows[5].items.map(\.detail).first == "claudeswitch · 0.8.19")
        #expect(rows[5].items.allSatisfy { $0.plugin != nil })
        #expect(rows[6].items.map(\.detail) == ["stdio · npx", "http · mcp.linear.example", "stdio · npx"])
    }

    @Test func testOneProfilesRowsCarryHowItHasEachItem() throws {
        let s = try #require(ClaudeSetup(data: try fixture("cli-claude-show-review.json")))
        let rows = SetupRows.build(s, now: now)
        #expect(rows.map(\.share) == [.shared, .own, .elsewhere, .missing, .shared, nil, nil])
        #expect(rows[0].summary == "CLAUDE.md · 42 lines", "one profile's: without the age")
        #expect(rows[1].summary == "model sonnet · 3 allowed")
        #expect(rows[3].summary == "not set up")
        #expect(rows[3].file == nil, "nothing to reveal")
        #expect(rows[6].summary == "3 servers, copied when review was made")
        #expect(rows[0].file == .open("/Users/example/.claude-review/CLAUDE.md"))
    }

    @Test func testEachStatesBadgeAndAction() {
        #expect(ShareState.shared.badge == "Shared" && ShareState.shared.action == .makeOwn)
        #expect(ShareState.elsewhere.badge == "Linked elsewhere" && ShareState.elsewhere.action == .makeOwn)
        #expect(ShareState.own.badge == "Its own" && ShareState.own.action == .useShared)
        #expect(ShareState.missing.badge == nil && ShareState.missing.action == .share)
        #expect([ShareAction.makeOwn, .useShared, .share].map(\.title) == ["Make Own Copy", "Use Shared…", "Share"])
    }

    @Test func testTheKindsTitlesAndItems() {
        #expect(SetupKind.allCases.map(\.title)
                == ["Instructions", "Settings", "Skills", "Agents", "Commands", "Plugins", "MCP Servers"])
        #expect(SetupKind.allCases.compactMap(\.item) == [.instructions, .settings, .skills, .agents, .commands])
        #expect(SetupItemName.allCases.filter(\.isFile) == [.instructions, .settings])
        #expect(SetupItemName.allCases.map(\.kind.item) == SetupItemName.allCases.map { Optional($0) })
    }
}

@Suite struct ClaudeSetupTextTests {
    func settings(_ j: String) -> SettingsFile? { SettingsFile(JSON(data: Data(j.utf8))!) }

    @Test func testSettingsSaysWhatItSets() {
        #expect(SetupText.settings(nil) == "not set up")
        #expect(SetupText.settings(settings(#"{"path": "/s"}"#)) == "no settings")
        #expect(SetupText.settings(settings(#"{"path": "/s", "deny": 2, "hooks": ["Stop"]}"#)) == "2 denied · 1 hook")
        #expect(SetupText.settings(settings(#"{"path": "/s", "error": "unexpected end of JSON input"}"#))
                == "not valid JSON: unexpected end of JSON input")
        #expect(SetupText.settingsLines(settings(#"{"path": "/s", "error": "x"}"#)).isEmpty)
        #expect(SetupText.settingsLines(settings(#"{"path": "/s"}"#)).map(\.detail)
                == ["Claude Code's default", "none", "none", "not set", "none"])
    }

    @Test func testCountsAndTheEnvironment() {
        #expect(SetupText.count(1, .skills) == "1 skill")
        #expect(SetupText.count(0, .agents) == "none")
        #expect(SetupText.count(3, .commands) == "3 commands")
        #expect(SetupText.environment(1) == "1 variable, value hidden")
        #expect(SetupText.environment(3) == "3 variables, values hidden")
        #expect(SetupText.plugins([]) == "none installed")
        #expect(SetupText.servers(0, copiedInto: "work") == "none")
        #expect(SetupText.servers(1, copiedInto: nil) == "1 server")
        #expect(SetupText.instructions(nil, edited: true, now: now) == "not set up")
    }

    @Test func testEditedAgesInWords() {
        func e(_ s: TimeInterval) -> String { SetupText.editedAgo(now.addingTimeInterval(-s), now: now) }
        #expect(e(10) == "edited just now")
        #expect(e(60) == "edited 1 minute ago")
        #expect(e(3 * 3600) == "edited 3 hours ago")
        #expect(e(2 * 86_400) == "edited 2 days ago")
        #expect(e(20 * 86_400) == "edited 2 weeks ago")
        #expect(e(90 * 86_400) == "edited 3 months ago")
    }

    @Test func testTheFooterSaysWhoSharesAndWhatEachKeeps() throws {
        let s = try #require(ClaudeSetup(data: try fixture("cli-claude-show.json")))
        #expect(SetupText.footer(s.profiles, picker: true)
                == "Shared by default (it is ~/.claude) and review; review keeps its own settings.json. "
                    + "Showing picks one profile's setup.")
        #expect(SetupText.footer(Array(s.profiles.prefix(1)), picker: false) == "Read by default (it is ~/.claude).")
        #expect(SetupText.footer([], picker: false) == nil)
    }

    @Test func testAProfilesLineInSettingsProfiles() throws {
        let s = try #require(ClaudeSetup(data: try fixture("cli-claude-show.json")))
        #expect(SetupText.profileLine(s.profiles[0]) == "This profile is ~/.claude: the setup every other profile can share.")
        #expect(SetupText.profileLine(s.profiles[1])
                == "Shares CLAUDE.md and commands; its own settings.json; skills linked elsewhere; no agents.")
        let fresh = try #require(ProfileSetup(JSON(data: Data(#"""
            {"name": "lab", "items": [{"item": "CLAUDE.md", "state": "missing"}, {"item": "skills", "state": "missing"}]}
            """#.utf8))!))
        #expect(SetupText.profileLine(fresh) == "No CLAUDE.md or skills.")
    }

    @Test func testThePickerNamesEachProfileAndItsDirectory() throws {
        let s = try #require(ClaudeSetup(data: try fixture("cli-claude-show.json")))
        #expect(s.profiles.map { SetupText.pickerLabel($0, home: home) } == ["default", "review (~/.claude-review)"])
        let long = try #require(ProfileSetup(JSON(data: Data(#"""
            {"name": "client-example-enterprise-integration", "base": false,
             "dir": "/Users/example/Projects/clients/example/very/deep/path/.claude-enterprise"}
            """#.utf8))!))
        #expect(SetupText.pickerLabel(long, home: home) == "client-example…e-integration (~/Projects/clien…aude-enterprise)")
        #expect(SetupText.everyProfile == "Every profile (~/.claude)")
    }

    @Test func testTheSubtitleNamesTheProfileShown() {
        #expect(SetupText.subtitle(scope: nil) == "What Claude Code reads from ~/.claude, in every profile that shares it.")
        #expect(SetupText.subtitle(scope: "work") == "What Claude Code reads in the profile work.")
    }

    @Test func testTheEditorSaysWhoReadsTheFile() throws {
        let p = try #require(ClaudeSetup(data: try fixture("cli-claude-show.json"))).profiles
        #expect(SetupText.readers(.instructions, scope: nil, profiles: p) == ["default", "review"])
        #expect(SetupText.readers(.settings, scope: nil, profiles: p) == ["default"])
        #expect(SetupText.readers(.settings, scope: "review", profiles: p) == ["review"])
        #expect(SetupText.readers(.instructions, scope: "review", profiles: p) == ["default", "review"])
        #expect(SetupText.readers(.instructions, scope: nil, profiles: []) == ["default"])
        #expect(SetupText.editorCaption(target: "/Users/example/.claude/CLAUDE.md", home: home,
                                        readers: ["default", "review"], linked: false)
                == "~/.claude/CLAUDE.md, read by default and review")
        #expect(SetupText.editorCaption(target: "/Users/example/.claude/CLAUDE.md", home: home,
                                        readers: ["default", "review"], linked: true)
                == "~/.claude/CLAUDE.md, shared: saving changes it for default and review")
        #expect(SetupText.editorTitle(.settings) == "Edit settings.json")
        #expect(SetupText.editorFoot(.settings).hasSuffix("It must be valid JSON."))
    }

    @Test func testTheNotesAfterEachChange() throws {
        let w = try #require(SetupWriteResult(JSON(data: try fixture("cli-claude-write.json"))!))
        #expect(SetupText.saved(w, home: home) == "Saved ~/.claude/CLAUDE.md; review reads it too")
        let share = try #require(ShareResult(JSON(data: try fixture("cli-profile-share.json"))!))
        #expect(SetupText.shareNote(share)
                == "review now shares settings.json with ~/.claude; its own is kept as settings.json.claudeswitch-20261008-024641")
        let own = try #require(ShareResult(JSON(data: try fixture("cli-profile-own.json"))!))
        #expect(SetupText.shareNote(own) == "review now has its own copy of skills")
        let t = try #require(PluginToggle(JSON(data: try fixture("cli-claude-plugin.json"))!))
        #expect(SetupText.pluginNote(t, name: "slack") == "slack is now on")
        #expect(SetupText.pluginFailed("slack", on: false) == "Could not turn slack off")
        #expect(SetupText.shareFailed(.makeOwn, profile: "work", item: .skills) == "Could not give work its own skills")
    }

    @Test func testUseSharedAsksFirstAndNamesTheBackup() {
        let c = SetupText.useSharedConfirmation(profile: "work", item: .settings)
        #expect(c.title == "Use the shared settings.json in work?")
        #expect(c.message.contains("settings.json.claudeswitch-<date>-<time>"))
        #expect(c.action == "Use Shared" && !c.destructive)
    }
}

@Suite struct SetupTargetTests {
    func profiles(_ j: String) -> [ProfileSetup] { JSON(data: Data(j.utf8))!.array.compactMap(ProfileSetup.init) }

    @Test func testAWriteGoesToTheProfileShownOrTheOneThatIsTheBaseDir() {
        let p = profiles(#"[{"name": "default", "base": true}, {"name": "work", "base": false}]"#)
        #expect(SetupTarget.of(scope: nil, profiles: p) == .profile(nil), "default is the CLI's own default")
        #expect(SetupTarget.of(scope: "work", profiles: p) == .profile("work"))
        #expect(SetupTarget.of(scope: "default", profiles: p) == .profile(nil))
        let other = profiles(#"[{"name": "main", "base": true}]"#)
        #expect(SetupTarget.of(scope: nil, profiles: other) == .profile("main"))
        let none = profiles(#"[{"name": "default", "base": false}]"#)
        #expect(SetupTarget.of(scope: nil, profiles: none) == .unavailable)
        #expect(SetupTarget.of(scope: nil, profiles: []) == .profile(nil))
    }
}

@Suite struct EditorBufferTests {
    @Test func testNoFileIsAnEmptyOneWithTheEmptyStringsHash() throws {
        let b = try EditorBuffer.load(nil).get()
        #expect(b.text == "")
        #expect(b.sha256 == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
    }

    @Test func testTheHashIsOfTheBytesRead() throws {
        let b = try EditorBuffer.load(Data("# Notes\n".utf8)).get()
        #expect(b.text == "# Notes\n")
        #expect(b.sha256 == EditorBuffer.hash(Data("# Notes\n".utf8)))
        #expect(b.sha256.count == 64)
    }

    @Test func testTooLargeOrNotTextIsNotOpened() {
        #expect(EditorBuffer.load(Data(count: EditorBuffer.limit + 1)) == .failure(.tooLarge))
        #expect(EditorBuffer.load(Data([0xff, 0xfe, 0xfd])) == .failure(.notText))
        #expect(EditorBuffer.loadMessage(.tooLarge, item: .instructions).hasPrefix("CLAUDE.md is larger than 1 MiB"))
    }
}

// MARK: the CLI calls

/// A fake claudeswitch that records its arguments and its stdin, and
/// answers with reply.json.
private enum FakeSetupCLI {
    static func make(in dir: URL) throws -> String {
        try FakeBinary.write(in: dir, script: """
            D="$(dirname "$0")"
            for a in "$@"; do printf '%s\\t' "$a" >> "$D/args.log"; done
            printf '\\n' >> "$D/args.log"
            if [ "$1" = version ]; then echo '{"version": "0.8.20", "contract": 2}'; exit 0; fi
            cat > "$D/stdin.txt"
            [ -f "$D/reply.json" ] && cat "$D/reply.json"
            exit "$(cat "$D/exit" 2>/dev/null || echo 0)"
            """)
    }
}

@Suite final class SetupCLITests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeSetupCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func reply(_ name: String, exit code: Int = 0) throws {
        try fixture(name).write(to: dir.appendingPathComponent("reply.json"))
        try String(code).write(to: dir.appendingPathComponent("exit"), atomically: true, encoding: .utf8)
    }

    var last: [String] { FakeAppCLI.calls(dir).last ?? [] }
    var stdin: String { (try? String(contentsOf: dir.appendingPathComponent("stdin.txt"), encoding: .utf8)) ?? "" }

    @Test func testRecentFoldersIsOneCall() throws {
        try reply("cli-profile-recent.json")
        #expect(try cli.profileRecent().get().folders("default").count == 6)
        #expect(last == ["profile", "recent", "--json"])
    }

    @Test func testShowReadsEveryProfileOrOne() throws {
        try reply("cli-claude-show.json")
        #expect(try cli.claudeShow().get().profiles.count == 2)
        #expect(last == ["claude", "show", "--json"])
        try reply("cli-claude-show-review.json")
        #expect(try cli.claudeShow(profile: "review").get().profile == "review")
        #expect(last == ["claude", "show", "--profile=review", "--json"])
    }

    @Test func testAnOlderBinaryWithoutTheCommandsIsNoSupportNotAnError() throws {
        try reply("cli-error-usage.json", exit: 2)
        let e = try #require(cli.claudeShow().failure)
        #expect(SetupSupport.unsupported(e))
        #expect(!SetupSupport.unsupported(.app(AppError(code: "invalid_value", message: "unknown profile"))))
        #expect(SetupSupport.unsupported(.cli(.tooOld(version: "0.8.19"))))
    }

    @Test func testWriteSendsTheTextOnStdinWithTheHashItWasOpenedAt() throws {
        try reply("cli-claude-write.json")
        let sha = EditorBuffer.hash(Data("old".utf8))
        let r = try cli.claudeWrite(.instructions, profile: "review", ifUnchanged: sha, text: "# New\n- it's “fine”\n").get()
        #expect(r.sharedWith == ["review"] && r.bytes == 312)
        #expect(last == ["claude", "write", "CLAUDE.md", "--profile=review", "--if-unchanged=" + sha, "--json"])
        #expect(stdin == "# New\n- it's “fine”\n", "the bytes typed, unchanged")
    }

    @Test func testWriteShowsTheCLIsRefusals() throws {
        try reply("cli-error-settings-json.json", exit: 1)
        let sha = EditorBuffer.hash(Data())
        let e = try #require(cli.claudeWrite(.settings, profile: nil, ifUnchanged: sha, text: "{,}").failure)
        #expect(e.code == "invalid_value" && e.message.contains("(line 8, column 1)"))
        #expect(!last.contains { $0.hasPrefix("--profile") }, "default: no --profile")
        try reply("cli-error-changed-since-opened.json", exit: 1)
        let c = try #require(cli.claudeWrite(.instructions, profile: nil, ifUnchanged: sha, text: "x").failure)
        #expect(c.code == "config_changed" && c.hint == "reopen it to see the change")
    }

    @Test func testWriteRefusesBeforeRunningWhatCannotBeSent() throws {
        let sha = EditorBuffer.hash(Data())
        #expect(cli.claudeWrite(.skills, profile: nil, ifUnchanged: sha, text: "").failureCode == "usage")
        #expect(cli.claudeWrite(.instructions, profile: nil, ifUnchanged: "abc", text: "").failureCode == "usage")
        #expect(cli.claudeWrite(.instructions, profile: "--yes", ifUnchanged: sha, text: "").failureCode == "usage")
        let big = String(repeating: "a", count: EditorBuffer.limit + 1)
        #expect(cli.claudeWrite(.instructions, profile: nil, ifUnchanged: sha, text: big).failureCode == "invalid_value")
        #expect(FakeAppCLI.calls(dir).isEmpty, "nothing ran")
    }

    @Test func testAPluginSwitchRunsClaudesPluginCommand() throws {
        try reply("cli-claude-plugin.json")
        let t = try cli.claudePlugin("slack@claude-plugins-official", enable: true, profile: nil).get()
        #expect(t.enabled && t.profile == "default")
        #expect(last == ["claude", "plugin", "enable", "slack@claude-plugins-official", "--json"])
        _ = cli.claudePlugin("slack@claude-plugins-official", enable: false, profile: "work")
        #expect(last == ["claude", "plugin", "disable", "slack@claude-plugins-official", "--profile=work", "--json"])
        #expect(cli.claudePlugin("--all", enable: true, profile: nil).failureCode == "usage")
    }

    @Test func testShareAndOwnNameTheProfileAndItem() throws {
        try reply("cli-profile-share.json")
        let s = try cli.profileShare("review", .settings).get()
        #expect(s.state == .shared && s.backup?.hasSuffix(".claudeswitch-20261008-024641") == true)
        #expect(last == ["profile", "share", "review", "settings.json", "--json"])
        try reply("cli-profile-own.json")
        let o = try cli.profileOwn("review", .skills).get()
        #expect(o.state == .own && o.backup == nil)
        #expect(last == ["profile", "own", "review", "skills", "--json"])
        #expect(cli.profileOwn("-x", .skills).failureCode == "usage")
    }
}

@Suite struct RunnerInputTests {
    @Test func testInputReachesTheProgramsStdin() {
        let r = ProcessRunner().run("/bin/cat", [], input: Data("one\ntwo\n".utf8), timeout: 10)
        #expect(r.exitCode == 0)
        #expect(r.stdoutText == "one\ntwo\n")
    }

    @Test func testMoreThanAPipeHoldsStillArrives() {
        let big = Data(String(repeating: "0123456789abcdef", count: 40_000).utf8) // 640 KB
        let r = ProcessRunner().run("/usr/bin/wc", ["-c"], input: big, timeout: 20)
        #expect(r.stdoutText.trimmingCharacters(in: .whitespacesAndNewlines) == "\(big.count)")
    }

    @Test func testNoInputIsAnEmptyStdin() {
        let r = ProcessRunner().run("/bin/cat", [], timeout: 10)
        #expect(r.exitCode == 0 && r.stdout.isEmpty)
    }
}
