import AppKit
import ClaudeSwitchCore
import SwiftUI

/// Profiles: each is a Claude Code setup with its own login and its own
/// accounts (docs/PROFILES.md).
struct ProfilesPane: View {
    @EnvironmentObject var store: Store
    @State private var creating = false
    @State private var editing: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                PaneHeader(title: "Profiles",
                           subtitle: "Each profile is a Claude Code setup with its own login and its own accounts.") {
                    Button("New profile") { creating = true }
                        .disabled(store.binaryPath == nil)
                }
                if let e = store.profilesError {
                    InlineError(message: "Could not read the profiles: " + e.message, hint: e.hint)
                }
                if let list = store.profiles {
                    ForEach(list.profiles) { p in
                        if editing == p.name || list.profiles.count == 1 {
                            ProfileEditor(profile: p, done: list.profiles.count == 1 ? nil : { editing = nil },
                                          create: { creating = true })
                        } else {
                            ProfileRow(profile: p) { editing = p.name }
                        }
                    }
                    if !list.ghosts.isEmpty { GhostsSection(ghosts: list.ghosts) }
                } else if store.profilesError == nil {
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Reading the profiles…").foregroundStyle(.secondary)
                    }
                }
            }
            .padding(24)
        }
        .onAppear { if editing == nil { editing = store.snapshot?.followed(store.menuProfile) } }
        .sheet(isPresented: $creating) { NewProfileSheet().environmentObject(store) }
    }
}

/// A collapsed profile: name, directory, accounts and thresholds, and Edit.
struct ProfileRow: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo
    let edit: () -> Void

    var body: some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text(profile.name).font(.headline).lineLimit(1).truncationMode(.tail).help(profile.name)
                Text(summary).font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle).help(summary)
            }
            Spacer()
            Button("Edit", action: edit)
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.secondary.opacity(0.25)))
    }

    var summary: String {
        var parts = [profile.dir ?? "~/.claude"]
        parts.append(profile.pool.isEmpty ? "no accounts" : profile.pool.joined(separator: ", "))
        let t = profile.thresholds
        if let a = t.switchAt, let w = t.switchAtWeekly {
            parts.append(String(format: "switch at %g%% session, %g%% week", a, w))
        }
        return parts.joined(separator: " · ")
    }
}

/// An expanded profile: sign-in, accounts (add, remove, move), per-profile
/// thresholds, and its actions in one row.
struct ProfileEditor: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo
    var done: (() -> Void)?
    var create: () -> Void = {}
    @State private var removing = false

    var removable: Bool { store.profiles.map { ProfileRemoval.canRemove($0, profile.name) } ?? false }
    var followed: Bool { store.snapshot?.followed(store.menuProfile) == profile.name }
    /// The live account's readings, for the rings beside the name.
    var live: AccountView? { profile.live.flatMap { id in store.snapshot?.accounts.first { $0.id == id } } }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 8) {
                MiniRings(account: live, size: 36, dotRadius: 2.4)
                Text(profile.name).font(.headline).lineLimit(1).truncationMode(.tail).help(profile.name)
                Text(profile.dir ?? "~/.claude").font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle).help(profile.dir ?? "~/.claude")
                if let b = ProfileText.badge(profile) {
                    Text(b).font(.caption2).foregroundStyle(.secondary)
                }
                Spacer()
                SignInBadge(signedIn: profile.signedIn, live: profile.live)
                if let d = done {
                    IconButton(symbol: "chevron.up", label: "Collapse \(profile.name)", action: d)
                }
            }
            if profile.implicit {
                // No [[profile]] blocks yet: there is nothing to split, so
                // no accounts or thresholds of its own to edit (B7).
                HStack(spacing: 8) {
                    Image(systemName: "rectangle.split.2x1").foregroundStyle(.secondary)
                    Text(ProfileText.createHint + ": every account belongs to this one setup until you do.")
                        .font(.callout).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer()
                    Button("New profile", action: create).disabled(store.binaryPath == nil)
                }
            } else {
                if profile.signedIn == "no" { SignInRow(profile: profile) }
                PoolEditor(profile: profile)
                if let paths = profile.paths { FoldersField(profile: profile, paths: paths) }
                OverridesEditor(profile: profile)
                ProfileChromePicker(profile: profile)
            }
            HStack(spacing: 8) {
                Button("Open Claude Code") { store.openClaudeCode(profile.name) }
                    .disabled(store.binaryPath == nil || profile.signedIn == "no")
                    .help(profile.signedIn == "no" ? "\(profile.name) is not signed in: sign it in first"
                          : "Open Claude Code in \(profile.name)")
                if let a = profile.live {
                    Button("Open Chrome") { store.openChrome(a) }
                        .disabled(store.chrome?.supported == false)
                        .help("Open Chrome for \(a)")
                }
                Toggle("Shown in menu bar", isOn: Binding(
                    get: { followed },
                    set: { on in store.menuProfile = on ? profile.name : nil }))
                    .toggleStyle(.checkbox)
                    .tint(.csAccent)
                    .disabled(followed && profile.name == StateFile.defaultProfile)
                    .help("The menu-bar title follows one profile")
                Spacer()
                if removable {
                    Button("Remove profile…", role: .destructive) { removing = true }
                        .foregroundStyle(.red)
                        .disabled(store.binaryPath == nil)
                }
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.csAccent))
        .sheet(isPresented: $removing) { RemoveProfileSheet(profile: profile).environmentObject(store) }
    }
}

