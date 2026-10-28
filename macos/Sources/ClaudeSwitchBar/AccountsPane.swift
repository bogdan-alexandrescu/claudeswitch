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

    /// The inset list draws its rows 16 pt in; 8 more line them up with the
    /// pane's header, 24 pt in like every other pane.
    static let inset: CGFloat = 8

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
                    ForEach(rows) { r in poolRow(r, all: all).padding(.horizontal, Self.inset) }
                        .onMove { from, to in moved(rows, all: all, from: from, to: to) }
                    HStack {
                        Spacer()
                        Button("New pool") { creatingPool = true }
                            .disabled(store.binaryPath == nil)
                            .help("A pool groups accounts; profiles list the pools they use")
                    }
                    .padding(.horizontal, Self.inset)
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
        .confirmationDialog(deleting.map { "Delete the account \(inQuotes($0.account))?" } ?? "",
                            isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
                            presenting: deleting) { ask in
            Button("Delete \(inQuotes(ask.account))", role: .destructive) { delete(ask.account) }
        } message: { ask in
            Text(ask.message + "\n\nThis cannot be undone.")
        }
        .confirming($pending)
    }

    /// One list in rotation order: no [[profile]] blocks (nothing to
    /// group), or a binary that predates pools (said in a line).
    /// Its header is a row, as a pool's is: a Section header pinned a
    /// tinted band whose rule ran past the pane's margins.
    var flat: some View {
        Group {
            HStack(spacing: 8) {
                Text("ROTATION ORDER").fontWeight(.semibold)
                if store.isBusy("priority") { ProgressView().controlSize(.mini) }
                Spacer()
                AccountColumns(plan: "Plan")
                Color.clear.frame(width: AccountColumns.trailing, height: 1)
            }
            .font(.subheadline).foregroundStyle(.secondary)
            .padding(.horizontal, Self.inset)
            .moveDisabled(true)
            .listRowSeparator(.hidden)
            ForEach(store.accountIDs, id: \.self) { id in
                row(id, section: store.accountIDs, targets: [], inPool: false).padding(.horizontal, Self.inset)
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
                    .padding(.horizontal, Self.inset)
                    .moveDisabled(true)
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
            row(id, section: all.first { $0.id == sid }?.accounts ?? [id],
                targets: PoolLayout.targets(for: id, sections: all), inPool: sid != PoolSection.noPool.id)
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

    /// One account's row; Move up and down keep it within `section`, the
    /// accounts of its pool (in one list, all of them).
    func row(_ id: String, section: [String], targets: [PoolSection], inPool: Bool) -> some View {
        AccountRow(id: id, rename: { renaming = id }, delete: { askDelete(id) },
                   targets: targets, canLeave: inPool,
                   moveTo: { askDrop(id, onto: $0) }, leave: { askDrop(id, onto: .noPool) },
                   moved: { by in RotationOrder.move(store.accountIDs, section: section, id, by: by) })
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
                var t = "Deleted the account \(inQuotes(r.account))"
                if let e = r.email { t += " (\(e))" }
                if let tw = r.twin { t += "; its credential lives on as \(inQuotes(tw))" }
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
            return AlertItem(title: "The account \(inQuotes(id)) is in use", message: e.message,
                             hint: e.hint.isEmpty ? "Switch that profile to another account first." : e.hint)
        case "config_changed":
            return AlertItem(title: "The config changed meanwhile", message: e.message,
                             hint: e.hint.isEmpty ? "The other change and the credential were kept. Look at the config, then try again." : e.hint)
        case "daemon_not_loaded":
            return AlertItem(title: "The daemon did not pick up the change", message: e.message,
                             hint: e.hint.isEmpty ? "Nothing was deleted. Check the daemon in the Daemon pane, then try again." : e.hint)
        default:
            return AlertItem("Could not delete \(inQuotes(id))", e)
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
                Text("No accounts. Drag one here to move it into " + (section.name.map { "the pool " + inQuotes($0) } ?? "this pool") + ".")
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
    /// The rotation order with it moved up (-1) or down (1) within its
    /// section; nil at the section's ends.
    let moved: (Int) -> [String]?

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
                // The email gives way first, whole; then the name truncates.
                // The chips never do: "in…" says nothing.
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 8) {
                        Text(id).bold().lineLimit(1).fixedSize()
                        if let e = email {
                            Text(e).foregroundStyle(.secondary).lineLimit(1).fixedSize().help(e)
                        }
                        chips
                    }
                    HStack(spacing: 8) {
                        Text(id).bold().lineLimit(1).truncationMode(.middle).help(id).layoutPriority(1)
                        chips
                    }
                }
                .help([id, email ?? ""].filter { !$0.isEmpty }.joined(separator: " · "))
                // Whole segments give way (AccountLine), the figure last: a
                // stub such as "s…" or "Chro…" says nothing. Truncating is the
                // last resort.
                ViewThatFits(in: .horizontal) {
                    ForEach(lineVariants, id: \.self) { v in
                        Text(v).lineLimit(1).fixedSize()
                    }
                    Text(lineVariants.last ?? "").lineLimit(1).truncationMode(.tail)
                }
                .font(.caption).foregroundStyle(.secondary)
                .help(lineVariants.first ?? "")
            }
            Spacer(minLength: 8)
            if busy { ProgressView().controlSize(.small) }
            AccountColumns(plan: PlanColumn.text(info?.plan))
                .foregroundStyle(.secondary)
                .help(PlanColumn.help(info?.plan))
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
            StatusChip(status: v.status, now: store.now).fixedSize()
        }
        if let p = liveIn {
            Badge(text: "in use: " + p, color: .csAccent)
                .fixedSize()
                .help("The account \(inQuotes(id)) is live in \(inQuotes(p))" + (usedBy.count > 1 ? "; the other profiles that use it take another" : ""))
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
        Button("Sign in…") { store.addAccount = AddAccountRequest(account: id, profile: store.profile(of: id)) }
        Divider()
        // One `priority` call at a time: two quick moves started two.
        Button("Move up") { shift(-1) }
            .disabled(moved(-1) == nil || store.isBusy("priority"))
        Button("Move down") { shift(1) }
            .disabled(moved(1) == nil || store.isBusy("priority"))
        Divider()
        Button("Open Chrome") { store.openChrome(id) }
            .disabled(store.chrome?.supported == false || store.isBusy("chrome:\(id)"))
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

    /// Also VoiceOver's Move up and Move down, which cannot be greyed out.
    func shift(_ by: Int) {
        guard !store.isBusy("priority"), let ids = moved(by) else { return }
        store.setPriority(ids)
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

    /// Who uses it, the figure, Chrome, the seat and the login, longest
    /// first (AccountLine).
    var lineVariants: [String] {
        let figure = view.flatMap { v in v.bindingPct.map { Format.pct($0) + " " + (Format.windowName(v.bindingWindow) ?? "") } }
        return AccountLine.variants(
            usedBy: info?.profiles ?? store.profile(of: id).map { [$0] }, figure: figure,
            chrome: chromeChoice == .sameAsProfile ? nil : "Chrome " + chromeLabel,
            seat: record?.seat.map { "seat " + shortSeat($0) }, login: loginText)
    }

    /// When the login renews; nothing once it needs signing in (the chip
    /// says so).
    var loginText: String? {
        Format.login(refresh: record?.refreshExpiry, token: record?.tokenExpiry, now: store.now,
                     needsSignIn: view?.status == .needsLogin || info?.state == "needs_login")
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
    /// What Create has done so far: once the pool exists, trying again
    /// makes only the moves left.
    @State private var run: NewPoolRun?

    var selected: [String] { store.accountIDs.filter(picked.contains) }
    var created: Bool { run?.created == true }
    /// What Create would do now.
    var plan: NewPoolPlan { NewPoolPlan.make(name: name, selected: selected, pools: store.pools) }
    /// Create ("Create…" when it asks first), then the moves left.
    var action: String { (created ? run : nil)?.action ?? NewPoolRun(plan).action }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("New pool").font(.title3.bold())
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16, verticalSpacing: 8) {
                GridRow {
                    Text("Name").frame(width: NewProfileSheet.labelWidth, alignment: .leading)
                    TextField("Name", text: $name, prompt: Text("e.g. team")).labelsHidden()
                        .textFieldStyle(.roundedBorder)
                        .disabled(created)
                }
                GridRow(alignment: .top) {
                    Text("Accounts").frame(width: NewProfileSheet.labelWidth, alignment: .leading)
                    accounts.disabled(created)
                }
            }
            Text("No profile uses a new pool until one lists it: in Profiles, open a profile and choose Change… beside its pools.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if !created, let s = NewPoolPlan.stops(name: name, selected: selected, pools: store.pools,
                                                   defaultExists: store.profiles.map { $0.profile(StateFile.defaultProfile) != nil } ?? true) {
                Label(s, systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let e = error { SheetError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if store.isBusy("newpool") { ProgressView().controlSize(.small) }
                Spacer()
                Button(run?.cancel ?? "Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(action) { ask() }
                    .keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(name.isEmpty || store.isBusy("newpool"))
            }
        }
        .padding(24)
        .frame(width: 480)
        .confirming($pending)
    }

    /// Every account, with the pool it is in, or would leave once checked;
    /// capped in height.
    var accounts: some View {
        let ids = store.accountIDs
        let rows = VStack(alignment: .leading, spacing: 4) {
            if ids.isEmpty { Text("No accounts yet").foregroundStyle(.secondary) }
            ForEach(ids, id: \.self) { id in
                Toggle(isOn: Binding(get: { picked.contains(id) },
                                     set: { on in if on { picked.insert(id) } else { picked.remove(id) } })) {
                    HStack(spacing: 4) {
                        Text(id).lineLimit(1).truncationMode(.middle)
                        if let n = NewPoolText.note(for: id, checked: picked.contains(id), name: name, pools: store.pools) {
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

    /// Create, confirmed first when accounts leave their pools; once the
    /// pool exists, the moves left, already confirmed.
    func ask() {
        if let r = run, r.created { create(r); return }
        let plan = self.plan
        if let c = plan.confirmation {
            pending = PendingAction(confirmation: c) { create(NewPoolRun(plan)) }
        } else {
            create(NewPoolRun(plan))
        }
    }

    func create(_ start: NewPoolRun) {
        error = nil
        run = start
        Task {
            var r = start
            for s in r.steps {
                if let e = await store.poolCall("newpool", { s.run($0) }) {
                    error = AppError(code: e.code ?? "failed", message: r.failure(s, e.message), hint: e.hint)
                    return
                }
                r.done(s)
                run = r
            }
            store.note = "Created the pool \(inQuotes(r.plan.name)); add it to a profile in Profiles"
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
        // The plan before its parenthesis (PlanColumn), the whole in the
        // tooltip: cut in the middle, "Max 5x (…rise seat)" said nothing. A
        // long one wraps to a second line before it is cut at the end.
        Text(plan).lineLimit(2).truncationMode(.tail).frame(width: 112, alignment: .leading)
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

    /// The button's state, and Return's in the field: Return would
    /// otherwise rename with the name unchanged, empty, or twice.
    var canSave: Bool { !name.isEmpty && name != old && !store.isBusy("rename") }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Rename the account \(inQuotes(old))").font(.title3.bold())
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16) {
                GridRow {
                    Text("New name")
                    TextField("New name", text: $name).labelsHidden().textFieldStyle(.roundedBorder).onSubmit(save)
                }
            }
            Text("A running daemon is stopped for a moment while the account is renamed, then started again.")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let e = error { SheetError(message: e.message, hint: e.hint) }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Rename", action: save).keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(!canSave)
            }
        }
        .padding(24)
        .frame(width: 440)
        .onAppear { name = old }
    }

    func save() {
        guard canSave else { return }
        let (o, n) = (old, name)
        Task {
            let r = await store.call("rename", { $0.accountRenamePausingDaemon(o, to: n) })
            store.loadDaemon()
            switch r {
            case .success(let p):
                store.refresh(full: true, profiles: true)
                dismiss()
                if let e = p.restartError {
                    store.alertBinding(.settings).wrappedValue =
                        AlertItem(AccountRename.note(o, to: n) + ", but the daemon did not start again", e)
                } else {
                    store.note = AccountRename.note(o, to: n)
                }
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

    var busy: Bool { store.isBusy("recovery") }

    var body: some View {
        Section {
            header
            if let items = store.recovery {
                if items.isEmpty { Text("No recovery copies.").foregroundStyle(.secondary).padding(.horizontal, AccountsPane.inset) }
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
                        .disabled(item.error != nil || busy)
                        Button("Clear…") { clear(item) }
                            .disabled(busy)
                    }
                    .padding(.horizontal, AccountsPane.inset)
                }
            } else {
                Text("Copies of credentials kept when a sign-in replaced them.").foregroundStyle(.secondary)
                    .padding(.horizontal, AccountsPane.inset)
            }
        }
        // The action carries its own slot: a dialog clears its binding before
        // the button runs, so reading state there would find nothing.
        .confirming($pending)
    }

    /// As a pool's header, a row with no rule: its name in capitals,
    /// semibold; its controls drawn as buttons, so they do not read as
    /// labels.
    var header: some View {
        HStack(spacing: 8) {
            Text("RECOVERY COPIES").fontWeight(.semibold).font(.subheadline).foregroundStyle(.secondary)
            if busy { ProgressView().controlSize(.mini) }
            Spacer()
            Group {
                Button(store.recovery == nil ? "Show" : "Reload") { store.loadRecovery(identify: false) }
                Button("Identify") { store.loadRecovery(identify: true) }
                    .help("Look up who each copy signs in as (one identity call each)")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .disabled(busy)
        }
        .padding(.horizontal, AccountsPane.inset)
        .padding(.top, 14)
        .listRowSeparator(.hidden)
    }

    func clear(_ item: RecoveryItem) {
        let s = item.slot
        pending = PendingAction(confirmation: Confirm.recoveryClear(slot: s, account: item.account)) {
            store.act("recovery", title: "Could not clear \(inQuotes(s))", { $0.recoveryClear(s) }) { _ in
                store.loadRecovery(identify: false)
            }
        }
    }

    func describe(_ i: RecoveryItem) -> String {
        if let e = i.error { return e }
        var p: [String] = []
        if let pr = i.profile { p.append("from " + pr) }
        if let k = i.keptAt { p.append("kept " + Format.age(k, now: store.now)) }
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
        store.act("recovery", title: "Could not restore \(inQuotes(slot))", { $0.recoveryRestore(slot, account: account, force: force) }) { r in
            store.note = "Restored \(inQuotes(r.slot)) to \(inQuotes(r.account))"
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
    /// The button's state, and Return's in the field.
    var canRename: Bool { !trimmed.isEmpty && trimmed != name && !busy }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Rename the pool \(inQuotes(name))").font(.title3.bold())
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 16) {
                GridRow {
                    Text("New name")
                    TextField("New name", text: $newName, prompt: Text(name))
                        .labelsHidden()
                        .textFieldStyle(.roundedBorder)
                        .onSubmit(rename)
                }
            }
            Text(PoolRename.note(name: name, own: own, to: trimmed))
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let e = error { SheetError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if busy { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Rename", action: rename).keyboardShortcut(.defaultAction).buttonStyle(PrimaryButtonStyle())
                    .disabled(!canRename)
            }
        }
        .padding(24)
        .frame(width: 440)
    }

    func rename() {
        guard canRename else { return }
        error = nil
        let (old, new) = (name, trimmed)
        Task {
            if let e = await store.poolCall("pool:\(old)", { $0.poolRename(old, to: new) }) {
                if case .app(let a) = e { error = a } else { error = AppError(code: "failed", message: e.message, hint: e.hint) }
            } else {
                store.note = "Renamed the pool \(inQuotes(old)) to \(inQuotes(new))"
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
            Text("Delete the pool \(inQuotes(name))?").font(.title3.bold())
            if let p = plan {
                if let caption = p.caption {
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
                            Text(caption)
                                .font(.caption).foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                Text(p.summary(to: chosen.wrappedValue))
                    .font(.callout)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let e = error { SheetError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                if busy { ProgressView().controlSize(.small) }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Delete \(inQuotes(name))", role: .destructive, action: delete)
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
                store.note = "Deleted the pool \(inQuotes(n))"
                dismiss()
            }
        }
    }
}

