import Foundation

// Settings → Profiles in words (QA 2026-10-28). A profile's or a pool's
// name ran into the words around it ("Remove profile review", "Pools
// default uses"), so a name in a sentence is quoted and kept whole, and no
// sentence starts with one. Pure, so the pane stays thin and this is tested.

public enum ProfilesText {
    /// “client-x”: a name in a sentence. Its hyphens do not break, so a
    /// long name stays whole on one line.
    public static func q(_ name: String) -> String {
        "“" + name.replacingOccurrences(of: "-", with: "\u{2011}") + "”"
    }

    /// A profile's directory as a person reads it: the home folder as ~,
    /// Claude Code's own (~/.claude) when it names none.
    public static func dir(_ dir: String?, home: String = NSHomeDirectory()) -> String {
        guard let d = dir, !d.isEmpty else { return "~/.claude" }
        if d == home { return "~" }
        return d.hasPrefix(home + "/") ? "~" + d.dropFirst(home.count) : d
    }

    // MARK: the cards

    /// Beside a profile's name, closed or open: "signed in · work-1",
    /// "needs sign-in · old-sso" (the account last live there), "needs
    /// sign-in", or "sign-in unknown".
    public static func signIn(_ signedIn: String, live: String?) -> String {
        let who = live.map { " · " + $0 } ?? ""
        switch signedIn {
        case "yes": return "signed in" + who
        case "no": return "needs sign-in" + who
        default: return "sign-in unknown"
        }
    }

    /// The menu under a profile that needs sign-in (`profile seed`); the
    /// badge says the state, so the row says only the action.
    public static let signInAction = "Sign in…"

    /// One of its accounts in that menu; one live in another profile cannot
    /// sign this one in (§3, one profile at a time).
    public static func signInWith(_ account: String, elsewhere: String?) -> String {
        account + (elsewhere.map { " — in use by \(q($0))" } ?? "")
    }

    public static func noAccountToSignIn(_ name: String) -> String { "Add an account to \(q(name)) first" }
    public static func signedIn(profile: String, account: String) -> String {
        "The profile \(q(profile)) is signed in with \(q(account))"
    }
    public static func signInFailed(_ name: String) -> String { "Could not sign \(q(name)) in" }

    /// Open Claude Code's help: why it is off when it is.
    public static func openHelp(_ name: String, signedIn: String) -> String {
        signedIn == "no" ? "The profile \(q(name)) needs sign-in: \(signInAction) first" : "Open Claude Code in \(q(name))"
    }

    /// A closed card's one line: its directory, its pools (its accounts
    /// with an older CLI, `pools` nil), and its thresholds as every other
    /// view says them, "session 85% · week 98%".
    public static func closedLine(_ p: ProfileInfo, pools: PoolList?) -> String {
        var parts = [dir(p.dir)]
        parts.append(ProfilePools.summary(p, pools: pools)
                     ?? (p.pool.isEmpty ? "no accounts" : p.pool.joined(separator: ", ")))
        if let a = p.thresholds.switchAt, let w = p.thresholds.switchAtWeekly {
            parts.append(String(format: "session %g%% · week %g%%", a, w))
        }
        return parts.joined(separator: " · ")
    }

    /// The POOLS row of a profile that uses none (and, for default, no
    /// account in no pool): the set may have pools, just none for it.
    public static let usesNoPool = "Uses no pool"

    /// Said once, at the top of the pane, with a CLI from before pools.
    public static let poolsNeedUpdate = "Update claudeswitch to group accounts into pools and share them between profiles."

    /// The one profile of a config with no [[profile]] blocks; New profile
    /// is the pane's own button, above it.
    public static let implicitNote = "Every account belongs to this one setup. To split them, create a profile with "
        + "New profile above."

    /// "Shown in menu bar": checked on the profile the menu bar follows. On
    /// default it cannot be unchecked, since the menu bar would follow
    /// default anyway.
    public static func menuBarHelp(followed: Bool, name: String) -> String {
        guard followed else { return "Make the menu bar follow \(q(name))" }
        if name == StateFile.defaultProfile {
            return "The menu bar follows this profile. To follow another, check Shown in menu bar on its card."
        }
        return "The menu bar follows this profile. Uncheck to follow \(q(StateFile.defaultProfile)) again."
    }

    // MARK: a profile's own thresholds (`profile set`)

    /// Shown after the field, so "75" reads as the global "90%" does.
    public static func unit(_ s: SettingSchema) -> String? { s.type == "percent" ? "%" : nil }

