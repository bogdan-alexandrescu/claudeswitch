import AppKit
import ClaudeSwitchCore
import SwiftUI

@main
struct ClaudeSwitchBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var store = Store()
    @StateObject private var login = LoginItem()

    var body: some Scene {
        MenuBarExtra {
            PopoverView()
                .environmentObject(store)
                .environmentObject(login)
        } label: {
            MenuBarLabel(title: MenuTitle.of(store.problem == .missing ? nil : store.snapshot,
                                             compact: store.compact))
        }
        .menuBarExtraStyle(.window)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        // LSUIElement does this for the bundle; this covers `swift run`.
        NSApp.setActivationPolicy(.accessory)
        let args = CommandLine.arguments
        if let i = args.firstIndex(of: "--render"), args.count > i + 2 {
            Task { @MainActor in
                Render.run(fixtures: args[i + 1], out: args[i + 2])
                exit(0)
            }
        }
    }
}

/// `ClaudeSwitch --render <fixtures-dir> <out-dir>` draws the menu-bar label
/// and the popover from state.json / why.json / config.json in a directory
/// (the test fixtures, say) to PNGs, so the layout can be checked without a
/// screen recording permission. It runs no command and reads nothing else.
@MainActor
enum Render {
    static func run(fixtures: String, out: String) {
        func data(_ n: String) -> Data? { try? Data(contentsOf: URL(fileURLWithPath: fixtures + "/" + n)) }
        let state = data("state.json").flatMap(StateFile.init(data:))
        let now = state?.accounts.values.compactMap(\.lastAt).max()?.addingTimeInterval(60) ?? Date()
        let snap = Snapshot.build(state: state, why: data("why.json").flatMap(WhyReport.init(data:)),
                                  settings: data("config.json").flatMap(ConfigSettings.init(data:)),
                                  now: now)
        let cases: [(String, Store)] = [
            ("popover", Store(preview: snap, binaryPath: "~/.local/bin/claudeswitch")),
            ("popover-missing", Store(preview: nil, problem: .missing)),
        ]
        for (name, store) in cases {
            let view = PopoverView()
                .environmentObject(store)
                .environmentObject(LoginItem())
                .background(Color(nsColor: .windowBackgroundColor))
            save(view, to: out + "/\(name).png")
        }
        for (name, s, compact) in [("label", snap, false), ("label-compact", snap, true)] {
            let v = MenuBarLabel(title: MenuTitle.of(s, compact: compact))
                .padding(4).background(Color(nsColor: .windowBackgroundColor))
            save(v, to: out + "/\(name).png")
        }
    }

    static func save<V: View>(_ v: V, to path: String) {
        let r = ImageRenderer(content: v)
        r.scale = 2
        guard let img = r.nsImage, let tiff = img.tiffRepresentation,
              let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) else {
            FileHandle.standardError.write(Data("could not render \(path)\n".utf8))
            return
        }
        try? png.write(to: URL(fileURLWithPath: path))
    }
}

/// The menu-bar item: a gauge whose needle tracks the binding window, and the
/// active account with its utilization. The menu bar draws template images
/// in one colour, so nearness to the threshold is carried by the symbol and a
/// trailing "!" rather than by colour alone.
struct MenuBarLabel: View {
    let title: MenuTitle

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: symbol)
            if !title.text.isEmpty { Text(title.text).monospacedDigit() }
        }
    }

    var symbol: String {
        if title.problem { return "questionmark.circle" }
        switch title.level {
        case .ok: return "gauge.with.dots.needle.33percent"
        case .near: return "gauge.with.dots.needle.67percent"
        case .over: return "exclamationmark.triangle.fill"
        }
    }
}
