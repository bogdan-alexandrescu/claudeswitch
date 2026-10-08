import Foundation
import Testing
@testable import ClaudeSwitchCore

@Suite struct LocatorTests {
    @Test func testOrder() {
        let l = BinaryLocator(home: "/h", pathEnv: "/a/bin::/b/bin:/h/.local/bin",
                              configured: "~/custom/claudeswitch", isExecutable: { _ in false })
        #expect(l.candidates == [
            NSString(string: "~/custom/claudeswitch").expandingTildeInPath,
            "/h/.local/bin/claudeswitch",
            "/a/bin/claudeswitch", "/b/bin/claudeswitch",
            "/opt/homebrew/bin/claudeswitch", "/usr/local/bin/claudeswitch",
            "/h/go/bin/claudeswitch", "/h/bin/claudeswitch",
        ])
    }

    @Test func testFirstExecutableWins() {
        var l = BinaryLocator(home: "/h", pathEnv: "/a/bin", configured: "/x/claudeswitch",
                              isExecutable: { $0 == "/a/bin/claudeswitch" || $0 == "/x/claudeswitch" })
        #expect(l.locate() == "/x/claudeswitch", "the chosen path wins when it is there")
        l.isExecutable = { $0 == "/h/.local/bin/claudeswitch" || $0 == "/a/bin/claudeswitch" }
        #expect(l.locate() == "/h/.local/bin/claudeswitch", "a chosen path that is gone falls back")
        l.isExecutable = { $0 == "/a/bin/claudeswitch" }
        #expect(l.locate() == "/a/bin/claudeswitch")
        l.configured = "  "
        l.isExecutable = { _ in true }
        #expect(l.locate() == "/h/.local/bin/claudeswitch", "a blank setting is no setting")
        l.isExecutable = { _ in false }
        #expect(l.locate() == nil)
    }

    @Test func testFindsARealExecutable() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let bin = try FakeBinary.write(in: dir, script: "echo hi")
        let l = BinaryLocator(home: "/nonexistent", pathEnv: dir.path)
        #expect(l.locate() == bin)
        // Present but not executable is not found.
        try FileManager.default.setAttributes([.posixPermissions: 0o644], ofItemAtPath: bin)
        #expect(BinaryLocator(home: "/nonexistent", pathEnv: dir.path).locate() == nil)
    }
}

/// A shell script standing in for claudeswitch. It answers from the Go-made
/// fixtures and records its arguments, so nothing real ever runs.
enum FakeBinary {
    static func directory() throws -> URL {
        let d = FileManager.default.temporaryDirectory
            .appendingPathComponent("claudeswitch-bar-tests-" + UUID().uuidString.prefix(8))
        try FileManager.default.createDirectory(at: d, withIntermediateDirectories: true)
        return d
    }

    @discardableResult
    static func write(in dir: URL, script: String) throws -> String {
        let p = dir.appendingPathComponent("claudeswitch").path
        try ("#!/bin/sh\n" + script + "\n").write(toFile: p, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: p)
        return p
    }

    /// A binary that behaves like the current one.
    static func current(in dir: URL) throws -> String {
        for name in ["why.json", "config.json"] {
            try fixture(name).write(to: dir.appendingPathComponent(name))
        }
        return try write(in: dir, script: """
            D="$(dirname "$0")"
            echo "$@" >> "$D/args.log"
            case "$1 $2" in
              "version --json") echo '{"version": "0.5.1", "contract": 2}' ;;
              "version "*) echo "claudeswitch 0.5.1" ;;
              "why "*) cat "$D/why.json" ;;
              "config "*) cat "$D/config.json" ;;
              *) echo "usage" >&2; exit 2 ;;
            esac
            """)
    }
}

/// Each test gets its own directory (swift-testing makes a fresh profile per
/// test), removed when the profile goes.
@Suite final class CLITests {
    let dir: URL

    init() throws { dir = try FakeBinary.directory() }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func args() -> [String] {
        ((try? String(contentsOf: dir.appendingPathComponent("args.log"))) ?? "")
            .split(separator: "\n").map(String.init)
    }

    @Test func testReadsWhyAndConfigThroughTheBinary() throws {
        let cli = CLI(path: try FakeBinary.current(in: dir))
        #expect(try cli.checkVersion().get() == "0.5.1")
        #expect(try cli.why().get().primary?.decision?.kind == "stay")
        #expect(try cli.settings().get().switchAt == 90)
        #expect(args() == ["version", "version --json", "why --json", "config --json"], "only keychain-free commands are run to read")
    }

    @Test func testIDsThatCouldBeFlagsAreNotIDs() {
        #expect(!CLI.validID("--dry-run"))
        #expect(!CLI.validID("a b"))
        #expect(!CLI.validID(""))
        #expect(CLI.validID("work-1"))
        #expect(CLI.validID("team.alpha_2"))
    }

    @Test func testAnOldBinaryIsReportedAsTooOld() throws {
        let p = try FakeBinary.write(in: dir, script: """
            case "$1" in
              version) echo "claudeswitch 0.3.2" ;;
              *) echo "flag provided but not defined: -json" >&2; exit 2 ;;
            esac
            """)
        let cli = CLI(path: p)
        #expect(cli.checkVersion() == .failure(.tooOld(version: "0.3.2")))
        #expect(cli.why().map { _ in "" } == .failure(.tooOld(version: "0.3.2")))
    }

