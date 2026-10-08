import Foundation

/// Where claudeswitch keeps its files, from a home directory (injectable so
/// tests never see the real one).
public struct Paths {
    public var home: String
    public init(home: String = NSHomeDirectory()) { self.home = home }

    public var stateDir: String { home + "/.local/state/claudeswitch" }
    public var stateFile: String { stateDir + "/state.json" }
}

/// Finds the claudeswitch binary: the path chosen in the app when there is
/// one, then ~/.local/bin (where install.sh puts it), then PATH.
///
/// An app started from Finder or at login gets launchd's PATH
/// (/usr/bin:/bin:/usr/sbin:/sbin), not the shell's, so the usual install
/// directories are searched after it as well.
public struct BinaryLocator {
    public var home: String
    public var pathEnv: String
    public var configured: String?
    public var isExecutable: (String) -> Bool

    public init(home: String = NSHomeDirectory(),
                pathEnv: String = ProcessInfo.processInfo.environment["PATH"] ?? "",
                configured: String? = nil,
                isExecutable: @escaping (String) -> Bool = { FileManager.default.isExecutableFile(atPath: $0) }) {
        self.home = home
        self.pathEnv = pathEnv
        self.configured = configured
        self.isExecutable = isExecutable
    }

    public static let name = "claudeswitch"

    public var candidates: [String] {
        var out: [String] = []
        if let c = configured?.trimmingCharacters(in: .whitespaces), !c.isEmpty {
            out.append((c as NSString).expandingTildeInPath)
        }
        let local = home + "/.local/bin/" + Self.name
        if !out.contains(local) { out.append(local) }
        var dirs = pathEnv.split(separator: ":").map(String.init)
        dirs += ["/opt/homebrew/bin", "/usr/local/bin", home + "/go/bin", home + "/bin"]
        for d in dirs where !d.isEmpty {
            let p = (d as NSString).expandingTildeInPath + "/" + Self.name
            if !out.contains(p) { out.append(p) }
        }
        return out
    }

    public func locate() -> String? { candidates.first(where: isExecutable) }
}

public struct CommandResult: Equatable {
    public var exitCode: Int32
    public var stdout: Data
    public var stderr: Data
    public var timedOut: Bool

    public init(exitCode: Int32, stdout: Data, stderr: Data, timedOut: Bool = false) {
        self.exitCode = exitCode
        self.stdout = stdout
        self.stderr = stderr
        self.timedOut = timedOut
    }

    public var stdoutText: String { String(decoding: stdout, as: UTF8.self) }
    public var stderrText: String { String(decoding: stderr, as: UTF8.self) }
}

public protocol CommandRunning {
    func run(_ executable: String, _ args: [String], timeout: TimeInterval) -> CommandResult
}

/// Runs a program and collects its output. Blocking: call it off the main
/// thread.
public struct ProcessRunner: CommandRunning {
    public var environment: [String: String]

    public init(environment: [String: String] = ProcessRunner.defaultEnvironment()) {
        self.environment = environment
    }

    /// The app's environment, minus CLAUDE_CONFIG_DIR: without it the binary
    /// acts on the default Claude Code profile, which is the one this app
    /// shows. No colour codes in output meant for a dialog.
    public static func defaultEnvironment() -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        env.removeValue(forKey: "CLAUDE_CONFIG_DIR")
        env["NO_COLOR"] = "1"
        return env
    }

    public func run(_ executable: String, _ args: [String], timeout: TimeInterval) -> CommandResult {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: executable)
        p.arguments = args
        p.environment = environment
        p.standardInput = FileHandle.nullDevice
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err

        let done = DispatchSemaphore(value: 0)
        p.terminationHandler = { _ in done.signal() }
        do {
            try p.run()
        } catch {
            return CommandResult(exitCode: 127, stdout: Data(),
                                 stderr: Data("could not run \(executable): \(error.localizedDescription)".utf8))
        }
        // Drain both pipes concurrently, or a chatty child fills one and blocks.
        // Dedicated threads rather than a dispatch queue: callers block while
        // they wait, and a pool of blocked workers must not be what the
        // drains are queued behind.
        let outDrain = Drain(out.fileHandleForReading), errDrain = Drain(err.fileHandleForReading)

        var timedOut = false
        if done.wait(timeout: .now() + timeout) == .timedOut {
            timedOut = true
            p.terminate()
            if done.wait(timeout: .now() + 2) == .timedOut {
                kill(p.processIdentifier, SIGKILL)
                done.wait()
            }
        }
        return CommandResult(exitCode: p.terminationStatus, stdout: outDrain.wait(), stderr: errDrain.wait(),
                             timedOut: timedOut)
    }
}

