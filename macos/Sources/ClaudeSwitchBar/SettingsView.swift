import AppKit
import ClaudeSwitchCore
import SwiftUI

enum Pane: String, CaseIterable, Identifiable {
    case overview = "Overview", profiles = "Profiles", accounts = "Accounts", rotation = "Rotation", polling = "Polling",
         daemon = "Daemon", cleanup = "Cleanup", history = "History", health = "Health", advanced = "Advanced"

    var id: String { rawValue }

    var symbol: String {
        switch self {
        case .overview: return "square.grid.2x2"
        case .profiles: return "person.2"
        case .accounts: return "person.crop.circle"
        case .rotation: return "arrow.triangle.2.circlepath"
        case .polling: return "timer"
        case .daemon: return "gearshape.2"
        case .cleanup: return "trash"
        case .history: return "chart.xyaxis.line"
        case .health: return "stethoscope"
        case .advanced: return "slider.horizontal.3"
        }
    }
}

/// The Settings window (IMPROVEMENTS M3, M7): a sidebar of panes, each
/// acting only through the JSON CLI.
struct SettingsView: View {
    static let windowID = "settings"
    static let windowTitle = "ClaudeSwitch Settings"
    /// The sidebar's least width (design call D1, 2026-10-28): room for the
    /// 145 pt wordmark at 18 pt between its 18 pt margins. At 170 it did not
    /// fit, and SwiftUI centred it, 5.5 pt left of the icons below.
    static let sidebarLeast: CGFloat = 182
    @EnvironmentObject var store: Store
    @State var pane: Pane? = .overview
    /// --render only: an off-screen capture does not draw the sidebar's
    /// vibrant material, so the render puts the same sidebar on a plain fill.
    var renderSidebar = false
    /// --render only: the sidebar's width, its ideal unless drawn at its least.
    var sidebarWidth: CGFloat = 190

    var body: some View {
        Group {
            if renderSidebar {
                HStack(spacing: 0) {
                    // The real sidebar, under the 40 pt the title bar takes on
                    // screen, on a plain fill for the material.
                    sidebar
                        .scrollContentBackground(.hidden)
                        .environment(\.controlActiveState, .key)
                        .padding(.top, 40)
                        .background(Color.secondary.opacity(0.08))
                        .frame(width: sidebarWidth)
                    Divider()
                    detail
                }
            } else {
                split
            }
        }
    }

    var sidebar: some View {
        List(Pane.allCases, selection: $pane) { p in
            Label(p.rawValue, systemImage: p.symbol).tag(p)
        }
        .listStyle(.sidebar)
        .safeAreaInset(edge: .top, spacing: 0) {
            Wordmark(size: 18, mark: 33, gap: 4).fixedSize()
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 18).padding(.top, 4).padding(.bottom, 8)
        }
    }

    var split: some View {
        NavigationSplitView {
            sidebar
                .navigationSplitViewColumnWidth(min: Self.sidebarLeast, ideal: 190, max: 240)
        } detail: {
            detail
        }
        .frame(minWidth: 760, minHeight: 520)
        .onAppear {
            store.acting(in: .settings)
            store.loadSettings()
            openRequested(store.next.settingsRequest)
        }
        .onChange(of: store.next.settingsRequest, perform: openRequested)
        .sheet(item: $store.addAccount) { req in
            AddAccountSheet(request: req).environmentObject(store)
        }
        .alert(item: store.alertBinding(.settings)) { a in
            Alert(title: Text(a.title), message: Text(a.hint.isEmpty ? a.message : a.message + "\n\n" + a.hint))
        }
    }

    /// A notification's action asked for a pane (F5: sign in again).
    func openRequested(_ r: SettingsRequest?) {
        guard let r else { return }
        if let p = r.pane { pane = p }
        store.next.settingsRequest = nil
    }

    var detail: some View {
            Group {
                switch pane ?? .overview {
                case .overview: OverviewPane(go: { pane = $0 })
                case .profiles: ProfilesPane()
                case .accounts: AccountsPane()
                case .rotation: SchemaPane(pane: .rotation, title: "Rotation",
                                           subtitle: "When an account is rotated away from, and where to.")
                case .polling: SchemaPane(pane: .polling, title: "Polling",
                                          subtitle: "How often the daemon reads each account's usage.")
                case .daemon: DaemonPane()
                case .cleanup: CleanupPane()
                case .history: HistoryPane()
                case .health: HealthPane()
                case .advanced: AdvancedPane()
                }
            }
            .frame(minWidth: 520, maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .safeAreaInset(edge: .bottom) {
                if let n = store.note(on: .settings) {
                    NoteBanner(text: n) { store.clearNote(on: .settings) }.padding(16)
                }
            }
    }
}

