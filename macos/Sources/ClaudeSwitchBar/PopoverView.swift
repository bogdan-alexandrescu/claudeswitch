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
            // A binary missing, too old or too new: the banner in place of
            // the cards, whose buttons would run that binary and fail.
            if let s = store.snapshot, !Self.replacesCards(store.problem) {
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
                Text(store.refreshing || readingOverride ? "Reading…"
                     : "No state yet: is the daemon installed? Run `claudeswitch setup`.")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
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
            UpdateLine(updates: AppHub.updates)
            footer
        }
        .padding(16)
        .frame(width: 384)
        .onPreferenceChange(CardsHeight.self) { cardsHeight = $0 }
        // A note ("default now uses work-2") is news while the popover is
        // open; reopened later it would be stale.
        .onDisappear { store.clearNote(on: .popover) }
        .onAppear {
            store.acting(in: .popover)
            login.reload()
            store.refresh(full: true, profiles: true)
        }
    }

    /// The visible frame of the screen the popover opens on (menu bar and
    /// Dock excluded).
    static var visibleHeight: CGFloat {
        if let h = heightOverride { return h }
        let item = NSApp.windows.first { StatusMenu.isStatusItem($0) }?.screen
        return (item ?? NSScreen.main ?? NSScreen.screens.first)?.visibleFrame.height ?? 800
    }

    /// --render only: draws every card whole, whatever this Mac's screen.
    static var heightOverride: CGFloat?

    /// --render only: draws the popover as it is while the first read runs.
    var readingOverride = false

    /// A binary that cannot be used (missing, too old, or speaking a
    /// contract this app does not): the cards are not shown, only how to fix
    /// it.
    static func replacesCards(_ p: CLIError?) -> Bool {
        switch p {
        case .missing?, .tooOld?, .unsupportedContract?: return true
        default: return false
        }
    }

    var header: some View {
        HStack(alignment: .center, spacing: 8) {
            Wordmark()
            Spacer()
            if store.refreshing || readingOverride {
                ProgressView().controlSize(.mini).accessibilityLabel("Refreshing")
            }
            if let s = store.snapshot {
                DaemonBadge(daemon: s.daemon)
                UsageModeToggle(mode: $store.usageMode)
            }
        }
    }

    var footer: some View {
        HStack(spacing: 8) {
            // Not with a binary too old or too new either: it would run it.
            let canAdd = store.binaryPath != nil && !Self.replacesCards(store.problem)
            Button {
                store.addAccount = AddAccountRequest(profile: store.snapshot?.followed(store.menuProfile))
                showSettings()
            } label: {
                Label("Add account", systemImage: "plus")
            }
            .buttonStyle(.borderless)
            // Accent only when it can be used: a disabled link in the accent
            // colour read as active (QA).
            .foregroundStyle(canAdd ? Color.csAccent : Color.secondary)
            .disabled(!canAdd)
            Spacer()
            // As the icon's menu opens it: on Overview when Settings is
            // closed, where it is when it is open. The request opens the
            // window (SettingsOpener) and picks the pane.
            Button("Settings") {
                store.openSettings(WindowFocus.settings?.isVisible == true ? nil : .overview)
            }
                .buttonStyle(.borderless)
                .foregroundStyle(Color.csAccent)
                .keyboardShortcut(",", modifiers: .command)
        }
        // The ⋯ menu moved to the menu-bar icon's right-click (StatusMenu,
        // owner request 2026-10-26); its shortcuts stay here.
        .background {
            Group {
                Button("Refresh now") { store.refresh(full: true, profiles: true) }
                    .keyboardShortcut("r", modifiers: .command)
                Button("Quit ClaudeSwitch") { AppQuit.quit() }
                    .keyboardShortcut("q", modifiers: .command)
            }
            .opacity(0)
            .frame(width: 0, height: 0)
            .accessibilityHidden(true)
        }
    }

    func showSettings() { WindowFocus.openSettings(openWindow) }
}

