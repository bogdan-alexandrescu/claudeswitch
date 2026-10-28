import AppKit
import ClaudeSwitchCore
import SwiftUI

/// The first-run setup window (F9), its own NSWindow so it can open at
/// launch, before anything else is on screen. Reopened from the menu-bar
/// icon's menu (Set up…). It grows and shrinks as steps open and close,
/// and keeps its middle where it is, as the update window does.
@MainActor
enum WelcomeWindow {
    static let title = "Welcome to ClaudeSwitch"
    private static var window: NSWindow?
    private static var closeObserver: NSObjectProtocol?
    private static var sizeObservers: [NSObjectProtocol] = []

    static func show(store: Store) {
        WindowFocus.closePopover()
        NSApp.activate(ignoringOtherApps: true)
        if let w = window {
            WindowFocus.bringForward { w }
            return
        }
        let host = NSHostingController(rootView: WelcomeView(close: { window?.close() }).environmentObject(store))
        host.sizingOptions = [.preferredContentSize]
        let w = NSWindow(contentViewController: host)
        w.title = title
        w.styleMask = [.titled, .closable]
        w.isReleasedWhenClosed = false
        w.setContentSize(host.view.fittingSize)
        WindowFocus.centre(w, on: WindowFocus.pointerScreen)
        sizeObservers = WindowFocus.keepCentred(w)
        window = w
        closeObserver = NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: w,
                                                               queue: .main) { _ in
            MainActor.assumeIsolated {
                if let o = closeObserver { NotificationCenter.default.removeObserver(o) }
                sizeObservers.forEach(NotificationCenter.default.removeObserver)
                sizeObservers = []
                closeObserver = nil
                window = nil
            }
        }
        WindowFocus.bringForward { w }
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
    /// The binary's banner, closed with its ×.
    @State private var bannerHidden = false

    var facts: SetupFacts {
        if let f = renderFacts { return f }
        let ids = store.accountList?.accounts.map(\.id) ?? store.accountIDs
        let profiles = store.profiles?.profiles.filter { $0.declared && $0.name != StateFile.defaultProfile }.count ?? 0
        return SetupFacts(accounts: ids.count, profiles: profiles,
                          daemonInstalled: SetupFacts.daemonInstalled(status: store.daemon, missing: store.daemonMissing),
                          currentLogin: SetupFacts.currentLogin(profiles: store.profiles, accounts: ids))
    }

    /// `daemon status`'s failure, while it has not said whether the
    /// service is installed.
    var daemonReadError: CallError? { renderFacts == nil ? store.daemonError : nil }

    /// Missing, too old or too new: the steps cannot run.
    var banner: SetupBanner? { renderFacts == nil ? FirstRun.banner(store.problem) : nil }