/// A profile with no login: sign it in with one of its accounts
/// (`profile seed`), the route Open Claude Code points to.
struct SignInRow: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo

    var body: some View {
        HStack(spacing: 8) {
            Label("Not signed in", systemImage: "person.crop.circle.badge.exclamationmark")
                .foregroundStyle(.orange)
            Spacer()
            Menu("Sign in with") {
                if profile.pool.isEmpty { Text("Add an account to \(profile.name) first") }
                ForEach(profile.pool, id: \.self) { id in
                    Button(id) {
                        let p = profile.name
                        store.act("seed:\(p)", title: "Could not sign \(p) in", profiles: true,
                                  { $0.profileSeed(p, account: id) }) { r in
                            store.note = "\(r.profile) is signed in with \(r.seeded)"
                        }
                    }
                }
            }
            .fixedSize()
            .disabled(store.binaryPath == nil || store.isBusy("seed:\(profile.name)"))
            if store.isBusy("seed:\(profile.name)") { ProgressView().controlSize(.small) }
        }
        .font(.callout)
    }
}

/// `profile remove`: where the profile's accounts go, what stays on disk,
/// and which account stays guarded (the ghost, PROFILES D22); then the
/// CLI's own confirmation text, then the removal.
struct RemoveProfileSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let profile: ProfileInfo
    @State private var to: String = ""
    @State private var confirm: String?
    @State private var error: AppError?

    var key: String { "remove:\(profile.name)" }
    var destinations: [String] {
        store.profiles.map { ProfileRemoval.destinations($0, removing: profile.name) } ?? []
    }
    /// Asked only when there are accounts to place; default always asks,
    /// since every account no profile lists is its own (the CLI needs --to).
    var needsDestination: Bool {
        ProfileRemoval.needsDestination(profile) || profile.name == StateFile.defaultProfile
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Remove profile \(profile.name)").font(.title3.bold())
            if needsDestination {
                Form {
                    Picker("Its accounts go to", selection: $to) {
                        if to.isEmpty { Text("Choose a profile").tag("") }
                        ForEach(destinations, id: \.self) { Text($0).tag($0) }
                    }
                }
            }
            Text(ProfileRemoval.summary(profile, to: to.isEmpty ? "the profile you choose" : to))
                .font(.callout)
                .fixedSize(horizontal: false, vertical: true)
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy(key) { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Remove…", role: .destructive) { ask() }
                    .buttonStyle(.borderedProminent)
                    .tint(.red)
                    .disabled((needsDestination && to.isEmpty) || store.isBusy(key))
            }
        }
        .padding(24)
        .frame(width: 480)
        .onAppear {
            if to.isEmpty, let list = store.profiles { to = ProfileRemoval.suggested(list, removing: profile.name) ?? "" }
        }
        .confirmationDialog("Remove profile \(profile.name)?",
                            isPresented: Binding(get: { confirm != nil }, set: { if !$0 { confirm = nil } })) {
            Button("Remove \(profile.name)", role: .destructive) { remove() }
        } message: {
            Text(confirm ?? "")
        }
    }

    /// Where the accounts go; none to name when it has none.
    var destination: String? { needsDestination && !to.isEmpty ? to : nil }

    /// The CLI's refusal without --yes says exactly what will happen,
    /// including the guard on the account last live there: that is the
    /// confirmation text (docs/APP_CLI.md, profile remove).
    func ask() {
        error = nil
        let (name, dest) = (profile.name, destination)
        Task {
            switch await store.call(key, { $0.profileRemove(name, to: dest, confirmed: false) }) {
            case .failure(.app(let e)) where e.code == "confirmation_required":
                confirm = e.message.prefix(1).uppercased() + e.message.dropFirst()
            case .failure(.app(let e)): error = e
            case .failure(let e): error = AppError(code: "failed", message: e.message, hint: e.hint)
            case .success(let r): done(r) // a CLI that removed without --yes
            }
        }
    }

    func remove() {
        let (name, dest) = (profile.name, destination)
        Task {
            switch await store.call(key, { $0.profileRemove(name, to: dest, confirmed: true) }) {
            case .success(let r): done(r)
            case .failure(.app(let e)): error = e
            case .failure(let e): error = AppError(code: "failed", message: e.message, hint: e.hint)
            }
        }
    }

    func done(_ r: ProfileRemoved) {
        var t = "Removed \(r.profile)"
        if let d = r.to, !r.moved.isEmpty { t += "; its accounts are in \(d)" }
        if let g = r.ghost { t += "; \(g.account) stays guarded until that login is gone" }
        if r.daemonRunning && r.daemonLoaded == false { t += " (the daemon has not picked it up yet)" }
        store.note = t
        if store.menuProfile == r.profile { store.menuProfile = nil }
        store.refresh(full: true, profiles: true)
        dismiss()
    }
}