private struct CardsHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

/// "Live · polled 40s ago", with a coloured dot. A daemon that is not
/// polling says so in the primary colour, and opens Settings → Daemon,
/// where it is started: the header was the only place it showed, in grey.
struct DaemonBadge: View {
    @EnvironmentObject var store: Store
    let daemon: DaemonView

    var polling: Bool {
        if case .polling = daemon.health { return true }
        return false
    }

    var body: some View {
        if polling {
            badge
        } else {
            Button { store.openSettings(.daemon) } label: { badge.contentShape(Rectangle()) }
                .buttonStyle(.plain)
                .help("Open Settings → Daemon to start it")
                .accessibilityHint("Opens Settings → Daemon")
        }
    }

    var badge: some View {
        HStack(spacing: 8) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(text).font(.caption.weight(polling ? .regular : .medium))
                .foregroundStyle(polling ? Color.secondary : Color.primary)
            if !polling {
                Image(systemName: "chevron.right").font(.caption2.weight(.semibold)).foregroundStyle(.secondary)
                    .accessibilityHidden(true)
            }
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
            .background(PillShape().fill(Color.csAccent))
            .foregroundStyle(Color.csOnAccent)
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
            if let n = store.chromeNotice(for: card) {
                ChromeSignInBanner(notice: n, card: card.name)
            }
            if let l = store.pinLiftNotice(card) { PinLiftBanner(lift: l) }
            if let a = card.active {
                VStack(alignment: .leading, spacing: 8) {
                    // Figures from before the login died: kept, their dots
                    // and figures dimmed; the labels and resets stay legible.
                    UsageView(account: a, card: card, now: now, dimmed: a.status == .needsLogin)
                    ForEach(Array(a.scopedWeekly.enumerated()), id: \.offset) { _, l in
                        ModelLimitRow(limit: l, level: card.level(of: l), now: now)
                    }
                    if a.status == .needsLogin {
                        HStack(spacing: 8) {
                            Label("needs sign-in · last read " + Format.age(a.lastAt, now: now),
                                  systemImage: "exclamationmark.triangle.fill")
                                .font(.caption).foregroundStyle(.red).lineLimit(2)
                                .help(a.lastError ?? "needs sign-in")
                            Spacer(minLength: 4)
                            Button("Sign in…") { store.beginSignIn(a.id) }.controlSize(.small)
                        }
                    } else if case .refused = a.status {
                        Label(Format.status(a.status, now: now), systemImage: "nosign")
                            .font(.caption).foregroundStyle(.red).lineLimit(2)
                    } else if let e = a.lastError {
                        Label(e, systemImage: "exclamationmark.triangle.fill")
                            .font(.caption).foregroundStyle(.red).lineLimit(2).help(e)
                    }
                }
                .padding(.top, 4)
            }
            PoolLine(card: card, now: now)
            HStack(spacing: 8) {
                Button {
                    store.openClaudeCode(card.name)
                } label: {
                    Text("Open Claude Code").lineLimit(1).frame(maxWidth: .infinity, minHeight: 32)
                }
                .buttonStyle(PillButtonStyle(primary: false))
                .disabled(store.binaryPath == nil || !card.canOpen || store.isBusy("run:\(card.name)"))
                .help(card.openBlocked ?? "Open Claude Code in \(card.name)")
                BestButton(card: card)
            }
            .padding(.top, 4)
            // Each greyed button says why, under the pair. A live account
            // that needs sign-in has its one remedy above (Sign in…): the
            // profile's own "not signed in" would point elsewhere for the
            // same trouble.
            if let why = card.openBlocked, card.active?.status != .needsLogin {
                OpenBlockedLine(text: why)
            }
            if let why = card.bestUnavailable {
                Text(why).font(.caption).foregroundStyle(.secondary).lineLimit(2).help(why)
                    .fixedSize(horizontal: false, vertical: true)
            } else if let warn = card.bestWarning {
                Label(warn, systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.orange).lineLimit(2).help(warn)
                    .fixedSize(horizontal: false, vertical: true)
            }
            // The first line of Next, Pin and ⋯ on one baseline.
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                if card.nextVisible {
                    // Its full height, up to three lines: given only one
                    // line's it was cut ("…under the 98…"). The popover has a
                    // fixed width, so the height is measured against it.
                    Text("Next: " + nextText)
                        .font(.caption).foregroundStyle(.secondary)
                        .lineLimit(6)
                        .fixedSize(horizontal: false, vertical: true)
                        .help(nextText)
                }
                Spacer(minLength: 0)
                PinToggle(card: card)
                HardPinMenu(card: card)
            }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10)
            .strokeBorder(followed ? Color.csAccent : Color.secondary.opacity(0.25), lineWidth: followed ? 2 : 1))
    }

    var titleRow: some View {
        HStack(spacing: 8) {
            Text(card.name).font(.headline).lineLimit(1).truncationMode(.middle)
                .help(card.name)
                .layoutPriority(1)
            if (store.snapshot?.cards.count ?? 0) > 1 {
                if followed {
                    MenuBarBadge()
                } else {
                    Button("Show in menu bar") { store.menuProfile = card.name }
                        .buttonStyle(.borderless).font(.caption)
                        .foregroundStyle(Color.csAccent)
                        .fixedSize()
                }
            }
            Spacer(minLength: 8)
            // Whole or not at all: squeezed beside a long name it showed ",".
            ViewThatFits(in: .horizontal) {
                Text(card.dirLabel).font(.caption).foregroundStyle(.secondary).lineLimit(1).fixedSize()
                    .help(card.dirLabel)
                Color.clear.frame(width: 0, height: 0)
            }
            if let c = collapse {
                IconButton(symbol: "chevron.up", label: "Collapse \(card.name)", action: c)
            }
        }
        // One height with or without the collapse button, so every card's
        // picker starts at the same distance below its title.
        .frame(height: 24)
    }

    var nextText: String { card.nextLine }
}

