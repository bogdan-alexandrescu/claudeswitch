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

    /// The window's "Don't show this again" (0.6.1), in UserDefaults.
    public static let dontShowAgainKey = "firstRunDontShowAgain"

    public static func dontShowAgain(_ defaults: UserDefaults = .standard) -> Bool {
        defaults.bool(forKey: dontShowAgainKey)
    }

    public static func setDontShowAgain(_ on: Bool, _ defaults: UserDefaults = .standard) {
        defaults.set(on, forKey: dontShowAgainKey)
    }

    /// Whether the window opens by itself at launch: there is no config and
    /// the person has not asked it not to. ⋯ → Set up… opens it regardless.
    public static func offerAtLaunch(configPath: String?, defaults: UserDefaults = .standard,
                                     home: String = NSHomeDirectory(),
                                     exists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }) -> Bool {
        !dontShowAgain(defaults) && shouldOffer(configPath: configPath, home: home, exists: exists)
    }

    /// What the window says when the binary predates `init --empty` (0.6.1).
    public static let initTooOld = "This claudeswitch cannot start an empty config (`cs init --empty`). "
        + "Update it, or run `cs setup` in Terminal, then open this window again."

    /// Makes sure there is a config at `path` before `add --json`, which
    /// appends to one but does not create it. The CLI writes it
    /// (`init --empty --json`): the app never writes claudeswitch's files.
    /// True when it was written now; false when one was already there.
    /// Blocks: call it off the main thread.
    public static func ensureConfig(path: String, cli: CLI,
                                    exists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) })
        -> Result<Bool, CallError> {
        if exists(path) { return .success(false) }
        switch cli.initEmpty(configPath: path) {
        case .success: return .success(true)
        case .failure(let e) where e.code == "exists": return .success(false)
        case .failure(.cli(.tooOld)): return .failure(.refused("too_old", initTooOld, hint: CLI.installSteps))
        case .failure(let e): return .failure(e)
        }
    }
}