    /// The empty field's placeholder: the global value it inherits, without
    /// the unit beside the field; "" until the values are read.
    public static func placeholder(_ s: SettingSchema, global raw: String?) -> String {
        guard let raw else { return "" }
        return unit(s) != nil ? raw.trimmingCharacters(in: .whitespaces) : SettingNames.value(s, raw)
    }

    /// Under the field. The global value is said once: in the placeholder
    /// while it inherits, here once it overrides.
    /// A global that reads "none" is said as "the global value (none)", so
    /// it does not read as no global at all.
    public static func overrideCaption(_ s: SettingSchema, overridden: Bool, global raw: String?) -> String {
        guard overridden else { return "inherits the global value" }
        guard let v = raw.map({ SettingNames.value(s, $0) }) else { return "overrides the global value" }
        return v == "none" ? "overrides the global value (none)" : "overrides the global \(v)"
    }

    /// ↺'s help.
    public static func inheritHelp(_ s: SettingSchema, global raw: String?) -> String {
        "Inherit the global value" + (raw.map { " (\(SettingNames.value(s, $0)))" } ?? "")
    }

    // MARK: remove

    public static func removeTitle(_ name: String) -> String { "Remove the profile \(q(name))?" }

    /// The sheet's own button: the sheet is the confirmation.
    public static func removeButton(_ name: String) -> String { "Remove \(q(name))" }

    /// The note once `profile remove` answers.
    public static func removed(_ r: ProfileRemoved) -> String {
        var t = "Removed the profile \(q(r.profile))"
        if let d = r.to, !r.moved.isEmpty { t += "; its accounts are in \(q(d))" }
        if let g = r.ghost { t += "; \(q(g.account)) stays guarded until that login is gone" }
        if r.daemonRunning && r.daemonLoaded == false { t += " (the daemon has not picked it up yet)" }
        return t
    }

    /// The note once Forget… (`profile forget`) answers: the old profile's
    /// last login is no longer guarded.
    public static func forgot(profile: String, account: String) -> String {
        "Forgot the old login of \(q(account)) in \(q(profile))"
    }

    // MARK: Change… and New profile

    public static func changeTitle(_ name: String) -> String { "Choose the pools \(q(name)) uses" }
    public static func changeHelp(_ name: String) -> String { changeTitle(name) + ", and their order" }

    /// Shared pools in one line (§3: one profile at a time).
    public static let sharing = "Profiles that use the same pool share its accounts; each account is live in one "
        + "profile at a time. No account moves."

    public static func changeIntro(_ name: String) -> String {
        "The profile \(q(name)) uses the checked pools in this order: an account in a later pool only when "
            + "nothing in an earlier one is usable. " + sharing
    }

    /// Default's accounts in no pool, after its pools (D6).
    public static func thenUnlisted(_ ids: [String], profile: String) -> String {
        "Then the \(ids.count == 1 ? "account" : "\(ids.count) accounts") in no pool "
            + "(\(ids.map(q).joined(separator: ", "))), as always for \(q(profile))."
    }

    public static let checkOne = "Check at least one pool."

    /// Pools in the order a profile uses them: "“ops”, then “team”".
    static func inOrder(_ pools: [String]) -> String { pools.map(q).joined(separator: ", then ") }

    /// The note once `profile pools` answers.
    public static func poolsSaved(_ name: String, _ chosen: [String]) -> String {
        "The profile \(q(name)) now uses " + inOrder(chosen)
    }

    /// A pool's line in the chooser, under its name: whose own it is ("its
    /// own pool" for the profile being edited) or who uses it, then its
    /// accounts. A profile's own pool goes by the profile's name, so the
    /// caption does not repeat it.
    public static func poolCaption(_ name: String, pools: PoolList?, owner: String?) -> String {
        guard let p = pools?.pool(name) else { return "" }
        let accounts = p.accounts.isEmpty ? "no accounts" : p.accounts.joined(separator: ", ")
        if p.own { return (name == owner ? "its own pool" : "a profile’s own pool") + " · " + accounts }
        let users = p.profiles.isEmpty ? "used by no profile" : "used by " + p.profiles.map(q).joined(separator: ", ")
        return users + " · " + accounts
    }

    /// New profile's Pools caption: the numbers explained once a pool is
    /// checked, then sharing.
    public static func newProfilePools(numbered: Bool, ownFirst: Bool) -> String {
        switch (numbered, ownFirst) {
        case (true, true): return "Used in the order numbered; its own accounts come first. " + sharing
        case (true, false): return "Used in the order numbered. " + sharing
        case (false, true): return "Its own accounts come first. " + sharing
        case (false, false): return sharing
        }
    }

