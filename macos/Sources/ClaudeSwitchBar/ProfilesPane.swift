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

/// A collapsed profile: name, directory, pools and thresholds, and Edit.
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
        parts.append(ProfilePools.summary(profile, pools: store.poolsUnsupported ? nil : store.pools)
                     ?? (profile.pool.isEmpty ? "no accounts" : profile.pool.joined(separator: ", ")))
        let t = profile.thresholds
        if let a = t.switchAt, let w = t.switchAtWeekly {
            parts.append(String(format: "switch at %g%% session, %g%% week", a, w))
        }
        return parts.joined(separator: " · ")
    }
}

/// An expanded profile: sign-in, the pools it uses (Change…), per-profile
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
                PoolsRow(profile: profile)
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
                    // Shared pools: one live in another profile cannot sign
                    // this one in (§3, one profile at a time).
                    let elsewhere = store.snapshot?.liveElsewhere(id, notIn: profile.name)
                    Button(id + (elsewhere.map { " — in use by \($0)" } ?? "")) {
                        let p = profile.name
                        store.act("seed:\(p)", title: "Could not sign \(p) in", profiles: true,
                                  { $0.profileSeed(p, account: id) }) { r in
                            store.note = "\(r.profile) is signed in with \(r.seeded)"
                        }
                    }
                    .disabled(elsewhere != nil)
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
    /// Asked only when it has accounts of its own to place (the pools it
    /// lists stay); default always asks, since every account no pool lists
    /// is its own (the CLI needs --to).
    var needsDestination: Bool {
        ProfileRemoval.needsDestination(profile, pools: store.pools) || profile.name == StateFile.defaultProfile
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
            Text(ProfileRemoval.summary(profile, to: to.isEmpty ? "the profile you choose" : to, pools: store.pools))
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

/// The pools a profile uses, numbered in the order it uses them (D33: an
/// account in a later pool only when nothing in an earlier one is usable),
/// and Change…. Default uses the accounts in no pool after them (D6). With a
/// binary from before pools, its accounts, read-only.
struct PoolsRow: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo
    @State private var changing = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(shown == nil ? "ACCOUNTS" : "POOLS").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            if let shown {
                HStack(alignment: .center, spacing: 8) {
                    FlowRow {
                        ForEach(Array(shown.enumerated()), id: \.element) { i, name in
                            PoolChip(n: i + 1, name: name, accounts: store.pools?.pool(name)?.accounts ?? [])
                        }
                        if unlisted > 0 {
                            Text((shown.isEmpty ? "" : "then ") + "\(unlisted) \(unlisted == 1 ? "account" : "accounts") in no pool")
                                .foregroundStyle(.secondary)
                                .padding(.vertical, 5)
                                .help(store.pools?.unlisted.joined(separator: ", ") ?? "")
                        }
                        if shown.isEmpty && unlisted == 0 {
                            Text("No pools yet").foregroundStyle(.secondary).padding(.vertical, 5)
                        }
                    }
                    Spacer()
                    if store.isBusy("pools:\(profile.name)") { ProgressView().controlSize(.small) }
                    Button("Change…") { changing = true }
                        .disabled(store.binaryPath == nil)
                        .help("Choose the pools \(profile.name) uses, and their order")
                }
            } else {
                Text(profile.pool.isEmpty ? "No accounts" : profile.pool.joined(separator: ", "))
                    .lineLimit(2).truncationMode(.tail)
                if store.poolsUnsupported {
                    Text("Update claudeswitch to group accounts into pools and share them between profiles.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .sheet(isPresented: $changing) { ChangePoolsSheet(profile: profile).environmentObject(store) }
    }

    var shown: [String]? { store.poolsUnsupported ? nil : ProfilePools.shown(profile, pools: store.pools) }

    /// Default's accounts in no pool, used after its pools.
    var unlisted: Int {
        ProfilePools.usesUnlisted(profile, pools: store.pools) ? store.pools?.unlisted.count ?? 0 : 0
    }
}

/// "1 personal": a pool's place in a profile's order, and its name; its
/// accounts on hover.
struct PoolChip: View {
    let n: Int
    let name: String
    let accounts: [String]

    var body: some View {
        HStack(spacing: 6) {
            Text("\(n)")
                .font(.caption.weight(.semibold)).monospacedDigit()
                .foregroundStyle(Color.csAccent)
                .frame(width: 18, height: 18)
                .background(Circle().fill(Color.csAccent.opacity(0.15)))
            Text(name).lineLimit(1).truncationMode(.middle)
        }
        .padding(.leading, 4).padding(.trailing, 10).padding(.vertical, 4)
        .overlay(PillShape().strokeBorder(Color.secondary.opacity(0.3)))
        .help(name + ": " + (accounts.isEmpty ? "no accounts" : accounts.joined(separator: ", ")))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Pool \(n): \(name), \(accounts.count) \(accounts.count == 1 ? "account" : "accounts")")
    }
}

/// The pools a profile may use: each with a checkbox, the checked ones
/// numbered in order with ↑ and ↓. Change… and New profile both use it.
struct PoolChooser: View {
    @Binding var choice: PoolChoice
    let pools: PoolList?
    /// The profile being edited: its own pool reads as such.
    var owner: String?

    var body: some View {
        let names = choice.order + choice.unchecked
        let rows = VStack(alignment: .leading, spacing: 6) {
            if names.isEmpty { Text("No pools yet: make one in Accounts.").foregroundStyle(.secondary) }
            ForEach(names, id: \.self) { name in
                HStack(spacing: 8) {
                    Toggle(name, isOn: Binding(get: { choice.isChecked(name) }, set: { _ in choice.toggle(name) }))
                        .toggleStyle(.checkbox).labelsHidden()
                    Text(choice.position(of: name).map(String.init) ?? "")
                        .font(.callout.weight(.semibold)).monospacedDigit().foregroundStyle(Color.csAccent)
                        .frame(width: 14, alignment: .trailing)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(name).lineLimit(1).truncationMode(.middle)
                        Text(caption(name)).font(.caption).foregroundStyle(.secondary)
                            .lineLimit(1).truncationMode(.tail).help(caption(name))
                    }
                    Spacer(minLength: 8)
                    if choice.isChecked(name) {
                        IconButton(symbol: "chevron.up", label: "Use \(name) earlier") { choice.move(name, by: -1) }
                            .disabled(choice.order.first == name)
                        IconButton(symbol: "chevron.down", label: "Use \(name) later") { choice.move(name, by: 1) }
                            .disabled(choice.order.last == name)
                    }
                }
                .accessibilityElement(children: .contain)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        if names.count > 5 {
            ScrollView { rows.padding(8) }
                .frame(height: 220)
                .background(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.secondary.opacity(0.25)))
        } else {
            rows
        }
    }

    /// "team-1, team-2 · used by default, work", or "work's own · w1".
    func caption(_ name: String) -> String {
        guard let p = pools?.pool(name) else { return "" }
        let accounts = p.accounts.isEmpty ? "no accounts" : p.accounts.joined(separator: ", ")
        if p.own { return (name == owner ? "its own" : "\(name)'s own") + " · " + accounts }
        let users = p.profiles.isEmpty ? "used by no profile" : "used by " + p.profiles.joined(separator: ", ")
        return accounts + " · " + users
    }
}

/// Change…: the pools a profile uses, in order (`profile pools`).
struct ChangePoolsSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let profile: ProfileInfo
    @State var choice = PoolChoice(all: [], chosen: [])
    @State var error: AppError?
    @State private var ready = false

    var key: String { "pools:\(profile.name)" }
    var current: [String] { ProfilePools.shown(profile, pools: store.pools) ?? [] }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Pools \(profile.name) uses").font(.title3.bold())
            Text("\(profile.name) uses the checked pools in this order: an account in a later pool only when "
                 + "nothing in an earlier one is usable. Profiles that use the same pool share its accounts, "
                 + "one profile at a time.")
                .font(.callout).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            PoolChooser(choice: $choice, pools: store.pools, owner: profile.name)
            if ProfilePools.usesUnlisted(profile, pools: store.pools), let n = store.pools?.unlisted.count {
                Text("Then the \(n == 1 ? "account" : "\(n) accounts") in no pool (\(store.pools?.unlisted.joined(separator: ", ") ?? "")), as always for \(profile.name).")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let n = ProfilePools.note(profile: profile.name, pools: store.pools, chosen: choice.order) {
                Label(n, systemImage: "info.circle").font(.caption).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy(key) { ProgressView().controlSize(.small) }
                if choice.order.isEmpty { Text("Check at least one pool.").font(.caption).foregroundStyle(.secondary) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Save", action: save).keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(choice.order.isEmpty || choice.order == current || store.isBusy(key))
            }
        }
        .padding(24)
        .frame(width: 520)
        .onAppear {
            guard !ready else { return }
            ready = true
            choice = PoolChoice(all: store.pools?.pools.map(\.name) ?? [], chosen: current)
        }
    }

    func save() {
        error = nil
        let (name, chosen) = (profile.name, choice.order)
        let steps = ProfilePools.steps(profile: name, pools: store.pools, chosen: chosen)
        Task {
            if let e = await store.setProfilePools(name, steps: steps) {
                error = AppError(code: e.code ?? "failed", message: e.message, hint: e.hint)
            } else {
                store.note = "\(name) uses " + chosen.joined(separator: ", ")
                dismiss()
            }
        }
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
/// in no pool to take as its own (`--pool`; with no [[profile]] blocks yet,
/// every account), the pools it uses, in order (`profile pools` once it
/// exists: it shares them, and nothing moves), and an optional account of
/// those to sign in with (`profile seed`).
struct NewProfileSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    @State var name = ""
    @State private var dir = ""
    @State var choice = PoolChoice(all: [], chosen: [])
    @State var own: Set<String> = []
    @State private var seed: String = ""
    @State private var error: AppError?
    @State private var ready = false

    static let labelWidth: CGFloat = 96

    var defaultDir: String { "~/.claude-" + (name.isEmpty ? "<name>" : name) }
    /// Pools need a binary that has them; without, the profile starts with none.
    var poolsKnown: Bool { store.pools != nil && !store.poolsUnsupported }
    /// The accounts in no pool, in rotation order: default's now (D6).
    var unlisted: [String] {
        guard poolsKnown, let l = store.pools?.unlisted else { return [] }
        return store.accountIDs.filter(l.contains) + l.filter { !store.accountIDs.contains($0) }
    }
    var ownChosen: [String] { unlisted.filter(own.contains) }
    /// No [[profile]] blocks yet: this is the first split.
    var firstSplit: Bool { !(store.profiles?.profiles.contains(where: \.declared) ?? true) }
    var candidates: [String] {
        NewProfilePlan.seedCandidates(pools: choice.order, own: ownChosen, list: store.pools) { store.liveIn($0) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("New profile").font(.title3.bold())
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 12) {
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
                if !unlisted.isEmpty {
                    GridRow(alignment: .top) {
                        Text("Its accounts").frame(width: Self.labelWidth, alignment: .leading)
                        VStack(alignment: .leading, spacing: 4) {
                            ownAccounts
                            Text("Accounts in no pool, which \(StateFile.defaultProfile) uses now: the ones checked "
                                 + "become this profile's own.")
                                .font(.caption).foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                // With no [[profile]] blocks yet there is nothing to share.
                if !(firstSplit && (store.pools?.pools.isEmpty ?? true)) {
                    GridRow(alignment: .top) {
                        Text("Pools").frame(width: Self.labelWidth, alignment: .leading)
                        if poolsKnown && !(store.pools?.pools.isEmpty ?? true) {
                            VStack(alignment: .leading, spacing: 4) {
                                PoolChooser(choice: $choice, pools: store.pools)
                                Text("It shares the pools checked with the profiles that use them, one profile at a time; "
                                     + "nothing moves." + (ownChosen.isEmpty ? "" : " Its own accounts come first."))
                                    .font(.caption).foregroundStyle(.secondary)
                                    .fixedSize(horizontal: false, vertical: true)
                                // Another profile's own pool becomes a shared one.
                                if let n = ProfilePools.note(profile: name, pools: store.pools, chosen: choice.order) {
                                    Label(n, systemImage: "info.circle").font(.caption).foregroundStyle(.orange)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                            }
                        } else {
                            Text(store.poolsUnsupported ? "Update claudeswitch to choose its pools here."
                                 : "No pools yet. To share accounts between profiles, make one in Accounts (New pool).")
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                GridRow {
                    Text("Sign in with").frame(width: Self.labelWidth, alignment: .leading)
                    Picker("Sign in with", selection: $seed) {
                        Text("None (sign in later)").tag("")
                        ForEach(candidates, id: \.self) { Text($0).tag($0) }
                    }
                    .labelsHidden()
                    .fixedSize()
                }
            }
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy("create") { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Create", action: create)
                    .keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(name.isEmpty || store.isBusy("create"))
            }
        }
        .padding(24)
        .frame(width: 560)
        .onAppear {
            guard !ready else { return }
            ready = true
            choice = PoolChoice(all: store.pools?.pools.map(\.name) ?? [], chosen: choice.order)
        }
        .onChange(of: choice) { _ in if !candidates.contains(seed) { seed = "" } }
        .onChange(of: own) { _ in if !candidates.contains(seed) { seed = "" } }
    }

    /// The accounts in no pool, each with a checkbox; capped in height.
    var ownAccounts: some View {
        let ids = unlisted
        let rows = VStack(alignment: .leading, spacing: 4) {
            ForEach(ids, id: \.self) { id in
                Toggle(isOn: Binding(get: { own.contains(id) },
                                     set: { on in if on { own.insert(id) } else { own.remove(id) } })) {
                    HStack(spacing: 4) {
                        Text(id).lineLimit(1).truncationMode(.middle)
                        if let p = store.liveIn(id) {
                            Text("in use by \(p)").foregroundStyle(.secondary).lineLimit(1)
                        }
                    }
                    .help(id)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        return Group {
            if ids.count > 5 {
                ScrollView { rows.padding(8) }
                    .frame(height: 140)
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

    func create() {
        error = nil
        let plan = NewProfilePlan.make(name: name, pools: poolsKnown ? choice.order : [], own: ownChosen,
                                       seed: seed.isEmpty ? nil : seed, candidates: candidates, unlisted: unlisted)
        let d = dir.isEmpty ? nil : dir
        Task {
            switch await store.call("create", { $0.profileCreate(name: plan.name, dir: d, pool: plan.own, seed: nil) }) {
            case .success:
                if !plan.pools.isEmpty,
                   case .failure(let e) = await store.call("create", { $0.profilePools(plan.name, plan.pools) }) {
                    fail("\(plan.name) was created, but not given its pools: ", e,
                         hint: e.hint.isEmpty ? "Choose them from its card in Profiles (Change…)." : e.hint)
                    return
                }
                if let s = plan.seed {
                    switch await store.call("create", { $0.profileSeed(plan.name, account: s) }) {
                    case .success: break
                    case .failure(.app(let e)) where e.code == "daemon_not_loaded":
                        fail("", .app(AppError(code: e.code, message: plan.notLoadedMessage(e.message))),
                             hint: "Sign it in from its card in Profiles, or try again once the daemon runs.")
                        return
                    case .failure(let e):
                        fail("\(plan.name) was created, but not signed in: ", e)
                        return
                    }
                }
                store.note = "Created \(plan.name)" + (plan.pools.isEmpty ? "" : "; it uses " + plan.pools.joined(separator: ", "))
                store.refresh(full: true, profiles: true)
                dismiss()
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