/// Reads a pipe to its end on a thread of its own.
private final class Drain {
    private var data = Data()
    private let done = DispatchSemaphore(value: 0)

    init(_ handle: FileHandle) {
        let t = Thread { [self] in
            data = handle.readDataToEndOfFile()
            done.signal()
        }
        t.start()
    }

    func wait() -> Data {
        done.wait()
        return data
    }
}

public enum CLIError: Error, Equatable {
    case missing
    case tooOld(version: String)
    /// A newer CLI whose app contract this app does not speak.
    case unsupportedContract(version: String, contract: Int)
    case failed(String)
    case unreadable(String)

    public var message: String {
        switch self {
        case .missing:
            return "claudeswitch is not installed, or not where this app looks."
        case .tooOld(let v):
            return "claudeswitch \(v) is too old for this app: it needs \(CLI.minimumVersion) or later, "
                + "speaking the app's contract \(CLI.requiredContract). Update claudeswitch."
        case .unsupportedContract(let v, let c):
            return "claudeswitch \(v) speaks the app's contract \(c); this app speaks "
                + "\(CLI.supportedContracts.lowerBound). Update ClaudeSwitch (./install-app.sh) to match it."
        case .failed(let s): return s
        case .unreadable(let s): return s
        }
    }
}

/// The claudeswitch binary, driven through its keychain-free commands.
///
/// `status --json` is deliberately not used: it reads every account's vault
/// item from the keychain (vault.Has / PlanOf / DescribeOf), and by default
/// refreshes stale readings through the usage API. `why --json` and
/// `config --json` read only config.toml and state.json.
public struct CLI {
    /// The JSON CLI the app acts through (docs/APP_CLI.md, lane 12) shipped
    /// after 0.5.0.
    public static let minimumVersion = "0.5.1"
    /// The app contract this app is built against (docs/APP_CLI.md): 2 is
    /// lane 16's (scope removed, `add --from`, `best`; with lane 15's
    /// `listed`, `account list` and `profile remove`). A version string
    /// cannot tell a dev build from before them from a current one, so
    /// `version --json` is asked, and a binary without the contract is too
    /// old (B8).
    public static let requiredContract = 2
    /// The contracts this app speaks: a newer one may have changed what it
    /// reads, so it is refused too (review), naming the app as what to update.
    public static let supportedContracts = 2...2
    public static let installSteps = """
        Install or update it from a source checkout:
          git clone https://github.com/bogdan-alexandrescu/claudeswitch
          cd claudeswitch && ./install.sh
        or download a release binary into ~/.local/bin/claudeswitch.
        If it lives elsewhere, choose it in Settings → Advanced → Choose….
        """

    public var path: String
    public var runner: CommandRunning

    public init(path: String, runner: CommandRunning = ProcessRunner()) {
        self.path = path
        self.runner = runner
    }

    /// "0.4.8", "dev", or nil when the output was not recognised.
    public func version() -> String? {
        let r = runner.run(path, ["version"], timeout: 10)
        guard r.exitCode == 0 else { return nil }
        let words = r.stdoutText.split(whereSeparator: { $0 == " " || $0 == "\n" })
        guard words.count >= 2, words[0] == "claudeswitch" else { return nil }
        return String(words[1])
    }

    /// Whether a version string is new enough. "dev" and anything
    /// unparseable are a source build, and are given the benefit of the doubt:
    /// a failure to decode `why --json` reports the problem anyway.
    public static func supports(version v: String) -> Bool {
        let parts = v.trimmingCharacters(in: CharacterSet(charactersIn: "v"))
            .split(separator: "-")[0].split(separator: ".").map { Int($0) }
        guard parts.count >= 2, !parts.contains(where: { $0 == nil }) else { return true }
        let have = parts.map { $0! } + [0, 0, 0]
        let need = minimumVersion.split(separator: ".").map { Int($0)! }
        for i in 0..<3 where have[i] != need[i] { return have[i] > need[i] }
        return true
    }

