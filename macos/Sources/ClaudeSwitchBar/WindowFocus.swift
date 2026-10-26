import AppKit
import SwiftUI

/// A window opened from the menu-bar popover comes up in front and focused,
/// and the popover closes (owner request 2026-10-25): it had stayed open and
/// kept the focus, leaving the new window behind it or behind other apps.
@MainActor
enum WindowFocus {
    /// The popover's own window: SwiftUI's MenuBarExtra panel.
    static var popover: NSWindow? {
        NSApp.windows.first { String(describing: type(of: $0)).contains("MenuBarExtraWindow") }
    }

    static func closePopover() {
        if let p = popover, p.isVisible { p.close() }
    }

    /// Closes the popover, then makes this app frontmost and the window that
    /// `find` returns key. A SwiftUI window appears a pass after openWindow,
    /// so it is looked for a few times before giving up.
    static func bringForward(_ find: @escaping () -> NSWindow?) {
        closePopover()
        NSApp.activate(ignoringOtherApps: true)
        func attempt(_ left: Int) {
            if let w = find() {
                if w.isMiniaturized { w.deminiaturize(nil) }
                w.makeKeyAndOrderFront(nil)
                NSApp.activate(ignoringOtherApps: true)
            } else if left > 0 {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) { attempt(left - 1) }
            }
        }
        DispatchQueue.main.async { attempt(10) }
    }

    /// The Settings window, once SwiftUI has made it.
    static var settings: NSWindow? {
        NSApp.windows.first {
            $0.identifier?.rawValue.hasPrefix(SettingsView.windowID) == true || $0.title == SettingsView.windowTitle
        }
    }

    /// Settings, in front and focused, from the popover or anywhere else.
    static func openSettings(_ openWindow: OpenWindowAction) {
        // Opened while the popover's view still runs the action; the
        // popover closes next.
        openWindow(id: SettingsView.windowID)
        bringForward { settings }
    }

    /// The standard About panel, in front of the popover's place.
    static func about() {
        closePopover()
        NSApp.activate(ignoringOtherApps: true)
        NSApp.orderFrontStandardAboutPanel(nil)
        bringForward { NSApp.windows.first { $0.isVisible && $0.className.contains("About") } }
    }
}
