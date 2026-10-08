import AppKit
import OSLog
import ClaudeSwitchCore
import SwiftUI

@main
struct ClaudeSwitchBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var store = Store()
    @StateObject private var login = LoginItem()

    init() {
        // Before the status item exists, so AppKit places it by this on a
        // first launch: near the right edge, clear of a notch. Never over
        // a stored position (a ⌘-drag).
        if !CommandLine.arguments.contains("--render") {
            StatusItemPlacement.seed(.standard)
        }
    }

    var body: some Scene {
        MenuBarExtra {
            PopoverView()
                .environmentObject(store)
                .environmentObject(login)
        } label: {
            MenuBarLabel(title: MenuTitle.of(store.problem == .missing ? nil : store.snapshot,
                                             compact: store.compact, profile: store.menuProfile,
                                             problem: store.problem))
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
    func applicationDidFinishLaunching(_ notification: Notification) {
        // LSUIElement does this for the bundle; this covers `swift run`.
        NSApp.setActivationPolicy(.accessory)
        let args = CommandLine.arguments
        if let i = args.firstIndex(of: "--render"), args.count > i + 2 {
            // --now 2026-10-08T02:45:41Z: the clock the fixtures were written at.
            let now = args.firstIndex(of: "--now").flatMap { j in
                args.count > j + 1 ? ISO8601DateFormatter().date(from: args[j + 1]) : nil
            }
            Task { @MainActor in
                Render.run(fixtures: args[i + 1], out: args[i + 2], now: now)
                exit(0)
            }
            return
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

/// `ClaudeSwitch --render <fixtures-dir> <out-dir> [--now <RFC 3339>]` draws
/// the menu-bar label, the popover (collapsed and expanded), every Settings
/// pane, the profile editors and every sheet, in light and dark, from the
/// JSON in a directory (the test fixtures, or macos/RenderFixtures) to PNGs,
/// so the layout can be checked without a screen recording permission. It
/// runs no command and reads nothing else. `--now` fixes the clock; without
/// it, a minute after the newest reading.
@MainActor
enum Render {
    static func run(fixtures: String, out: String, now fixed: Date? = nil) {
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
        fx.chrome = data("cli-chrome-list-some.json").flatMap(ChromeList.init(data:))
        fx.accountList = data("cli-account-list.json").flatMap(AccountList.init(data:))
        fx.schema = data("cli-config-schema.json").flatMap(ConfigSchema.init(data:))
        fx.values = data("config.json").flatMap(ConfigValues.init(data:))
        fx.daemon = data("cli-daemon-status.json").flatMap(DaemonStatus.init(data:))
        fx.recovery = data("cli-recovery.json").flatMap(RecoveryItem.list(data:))
        let store = Store(preview: snap, binaryPath: "~/.local/bin/claudeswitch", fixtures: fx)
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
            extras(store: store, snap: snap, login: login, profiles: profiles, dark: dark, out: out)
        }
        save(PopoverView().environmentObject(missing).environmentObject(login), size: nil, dark: false,
             to: out + "/popover-missing.png")
        for (name, compact) in [("label", false), ("label-compact", true)] {
            for p in snap.cards.map(\.name) {
                save(MenuBarLabel(title: MenuTitle.of(snap, compact: compact, profile: p)).padding(4),
                     size: nil, dark: false, to: out + "/\(name)-\(p).png")
            }
        }
    }

    /// The states the base set does not reach: every card expanded, the
    /// profile editors, and the other sheets (from QA's render extras).
    static func extras(store: Store, snap: Snapshot, login: LoginItem, profiles: ProfileList?, dark: Bool, out: String) {
        let sfx = dark ? "-dark" : ""
        save(PopoverView(expanded: Set(snap.cards.map(\.name))).environmentObject(store).environmentObject(login),
             size: nil, dark: dark, to: out + "/popover-expanded\(sfx).png")
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
            save(ScrollView { VStack(spacing: 16) { ForEach(list) { ProfileEditor(profile: $0, done: {}) } }.padding(24) }
                    .frame(width: 680, height: max(640, CGFloat(list.count) * 440))
                    .environmentObject(store),
                 size: nil, dark: dark, to: out + "/settings-profile-editors\(sfx).png")
        }
        if let a = store.accountIDs.first {
            save(RenameSheet(old: a).environmentObject(store), size: nil, dark: dark, to: out + "/sheet-rename\(sfx).png")
            save(AddAccountSheet(request: AddAccountRequest(account: a, profile: store.pool(of: a))).environmentObject(store),
                 size: nil, dark: dark, to: out + "/add-account-relogin\(sfx).png")
        }
        save(AddAccountSheet(request: AddAccountRequest()).environmentObject(store),
             size: nil, dark: dark, to: out + "/add-account-from-accounts-pane\(sfx).png")
    }

    /// Draws a view through a real (off-screen) window, so AppKit-backed
    /// controls — lists, text fields, menus — render as they do on screen.
    static func save<V: View>(_ v: V, size: CGSize?, dark: Bool, to path: String) {
        let host = NSHostingView(rootView: v.background(Color(nsColor: .windowBackgroundColor)))
        let fit = size ?? host.fittingSize
        let window = NSWindow(contentRect: NSRect(x: -20_000, y: -20_000, width: fit.width, height: fit.height),
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

/// The menu-bar item: a gauge whose needle tracks the binding window, and the
/// followed profile's live account with its utilization. The menu bar draws
/// template images in one colour, so nearness to the threshold is carried by
/// the symbol and a trailing "!" rather than by colour alone.
struct MenuBarLabel: View {
    let title: MenuTitle

    /// Never empty: a symbol this system lacks falls back to another, and
    /// to the text "cs" when none is there, so the item never has zero width.
    var body: some View {
        let symbol = MenuBarIcon.symbol(title) { NSImage(systemSymbolName: $0, accessibilityDescription: nil) != nil }
        let text = MenuBarIcon.text(title, symbol: symbol)
        HStack(spacing: 3) {
            if let s = symbol { Image(systemName: s) }
            if !text.isEmpty { Text(text).monospacedDigit() }
        }
        .accessibilityLabel(title.text.isEmpty ? "ClaudeSwitch" : "ClaudeSwitch, " + title.text)
        .onAppear { menuBarLog.notice("status item label drawn: \(symbol ?? "no symbol", privacy: .public) \(text, privacy: .public)") }
    }
}