/// "Switch to best: w02 12%": the account why would pick now (owner
/// decision; the picker beside it switches to any other).
struct BestButton: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    var busy: Bool { store.isBusy("use:\(card.name)") }
    var disabled: Bool { card.best == nil || card.alreadyOnBest || store.binaryPath == nil || busy }

    /// A greyed button's second line is news, not the action ("next:
    /// work-2 · 12%"): drawn over the button, outside its dimming, where
    /// the label keeps its place. Dimmed with it, it measured 2.65:1.
    var subtitleOutside: Bool { disabled && !busy && !card.bestIsPrimary }

    var body: some View {
        Button {
            store.switchToBest(card)
        } label: {
            if busy {
                ProgressView().controlSize(.small).frame(maxWidth: .infinity, minHeight: 32)
            } else {
                lines(subtitle: !subtitleOutside)
            }
        }
        // Filled only when the daemon would move anyway (D3); quiet while it
        // stays, and when there is nowhere better to go.
        .buttonStyle(PillButtonStyle(primary: card.bestIsPrimary))
        .overlay {
            if subtitleOutside, card.bestSubtitle != nil {
                lines(title: false)
                    .font(.body.weight(.semibold))
                    .padding(.vertical, 7).padding(.horizontal, 12)
                    .foregroundStyle(Color.primary.opacity(0.75))
                    .allowsHitTesting(false)
                    .accessibilityHidden(true)
            }
        }
        .disabled(disabled)
        .help(card.bestUnavailable ?? (card.alreadyOnBest
            ? "No other account has more room than \(card.active?.id ?? "the active one"); the picker still switches"
            : "Switch \(card.name) to \(card.best?.id ?? "")"))
        .accessibilityLabel(card.alreadyOnBest ? "Already on the best account"
            : card.best.map { "Switch \(card.name) to \($0.id)" } ?? "No account to switch to")
    }

    /// The button's two lines, so a long id is never cut out of the verb.
    /// Either can be left out and still hold its place (the overlay draws
    /// the subtitle where the label would).
    func lines(title: Bool = true, subtitle: Bool = true) -> some View {
        VStack(spacing: 0) {
            Text(card.bestTitle).lineLimit(1).opacity(title ? 1 : 0)
            if let sub = card.bestSubtitle {
                Text(sub).font(.caption)
                    .lineLimit(1).truncationMode(.middle)
                    .opacity(subtitle ? 1 : 0)
            }
        }
        .frame(maxWidth: .infinity, minHeight: 32)
    }
}

