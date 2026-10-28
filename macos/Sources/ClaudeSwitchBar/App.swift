import AppKit
import OSLog
import ClaudeSwitchCore
import SwiftUI

@main
struct ClaudeSwitchBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var store = AppHub.store
    @StateObject private var login = LoginItem()

    /// --render: drawing the screens to files, then exiting.
    static let rendering = CommandLine.arguments.contains("--render")

    init() {
        // Before the status item exists, so AppKit places it by this on a
        // first launch: near the right edge, clear of a notch. Never over
        // a stored position (a ⌘-drag).
        if !Self.rendering {
            StatusItemPlacement.seed(.standard)
        }
    }

    var body: some Scene {
        MenuBarExtra {
            PopoverView()
                .environmentObject(store)
                .environmentObject(login)
        } label: {
            MenuBarLabel(title: Self.rendering ? .rendering
                                : MenuTitle.of(store.binaryUnusable ? nil : store.snapshot,
                                               compact: store.compact, profile: store.menuProfile,
                                               problem: store.problem),
                         glyph: GlyphReading.of(store.binaryUnusable ? nil : store.snapshot,
                                                profile: store.menuProfile))
                .background(SettingsOpener(store: store))
        }
        .menuBarExtraStyle(.window)

        Window(SettingsView.windowTitle, id: SettingsView.windowID) {
            SettingsView()
                .environmentObject(store)
                .environmentObject(login)
        }
        .defaultSize(width: 880, height: 620)
        .windowResizability(.contentMinSize)
    }
}

/// `log stream --predicate 'subsystem == "xyz.claudeswitch.menubar"'` shows
/// whether the status item was made and drawn.
let menuBarLog = Logger(subsystem: "xyz.claudeswitch.menubar", category: "menubar")

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationWillFinishLaunching(_ notification: Notification) {
        listenForURLs()
    }

    /// Any quit, the app menu's ⌘Q included: close what would refuse it.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        MainActor.assumeIsolated { AppQuit.closeSheets() }
        return .terminateNow
    }

    /// claudeswitch://switch-best?profile=…, pin, unpin, status (F6): the
    /// URL scheme Shortcuts' "Open URL" and Raycast use.
    func listenForURLs() {
        NSAppleEventManager.shared().setEventHandler(self, andSelector: #selector(handleGetURL(_:withReplyEvent:)),
                                                     forEventClass: AEEventClass(kInternetEventClass),
                                                     andEventID: AEEventID(kAEGetURL))
    }

    @objc func handleGetURL(_ event: NSAppleEventDescriptor, withReplyEvent reply: NSAppleEventDescriptor) {
        guard let s = event.paramDescriptor(forKeyword: AEKeyword(keyDirectObject))?.stringValue,
              let url = URL(string: s) else { return }
        open(url)
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        urls.forEach(open)
    }

    func open(_ url: URL) {
        guard let c = ShortcutURL.parse(url) else {
            flowsLog.error("not a claudeswitch command: \(url.absoluteString, privacy: .public)")
            return
        }
        flowsLog.notice("shortcut \(c.verb, privacy: .public)")
        Task { @MainActor in ShortcutRunner.run(c) }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        // LSUIElement does this for the bundle; this covers `swift run`.
        NSApp.setActivationPolicy(.accessory)
        BrandFont.register()
        let args = CommandLine.arguments
        // --render-icon <dir.iconset>: the app icon at every iconset size
        // (macos/scripts/make-icon.sh turns it into AppIcon.icns).
        if let i = args.firstIndex(of: "--render-icon"), args.count > i + 1 {
            let dir = args[i + 1]
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            for (name, px) in AppIcon.iconset {
                guard let png = AppIcon.png(pixels: px) else {
                    FileHandle.standardError.write(Data("could not draw \(name)\n".utf8))
                    exit(1)
                }
                try? png.write(to: URL(fileURLWithPath: dir + "/" + name))
            }
            exit(0)
        }
        if let i = args.firstIndex(of: "--render"), args.count > i + 2 {
            // --now 2026-10-08T02:45:41Z: the clock the fixtures were written at.
            let now = args.firstIndex(of: "--now").flatMap { j in
                args.count > j + 1 ? ISO8601DateFormatter().date(from: args[j + 1]) : nil
            }
            // --version 0.8.17: the release the renders show, for the app and
            // the daemon alike.
            let version = args.firstIndex(of: "--version").flatMap { j in args.count > j + 1 ? args[j + 1] : nil }
            Task { @MainActor in
                Render.run(fixtures: args[i + 1], out: args[i + 2], now: now, version: version,
                           least: args.contains("--min"))
                exit(0)
            }
            return
        }
        // SwiftUI may have set its own URL handler since; this one wins.
        listenForURLs()
        Task { @MainActor in
            AppHub.start()
            StatusMenu.shared.install()
        }
        let compact = UserDefaults.standard.bool(forKey: "compactMenuBar")
        menuBarLog.notice("launched \(Bundle.main.bundlePath, privacy: .public); icon only: \(compact, privacy: .public)")
        // SwiftUI makes the status item itself and says nothing when the
        // system refuses it (macOS 26: System Settings → Menu Bar → "Allow in
        // the Menu Bar" off for this app), so look for its window.
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
            let items = NSApp.windows.filter { String(describing: type(of: $0)).contains("StatusBar") }
            if items.isEmpty {
                menuBarLog.error("no status item window 3s after launch: check System Settings → Menu Bar → Allow in the Menu Bar for ClaudeSwitch")
            }
            for w in items {
                menuBarLog.notice("status item window \(String(describing: type(of: w)), privacy: .public) frame \(NSStringFromRect(w.frame), privacy: .public) visible \(w.isVisible, privacy: .public)")
            }
        }
        // Once the item is placed, make sure it is not under the notch.
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) { Task { @MainActor in NotchCheck.run() } }
    }
}