struct SignInBadge: View {
    let signedIn: String
    let live: String?

    var body: some View {
        HStack(spacing: 8) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(text).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.tail)
        }
        .help(text)
        .accessibilityElement(children: .combine)
    }

    var text: String {
        switch signedIn {
        case "yes": return "signed in" + (live.map { " · " + $0 } ?? "")
        case "no": return "not signed in"
        default: return "sign-in unknown"
        }
    }

    var color: Color { signedIn == "yes" ? .green : signedIn == "no" ? .red : .gray }
}

/// The profile's accounts as chips, each with its status. A chip's menu
/// removes it (to default) or moves it; "+ Add" brings an account in. Every
/// way in is "Move to X", confirmed, whether a pool lists the account or
/// default holds it by D6 alone (owner decision).
struct PoolEditor: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo
    @State private var pending: PendingAction?

    var key: String { "pool:\(profile.name)" }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("ACCOUNTS").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            FlowRow {
                ForEach(profile.pool, id: \.self) { id in
                    let implicit = profile.isImplicitMember(id)
                    Menu {
                        if profile.name != StateFile.defaultProfile {
                            Button("Remove from \(profile.name)…") {
                                ask(Confirm.poolRemove(account: id, from: profile.name)) {
                                    store.removeFromPool(id, profile: profile.name, key: key)
                                }
                            }
                        }
                        ForEach(store.profiles?.moveTargets(for: id) ?? [], id: \.self) { o in
                            stepButton(id, to: o, label: "Move to \(o)…")
                        }
                    } label: {
                        Text(chip(id)).lineLimit(1)
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                    // The account's week and session beside its name.
                    .padding(.leading, 24)
                    .overlay(alignment: .leading) {
                        MiniRings(account: store.snapshot?.accounts.first { $0.id == id }, size: 19, dotRadius: 3)
                            .allowsHitTesting(false)
                    }
                    .padding(.leading, 6).padding(.trailing, 8).padding(.vertical, 5)
                    .background(PillShape().fill(id == profile.live ? Color.csAccent.opacity(0.12) : Color.clear))
                    .overlay(PillShape().strokeBorder(border(id),
                                                    style: StrokeStyle(lineWidth: 1, dash: implicit ? [3] : [])))
                    .help(implicit ? "\(id) is here because no profile lists it" : id)
                    .accessibilityLabel("\(chip(id)), \(id == profile.live ? "live, " : "")"
                                        + (implicit ? "here by default, " : "") + "account in \(profile.name)")
                }
                Menu {
                    let candidates = store.accountIDs.filter { !profile.pool.contains($0) }
                    if candidates.isEmpty { Text("Every account is in this profile") }
                    ForEach(candidates, id: \.self) { id in
                        stepButton(id, to: profile.name, label: nil)
                    }
                } label: {
                    Label("Add", systemImage: "plus")
                }
                .menuStyle(.borderlessButton)
                .fixedSize()
                .padding(.horizontal, 8).padding(.vertical, 4)
                .overlay(PillShape().strokeBorder(Color.secondary.opacity(0.4), style: StrokeStyle(lineWidth: 1, dash: [3])))
                .accessibilityLabel("Add an account to \(profile.name)")
                if store.isBusy(key) { ProgressView().controlSize(.small) }
            }
        }
        .confirming($pending)
    }

    /// "Move to X…" for any account not there yet, confirmed, then the
    /// edit the CLI needs (a move from the pool that lists it, or an add of
    /// one no pool lists).
    @ViewBuilder func stepButton(_ id: String, to target: String, label: String?) -> some View {
        let step = store.profiles?.step(adding: id, to: target) ?? .add(account: id, to: target)
        if let c = Confirm.poolStep(step) {
            Button(label ?? addLabel(id, step)) {
                ask(c) { store.movePool(step, key: key) }
            }
        }
    }

    func addLabel(_ id: String, _ step: PoolStep) -> String {
        if case .move(_, let from, _) = step { return "\(id) (in \(from))…" }
        return "\(id)…"
    }

    func ask(_ c: Confirmation, _ run: @escaping () -> Void) {
        pending = PendingAction(confirmation: c, run: run)
    }

    func chip(_ id: String) -> String {
        let v = store.snapshot?.accounts.first { $0.id == id }
        return Chip.text(id, status: v?.status, pct: v?.bindingPct, error: v?.lastError)
    }

    func border(_ id: String) -> Color {
        let v = store.snapshot?.accounts.first { $0.id == id }
        switch v?.status {
        case .needsLogin?, .refused?: return .red.opacity(0.7)
        default: break
        }
        if v?.lastError != nil { return .orange.opacity(0.7) }
        return id == profile.live ? Color.csAccent.opacity(0.5) : Color.secondary.opacity(0.3)
    }
}

