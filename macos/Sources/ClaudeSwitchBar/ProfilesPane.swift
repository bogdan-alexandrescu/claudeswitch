import AppKit
import ClaudeSwitchCore
import SwiftUI

/// Profiles: each is a Claude Code setup with its own login and the pools
/// of accounts it uses, which profiles can share (docs/PROFILES.md §10).
struct ProfilesPane: View {
    @EnvironmentObject var store: Store
    @State private var creating = false
    @State private var editing: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                PaneHeader(title: "Profiles",
                           subtitle: "Each profile is a Claude Code setup with its own login and pools it can share.") {
                    Button("New Profile") { creating = true }
                        .disabled(store.binaryPath == nil)
                }
                if let e = store.profilesError {
                    InlineError(message: "Could not read the profiles: " + e.message, hint: e.hint)
                }
                // Said once here, not in every card.
                if store.poolsUnsupported && store.profiles != nil {
                    Text(ProfilesText.poolsNeedUpdate)
                        .font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if let list = store.profiles {
                    ForEach(list.profiles) { p in
                        if editing == p.name || list.profiles.count == 1 {
                            ProfileEditor(profile: p, done: list.profiles.count == 1 ? nil : { editing = nil })
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

/// A collapsed profile: name, directory, pools and thresholds, whether it
/// is signed in (so one that needs sign-in is found closed), and Edit.
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
            SignInBadge(signedIn: profile.signedIn, live: profile.live)
            Button("Edit", action: edit)
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.secondary.opacity(0.25)))
    }

    var summary: String { ProfilesText.closedLine(profile, pools: store.poolsUnsupported ? nil : store.pools) }
}

/// An expanded profile: sign-in, the pools it uses (Change…), per-profile
/// thresholds, and its actions in one row.
struct ProfileEditor: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo
    var done: (() -> Void)?
    @State private var removing = false

    var removable: Bool { store.profiles.map { ProfileRemoval.canRemove($0, profile.name) } ?? false }
    var followed: Bool { store.snapshot?.followed(store.menuProfile) == profile.name }
    /// The live account's readings, for the rings beside the name.
    var live: AccountView? { profile.live.flatMap { id in store.snapshot?.accounts.first { $0.id == id } } }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 8) {
                MiniRings(account: live, size: 36, dotRadius: 2.4)
                // The name keeps its room; a long directory gives way first.
                Text(profile.name).font(.headline).lineLimit(1).truncationMode(.tail).help(profile.name)
                    .layoutPriority(1)
                Text(ProfilesText.dir(profile.dir)).font(.caption).foregroundStyle(.secondary)
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
                // no accounts or thresholds of its own to edit (B7). The
                // pane's own New profile, above, is the one button for it.
                HStack(spacing: 8) {
                    Image(systemName: "rectangle.split.2x1").foregroundStyle(.secondary)
                    Text(ProfilesText.implicitNote)
                        .font(.callout).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer()
                }
            } else {
                if profile.signedIn == "no" { SignInRow(profile: profile) }
                PoolsRow(profile: profile)
                if let paths = profile.paths { FoldersField(profile: profile, paths: paths) }
                OverridesEditor(profile: profile)
                ProfileChromePicker(profile: profile)
            }
            // One row while it fits; at the window's least width Remove
            // profile… takes a line of its own rather than squeeze "Shown
            // in menu bar" onto two.
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 8) {
                    actions
                    Spacer(minLength: 8)
                    removeButton
                }
                VStack(alignment: .leading, spacing: 8) {
                    HStack(spacing: 8) {
                        actions
                        Spacer(minLength: 0)
                    }
                    HStack {
                        Spacer()
                        removeButton
                    }
                }
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.csAccent))
        .sheet(isPresented: $removing) { RemoveProfileSheet(profile: profile).environmentObject(store) }
    }

    /// Open Claude Code, Open Chrome and Shown in menu bar, each on one line.
    @ViewBuilder var actions: some View {
        // Off while one opens, so a double click opens one, as the popover
        // card's ▶ does.
        let running = store.isBusy("run:\(profile.name)")
        Button("Open Claude Code") { if !running { store.openClaudeCode(profile.name) } }
            .disabled(store.binaryPath == nil || profile.signedIn == "no" || running)
            .help(ProfilesText.openHelp(profile.name, signedIn: profile.signedIn))
            .fixedSize()
        if let a = profile.live {
            let opening = store.isBusy("chrome:\(a)")
            Button("Open Chrome") { if !opening { store.openChrome(a) } }
                .disabled(store.chrome?.supported == false || opening)
                .help("Open Chrome for \(ProfilesText.q(a))")
                .fixedSize()
        }
        // The help sits on a wrapper: a disabled control shows none.
        HStack {
            Toggle("Shown in menu bar", isOn: Binding(
                get: { followed },
                set: { on in store.menuProfile = on ? profile.name : nil }))
                .toggleStyle(.checkbox)
                .tint(.csAccent)
                .lineLimit(1)
                .fixedSize()
                .disabled(followed && profile.name == StateFile.defaultProfile)
        }
        .help(ProfilesText.menuBarHelp(followed: followed, name: profile.name))
    }

    @ViewBuilder var removeButton: some View {
        if removable {
            Button("Remove Profile…", role: .destructive) { removing = true }
                .foregroundStyle(Color.csBad)
                .fixedSize()
                .disabled(store.binaryPath == nil)
        }
    }
}