    @Test func testSomethingElseCalledClaudeswitch() throws {
        let p = try FakeBinary.write(in: dir, script: "echo 'hello'")
        guard case .failure(.failed) = CLI(path: p).checkVersion() else { Issue.record("unexpected result"); return }
        guard case .failure(.unreadable) = CLI(path: p).why() else { Issue.record("unexpected result"); return }
    }

    @Test func testAFailingCommandSaysWhy() throws {
        let p = try FakeBinary.write(in: dir, script: """
            [ "$1" = version ] && { echo "claudeswitch dev"; exit 0; }
            echo "claudeswitch: config.toml: bad things" >&2; exit 1
            """)
        #expect(CLI(path: p).why().map { _ in "" } == .failure(.failed("config.toml: bad things")))
    }

    @Test func testVersions() {
        #expect(CLI.supports(version: "0.5.1"))
        #expect(CLI.supports(version: "v0.5.1"))
        #expect(CLI.supports(version: "1.0"))
        #expect(CLI.supports(version: "0.10.0"))
        #expect(CLI.supports(version: "dev"))
        #expect(CLI.supports(version: "0.5.1-rc1"))
        #expect(!(CLI.supports(version: "0.5.0")))
        #expect(!(CLI.supports(version: "0.3.9")))
        #expect(!(CLI.supports(version: "v0.1")))
    }

    @Test func testTidy() {
        #expect(CLI.tidy("\n\n", fallback: "f") == "f")
        #expect(CLI.tidy("claudeswitch: boom\n  detail  \n", fallback: "f") == "boom\ndetail")
    }
}

@Suite struct RunnerTests {
    @Test func testCapturesBothStreamsAndTheStatus() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: "echo out; echo err >&2; exit 3")
        let r = ProcessRunner().run(p, [], timeout: 10)
        #expect(r.exitCode == 3)
        #expect(r.stdoutText == "out\n")
        #expect(r.stderrText == "err\n")
        #expect(!(r.timedOut))
    }

    @Test func testLargeOutputDoesNotDeadlock() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: "head -c 300000 /dev/zero; head -c 300000 /dev/zero >&2")
        let r = ProcessRunner().run(p, [], timeout: 10)
        #expect(r.stdout.count == 300_000)
        #expect(r.stderr.count == 300_000)
    }

    @Test func testTimesOut() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: "exec sleep 30")
        let start = Date()
        let r = ProcessRunner().run(p, [], timeout: 0.5)
        #expect(r.timedOut)
        #expect(Date().timeIntervalSince(start) < 10)
    }

    @Test func testAMissingProgram() {
        let r = ProcessRunner().run("/nonexistent/claudeswitch", [], timeout: 1)
        #expect(r.exitCode == 127)
    }

    /// The binary acts on the default profile, which is what the app shows.
    @Test func testTheEnvironmentDropsClaudeConfigDir() throws {
        let dir = try FakeBinary.directory()
        defer { try? FileManager.default.removeItem(at: dir) }
        let p = try FakeBinary.write(in: dir, script: #"echo "[${CLAUDE_CONFIG_DIR-unset}] [$NO_COLOR]""#)
        var env = ProcessRunner.defaultEnvironment()
        #expect(env["CLAUDE_CONFIG_DIR"] == nil)
        env["CLAUDE_CONFIG_DIR"] = "/elsewhere"
        #expect(ProcessRunner(environment: env).run(p, [], timeout: 5).stdoutText == "[/elsewhere] [1]\n")
        #expect(ProcessRunner().run(p, [], timeout: 5).stdoutText == "[unset] [1]\n")
    }
}

/// The version check is cached per binary, keyed by path and modification
/// time, so replacing the binary in place is noticed.
@Suite final class VersionCacheTests {
    let dir: URL

    init() throws { dir = try FakeBinary.directory() }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func calls() -> Int {
        ((try? String(contentsOf: dir.appendingPathComponent("args.log"))) ?? "")
            .split(separator: "\n").count
    }

    @Test func testChecksOncePerBinary() throws {
        let p = try FakeBinary.current(in: dir)
        var cache = VersionCache()
        #expect(try cache.check(CLI(path: p)).get() == "0.5.1")
        #expect(try cache.check(CLI(path: p)).get() == "0.5.1")
        #expect(calls() == 2, "version and the contract once; the second check came from the cache")

        // Replaced in place (an upgrade): checked again.
        try FakeBinary.write(in: dir, script: """
            echo version >> "$(dirname "$0")/args.log"; echo "claudeswitch 0.3.0"
            """)
        try FileManager.default.setAttributes([.modificationDate: Date().addingTimeInterval(60)],
                                              ofItemAtPath: p)
        #expect(cache.check(CLI(path: p)) == .failure(.tooOld(version: "0.3.0")))
        #expect(calls() == 3)
    }

    @Test func testAFailureIsNotCached() throws {
        let p = try FakeBinary.write(in: dir, script: """
            echo version >> "$(dirname "$0")/args.log"; echo nonsense
            """)
        var cache = VersionCache()
        _ = cache.check(CLI(path: p))
        _ = cache.check(CLI(path: p))
        #expect(calls() == 2)
    }
}

@Suite struct PathsTests {
    @Test func testPathsAreUnderTheGivenHome() {
        let p = Paths(home: "/h")
        #expect(p.stateFile == "/h/.local/state/claudeswitch/state.json")
    }
}