/// `ClaudeSwitch --render <fixtures-dir> <out-dir> [--now <RFC 3339>] [--version <v>] [--min]` draws
/// the menu-bar label, the popover (collapsed and expanded), every Settings
/// pane, the profile editors and every sheet, in light and dark, from the
/// JSON in a directory (the test fixtures, or macos/RenderFixtures) to PNGs,
/// so the layout can be checked without a screen recording permission. It
/// runs no command and reads nothing else. `--now` fixes the clock; without
/// it, a minute after the newest reading.
@MainActor
enum Render {
    static func run(fixtures: String, out: String, now fixed: Date? = nil, version: String? = nil,
                    least: Bool = false) {
        // Every card whole, whatever this Mac's screen (PopoverView's
        // height cap measures the screen the renders run on).
        PopoverView.heightOverride = 4000
        func data(_ n: String) -> Data? { try? Data(contentsOf: URL(fileURLWithPath: fixtures + "/" + n)) }
        let state = data("state.json").flatMap(StateFile.init(data:))
        let now = fixed ?? state?.accounts.values.compactMap(\.lastAt).max()?.addingTimeInterval(60) ?? Date()
        let profiles = data("cli-profile-list.json").flatMap(ProfileList.init(data:))
        let why = data("why-profiles.json").flatMap(WhyReport.init(data:))
        let snap = Snapshot.build(state: state, why: why,
                                  settings: data("config.json").flatMap(ConfigSettings.init(data:)),
                                  profiles: profiles, now: now)
        var fx = PreviewData()
        fx.state = state
        fx.why = why
        fx.profiles = profiles
        // A set may bring its own Chrome state (RenderFixtures/tutorial).
        fx.chrome = (data("render-chrome-list.json") ?? data("cli-chrome-list-some.json")).flatMap(ChromeList.init(data:))
        fx.chromeProfiles = data("render-chrome-profiles.json").flatMap(ChromeProfiles.init(data:))
        fx.accountList = data("cli-account-list.json").flatMap(AccountList.init(data:))
        // Shared pools: `pool list --json`, or a binary from before pools
        // (a set brings a `usage` error object in its place).
        if let d = data("cli-pool-list.json") {
            fx.pools = PoolList(data: d)
            fx.poolsUnsupported = fx.pools == nil && AppError(data: d)?.code == "usage"
        }
        fx.schema = data("cli-config-schema.json").flatMap(ConfigSchema.init(data:))
        fx.values = data("config.json").flatMap(ConfigValues.init(data:))
        fx.daemon = data("cli-daemon-status.json").flatMap(DaemonStatus.init(data:))
        fx.recovery = data("cli-recovery.json").flatMap(RecoveryItem.list(data:))
        fx.history = data("cli-history-usage.json").flatMap(UsageHistory.init(data:))
        fx.doctor = data("cli-doctor.json").flatMap(DoctorReport.init(data:))
        fx.cleanPlan = data("cli-clean-plan.json").flatMap(CleanPlan.init(data:))
        fx.cleanRuns = data("cli-clean-runs.json").flatMap(CleanRunSummary.list(data:))
        // The runs fixture postdates the overlay sets' fixed clock: keep its
        // runs in the past, the newest two hours before now.
        if let newest = fx.cleanRuns?.compactMap(\.at).max(), newest > now {
            let shift = now.addingTimeInterval(-7200).timeIntervalSince(newest)
            fx.cleanRuns = fx.cleanRuns?.map { r in
                var r = r
                r.at = r.at?.addingTimeInterval(shift)
                return r
            }
        }
        // The daemon's start in the base fixture is later than the overlay
        // sets' fixed clock: keep it in the past, as a daemon up three hours.
        if let since = fx.daemon?.since, since > now { fx.daemon?.since = now.addingTimeInterval(-3 * 3600) }
        // One release throughout: the fixture's daemon is as old as the tests
        // need it, the renders show the release they document.
        if let version { fx.daemon?.daemonVersion = version }
        // F3: a set may bring audit log lines, for a lifted pin's notice.
        fx.pinLifts = data("render-audit.jsonl").map(PinLift.latest(jsonl:)) ?? [:]
        let store = Store(preview: snap, binaryPath: "~/.local/bin/claudeswitch", binaryVersion: version, fixtures: fx)
        AppHub.updates.preview(version: version ?? "0.0.0", lastCheck: now.addingTimeInterval(-3600), now: now)
        if least {
            // --min: only Settings, at the least the window and its sidebar
            // can be (760 × 520, SettingsView.sidebarLeast), to see what cuts
            // off there.
            for dark in [false, true] {
                for pane in Pane.allCases {
                    save(SettingsView(pane: pane, renderSidebar: true, sidebarWidth: SettingsView.sidebarLeast).environmentObject(store)
                        .environmentObject(LoginItem()),
                         size: CGSize(width: 760, height: 520), dark: dark,
                         to: out + "/settings-\(pane.rawValue.lowercased())-min\(dark ? "-dark" : "").png")
                }
            }
            return
        }
        poolSheets(fx: fx, snap: snap, out: out)
        let missing = Store(preview: nil, problem: .missing)
        let login = LoginItem()

        for dark in [false, true] {
            let suffix = dark ? "-dark" : ""
            save(PopoverView().environmentObject(store).environmentObject(login),
                 size: nil, dark: dark, to: out + "/popover\(suffix).png")
            for pane in Pane.allCases {
                save(SettingsView(pane: pane, renderSidebar: true).environmentObject(store).environmentObject(login),
                     size: CGSize(width: 880, height: 640), dark: dark,
                     to: out + "/settings-\(pane.rawValue.lowercased())\(suffix).png")
            }
            save(AddAccountSheet(request: AddAccountRequest(profile: profiles?.profiles.first?.name)).environmentObject(store),
                 size: nil, dark: dark, to: out + "/add-account\(suffix).png")
            save(AddAccountSheet(request: AddAccountRequest(), route: .current, name: "person4").environmentObject(store),
                 size: nil, dark: dark, to: out + "/add-account-current\(suffix).png")
            // The Organization choice (GROUND_TRUTH §48): one already here,
            // one pasted by id, one that cannot be asked for, and a sign-in
            // refused as a duplicate, opened again for another try.
            let first = profiles?.profiles.first?.name
            if let org = AddForm.organizations(store.accountList).first {
                save(AddAccountSheet(request: AddAccountRequest(profile: first), name: "work-4",
                                     orgPick: .known(org.id)).environmentObject(store),
                     size: nil, dark: dark, to: out + "/add-account-org-known\(suffix).png")
            }
            save(AddAccountSheet(request: AddAccountRequest(profile: first), name: "work-4", orgPick: .other,
                                 orgTyped: "44444444-4444-4444-4444-444444444444").environmentObject(store),
                 size: nil, dark: dark, to: out + "/add-account-org-other\(suffix).png")
            save(AddAccountSheet(request: AddAccountRequest(profile: first), name: "work-4", orgPick: .other,
                                 orgTyped: "not an id").environmentObject(store),
                 size: nil, dark: dark, to: out + "/add-account-org-invalid\(suffix).png")
            save(AddAccountSheet(request: AddAccountRequest(profile: first), name: "work-4",
                                 error: AppError(code: "already_vaulted", message: """
                                    this is the same account AND organization already vaulted as "work-1" — \
                                    person1@example.com in Example.
                                      Nothing was stored. A quota pool is one person in one organization, so to
                                      add a different pool you need a different person, a different organization,
                                      or both.
                                    """,
                                                 hint: AddForm.appHint(code: "already_vaulted", asked: nil) ?? ""))
                                    .environmentObject(store),
                 size: nil, dark: dark, to: out + "/add-account-duplicate\(suffix).png")
            extras(store: store, snap: snap, login: login, profiles: profiles, fx: fx, version: version, dark: dark,
                   out: out)
            save(MoreMenuFacsimile(appearance: store.appearance, usage: store.usageMode, open: "Appearance"),
                 size: nil, dark: dark, to: out + "/menu-more\(suffix).png")
            save(MoreMenuFacsimile(appearance: store.appearance, usage: store.usageMode, open: "Show Usage As"),
                 size: nil, dark: dark, to: out + "/menu-more-usage\(suffix).png")
        }
        for dark in [false, true] {
            save(PopoverView().environmentObject(missing).environmentObject(login), size: nil, dark: dark,
                 to: out + "/popover-missing\(dark ? "-dark" : "").png")
        }
        for (name, compact) in [("label", false), ("label-compact", true)] {
            for p in snap.cards.map(\.name) {
                for dark in [false, true] {
                    save(MenuBarLabel(title: MenuTitle.of(snap, compact: compact, profile: p),
                                      glyph: GlyphReading.of(snap, profile: p)).padding(4),
                         size: nil, dark: dark, to: out + "/\(name)-\(p)\(dark ? "-dark" : "").png")
                }
            }
        }
        glyphStates(snap: snap, out: out)
        // The bars view of the usage, for review.
        store.usageMode = .bars
        for dark in [false, true] {
            save(PopoverView().environmentObject(store).environmentObject(login),
                 size: nil, dark: dark, to: out + "/popover-bars\(dark ? "-dark" : "").png")
        }
        store.usageMode = .dials
        flows(store: store, login: login, out: out)
    }

