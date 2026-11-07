import AppKit
import ClaudeSwitchCore
import SwiftUI

/// Settings → Claude Code (CC1 A, CC2 B, CC3 B; the owner's picks,
/// 2026-10-28): what Claude Code reads from ~/.claude, or with Showing
/// picked, from one profile. One bordered list, a folding row per kind with
/// its summary and Open, Edit… or Reveal; plugins with their switches; one
/// profile's items with how it has them and what changes that. It writes
/// only through the CLI, and never shows an env value or a server's
/// arguments (the model has none).
struct ClaudeCodePane: View {
    @EnvironmentObject var store: Store
    /// The rows drawn unfolded (--render opens some).
    @State var unfolded: Set<SetupKind> = []
    @State private var pending: PendingAction?
    @State private var editing: SetupEditRequest?

    var scope: String? { store.claude.scope }
    var report: ClaudeSetup? { store.claude.report(scope) }
    var profiles: [ProfileSetup] { store.claude.profiles }
    /// One profile alone has nothing to pick between.
    var picker: Bool { profiles.count > 1 }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                PaneHeader(title: "Claude Code", subtitle: SetupText.subtitle(scope: scope)) {
                    Button("Show in Finder") {
                        store.showInFinder(report?.dir ?? (store.claude.home as NSString).appendingPathComponent(".claude"))
                    }
                }
                if store.claude.unsupported {
                    Label(SetupText.needsNewer, systemImage: "arrow.down.circle")
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    if picker { ScopePicker() }
                    content
                }
            }
            .padding(24)
        }
        .onAppear { store.reloadSetup(scope) }
        .onChange(of: store.claude.scope) { s in store.loadClaudeSetup(s) }
        .confirming($pending)
        .sheet(item: $editing) { r in SetupEditorSheet(request: r).environmentObject(store) }
    }

    @ViewBuilder var content: some View {
        if let r = report {
            // A failed reread keeps what was read, and says so.
            if let e = store.claude.error(scope) {
                InlineError(message: "Could not read it again: " + e.message, hint: e.hint)
            }
            SetupList(rows: SetupRows.build(r, now: store.now), unfolded: $unfolded, scope: scope,
                      target: SetupTarget.of(scope: scope, profiles: profiles),
                      edit: { editing = editRequest($0, report: r) }, ask: { pending = $0 })
            if scope == nil, let f = SetupText.footer(profiles, picker: picker) {
                Text(f).font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        } else if let e = store.claude.error(scope) {
            InlineError(message: "Could not read what Claude Code reads: " + e.message, hint: e.hint)
            Button("Try Again") { store.loadClaudeSetup(scope) }
                .disabled(store.binaryPath == nil)
        } else {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text(scope.map { "Reading what \($0) reads…" } ?? "Reading ~/.claude…").foregroundStyle(.secondary)
            }
        }
    }

    /// Edit…: the file the profile shown reads (its link's target), and who
    /// else reads it.
    func editRequest(_ item: SetupItemName, report r: ClaudeSetup) -> SetupEditRequest {
        let target = (item == .instructions ? r.instructions?.target : r.settings?.target)
            ?? (r.dir as NSString).appendingPathComponent(item.rawValue)
        let linked = r.share(item) == .shared
        return SetupEditRequest(item: item, scope: scope, target: target,
                                readers: SetupText.readers(item, scope: scope, profiles: profiles), linked: linked)
    }
}

/// "Showing": every profile (~/.claude), or one profile.
struct ScopePicker: View {
    @EnvironmentObject var store: Store

    var body: some View {
        HStack(spacing: 8) {
            Text("Showing").foregroundStyle(.secondary).fixedSize()
            Picker("Showing", selection: Binding(get: { store.claude.scope ?? "" },
                                                 set: { store.claude.scope = $0.isEmpty ? nil : $0 })) {
                Text(SetupText.everyProfile).tag("")
                ForEach(store.claude.profiles) { p in
                    Text(SetupText.pickerLabel(p, home: store.claude.home)).tag(p.name)
                }
            }
            .labelsHidden()
            .fixedSize()
            .accessibilityLabel("Showing")
            if store.isBusy("load:claude:" + ClaudeCodeState.key(store.claude.scope)) {
                ProgressView().controlSize(.small)
            }
            Spacer()
        }
    }
}

