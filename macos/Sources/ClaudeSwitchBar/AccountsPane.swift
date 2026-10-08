import AppKit
import ClaudeSwitchCore
import SwiftUI

/// Accounts: rotation order (drag, or Move up/down from the keyboard and
/// VoiceOver), and per account rename, move to a profile, sign in again,
/// Chrome, delete; then the recovery copies.
struct AccountsPane: View {
    @EnvironmentObject var store: Store
    @State private var renaming: String?
    @State private var deleting: DeleteAsk?
    @State private var pending: PendingAction?

    struct DeleteAsk: Identifiable {
        var id: String { account }
        var account: String
        var message: String
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            PaneHeader(title: "Accounts",
                       subtitle: "Drag to change the order rotation spends them in.") {
                Button("Add account") {
                    store.addAccount = AddAccountRequest(profile: AddForm.initialTarget(requested: nil, list: store.profiles))
                }
                .buttonStyle(PrimaryButtonStyle())
                .disabled(store.binaryPath == nil)
            }
            .padding([.horizontal, .top], 24)
            List {
                Section {
                    ForEach(store.accountIDs, id: \.self) { id in
                        AccountRow(id: id, rename: { renaming = id }, delete: { askDelete(id) },
                                   move: { step in askMove(step) }, shift: { by in shift(id, by: by) })
                    }
                    .onMove { from, to in
                        var ids = store.accountIDs
                        ids.move(fromOffsets: from, toOffset: to)
                        store.setPriority(ids)
                    }
                } header: {
                    HStack {
                        Text("Rotation order")
                        if store.isBusy("priority") { ProgressView().controlSize(.mini) }
                        Spacer()
                        AccountColumns(plan: "Plan")
                        Color.clear.frame(width: AccountColumns.trailing, height: 1)
                    }
                }
                RecoverySection()
            }
            .listStyle(.inset)
        }
        .sheet(item: Binding(get: { renaming.map(RenameTarget.init) }, set: { renaming = $0?.id })) { t in
            RenameSheet(old: t.id).environmentObject(store)
        }
        .confirmationDialog(deleting.map { "Delete \($0.account)?" } ?? "",
                            isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
                            presenting: deleting) { ask in
            Button("Delete \(ask.account)", role: .destructive) { delete(ask.account) }
        } message: { ask in
            Text(ask.message + "\n\nThis cannot be undone.")
        }
        .confirming($pending)
    }

    struct RenameTarget: Identifiable { var id: String }

    /// "Move to X", with the same confirmation and checks as the Profiles
    /// pane (owner decision): the CLI refuses a live account itself.
    func askMove(_ step: PoolStep) {
        guard let c = Confirm.poolStep(step) else { return }
        pending = PendingAction(confirmation: c) { store.movePool(step, key: "move:\(account(step))") }
    }

    func account(_ step: PoolStep) -> String {
        switch step {
        case .already: return ""
        case .add(let a, _), .move(let a, _, _): return a
        }
    }

    func shift(_ id: String, by: Int) {
        if let ids = RotationOrder.move(store.accountIDs, id, by: by) { store.setPriority(ids) }
    }

    /// The CLI's own refusal names the account and seat; that is the
    /// confirmation text (docs/APP_CLI.md, account delete).
    func askDelete(_ id: String) {
        Task {
            switch await store.call("delete:\(id)", { $0.accountDelete(id, confirmed: false) }) {
            case .failure(.app(let e)) where e.code == "confirmation_required":
                deleting = DeleteAsk(account: id, message: e.message)
            case .failure(let e):
                store.alert = deleteAlert(id, e)
            case .success:
                // A CLI that deleted without --yes: nothing to confirm any more.
                store.refresh(full: true, profiles: true)
            }
        }
    }

    func delete(_ id: String) {
        Task {
            switch await store.call("delete:\(id)", { $0.accountDelete(id, confirmed: true) }) {
            case .success(let r):
                var t = "Deleted \(r.account)"
                if let e = r.email { t += " (\(e))" }
                if let tw = r.twin { t += "; its credential lives on as \(tw)" }
                store.note = t
                store.refresh(full: true, profiles: true)
            case .failure(let e):
                store.alert = deleteAlert(id, e)
                store.refresh(full: true, profiles: true)
            }
        }
    }

    func deleteAlert(_ id: String, _ e: CallError) -> AlertItem {
        switch e.code {
        case "live":
            return AlertItem(title: "\(id) is in use", message: e.message,
                             hint: e.hint.isEmpty ? "Switch that profile to another account first." : e.hint)
        case "config_changed":
            return AlertItem(title: "The config changed meanwhile", message: e.message,
                             hint: e.hint.isEmpty ? "The other change and the credential were kept. Look at the config, then try again." : e.hint)
        case "daemon_not_loaded":
            return AlertItem(title: "The daemon did not pick up the change", message: e.message,
                             hint: e.hint.isEmpty ? "Nothing was deleted. Check the daemon in the Daemon pane, then try again." : e.hint)
        default:
            return AlertItem("Could not delete \(id)", e)
        }
    }
}