    /// F9 and F10: the setup window at its first step and mid-way, and the
    /// popover offering an update (fixture versions; no call is made).
    static func flows(store: Store, login: LoginItem, out: String) {
        for dark in [false, true] {
            let sfx = dark ? "-dark" : ""
            save(WelcomeView(renderFacts: SetupFacts()).environmentObject(store),
                 size: nil, dark: dark, to: out + "/welcome\(sfx).png")
            var mid = SetupFlow()
            mid.complete(.addAccounts)
            save(WelcomeView(flow: mid, renderFacts: SetupFacts(accounts: 3, profiles: 0)).environmentObject(store),
                 size: nil, dark: dark, to: out + "/welcome-profile\(sfx).png")
            AppHub.updates.preview(available: "0.5.6")
            save(PopoverView().environmentObject(store).environmentObject(login),
                 size: nil, dark: dark, to: out + "/popover-update\(sfx).png")
            AppHub.updates.preview(available: "0.5.6", phase: .updating("0.5.6"))
            AppHub.updates.preview(dialog: .installing(version: "0.5.6",
                                                       step: .downloading(received: 4_400_000, total: 11_300_000)))
            save(PopoverView().environmentObject(store).environmentObject(login),
                 size: nil, dark: dark, to: out + "/popover-updating\(sfx).png")
            AppHub.updates.preview(available: nil)
            for (name, d) in updateDialogs {
                AppHub.updates.preview(dialog: d)
                save(UpdateDialogView(updates: AppHub.updates), size: nil, dark: dark,
                     to: out + "/update-\(name)\(sfx).png")
            }
            AppHub.updates.preview(dialog: nil)
        }
    }