/// A pane's title and one-line description.
struct PaneHeader<Trailing: View>: View {
    let title: String
    let subtitle: String
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(alignment: .center) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.title2.bold())
                Text(subtitle).foregroundStyle(.secondary)
            }
            Spacer()
            trailing
        }
    }
}

extension PaneHeader where Trailing == EmptyView {
    init(title: String, subtitle: String) {
        self.init(title: title, subtitle: subtitle) { EmptyView() }
    }
}

/// "switch_at_weekly" → "Switch at week" (SettingNames).
func settingLabel(_ key: String) -> String { SettingNames.label(key) }

// MARK: settings generated from `config schema --json`

struct SchemaPane: View {
    @EnvironmentObject var store: Store
    let pane: SettingsPane
    let title: String
    let subtitle: String

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
        PaneHeader(title: title, subtitle: subtitle).padding([.horizontal, .top], 24)
        Form {
            if let schema = store.schema {
                if store.values == nil, let e = store.valuesError {
                    Section {
                        PaneUnavailable(error: e, what: "The current settings")
                        Button("Try Again") { store.reloadValues() }
                    }
                }
                Section {
                    ForEach(schema.settings(in: pane)) { s in
                        SettingRow(setting: s)
                    }
                }
            } else if let e = store.schemaError {
                Section {
                    PaneUnavailable(error: e, what: "The settings")
                    Button("Try Again") { store.loadSettings() }
                }
            } else {
                Section {
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Reading the settings…").foregroundStyle(.secondary)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .tint(.csAccent)
        }
    }
}

/// One global setting: a control for its type, the schema's description
/// and default, inline validation, then `config set` and the CLI's answer.
struct SettingRow: View {
    @EnvironmentObject var store: Store
    let setting: SettingSchema
    /// Off while the setting it depends on is (Every with the schedule off).
    var off = false
    @State private var text = ""
    @State private var edited = false
    @State private var cliError: AppError?
    @State private var saved: ConfigSetResult?
    @FocusState private var focused: Bool

