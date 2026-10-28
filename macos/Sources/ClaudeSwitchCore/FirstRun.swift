import Foundation

// F9: the first-run setup window. On launch with no config the app offers
// what `cs setup` does, through the CLI's JSON forms: save the login Claude
// Code is using (`add --json`), add more accounts (the Add account sheet),
// optionally create a work profile (`profile create --json`), and install
// the daemon in dry run (`daemon install --dry-run --json`, once `daemon
// status` has said it is not installed). Every step can be skipped, and the
// window can be reopened from the menu-bar icon's own menu (a right-click;
// the popover's ⋯ menu is gone since 2026-10-26).

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
            // On a first run nothing says Claude Code is signed in at all.
            // "Session" is the 5-hour window everywhere else in the app.
            return "If Claude Code is signed in already, save that login under a name you choose. "
                + "The login Claude Code is using now is not touched."
        case .addAccounts:
            return "Sign in to each other Claude account with your browser, or save another profile’s login. "
                + "Rotation moves between the accounts you add."
        case .workProfile:
            return "Optional. A second Claude Code directory with its own pool of accounts, for example one "
                + "for work and one for personal use."
        case .daemon:
            return "The background service that reads each account’s usage. In dry run it decides but never "
                + "switches; turn it live in Settings → Daemon when you trust it."
        }
    }

    /// The step's button.
    public var action: String {
        switch self {
        case .saveLogin: return "Save Current Login…"
        case .addAccounts: return "Add Account…"
        case .workProfile: return "New Profile…"
        case .daemon: return "Install (Dry Run)"
        }
    }

    /// The link on a step that is done or skipped, which opens it again.
    public func reopen(_ status: StepStatus) -> String {
        if status == .skipped { return "Do It Now" }
        switch self {
        case .addAccounts, .workProfile: return "Add Another"
        case .saveLogin, .daemon: return "Open"
        }
    }

    /// What that link does. A skipped step, and step 1, open again in the
    /// window; Add another runs the step's sheet and leaves it done; step
    /// 4's Open is Settings → Daemon, never Install over the service.
    public func reopenAction(_ status: StepStatus) -> ReopenAction {
        if status == .skipped { return .reopen }
        switch self {
        case .addAccounts, .workProfile: return .run
        case .saveLogin: return .reopen
        case .daemon: return .daemonSettings
        }
    }
}

public enum StepStatus: Equatable { case pending, done, skipped }

/// What the link on a done or skipped step does (`SetupStep.reopenAction`).
public enum ReopenAction: Equatable {
    /// The step opens again and is the current step (`SetupFlow.reopen`).
    case reopen
    /// The step's button runs again (its sheet); the step stays done.
    case run
    /// Settings → Daemon.
    case daemonSettings
}

/// Step 4's offer while it is open (`SetupFlow.daemonOffer`).
public enum DaemonOffer: Equatable {
    /// The CLI said the service is not installed: Install (dry run).
    case install
    /// It is installed: Settings → Daemon, where it turns live or goes.
    case settings
    /// `daemon status` has not answered yet.
    case reading
    /// It failed: the failure, with Try Again.
    case readFailed(CallError)
}

/// What is already true, read from the CLI: what makes a step done without
/// the window having done it.
public struct SetupFacts: Equatable {
    /// Accounts in the config.
    public var accounts: Int
    /// Declared profiles other than default.
    public var profiles: Int
    /// Whether the daemon service is installed, once the CLI has said; nil
    /// while `daemon status` has not answered, or failed.
    public var daemonInstalled: Bool?
    /// The saved account the login Claude Code uses now is, when the CLI
    /// knows it; nil does not mean it is not saved.
    public var currentLogin: String?

    public init(accounts: Int = 0, profiles: Int = 0, daemonInstalled: Bool? = nil, currentLogin: String? = nil) {
        self.accounts = accounts
        self.profiles = profiles
        self.daemonInstalled = daemonInstalled
        self.currentLogin = currentLogin
    }

