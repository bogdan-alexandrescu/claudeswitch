import AppKit
import ClaudeSwitchCore
import SwiftUI

/// The first-run setup window (F9), its own NSWindow so it can open at
/// launch, before anything else is on screen. Reopened from the ⋯ menu.
@MainActor
enum WelcomeWindow {
    static let title = "Welcome to ClaudeSwitch"
    private static var window: NSWindow?
    private static var closeObserver: NSObjectProtocol?

    static func show(store: Store) {
        NSApp.activate(ignoringOtherApps: true)
        if let w = window {
            w.makeKeyAndOrderFront(nil)
            return
        }
        let host = NSHostingController(rootView: WelcomeView(close: { window?.close() }).environmentObject(store))
        let w = NSWindow(contentViewController: host)
        w.title = title
        w.styleMask = [.titled, .closable]
        w.isReleasedWhenClosed = false
        w.center()
        window = w
        closeObserver = NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: w,
                                                               queue: .main) { _ in
            MainActor.assumeIsolated {
                if let o = closeObserver { NotificationCenter.default.removeObserver(o) }
                closeObserver = nil
                window = nil
            }
        }
        w.makeKeyAndOrderFront(nil)
    }
}

/// The steps, one card each: the current one open with its action and
/// Skip, the others a line with their state.
struct WelcomeView: View {
    @EnvironmentObject var store: Store
    @State var flow = SetupFlow()
    /// --render: the facts to draw instead of the store's.
    var renderFacts: SetupFacts?
    var close: () -> Void = {}

    enum Sheet: String, Identifiable {
        case saveLogin, addAccount, newProfile
        var id: String { rawValue }
    }

    @State private var sheet: Sheet?
    @State private var error: String?
    @State private var installing = false
    @State private var dontShowAgain = FirstRun.dontShowAgain()

    var facts: SetupFacts {
        if let f = renderFacts { return f }
        let accounts = store.accountList?.accounts.count ?? store.accountIDs.count
        let profiles = store.profiles?.profiles.filter { $0.declared && $0.name != StateFile.defaultProfile }.count ?? 0
        return SetupFacts(accounts: accounts, profiles: profiles, daemonInstalled: store.daemon?.installed ?? false)
    }