    /// Every state of the update window (fixture versions; no call is made).
    static let updateDialogs: [(String, UpdateDialog)] = [
        ("checking", .checking),
        ("check-off", .checkOff),
        ("version-unknown", .versionUnknown(latest: "0.5.6")),
        ("up-to-date", .upToDate(current: "0.5.6", latest: "0.5.6")),
        ("check-failed", .checkFailed(UpdateError.noRelease("The Internet connection appears to be offline")
            .message)),
        ("available", .available(version: "0.5.6", current: "0.5.5",
                                 notes: "**Cleanup**\n• `cs clean --apply --plan <file>` removes only what you read\n"
                                     + "• A branch's last change is read from its reflog, which `git gc` does not move\n\n"
                                     + "**The app**\n• Check for updates opens this window, with a progress bar",
                                 bytes: 11_300_000, page: URL(string: UpdateSource.home + "/releases/tag/v0.5.6"))),
        ("available-plain", .available(version: "0.5.6", current: "0.5.5", notes: nil, bytes: 11_300_000,
                                       page: URL(string: UpdateSource.home + "/releases/tag/v0.5.6"))),
        ("downloading", .installing(version: "0.5.6", step: .downloading(received: 4_400_000, total: 11_300_000))),
        ("installing", .installing(version: "0.5.6", step: .installingApp)),
        ("failed", .failed(version: "0.5.6", message: UpdateError.checksumMismatch("ClaudeSwitch-0.5.6-macos.zip")
            .message)),
        ("installed", .installed(version: "0.5.6")),
    ]

