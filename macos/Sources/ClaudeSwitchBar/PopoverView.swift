import AppKit
import ClaudeSwitchCore
import SwiftUI

struct PopoverView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var login: LoginItem

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            header
            if let p = store.problem { ProblemBanner(problem: p) }
            if let s = store.snapshot {
                NextLine(decision: s.decision)
                if s.profiles.count > 1 { ProfilesSection(profiles: s.profiles) }
                Divider()
                let rows = VStack(alignment: .leading, spacing: 12) {
                    ForEach(s.accounts) { a in
                        AccountRow(account: a, settings: s.settings, now: s.now)
                    }
                }
                // A scroll view has no height of its own in a menu-bar window,
                // so only a list too long for the screen gets one.
                if s.accounts.count > 5 {
                    ScrollView { rows }.frame(height: 460)
                } else {
                    rows
                }
            } else if store.problem == nil {
                Text(store.refreshing ? "Reading…" : "No state yet: is the daemon installed? Run `claudeswitch setup`.")
                    .foregroundStyle(.secondary)
            }
            if let r = store.lastSwitch { SwitchResult(id: r.id, outcome: r.outcome) }
            Divider()
            footer
        }
        .padding(14)
        .frame(width: 380)
        .onAppear {
            login.reload()
            store.refresh(full: false)
        }
    }

    var header: some View {
        HStack(alignment: .firstTextBaseline) {
            Text("claudeswitch").font(.headline)
            if let d = store.snapshot?.daemon {
                Text(d.mode)
                    .font(.caption)
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .background(Capsule().fill(daemonColor(d).opacity(0.18)))
                    .foregroundStyle(daemonColor(d))
            }
            Spacer()
            if let s = store.snapshot {
                Text("polled " + Format.age(s.daemon.lastPoll, now: s.now))
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    func daemonColor(_ d: DaemonView) -> Color {
        switch d.health {
        case .never, .stale: return .red
        case .polling: return d.live ? .green : .orange
        }
    }

    var footer: some View {
        VStack(alignment: .leading, spacing: 6) {
            if login.available {
                Toggle("Launch at login", isOn: Binding(get: { login.enabled }, set: { login.set($0) }))
                if let e = login.error { Text(e).font(.caption).foregroundStyle(.red) }
            }
            Toggle("Icon only in the menu bar", isOn: $store.compact)
            HStack {
                Text(store.binaryPath.map { "\($0)" + (store.binaryVersion.map { " · \($0)" } ?? "") }
                     ?? "claudeswitch not found")
                    .font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
                Spacer()
                Button("Binary…") { chooseBinary() }.controlSize(.small)
            }
            HStack {
                Button("Refresh") { store.refresh(full: true) }
                Spacer()
                Button("Quit") { NSApp.terminate(nil) }
            }
        }
        .toggleStyle(.checkbox)
    }

    func chooseBinary() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.message = "Choose the claudeswitch binary"
        panel.showsHiddenFiles = true
        NSApp.activate(ignoringOtherApps: true)
        if panel.runModal() == .OK, let url = panel.url {
            store.configuredBinary = url.path
        }
    }
}

struct ProblemBanner: View {
    let problem: CLIError

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Label(problem.message, systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
                .fixedSize(horizontal: false, vertical: true)
            if problem == .missing || isTooOld {
                Text(CLI.installSteps)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 6).fill(Color.orange.opacity(0.1)))
    }

    var isTooOld: Bool {
        if case .tooOld = problem { return true }
        return false
    }
}

struct NextLine: View {
    let decision: Decision?

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 4) {
                Text("Next:").foregroundStyle(.secondary)
                Text(Format.next(decision)).bold()
            }
            if let r = decision?.reason, !r.isEmpty {
                Text(r).font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}

