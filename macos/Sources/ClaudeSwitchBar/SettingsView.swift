import AppKit
import ClaudeSwitchCore
import SwiftUI

enum Pane: String, CaseIterable, Identifiable {
    case profiles = "Profiles", accounts = "Accounts", rotation = "Rotation", polling = "Polling",
         daemon = "Daemon", history = "History", health = "Health", advanced = "Advanced"

    var id: String { rawValue }

    var symbol: String {
        switch self {
        case .profiles: return "person.2"
        case .accounts: return "person.crop.circle"
        case .rotation: return "arrow.triangle.2.circlepath"
        case .polling: return "timer"
        case .daemon: return "gearshape.2"
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
    @EnvironmentObject var store: Store
    @State var pane: Pane? = .profiles
    /// --render only: an off-screen capture does not draw the sidebar's
    /// vibrant material, so the render draws a plain copy of it.
    var renderSidebar = false

    var body: some View {
        Group {
            if renderSidebar {
                HStack(spacing: 0) {
                    VStack(alignment: .leading, spacing: 2) {
                        Wordmark(size: 14, mark: 22).padding(.horizontal, 8).padding(.bottom, 12)
                        ForEach(Pane.allCases) { p in
                            Label {
                                Text(p.rawValue).fontWeight(p == pane ? .semibold : .regular)
                            } icon: {
                                Image(systemName: p.symbol).foregroundStyle(p == pane ? Color.csAccent : Color.secondary)
                            }
                            .padding(.horizontal, 8).padding(.vertical, 5)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(RoundedRectangle(cornerRadius: 6)
                                .fill(p == pane ? Color.csAccent.opacity(0.14) : Color.clear))
                        }
                        Spacer()
                    }
                    .padding(.horizontal, 10).padding(.top, 40)
                    .frame(width: 200)
                    .background(Color.secondary.opacity(0.08))
                    Divider()
                    detail
                }
            } else {
                split
            }
        }
    }

    var split: some View {
        NavigationSplitView {
            List(Pane.allCases, selection: $pane) { p in
                Label(p.rawValue, systemImage: p.symbol).tag(p)
            }
            .safeAreaInset(edge: .top, spacing: 0) {
                Wordmark(size: 14, mark: 22)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 18).padding(.top, 4).padding(.bottom, 8)
            }
            .navigationSplitViewColumnWidth(min: 170, ideal: 190, max: 240)
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
        pane = r.pane
        store.next.settingsRequest = nil
    }

    var detail: some View {
            Group {
                switch pane ?? .profiles {
                case .profiles: ProfilesPane()
                case .accounts: AccountsPane()
                case .rotation: SchemaPane(pane: .rotation, title: "Rotation",
                                           subtitle: "When an account is rotated away from, and where to.")
                case .polling: SchemaPane(pane: .polling, title: "Polling",
                                          subtitle: "How often the daemon reads each account's usage.")
                case .daemon: DaemonPane()
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
                Section {
                    ForEach(schema.settings(in: pane)) { s in
                        SettingRow(setting: s)
                    }
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
    @State private var text = ""
    @State private var edited = false
    @State private var cliError: AppError?
    @State private var saved: ConfigSetResult?

    var current: String { store.values?.values[setting.key] ?? setting.defaultValue }
    var local: String? { edited ? setting.validate(text) : nil }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            LabeledContent {
                control.frame(maxWidth: 220)
            } label: {
                VStack(alignment: .leading, spacing: 2) {
                    Text(settingLabel(setting.key))
                    Text(SettingNames.description(setting)).font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            HStack(spacing: 8) {
                Text("Default " + SettingNames.value(setting, setting.defaultValue) + range)
                    .font(.caption).foregroundStyle(.tertiary)
                Spacer()
                if store.isBusy("set:\(setting.key)") { ProgressView().controlSize(.mini) }
            }
            if let e = local {
                InlineError(message: "Must be " + e, hint: "")
            } else if let e = cliError {
                InlineError(message: e.message, hint: e.hint)
            } else if let s = saved {
                Text("Saved: \(SettingNames.value(setting, s.previous)) → \(SettingNames.value(setting, s.value))" + (s.daemonRunning ? " · the running daemon picks it up" : ""))
                    .font(.caption).foregroundStyle(.green)
                ForEach(s.warnings, id: \.self) { w in
                    Label(w, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                }
            }
        }
        .onAppear { if !edited { text = SettingNames.editable(setting, current) } }
        .onChange(of: current) { v in if !edited { text = SettingNames.editable(setting, v) } }
    }

    @ViewBuilder var control: some View {
        if setting.type == "enum" {
            Picker("", selection: Binding(get: { current }, set: { save($0) })) {
                ForEach(setting.enumValues, id: \.self) { Text(SettingNames.value(setting, $0)).tag($0) }
            }
            .labelsHidden()
            .accessibilityLabel(settingLabel(setting.key))
        } else {
            TextField("", text: Binding(get: { text }, set: { text = $0; edited = true; cliError = nil }),
                      prompt: Text(SettingNames.editable(setting, setting.defaultValue)))
                .labelsHidden()
                .textFieldStyle(.roundedBorder)
                .multilineTextAlignment(.trailing)
                .onSubmit { if local == nil, text != SettingNames.editable(setting, current) { save(text) } }
                .accessibilityLabel(settingLabel(setting.key))
        }
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
            Label(message, systemImage: "xmark.octagon.fill").foregroundStyle(.red)
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
                    LabeledContent("Status", value: d.running ? "running" : d.loaded ? "loaded, not running"
                                   : d.installed ? "installed, stopped" : "not installed")
                    if d.installed {
                        Toggle("Live (switches accounts; off is dry run)", isOn: Binding(
                            get: { d.mode == "live" },
                            set: { on in
                                if on { goLive(install: false) } else {
                                    store.setDaemon(.dryRun, title: "Could not change the daemon's mode")
                                }
                            }))
                        .disabled(store.isBusy("daemon"))
                    }
                    if let v = d.daemonVersion { LabeledContent("Version", value: v) }
                    if let s = d.since { LabeledContent("Running since", value: Format.age(s, now: Date())) }
                    if let b = d.binary {
                        LabeledContent("Binary", value: b)
                        if !d.binaryIsThis {
                            Label("The service runs another build than this app uses (\(d.thisBinary ?? "?")).",
                                  systemImage: "exclamationmark.triangle").foregroundStyle(.orange).font(.caption)
                        }
                    }
                    if let f = d.file { LabeledContent("Service file", value: f) }
                    if let l = d.log {
                        LabeledContent("Log") {
                            Button(l) { NSWorkspace.shared.open(URL(fileURLWithPath: l)) }.buttonStyle(.link)
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
                            Button("Uninstall…", role: .destructive) { confirmUninstall = true }
                        } else {
                            Button("Install (dry run)") { store.setDaemon(.install(live: false), title: "Could not install the daemon") }
                            Button("Install live…") { goLive(install: true) }
                                .buttonStyle(PrimaryButtonStyle())
                        }
                        if store.isBusy("daemon") { ProgressView().controlSize(.small) }
                    }
                    .disabled(store.isBusy("daemon"))
                }
            } else {
                Section {
                    HStack(spacing: 8) {
                        if store.isBusy("load:daemon") { ProgressView().controlSize(.small) }
                        Text(store.isBusy("load:daemon") ? "Reading the service…" : "The daemon service is not installed.")
                            .foregroundStyle(.secondary)
                        Spacer()
                        Button("Install (dry run)") { store.setDaemon(.install(live: false), title: "Could not install the daemon") }
                        Button("Install live…") { goLive(install: true) }
                            .buttonStyle(PrimaryButtonStyle())
                    }
                    .disabled(store.isBusy("daemon") || store.binaryPath == nil)
                }
            }
            Section("This app") {
                if login.available {
                    Toggle("Open ClaudeSwitch at login", isOn: Binding(get: { login.enabled }, set: { login.set($0) }))
                    if let e = login.error { Text(e).font(.caption).foregroundStyle(.red) }
                } else {
                    Text("Launch at login is available once the app is installed in Applications.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .formStyle(.grouped)
        .tint(.csAccent)
        }
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

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
        PaneHeader(title: "Advanced", subtitle: "Credential refresh, call budgets, and this app.")
            .padding([.horizontal, .top], 24)
        Form {
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
                LabeledContent("claudeswitch") {
                    HStack {
                        Text(store.binaryPath.map { $0 + (store.binaryVersion.map { " · " + $0 } ?? "") } ?? "not found")
                            .foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                        Button("Choose…") { chooseBinary() }
                    }
                }
                AppFlowsRows(updates: AppHub.updates, notifier: AppHub.notifier, hotKey: AppHub.hotKey)
            }
            if let schema = store.schema {
                Section("Settings") {
                    ForEach(schema.settings(in: .advanced)) { SettingRow(setting: $0) }
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