    /// The states the base set does not reach: every card expanded, the
    /// profile editors, and the other sheets (from QA's render extras).
    static func extras(store: Store, snap: Snapshot, login: LoginItem, profiles: ProfileList?, fx: PreviewData,
                       version: String?, dark: Bool, out: String) {
        let sfx = dark ? "-dark" : ""
        // The panes whose rows run below the 640 pt window, whole.
        for pane in [Pane.rotation, .polling, .cleanup, .advanced, .daemon] {
            save(SettingsView(pane: pane, renderSidebar: true).environmentObject(store).environmentObject(login),
                 size: CGSize(width: 880, height: 1500), dark: dark,
                 to: out + "/settings-\(pane.rawValue.lowercased())-tall\(sfx).png")
        }
        // A binary older than this app speaks to runs nothing: what the app
        // has is state.json alone, and the banner takes the cards' place.
        var oldFx = PreviewData()
        oldFx.state = fx.state
        let oldSnap = Snapshot.build(state: fx.state, why: nil, settings: nil, profiles: nil, now: snap.now)
        let old = Store(preview: oldSnap, problem: .tooOld(version: "0.5.0"), binaryPath: "~/.local/bin/claudeswitch",
                        fixtures: oldFx)
        save(PopoverView().environmentObject(old).environmentObject(login), size: nil, dark: dark,
             to: out + "/popover-too-old\(sfx).png")
        // A note, as a switch leaves it (the followed card shows the account
        // it names), and `why` timing out on the next read: the app's own
        // words for both.
        let noted = Store(preview: snap, problem: .failed("`claudeswitch why --json` did not finish in time."),
                          binaryPath: "~/.local/bin/claudeswitch", binaryVersion: version, fixtures: fx)
        noted.acting(in: .popover)
        if let c = snap.card(snap.followed(nil)), let a = c.active { noted.note = "\(c.name) now uses \(a.id)" }
        save(PopoverView().environmentObject(noted).environmentObject(login), size: nil, dark: dark,
             to: out + "/popover-notes\(sfx).png")
        states(snap: snap, fx: fx, version: version, login: login, dark: dark, out: out)
        save(PopoverView(expanded: Set(snap.cards.map(\.name))).environmentObject(store).environmentObject(login),
             size: nil, dark: dark, to: out + "/popover-expanded\(sfx).png")
        // M11: a refused `use` shown inline on its card, with R1's retry time.
        if let c = snap.cards.first {
            let target = c.best?.id ?? c.accounts.first { !$0.isActive }?.id ?? "work-1"
            let at = snap.now.addingTimeInterval(4 * 60)
            let message = "can't confirm \"\(target)\" isn't signed in under profile \"\(c.name)\": the check is "
                + "rate limited until \(CardError.clock(at)); try again then"
            store.renderCardError(CardError(title: "Could not switch \(c.name) to \(target)", message: message, retryAt: at),
                                  card: c.name)
            save(PopoverView().environmentObject(store).environmentObject(login),
                 size: nil, dark: dark, to: out + "/popover-card-error\(sfx).png")
            store.renderCardError(nil, card: c.name)
        }
        // C2: a card whose Chrome profile is still signed in as the account
        // before the last rotation, expanded to show its banner.
        if let c = snap.cards.first(where: { store.chromeNotice(for: $0) != nil }) {
            save(PopoverView(expanded: [c.name]).environmentObject(store).environmentObject(login),
                 size: nil, dark: dark, to: out + "/popover-chrome-signin\(sfx).png")
        }
        save(NewProfileSheet().environmentObject(store), size: nil, dark: dark, to: out + "/sheet-new-profile\(sfx).png")
        if let p = profiles?.profiles.last(where: \.declared), profiles?.profiles.filter(\.declared).count ?? 0 > 1 {
            save(RemoveProfileSheet(profile: p).environmentObject(store), size: nil, dark: dark,
                 to: out + "/sheet-remove-profile\(sfx).png")
        }
        if let empty = profiles?.profiles.first(where: { $0.declared && $0.pool.isEmpty }) {
            save(RemoveProfileSheet(profile: empty).environmentObject(store), size: nil, dark: dark,
                 to: out + "/sheet-remove-empty-profile\(sfx).png")
        }
        if let list = profiles?.profiles {
            // Each editor at its own height: a fixed 440 pt each cut the last
            // ones off.
            save(VStack(spacing: 16) { ForEach(list) { ProfileEditor(profile: $0, done: {}) } }.padding(24)
                    .frame(width: 680)
                    .environmentObject(store),
                 size: nil, dark: dark, to: out + "/settings-profile-editors\(sfx).png")
        }
        if let a = store.accountIDs.first {
            save(RenameSheet(old: a).environmentObject(store), size: nil, dark: dark, to: out + "/sheet-rename\(sfx).png")
            save(AddAccountSheet(request: AddAccountRequest(account: a, profile: store.profile(of: a))).environmentObject(store),
                 size: nil, dark: dark, to: out + "/add-account-relogin\(sfx).png")
        }
        save(AddAccountSheet(request: AddAccountRequest()).environmentObject(store),
             size: nil, dark: dark, to: out + "/add-account-from-accounts-pane\(sfx).png")
        pools(store: store, snap: snap, profiles: profiles, dark: dark, out: out)
    }

    /// Popovers no set's data reaches (QA 2026-10-28): no daemon ever
    /// started, the first read under way, no state file yet, and the ⋯
    /// menu of a card pinned softly.
    static func states(snap: Snapshot, fx: PreviewData, version: String?, login: LoginItem, dark: Bool, out: String) {
        let sfx = dark ? "-dark" : ""
        // No daemon: the same state.json with no daemon_since.
        var never = fx
        never.state?.daemonSince = nil
        never.daemon = nil
        let neverSnap = Snapshot.build(state: never.state, why: fx.why, settings: snap.settings, profiles: fx.profiles,
                                       now: snap.now)
        let none = Store(preview: neverSnap, binaryPath: "~/.local/bin/claudeswitch", binaryVersion: version,
                         fixtures: never)
        save(PopoverView().environmentObject(none).environmentObject(login), size: nil, dark: dark,
             to: out + "/popover-no-daemon\(sfx).png")
        // Before the first read, and with no state file to read.
        let empty = Store(preview: nil, binaryPath: "~/.local/bin/claudeswitch", binaryVersion: version)
        save(PopoverView(readingOverride: true).environmentObject(empty).environmentObject(login), size: nil,
             dark: dark, to: out + "/popover-reading\(sfx).png")
        save(PopoverView().environmentObject(empty).environmentObject(login), size: nil, dark: dark,
             to: out + "/popover-no-state\(sfx).png")
        // A soft pin's ⋯, open: it offers to keep the pin even if it runs out.
        let store = Store(preview: snap, binaryPath: "~/.local/bin/claudeswitch", binaryVersion: version, fixtures: fx)
        guard store.accountList?.knowsHardPin == true else { return }
        for c in snap.cards where c.isPinned && store.pinIsHard(c) == false {
            save(PinMenuFacsimile(), size: nil, dark: dark, to: out + "/menu-pin-\(c.name)\(sfx).png")
        }
    }