struct AccountRow: View {
    @EnvironmentObject var store: Store
    let id: String
    let rename: () -> Void
    let delete: () -> Void
    let move: (PoolStep) -> Void
    let shift: (Int) -> Void

    var view: AccountView? { store.snapshot?.accounts.first { $0.id == id } }
    var record: AccountRecord? { store.state?.accounts[id] }
    /// `account list --json`: the plan (lane 15).
    var info: AccountInfo? { store.accountList?.account(id) }
    var email: String? { store.state?.emails[id] ?? info?.email }

    var body: some View {
        HStack(spacing: 16) {
            Image(systemName: "line.3.horizontal").foregroundStyle(.tertiary).accessibilityHidden(true)
            MiniRings(account: view, size: 34)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(id).bold().lineLimit(1).truncationMode(.middle).help(id)
                    if let e = email {
                        Text(e).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle).help(e)
                    }
                    if let v = view { StatusChip(status: v.status, now: store.snapshot?.now ?? Date()).fixedSize() }
                }
                HStack(spacing: 0) {
                    Text(details).lineLimit(1).truncationMode(.middle)
                    if let l = loginText {
                        // Kept whole: when the login renews matters more than the seat.
                        Text((details.isEmpty ? "" : " · ") + l).lineLimit(1).fixedSize()
                    }
                }
                .font(.caption).foregroundStyle(.secondary)
                .help([details, loginText ?? ""].filter { !$0.isEmpty }.joined(separator: " · "))
            }
            Spacer(minLength: 8)
            if busy { ProgressView().controlSize(.small) }
            AccountColumns(plan: info?.planLabel ?? "—")
                .foregroundStyle(.secondary)
                .help(info?.plan ?? "No plan recorded yet")
            Menu {
                actions
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .accessibilityLabel("Actions for \(id)")
        }
        .padding(.vertical, 4)
        .contextMenu { actions }
        .accessibilityAction(named: "Move up") { shift(-1) }
        .accessibilityAction(named: "Move down") { shift(1) }
    }

    var busy: Bool {
        store.isBusy("delete:\(id)") || store.isBusy("chrome:\(id)") || store.isBusy("move:\(id)")
    }

    var targets: [String] { store.profiles?.moveTargets(for: id) ?? [] }

    @ViewBuilder var actions: some View {
        Button("Rename…", action: rename)
        Menu("Move to profile") {
            if targets.isEmpty { Text("No other profile") }
            ForEach(targets, id: \.self) { p in
                Button("Move to \(p)…") {
                    if let s = store.profiles?.step(adding: id, to: p) { move(s) }
                }
            }
        }
        .disabled(store.binaryPath == nil)
        Button("Sign in again…") { store.addAccount = AddAccountRequest(account: id, profile: store.pool(of: id)) }
        Divider()
        Button("Move up") { shift(-1) }
            .disabled(store.accountIDs.first == id)
        Button("Move down") { shift(1) }
            .disabled(store.accountIDs.last == id)
        Divider()
        Button("Open Chrome") { store.openChrome(id) }
            .disabled(store.chrome?.supported == false)
        Menu("Chrome profile: " + chromeLabel) {
            choiceItem("Same as its profile", selected: chromeChoice == .sameAsProfile) {
                store.setAccountChrome(id, .sameAsProfile)
            }
            let mine = store.chromeProfiles?.pickable ?? []
            if !mine.isEmpty {
                Section("Your Chrome profiles") {
                    ForEach(mine) { p in
                        choiceItem(p.label, selected: usesExisting(p.folder)) {
                            store.setAccountChrome(id, .existing(p.folder))
                        }
                    }
                }
            }
            if case .created(let f) = chromeChoice {
                choiceItem("Its own (\(store.chromeProfiles?.label(f) ?? f))", selected: true) {}
            }
            Divider()
            Button("Create a new one") { store.setAccountChrome(id, .createNew) }
        }
        .disabled(store.binaryPath == nil || store.chrome?.supported == false)
        Divider()
        Button("Delete…", role: .destructive, action: delete)
    }

    /// C2: the account's Chrome choice, as the CLI last reported it.
    var chromeChoice: AccountChrome { store.chrome?.choice(for: id) ?? .sameAsProfile }

    func usesExisting(_ folder: String) -> Bool {
        if case .existing(let f, _) = chromeChoice { return f == folder }
        return false
    }

    var chromeLabel: String {
        switch chromeChoice {
        case .sameAsProfile: return "same as its profile"
        case .existing(let f, let n): return n ?? store.chromeProfiles?.label(f) ?? f
        case .created(let f): return store.chromeProfiles?.label(f) ?? f
        }
    }

    /// A menu item with a checkmark when it is the current choice.
    func choiceItem(_ title: String, selected: Bool, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            if selected { Label(title, systemImage: "checkmark") } else { Text(title) }
        }
    }

    /// Profile, seat and figure: shortened in the middle when the row is
    /// narrow.
    var details: String {
        var parts: [String] = []
        if let p = store.pool(of: id) { parts.append("profile " + p) }
        if chromeChoice != .sameAsProfile { parts.append("Chrome " + chromeLabel) }
        if let s = record?.seat { parts.append("seat " + shortSeat(s)) }
        if let v = view, let p = v.bindingPct { parts.append(Format.pct(p) + " " + Format.window(v.bindingWindow)) }
        return parts.joined(separator: " · ")
    }

    var loginText: String? {
        if let r = record?.refreshExpiry {
            return (r > Date() ? "login renews until " : "login expired ") + r.formatted(date: .abbreviated, time: .shortened)
        }
        if let t = record?.tokenExpiry {
            return "token " + (t > Date() ? "valid until " : "expired ") + t.formatted(date: .abbreviated, time: .shortened)
        }
        return nil
    }

    func shortSeat(_ s: String) -> String {
        s.split(separator: "@").map { String($0.prefix(8)) }.joined(separator: "@")
    }
}