    public static let newProfileOwn = "Accounts in no pool, which \(q(StateFile.defaultProfile)) uses now: the ones "
        + "checked become this profile’s own."

    /// Beside an account live in another profile.
    public static func inUseBy(_ profile: String) -> String { "in use by \(q(profile))" }

    /// Not a name the example data uses.
    public static let namePrompt = "e.g. client-x"

    /// Beside Create while it is off.
    public static let enterName = "Enter a name to continue."

    /// Above Create when nothing gives the new profile an account: none of
    /// its own checked, and no checked pool with one. Allowed, but said,
    /// with what the sheet offers to change it. Nil when it gets accounts.
    public static func startsEmpty(own: [String], chosen: [String], pools: PoolList?,
                                   offersAccounts: Bool, offersPools: Bool) -> String? {
        guard own.isEmpty, !chosen.contains(where: { !(pools?.pool($0)?.accounts.isEmpty ?? true) }) else { return nil }
        let check: String
        switch (offersAccounts, offersPools) {
        case (true, true): check = "check one of its accounts or a pool, or "
        case (true, false): check = "check one of its accounts, or "
        case (false, true): check = "check a pool, or "
        case (false, false): check = ""
        }
        return "It starts with no accounts: " + check + "add one later."
    }
}

// MARK: New profile, step by step

/// New profile's calls (`profile create`, then `profile pools`, then
/// `profile seed`) and how far they got. Once the profile exists a step
/// that fails is retried alone, with what follows it: `profile create`
/// again would answer that it exists (QA 2026-10-28).
public struct NewProfileRun: Equatable {
    public enum Step: Equatable { case create, pools, seed }

    public struct Failure: Equatable {
        public var step: Step
        public var message: String
        public var hint: String
    }

    public let plan: NewProfilePlan
    public private(set) var done: [Step] = []
    /// The step that failed and has not worked since.
    public private(set) var failure: Failure?
    /// A step after `profile create` failed: from then on the sheet says
    /// what was done, with Retry and Close, in place of the form.
    public private(set) var interrupted = false

    public init(plan: NewProfilePlan) { self.plan = plan }

    /// What the plan takes, in order.
    public var steps: [Step] {
        [.create] + (plan.pools.isEmpty ? [] : [.pools]) + (plan.seed == nil ? [] : [.seed])
    }

    /// What is left to run, in order: after a failure, from the step that failed.
    public var remaining: [Step] { steps.filter { !done.contains($0) } }
    public var created: Bool { done.contains(.create) }
    public var finished: Bool { remaining.isEmpty }

    public mutating func succeeded(_ s: Step) {
        if !done.contains(s) { done.append(s) }
        if failure?.step == s { failure = nil }
    }

    /// A step's refusal, with where to finish it by hand when the CLI
    /// names nothing. `profile seed` timing out on the daemon
    /// (daemon_not_loaded) left the profile as it was.
    public mutating func failed(_ s: Step, code: String?, message: String, hint: String) {
        var h = hint
        switch s {
        case .seed where code == "daemon_not_loaded":
            h = "Retry once the daemon runs, or Close and sign it in later from its card in Profiles (Sign in…)."
        case .pools where hint.isEmpty:
            h = "Retry, or Close and choose them later from its card in Profiles (Change…)."
        case .seed where hint.isEmpty:
            h = "Retry, or Close and sign it in later from its card in Profiles (Sign in…)."
        default: break
        }
        failure = Failure(step: s, message: message, hint: h)
        if created { interrupted = true }
    }

    /// The title once interrupted: what failed, or what runs now.
    public var headline: String? {
        guard interrupted else { return nil }
        let made = "The profile \(ProfilesText.q(plan.name)) was created"
        if let f = failure { return made + ", but \(Self.doing(f.step)) failed." }
        guard let next = remaining.first else { return made + "." }
        return made + "; \(Self.doing(next))…"
    }

    static func doing(_ s: Step) -> String {
        switch s {
        case .create: return "creating it"
        case .pools: return "giving it its pools"
        case .seed: return "signing it in"
        }
    }

    /// The note once every step is done.
    public var note: String {
        "Created the profile \(ProfilesText.q(plan.name))"
            + (plan.pools.isEmpty ? "" : "; it uses " + ProfilesText.inOrder(plan.pools))
    }
}