    /// The pool sheets (D38, D39): Rename for a [[pool]] and for a
    /// profile's own pool, and Delete for a pool in use with accounts and for
    /// an empty one.
    static func poolSheets(fx: PreviewData, snap: Snapshot, out: String) {
        guard let pools = fx.pools else { return }
        let store = Store(preview: snap, binaryPath: "~/.local/bin/claudeswitch", fixtures: fx)
        let used = pools.pools.first { !$0.own && !$0.accounts.isEmpty && !$0.profiles.isEmpty }
        let empty = pools.pools.first { !$0.own && $0.accounts.isEmpty }
        let own = pools.pools.first { $0.own && !$0.accounts.isEmpty }
        for dark in [false, true] {
            let sfx = dark ? "-dark" : ""
            if let p = used {
                save(RenamePoolSheet(name: p.name, newName: "crew").environmentObject(store),
                     size: nil, dark: dark, to: out + "/sheet-rename-pool\(sfx).png")
                save(DeletePoolSheet(name: p.name).environmentObject(store),
                     size: nil, dark: dark, to: out + "/sheet-delete-pool\(sfx).png")
            }
            if let p = own {
                save(RenamePoolSheet(name: p.name, newName: "laptop").environmentObject(store),
                     size: nil, dark: dark, to: out + "/sheet-rename-own-pool\(sfx).png")
            }
            if let p = empty {
                save(DeletePoolSheet(name: p.name).environmentObject(store),
                     size: nil, dark: dark, to: out + "/sheet-delete-empty-pool\(sfx).png")
            }
        }
    }

    /// Shared pools: the whole Accounts pane (every section, not only what
    /// fits), New pool, Change…,
    /// New profile with pools checked, and the followed card's account menu.
    static func pools(store: Store, snap: Snapshot, profiles: ProfileList?, dark: Bool, out: String) {
        let sfx = dark ? "-dark" : ""
        let pools = store.pools
        let sections = pools.map { PoolLayout.sections($0, order: store.accountIDs, profiles: profiles) } ?? []
        let rows = CGFloat(store.accountIDs.count) * 76 + CGFloat(sections.count) * 44
        save(AccountsPane().environmentObject(store),
             size: CGSize(width: 680, height: max(640, 300 + rows)), dark: dark,
             to: out + "/settings-accounts-full\(sfx).png")
        if let l = pools {
            // New pool is offered only where the pane groups by pool.
            if PoolLayout.grouped(l, profiles: profiles) {
                let movers = store.accountIDs.filter { l.pool(of: $0) != nil }.prefix(1) + l.unlisted.prefix(1)
                save(NewPoolSheet(name: "lab", picked: Set(movers)).environmentObject(store),
                     size: nil, dark: dark, to: out + "/sheet-new-pool\(sfx).png")
            }
            let names = l.pools.map(\.name)
            save(NewProfileSheet(name: "lab", choice: PoolChoice(all: names, chosen: Array(names.reversed().prefix(2))),
                                 own: Set(l.unlisted.suffix(1)))
                    .environmentObject(store),
                 size: nil, dark: dark, to: out + "/sheet-new-profile-pools\(sfx).png")
        }
        // Change… is offered only with a binary that reports pools.
        for p in (profiles?.profiles ?? []).filter(\.declared) where ProfilePools.shown(p, pools: pools) != nil {
            save(ChangePoolsSheet(profile: p).environmentObject(store),
                 size: nil, dark: dark, to: out + "/sheet-change-pools-\(p.name)\(sfx).png")
        }
        for c in snap.cards {
            save(AccountMenuFacsimile(card: c).environmentObject(store),
                 size: nil, dark: dark, to: out + "/menu-accounts-\(c.name)\(sfx).png")
        }
    }