/// The five per-profile overrides (`profile set`): empty inherits.
struct OverridesEditor: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo

    var body: some View {
        let keys = store.schema?.settings.filter(\.profileScoped) ?? []
        if !keys.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Text("THIS PROFILE'S THRESHOLDS").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                LazyVGrid(columns: [GridItem(.flexible(), spacing: 16), GridItem(.flexible(), spacing: 16),
                                    GridItem(.flexible(), spacing: 16)], alignment: .leading, spacing: 16) {
                    ForEach(keys) { s in
                        if s.type == "enum" {
                            EnumOverrideField(profile: profile, setting: s)
                        } else {
                            OverrideField(profile: profile, setting: s)
                        }
                    }
                }
            }
        }
    }
}

struct OverrideField: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo
    let setting: SettingSchema
    @State private var text = ""
    @State private var edited = false
    @State private var cliError: AppError?

    var override: String? { profile.overrides[setting.key] }
    var global: String { SettingNames.value(setting, store.values?.values[setting.key] ?? setting.defaultValue) }
    var local: String? { edited && !text.isEmpty ? setting.validate(text) : nil }
    var label: String { SettingNames.label(setting.key) }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label).font(.caption).lineLimit(1)
            HStack(spacing: 4) {
                TextField(label, text: Binding(get: { text }, set: { text = $0; edited = true; cliError = nil }),
                          prompt: Text(global + " (global)"))
                    .labelsHidden()
                    .textFieldStyle(.roundedBorder)
                    .onSubmit(save)
                    .accessibilityLabel("\(label) in \(profile.name)")
                if override != nil {
                    Button { text = ""; save() } label: { Image(systemName: "arrow.uturn.backward") }
                        .buttonStyle(.borderless)
                        .help("Inherit the global value (\(global))")
                        .accessibilityLabel("Inherit the global \(label)")
                }
                if store.isBusy("override:\(profile.name):\(setting.key)") { ProgressView().controlSize(.mini) }
            }
            if let e = local {
                InlineError(message: "Must be " + e, hint: "")
            } else if let e = cliError {
                InlineError(message: e.message, hint: e.hint)
            } else {
                Text(override == nil ? "inherits \(global)" : "overrides \(global)")
                    .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .onAppear { text = override ?? "" }
        .onChange(of: override) { v in if !edited { text = v ?? "" } }
    }

    func save() {
        guard local == nil, text != (override ?? "") else { return }
        let (p, k, v) = (profile.name, setting.key, text)
        Task {
            switch await store.call("override:\(p):\(k)", { $0.profileSet(p, k, v) }) {
            case .success:
                edited = false
                store.refresh(full: true, profiles: true)
            case .failure(.app(let e)): cliError = e
            case .failure(let e): cliError = AppError(code: "failed", message: e.message, hint: e.hint)
            }
        }
    }
}