    /// What `daemon status` said: false only when the CLI said the service
    /// is not installed (`missing`, its not_installed refusal, or a status
    /// saying so); nil when it has not answered or failed.
    public static func daemonInstalled(status: DaemonStatus?, missing: Bool) -> Bool? {
        missing ? false : status?.installed
    }

    /// Default's live account, signed in, when the config has it: the
    /// login in use is that saved account. `add` does not record the live
    /// account, so this is often unknown until the daemon has polled.
    public static func currentLogin(profiles: ProfileList?, accounts: [String]) -> String? {
        guard let d = profiles?.profile(StateFile.defaultProfile), d.signedIn == "yes",
              let live = d.live, accounts.contains(live) else { return nil }
        return live
    }
}

/// The window's progress: a step is done when what it does is true (or the
/// person said so), skipped when they skipped it, else pending. A step
/// opened again while what it does was true (Open) stays pending until it
/// is completed or skipped; one opened while it was not (Do it now) is done
/// once it is. The current step is the open one opened last, else the
/// first pending one.
public struct SetupFlow: Equatable {
    public var facts = SetupFacts()
    private var marks: [SetupStep: StepStatus] = [:]
    /// Steps opened again, the last one opened last, each with whether what
    /// it does was true then.
    private var reopened: [Reopened] = []

    struct Reopened: Equatable {
        var step: SetupStep
        var wasTrue: Bool
    }

    public init(facts: SetupFacts = SetupFacts()) { self.facts = facts }

    public func status(_ s: SetupStep) -> StepStatus {
        if let r = reopened.first(where: { $0.step == s }), r.wasTrue || !isTrue(s) { return .pending }
        if isTrue(s) { return .done }
        return marks[s] ?? .pending
    }

    /// Adding more accounts is never assumed finished: only Continue ends it.
    /// Step 1 is done once the config has an account: the CLI cannot say
    /// whether that is the login in use, so its note says so only when it
    /// knows (`note`).
    func isTrue(_ s: SetupStep) -> Bool {
        switch s {
        case .saveLogin: return facts.accounts > 0
        case .addAccounts: return false
        case .workProfile: return facts.profiles > 0
        case .daemon: return facts.daemonInstalled == true
        }
    }

    /// What step 4 offers while it is open. `daemon install --dry-run` over
    /// an installed service rewrites it as dry run, a live one too, so
    /// Install is offered only when the CLI said the service is not
    /// installed. `readError` is `daemon status`'s failure, if it failed.
    public func daemonOffer(readError: CallError? = nil) -> DaemonOffer {
        switch facts.daemonInstalled {
        case false?: return .install
        case true?: return .settings
        case nil: return readError.map(DaemonOffer.readFailed) ?? .reading
        }
    }

    public var current: SetupStep? {
        reopened.last { status($0.step) == .pending }?.step ?? SetupStep.allCases.first { status($0) == .pending }
    }

    public var finished: Bool { current == nil }

    /// Whether the open step has Continue: once what it does is true, so a
    /// step opened again can be left without skipping it. Step 2 has it
    /// once an account is saved.
    public func canContinue(_ s: SetupStep) -> Bool {
        s == .addAccounts ? facts.accounts > 0 : isTrue(s)
    }

    public mutating func skip(_ s: SetupStep) {
        reopened.removeAll { $0.step == s }
        marks[s] = .skipped
    }

    public mutating func complete(_ s: SetupStep) {
        reopened.removeAll { $0.step == s }
        marks[s] = .done
    }

    /// Back to a skipped or finished step: it is the current step until it
    /// is completed or skipped, even when what it does is true already
    /// (Open). Opened while it was not (Do it now), it is done once it is.
    /// Reads `facts`: set them to what is true now first.
    public mutating func reopen(_ s: SetupStep) {
        reopened.removeAll { $0.step == s }
        reopened.append(Reopened(step: s, wasTrue: isTrue(s)))
        marks[s] = nil
    }

