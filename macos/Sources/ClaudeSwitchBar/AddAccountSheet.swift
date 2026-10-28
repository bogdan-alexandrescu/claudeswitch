import AppKit
import ClaudeSwitchCore
import SwiftUI

/// Adding an account (IMPROVEMENTS M4, mockup 5): sign in with the browser
/// and paste the code back (`login --direct`, the live session untouched),
/// or save the login Claude Code is using in one profile and add it to
/// another (`add --from S --profile T`, owner decision B2). Signing an
/// existing account in again is the browser route with its name fixed and
/// where it is shown, not chosen: signing in never moves an account (B3). An
/// account is in one pool, which several profiles may use (shared pools).
struct AddAccountSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let request: AddAccountRequest

    enum Route: String, CaseIterable { case browser = "Sign In with Browser", current = "Save Current Login" }

    @State var route: Route = .browser
    @State var name = ""
    /// The target: the profile whose accounts it joins.
    @State var profile = StateFile.defaultProfile
    /// Save current login: the profile whose Claude Code login is saved.
    @State var source = StateFile.defaultProfile
    @State var browser: Browser?
    @State var browsers: [Browser] = []
    /// The pool a new account goes into (D37); nil until the pools are known.
    @State var poolPick: PoolPick?
    /// The organization a new account signs in to (GROUND_TRUTH §48).
    @State var orgPick: OrgPick = .browser
    @State var orgTyped = ""
    @State var started: LoginStart?
    @State var code = ""
    @State var error: AppError?
    @State var added: AccountAdded?
    @State var suggestion = NameSuggestion()
    @State var ready = false
    /// Save's `add` is running, not the probe for a name.
    @State private var saving = false
    /// Cancelled or closed: a call still running drops its answer.
    @State private var life = SheetLife()

    static let labelWidth: CGFloat = 112

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(request.relogin ? "Sign In to \(inQuotes(request.account ?? "")) Again" : "Add an Account")
                .font(.title3.bold())
            if let a = added {
                AddedView(added: a)
            } else {
                if !request.relogin {
                    Picker("", selection: $route) {
                        ForEach(Route.allCases, id: \.self) { Text($0.rawValue).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    .disabled(started != nil)
                }
                fields
                if route == .browser { browserSteps } else { currentNote }
                if let e = error { SheetError(message: e.message, hint: e.hint) }
            }
            buttons
        }
        .padding(24)
        .frame(width: 520)
        .onAppear(perform: setUp)
        .onDisappear { life.closed = true }
        .onChange(of: route) { r in if r == .current { askName() } }
        .onChange(of: source) { _ in if route == .current { askName() } }
    }

    /// The account's own profile when it exists (re-login, or a name
    /// already configured): shown, not chosen — on both routes, so saving
    /// a login under an existing name never fails at the CLI for its
    /// profile (review).
    var locked: String? {
        let id = request.account ?? name
        guard !id.isEmpty else { return nil }
        return AddForm.lockedProfile(account: id, accounts: store.accountList, list: store.profiles)
    }

    /// `account list`'s entry for an existing account: its pool and the
    /// profiles that may use it.
    var info: AccountInfo? { store.accountList?.account(request.account ?? name) }

    /// The profile `add` names: for an existing account the source when it
    /// may use it, else the first that may; for a new one, the one chosen.
    var target: String {
        guard let l = locked else { return profile }
        return AddForm.existingTarget(info: info, source: source, fallback: l)
    }

    /// Whether the source profile may use the account too (a pool both
    /// use): then it keeps it until its own rotation moves on (D34).
    var sourceUsesIt: Bool {
        if locked != nil { return info?.profiles?.contains(source) ?? false }
        return AddForm.newAccountShared(source: source, target: profile, list: store.profiles, pools: store.pools)
    }

    /// Whether a new account's pool is chosen here (D37): the CLI lists
    /// pools. An older one, or no [[profile]] blocks, keeps the Profile row.
    var choosesPool: Bool { locked == nil && store.pools != nil && !(store.profiles?.profiles.isEmpty ?? true) }

    /// No pool is the only choice (one profile, no pools): no Pool row, and
    /// the notes say it is added to default.
    var onlyNoPool: Bool { choosesPool && AddForm.onlyNoPool(pools: store.pools, profiles: store.profiles) }

    /// Where the notes say it goes: nothing more for an existing account
    /// (Used by and its note name its pool), the pool a new one goes into,
    /// or, without pools to choose, the profile it is added to.
    var place: AddPlace {
        AddForm.place(existing: locked != nil, pool: choosesPool ? poolPick : nil,
                      target: route == .current ? target : (started?.profile ?? profile),
                      profiles: store.profiles, pools: store.pools)
    }

    /// The profiles that may use a new account where it goes.
    var newUsers: [String] {
        guard let p = poolPick else { return [target] }
        switch p {
        case .none: return [StateFile.defaultProfile]
        case .pool(let n): return store.pools?.pool(n)?.profiles ?? []
        }
    }

    func label(_ s: String) -> some View {
        Text(s).frame(width: Self.labelWidth, alignment: .leading)
    }

    var fields: some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 8) {
            if route == .current {
                GridRow {
                    label("Save login from")
                    profilePicker("Save the login Claude Code is using in", $source)
                }
            }
            GridRow {
                label("Name")
                if request.relogin {
                    // Fixed, and in the title: a disabled field's grey name
                    // read as a placeholder.
                    Text(name).frame(maxWidth: .infinity, alignment: .leading)
                } else {
                    TextField("Name", text: $name,
                              prompt: Text(route == .current ? (suggestion.suggested ?? "name") : "e.g. work-3"))
                        .labelsHidden()
                        .textFieldStyle(.roundedBorder)
                        .disabled(started != nil)
                }
            }
            // No pool as the only choice is no choice: the note says where
            // it goes.
            if !(locked == nil && onlyNoPool) { placeRow }
            if route == .browser {
                GridRow {
                    label("Organization")
                    organization
                }
                GridRow {
                    label("Browser")
                    Picker("Browser", selection: $browser) {
                        Text("Default browser").tag(Browser?.none)
                        ForEach(browsers) { Text($0.name).tag(Optional($0)) }
                    }
                    .labelsHidden()
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .disabled(started != nil)
                }
            }
        }
    }

    /// Where it goes: Used by for an existing account, else the pool, or,
    /// with an older CLI, the profile.
    var placeRow: some View {
        GridRow {
            label(locked != nil ? "Used by" : choosesPool ? (route == .current ? "Add it to pool" : "Pool")
                  : route == .current ? "Add it to profile" : "Profile")
            VStack(alignment: .leading, spacing: 4) {
                if let l = locked {
                    // Shown, not chosen: an existing account stays in its pool.
                    Text(AddForm.usedBy(info: info, profile: l))
                    Text(AddForm.lockedNote(account: request.account ?? name, info: info, profile: l,
                                            grouped: PoolLayout.grouped(store.pools, profiles: store.profiles)))
                        .font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                } else if choosesPool {
                    let options = NewAccountPool.options(pools: store.pools, profiles: store.profiles)
                    if options.count == 1 {
                        // Nothing to choose: said, not a one-item pop-up.
                        Text(options[0].label)
                    } else {
                        Picker("Pool", selection: $poolPick) {
                            ForEach(options, id: \.self) { Text($0.label).tag(Optional($0.pick)) }
                        }
                        .labelsHidden()
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .disabled(started != nil)
                    }
                } else {
                    profilePicker(route == .current ? "Add it to profile" : "Profile", $profile)
                        .disabled(started != nil)
                }
            }
            // As the Organization cell: measured narrower, a wrapped
            // note reserved space above the row.
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    /// A new account picks one; an existing one asks for its own, which
    /// the CLI does by itself.
    @ViewBuilder var organization: some View {
        if locked != nil {
            Text(existingSeat ? "Its own — the sign-in asks for it" : "The one your browser is in")
                .foregroundStyle(.secondary)
        } else {
            VStack(alignment: .leading, spacing: 4) {
                Picker("Organization", selection: $orgPick) {
                    Text("The one your browser is in").tag(OrgPick.browser)
                    ForEach(AddForm.organizations(store.accountList), id: \.self) {
                        Text($0.label).tag(OrgPick.known($0.id))
                    }
                    Text("Another organization…").tag(OrgPick.other)
                }
                .labelsHidden()
                .frame(maxWidth: .infinity, alignment: .leading)
                .disabled(started != nil)
                if orgPick == .other {
                    TextField("Organization id", text: $orgTyped, prompt: Text("organization id"))
                        .labelsHidden()
                        .textFieldStyle(.roundedBorder)
                        .disabled(started != nil)
                }
                if let problem = AddForm.orgProblem(orgPick, typed: orgTyped) {
                    if orgTyped.isEmpty {
                        // Nothing typed yet: what to paste, not a problem.
                        Text(problem).font(.caption).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    } else {
                        // Not colour alone: the mark says it too.
                        Label(problem, systemImage: "exclamationmark.triangle").font(.caption)
                            .foregroundStyle(Color.csWarn)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                } else {
                    Text(orgPick == .browser
                         ? "To add someone in a second organization, choose it here."
                         : "The sign-in asks for this organization, and is refused if it comes back in another.")
                        .font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            // The cell takes the column's width: measured narrower, a
            // one-line caption reserved a second line above the row.
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    var existingSeat: Bool { store.accountList?.account(request.account ?? name)?.seat != nil }

    /// A profile to choose, the column's width; one alone is said, not a
    /// one-item pop-up.
    @ViewBuilder func profilePicker(_ title: String, _ selection: Binding<String>) -> some View {
        let all = AddForm.targets(store.profiles)
        let choices = all + (all.contains(selection.wrappedValue) ? [] : [selection.wrappedValue])
        if choices.count == 1 {
            Text(choices[0]).frame(maxWidth: .infinity, alignment: .leading)
                .accessibilityLabel(title + ": " + choices[0])
        } else {
            Picker(title, selection: selection) {
                ForEach(choices, id: \.self) { Text($0).tag($0) }
            }
            .labelsHidden()
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityLabel(title)
        }
    }

    var browserSteps: some View {
        VStack(alignment: .leading, spacing: 8) {
            Step(n: 1, done: started != nil,
                 text: started == nil ? "Open the sign-in page" : "Sign-in page opened in \(browser?.name ?? "your browser")")
            // Dimmed until the page is open: the current step is the one
            // drawn at full strength (a disabled field looks enabled in light
            // mode).
            VStack(alignment: .leading, spacing: 8) {
                Step(n: 2, done: false, text: "Paste the code it shows:")
                TextField("Code", text: $code, prompt: Text("code from the browser"))
                    .labelsHidden()
                    .textFieldStyle(.roundedBorder)
                    .disabled(started == nil)
                    .onSubmit(finish)
            }
            .opacity(started == nil ? 0.45 : 1)
            if let s = started, let m = AddForm.mismatch(chosen: profile, start: s) {
                Label(m, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(Color.csWarn)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Text(AddForm.signInNote(place))
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let s = started {
                Button("Open the Page Again") { Launcher.open(s.url, in: browser) }.buttonStyle(.link).font(.caption)
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.secondary.opacity(0.08)))
    }

    var currentNote: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(AddForm.saveNote(source: source, place))
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let n = crossNote {
                Label(n.prefix(1).uppercased() + n.dropFirst() + ".", systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(Color.csWarn)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    /// Saving one profile's login where another may use it: what happens to
    /// the source's session. With a pool chosen, the profiles that use it.
    var crossNote: String? {
        guard choosesPool else { return AddForm.crossProfileNote(source: source, target: target, sourceUsesIt: sourceUsesIt) }
        let others = newUsers.filter { $0 != source }
        guard !others.isEmpty else { return nil }
        return AddForm.crossProfileNote(source: source, target: others.joined(separator: ", "),
                                        sourceUsesIt: newUsers.contains(source))
    }

    var buttons: some View {
        HStack(spacing: 8) {
            if busy {
                ProgressView().controlSize(.small)
                if let t = AddForm.busyText(login: store.isBusy("login"), add: store.isBusy("add"),
                                            started: started != nil, saving: saving) {
                    Text(t).font(.caption).foregroundStyle(.secondary)
                }
            }
            if added == nil, started == nil, !busy,
               AddForm.needsName(name, suggested: suggestion.suggested, saving: route == .current) {
                // Why the button is disabled; Save takes the suggestion.
                Text("Enter a name to continue.").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if added != nil {
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .background {
                        // Escape closes it too: nothing is left to cancel.
                        Button("Close") { dismiss() }.keyboardShortcut(.cancelAction)
                            .opacity(0).accessibilityHidden(true)
                    }
            } else {
                Button("Cancel") { life.closed = true; dismiss() }.keyboardShortcut(.cancelAction)
                if route == .browser {
                    if started == nil {
                        Button("Open Sign-In Page", action: start).keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                            .disabled(!canStart)
                    } else {
                        Button("Finish", action: finish).keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                            .disabled(!canFinish)
                    }
                } else {
                    Button("Save", action: save).keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                        .disabled(!canSave)
                }
            }
        }
    }

    var busy: Bool { store.isBusy("login") || store.isBusy("add") }

    // Each button's state, and its action's guard: Return in the code
    // field runs Finish even while the button is disabled.
    var canStart: Bool {
        !AddForm.needsName(name, suggested: nil, saving: false) && !busy
            && (locked != nil || AddForm.orgProblem(orgPick, typed: orgTyped) == nil)
    }
    var canFinish: Bool { started != nil && !code.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !busy }
    var canSave: Bool { !AddForm.needsName(name, suggested: suggestion.suggested, saving: true) && !busy }

    func setUp() {
        guard !ready else { return }
        ready = true
        browsers = Launcher.browsers()
        if let a = request.account { name = a }
        profile = AddForm.initialTarget(requested: request.profile, list: store.profiles)
        // The login to save: the profile asked about, else the followed one.
        source = AddForm.initialTarget(requested: request.profile ?? store.snapshot?.followed(store.menuProfile),
                                       list: store.profiles)
        poolPick = NewAccountPool.initial(profile: request.profile ?? profile, pools: store.pools, profiles: store.profiles)
        if route == .current { askName() }
    }

    func fail(_ e: CallError) {
        var a: AppError
        if case .app(let x) = e { a = x } else { a = AppError(code: "failed", message: e.message, hint: e.hint) }
        if a.code == "already_vaulted" {
            // The app's words, not a terminal's.
            a.message = AddForm.duplicate(a.message)
            if let h = AddForm.duplicateHint(saving: route == .current, asked: started?.org) { a.hint = h }
        }
        error = a
    }

    func start() {
        guard canStart else { return }
        error = nil
        // An existing account keeps its profile and asks for its own
        // organization: never ask for another.
        let (n, p, b) = (name, locked == nil ? profile : nil, browser?.name)
        let o = locked == nil ? AddForm.org(orgPick, typed: orgTyped) : nil
        // D37: the pool chosen, in place of the profile.
        let pool = choosesPool ? poolPick : nil
        let prof = pool == nil ? p : nil
        let life = life
        Task {
            let r = await store.call("login", { $0.loginStart(account: n, profile: prof, browser: b, org: o, pool: pool) })
            // Cancelled meanwhile: no page opens for a sheet that is gone.
            guard !life.closed else { return }
            switch r {
            case .success(let st):
                started = st
                Launcher.open(st.url, in: browser)
            case .failure(let e): fail(e)
            }
        }
    }

    func finish() {
        guard canFinish, let st = started else { return }
        error = nil
        let (a, c) = (st.account.isEmpty ? name : st.account, code)
        Task {
            switch await store.call("login", { $0.loginCode(account: a, code: c) }) {
            case .success(let r):
                added = r
                store.refresh(full: true, profiles: true)
            case .failure(let e):
                fail(e)
                // A refusal spent the attempt: open the form again, to pick
                // another organization or browser and sign in again.
                if let c = e.code, AddForm.startsOver(after: c) { started = nil; code = "" }
            }
        }
    }

    /// `add` with no name answers name_required, the name suggested for the
    /// source's login as the whole hint. Asked again for each source; a
    /// late answer for an earlier one is dropped.
    func askName() {
        let token = suggestion.probe(source)
        let s = source
        Task {
            let r = await store.call("add", { $0.add(account: nil, from: s, profile: nil) })
            switch r {
            case .failure(.app(let e)) where e.code == "name_required":
                name = suggestion.answer(token: token, hint: e.hint, name: name)
            case .failure(let e):
                name = suggestion.answer(token: token, hint: nil, name: name)
                if suggestion.source == s { fail(e) }
            case .success(let v):
                added = v // a CLI that needed no name
            }
        }
    }

    func save() {
        guard canSave else { return }
        error = nil
        let (n, f, p) = (name.isEmpty ? suggestion.suggested : name, source, target)
        // D37: the pool chosen; the source alone is then the profile named.
        let pool = choosesPool ? poolPick : nil
        let prof = pool == nil ? p : nil
        saving = true
        Task {
            let r = await store.call("add", { $0.add(account: n, from: f, profile: prof, pool: pool) })
            saving = false
            switch r {
            case .success(let r):
                added = r
                store.refresh(full: true, profiles: true)
            case .failure(let e): fail(e)
            }
        }
    }
}

/// Whether a sheet is still open, for a call that ends after it closed.
/// A class, so the call reads the sheet's own copy, not a captured value.
final class SheetLife {
    var closed = false
}

/// A sheet's refusal: InlineError's look, with the hint under the
/// message's text rather than under its icon.
struct SheetError: View {
    let message: String
    let hint: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Image(systemName: "xmark.octagon.fill").foregroundStyle(Color.csBad).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(message).foregroundStyle(Color.csBad)
                if !hint.isEmpty { Text(hint).foregroundStyle(.secondary) }
            }
        }
        .font(.caption)
        .fixedSize(horizontal: false, vertical: true)
        .accessibilityElement(children: .combine)
    }
}

struct Step: View {
    let n: Int
    let done: Bool
    let text: String

    var body: some View {
        HStack(spacing: 8) {
            ZStack {
                Circle().fill(done ? Color.green : Color.csAccent).frame(width: 20, height: 20)
                if done {
                    Image(systemName: "checkmark").font(.caption2.bold()).foregroundStyle(.white)
                } else {
                    Text("\(n)").font(.caption.bold()).foregroundStyle(Color.csOnAccent)
                }
            }
            .accessibilityHidden(true)
            Text(text)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Step \(n)\(done ? ", done" : ""): \(text)")
    }
}

/// The verified account.
struct AddedView: View {
    let added: AccountAdded

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("The account \(inQuotes(added.account)) is stored and verified", systemImage: "checkmark.seal.fill")
                .foregroundStyle(Color.csGood).font(.headline)
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                row("Email", added.email)
                row("Plan", added.plan)
                row("Seat", added.seat)
                if let pool = added.poolName {
                    row("Pool", pool)
                }
                if let users = added.profiles {
                    row("Used by", users.isEmpty ? "no profile" : users.joined(separator: ", "))
                } else {
                    row("Profile", added.profile.map { p in p + (added.pool.isEmpty ? "" : " (" + added.pool.joined(separator: ", ") + ")") })
                }
                row("Login", added.renewable ? "renews by itself" : "will need signing in again")
            }
            .font(.callout)
            if let n = added.note {
                Label(n.prefix(1).uppercased() + n.dropFirst() + ".", systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(Color.csWarn)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.green.opacity(0.08)))
    }

    @ViewBuilder func row(_ k: String, _ v: String?) -> some View {
        if let v {
            GridRow {
                Text(k).foregroundStyle(.secondary)
                Text(v).textSelection(.enabled).lineLimit(1).truncationMode(.middle)
            }
        }
    }
}