/// The bordered list: a row per kind, each unfolding to its items.
struct SetupList: View {
    @EnvironmentObject var store: Store
    let rows: [SetupRow]
    @Binding var unfolded: Set<SetupKind>
    let scope: String?
    let target: SetupTarget
    let edit: (SetupItemName) -> Void
    let ask: (PendingAction) -> Void

    var body: some View {
        VStack(spacing: 0) {
            ForEach(Array(rows.enumerated()), id: \.element.id) { i, row in
                if i > 0 { Divider() }
                SetupRowView(row: row, open: unfolded.contains(row.kind), scope: scope, target: target,
                             toggle: { if unfolded.contains(row.kind) { unfolded.remove(row.kind) } else { unfolded.insert(row.kind) } },
                             edit: edit, ask: ask)
                if unfolded.contains(row.kind) {
                    ForEach(row.items) { item in
                        Divider().padding(.leading, 40)
                        SetupItemRow(line: item, scope: scope, target: target).padding(.leading, 40)
                    }
                }
            }
        }
        .padding(.horizontal, 12)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.primary.opacity(0.03)))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.primary.opacity(0.1)))
    }
}

struct SetupRowView: View {
    @EnvironmentObject var store: Store
    let row: SetupRow
    let open: Bool
    let scope: String?
    let target: SetupTarget
    let toggle: () -> Void
    let edit: (SetupItemName) -> Void
    let ask: (PendingAction) -> Void

    var body: some View {
        HStack(spacing: 10) {
            if row.items.isEmpty {
                lead
            } else {
                Button(action: toggle) { lead.contentShape(Rectangle()) }
                    .buttonStyle(.plain)
                    .accessibilityLabel("\(row.kind.title), \(row.summary)")
                    .accessibilityHint(open ? "Folds its items away" : "Shows its items")
            }
            actions
        }
        .padding(.vertical, 9)
    }

    /// Chevron, symbol, name, badge and summary: the part that unfolds it.
    var lead: some View {
        HStack(spacing: 10) {
            Image(systemName: open ? "chevron.down" : "chevron.right")
                .font(.caption.weight(.semibold)).foregroundStyle(.secondary).frame(width: 10)
                .opacity(row.items.isEmpty ? 0 : 1)
                .accessibilityHidden(true)
            Image(systemName: row.kind.symbol).foregroundStyle(Color.csAccent).frame(width: 18)
                .accessibilityHidden(true)
            Text(row.kind.title).font(.body.weight(.semibold)).lineLimit(1).fixedSize()
            if let s = row.share { ShareBadge(state: s) }
            Spacer(minLength: 8)
            Text(row.summary).foregroundStyle(.secondary).lineLimit(1).truncationMode(.tail).help(row.summary)
        }
    }

    @ViewBuilder var actions: some View {
        if let s = row.share, let item = row.kind.item, let p = scope {
            let key = "share:\(p):\(item.rawValue)"
            if store.isBusy(key) { ProgressView().controlSize(.small) }
            Button(s.action.title) {
                if s.action == .useShared {
                    ask(PendingAction(confirmation: SetupText.useSharedConfirmation(profile: p, item: item)) {
                        store.runShare(.useShared, profile: p, item: item)
                    })
                } else {
                    store.runShare(s.action, profile: p, item: item)
                }
            }
            .controlSize(.small)
            .fixedSize()
            .disabled(store.binaryPath == nil || store.isBusy(key))
        }
        if let item = row.edit {
            Button("Edit…") { edit(item) }
                .controlSize(.small)
                .fixedSize()
                .disabled(target == .unavailable)
                .help(target == .unavailable ? "No profile is ~/.claude, so nothing here is saved"
                      : "Edit \(item.rawValue) here")
        }
        if let f = row.file {
            Button(f.title) { store.perform(f) }
                .controlSize(.small)
                .fixedSize()
                .help(helpText(f))
        }
    }