/// Why Open Claude Code is greyed: the profile has no login of its own.
struct OpenBlockedLine: View {
    let text: String

    var body: some View {
        Label(text, systemImage: "person.crop.circle.badge.exclamationmark")
            .font(.caption).foregroundStyle(.orange)
            .fixedSize(horizontal: false, vertical: true)
    }
}

/// One per-model weekly limit: its name, a mini bar in the rings' usage
/// ramp (as the dials above it), and the figure, coloured, with a triangle,
/// when it is near or over the weekly trigger.
struct ModelLimitRow: View {
    let limit: LimitReading
    let level: Level?
    let now: Date
    @Environment(\.colorScheme) private var scheme

    var fill: Color {
        guard let p = limit.percent, p.isFinite else { return .clear }
        return BrandTokens(scheme).usage(min(max(p, 0), 100) / 100)
    }

    var body: some View {
        HStack(spacing: 8) {
            Text(Format.limitName(limit)).lineLimit(2).truncationMode(.tail)
                .help(Format.limitName(limit))
            if level == .near || level == .over {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(levelColor(level))
                    .accessibilityLabel(level == .over ? "over the weekly trigger" : "near the weekly trigger")
            }
            Spacer(minLength: 8)
            Capsule().fill(Color.secondary.opacity(0.18))
                .frame(width: 48, height: 4)
                .overlay(alignment: .leading) {
                    Capsule().fill(fill)
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
    case .ok?: return .csAccent
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
                    // A long id cut in its middle, as names are everywhere:
                    // its start and end tell accounts apart. The plan, at the
                    // end, gives way first. One Text: a menu's label draws
                    // only its first.
                    (Text(Format.middle(a.id, 28)).bold() + Text(detail(a.id)).foregroundColor(.secondary))
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
/// `use <id> --profile P --json`. One live in another profile that shares
/// its pool is shown "in use by" that profile, and not offered (D34).
struct AccountMenuItems: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    var body: some View {
        ForEach(card.accounts) { a in
            let elsewhere = store.snapshot?.liveElsewhere(a.id, notIn: card.name)
            // A Toggle puts the live account's check in the menu's gutter,
            // so every id starts in the same place.
            Toggle(isOn: Binding(get: { a.isActive }, set: { on in
                if on && !a.isActive && elsewhere == nil { store.use(a.id, profile: card.name) }
            })) {
                Text(Format.pickerLine(a, inUseBy: elsewhere, now: store.now))
            }
            .disabled(elsewhere != nil)
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
            Label(card.isPinned ? (store.pinIsHard(card) == true ? "Pinned (hard)" : "Pinned") : "Pin",
                  systemImage: card.isPinned ? "pin.fill" : "pin")
                .font(.caption)
        }
        .toggleStyle(PinToggleStyle())
        .controlSize(.small)
        .disabled(card.active == nil || store.isBusy("pin:\(card.name)"))
        .help(card.isPinned ? "Rotation is off in \(card.name): let it rotate again"
              : "Keep \(card.name) on \(card.active?.id ?? "its account"): turn rotation off"
                + (store.accountList?.knowsHardPin == true ? " until it is refused or needs sign-in" : ""))
        .accessibilityLabel(card.isPinned ? "Unpin \(card.name)" : "Pin \(card.name) to its account")
    }
}

/// Pin's look: off, the small bordered button (as ⋯ beside it); on, the
/// brand accent with onAccent text. The system's toggle button filled with
/// the system accent, a saturated blue beside the brand's light-blue pills
/// in dark mode.
struct PinToggleStyle: ToggleStyle {
    func makeBody(configuration: Configuration) -> some View {
        if configuration.isOn {
            Button { configuration.isOn.toggle() } label: {
                configuration.label.foregroundStyle(Color.csOnAccent)
            }
            .buttonStyle(.borderedProminent)
            .tint(Color.csAccent)
        } else {
            Button { configuration.isOn.toggle() } label: { configuration.label }
                .buttonStyle(.bordered)
        }
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
            // The greyed ▶'s reason, in the row: a disabled control shows
            // no tooltip. Not again when the line already says the live
            // account needs sign-in.
            if let why = card.openBlocked, card.active?.status != .needsLogin {
                OpenBlockedLine(text: why)
            }
            CardErrorView(card: card.name)
            PoolLine(card: card, now: store.now)
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.secondary.opacity(0.25)))
    }

    var row: some View {
        HStack(spacing: 8) {
            MiniRings(account: card.active, size: 24, dotRadius: 3)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 4) {
                    Text(card.name).font(.headline).lineLimit(1).truncationMode(.middle).help(card.name)
                    if card.isPinned {
                        Image(systemName: "pin.fill").font(.caption2).foregroundStyle(.secondary)
                            .accessibilityLabel("pinned")
                    }
                }
                // One line when it fits; else the figures go under the lead,
                // which truncates only when it alone is too long. The figures
                // never do (a short id was cut to "wor…eam" to make room).
                let parts = Format.cardSummaryParts(card)
                ViewThatFits(in: .horizontal) {
                    Text(parts.figures.map { parts.lead + " · " + $0 } ?? parts.lead).lineLimit(1).fixedSize()
                    VStack(alignment: .leading, spacing: 1) {
                        Text(parts.lead).lineLimit(1).truncationMode(.middle)
                        if let f = parts.figures { Text(f).lineLimit(1).fixedSize() }
                    }
                }
                .font(.caption)
                .foregroundStyle(trouble ? Color.red : Color.secondary)
                .help(Format.cardSummary(card))
            }
            Spacer(minLength: 8)
            IconButton(symbol: "play.fill", label: card.openBlocked ?? "Open Claude Code in \(card.name)",
                       busy: store.isBusy("run:\(card.name)")) { store.openClaudeCode(card.name) }
                // As the open card's Open Claude Code: greyed without a binary too.
                .disabled(store.binaryPath == nil || !card.canOpen)
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

/// C2: the profile's Chrome profile cannot follow the last rotation, since
/// the account has no Chrome profile of its own. The amber warning tone, a
/// brand pill that gives it one (C4: never a sign-in), and a dismiss that
/// lasts until the next rotation.
struct ChromeSignInBanner: View {
    @EnvironmentObject var store: Store
    let notice: ChromeSignInNotice
    let card: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            AmberBannerLine(symbol: "exclamationmark.triangle.fill", text: notice.text,
                            dismissHelp: "Dismiss until the next rotation",
                            dismissLabel: "Dismiss the Claude in Chrome notice") {
                store.dismissChromeNotice(notice.key)
            }
            // P2, C4: once the account has its own Chrome profile,
            // claudeswitch opens it after every switch.
            Button {
                // Dismissed once it worked: a failure keeps the notice.
                store.setAccountChrome(notice.account, .createNew) { store.dismissChromeNotice(notice.key) }
            } label: {
                if store.isBusy("chrome:\(notice.account)") {
                    ProgressView().controlSize(.small).frame(maxWidth: .infinity, minHeight: 28)
                } else {
                    Text(notice.ownButton).lineLimit(1).truncationMode(.middle).frame(maxWidth: .infinity, minHeight: 28)
                }
            }
            .buttonStyle(PillButtonStyle(primary: true))
            .disabled(store.binaryPath == nil || store.isBusy("chrome:\(notice.account)"))
            .help("Creates a Chrome profile for \(notice.account) and opens it at the sign-in pages; " +
                  "sign in there once, and claudeswitch opens it after every switch to \(notice.account)")
        }
        .amberBanner()
    }
}