    /// The app contract `version --json` names; nil from a binary that
    /// predates it (its plain version line is not JSON).
    public func contract() -> Int? {
        let r = runner.run(path, ["version", "--json"], timeout: 10)
        guard r.exitCode == 0, let j = JSON(data: r.stdout), let n = j["contract"].double else { return nil }
        return Int(n)
    }

    /// The version, when it is new enough and speaks the app's contract.
    public func checkVersion() -> Result<String, CLIError> {
        guard let v = version() else {
            return .failure(.failed("`\(path) version` did not answer as claudeswitch does."))
        }
        guard Self.supports(version: v), let c = contract(), c >= Self.requiredContract else {
            return .failure(.tooOld(version: v))
        }
        guard Self.supportedContracts.contains(c) else {
            return .failure(.unsupportedContract(version: v, contract: c))
        }
        return .success(v)
    }

    public func why() -> Result<WhyReport, CLIError> {
        let r = runner.run(path, ["why", "--json"], timeout: 20)
        if let e = commandError(r, "why --json") { return .failure(e) }
        guard let w = WhyReport(data: r.stdout) else {
            return .failure(.unreadable("`claudeswitch why --json` printed something this app cannot read."))
        }
        return .success(w)
    }

    public func settings() -> Result<ConfigSettings, CLIError> {
        let r = runner.run(path, ["config", "--json"], timeout: 20)
        if let e = commandError(r, "config --json") { return .failure(e) }
        guard let s = ConfigSettings(data: r.stdout) else {
            return .failure(.unreadable("`claudeswitch config --json` printed something this app cannot read."))
        }
        return .success(s)
    }

    /// Account ids are names from the config; anything else is refused before
    /// it reaches a command line, so an id can never be read as a flag.
    public static func validID(_ id: String) -> Bool {
        guard let first = id.unicodeScalars.first, first != "-" else { return false }
        let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "._-@"))
        return id.unicodeScalars.allSatisfy { allowed.contains($0) }
    }

    func commandError(_ r: CommandResult, _ what: String) -> CLIError? {
        if r.timedOut { return .failed("`claudeswitch \(what)` did not finish in time.") }
        guard r.exitCode != 0 else { return nil }
        let err = r.stderrText
        if err.contains("flag provided but not defined") || r.exitCode == 2 && err.contains("usage") {
            return .tooOld(version: version() ?? "this version")
        }
        return .failed(Self.tidy(err, fallback: "`claudeswitch \(what)` failed (exit \(r.exitCode))."))
    }

    /// Output for a person: blank lines and the "claudeswitch: " prefix gone,
    /// leading glyphs kept.
    public static func tidy(_ s: String, fallback: String) -> String {
        let lines = s.split(separator: "\n").map { line -> String in
            var l = line.trimmingCharacters(in: .whitespaces)
            if l.hasPrefix("claudeswitch: ") { l.removeFirst("claudeswitch: ".count) }
            return l
        }.filter { !$0.isEmpty }
        return lines.isEmpty ? fallback : lines.joined(separator: "\n")
    }
}

/// The version check, remembered per binary. Keyed by path and modification
/// time, so a binary upgraded in place is checked again; a failure is not
/// remembered, so it is retried.
public struct VersionCache {
    private var key: String?
    private var version: String?

    public init() {}

    public mutating func check(_ cli: CLI) -> Result<String, CLIError> {
        let k = Self.key(cli.path)
        if let k, k == key, let v = version { return .success(v) }
        let r = cli.checkVersion()
        if case .success(let v) = r, let k {
            key = k
            version = v
        } else {
            key = nil
            version = nil
        }
        return r
    }

    static func key(_ path: String) -> String? {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: path),
              let m = attrs[.modificationDate] as? Date else { return nil }
        return path + "@" + String(m.timeIntervalSince1970)
    }
}