    /// nil until the values are read: the default is not the value.
    var current: String? { store.values.map { $0.values[setting.key] ?? setting.defaultValue } }
    var local: String? { edited ? setting.validate(text) : nil }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            LabeledContent {
                // A text field takes a fixed column; a switch or a picker
                // only its own width, so the description beside it runs on.
                if setting.type == "bool" || setting.type == "enum" { control.fixedSize() } else { control.frame(maxWidth: 220) }
            } label: {
                VStack(alignment: .leading, spacing: 2) {
                    Text(settingLabel(setting.key))
                    Text(SettingNames.description(setting)).font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            HStack(spacing: 8) {
                Text("Default " + SettingNames.value(setting, setting.defaultValue) + range)
                    .font(.caption).foregroundStyle(.secondary)
                Spacer()
                if store.isBusy("set:\(setting.key)") { ProgressView().controlSize(.mini) }
            }
            if !edited, let c = current, let why = SettingNames.outOfRange(setting, c) {
                Label(why, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(Color.csWarn)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let e = local {
                InlineError(message: "Must be " + e, hint: "")
            } else if let e = cliError {
                InlineError(message: e.message, hint: e.hint)
            } else if let s = saved {
                Text("Saved: \(SettingNames.value(setting, s.previous)) → \(SettingNames.value(setting, s.value))" + (s.daemonRunning ? " · the running daemon picks it up" : ""))
                    .font(.caption).foregroundStyle(Color.csGood)
                ForEach(s.warnings, id: \.self) { w in
                    Label(w, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(Color.csWarn)
                }
            }
        }
        .onAppear { if !edited, let c = current { text = SettingNames.editable(setting, c) } }
        .onChange(of: current) { v in if !edited, let v { text = SettingNames.editable(setting, v) } }
        .disabled(current == nil || off)
    }

    @ViewBuilder var control: some View {
        if setting.type == "bool" {
            Toggle("", isOn: Binding(get: { current == "true" }, set: { save($0 ? "true" : "false") }))
                .labelsHidden()
                .toggleStyle(.switch)
                .accessibilityLabel(settingLabel(setting.key))
        } else if setting.type == "enum" {
            Picker("", selection: Binding(get: { current ?? setting.defaultValue }, set: { save($0) })) {
                ForEach(setting.enumValues, id: \.self) { Text(SettingNames.value(setting, $0)).tag($0) }
            }
            .labelsHidden()
            .fixedSize()
            .accessibilityLabel(settingLabel(setting.key))
        } else {
            HStack(spacing: 4) {
            TextField("", text: Binding(get: { text }, set: { text = $0; edited = true; cliError = nil }),
                      // An empty list's default reads "none", as elsewhere.
                      prompt: Text(setting.type == "list" ? SettingNames.value(setting, setting.defaultValue)
                                   : SettingNames.editable(setting, setting.defaultValue)))
                .labelsHidden()
                .textFieldStyle(.roundedBorder)
                .multilineTextAlignment(.trailing)
                .focused($focused)
                .onSubmit(commit)
                // Leaving the field saves it too, as a Mac field does; a
                // value typed and clicked away from was lost.
                .onChange(of: focused) { f in if !f { commit() } }
                .accessibilityLabel(settingLabel(setting.key))
            // The unit beside the figure, as the profile fields have it.
            if let u = ProfilesText.unit(setting) { Text(u).foregroundStyle(.secondary) }
            }
        }
    }

    func commit() {
        // Not while a save of it runs: Return then click-away saved twice.
        guard !store.isBusy("set:\(setting.key)") else { return }
        if edited, local == nil, let c = current, text != SettingNames.editable(setting, c) { save(text) }
    }

    var range: String {
        func f(_ v: Double) -> String { setting.type == "duration" ? Format.duration(v) : String(format: "%g", v) }
        switch (setting.min, setting.max) {
        case let (lo?, hi?): return " · " + (setting.minExclusive ? "above " : "from ") + f(lo) + " to " + f(hi)
        case let (lo?, nil): return " · " + (setting.minExclusive ? "above " : "at least ") + f(lo)
        case let (nil, hi?): return " · at most " + f(hi)
        default: return setting.type == "list" ? " · comma-separated" : ""
        }
    }

    func save(_ value: String) {
        cliError = nil
        saved = nil
        let key = setting.key
        Task {
            switch await store.call("set:\(key)", { $0.configSet(key, value) }) {
            case .success(let r):
                saved = r
                edited = false
                store.reloadValues()
            case .failure(.app(let e)):
                cliError = e
            case .failure(let e):
                cliError = AppError(code: "failed", message: e.message, hint: e.hint)
            }
        }
    }
}

struct InlineError: View {
    let message: String
    let hint: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Label(message, systemImage: "xmark.octagon.fill").foregroundStyle(Color.csBad)
            if !hint.isEmpty { Text(hint).foregroundStyle(.secondary) }
        }
        .font(.caption)
        .fixedSize(horizontal: false, vertical: true)
    }
}

// MARK: Daemon

struct DaemonPane: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var login: LoginItem
    @State private var confirmUninstall = false
    @State private var pending: PendingAction?

    /// How long the daemon has not polled, when it has stopped; nil while it
    /// polls.
    var stale: String? {
        guard case .stale(let since) = store.snapshot?.daemon.health else { return nil }
        return Format.duration(store.now.timeIntervalSince(since))
    }

