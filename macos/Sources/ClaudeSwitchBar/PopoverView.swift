import AppKit
import ClaudeSwitchCore
import SwiftUI

/// The menu-bar popover: a card per profile (IMPROVEMENTS M7, mockup 1).
/// The profile the menu-bar title follows is expanded and outlined; the
/// others are compact rows that expand on demand.
struct PopoverView: View {
    @EnvironmentObject var store: Store
    @EnvironmentObject var login: LoginItem
    @Environment(\.openWindow) private var openWindow
    @State var expanded: Set<String> = []
    /// The cards' measured height: they scroll only past what the screen
    /// can show (QA: a fixed 520-pt cap cut short lists and wasted tall
    /// screens).
    @State private var cardsHeight: CGFloat = 0

    /// Header, banners and footer, around the cards.
    static let chrome: CGFloat = 140

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            if let p = store.problem { ProblemBanner(problem: p) }
            if let s = store.snapshot {
                let followed = s.followed(store.menuProfile)
                let cards = VStack(spacing: 8) {
                    ForEach(s.cards) { c in
                        if c.name == followed || expanded.contains(c.name) || s.cards.count == 1 {
                            ProfileCardView(card: c, followed: c.name == followed, now: s.now,
                                            collapse: c.name == followed ? nil : { expanded.remove(c.name) })
                        } else {
                            CompactCardView(card: c) { expanded.insert(c.name) }
                        }
                    }
                }
                .background(GeometryReader { g in
                    Color.clear.preference(key: CardsHeight.self, value: g.size.height)
                })
                let room = PopoverLayout.maxHeight(visible: Self.visibleHeight) - Self.chrome
                if cardsHeight > room {
                    ScrollView { cards }.frame(height: room)
                } else {
                    cards
                }
            } else if store.problem == nil {
                Text(store.refreshing ? "Reading…" : "No state yet: is the daemon installed? Run `claudeswitch setup`.")
                    .foregroundStyle(.secondary)
            }
            // M11: an error with no card to show on, inline; never a modal
            // alert in the popover.
            if let a = store.alert(on: .popover) {
                InlineErrorView(title: a.title, message: a.message, hint: a.hint, retryLine: nil) {
                    store.clearAlert(on: .popover)
                }
            }
            if let n = store.note(on: .popover) { NoteBanner(text: n) { store.clearNote(on: .popover) } }
            Divider()
            footer
        }
        .padding(16)
        .frame(width: 384)
        .onPreferenceChange(CardsHeight.self) { cardsHeight = $0 }
        .onAppear {
            store.acting(in: .popover)
            login.reload()
            store.refresh(full: true, profiles: true)
        }
    }

    /// The visible frame of the screen the popover opens on (menu bar and
    /// Dock excluded).
    static var visibleHeight: CGFloat {
        (NSScreen.main ?? NSScreen.screens.first)?.visibleFrame.height ?? 800
    }

    var header: some View {
        HStack(alignment: .firstTextBaseline) {
            Text("ClaudeSwitch").font(.headline)
            Spacer()
            if let s = store.snapshot {
                DaemonBadge(daemon: s.daemon)
            }
        }
    }

    var footer: some View {
        HStack(spacing: 8) {
            let canAdd = store.binaryPath != nil
            Button {
                store.addAccount = AddAccountRequest(profile: store.snapshot?.followed(store.menuProfile))
                showSettings()
            } label: {
                Label("Add account", systemImage: "plus")
            }
            .buttonStyle(.borderless)
            // Accent only when it can be used: a disabled link in the accent
            // colour read as active (QA).
            .foregroundStyle(canAdd ? Color.accentColor : Color.secondary)
            .disabled(!canAdd)
            Spacer()
            Button("Settings…") { showSettings() }
                .buttonStyle(.borderless)
                .keyboardShortcut(",", modifiers: .command)
            Menu {
                Button("Refresh") { store.refresh(full: true, profiles: true) }
                Divider()
                Button("Quit ClaudeSwitch") { NSApp.terminate(nil) }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .accessibilityLabel("More")
        }
    }

    func showSettings() {
        NSApp.activate(ignoringOtherApps: true)
        openWindow(id: SettingsView.windowID)
    }
}

private struct CardsHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

/// "Live · polled 40s ago", with a coloured dot.
struct DaemonBadge: View {
    let daemon: DaemonView

    var body: some View {
        HStack(spacing: 8) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(text).font(.caption).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }

    var text: String {
        switch daemon.health {
        case .never: return "No daemon"
        case .stale: return daemon.mode.prefix(1).uppercased() + daemon.mode.dropFirst()
        case .polling:
            return (daemon.live ? "Live" : "Dry run") + " · polled " + Format.age(daemon.lastPoll, now: daemon.now)
        }
    }

