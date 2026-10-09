import Foundation

// F9: the first-run setup window. On launch with no config the app offers
// what `cs setup` does, through the CLI's JSON forms: save the login Claude
// Code is using (`add --json`), add more accounts (the Add account sheet),
// optionally create a work profile (`profile create --json`), and install
// the daemon in dry run (`daemon install --dry-run --json`). Every step can
// be skipped, and the window can be reopened from the ⋯ menu.

public enum SetupStep: Int, CaseIterable, Identifiable {
    case saveLogin, addAccounts, workProfile, daemon

    public var id: Int { rawValue }

    public var title: String {
        switch self {
        case .saveLogin: return "Save the login you use now"
        case .addAccounts: return "Add your other accounts"
        case .workProfile: return "Create a work profile"
        case .daemon: return "Install the daemon in dry run"
        }
    }

    public var detail: String {
        switch self {
        case .saveLogin:
            return "Claude Code is signed in to an account already. Saving it vaults that credential under a "
                + "name you choose; your session is not touched."
        case .addAccounts:
            return "Sign in to each other Claude account with your browser, or save another profile's login. "
                + "Rotation moves between the accounts you add."
        case .workProfile:
            return "Optional. A second Claude Code directory with its own pool of accounts, for example one "
                + "for work and one for personal use."
        case .daemon:
            return "The background service that reads each account's usage. In dry run it decides but never "
                + "switches; turn it live in Settings → Daemon when you trust it."
        }
    }

    /// The step's button.
    public var action: String {
        switch self {
        case .saveLogin: return "Save current login…"
        case .addAccounts: return "Add account…"
        case .workProfile: return "New profile…"
        case .daemon: return "Install (dry run)"
        }
    }
}

public enum StepStatus: Equatable { case pending, done, skipped }

/// What is already true, read from the CLI: what makes a step done without
/// the window having done it.
public struct SetupFacts: Equatable {
    /// Accounts in the config.
    public var accounts: Int
    /// Declared profiles other than default.
    public var profiles: Int
    public var daemonInstalled: Bool

    public init(accounts: Int = 0, profiles: Int = 0, daemonInstalled: Bool = false) {
        self.accounts = accounts
        self.profiles = profiles
        self.daemonInstalled = daemonInstalled
    }
}

/// The window's progress: a step is done when what it does is true (or the
/// person said so), skipped when they skipped it, else pending. The current
/// step is the first pending one.
public struct SetupFlow: Equatable {
    public var facts = SetupFacts()
    private var marks: [SetupStep: StepStatus] = [:]

    public init(facts: SetupFacts = SetupFacts()) { self.facts = facts }

    public func status(_ s: SetupStep) -> StepStatus {
        if isTrue(s) { return .done }
        return marks[s] ?? .pending
    }

    /// Adding more accounts is never assumed finished: only Continue ends it.
    func isTrue(_ s: SetupStep) -> Bool {
        switch s {
        case .saveLogin: return facts.accounts > 0
        case .addAccounts: return false
        case .workProfile: return facts.profiles > 0
        case .daemon: return facts.daemonInstalled
        }
    }

    public var current: SetupStep? { SetupStep.allCases.first { status($0) == .pending } }
    public var finished: Bool { current == nil }

    public mutating func skip(_ s: SetupStep) { marks[s] = .skipped }
    public mutating func complete(_ s: SetupStep) { marks[s] = .done }
    /// Back to a skipped or finished step.
    public mutating func reopen(_ s: SetupStep) { marks[s] = nil }
}

public enum FirstRun {
    /// Where the CLI keeps its config when it is not told otherwise
    /// (config.DefaultPath).
    public static func defaultConfigPath(home: String = NSHomeDirectory()) -> String {
        home + "/.config/claudeswitch/config.toml"
    }

    /// True when there is no config: at `configPath` (what `config --json`
    /// reports), else the default path.
    public static func shouldOffer(configPath: String?, home: String = NSHomeDirectory(),
                                   exists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }) -> Bool {
        !exists(configPath ?? defaultConfigPath(home: home))
    }

    /// An empty config: comments only, so every setting keeps its default.
    /// `add --json` appends each account's block to the config but does not
    /// create one, and the CLI has no JSON form of `setup` or `init` (whose
    /// template names example accounts), so the window writes this first.
    public static let seed = """
        # claudeswitch, started by the ClaudeSwitch app's setup window.
        # Accounts are added below as you save or sign in to them.
        # `cs config` lists every setting and its default; `cs doctor` checks it all.

        """

    /// Writes `seed` at `path` (mode 0600, its directory 0700) when nothing
    /// is there. True when it wrote; false when a config already exists.
    public static func seedConfig(at path: String) -> Result<Bool, AppError> {
        let fm = FileManager.default
        if fm.fileExists(atPath: path) { return .success(false) }
        let dir = (path as NSString).deletingLastPathComponent
        do {
            if !fm.fileExists(atPath: dir) {
                try fm.createDirectory(atPath: dir, withIntermediateDirectories: true,
                                       attributes: [.posixPermissions: 0o700])
            }
            guard fm.createFile(atPath: path, contents: Data(seed.utf8), attributes: [.posixPermissions: 0o600]) else {
                return .failure(AppError(code: "failed", message: "Could not write \(path)."))
            }
            return .success(true)
        } catch {
            return .failure(AppError(code: "failed", message: "Could not create \(dir): \(error.localizedDescription)"))
        }
    }
}