struct ProfilesSection: View {
    let profiles: [ProfileView]

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("Profiles").font(.caption).foregroundStyle(.secondary)
            ForEach(profiles) { i in
                HStack(spacing: 6) {
                    Text(i.name).font(.caption.monospaced())
                    Text("→ " + (i.active ?? "–")).font(.caption)
                    if i.pinned != nil { Image(systemName: "pin.fill").font(.caption2) }
                    Spacer()
                    if let d = i.decision {
                        Text(Format.next(d)).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        }
    }
}

struct AccountRow: View {
    @EnvironmentObject var store: Store
    let account: AccountView
    let settings: ConfigSettings
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Image(systemName: account.isActive ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(account.isActive ? Color.accentColor : .secondary)
                Text(account.id).bold()
                if account.isPinned { Image(systemName: "pin.fill").font(.caption) }
                StatusChip(status: account.status, now: now)
                Spacer()
                if !account.isActive { switchButton }
            }
            WindowBar(label: "5h", window: account.fiveHour,
                      trigger: settings.trigger(for: "five_hour"), now: now)
            WindowBar(label: "7d", window: account.sevenDay,
                      trigger: settings.trigger(for: "seven_day"), now: now)
            ForEach(Array(account.scopedWeekly.enumerated()), id: \.offset) { _, l in
                HStack {
                    Text(Format.limitName(l)).font(.caption)
                    Text(Format.pct(l.percent)).font(.caption.monospacedDigit())
                    Spacer()
                    Text(Format.resets(l.resetsAt, now: now)).font(.caption).foregroundStyle(.secondary)
                }
                .padding(.leading, 22)
            }
            if let why = account.why, !why.isEmpty {
                Text(why).font(.caption).foregroundStyle(.secondary).padding(.leading, 22)
            }
            if let e = account.lastError {
                Text(e).font(.caption).foregroundStyle(.red).lineLimit(2).padding(.leading, 22)
            }
        }
    }

    @ViewBuilder var switchButton: some View {
        if store.switching == account.id {
            ProgressView().controlSize(.small)
        } else {
            Button("Switch") { store.switchTo(account.id) }
                .controlSize(.small)
                .disabled(store.switching != nil || store.binaryPath == nil)
                .help("Run `claudeswitch use \(account.id)`")
        }
    }
}

struct StatusChip: View {
    let status: AccountStatus
    let now: Date

    var body: some View {
        Text(Format.status(status, now: now))
            .font(.caption2)
            .padding(.horizontal, 5).padding(.vertical, 1)
            .background(Capsule().fill(color.opacity(0.18)))
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

struct WindowBar: View {
    let label: String
    let window: WindowReading?
    let trigger: Double
    let now: Date

    var body: some View {
        HStack(spacing: 6) {
            Text(label).font(.caption.monospaced()).frame(width: 16, alignment: .leading)
            GeometryReader { g in
                ZStack(alignment: .leading) {
                    Capsule().fill(Color.secondary.opacity(0.2))
                    Capsule().fill(color)
                        .frame(width: g.size.width * CGFloat(min(max(pct ?? 0, 0), 100) / 100))
                    // The switch threshold, so "near" is visible, not inferred.
                    Rectangle().fill(Color.primary.opacity(0.5))
                        .frame(width: 1)
                        .offset(x: g.size.width * CGFloat(min(trigger, 100) / 100))
                }
            }
            .frame(height: 6)
            Text(Format.pct(pct)).font(.caption.monospacedDigit()).frame(width: 34, alignment: .trailing)
            Text(Format.resets(window?.resetsAt, now: now))
                .font(.caption).foregroundStyle(.secondary)
                .frame(width: 104, alignment: .leading)
        }
        .padding(.leading, 22)
    }

    var pct: Double? { window?.utilization }

    var color: Color {
        guard let p = pct else { return .clear }
        switch Level.of(p, trigger: trigger) {
        case .ok: return .green
        case .near: return .orange
        case .over: return .red
        }
    }
}

struct SwitchResult: View {
    @EnvironmentObject var store: Store
    let id: String
    let outcome: UseOutcome

    var body: some View {
        HStack(alignment: .top) {
            Image(systemName: outcome.ok ? "checkmark.circle.fill" : "xmark.octagon.fill")
                .foregroundStyle(outcome.ok ? .green : .red)
            Text(outcome.message)
                .font(.caption)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            Spacer()
            Button { store.dismissSwitchResult() } label: { Image(systemName: "xmark") }
                .buttonStyle(.borderless)
        }
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 6)
            .fill((outcome.ok ? Color.green : Color.red).opacity(0.1)))
    }
}
