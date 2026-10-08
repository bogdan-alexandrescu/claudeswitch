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
                                             problem: store.problem),
                         glyph: GlyphReading.of(store.problem == .missing ? nil : store.snapshot,
                                                profile: store.menuProfile))
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
    }

    /// The states the base set does not reach: every card expanded, the
    /// profile editors, and the other sheets (from QA's render extras).
    static func extras(store: Store, snap: Snapshot, login: LoginItem, profiles: ProfileList?, dark: Bool, out: String) {
        let sfx = dark ? "-dark" : ""
        save(PopoverView(expanded: Set(snap.cards.map(\.name))).environmentObject(store).environmentObject(login),
             size: nil, dark: dark, to: out + "/popover-expanded\(sfx).png")
        // M11: a refused `use` shown inline on its card, with R1's retry time.
        if let c = snap.cards.first {
            let target = c.best?.id ?? c.accounts.first?.id ?? "work-1"
            let at = snap.now.addingTimeInterval(4 * 60)
            let message = "can't confirm \"\(target)\" isn't signed in under profile \"\(c.name)\": the check is "
                + "rate limited until \(CardError.clock(at)); try again then"
            store.renderCardError(CardError(title: "Could not switch \(c.name) to \(target)", message: message, retryAt: at),
                                  card: c.name)
            save(PopoverView().environmentObject(store).environmentObject(login),
                 size: nil, dark: dark, to: out + "/popover-card-error\(sfx).png")
            store.renderCardError(nil, card: c.name)
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

    /// The menu-bar glyph in each of its four states (the mockup's STATES)
    /// and the fixture's own, on a light and a dark menu bar, in one sheet.
    static func glyphStates(snap: Snapshot, out: String) {
        func reading(_ state: GlyphState, _ w: Double, _ s: Double, _ sat: Double?) -> GlyphReading {
            GlyphReading(state: state, week: w, session: s, satellite: sat, accessibility: "")
        }
        let live = MenuTitle.of(snap, compact: false, profile: nil).text
        let rows: [(String, GlyphReading, String)] = [
            ("Healthy", reading(.healthy, 0.41, 0.18, 0.41), "default · work-1 41%"),
            ("Climbing", reading(.climbing, 0.52, 0.81, 0.52), "work · work-team 81%"),
            ("Switching", reading(.switching, 0.12, 0.06, 0.62), "work · work-team 12%"),
            ("Needs you", reading(.needsYou, 0.71, 1, nil), "default · research 100%!"),
            ("This fixture", GlyphReading.of(snap, profile: nil), live),
        ]
        let sheet = VStack(alignment: .leading, spacing: 10) {
            ForEach(rows, id: \.0) { r in
                HStack(spacing: 14) {
                    Text(r.0).frame(width: 90, alignment: .leading)
                    ForEach([false, true], id: \.self) { dark in
                        HStack(spacing: 4) {
                            Image(nsImage: MenuGlyph.image(r.1)).renderingMode(.template)
                            Text(r.2).monospacedDigit()
                        }
                        .font(.system(size: 13))
                        .padding(.horizontal, 10).frame(height: 24)
                        .foregroundStyle(dark ? Color(white: 0.95) : Color(white: 0.07))
                        .background(RoundedRectangle(cornerRadius: 6).fill(dark ? Color(white: 0.17) : Color(white: 0.92)))
                    }
                }
            }
        }
        .padding(16)
        save(sheet, size: nil, dark: false, to: out + "/glyph-states.png")
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

/// The menu-bar item: the Twin rings drawn live from the followed profile's
/// week (outer) and session (inner), and its live account with its
/// utilization. The glyph is a template image, so macOS tints it for every
/// menu bar; trouble is carried by its warning mark and a trailing "!"
/// rather than by colour. A missing or too-old binary shows a question mark.
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
            if !text.isEmpty { Text(text).monospacedDigit() }
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