    /// The flow with what is true now.
    var shown: SetupFlow {
        var f = flow
        f.facts = facts
        return f
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            header
            if store.problem == .missing && renderFacts == nil {
                InlineErrorView(title: "claudeswitch is not installed", message: CLIError.missing.message,
                                hint: CLI.installSteps, retryLine: nil) {}
            }
            VStack(spacing: 8) {
                ForEach(SetupStep.allCases) { s in step(s) }
            }
            if let e = error {
                InlineErrorView(title: "That step did not finish", message: e, hint: "", retryLine: nil) { error = nil }
            }
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Each step can be skipped. ⋯ → Set up… opens this again.")
                        .font(.caption).foregroundStyle(.secondary)
                    Toggle("Don't show this again", isOn: $dontShowAgain)
                        .toggleStyle(.checkbox).font(.caption)
                        .help("Stop opening this window by itself at launch. ⋯ → Set up… still opens it.")
                        .onChange(of: dontShowAgain) { on in
                            if renderFacts == nil { FirstRun.setDontShowAgain(on) }
                        }
                }
                Spacer()
                if shown.finished {
                    Button("Done") { close() }.buttonStyle(PrimaryButtonStyle()).keyboardShortcut(.defaultAction)
                } else {
                    Button("Finish later") { close() }.keyboardShortcut(.cancelAction)
                }
            }
        }
        .padding(28)
        .frame(width: 560)
        .tint(.csAccent)
        .onAppear {
            guard renderFacts == nil else { return }
            store.refresh(full: true, profiles: true)
            store.loadDaemon()
        }
        .sheet(item: $sheet, onDismiss: { store.refresh(full: true, profiles: true) }) { s in
            switch s {
            case .saveLogin:
                AddAccountSheet(request: AddAccountRequest(), route: .current).environmentObject(store)
            case .addAccount:
                AddAccountSheet(request: AddAccountRequest()).environmentObject(store)
            case .newProfile:
                NewProfileSheet().environmentObject(store)
            }
        }
    }

    var header: some View {
        HStack(alignment: .center, spacing: 16) {
            TwinRingsView(week: 0.62, session: 0.3, dotRadius: 2.4, satelliteRadius: 4.6)
                .frame(width: 64, height: 64)
            VStack(alignment: .leading, spacing: 6) {
                Text("claudeswitch")
                    .font(BrandFont.wordmark(24))
                    .tracking(-0.055 * 24)
                Text("Set up rotation between your Claude accounts. Four steps, each done by the claudeswitch CLI; "
                     + "nothing here reads your keychain.")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    func step(_ s: SetupStep) -> some View {
        let f = shown
        let status = f.status(s)
        let current = f.current == s
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                badge(s, status: status, current: current)
                VStack(alignment: .leading, spacing: 2) {
                    Text(s.title).fontWeight(current ? .semibold : .regular)
                        .foregroundStyle(status == .pending || current ? Color.primary : Color.secondary)
                    if let note = note(s, status: status) {
                        Text(note).font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if !current && status != .pending {
                    Button(status == .skipped ? "Do it now" : "Again") { flow.reopen(s) }
                        .buttonStyle(.borderless).font(.caption)
                        .foregroundStyle(Color.csAccent)
                }
            }
            if current {
                Text(s.detail).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 34)
                HStack(spacing: 8) {
                    Button(s.action) { run(s) }
                        .buttonStyle(PrimaryButtonStyle())
                        .disabled(store.binaryPath == nil && renderFacts == nil || installing)
                    if s == .addAccounts && f.facts.accounts > 0 {
                        Button("Continue") { flow.complete(s) }
                    }
                    if installing && s == .daemon { ProgressView().controlSize(.small) }
                    Spacer()
                    Button("Skip") { flow.skip(s) }.buttonStyle(.borderless).foregroundStyle(.secondary)
                }
                .padding(.leading, 34)
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous)
            .fill(current ? Color.csAccent.opacity(0.08) : Color.secondary.opacity(0.05)))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous)
            .strokeBorder(current ? Color.csAccent.opacity(0.6) : Color.secondary.opacity(0.15), lineWidth: 1))
    }

    func badge(_ s: SetupStep, status: StepStatus, current: Bool) -> some View {
        ZStack {
            Circle().fill(current ? Color.csAccent : status == .done ? Color.csAccent.opacity(0.18)
                          : Color.secondary.opacity(0.12))
            if status == .done && !current {
                Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Color.csAccent)
            } else {
                Text("\(s.rawValue + 1)").font(.system(size: 12, weight: .semibold).monospacedDigit())
                    .foregroundStyle(current ? Color.csOnAccent : Color.secondary)
            }
        }
        .frame(width: 24, height: 24)
        .accessibilityLabel(status == .done ? "done" : status == .skipped ? "skipped" : "step \(s.rawValue + 1)")
    }

    func note(_ s: SetupStep, status: StepStatus) -> String? {
        let f = shown.facts
        switch (s, status) {
        case (_, .skipped): return "Skipped"
        case (.saveLogin, .done), (.addAccounts, _) where f.accounts > 0:
            return f.accounts == 1 ? "1 account saved" : "\(f.accounts) accounts saved"
        case (.workProfile, .done): return f.profiles == 1 ? "1 profile besides default" : "\(f.profiles) profiles besides default"
        case (.daemon, .done): return store.daemon.map { "Installed, \($0.mode == "live" ? "live" : "dry run")" } ?? "Installed"
        default: return nil
        }
    }

    func run(_ s: SetupStep) {
        error = nil
        switch s {
        case .saveLogin, .addAccounts, .workProfile:
            Task {
                guard await ensureConfig() else { return }
                sheet = s == .saveLogin ? .saveLogin : s == .addAccounts ? .addAccount : .newProfile
            }
        case .daemon:
            installing = true
            Task {
                let r = await store.call("daemon") { $0.daemon(.install(live: false)) }
                installing = false
                if case .failure(let e) = r { error = e.hint.isEmpty ? e.message : e.message + " " + e.hint }
                store.loadDaemon()
                store.refresh(full: true)
            }
        }
    }

    /// `add --json` appends to the config but does not create it: have the
    /// CLI start an empty one first (`init --empty --json`), where the CLI
    /// looks for it. The app never writes claudeswitch's files itself.
    func ensureConfig() async -> Bool {
        let path = store.data.settings?.path ?? FirstRun.defaultConfigPath()
        switch await store.call("init", { FirstRun.ensureConfig(path: path, cli: $0) }) {
        case .success(let wrote):
            if wrote { store.refresh(full: true, profiles: true) }
            return true
        case .failure(let e):
            error = e.hint.isEmpty ? e.message : e.message + " " + e.hint
            return false
        }
    }
}