    func helpText(_ f: SetupFileAction) -> String {
        switch f {
        case .open(let p): return "Open \(FolderText.abbreviate(p, home: store.claude.home)) in its editor"
        case .reveal(let p): return "Show \(FolderText.abbreviate(p, home: store.claude.home)) in Finder"
        }
    }
}

/// An unfolded item: its name and description; a plugin with its switch.
struct SetupItemRow: View {
    @EnvironmentObject var store: Store
    let line: SetupLine
    let scope: String?
    let target: SetupTarget

    var body: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 1) {
                Text(line.name).lineLimit(1)
                Text(line.detail).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    .help(line.detail)
            }
            Spacer(minLength: 8)
            if let p = line.plugin {
                if store.isBusy("plugin:\(p.id)") { ProgressView().controlSize(.small) }
                Toggle(p.name, isOn: Binding(get: { p.enabled }, set: { store.setPlugin(p, on: $0, scope: scope) }))
                    .toggleStyle(.switch)
                    .labelsHidden()
                    .tint(.csAccent)
                    .disabled(store.binaryPath == nil || target == .unavailable || store.isBusy("plugin:\(p.id)"))
                    .help(p.enabled ? "Turn \(p.name) off" : "Turn \(p.name) on")
                    .accessibilityLabel(p.name)
            }
        }
        .padding(.vertical, 6)
        .accessibilityElement(children: .combine)
    }
}

/// How a profile has an item: Shared (a link, in the accent), Its own, or
/// Linked elsewhere; nothing when it has none.
struct ShareBadge: View {
    let state: ShareState

    var body: some View {
        if let text = state.badge {
            Group {
                if state == .shared {
                    Label(text, systemImage: "link").labelStyle(.titleAndIcon)
                        .padding(.horizontal, 6).padding(.vertical, 1)
                        .background(PillShape().fill(Color.csAccent.opacity(0.14)))
                        .foregroundStyle(Color.csAccent)
                } else {
                    Text(text)
                        .padding(.horizontal, 6).padding(.vertical, 1)
                        .background(PillShape().fill(Color.secondary.opacity(0.15)))
                        .foregroundStyle(.secondary)
                }
            }
            .font(.caption2)
            .lineLimit(1)
            .fixedSize()
            .accessibilityLabel(text)
        }
    }
}

// MARK: Edit… (CC2 B)

struct SetupEditRequest: Identifiable {
    let id = UUID()
    var item: SetupItemName
    var scope: String?
    /// The file read and written (a shared link's target).
    var target: String
    var readers: [String]
    /// The profile shown has it as a link to ~/.claude's.
    var linked: Bool
}

