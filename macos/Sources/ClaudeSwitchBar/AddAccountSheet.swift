import AppKit
import ClaudeSwitchCore
import SwiftUI

/// Adding an account (IMPROVEMENTS M4, mockup 5): sign in with the browser
/// and paste the code back (`login --direct`, the live session untouched),
/// or save the login Claude Code is using in one profile and add it to
/// another (`add --from S --profile T`, owner decision B2). Signing an
/// existing account in again is the browser route with its name fixed and
/// its profile shown, not chosen: signing in never moves an account (B3).
struct AddAccountSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let request: AddAccountRequest

    enum Route: String, CaseIterable { case browser = "Sign in with browser", current = "Save current login" }

    @State var route: Route = .browser
    @State var name = ""
    /// The target: the profile whose accounts it joins.
    @State var profile = StateFile.defaultProfile
    /// Save current login: the profile whose Claude Code login is saved.
    @State var source = StateFile.defaultProfile
    @State var browser: Browser?
    @State var browsers: [Browser] = []
    @State var started: LoginStart?
    @State var code = ""
    @State var error: AppError?
    @State var added: AccountAdded?
    @State var suggestion = NameSuggestion()
    @State var ready = false

    static let labelWidth: CGFloat = 112

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(request.relogin ? "Sign in to \(request.account ?? "") again" : "Add an account")
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
                if let e = error { InlineError(message: e.message, hint: e.hint) }
            }
            buttons
        }
        .padding(24)
        .frame(width: 520)
        .onAppear(perform: setUp)
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

    /// The profile the account will join: its own when it exists.
    var target: String { locked ?? profile }

    func label(_ s: String) -> some View {
        Text(s).frame(width: Self.labelWidth, alignment: .leading)
    }

    var fields: some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 8) {
            if route == .current {
                GridRow {
                    label("Save login from")
                    VStack(alignment: .leading, spacing: 4) {
                        profilePicker("Save the login Claude Code is using in", $source)
                        Text("The login Claude Code is using in this profile is saved.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            GridRow {
                label("Name")
                TextField("Name", text: $name,
                          prompt: Text(route == .current ? (suggestion.suggested ?? "name") : "e.g. work-3"))
                    .labelsHidden()
                    .textFieldStyle(.roundedBorder)
                    .disabled(request.relogin || started != nil)
            }
            GridRow {
                label(route == .current ? "Add it to profile" : "Profile")
                VStack(alignment: .leading, spacing: 4) {
                    if let l = locked {
                        profilePicker("Profile", .constant(l)).disabled(true)
                        Text(AddForm.lockedNote(account: request.account ?? name, profile: l))
                            .font(.caption).foregroundStyle(.secondary)
                    } else {
                        profilePicker(route == .current ? "Add it to profile" : "Profile", $profile)
                            .disabled(started != nil)
                    }
                }
            }
            if route == .browser {
                GridRow {
                    label("Browser")
                    Picker("Browser", selection: $browser) {
                        Text("Default browser").tag(Browser?.none)
                        ForEach(browsers) { Text($0.name).tag(Optional($0)) }
                    }
                    .labelsHidden()
                    .disabled(started != nil)
                }
            }
        }
    }

    func profilePicker(_ title: String, _ selection: Binding<String>) -> some View {
        Picker(title, selection: selection) {
            ForEach(AddForm.targets(store.profiles) + (AddForm.targets(store.profiles).contains(selection.wrappedValue)
                                                      ? [] : [selection.wrappedValue]), id: \.self) {
                Text($0).tag($0)
            }
        }
        .labelsHidden()
        .accessibilityLabel(title)
    }

    var browserSteps: some View {
        VStack(alignment: .leading, spacing: 8) {
            Step(n: 1, done: started != nil,
                 text: started == nil ? "Open the sign-in page" : "Sign-in page opened in \(browser?.name ?? "your browser")")
            Step(n: 2, done: false, text: "Paste the code it shows:")
            TextField("Code", text: $code, prompt: Text("code from the browser"))
                .labelsHidden()
                .textFieldStyle(.roundedBorder)
                .disabled(started == nil)
                .onSubmit(finish)
            if let s = started, let m = AddForm.mismatch(chosen: profile, start: s) {
                Label(m, systemImage: "info.circle").font(.caption).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Text("Your current Claude Code session is not touched. The account is checked, stored and added to "
                 + (started?.profile ?? locked ?? profile) + ".")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let s = started {
                Button("Open the page again") { Launcher.open(s.url, in: browser) }.buttonStyle(.link).font(.caption)
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.secondary.opacity(0.08)))
    }

    var currentNote: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Saves the account Claude Code is signed into in \(source), under this name, and adds it to \(target).")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let n = AddForm.crossProfileNote(source: source, target: target) {
                Label(n.prefix(1).uppercased() + n.dropFirst() + ".", systemImage: "info.circle")
                    .font(.caption).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    var buttons: some View {
        HStack(spacing: 8) {
            if busy { ProgressView().controlSize(.small) }
            Spacer()
            if added != nil {
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            } else {
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                if route == .browser {
                    if started == nil {
                        Button("Open sign-in page", action: start).keyboardShortcut(.defaultAction)
                            .disabled(name.isEmpty || busy)
                    } else {
                        Button("Finish", action: finish).keyboardShortcut(.defaultAction)
                            .disabled(code.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || busy)
                    }
                } else {
                    Button("Save", action: save).keyboardShortcut(.defaultAction)
                        .disabled((name.isEmpty && suggestion.suggested == nil) || busy)
                }
            }
        }
    }

    var busy: Bool { store.isBusy("login") || store.isBusy("add") }

    func setUp() {
        guard !ready else { return }
        ready = true
        browsers = Launcher.browsers()
        if let a = request.account { name = a }
        profile = AddForm.initialTarget(requested: request.profile, list: store.profiles)
        // The login to save: the profile asked about, else the followed one.
        source = AddForm.initialTarget(requested: request.profile ?? store.snapshot?.followed(store.menuProfile),
                                       list: store.profiles)
        if route == .current { askName() }
    }

    func fail(_ e: CallError) {
        if case .app(let a) = e { error = a } else { error = AppError(code: "failed", message: e.message, hint: e.hint) }
    }

    func start() {
        error = nil
        // An existing account keeps its profile: never ask for another.
        let (n, p, b) = (name, locked == nil ? profile : nil, browser?.name)
        Task {
            switch await store.call("login", { $0.loginStart(account: n, profile: p, browser: b) }) {
            case .success(let st):
                started = st
                Launcher.open(st.url, in: browser)
            case .failure(let e): fail(e)
            }
        }
    }

    func finish() {
        guard let st = started else { return }
        error = nil
        let (a, c) = (st.account.isEmpty ? name : st.account, code)
        Task {
            switch await store.call("login", { $0.loginCode(account: a, code: c) }) {
            case .success(let r):
                added = r
                store.refresh(full: true, profiles: true)
            case .failure(let e):
                fail(e)
                if e.code == "no_pending_login" { started = nil; code = "" }
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
        error = nil
        let (n, f, p) = (name.isEmpty ? suggestion.suggested : name, source, target)
        Task {
            switch await store.call("add", { $0.add(account: n, from: f, profile: p) }) {
            case .success(let r):
                added = r
                store.refresh(full: true, profiles: true)
            case .failure(let e): fail(e)
            }
        }
    }
}
struct Step: View {
    let n: Int
    let done: Bool
    let text: String

    var body: some View {
        HStack(spacing: 8) {
            ZStack {
                Circle().fill(done ? Color.green : Color.accentColor).frame(width: 20, height: 20)
                if done {
                    Image(systemName: "checkmark").font(.caption2.bold()).foregroundStyle(.white)
                } else {
                    Text("\(n)").font(.caption.bold()).foregroundStyle(.white)
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
            Label("\(added.account) is stored and verified", systemImage: "checkmark.seal.fill")
                .foregroundStyle(.green).font(.headline)
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                row("Email", added.email)
                row("Plan", added.plan)
                row("Seat", added.seat)
                row("Profile", added.profile.map { p in p + (added.pool.isEmpty ? "" : " (" + added.pool.joined(separator: ", ") + ")") })
                row("Login", added.renewable ? "renews by itself" : "will need signing in again")
            }
            .font(.callout)
            if let n = added.note {
                Label(n.prefix(1).uppercased() + n.dropFirst() + ".", systemImage: "info.circle")
                    .font(.caption).foregroundStyle(.orange)
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