/// C2: the Chrome profile Claude in Chrome is used from in this profile,
/// for its accounts without one of their own (`profile set <p> chrome`).
/// Names come from `chrome profiles --json`; the default is Chrome's last
/// used.
struct ProfileChromePicker: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo

    struct Option: Identifiable {
        let id: String
        let label: String
    }

    /// Chrome's profiles, plus the one set here when Chrome no longer lists
    /// it, so the picker can show it.
    var options: [Option] {
        var out = (store.chromeProfiles?.pickable ?? []).map { Option(id: $0.folder, label: $0.label) }
        if let c = profile.chrome, !out.contains(where: { $0.id == c }) {
            out.append(Option(id: c, label: profile.chromeName ?? c))
        }
        return out
    }

    var lastUsedTitle: String {
        "Chrome's last used" + (store.chromeProfiles?.lastUsedLabel.map { " (\($0))" } ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("CLAUDE IN CHROME").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Picker("Chrome profile", selection: Binding(
                    get: { profile.chrome ?? "" },
                    set: { v in
                        guard v != (profile.chrome ?? "") else { return }
                        store.setProfileChrome(profile.name, folder: v.isEmpty ? nil : v)
                    })) {
                    Text(lastUsedTitle).tag("")
                    ForEach(options) { o in Text(o.label).tag(o.id) }
                }
                .fixedSize()
                .disabled(store.binaryPath == nil || store.chrome?.supported == false)
                .accessibilityLabel("Chrome profile for \(profile.name)")
                if store.isBusy("chrome-profile:\(profile.name)") { ProgressView().controlSize(.mini) }
            }
            Text("Accounts without a Chrome profile of their own use this one. It needs signing in again after "
                 + "each rotation; give an account its own Chrome profile (Accounts) to have it follow by itself.")
                .font(.caption2).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

struct GhostsSection: View {
    @EnvironmentObject var store: Store
    let ghosts: [Ghost]
    @State private var pending: PendingAction?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Old profiles still holding a credential").font(.headline)
            Text("A removed or repointed profile's last login stays guarded until it is forgotten.")
                .font(.caption).foregroundStyle(.secondary)
            ForEach(ghosts) { g in
                HStack(spacing: 8) {
                    Text(g.profile).bold().lineLimit(1).truncationMode(.middle).help(g.profile)
                    Text("\(g.account) · \(g.why)" + (g.since.map { " · " + Format.age($0, now: Date()) } ?? ""))
                        .foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Button("Forget…") {
                        pending = PendingAction(confirmation: Confirm.forgetGhost(profile: g.profile, account: g.account)) {
                            store.act("forget:\(g.profile)", title: "Could not forget \(g.profile)", profiles: true,
                                      { $0.profileForget(g.profile) })
                        }
                    }
                    .disabled(store.isBusy("forget:\(g.profile)"))
                }
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.secondary.opacity(0.25)))
        .confirming($pending)
    }
}

