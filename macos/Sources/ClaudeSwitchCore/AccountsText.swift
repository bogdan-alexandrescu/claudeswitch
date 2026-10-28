import Foundation

/// A name in prose or a title, in curly quotes, so it never reads as part of
/// the sentence (visual QA: "Rename personal", "Same as personal").
public func inQuotes(_ name: String) -> String { "“" + name + "”" }

/// An Accounts row's second line: who uses it, its figure, its Chrome
/// profile, its seat and when its login renews.
public enum AccountLine {
    /// The line, longest first. Whole segments give way, the seat first,
    /// then Chrome, then the login, then who uses it (named, then counted);
    /// the figure goes last. `usedBy` nil when the CLI does not say, empty
    /// when no profile uses it.
    public static func variants(usedBy: [String]?, figure: String?, chrome: String?, seat: String?,
                                login: String?) -> [String] {
        let named = usedBy.map { $0.isEmpty ? "used by no profile" : "used by " + $0.joined(separator: ", ") }
        let counted = (usedBy?.count ?? 0) > 1 ? "used by \(usedBy!.count) profiles" : nil
        let steps: [[String?]] = [
            [named, figure, chrome, seat, login],
            [named, figure, chrome, login],
            [named, figure, login],
            [named, figure],
            [counted, figure],
            [figure],
        ]
        var out: [String] = []
        for s in steps {
            let line = s.compactMap { $0 }.joined(separator: " · ")
            if !line.isEmpty, !out.contains(line) { out.append(line) }
        }
        return out.isEmpty ? [""] : out
    }
}

/// Renaming a pool (D39).
public enum PoolRename {
    /// Under the field: a profile's own pool becomes a pool of its own under
    /// the new name, which that profile keeps using; any other keeps its
    /// place in every profile's order.
    public static func note(name: String, own: Bool, to new: String) -> String {
        guard own else { return "Every profile that uses it keeps using it, in the same place in its order." }
        let renamed = new.isEmpty ? "Renamed" : "Renamed to \(inQuotes(new))"
        return renamed + ", it becomes a separate pool with the same accounts, which \(inQuotes(name)) keeps using."
    }
}

/// Renaming an account.
public enum AccountRename {
    /// The note once it is done.
    public static func note(_ old: String, to new: String) -> String {
        "Renamed \(inQuotes(old)) to \(inQuotes(new))"
    }
}

/// The Accounts pane's Plan column.
public enum PlanColumn {
    /// The plan before its parenthesis ("Max 5x" of "Max 5x (team,
    /// enterprise seat)"), so the column never cuts one in the middle; a
    /// dash when none is recorded.
    public static func text(_ plan: String?) -> String {
        guard let p = plan, !p.isEmpty else { return "—" }
        guard let open = p.range(of: " ("), open.lowerBound > p.startIndex else { return p }
        return String(p[..<open.lowerBound])
    }

    /// The tooltip: the whole plan.
    public static func help(_ plan: String?) -> String {
        plan.flatMap { $0.isEmpty ? nil : $0 } ?? "No plan recorded yet"
    }
}

/// The New pool sheet's words.
public enum NewPoolText {
    /// Beside each account: the pool it is in, or, checked, the one it
    /// leaves (NewPoolPlan.note); for one in no pool, that, or, checked,
    /// that it joins the new pool. Nil when no pools are known.
    public static func note(for account: String, checked: Bool, name: String, pools: PoolList?) -> String? {
        guard let pools else { return nil }
        if let n = NewPoolPlan.note(for: account, checked: checked, pools: pools) { return n }
        guard checked else { return "in no pool" }
        return "joins " + (name.isEmpty ? "the new pool" : inQuotes(name))
    }
}

/// Where the Add account sheet says the account goes.
public enum AddPlace: Equatable {
    /// An account that exists: Used by and its note say where it is.
    case existing
    /// A new account's pool, in NewAccountPool.describe's words ("in the
    /// pool “team”, which …").
    case pool(String)
    /// What it is added to: "the profile “work”", "the pool “x”, the
    /// first “work” uses", or, when no pool is the only choice, "“default”".
    case added(String)
}

