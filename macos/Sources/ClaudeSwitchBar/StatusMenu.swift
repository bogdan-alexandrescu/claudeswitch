import AppKit
import ClaudeSwitchCore
import SwiftUI

/// The menu-bar icon's own menu (owner request 2026-10-26): a right-click,
/// or a Control-click, opens what the popover's ⋯ button used to hold, and
/// that button is gone. A plain click still opens the popover. SwiftUI's
/// MenuBarExtra has no menu of its own, so a local event monitor catches the
/// click on the status item's window before SwiftUI opens the popover.
@MainActor
final class StatusMenu: NSObject {
    static let shared = StatusMenu()
    private var monitor: Any?

    func install() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.rightMouseDown, .leftMouseDown]) { event in
            guard Self.isStatusItem(event.window),
                  event.type == .rightMouseDown || event.modifierFlags.contains(.control) else { return event }
            MainActor.assumeIsolated { StatusMenu.shared.show(from: event.window) }
            return nil
        }
    }

    static func isStatusItem(_ w: NSWindow?) -> Bool {
        guard let w else { return false }
        return String(describing: type(of: w)).contains("StatusBar")
    }

    /// Drops the menu down from the icon, the way a menu-bar menu opens.
    func show(from window: NSWindow?) {
        guard let view = window?.contentView else { return }
        WindowFocus.closePopover()
        let below = NSPoint(x: 0, y: view.isFlipped ? view.bounds.height + 4 : -4)
        menu().popUp(positioning: nil, at: below, in: view)
    }

    /// The menu's lines in order, nil a separator: what menu() builds and
    /// the docs' facsimile draws (MoreMenuFacsimile), so they cannot drift.
    enum Line {
        case refresh, appearance, usage, settings, setup, updates, about, quit

        var title: String {
            switch self {
            case .refresh: "Refresh now"
            case .appearance: "Appearance"
            case .usage: "Show usage as"
            case .settings: "Settings"
            case .setup: "Set up…"
            case .updates: "Check for updates…"
            case .about: "About ClaudeSwitch"
            case .quit: "Quit ClaudeSwitch"
            }
        }

        /// The key with ⌘, or "".
        var key: String {
            switch self {
            case .refresh: "r"
            case .settings: ","
            case .quit: "q"
            default: ""
            }
        }

        var submenu: Bool { self == .appearance || self == .usage }
    }

    static let layout: [Line?] = [.refresh, .appearance, .usage, nil, .settings, .setup, .updates, .about, nil, .quit]

    func menu() -> NSMenu {
        let store = AppHub.store
        let m = NSMenu()
        m.autoenablesItems = false
        for line in Self.layout {
            guard let line else { m.addItem(.separator()); continue }
            switch line {
            case .refresh:
                m.addItem(item(line.title, key: line.key) { store.refresh(full: true, profiles: true) })
            case .appearance:
                m.addItem(choices(line.title, AppearancePref.allCases.map { ($0.label, $0 == store.appearance) }) { i in
                    store.appearance = AppearancePref.allCases[i]
                })
            case .usage:
                m.addItem(choices(line.title, UsageMode.allCases.map { ($0.label, $0 == store.usageMode) }) { i in
                    store.usageMode = UsageMode.allCases[i]
                })
            case .settings:
                // Brings an open Settings forward where it is; opens on
                // Overview only when it was closed.
                m.addItem(item(line.title, key: line.key) {
                    store.openSettings(WindowFocus.settings?.isVisible == true ? nil : .overview)
                })
            case .setup:
                m.addItem(item(line.title, key: line.key) { WelcomeWindow.show(store: store) })
            case .updates:
                let updates = item(line.title, key: line.key) { AppHub.updates.checkNow() }
                updates.isEnabled = AppHub.updates.enabled
                m.addItem(updates)
            case .about:
                m.addItem(item(line.title, key: line.key) { WindowFocus.about() })
            case .quit:
                m.addItem(item(line.title, key: line.key) { AppQuit.quit() })
            }
        }
        // The app's Appearance, not the menu bar's: popped up from the
        // status item, the menu would otherwise take the menu bar's look.
        // NSApp.effectiveAppearance is the Mac's own while it follows the
        // system.
        let system = NSApp.effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) ?? .aqua
        let look = NSAppearance(named: store.appearance.resolved(system: system))
        m.appearance = look
        for i in m.items { i.submenu?.appearance = look }
        return m
    }

    /// A submenu of exclusive choices, the current one ticked.
    func choices(_ title: String, _ options: [(String, Bool)], pick: @escaping (Int) -> Void) -> NSMenuItem {
        let parent = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        let sub = NSMenu(title: title)
        sub.autoenablesItems = false
        for (i, (label, on)) in options.enumerated() {
            let it = item(label) { pick(i) }
            it.state = on ? .on : .off
            sub.addItem(it)
        }
        parent.submenu = sub
        return parent
    }

    func item(_ title: String, key: String = "", _ action: @escaping () -> Void) -> NSMenuItem {
        let it = ActionItem(title: title, action: #selector(ActionItem.run), keyEquivalent: key)
        it.target = it
        it.handler = action
        return it
    }
}

/// A menu item that runs a closure.
final class ActionItem: NSMenuItem {
    var handler: () -> Void = {}
    @objc func run() { handler() }
}