/// `profile create`: name, directory (default ~/.claude-<name>), accounts
/// and an optional account to sign in with. Accounts another profile lists
/// are moved in after it is created, confirmed first (B4).
struct NewProfileSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var dir = ""
    @State private var pool: Set<String> = []
    @State private var seed: String = ""
    @State private var error: AppError?
    @State private var pending: PendingAction?

    static let labelWidth: CGFloat = 96

    var defaultDir: String { "~/.claude-" + (name.isEmpty ? "<name>" : name) }
    var selected: [String] { store.accountIDs.filter(pool.contains) }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("New profile").font(.title3.bold())
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 8) {
                GridRow {
                    Text("Name").frame(width: Self.labelWidth, alignment: .leading)
                    TextField("Name", text: $name, prompt: Text("e.g. review")).labelsHidden()
                        .textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("Directory").frame(width: Self.labelWidth, alignment: .leading)
                    HStack(spacing: 8) {
                        TextField("Directory", text: $dir, prompt: Text(defaultDir)).labelsHidden()
                            .textFieldStyle(.roundedBorder)
                        Button("Choose…", action: choose)
                    }
                }
                GridRow(alignment: .top) {
                    Text("Accounts").frame(width: Self.labelWidth, alignment: .leading)
                    accountList
                }
                GridRow {
                    Text("Sign in with").frame(width: Self.labelWidth, alignment: .leading)
                    Picker("Sign in with", selection: $seed) {
                        Text("None (sign in later)").tag("")
                        ForEach(selected, id: \.self) { Text($0).tag($0) }
                    }
                    .labelsHidden()
                }
            }
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy("create") { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Create") { ask() }
                    .keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(name.isEmpty || store.isBusy("create"))
            }
        }
        .padding(24)
        .frame(width: 520)
        .confirming($pending)
    }

    /// Every account, with where it is when it has to move; capped in
    /// height, scrolling past it.
    var accountList: some View {
        let ids = store.accountIDs
        let rows = VStack(alignment: .leading, spacing: 4) {
            if ids.isEmpty { Text("No accounts yet").foregroundStyle(.secondary) }
            ForEach(ids, id: \.self) { id in
                Toggle(isOn: Binding(get: { pool.contains(id) },
                                     set: { on in if on { pool.insert(id) } else { pool.remove(id); if seed == id { seed = "" } } })) {
                    HStack(spacing: 4) {
                        Text(id).lineLimit(1).truncationMode(.middle)
                        if let n = NewProfilePlan.note(for: id, list: store.profiles) {
                            Text(n).foregroundStyle(.secondary).lineLimit(1).truncationMode(.tail)
                        }
                    }
                    .help(id)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        return Group {
            if ids.count > 6 {
                ScrollView { rows.padding(8) }
                    .frame(height: 168)
                    .background(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.secondary.opacity(0.25)))
            } else {
                rows
            }
        }
    }

    func choose() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.showsHiddenFiles = true
        panel.directoryURL = URL(fileURLWithPath: NSHomeDirectory())
        if panel.runModal() == .OK, let u = panel.url { dir = u.path }
    }

    /// Moving accounts out of another profile is confirmed first.
    func ask() {
        let plan = NewProfilePlan.make(name: name, selected: selected, seed: seed.isEmpty ? nil : seed,
                                       list: store.profiles)
        if let c = plan.confirmation {
            pending = PendingAction(confirmation: c) { create(plan) }
        } else {
            create(plan)
        }
    }

    func create(_ plan: NewProfilePlan) {
        error = nil
        let d = dir.isEmpty ? nil : dir
        Task {
            switch await store.call("create", { $0.profileCreate(name: plan.name, dir: d, pool: plan.pool, seed: plan.seedAtCreate) }) {
            case .success:
                for step in plan.moves {
                    guard case .move(let a, let from, let to) = step else { continue }
                    if case .failure(let e) = await store.call("create", { $0.profilePool(from, .remove, account: a, to: to) }) {
                        fail("\(plan.name) was created, but \(a) could not be moved in: ", e)
                        return
                    }
                }
                if let s = plan.seedAfter,
                   case .failure(let e) = await store.call("create", { $0.profileSeed(plan.name, account: s) }) {
                    fail("\(plan.name) was created, but not signed in: ", e)
                    return
                }
                store.note = "Created \(plan.name)"
                store.refresh(full: true, profiles: true)
                dismiss()
            case .failure(.app(let e)) where e.code == "daemon_not_loaded":
                // The profile is made, not seeded, and nothing after it ran.
                store.refresh(full: true, profiles: true)
                error = AppError(code: e.code, message: plan.notLoadedMessage(e.message),
                                 hint: "Sign it in from its row in Profiles, or try again once the daemon runs.")
            case .failure(let e):
                error = AppError(code: e.code ?? "failed", message: e.message, hint: e.hint)
            }
        }
    }

    func fail(_ prefix: String, _ e: CallError, hint: String? = nil) {
        store.refresh(full: true, profiles: true)
        error = AppError(code: e.code ?? "failed", message: prefix + e.message, hint: hint ?? e.hint)
    }
}

/// Lays chips out in rows, wrapping at the available width.
struct FlowRow: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, row: CGFloat = 0, widest: CGFloat = 0
        for s in subviews {
            let sz = s.sizeThatFits(.unspecified)
            if x > 0 && x + sz.width > width { y += row + spacing; x = 0; row = 0 }
            x += sz.width + spacing
            row = max(row, sz.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: min(widest, width), height: y + row)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, row: CGFloat = 0
        for s in subviews {
            let sz = s.sizeThatFits(.unspecified)
            if x > bounds.minX && x + sz.width > bounds.maxX { y += row + spacing; x = bounds.minX; row = 0 }
            s.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(sz))
            x += sz.width + spacing
            row = max(row, sz.height)
        }
    }
}