/// The app's own editor for CLAUDE.md and settings.json: the file's text,
/// read here; Save writes it through `claude write`, refused when the file
/// changed since it was opened, or (settings.json) when it is not valid
/// JSON. A refusal shows here, and the text stays.
struct SetupEditorSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let request: SetupEditRequest
    @State var text = ""
    @State var loaded: EditorBuffer?
    @State var loadError: String?
    @State var error: AppError?
    /// --render only: the text and a refusal to draw, read from no file.
    var renderText: String?

    var busy: Bool { store.isBusy("write:\(request.item.rawValue)") }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(SetupText.editorTitle(request.item)).font(.headline)
            Text(SetupText.editorCaption(target: request.target, home: store.claude.home, readers: request.readers,
                                         linked: request.linked))
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            PlainTextEditor(text: $text)
                .frame(minHeight: 230)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color(nsColor: .textBackgroundColor)))
                .clipShape(RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.primary.opacity(0.15)))
                .accessibilityLabel(request.item.rawValue)
            if let l = loadError { InlineError(message: l, hint: "") }
            if let e = error { InlineError(message: e.message, hint: e.hint) }
            HStack(spacing: 8) {
                Text(SetupText.editorFoot(request.item)).font(.caption).foregroundStyle(.secondary)
                Spacer()
                if busy { ProgressView().controlSize(.small) }
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Save", action: save)
                    .buttonStyle(PrimaryButtonStyle())
                    .keyboardShortcut("s", modifiers: .command)
                    .disabled(loaded == nil || text == loaded?.text || busy || store.binaryPath == nil)
            }
        }
        .padding(20)
        .frame(width: 560)
        .onAppear(perform: load)
    }

    func load() {
        guard loaded == nil, loadError == nil else { return }
        if let t = renderText {
            text = t
            loaded = EditorBuffer(text: "", sha256: EditorBuffer.hash(Data()))
            return
        }
        switch store.readEditorFile(request.target, item: request.item) {
        case .loaded(let b):
            loaded = b
            text = b.text
        case .failed(let m):
            loadError = m
        }
    }

    func save() {
        guard let b = loaded else { return }
        error = nil
        let (item, scope, t) = (request.item, request.scope, text)
        Task {
            switch await store.writeSetupFile(item, scope: scope, text: t, sha: b.sha256) {
            case .success: dismiss()
            case .failure(.app(let e)): error = e
            case .failure(let e): error = AppError(code: e.code ?? "failed", message: e.message, hint: e.hint)
            }
        }
    }
}

/// A plain-text editor: monospaced, and none of the substitutions a Mac
/// text view makes by default (smart quotes and dashes, text replacement,
/// spelling correction, links), any of which would break settings.json.
struct PlainTextEditor: NSViewRepresentable {
    @Binding var text: String

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        guard let tv = scroll.documentView as? NSTextView else { return scroll }
        tv.isRichText = false
        tv.importsGraphics = false
        tv.allowsUndo = true
        tv.isAutomaticQuoteSubstitutionEnabled = false
        tv.isAutomaticDashSubstitutionEnabled = false
        tv.isAutomaticTextReplacementEnabled = false
        tv.isAutomaticSpellingCorrectionEnabled = false
        tv.isContinuousSpellCheckingEnabled = false
        tv.isGrammarCheckingEnabled = false
        tv.isAutomaticLinkDetectionEnabled = false
        tv.isAutomaticDataDetectionEnabled = false
        tv.smartInsertDeleteEnabled = false
        tv.drawsBackground = false
        tv.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        tv.textColor = .textColor
        tv.textContainerInset = NSSize(width: 6, height: 10)
        tv.string = text
        tv.delegate = context.coordinator
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        guard let tv = scroll.documentView as? NSTextView, tv.string != text else { return }
        tv.string = text
    }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: PlainTextEditor
        init(_ p: PlainTextEditor) { parent = p }

        func textDidChange(_ n: Notification) {
            guard let tv = n.object as? NSTextView else { return }
            parent.text = tv.string
        }
    }
}

// MARK: Settings → Profiles (CC3 B)

/// A profile's Claude Code setup in one line, and Show in Claude Code,
/// which opens the pane showing that profile.
struct ProfileSetupLine: View {
    @EnvironmentObject var store: Store
    let profile: ProfileInfo

    var setup: ProfileSetup? { store.claude.profiles.first { $0.name == profile.name } }

    var line: String {
        if store.claude.unsupported { return SetupText.needsNewer }
        if let s = setup { return SetupText.profileLine(s) }
        if let e = store.claude.error(nil) { return "Could not read it: " + e.message }
        return "Reading…"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("CLAUDE CODE SETUP").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Text(line).foregroundStyle(.secondary)
                    .lineLimit(2).fixedSize(horizontal: false, vertical: true)
                Spacer()
                if !store.claude.unsupported {
                    Button("Show in Claude Code") { store.showClaudeCode(scope: profile.name) }
                        .fixedSize()
                        .help("Open Settings → Claude Code showing \(profile.name)")
                }
            }
        }
    }
}