/// The Plan column of the Accounts pane, the header's and each row's laid
/// out alike.
struct AccountColumns: View {
    /// The width of what follows the columns in a row (its actions menu).
    static let trailing: CGFloat = 28
    let plan: String

    var body: some View {
        Text(plan).lineLimit(1).truncationMode(.tail).frame(width: 112, alignment: .leading)
            .font(.callout)
    }
}

struct StatusChip: View {
    let status: AccountStatus
    let now: Date

    var body: some View {
        Text(Format.status(status, now: now))
            .font(.caption2)
            .padding(.horizontal, 6).padding(.vertical, 1)
            .background(PillShape().fill(color.opacity(0.18)))
            .foregroundStyle(color)
    }

    var color: Color {
        switch status {
        case .available: return .green
        case .noHeadroom, .windowReset: return .orange
        case .refused, .needsLogin: return .red
        case .unknown: return .secondary
        }
    }
}

struct RenameSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let old: String
    @State private var name = ""
    @State private var error: AppError?

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Rename \(old)").font(.title3.bold())
            TextField("New name", text: $name).textFieldStyle(.roundedBorder).onSubmit(save)
            Text("Renaming needs the daemon stopped (Daemon pane); the CLI says so if it runs.")
                .font(.caption).foregroundStyle(.secondary)
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Rename", action: save).keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(name.isEmpty || name == old || store.isBusy("rename"))
            }
        }
        .padding(24)
        .frame(width: 400)
        .onAppear { name = old }
    }

    func save() {
        let (o, n) = (old, name)
        Task {
            switch await store.call("rename", { $0.accountRename(o, to: n) }) {
            case .success:
                store.refresh(full: true, profiles: true)
                dismiss()
            case .failure(.app(let e)): error = e
            case .failure(let e): error = AppError(code: "failed", message: e.message, hint: e.hint)
            }
        }
    }
}