public extension AddForm {
    /// already_vaulted in the app's words. The CLI's message is a
    /// terminal's (wrapped, indented), and its "quota pool" is not the
    /// app's pool. `login --code` says `already vaulted as "X" — WHO.`,
    /// `add` says `already vaulted as "X" (WHO).`, WHO being "email in
    /// Organization". Any other wording is kept, made one paragraph.
    static func duplicate(_ message: String) -> String {
        let first = message.split(separator: "\n", maxSplits: 1).first.map(String.init) ?? message
        guard let open = first.range(of: "already vaulted as \""),
              let close = first[open.upperBound...].firstIndex(of: "\"") else { return paragraph(message) }
        let name = String(first[open.upperBound..<close])
        var rest = String(first[first.index(after: close)...])
        if rest.hasPrefix(" — ") {
            rest = String(rest.dropFirst(3))
            if rest.hasSuffix(".") { rest.removeLast() }
        } else if rest.hasPrefix(" (") {
            rest = String(rest.dropFirst(2))
            if rest.hasSuffix(").") { rest.removeLast(2) }
        } else {
            rest = ""
        }
        rest = rest.trimmingCharacters(in: .whitespaces)
        // "person1@example.com in Example": the email, then the organization.
        var who = rest
        if let i = rest.range(of: " in ") {
            let org = rest[i.upperBound...].trimmingCharacters(in: .whitespaces)
            who = String(rest[..<i.lowerBound]) + (org.isEmpty ? "" : ", " + org)
        } else if rest.hasSuffix(" in") {
            who = String(rest.dropLast(3))
        }
        return "This person in this organization is already saved as \(inQuotes(name))"
            + (who.isEmpty ? "" : " (\(who))") + ". Nothing was stored."
    }

    /// The next step under a duplicate. Save current login saves whatever
    /// Claude Code is signed in to, so another account is a sign-in; a
    /// sign-in's is appHint's.
    static func duplicateHint(saving: Bool, asked: String?) -> String? {
        guard saving else { return appHint(code: "already_vaulted", asked: asked) }
        return "Save Current Login saves the account Claude Code is signed in to. To add another, choose Sign In with Browser."
    }

    /// Whether a new account can go nowhere but no pool: no pools, and a
    /// profile named default to use it (one profile, say). The sheet then
    /// has no Pool row, and says the account is added to default.
    static func onlyNoPool(pools: PoolList?, profiles: ProfileList?) -> Bool {
        guard pools != nil else { return false }
        let o = NewAccountPool.options(pools: pools, profiles: profiles)
        return o.count == 1 && o[0].pick == .none
    }

    /// Where the sheet says the account goes. `pool` is the one chosen when
    /// the sheet chooses a pool, else nil; `target` the profile it is added
    /// to without one.
    static func place(existing: Bool, pool: PoolPick?, target: String, profiles: ProfileList?,
                      pools: PoolList?) -> AddPlace {
        if existing { return .existing }
        if let p = pool {
            if onlyNoPool(pools: pools, profiles: profiles) { return .added(inQuotes(StateFile.defaultProfile)) }
            return .pool(NewAccountPool.describe(p, pools: pools))
        }
        let d = destination(target: target, list: profiles, pools: pools)
        return .added(d == target ? "the profile " + inQuotes(target) : d)
    }

    /// Under the sign-in steps.
    static func signInNote(_ place: AddPlace) -> String {
        let first = "The login Claude Code is using now is not touched. "
        switch place {
        case .existing: return first + "The account is checked and stored."
        case .pool(let w): return first + "The account is checked and stored \(w)."
        case .added(let w): return first + "The account is checked, stored and added to \(w)."
        }
    }

    /// Under Save current login.
    static func saveNote(source: String, _ place: AddPlace) -> String {
        let first = "Saves the login Claude Code is using in \(inQuotes(source)) under this name"
        switch place {
        case .existing: return first + "."
        case .pool(let w): return first + ", \(w)."
        case .added(let w): return first + ", and adds it to \(w)."
        }
    }

    /// Whether the sheet waits for a name: none typed and, saving, none
    /// suggested either (Save takes the suggestion).
    static func needsName(_ name: String, suggested: String?, saving: Bool) -> Bool {
        name.isEmpty && !(saving && suggested != nil)
    }

    /// Beside the spinner: what the sheet waits for. `login` and `add` are
    /// the calls running; `started` once the sign-in page is open; `saving`
    /// when the `add` is Save's, not the probe for a name.
    static func busyText(login: Bool, add: Bool, started: Bool, saving: Bool) -> String? {
        if login { return started ? "Checking the account…" : "Opening the sign-in page…" }
        if add { return saving ? "Saving the login…" : "Reading the login…" }
        return nil
    }

    /// A terminal's text as one paragraph: the lines joined, the indents
    /// gone, a capital first and a full stop last.
    internal static func paragraph(_ s: String) -> String {
        var out = s.split(separator: "\n").map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }.joined(separator: " ")
        if let c = out.first { out = c.uppercased() + out.dropFirst() }
        if let l = out.last, !".!?".contains(l) { out += "." }
        return out
    }
}