    /// The line under a step's title. Step 1 names the account the login in
    /// use is saved as, when that is known, and nothing else: the count of
    /// saved accounts is step 2's. `daemonMode` is the daemon's, once read.
    /// A step opened again keeps its line while what it does is true.
    public func note(_ s: SetupStep, daemonMode: String? = nil) -> String? {
        let st = status(s)
        if st == .skipped { return "Skipped" }
        let done = st == .done || isTrue(s)
        switch s {
        case .saveLogin:
            return done ? facts.currentLogin.map { "Saved as " + ProfilesText.q($0) } : nil
        case .addAccounts:
            guard facts.accounts > 0 else { return nil }
            return facts.accounts == 1 ? "1 account saved" : "\(facts.accounts) accounts saved"
        case .workProfile:
            guard done else { return nil }
            let others = facts.profiles == 1 ? "1 profile" : "\(facts.profiles) profiles"
            return others + " besides " + ProfilesText.q(StateFile.defaultProfile)
        case .daemon:
            guard done else { return nil }
            return daemonMode.map { "Installed, " + ($0 == "live" ? "live" : "dry run") } ?? "Installed"
        }
    }
}

/// The window's banner when the binary cannot run the steps: missing, too
/// old for this app, or speaking a newer contract. The step buttons are off
/// while the problem stands.
public struct SetupBanner: Equatable {
    public var title: String
    public var message: String
    public var hint: String
    /// What stays by the steps once the banner is closed with its ×, so
    /// the greyed buttons still say why.
    public var line: String
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

    /// The window’s “Don’t show this again” (0.6.1), in UserDefaults.
    public static let dontShowAgainKey = "firstRunDontShowAgain"

    public static func dontShowAgain(_ defaults: UserDefaults = .standard) -> Bool {
        defaults.bool(forKey: dontShowAgainKey)
    }

    public static func setDontShowAgain(_ on: Bool, _ defaults: UserDefaults = .standard) {
        defaults.set(on, forKey: dontShowAgainKey)
    }

    /// The window's foot, and its checkbox's label and help: where it opens
    /// again. Short enough to stay on one line beside the window's button;
    /// every step shows its own Skip.
    public static let reopenHint = "Right-click the menu-bar icon → Set Up… opens this again."
    public static let dontShowLabel = "Don’t show this again"
    public static let dontShowHelp = "Stop opening this window by itself at launch. Right-click the menu-bar icon → "
        + "Set Up… still opens it."

    public static func banner(_ problem: CLIError?) -> SetupBanner? {
        switch problem {
        case .missing?:
            return SetupBanner(title: "claudeswitch is not installed", message: CLIError.missing.message,
                               hint: CLI.installSteps, line: "claudeswitch is missing: install it first")
        case .tooOld(let v)?:
            return SetupBanner(title: "claudeswitch is too old", message: CLIError.tooOld(version: v).message,
                               hint: CLI.installSteps, line: "claudeswitch is too old: update it first")
        case let .unsupportedContract(v, c)?:
            return SetupBanner(title: "claudeswitch is newer than this app",
                               message: CLIError.unsupportedContract(version: v, contract: c).message, hint: "",
                               line: "claudeswitch is newer than this app: update the app first")
        default:
            return nil
        }
    }

    /// Whether the window opens by itself at launch: there is no config and
    /// the person has not asked it not to. Set up… in the menu-bar icon's
    /// menu opens it regardless.
    public static func offerAtLaunch(configPath: String?, defaults: UserDefaults = .standard,
                                     home: String = NSHomeDirectory(),
                                     exists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }) -> Bool {
        !dontShowAgain(defaults) && shouldOffer(configPath: configPath, home: home, exists: exists)
    }

    /// Step 4 while `daemon status` has not answered, and when it failed.
    public static let daemonReading = "Reading the service…"
    public static let tryAgain = "Try Again"
    /// Step 4's button when the service is installed already.
    public static let daemonSettings = "Open Settings → Daemon"

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