/// A profile that needs sign-in: Sign in… with one of its accounts
/// (`profile seed`), the route Open Claude Code points to. The badge by
/// the name says the state, so this row says only the action.
struct SignInRow: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo

    var body: some View {
        HStack(spacing: 8) {
            Menu(ProfilesText.signInAction) {
                if profile.pool.isEmpty { Text(ProfilesText.noAccountToSignIn(profile.name)) }
                ForEach(profile.pool, id: \.self) { id in
                    // Shared pools: one live in another profile cannot sign
                    // this one in (§3, one profile at a time).
                    let elsewhere = store.snapshot?.liveElsewhere(id, notIn: profile.name)
                    Button(ProfilesText.signInWith(id, elsewhere: elsewhere)) {
                        let p = profile.name
                        store.act("seed:\(p)", title: ProfilesText.signInFailed(p), profiles: true,
                                  { $0.profileSeed(p, account: id) }, done: { r in
                            store.note = ProfilesText.signedIn(profile: r.profile, account: r.seeded)
                        })
                    }
                    .disabled(elsewhere != nil)
                }
            }
            .fixedSize()
            .disabled(store.binaryPath == nil || store.isBusy("seed:\(profile.name)"))
            if store.isBusy("seed:\(profile.name)") { ProgressView().controlSize(.small) }
            Spacer()
        }
        .font(.callout)
    }
}