/// The popover's amber banners (Claude in Chrome, a lifted pin, a binary
/// that cannot be used) in one style: a tinted fill, an orange border.
extension View {
    func amberBanner() -> some View {
        frame(maxWidth: .infinity, alignment: .leading)
            .padding(8)
            .background(RoundedRectangle(cornerRadius: 8).fill(Color.orange.opacity(0.12)))
            .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.orange.opacity(0.5)))
    }
}

/// An amber banner's first line: a filled orange symbol, the text, and a
/// full-size × that dismisses it.
struct AmberBannerLine: View {
    let symbol: String
    let text: String
    var dismissHelp = "Dismiss"
    var dismissLabel = "Dismiss"
    let dismiss: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: symbol).foregroundStyle(.orange).accessibilityHidden(true)
            Text(text).font(.caption).foregroundStyle(.primary)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button(action: dismiss) { Image(systemName: "xmark") }
                .buttonStyle(.borderless)
                .help(dismissHelp)
                .accessibilityLabel(dismissLabel)
        }
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
                // One command per line, never wrapped: a wrapped URL read as
                // a command of its own.
                VStack(alignment: .leading, spacing: 4) {
                    Text(CLI.installIntro).font(.caption)
                    ForEach(CLI.installCommands, id: \.self) { InstallCommandRow(command: $0) }
                    Text(CLI.installOutro).font(.caption).fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .amberBanner()
    }

    var isTooOld: Bool {
        if case .tooOld = problem { return true }
        return false
    }
}