    /// The menu-bar glyph in each of its four states (the mockup's STATES)
    /// and the fixture's own, on a light and a dark menu bar, in one sheet.
    static func glyphStates(snap: Snapshot, out: String) {
        func reading(_ state: GlyphState, _ w: Double, _ s: Double, _ sat: Double?) -> GlyphReading {
            GlyphReading(state: state, week: w, session: s, satellite: sat, accessibility: "")
        }
        let live = MenuTitle.of(snap, compact: false, profile: nil).text
        // Switching: work-1 is over its session trigger (81%) and the
        // satellite has moved to the incoming account's week (12%).
        let rows: [(String, GlyphReading, String)] = [
            ("Healthy", reading(.healthy, 0.41, 0.18, 0.41), "default · work-1 41%"),
            ("Climbing", reading(.climbing, 0.52, 0.81, 0.52), "work · work-team 81%"),
            ("Switching", reading(.switching, 0.52, 0.81, 0.12), "work · work-1 81%"),
            ("Needs you", reading(.needsYou, 0.71, 1, nil), "default · research 100%!"),
            ("This fixture", GlyphReading.of(snap, profile: nil), live),
        ]
        // A grid, so each column's chips share one width and their glyphs
        // and labels line up, in the dark column as in the light.
        let sheet = Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 10) {
            ForEach(rows, id: \.0) { r in
                GridRow {
                    Text(r.0)
                    ForEach([false, true], id: \.self) { dark in
                        HStack(spacing: 4) {
                            Image(nsImage: MenuGlyph.image(r.1, dark: dark))
                            Text(r.2).monospacedDigit().lineLimit(1).fixedSize()
                        }
                        .font(.system(size: 13))
                        .padding(.horizontal, 10)
                        .frame(maxWidth: .infinity, minHeight: 24, maxHeight: 24, alignment: .leading)
                        .foregroundStyle(dark ? Color(white: 0.95) : Color(white: 0.07))
                        .background(RoundedRectangle(cornerRadius: 6).fill(dark ? Color(white: 0.17) : Color(white: 0.92)))
                    }
                }
            }
        }
        .fixedSize()
        .padding(16)
        save(sheet, size: nil, dark: false, to: out + "/glyph-states.png")
    }

    /// Draws a view through a real (off-screen) window, so AppKit-backed
    /// controls — lists, text fields, menus — render as they do on screen.
    static func save<V: View>(_ v: V, size: CGSize?, dark: Bool, to path: String) {
        // Drawn as in a window in use: the off-screen window is never the
        // active one, and switches that are on, red buttons and tinted
        // bars drew grey.
        let fit = size ?? {
            let f = NSHostingView(rootView: v.environment(\.controlActiveState, .key)).fittingSize
            return CGSize(width: f.width.rounded(.up), height: f.height.rounded(.up))
        }()
        // Framed to the whole-point size before the background, so the
        // background fills every pixel: a fractional fitting size left a
        // transparent row or column at the edge.
        let host = NSHostingView(rootView: v.frame(width: fit.width, height: fit.height)
            .background(Color(nsColor: .windowBackgroundColor))
            .environment(\.controlActiveState, .key))
        let window = RenderWindow(contentRect: NSRect(x: -20_000, y: -20_000, width: fit.width, height: fit.height),
                                  styleMask: [.borderless], backing: .buffered, defer: false)
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.contentView = host
        host.frame = NSRect(origin: .zero, size: fit)
        window.orderFront(nil)
        RunLoop.current.run(until: Date().addingTimeInterval(0.6))
        host.layoutSubtreeIfNeeded()
        defer { window.orderOut(nil) }
        guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else {
            FileHandle.standardError.write(Data("could not render \(path)\n".utf8))
            return
        }
        host.cacheDisplay(in: host.bounds, to: rep)
        guard let png = rep.representation(using: .png, properties: [:]) else { return }
        try? png.write(to: URL(fileURLWithPath: path))
    }
}

/// The off-screen window renders draw in. It says it is the key window, so
/// AppKit-backed controls (the Settings sidebar's list) draw as they do in a
/// window in use, not dimmed as in a background one.
final class RenderWindow: NSWindow {
    override var isKeyWindow: Bool { true }
    override var isMainWindow: Bool { true }
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

/// A card's account picker, drawn for review: a menu cannot be drawn off
/// screen, so this lays out the same lines (AccountMenuItems), as the menu
/// has them: no header, the live account enabled and checked, an account
/// live in another profile dimmed as the menu disables it. Render only.
struct AccountMenuFacsimile: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(card.accounts) { a in
                let elsewhere = store.snapshot?.liveElsewhere(a.id, notIn: card.name)
                // One line, as a menu item is: allowed to wrap inside its
                // fixed height, a long line (a trigger time) left AppKit's
                // constraint passes never settling, and the render crashed.
                HStack(spacing: 0) {
                    Image(systemName: "checkmark").font(.system(size: 11, weight: .semibold))
                        .opacity(a.isActive ? 1 : 0).frame(width: 18)
                    Text(Format.pickerLine(a, inUseBy: elsewhere, now: store.now)).lineLimit(1).fixedSize()
                }
                .font(.system(size: 13))
                .foregroundStyle(elsewhere != nil ? Color.secondary : Color.primary)
                .padding(.horizontal, 6).frame(height: 22, alignment: .leading)
            }
            if card.accounts.isEmpty {
                Text("No accounts").font(.system(size: 13)).foregroundStyle(.secondary).padding(.horizontal, 6)
            }
        }
        .padding(5)
        .frame(minWidth: 260, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.15)))
        .padding(18)
        .accessibilityHidden(true)
    }
}