    var color: Color {
        switch daemon.health {
        case .never, .stale: return .red
        case .polling: return daemon.live ? .green : .orange
        }
    }
}

/// "Menu bar": which profile the menu-bar title follows.
struct MenuBarBadge: View {
    var body: some View {
        Label("Menu bar", systemImage: "menubar.rectangle")
            .labelStyle(.titleAndIcon)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 8).padding(.vertical, 2)
            .background(Capsule().fill(Color.accentColor))
            .foregroundStyle(.white)
            .accessibilityLabel("Shown in the menu bar")
    }
}

/// The expanded card: account picker, session and week bars against the
/// profile's thresholds, Open Claude Code and Switch to best.
struct ProfileCardView: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard
    let followed: Bool
    let now: Date
    var collapse: (() -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            titleRow
            HStack(spacing: 8) {
                AccountPicker(card: card)
                if let a = card.active, store.chrome?.supported != false {
                    IconButton(symbol: "globe", label: "Open Chrome for \(a.id)",
                               busy: store.isBusy("chrome:\(a.id)")) { store.openChrome(a.id, card: card.name) }
                }
            }
            CardErrorView(card: card.name)
            if let a = card.active {
                VStack(alignment: .leading, spacing: 8) {
                    UsageBar(title: "Session", window: a.fiveHour, trigger: card.switchAt, now: now)
                    UsageBar(title: "Week", window: a.sevenDay, trigger: card.switchAtWeekly, now: now)
                    ForEach(Array(a.scopedWeekly.enumerated()), id: \.offset) { _, l in
                        ModelLimitRow(limit: l, level: card.level(of: l), now: now)
                    }
                    if a.status == .needsLogin || a.lastError != nil {
                        Label(a.lastError ?? "needs login", systemImage: "exclamationmark.triangle.fill")
                            .font(.caption).foregroundStyle(.red).lineLimit(2)
                            .help(a.lastError ?? "needs login")
                    }
                }
                .padding(.top, 4)
            }
            HStack(spacing: 8) {
                Button {
                    store.openClaudeCode(card.name)
                } label: {
                    Text("Open Claude Code").lineLimit(1).frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(store.binaryPath == nil || !card.canOpen)
                .help(card.openBlocked ?? "Open Claude Code in \(card.name)")
                BestButton(card: card)
            }
            .padding(.top, 4)
            if let why = card.openBlocked {
                Label(why, systemImage: "person.crop.circle.badge.exclamationmark")
                    .font(.caption).foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            } else if let why = card.bestUnavailable {
                Text(why).font(.caption).foregroundStyle(.secondary).lineLimit(2).help(why)
            } else if let warn = card.bestWarning {
                Label(warn, systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.orange).lineLimit(2).help(warn)
            }
            HStack(alignment: .top, spacing: 8) {
                if card.nextVisible {
                    Text("Next: " + nextText)
                        .font(.caption).foregroundStyle(.secondary)
                        .lineLimit(3)
                        .help(nextText)
                }
                Spacer(minLength: 0)
                PinToggle(card: card)
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10)
            .strokeBorder(followed ? Color.accentColor : Color.secondary.opacity(0.25), lineWidth: followed ? 2 : 1))
    }

    var titleRow: some View {
        HStack(spacing: 8) {
            Text(card.name).font(.headline).lineLimit(1).truncationMode(.tail)
                .help(card.name)
                .layoutPriority(1)
            if followed {
                MenuBarBadge()
            } else {
                Button("Show in menu bar") { store.menuProfile = card.name }
                    .buttonStyle(.borderless).font(.caption)
            }
            Spacer(minLength: 8)
            Text(card.dirLabel).font(.caption).foregroundStyle(.secondary)
                .lineLimit(1).truncationMode(.middle)
                .help(card.dirLabel)
            if let c = collapse {
                IconButton(symbol: "chevron.up", label: "Collapse \(card.name)", action: c)
            }
        }
    }

    var nextText: String {
        guard let d = card.decision else { return "unknown" }
        let head = Format.next(d)
        return d.reason.isEmpty ? head : head + " — " + d.reason
    }
}

/// "Switch to best: w02 12%": the account why would pick now (owner
/// decision; the picker beside it switches to any other).
struct BestButton: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    var body: some View {
        Button {
            store.switchToBest(card)
        } label: {
            if store.isBusy("use:\(card.name)") {
                ProgressView().controlSize(.small).frame(maxWidth: .infinity)
            } else {
                // Two lines, so a long id is never cut out of the verb.
                VStack(spacing: 0) {
                    Text(card.bestTitle).lineLimit(1)
                    if let sub = card.bestSubtitle {
                        Text(sub).font(.caption)
                            .lineLimit(1).truncationMode(.middle)
                    }
                }
                .frame(maxWidth: .infinity)
            }
        }
        .controlSize(.large)
        .disabled(card.best == nil || card.alreadyOnBest || store.binaryPath == nil
            || store.isBusy("use:\(card.name)"))
        .help(card.bestUnavailable ?? (card.alreadyOnBest
            ? "No other account has more room than \(card.active?.id ?? "the active one"); the picker still switches"
            : "Switch \(card.name) to \(card.best?.id ?? "")"))
        .accessibilityLabel(card.alreadyOnBest ? "Already on the best account"
            : card.best.map { "Switch \(card.name) to \($0.id)" } ?? "No account to switch to")
    }
}

