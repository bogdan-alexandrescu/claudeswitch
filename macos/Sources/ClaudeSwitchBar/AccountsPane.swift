import AppKit
import ClaudeSwitchCore
import SwiftUI
import UniformTypeIdentifiers

/// Accounts: one section per pool (shared pools, docs/PROFILES.md §10), in
/// rotation order within each (drag, or Move up/down from the keyboard and
/// VoiceOver); drag a row onto another pool to move it there. Per account:
/// rename, move to a pool, sign in again, Chrome, delete; then the recovery
/// copies. With no [[profile]] blocks, or a binary from before pools, one
/// list in rotation order.
struct AccountsPane: View {
    @EnvironmentObject var store: Store
    @State private var renaming: String?
    @State private var deleting: DeleteAsk?
    @State private var pending: PendingAction?
    @State private var creatingPool = false
    /// The pool being renamed or deleted (D38, D39), in its sheet. Settable
    /// for the render harness.
    @State var renamingPool: String?
    @State var deletingPool: String?

    struct DeleteAsk: Identifiable {
        var id: String { account }
        var account: String
        var message: String
    }

    var grouped: Bool { PoolLayout.grouped(store.pools, profiles: store.profiles) }

    var sections: [PoolSection] {
        store.pools.map { PoolLayout.sections($0, order: store.accountIDs, profiles: store.profiles) } ?? []
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            PaneHeader(title: "Accounts",
                       subtitle: grouped ? "Drag to reorder within a pool, or onto another pool to move it there."
                                         : "Drag to change the order rotation spends them in.") {
                Button("Add account") {
                    store.addAccount = AddAccountRequest(profile: AddForm.initialTarget(requested: nil, list: store.profiles))
                }
                .buttonStyle(PrimaryButtonStyle())
                .disabled(store.binaryPath == nil)
            }
            .padding([.horizontal, .top], 24)
            List {
                if grouped {
                    // One ForEach, headers as rows: a List row drops only
                    // within the ForEach it came from, so one per pool
                    // offered no other pool a drop spot.
                    let all = sections
                    let rows = PoolLayout.rows(all)
                    ForEach(rows) { r in poolRow(r, all: all) }
                        .onMove { from, to in moved(rows, all: all, from: from, to: to) }
                    HStack {
                        Spacer()
                        Button("New pool") { creatingPool = true }
                            .disabled(store.binaryPath == nil)
                            .help("A pool groups accounts; profiles list the pools they use")
                    }
                    .moveDisabled(true)
                    .listRowSeparator(.hidden)
                } else {
                    flat
                }
                RecoverySection()
            }
            .listStyle(.inset)
        }
        .sheet(item: Binding(get: { renaming.map(RenameTarget.init) }, set: { renaming = $0?.id })) { t in
            RenameSheet(old: t.id).environmentObject(store)
        }
        .sheet(isPresented: $creatingPool) { NewPoolSheet().environmentObject(store) }
        .sheet(item: Binding(get: { renamingPool.map(RenameTarget.init) }, set: { renamingPool = $0?.id })) { t in
            RenamePoolSheet(name: t.id).environmentObject(store)
        }
        .sheet(item: Binding(get: { deletingPool.map(RenameTarget.init) }, set: { deletingPool = $0?.id })) { t in
            DeletePoolSheet(name: t.id).environmentObject(store)
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

    /// One list in rotation order: no [[profile]] blocks (nothing to
    /// group), or a binary that predates pools (said in a line).
    var flat: some View {
        Section {
            ForEach(store.accountIDs, id: \.self) { id in
                row(id, targets: [], inPool: false)
            }
            .onMove { from, to in
                var ids = store.accountIDs
                ids.move(fromOffsets: from, toOffset: to)
                store.setPriority(ids)
            }
            if store.poolsUnsupported {
                Label("Update claudeswitch to group accounts into pools and share them between profiles.",
                      systemImage: "info.circle")
                    .font(.caption).foregroundStyle(.secondary)
                    .moveDisabled(true)
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
    }

    /// A pool's rows: its accounts, reordered by a drag within it; a row
    /// from another section dropped here moves into this pool. An empty
    /// pool has one row saying so, which a dragged row can land under too.
    @ViewBuilder func poolRow(_ r: PoolRow, all: [PoolSection]) -> some View {
        switch r {
        case .header(let sid):
            if let s = all.first(where: { $0.id == sid }) {
                PoolHeader(section: s, columns: sid == all.first?.id, busy: busy(s),
                           rename: s.name.map { n in { renamingPool = n } },
                           delete: s.name.map { n in { deletingPool = n } })
                    .font(.subheadline).foregroundStyle(.secondary)
                    .padding(.top, sid == all.first?.id ? 0 : 14)
                    .moveDisabled(true)
                    .listRowSeparator(.hidden)
            }
        case .account(let id, let sid):
            row(id, targets: PoolLayout.targets(for: id, sections: all), inPool: sid != PoolSection.noPool.id)
        case .empty(let sid):
            if let s = all.first(where: { $0.id == sid }) {
                EmptyPoolRow(section: s)
                    .moveDisabled(true)
            }
        }
    }

    /// A move in the one list: a reorder within a pool, or a move to the
    /// pool whose header the row was dropped under (confirmed first).
    func moved(_ rows: [PoolRow], all: [PoolSection], from: IndexSet, to: Int) {
        switch PoolLayout.move(rows, from: from, to: to) {
        case .reorder(let sid, let f, let t):
            guard let s = all.first(where: { $0.id == sid }) else { return }
            store.setPriority(RotationOrder.reorder(store.accountIDs, section: s.accounts, from: f, to: t))
        case .drop(let id, let sid):
            guard let s = all.first(where: { $0.id == sid }) else { return }
            askDrop(id, onto: s)
        case .none:
            break
        }
    }

    func row(_ id: String, targets: [PoolSection], inPool: Bool) -> some View {
        AccountRow(id: id, rename: { renaming = id }, delete: { askDelete(id) },
                   targets: targets, canLeave: inPool,
                   moveTo: { askDrop(id, onto: $0) }, leave: { askDrop(id, onto: .noPool) }, shift: { by in shift(id, by: by) })
    }

    func busy(_ s: PoolSection) -> Bool {
        s.accounts.contains { store.isBusy("move:\($0)") } || s.name.map { store.isBusy("pool:\($0)") } == true
    }

    // MARK: moving between pools

    /// Confirmed, then `pool move` or `pool remove` (a drop within its own
    /// pool is a reorder, handled by the list).
    func askDrop(_ id: String, onto s: PoolSection) {
        guard store.accountIDs.contains(id), let pools = store.pools,
              let d = PoolLayout.drop(id, onto: s, pools: pools) else { return }
        pending = PendingAction(confirmation: Confirm.poolDrop(d, pools: pools, defaultExists: defaultExists)) {
            store.runDrop(d)
        }
    }

    var defaultExists: Bool { store.profiles.map { $0.profile(StateFile.defaultProfile) != nil } ?? true }


    struct RenameTarget: Identifiable { var id: String }

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

/// A pool section's header: "TEAM · used by default (2nd), work (2nd)", and
/// on the first one the Plan column's label.
struct PoolHeader: View {
    let section: PoolSection
    let columns: Bool
    let busy: Bool
    /// A pool's Rename… and Delete… (D38, D39); nil for the accounts in no
    /// pool.
    var rename: (() -> Void)?
    var delete: (() -> Void)?

    var body: some View {
        HStack(spacing: 8) {
            (Text(section.title).fontWeight(.semibold) + Text(" · " + section.subtitle))
                .lineLimit(1).truncationMode(.tail)
                .help(section.title + " · " + section.subtitle)
            if busy { ProgressView().controlSize(.mini) }
            Spacer()
            if columns {
                AccountColumns(plan: "Plan")
            }
            if let rename, let delete {
                Menu {
                    Button("Rename…", action: rename)
                    Button("Delete…", role: .destructive, action: delete)
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .frame(width: AccountColumns.trailing, alignment: .trailing)
                .accessibilityLabel("Actions for pool \(section.name ?? section.title)")
            } else if columns {
                Color.clear.frame(width: AccountColumns.trailing, height: 1)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel((section.name.map { "Pool \($0)" } ?? section.title) + ", " + section.subtitle)
    }
}

/// An empty pool's one row: where to drop an account. Delete is in the
/// pool's header menu, with every other pool's.
struct EmptyPoolRow: View {
    let section: PoolSection

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Image(systemName: "tray.and.arrow.down").foregroundStyle(.tertiary).accessibilityHidden(true)
                Text("No accounts. Drag one here to move it into \(section.name ?? "this pool").")
                    .foregroundStyle(.secondary)
                Spacer()
            }
        }
        .padding(.vertical, 6)
    }
}

struct AccountRow: View {
    @EnvironmentObject var store: Store
    let id: String
    let rename: () -> Void
    let delete: () -> Void
    /// "Move to pool": every other pool; empty in one list.
    var targets: [PoolSection] = []
    /// It is in a pool, and can be taken out of it (to no pool).
    var canLeave = false
    var moveTo: (PoolSection) -> Void = { _ in }
    var leave: () -> Void = {}
    let shift: (Int) -> Void

    var view: AccountView? { store.snapshot?.accounts.first { $0.id == id } }
    var record: AccountRecord? { store.state?.accounts[id] }
    /// `account list --json`: the plan (lane 15), its pool and profiles.
    var info: AccountInfo? { store.accountList?.account(id) }
    var email: String? { store.state?.emails[id] ?? info?.email }
    /// The profile it is live in.
    var liveIn: String? { store.liveIn(id) }

    var body: some View {
        HStack(spacing: 16) {
            Image(systemName: "line.3.horizontal").foregroundStyle(.tertiary).accessibilityHidden(true)
            MiniRings(account: view, size: 34)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(id).bold().lineLimit(1).truncationMode(.middle).help(id).layoutPriority(1)
                    if let e = email {
                        Text(e).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle).help(e)
                    }
                    chips
                }
                // Whole segments give way from the end, the seat first and who
                // uses it last: a stub such as "s…" or "Chro…" says nothing.
                // Truncating is the last resort.
                ViewThatFits(in: .horizontal) {
                    ForEach(lineVariants, id: \.self) { v in
                        detailsRow(v, truncating: false)
                    }
                    detailsRow(lineVariants.last ?? "", truncating: true)
                }
                .font(.caption).foregroundStyle(.secondary)
                .help([line, loginText ?? ""].filter { !$0.isEmpty }.joined(separator: " · "))
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

    /// Its state, and where it is live: "in use: work" takes the place of
    /// "available" (the owner's layout); trouble stays beside it.
    @ViewBuilder var chips: some View {
        if let v = view, !(liveIn != nil && v.status == .available) {
            StatusChip(status: v.status, now: store.snapshot?.now ?? Date()).fixedSize()
        }
        if let p = liveIn {
            Badge(text: "in use: " + p, color: .csAccent)
                .help("\(id) is live in \(p)" + (usedBy.count > 1 ? "; the other profiles that use it take another" : ""))
        }
    }

    var busy: Bool {
        store.isBusy("delete:\(id)") || store.isBusy("chrome:\(id)") || store.isBusy("move:\(id)")
    }

    @ViewBuilder var actions: some View {
        Button("Rename…", action: rename)
        if !targets.isEmpty || canLeave {
            Menu("Move to pool") {
                ForEach(targets) { s in
                    Button((s.name ?? s.title) + "…") { moveTo(s) }
                }
                if canLeave {
                    if !targets.isEmpty { Divider() }
                    Button("In no pool…", action: leave)
                }
            }
            .disabled(store.binaryPath == nil)
        }
        Button("Sign in again…") { store.addAccount = AddAccountRequest(account: id, profile: store.profile(of: id)) }
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

    /// Every profile that may use it (shared pools: several may).
    var usedBy: [String] {
        info?.profiles ?? store.profile(of: id).map { [$0] } ?? []
    }

    /// "used by default, work"; nil when nothing says.
    var usedByText: String? {
        if !usedBy.isEmpty { return "used by " + usedBy.joined(separator: ", ") }
        return info?.profiles != nil ? "used by no profile" : nil
    }

    /// Who uses it, then figure, Chrome and seat.
    var line: String { lineSegments.joined(separator: " · ") }

    /// Who uses it, the figure, Chrome and the seat, in that order.
    var lineSegments: [String] { ([usedByText ?? ""] + detailParts).filter { !$0.isEmpty } }

    /// The line with whole segments dropped from the end, longest first.
    var lineVariants: [String] {
        let s = lineSegments
        guard !s.isEmpty else { return [""] }
        return (1...s.count).reversed().map { s.prefix($0).joined(separator: " · ") }
    }

    /// One way of drawing the line, with when the login renews kept whole: it
    /// matters more than the seat.
    func detailsRow(_ text: String, truncating: Bool) -> some View {
        HStack(spacing: 0) {
            if truncating {
                Text(text).lineLimit(1).truncationMode(.tail)
            } else {
                Text(text).lineLimit(1).fixedSize()
            }
            if let l = loginText {
                Text((text.isEmpty ? "" : " · ") + l).lineLimit(1).fixedSize()
            }
        }
    }

    /// Figure, Chrome and seat.
    var details: String { detailParts.joined(separator: " · ") }

    var detailParts: [String] {
        var parts: [String] = []
        if let v = view, let p = v.bindingPct { parts.append(Format.pct(p) + " " + Format.window(v.bindingWindow)) }
        if chromeChoice != .sameAsProfile { parts.append("Chrome " + chromeLabel) }
        if let s = record?.seat { parts.append("seat " + shortSeat(s)) }
        return parts
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

/// "New pool": a name, and accounts to start with. One already in a pool
/// leaves it (said beside it, and confirmed); `pool create` takes the ones
/// in no pool, `pool move` brings in the others.
struct NewPoolSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    @State var name = ""
    @State var picked: Set<String> = []
    @State var error: AppError?
    @State private var pending: PendingAction?

    var selected: [String] { store.accountIDs.filter(picked.contains) }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("New pool").font(.title3.bold())
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 8) {
                GridRow {
                    Text("Name").frame(width: NewProfileSheet.labelWidth, alignment: .leading)
                    TextField("Name", text: $name, prompt: Text("e.g. team")).labelsHidden()
                        .textFieldStyle(.roundedBorder)
                }
                GridRow(alignment: .top) {
                    Text("Accounts").frame(width: NewProfileSheet.labelWidth, alignment: .leading)
                    accounts
                }
            }
            Text("No profile uses a new pool until one lists it: in Profiles, open a profile and choose Change… beside its pools.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy("newpool") { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Create") { ask() }
                    .keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(name.isEmpty || store.isBusy("newpool"))
            }
        }
        .padding(24)
        .frame(width: 480)
        .confirming($pending)
    }

    /// Every account, with the pool it would leave; capped in height.
    var accounts: some View {
        let ids = store.accountIDs
        let rows = VStack(alignment: .leading, spacing: 4) {
            if ids.isEmpty { Text("No accounts yet").foregroundStyle(.secondary) }
            ForEach(ids, id: \.self) { id in
                Toggle(isOn: Binding(get: { picked.contains(id) },
                                     set: { on in if on { picked.insert(id) } else { picked.remove(id) } })) {
                    HStack(spacing: 4) {
                        Text(id).lineLimit(1).truncationMode(.middle)
                        if let n = NewPoolPlan.note(for: id, pools: store.pools) {
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

    func ask() {
        let plan = NewPoolPlan.make(name: name, selected: selected, pools: store.pools)
        if let c = plan.confirmation {
            pending = PendingAction(confirmation: c) { create(plan) }
        } else {
            create(plan)
        }
    }

    func create(_ plan: NewPoolPlan) {
        error = nil
        Task {
            if let e = await store.poolCall("newpool", { $0.poolCreate(plan.name, accounts: plan.create) }) {
                error = AppError(code: e.code ?? "failed", message: e.message, hint: e.hint)
                return
            }
            for d in plan.moves {
                guard case .move(let a, _, let to) = d else { continue }
                if let e = await store.poolCall("newpool", { $0.poolMove(a, to: to) }) {
                    error = AppError(code: e.code ?? "failed",
                                     message: "\(plan.name) was created, but \(a) could not be moved in: " + e.message,
                                     hint: e.hint)
                    return
                }
            }
            store.note = "Created the pool \(plan.name); add it to a profile in Profiles"
            dismiss()
        }
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

/// A small pill: a state ("available", "needs login") or where an account
/// is live ("in use: work").
struct Badge: View {
    let text: String
    let color: Color

    var body: some View {
        Text(text).lineLimit(1).truncationMode(.tail)
            .font(.caption2)
            .padding(.horizontal, 6).padding(.vertical, 1)
            .background(PillShape().fill(color.opacity(0.18)))
            .foregroundStyle(color)
    }
}

struct StatusChip: View {
    let status: AccountStatus
    let now: Date

    var body: some View {
        Badge(text: Format.status(status, now: now), color: color)
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

/// Renaming a pool (D39). A profile's own pool becomes a pool of its own
/// with the new name, which that profile uses.
struct RenamePoolSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let name: String
    @State var newName = ""
    @State var error: AppError?

    var own: Bool { store.pools?.pool(name)?.own ?? false }
    var trimmed: String { newName.trimmingCharacters(in: .whitespacesAndNewlines) }
    var busy: Bool { store.isBusy("pool:\(name)") }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Rename pool \(name)").font(.title3.bold())
            TextField("New name", text: $newName, prompt: Text(name))
                .textFieldStyle(.roundedBorder)
                .onSubmit(rename)
            Text(own
                 ? "\(name) is profile \(name)'s own pool. Renamed, it becomes a pool of its own, with the same accounts, that \(name) uses."
                 : "Every profile that uses it keeps using it, in the same place in its order.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if busy { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Rename", action: rename).keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(trimmed.isEmpty || trimmed == name || busy)
            }
        }
        .padding(24)
        .frame(width: 440)
    }

    func rename() {
        guard !trimmed.isEmpty, trimmed != name else { return }
        error = nil
        let (old, new) = (name, trimmed)
        Task {
            if let e = await store.poolCall("pool:\(old)", { $0.poolRename(old, to: new) }) {
                if case .app(let a) = e { error = a } else { error = AppError(code: "failed", message: e.message, hint: e.hint) }
            } else {
                store.note = "Renamed the pool \(old) to \(new)"
                dismiss()
            }
        }
    }
}

/// Deleting a pool (D38): where its accounts go, and which profiles stop
/// using it, said before anything is done.
struct DeletePoolSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let name: String
    @State var to: PoolPick?
    @State var error: AppError?

    var plan: PoolDeletePlan? { store.pools.map { PoolDeletePlan.make(name, pools: $0, profiles: store.profiles) } }
    var busy: Bool { store.isBusy("pool:\(name)") }
    /// Where its accounts go: the choice, else where the plan starts.
    var chosen: Binding<PoolPick?> { Binding(get: { to ?? plan?.initial }, set: { to = $0 }) }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Delete pool \(name)?").font(.title3.bold())
            if let p = plan {
                if !p.accounts.isEmpty {
                    Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 4) {
                        GridRow {
                            Text("Move its accounts to")
                            Picker("Move its accounts to", selection: chosen) {
                                ForEach(p.destinations, id: \.self) { Text($0.label).tag(Optional($0.pick)) }
                            }
                            .labelsHidden()
                        }
                        GridRow {
                            Color.clear.frame(width: 1, height: 1)
                            Text(p.accounts.joined(separator: ", "))
                                .font(.caption).foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                Text(p.summary(to: chosen.wrappedValue))
                    .font(.callout)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if busy { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Delete \(name)", role: .destructive, action: delete)
                    .buttonStyle(.borderedProminent)
                    .tint(.red)
                    .disabled(busy || plan == nil)
            }
        }
        .padding(24)
        .frame(width: 480)
    }

    func delete() {
        error = nil
        let (n, dest) = (name, (plan?.accounts.isEmpty ?? true) ? nil : chosen.wrappedValue)
        Task {
            if let e = await store.poolCall("pool:\(n)", { $0.poolDelete(n, to: dest) }) {
                if case .app(let a) = e { error = a } else { error = AppError(code: "failed", message: e.message, hint: e.hint) }
            } else {
                store.note = "Deleted the pool \(n)"
                dismiss()
            }
        }
    }
}