/// Pin's ⋯ menu open on a card pinned softly, drawn for review: a menu
/// cannot be drawn off screen. Its one item is HardPinMenu's for a pin
/// already made. Render only.
struct PinMenuFacsimile: View {
    var body: some View {
        HStack(spacing: 0) {
            Text("Keep the Pin Even If It Runs Out…").lineLimit(1).fixedSize()
        }
        .font(.system(size: 13))
        .padding(.horizontal, 6 + 18).frame(height: 22, alignment: .leading)
        .padding(5)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.15)))
        .padding(18)
        .accessibilityHidden(true)
    }
}

/// The menu-bar icon's menu (right-click or Control-click; it was the
/// popover's ⋯ menu), drawn for the docs: an NSMenu cannot be drawn off
/// screen, so this lays out StatusMenu.layout's lines with one submenu
/// open ("Appearance" or "Show usage as"). Render only; the app never
/// shows it.
struct MoreMenuFacsimile: View {
    let appearance: AppearancePref
    let usage: UsageMode
    let open: String

    enum Row { case item(String, String?, Bool), divider }

    var main: [Row] {
        StatusMenu.layout.map { line in
            line.map { .item($0.title, $0.key.isEmpty ? nil : "⌘" + $0.key.uppercased(), $0.submenu) } ?? .divider
        }
    }

    var body: some View {
        HStack(alignment: .top, spacing: -4) {
            panel(main, highlight: open, checked: nil, width: 210)
            if open == "Appearance" {
                panel(AppearancePref.allCases.map { .item($0.label, nil, false) }, highlight: nil,
                      checked: appearance.label, width: 120)
                    .padding(.top, 22)
            } else {
                panel(UsageMode.allCases.map { .item($0.label, nil, false) }, highlight: nil,
                      checked: usage.label, width: 120)
                    .padding(.top, 44)
            }
        }
        .padding(18)
        .accessibilityHidden(true)
    }

    func panel(_ rows: [Row], highlight: String?, checked: String?, width: CGFloat) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(rows.enumerated()), id: \.offset) { _, r in
                switch r {
                case .divider:
                    Divider().padding(.horizontal, 10).padding(.vertical, 5)
                case let .item(title, key, sub):
                    let on = title == highlight
                    HStack(spacing: 0) {
                        Image(systemName: "checkmark").font(.system(size: 11, weight: .semibold))
                            .opacity(title == checked ? 1 : 0).frame(width: 18)
                        Text(title)
                        Spacer(minLength: 16)
                        if let key { Text(key).foregroundStyle(on ? Color.white : Color.secondary) }
                        if sub { Image(systemName: "chevron.right").font(.system(size: 11, weight: .semibold)) }
                    }
                    .font(.system(size: 13))
                    .foregroundStyle(on ? Color.white : Color.primary)
                    .padding(.horizontal, 6).frame(height: 22)
                    .background(RoundedRectangle(cornerRadius: 4).fill(on ? Color.accentColor : Color.clear))
                }
            }
        }
        .padding(5)
        .frame(width: width)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.15)))
        .shadow(color: .black.opacity(0.18), radius: 8, y: 3)
    }
}

/// The menu-bar item: the Twin rings drawn live from the followed profile's
/// session (outer) and week (inner), and its live account with its
/// utilization. The glyph is in colour, each ring green to red by its use,
/// drawn for the menu bar's light or dark appearance; trouble also carries
/// its warning mark and a trailing "!", which read without colour. A
/// missing or too-old binary shows a question mark.
/// It is redrawn whenever the store changes and never animated, so Reduce
/// Motion has nothing to stop.
struct MenuBarLabel: View {
    let title: MenuTitle
    let glyph: GlyphReading

    /// Never empty: the rings are always there (empty rings before data).
    var body: some View {
        let text = MenuBarIcon.text(title, symbol: "rings")
        HStack(spacing: 3) {
            if title.problem {
                Image(systemName: "questionmark.circle")
            } else {
                Image(nsImage: MenuGlyph.image(glyph))
            }
            // Its own width: given a point less it cut its tail, the "%"
            // and the "!" that says something needs the person.
            if !text.isEmpty { Text(text).monospacedDigit().lineLimit(1).fixedSize() }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(label)
        .onAppear {
            menuBarLog.notice("status item label drawn: \(String(describing: glyph.state), privacy: .public) \(text, privacy: .public)")
        }
    }

    var label: String {
        if title.problem { return title.text.isEmpty ? "ClaudeSwitch" : "ClaudeSwitch, " + title.text }
        return glyph.accessibility
    }
}