/// One per-model weekly limit: its name, a mini bar and the figure,
/// coloured by how close it is to the weekly trigger.
struct ModelLimitRow: View {
    let limit: LimitReading
    let level: Level?
    let now: Date

    var body: some View {
        HStack(spacing: 8) {
            Text(Format.limitName(limit)).lineLimit(1).truncationMode(.tail)
                .help(Format.limitName(limit))
            Spacer(minLength: 8)
            Capsule().fill(Color.secondary.opacity(0.18))
                .frame(width: 48, height: 4)
                .overlay(alignment: .leading) {
                    Capsule().fill(levelColor(level))
                        .frame(width: 48 * CGFloat(min(max(limit.percent ?? 0, 0), 100) / 100), height: 4)
                }
                .accessibilityHidden(true)
            Text(Format.pct(limit.percent) + " · " + Format.resets(limit.resetsAt, now: now))
                .foregroundStyle(level.map { $0 == .ok ? Color.secondary : levelColor($0) } ?? .secondary)
                .monospacedDigit()
                .lineLimit(1)
        }
        .font(.caption)
        .accessibilityElement(children: .combine)
    }
}

func levelColor(_ l: Level?) -> Color {
    switch l {
    case .ok?: return .accentColor
    case .near?: return .orange
    case .over?: return .red
    case nil: return .clear
    }
}

/// The live account, as a menu of the profile's pool.
struct AccountPicker: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    var body: some View {
        Menu {
            AccountMenuItems(card: card)
        } label: {
            HStack {
                if let a = card.active {
                    (Text(a.id).bold() + Text(detail(a.id)).foregroundColor(.secondary))
                        .lineLimit(1).truncationMode(.tail)
                } else {
                    Text("No account live").foregroundColor(.secondary)
                }
                Spacer()
            }
        }
        .menuStyle(.borderlessButton)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 8).padding(.vertical, 8)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.secondary.opacity(0.08)))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.secondary.opacity(0.25)))
        .disabled(store.isBusy("use:\(card.name)") || card.accounts.isEmpty)
        .help(card.active.flatMap { store.state?.emails[$0.id] } ?? "")
        .accessibilityLabel("Account live in \(card.name): \(card.active?.id ?? "none")")
    }

    /// The plan, as the account list records it (no scope since lane 16).
    func detail(_ id: String) -> String {
        store.accountList?.account(id)?.plan.map { " · " + $0 } ?? ""
    }
}

/// The pool's accounts, with their headroom; choosing one runs
/// `use <id> --profile P --json`.
struct AccountMenuItems: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    var body: some View {
        ForEach(card.accounts) { a in
            Button {
                if !a.isActive { store.use(a.id, profile: card.name) }
            } label: {
                Text((a.isActive ? "✓ " : "") + a.id + "  " + Format.pct(a.bindingPct) + " · " + a.status.label)
            }
            .disabled(a.isActive)
        }
    }
}

/// Pin: freeze the profile on its live account (`account pin`), or let
/// rotation run again (`account unpin --profile`).
struct PinToggle: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    var body: some View {
        Toggle(isOn: Binding(get: { card.isPinned },
                             set: { store.setPinned($0, profile: card.name, account: card.active?.id) })) {
            Label(card.isPinned ? "Pinned" : "Pin", systemImage: card.isPinned ? "pin.fill" : "pin")
                .font(.caption)
        }
        .toggleStyle(.button)
        .controlSize(.small)
        .disabled(card.active == nil || store.isBusy("pin:\(card.name)"))
        .help(card.isPinned ? "Rotation is off in \(card.name): let it rotate again"
              : "Keep \(card.name) on \(card.active?.id ?? "its account"): turn rotation off")
        .accessibilityLabel(card.isPinned ? "Unpin \(card.name)" : "Pin \(card.name) to its account")
    }
}