/// Kept copies of credentials (`recovery --json`): restore one to an
/// account, or clear it.
struct RecoverySection: View {
    @EnvironmentObject var store: Store
    @State private var pending: PendingAction?

    var body: some View {
        Section {
            if let items = store.recovery {
                if items.isEmpty { Text("No recovery copies.").foregroundStyle(.secondary) }
                ForEach(items) { item in
                    HStack(spacing: 12) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(item.slot).bold() + Text(item.account.map { " · " + $0 } ?? "").foregroundColor(.secondary)
                            Text(describe(item)).font(.caption).foregroundStyle(item.error == nil ? Color.secondary : Color.red)
                        }
                        Spacer()
                        Menu("Restore") {
                            ForEach(store.accountIDs, id: \.self) { id in
                                Button(id == item.account ? "\(id) (its account)" : id) { askRestore(item, id) }
                            }
                        }
                        .fixedSize()
                        .disabled(item.error != nil)
                        Button("Clear…") { clear(item) }
                    }
                }
            } else {
                Text("Copies of credentials kept when a sign-in replaced them.").foregroundStyle(.secondary)
            }
        } header: {
            HStack {
                Text("Recovery copies")
                Spacer()
                if store.isBusy("recovery") { ProgressView().controlSize(.mini) }
                Button(store.recovery == nil ? "Show" : "Reload") { store.loadRecovery(identify: false) }
                    .buttonStyle(.borderless)
                Button("Identify") { store.loadRecovery(identify: true) }
                    .buttonStyle(.borderless)
                    .help("Look up who each copy signs in as (one identity call each)")
            }
        }
        // The action carries its own slot: a dialog clears its binding before
        // the button runs, so reading state there would find nothing.
        .confirming($pending)
    }

    func clear(_ item: RecoveryItem) {
        let s = item.slot
        pending = PendingAction(confirmation: Confirm.recoveryClear(slot: s, account: item.account)) {
            store.act("recovery", title: "Could not clear \(s)", { $0.recoveryClear(s) }) { _ in
                store.loadRecovery(identify: false)
            }
        }
    }

    func describe(_ i: RecoveryItem) -> String {
        if let e = i.error { return e }
        var p: [String] = []
        if let pr = i.profile { p.append("from " + pr) }
        if let k = i.keptAt { p.append("kept " + Format.age(k, now: Date())) }
        if let w = i.who { p.append(w) } else if let s = i.seat { p.append("seat " + s.prefix(8) + "…") }
        p.append(i.renewable ? "renewable" : "not renewable")
        return p.joined(separator: " · ")
    }

    func askRestore(_ item: RecoveryItem, _ account: String) {
        let slot = item.slot
        pending = PendingAction(confirmation: Confirm.recoveryRestore(slot: slot, to: account, itsAccount: item.account)) {
            restore(slot, account, force: false)
        }
    }

    func restore(_ slot: String, _ account: String, force: Bool) {
        store.act("recovery", title: "Could not restore \(slot)", { $0.recoveryRestore(slot, account: account, force: force) }) { r in
            store.note = "Restored \(r.slot) to \(r.account)"
            store.loadRecovery(identify: false)
        }
    }
}