/// One install command: cut in its middle when it is wider than the
/// banner (a hidden horizontal scroll cut the URL with no sign of more),
/// and a Copy button that puts the whole command on the clipboard.
struct InstallCommandRow: View {
    let command: String
    @State private var copied = false

    var body: some View {
        HStack(spacing: 8) {
            Text(command).font(.system(.caption, design: .monospaced))
                .lineLimit(1).truncationMode(.middle)
                .frame(maxWidth: .infinity, alignment: .leading)
                .help(command)
            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(command, forType: .string)
                copied = true
                DispatchQueue.main.asyncAfter(deadline: .now() + 2) { copied = false }
            } label: {
                Label(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc")
                    .font(.caption)
            }
            .buttonStyle(.borderless)
            .foregroundStyle(Color.csAccent)
            .fixedSize()
            .help("Copy “\(command)”")
            .accessibilityLabel("Copy the command \(command)")
        }
    }
}

/// A card's inline error (M11), if it has one.
struct CardErrorView: View {
    @EnvironmentObject var store: Store
    let card: String

    var body: some View {
        if let e = store.cardError(card) {
            InlineErrorView(title: e.title, message: e.message, hint: e.hint, retryLine: e.retryLine(now: store.now)) {
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
            // Up to four lines across the width it is given, not fixed to its
            // ideal height: in Settings the banner sits in the window's
            // bottom inset, where the window asks for an ideal size with no
            // width, and a fixed height then came back as one word a line.
            // The window's content was then taller than the window, and
            // AppKit pushed its top under the title bar (2026-10-21).
            Text(text).font(.caption).textSelection(.enabled)
                .lineLimit(1...4)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button(action: dismiss) { Image(systemName: "xmark") }
                .buttonStyle(.borderless)
                .accessibilityLabel("Dismiss")
        }
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color.green.opacity(0.1)))
        // Opaque under the tint: rows scrolled beneath the banner showed
        // through its text.
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .windowBackgroundColor)))
    }
}