/// A collapsed card: name, one summary line (trouble first), Open Claude
/// Code and expand.
struct CompactCardView: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard
    let expand: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            row
            CardErrorView(card: card.name)
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.secondary.opacity(0.25)))
    }

    var row: some View {
        HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 4) {
                    Text(card.name).font(.headline).lineLimit(1).truncationMode(.tail).help(card.name)
                    if card.isPinned {
                        Image(systemName: "pin.fill").font(.caption2).foregroundStyle(.secondary)
                            .accessibilityLabel("pinned")
                    }
                }
                Text(Format.cardSummary(card)).font(.caption)
                    .foregroundStyle(trouble ? Color.red : Color.secondary)
                    .lineLimit(1).truncationMode(.tail)
                    .help(Format.cardSummary(card))
            }
            Spacer(minLength: 8)
            IconButton(symbol: "play.fill", label: card.openBlocked ?? "Open Claude Code in \(card.name)",
                       busy: store.isBusy("run:\(card.name)")) { store.openClaudeCode(card.name) }
                .disabled(!card.canOpen)
            IconButton(symbol: "chevron.down", label: "Expand \(card.name)", action: expand)
        }
    }

    var trouble: Bool {
        switch card.active?.status {
        case .needsLogin?, .refused?: return true
        default: return false
        }
    }
}

/// A square bordered icon button with a VoiceOver label.
struct IconButton: View {
    let symbol: String
    let label: String
    var busy = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Group {
                if busy { ProgressView().controlSize(.small) } else { Image(systemName: symbol) }
            }
            .frame(width: 16, height: 16)
        }
        .buttonStyle(.bordered)
        .help(label)
        .accessibilityLabel(label)
        .disabled(busy)
    }
}

/// "Session 62% · resets 2h 10m" over a bar with a tick at the threshold.
struct UsageBar: View {
    let title: String
    let window: WindowReading?
    let trigger: Double
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(title)
                Spacer()
                Text(detail).foregroundStyle(.secondary).monospacedDigit()
            }
            .font(.caption)
            GeometryReader { g in
                ZStack(alignment: .leading) {
                    Capsule().fill(Color.secondary.opacity(0.18))
                    Capsule().fill(color)
                        .frame(width: g.size.width * CGFloat(min(max(pct ?? 0, 0), 100) / 100))
                    // The switch threshold, so "near" is visible, not inferred.
                    RoundedRectangle(cornerRadius: 1).fill(Color.primary.opacity(0.6))
                        .frame(width: 2, height: 10)
                        .offset(x: g.size.width * CGFloat(min(trigger, 100) / 100) - 1)
                }
            }
            .frame(height: 6)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(title): \(detail), switches at \(Int(trigger))%")
    }

    var pct: Double? { window?.utilization }

    var detail: String {
        let r = Format.resets(window?.resetsAt, now: now)
        return Format.pct(pct) + (r.isEmpty ? "" : " · " + r)
    }

    var color: Color {
        guard let p = pct else { return .clear }
        return levelColor(Level.of(p, trigger: trigger))
    }
}

struct ProblemBanner: View {
    let problem: CLIError

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label {
                Text(problem.message).foregroundStyle(.primary)
            } icon: {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
            }
            .fixedSize(horizontal: false, vertical: true)
            if problem == .missing || isTooOld {
                Text(CLI.installSteps)
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.primary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.orange.opacity(0.12)))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.orange.opacity(0.5)))
    }

    var isTooOld: Bool {
        if case .tooOld = problem { return true }
        return false
    }
}

/// A card's inline error (M11), if it has one.
struct CardErrorView: View {
    @EnvironmentObject var store: Store
    let card: String

    var body: some View {
        if let e = store.cardError(card) {
            InlineErrorView(title: e.title, message: e.message, hint: e.hint, retryLine: e.retryLine()) {
                store.dismissCardError(card)
            }
        }
    }
}

/// A refused action, inline: the title, the CLI's message verbatim, its
/// hint, "Try again at …" when the CLI gave retry_at, and a dismiss ×. The
/// popover shows errors this way, never as a modal alert (M11).
struct InlineErrorView: View {
    let title: String
    let message: String
    let hint: String
    let retryLine: String?
    let dismiss: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "exclamationmark.octagon.fill").foregroundStyle(.red)
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.caption.weight(.semibold))
                Text(message).font(.caption)
                if !hint.isEmpty {
                    Text(hint).font(.caption).foregroundStyle(.secondary)
                }
                if let r = retryLine {
                    Text(r).font(.caption.weight(.semibold))
                }
            }
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button(action: dismiss) { Image(systemName: "xmark") }
                .buttonStyle(.borderless)
                .accessibilityLabel("Dismiss")
        }
        .padding(8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.red.opacity(0.1)))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.red.opacity(0.35)))
        .accessibilityElement(children: .combine)
    }
}

struct NoteBanner: View {
    let text: String
    let dismiss: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
            Text(text).font(.caption).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
            Spacer()
            Button(action: dismiss) { Image(systemName: "xmark") }
                .buttonStyle(.borderless)
                .accessibilityLabel("Dismiss")
        }
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.green.opacity(0.1)))
    }
}