    /// Live swaps for real: asked first.
    func goLive(install: Bool) {
        pending = PendingAction(confirmation: Confirm.daemonLive(install: install)) {
            store.setDaemon(install ? .install(live: true) : .live,
                            title: install ? "Could not install the daemon" : "Could not change the daemon's mode")
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
        PaneHeader(title: "Daemon", subtitle: "The background service that polls and rotates.")
            .padding([.horizontal, .top], 24)
        Form {
            if let d = store.daemon {
                Section("Service") {
                    LabeledContent("Status", value: d.running ? (stale == nil ? "running" : "running, not polling")
                                   : d.loaded ? "loaded, not running" : d.installed ? "installed, stopped" : "not installed")
                    // Running but not reading: Overview sends people here, so
                    // say it here too.
                    if let s = stale {
                        Label("Not polling for \(s): its readings are old and nothing rotates. Restart it.",
                              systemImage: "exclamationmark.triangle.fill")
                            .font(.caption).foregroundStyle(Color.csWarn)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if d.installed {
                        Toggle(isOn: Binding(
                            get: { d.mode == "live" },
                            set: { on in
                                if on { goLive(install: false) } else {
                                    store.setDaemon(.dryRun, title: "Could not change the daemon's mode")
                                }
                            })) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("Live")
                                Text("Switches accounts. Off is a dry run: it says what it would do and does nothing.")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        .disabled(store.isBusy("daemon"))
                    }
                    if let v = d.daemonVersion { LabeledContent("Version", value: v) }
                    if let s = d.since { LabeledContent("Running for", value: Format.uptime(since: s, now: store.now)) }
                    if let at = store.snapshot?.daemon.lastPoll {
                        LabeledContent("Last poll", value: Format.age(at, now: store.now))
                    }
                    if let b = d.binary {
                        LabeledContent("Binary", value: (b as NSString).abbreviatingWithTildeInPath)
                        if !d.binaryIsThis {
                            Label("The service runs another build than this app uses (\(d.thisBinary ?? "?")).",
                                  systemImage: "exclamationmark.triangle").foregroundStyle(Color.csWarn).font(.caption)
                        }
                    }
                    if let f = d.file { LabeledContent("Service file", value: (f as NSString).abbreviatingWithTildeInPath) }
                    if let l = d.log {
                        LabeledContent("Log") {
                            Button((l as NSString).abbreviatingWithTildeInPath) {
                                NSWorkspace.shared.open(URL(fileURLWithPath: l))
                            }.buttonStyle(.link)
                        }
                    }
                }
                Section {
                    HStack(spacing: 8) {
                        if d.installed {
                            Button("Restart") { store.setDaemon(.restart, title: "Could not restart the daemon") }
                            if d.running {
                                Button("Stop") { store.setDaemon(.stop, title: "Could not stop the daemon") }
                            } else {
                                Button("Start") { store.setDaemon(.start, title: "Could not start the daemon") }
                            }
                            Spacer()
                            // Red, as destructive: the Form's accent tint drew it blue.
                            Button("Uninstall…", role: .destructive) { confirmUninstall = true }
                                .tint(.red).foregroundStyle(Color.csBad)
                        } else {
                            Button("Install (Dry Run)") { store.setDaemon(.install(live: false), title: "Could not install the daemon") }
                            Button("Install Live…") { goLive(install: true) }
                                .buttonStyle(PrimaryButtonStyle())
                        }
                        if store.isBusy("daemon") { ProgressView().controlSize(.small) }
                    }
                    .disabled(store.isBusy("daemon"))
                }
            } else if let e = store.daemonError, !store.daemonMissing {
                Section {
                    PaneUnavailable(error: e, what: "The service's status")
                    Button("Try Again") { store.loadDaemon() }.disabled(store.isBusy("load:daemon"))
                }
            } else if !store.daemonMissing {
                Section {
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Reading the service…").foregroundStyle(.secondary)
                    }
                }
            } else {
                Section {
                    HStack(spacing: 8) {
                        Text("The daemon service is not installed.")
                            .foregroundStyle(.secondary)
                        Spacer()
                        Button("Install (Dry Run)") { store.setDaemon(.install(live: false), title: "Could not install the daemon") }
                        Button("Install Live…") { goLive(install: true) }
                            .buttonStyle(PrimaryButtonStyle())
                    }
                    .disabled(store.isBusy("daemon") || store.binaryUnusable)
                    if store.binaryUnusable {
                        Text("Install it once claudeswitch is: see Settings → Advanced.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .tint(.csAccent)
        }
        // Read afresh each time the pane is shown: the status from when the
        // window opened, or from the last verb, may no longer be true.
        .onAppear { store.loadDaemon() }
        .confirming($pending)
        .confirmationDialog("Uninstall the daemon?", isPresented: $confirmUninstall) {
            Button("Uninstall", role: .destructive) { store.setDaemon(.uninstall, title: "Could not uninstall the daemon") }
        } message: {
            Text("The service is unloaded and its file removed. State, logs and stored accounts stay.")
        }
    }
}

// MARK: Advanced

struct AdvancedPane: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var login: LoginItem

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
        PaneHeader(title: "Advanced", subtitle: "Credential refresh, call budgets, and this app.")
            .padding([.horizontal, .top], 24)
        Form {
            // First, as the subtitle names it first, and named for what it is.
            if let schema = store.schema {
                Section("Credential refresh and call budgets") {
                    if store.values == nil, let e = store.valuesError {
                        PaneUnavailable(error: e, what: "The current settings")
                        Button("Try Again") { store.reloadValues() }
                    }
                    ForEach(schema.settings(in: .advanced)) { SettingRow(setting: $0) }
                }
            } else if let e = store.schemaError {
                Section("Credential refresh and call budgets") {
                    PaneUnavailable(error: e, what: "These settings")
                    Button("Try Again") { store.loadSettings() }
                }
            }
            Section("This app") {
                LabeledContent {
                    Picker("Appearance", selection: $store.appearance) {
                        ForEach(AppearancePref.allCases) { Text($0.label).tag($0) }
                    }
                    .pickerStyle(.segmented).labelsHidden().fixedSize()
                } label: {
                    described("Appearance", "System follows your Mac. Light or Dark keeps ClaudeSwitch in one "
                              + "appearance whatever the system uses.")
                }
                LabeledContent {
                    Picker("Usage in the popover", selection: $store.usageMode) {
                        ForEach(UsageMode.allCases) { Text($0.label).tag($0) }
                    }
                    .pickerStyle(.segmented).labelsHidden().fixedSize()
                } label: {
                    described("Usage in the popover", "Dials or bars for session and week. The switch in the "
                              + "popover changes this too.")
                }
                Picker("Open Claude Code in", selection: $store.terminal) {
                    ForEach(TerminalApp.allCases) { Text($0.rawValue).tag($0) }
                }
                Toggle(isOn: $store.compact) {
                    described("Icon only in the menu bar", "Hide the profile, account and percentage next to the rings.")
                }
                // With the app's other preferences (it sat under Daemon).
                if login.available {
                    Toggle("Open ClaudeSwitch at login", isOn: Binding(get: { login.enabled }, set: { login.set($0) }))
                    if let e = login.error { Text(e).font(.caption).foregroundStyle(Color.csBad) }
                } else {
                    Text("Open at login is available once the app is installed in Applications.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                LabeledContent("claudeswitch") {
                    HStack {
                        Text(store.binaryPath.map { ($0 as NSString).abbreviatingWithTildeInPath
                            + (store.binaryVersion.map { " · " + $0 } ?? "") } ?? "not found")
                            .foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                        Button("Choose…") { chooseBinary() }
                        if !store.configuredBinary.isEmpty {
                            Button("Reset") { store.configuredBinary = "" }
                                .help("Look for claudeswitch where installers put it")
                        }
                    }
                }
                // Missing or too old: the reason and the way to fix it, where
                // Overview's need sends people.
                if store.binaryUnusable, let p = store.problem {
                    VStack(alignment: .leading, spacing: 4) {
                        Label(p.message, systemImage: "exclamationmark.triangle.fill").foregroundStyle(Color.csWarn)
                        Text(CLI.installIntro).foregroundStyle(.secondary)
                        ForEach(CLI.installCommands, id: \.self) { c in
                            Text(c).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        }
                        Text(CLI.installOutro).foregroundStyle(.secondary)
                    }
                    .font(.caption)
                    .fixedSize(horizontal: false, vertical: true)
                }
                // A chosen file that cannot be run is passed over for the one
                // found: say so, or the choice looks ignored.
                if !store.configuredBinary.isEmpty,
                   (store.configuredBinary as NSString).expandingTildeInPath != store.binaryPath {
                    Label("The chosen \((store.configuredBinary as NSString).abbreviatingWithTildeInPath) cannot be run; "
                          + (store.binaryPath.map { "using \(($0 as NSString).abbreviatingWithTildeInPath)." } ?? "none found."),
                          systemImage: "exclamationmark.triangle")
                        .font(.caption).foregroundStyle(Color.csWarn)
                }
                AppFlowsRows(updates: AppHub.updates, notifier: AppHub.notifier, hotKey: AppHub.hotKey)
                LabeledContent {
                    Button("Quit ClaudeSwitch") { AppQuit.quit() }
                } label: {
                    described("Quit", "Closes the menu-bar app. The daemon keeps switching accounts; "
                              + "open ClaudeSwitch from Applications to bring the icon back.")
                }
            }
        }
        .formStyle(.grouped)
        .tint(.csAccent)
        }
    }

    /// A row's label with its description under it, as SettingRow has.
    func described(_ title: String, _ detail: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
            Text(detail).font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    func chooseBinary() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.message = "Choose the claudeswitch binary"
        panel.showsHiddenFiles = true
        if panel.runModal() == .OK, let url = panel.url {
            store.configuredBinary = url.path
        }
    }
}
