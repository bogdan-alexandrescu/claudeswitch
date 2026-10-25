import ClaudeSwitchCore
import SwiftUI

/// Settings → Cleanup (owner decisions 2026-10-25): the clean_* settings,
/// a preview of what would go, Clean now after a confirmation, and the
/// runs, each of which can be put back. Everything goes through
/// `claudeswitch clean`.
struct CleanupPane: View {
    @EnvironmentObject var store: Store
    @State private var pending: PendingAction?
    @State private var showItems = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            PaneHeader(title: "Cleanup",
                       subtitle: "What Claude left behind once its work merged, moved to the Trash.") {
                Button { store.previewClean() } label: {
                    if store.isBusy("clean:plan") {
                        ProgressView().controlSize(.small)
                    } else {
                        Label("Preview", systemImage: "magnifyingglass")
                    }
                }
                .disabled(store.binaryPath == nil || store.isBusy("clean:plan") || store.isBusy("clean:apply"))
            }
            .padding([.horizontal, .top], 24)
            Form {
                now
                runs
                if let schema = store.schema {
                    Section("Schedule") {
                        ForEach(SettingsPane.cleanupScheduleKeys.compactMap(schema.setting)) { SettingRow(setting: $0) }
                    }
                    Section("What it removes") {
                        ForEach(SettingsPane.cleanupKindKeys.compactMap(schema.setting)) { SettingRow(setting: $0) }
                    }
                }
            }
            .formStyle(.grouped)
            .tint(.csAccent)
        }
        .onAppear { if store.next.cleanRuns == nil { store.loadCleanRuns() } }
        .confirming($pending)
    }

    // MARK: the plan and Clean now

    @ViewBuilder var now: some View {
        Section {
            if store.isBusy("clean:plan") {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Looking through every repository Claude has worked in… this can take a minute.")
                        .foregroundStyle(.secondary)
                }
            } else if store.isBusy("clean:apply") {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Judging each item again and moving it to the Trash…").foregroundStyle(.secondary)
                }
            } else if let p = store.next.cleanPlan {
                planView(p)
            } else if let e = store.next.cleanPlanError {
                InlineError(message: "Could not make the plan: " + e.message, hint: e.hint)
            } else {
                Text("Preview shows what would go and why. Nothing moves until Clean now, and Undo puts a run back.")
                    .foregroundStyle(.secondary)
            }
            if let r = store.next.cleanRun {
                Label(r.summary, systemImage: "checkmark.circle").foregroundStyle(.green)
                    .fixedSize(horizontal: false, vertical: true)
                ForEach(r.failed, id: \.self) { f in
                    Label(f, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                }
            }
            if let e = store.next.cleanRunError {
                InlineError(message: e.message, hint: e.hint)
            }
        } header: {
            Text("Now")
        }
    }

    @ViewBuilder func planView(_ p: CleanPlan) -> some View {
        if p.removable.isEmpty {
            Text("Nothing to remove: everything found is unmerged, in use, protected or changed recently "
                 + "(\(p.kept) kept).").foregroundStyle(.secondary)
        } else {
            ForEach(p.groups) { g in
                LabeledContent(g.title) {
                    Text("\(g.items.count)" + (g.bytes > 0 ? " · " + Format.bytes(g.bytes) : ""))
                        .monospacedDigit().foregroundStyle(.secondary)
                }
            }
            DisclosureGroup("Each item, and why", isExpanded: $showItems) {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 6) {
                        ForEach(p.removable.prefix(500)) { it in
                            VStack(alignment: .leading, spacing: 1) {
                                Text(it.place()).font(.caption.monospaced()).lineLimit(1).truncationMode(.middle)
                                Text(it.why + (it.bytes > 0 ? " · " + Format.bytes(it.bytes) : ""))
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        if p.removable.count > 500 {
                            Text("…and \(p.removable.count - 500) more (claudeswitch clean lists them all)")
                                .font(.caption).foregroundStyle(.tertiary)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(maxHeight: 220)
            }
            HStack {
                Text("\(p.kept) kept: unmerged, in use, protected or changed recently."
                     + (store.next.cleanPlanAt.map { " Looked " + Format.age($0, now: Date()) + "." } ?? ""))
                    .font(.caption).foregroundStyle(.tertiary)
                Spacer()
                Button("Clean now…") {
                    pending = PendingAction(confirmation: Confirm.cleanApply(p)) { store.applyClean() }
                }
                .buttonStyle(PrimaryButtonStyle())
                .disabled(store.isBusy("clean:apply"))
            }
        }
    }

    // MARK: the runs

    @ViewBuilder var runs: some View {
        if let runs = store.next.cleanRuns, !runs.isEmpty {
            Section("Runs") {
                ForEach(runs.prefix(5)) { r in
                    HStack {
                        VStack(alignment: .leading, spacing: 1) {
                            Text(r.at.map { $0.formatted(date: .abbreviated, time: .shortened) } ?? r.id)
                            Text("\(r.removed) removed · \(Format.bytes(r.bytes))"
                                 + (r.undone > 0 ? " · \(r.undone) put back" : ""))
                                .font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        if r.undoable {
                            Button("Undo") { store.undoClean(r.id) }
                                .disabled(store.isBusy("clean:undo") || store.isBusy("clean:apply"))
                        }
                    }
                }
            }
        }
    }
}