    /// The flow with what is true now.
    var shown: SetupFlow {
        var f = flow
        f.facts = facts
        return f
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            header
            if let b = banner, !bannerHidden {
                InlineErrorView(title: b.title, message: b.message, hint: b.hint, retryLine: nil) { bannerHidden = true }
            } else if let b = banner {
                // Closed with its ×: the greyed step buttons still say why.
                Label {
                    Text(b.line).foregroundStyle(.secondary)
                } icon: {
                    Image(systemName: "exclamationmark.octagon.fill").foregroundStyle(Color.csBad)
                }
                .font(.caption)
            }
            VStack(spacing: 8) {
                ForEach(SetupStep.allCases) { s in step(s) }
            }
            if let e = error {
                InlineErrorView(title: "That step did not finish", message: e, hint: "", retryLine: nil) { error = nil }
            }
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text(FirstRun.reopenHint)
                        .font(.caption).foregroundStyle(.secondary)
                    Toggle(FirstRun.dontShowLabel, isOn: $dontShowAgain)
                        .toggleStyle(.checkbox).font(.caption)
                        .tint(.csAccent)
                        .help(FirstRun.dontShowHelp)
                        .onChange(of: dontShowAgain) { on in
                            if renderFacts == nil { FirstRun.setDontShowAgain(on) }
                        }
                }
                Spacer()
                // No tint here: Finish later and Continue draw as every other
                // secondary button does, plain bordered.
                if shown.finished {
                    Button("Done") { close() }.buttonStyle(PrimaryButtonStyle()).keyboardShortcut(.defaultAction)
                        // Escape closes the window too, as Finish later's does.
                        .background(Button("", action: close).keyboardShortcut(.cancelAction)
                            .opacity(0).accessibilityHidden(true))
                } else {
                    Button("Finish Later") { close() }.keyboardShortcut(.cancelAction)
                }
            }
        }
        .padding(28)
        .frame(width: 560)
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
            TwinRingsView(week: 0.3, session: 0.62, dotRadius: 2.4, satelliteRadius: 4.6)
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
                    if let note = f.note(s, daemonMode: store.daemon?.mode) {
                        Text(note).font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if !current && status != .pending {
                    let action = s.reopenAction(status)
                    Button(s.reopen(status)) { reopen(s, action) }
                        .buttonStyle(.borderless).font(.caption)
                        .foregroundStyle(Color.csAccent)
                        .disabled(action == .run && stepsOff)
                }
            }
            if current {
                Text(s.detail).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 34)
                // The binary's banner, or its line, says it already when it is the binary.
                if s == .daemon, banner == nil, case .readFailed(let e) = f.daemonOffer(readError: daemonReadError) {
                    InlineError(message: e.message, hint: e.hint).padding(.leading, 34)
                }
                HStack(spacing: 8) {
                    // Return runs the open step; Done is the default once all are.
                    if s == .daemon {
                        daemonAction(f)
                    } else {
                        Button(s.action) { run(s) }
                            .buttonStyle(PrimaryButtonStyle())
                            .keyboardShortcut(.defaultAction)
                            .disabled(stepsOff)
                    }
                    if f.canContinue(s) {
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

    /// The step buttons are off while the binary cannot run them.
    var stepsOff: Bool { store.binaryPath == nil && renderFacts == nil || banner != nil || installing }

    /// Step 4's button: Install only when the CLI said the service is not
    /// installed. `daemon install --dry-run` over an installed one rewrites
    /// it as dry run, a live one too, so until `daemon status` answers the
    /// step says it is reading, or shows the failure with Try Again.
    @ViewBuilder
    func daemonAction(_ f: SetupFlow) -> some View {
        switch f.daemonOffer(readError: daemonReadError) {
        case .install:
            Button(SetupStep.daemon.action) { run(.daemon) }
                .buttonStyle(PrimaryButtonStyle())
                .keyboardShortcut(.defaultAction)
                .disabled(stepsOff)
        case .settings:
            Button(FirstRun.daemonSettings) { store.openSettings(.daemon) }
                .buttonStyle(PrimaryButtonStyle())
                .keyboardShortcut(.defaultAction)
        case .reading:
            ProgressView().controlSize(.small)
            Text(FirstRun.daemonReading).foregroundStyle(.secondary)
        case .readFailed:
            Button(FirstRun.tryAgain) { store.loadDaemon() }
                .buttonStyle(PrimaryButtonStyle())
                .keyboardShortcut(.defaultAction)
                .disabled(stepsOff || store.isBusy("load:daemon"))
            if store.isBusy("load:daemon") { ProgressView().controlSize(.small) }
        }
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

    /// A done or skipped step's link: open the step again, run its sheet
    /// (Add another), or Settings → Daemon (step 4's Open).
    func reopen(_ s: SetupStep, _ action: ReopenAction) {
        switch action {
        case .reopen:
            // Whether what it does is true now decides when it closes again.
            flow.facts = facts
            flow.reopen(s)
        case .run: run(s)
        case .daemonSettings: store.openSettings(.daemon)
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
            // Never over a service that is there, or one not read yet.
            guard shown.daemonOffer() == .install else { return }
            installing = true
            Task {
                let r = await store.call("daemon") { $0.daemon(.install(live: false)) }
                installing = false
                switch r {
                case .success: flow.complete(.daemon)
                case .failure(let e): error = e.hint.isEmpty ? e.message : e.message + " " + e.hint
                }
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