/// `profile remove`: what removing does, where the profile's accounts go,
/// what stays on disk, and which account stays guarded (the ghost,
/// PROFILES D22). The sheet is the confirmation: its button removes.
struct RemoveProfileSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let profile: ProfileInfo
    @State private var to: String = ""
    @State private var error: AppError?

    var key: String { "remove:\(profile.name)" }
    var destinations: [String] {
        store.profiles.map { ProfileRemoval.destinations($0, removing: profile.name) } ?? []
    }
    /// Asked only when it has accounts of its own to place (the pools it
    /// lists stay); default always asks, since every account no pool lists
    /// is its own (the CLI needs --to), and the summary says why.
    var needsDestination: Bool { ProfileRemoval.needsDestination(profile, pools: store.pools) }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(ProfilesText.removeTitle(profile.name)).font(.title3.bold())
            if needsDestination {
                Form {
                    Picker(ProfileRemoval.destinationLabel(profile, pools: store.pools), selection: $to) {
                        if to.isEmpty { Text("Choose a profile").tag("") }
                        ForEach(destinations, id: \.self) { Text($0).tag($0) }
                    }
                }
            }
            Text(ProfileRemoval.summary(profile, to: to.isEmpty ? nil : to, pools: store.pools))
                .font(.callout)
                .fixedSize(horizontal: false, vertical: true)
            // A pool only this profile uses is left to no profile.
            if let w = ProfileRemoval.unusedAfter(profile, pools: store.pools) {
                Label(w, systemImage: "exclamationmark.triangle")
                    .font(.callout).foregroundStyle(Color.csWarn)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy(key) { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(ProfilesText.removeButton(profile.name), role: .destructive, action: remove)
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
    }

    /// Where the accounts go; none to name when it has none.
    var destination: String? { needsDestination && !to.isEmpty ? to : nil }

    /// The sheet has said what happens, so it removes with --yes: a second
    /// dialog with the CLI's own text said the same again.
    func remove() {
        error = nil
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
        store.note = ProfilesText.removed(r)
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

    var text: String { ProfilesText.signIn(signedIn, live: live) }

    var color: Color { signedIn == "yes" ? .csGood : signedIn == "no" ? .csBad : .gray }
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
                            // This profile uses none; the set may have some.
                            Text(ProfilesText.usesNoPool).foregroundStyle(.secondary).padding(.vertical, 5)
                        }
                    }
                    Spacer()
                    if store.isBusy("pools:\(profile.name)") { ProgressView().controlSize(.small) }
                    Button("Change…") { changing = true }
                        .disabled(store.binaryPath == nil)
                        .help(ProfilesText.changeHelp(profile.name))
                }
            } else {
                // An older CLI: the pane's top says once to update.
                Text(profile.pool.isEmpty ? "No accounts" : profile.pool.joined(separator: ", "))
                    .lineLimit(2).truncationMode(.tail)
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

/// The pools a profile may use: each with a checkbox whose label is the
/// pool, so a click on the name ticks it; once one is checked they are
/// numbered in order, and with two or more ↑ and ↓ order them. Change… and
/// New profile both use it.
struct PoolChooser: View {
    @Binding var choice: PoolChoice
    let pools: PoolList?
    /// The profile being edited: its own pool reads as such.
    var owner: String?

    var body: some View {
        let names = choice.order + choice.unchecked
        let rows = VStack(alignment: .leading, spacing: 6) {
            if names.isEmpty {
                Text("No pools yet: make one in Accounts.").foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            ForEach(names, id: \.self) { name in
                let caption = ProfilesText.poolCaption(name, pools: pools, owner: owner)
                HStack(spacing: 8) {
                    Toggle(isOn: Binding(get: { choice.isChecked(name) }, set: { _ in choice.toggle(name) })) {
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            // The order's column, kept even before there is
                            // an order, so the rows do not jump right when
                            // the first box is checked.
                            Text(choice.position(of: name).map(String.init) ?? "")
                                .font(.callout.weight(.semibold)).monospacedDigit().foregroundStyle(Color.csAccent)
                                .frame(width: 14, alignment: .trailing)
                                .accessibilityHidden(choice.position(of: name) == nil)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(name).lineLimit(1).truncationMode(.middle)
                                Text(caption).font(.caption).foregroundStyle(.secondary)
                                    .lineLimit(1).truncationMode(.tail).help(caption)
                            }
                        }
                    }
                    .toggleStyle(.checkbox)
                    Spacer(minLength: 8)
                    if choice.reorderable && choice.isChecked(name) {
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
            Text(ProfilesText.changeTitle(profile.name)).font(.title3.bold())
            Text(ProfilesText.changeIntro(profile.name))
                .font(.callout).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            PoolChooser(choice: $choice, pools: store.pools, owner: profile.name)
            if ProfilePools.usesUnlisted(profile, pools: store.pools), let ids = store.pools?.unlisted {
                Text(ProfilesText.thenUnlisted(ids, profile: profile.name))
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            // What else the change does: a note, so not drawn as a warning.
            if let n = ProfilePools.note(profile: profile.name, pools: store.pools, chosen: choice.order) {
                Label(n, systemImage: "info.circle").font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy(key) { ProgressView().controlSize(.small) }
                if choice.order.isEmpty { Text(ProfilesText.checkOne).font(.caption).foregroundStyle(.secondary) }
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
                store.note = ProfilesText.poolsSaved(name, chosen)
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
                Text("THIS PROFILE’S THRESHOLDS").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
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
    /// The value a save is running for.
    @State private var sending: String?
    @FocusState private var focused: Bool

    var override: String? { profile.overrides[setting.key] }
    /// The global value as the CLI reports it; nil until the values are read.
    var global: String? { store.values.map { $0.values[setting.key] ?? setting.defaultValue } }
    var local: String? { edited && !text.isEmpty ? setting.validate(text) : nil }
    var label: String { SettingNames.label(setting.key) }
    var key: String { "override:\(profile.name):\(setting.key)" }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label).font(.caption).lineLimit(1)
            HStack(spacing: 4) {
                TextField(label, text: Binding(get: { text }, set: { text = $0; edited = true; cliError = nil }),
                          prompt: Text(ProfilesText.placeholder(setting, global: global)))
                    .labelsHidden()
                    .textFieldStyle(.roundedBorder)
                    .focused($focused)
                    .onSubmit(save)
                    .accessibilityLabel("\(label) in \(profile.name)")
                if let u = ProfilesText.unit(setting) { Text(u).foregroundStyle(.secondary) }
                // ↺'s place is kept, so the field does not shrink once it
                // overrides; the spinner takes it while a save runs.
                ZStack {
                    Button { text = ""; save() } label: { Image(systemName: "arrow.uturn.backward") }
                        .buttonStyle(.borderless)
                        .help(ProfilesText.inheritHelp(setting, global: global))
                        .accessibilityLabel("Inherit the global \(label)")
                        .opacity(override == nil || store.isBusy(key) ? 0 : 1)
                        .disabled(override == nil || store.isBusy(key))
                        .accessibilityHidden(override == nil)
                    if store.isBusy(key) { ProgressView().controlSize(.mini) }
                }
            }
            if let e = local {
                InlineError(message: "Must be " + e, hint: "")
            } else if let e = cliError {
                InlineError(message: e.message, hint: e.hint)
            } else {
                Text(ProfilesText.overrideCaption(setting, overridden: override != nil, global: global))
                    .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .onAppear { text = override ?? "" }
        .onChange(of: override) { v in if !edited { text = v ?? "" } }
        // A value typed and left is saved, as Return saves it: clicking
        // away lost it.
        .onChange(of: focused) { f in if !f { save() } }
        // Collapsing the card or closing Settings removes the field before
        // its focus moves: save what was typed then too.
        .onDisappear(perform: save)
    }

    /// Saves a valid, changed value; empty inherits. Once per value: losing
    /// focus and going away can both ask.
    func save() {
        guard local == nil, text != (override ?? ""), !store.isBusy(key), sending != text else { return }
        let (p, k, v) = (profile.name, setting.key, text)
        sending = v
        Task {
            switch await store.call(key, { $0.profileSet(p, k, v) }) {
            case .success:
                edited = false
                store.refresh(full: true, profiles: true)
            case .failure(.app(let e)): cliError = e
            case .failure(let e): cliError = AppError(code: "failed", message: e.message, hint: e.hint)
            }
            sending = nil
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
        "Chrome’s last used" + (store.chromeProfiles?.lastUsedLabel.map { " (\($0))" } ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("CLAUDE IN CHROME").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            HStack(spacing: 8) {
                // A caption, as every other field label on the card.
                Text("Chrome profile").font(.caption)
                Picker("Chrome profile", selection: Binding(
                    get: { profile.chrome ?? "" },
                    set: { v in
                        guard v != (profile.chrome ?? "") else { return }
                        store.setProfileChrome(profile.name, folder: v.isEmpty ? nil : v)
                    })) {
                    Text(lastUsedTitle).tag("")
                    ForEach(options) { o in Text(o.label).tag(o.id) }
                }
                .labelsHidden()
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
            Text("OLD PROFILES STILL HOLDING A CREDENTIAL").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            Text("A removed or repointed profile’s last login stays guarded until it is forgotten.")
                .font(.caption).foregroundStyle(.secondary)
            ForEach(ghosts) { g in
                HStack(spacing: 8) {
                    Text(g.profile).bold().lineLimit(1).truncationMode(.middle).help(g.profile)
                    Text("\(g.account) · \(g.why)" + (g.since.map { " · " + Format.age($0, now: store.now) } ?? ""))
                        .foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Button("Forget…") {
                        pending = PendingAction(confirmation: Confirm.forgetGhost(profile: g.profile, account: g.account)) {
                            store.act("forget:\(g.profile)", title: "Could not forget \(ProfilesText.q(g.profile))", profiles: true,
                                      { $0.profileForget(g.profile) }, done: { _ in
                                store.note = ProfilesText.forgot(profile: g.profile, account: g.account)
                            })
                        }
                    }
                    .disabled(store.isBusy("forget:\(g.profile)"))
                }
            }
        }
        .padding(.top, 8)
        .confirming($pending)
    }
}

/// `profile create`: name, directory (default ~/.claude-<name>), accounts
/// in no pool to take as its own (`--pool`; with no [[profile]] blocks yet,
/// every account), the pools it uses, in order (`profile pools` once it
/// exists: it shares them, and nothing moves), and an optional account of
/// those to sign in with (`profile seed`). Once the profile exists, a step
/// that fails turns the sheet into what was done, with Retry for what is
/// left and Close.
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
    @State private var run: NewProfileRun?

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
    /// The Pools row offers pools to check.
    var offersPools: Bool { poolsKnown && !(store.pools?.pools.isEmpty ?? true) }
    /// What the sheet says when the profile would start with no accounts.
    var startsEmpty: String? {
        ProfilesText.startsEmpty(own: ownChosen, chosen: poolsKnown ? choice.order : [], pools: store.pools,
                                 offersAccounts: !unlisted.isEmpty, offersPools: offersPools)
    }

    var body: some View {
        Group {
            if let r = run, r.interrupted { interrupted(r) } else { form }
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

    /// The profile exists and a later step failed: what was done, the
    /// CLI's refusal, Retry for what is left, and Close.
    func interrupted(_ r: NewProfileRun) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(r.headline ?? "").font(.title3.bold()).fixedSize(horizontal: false, vertical: true)
            if let f = r.failure { InlineError(message: f.message, hint: f.hint) }
            HStack(spacing: 8) {
                if store.isBusy("create") { ProgressView().controlSize(.small) }
                Spacer()
                Button("Close") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Retry", action: proceed)
                    .keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(store.isBusy("create"))
            }
        }
    }

    var form: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("New Profile").font(.title3.bold())
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 12) {
                GridRow {
                    Text("Name").frame(width: Self.labelWidth, alignment: .leading)
                    TextField("Name", text: $name, prompt: Text(ProfilesText.namePrompt)).labelsHidden()
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
                            Text(ProfilesText.newProfileOwn)
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
                                Text(ProfilesText.newProfilePools(numbered: choice.numbered, ownFirst: !ownChosen.isEmpty))
                                    .font(.caption).foregroundStyle(.secondary)
                                    .fixedSize(horizontal: false, vertical: true)
                                // Another profile's own pool becomes a shared one: a note.
                                if let n = ProfilePools.note(profile: name, pools: store.pools, chosen: choice.order) {
                                    Label(n, systemImage: "info.circle").font(.caption).foregroundStyle(.secondary)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                            }
                        } else {
                            // Full width: measured narrower, the wrapped
                            // text reserved an empty second line.
                            Text(store.poolsUnsupported ? "Update claudeswitch to choose its pools here."
                                 : "No pools yet. To share accounts between profiles, make one in Accounts (New Pool).")
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                                .frame(maxWidth: .infinity, alignment: .leading)
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
            // Allowed, but said: nothing checked gives it an account.
            if let n = startsEmpty {
                Label(n, systemImage: "info.circle").font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy("create") { ProgressView().controlSize(.small) }
                // Why Create is off.
                if name.isEmpty { Text(ProfilesText.enterName).font(.caption).foregroundStyle(.secondary) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Create", action: create)
                    .keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(name.isEmpty || store.isBusy("create"))
            }
        }
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
                            Text(ProfilesText.inUseBy(p)).foregroundStyle(.secondary).lineLimit(1)
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

    /// Create: a new run from the form. Until `profile create` works the
    /// form stays, and Create starts over.
    func create() {
        error = nil
        let plan = NewProfilePlan.make(name: name, pools: poolsKnown ? choice.order : [], own: ownChosen,
                                       seed: seed.isEmpty ? nil : seed, candidates: candidates, unlisted: unlisted)
        run = NewProfileRun(plan: plan)
        proceed()
    }

    /// Runs what is left of the run, in order, and stops at the first
    /// refusal; Retry runs it again from there.
    func proceed() {
        guard run != nil, !store.isBusy("create") else { return }
        let d = dir.isEmpty ? nil : dir
        Task {
            guard var r = run else { return }
            let plan = r.plan
            for step in r.remaining {
                let result: Result<Void, CallError>
                switch step {
                case .create:
                    result = await store.call("create", { $0.profileCreate(name: plan.name, dir: d, pool: plan.own, seed: nil) })
                        .map { _ in () }
                case .pools:
                    result = await store.call("create", { $0.profilePools(plan.name, plan.pools) }).map { _ in () }
                case .seed:
                    guard let s = plan.seed else { continue }
                    result = await store.call("create", { $0.profileSeed(plan.name, account: s) }).map { _ in () }
                }
                switch result {
                case .success:
                    r.succeeded(step)
                    run = r
                case .failure(let e):
                    r.failed(step, code: e.code, message: e.message, hint: e.hint)
                    run = r
                    if r.created { store.refresh(full: true, profiles: true) }
                    if !r.interrupted { error = AppError(code: e.code ?? "failed", message: e.message, hint: e.hint) }
                    return
                }
            }
            store.note = r.note
            store.refresh(full: true, profiles: true)
            dismiss()
        }
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
