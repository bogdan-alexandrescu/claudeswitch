import AppKit
import ClaudeSwitchCore
import SwiftUI

/// A window opened from the menu-bar popover comes up in front and focused,
/// and the popover closes (owner request 2026-10-25): it had stayed open and
/// kept the focus, leaving the new window behind it or behind other apps.
/// A window that was not on screen opens in the middle of the screen the
/// pointer is on, placed there before it shows (moving it after made it
/// jump from where it was last closed), and the update window stays centred
/// as it grows and shrinks between states (owner requests 2026-10-26).
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
    /// `find` returns key. It never moves the window: one shown already
    /// would jump. A SwiftUI window appears a pass after openWindow, so it
    /// is looked for a few times before giving up.
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

    /// The screen the pointer is on: the one whose menu bar was clicked.
    static var pointerScreen: NSScreen? {
        let p = NSEvent.mouseLocation
        return NSScreen.screens.first { NSMouseInRect(p, $0.frame, false) } ?? NSScreen.main
    }

    /// Puts `w` in the middle of the screen's visible area (below the menu
    /// bar, beside the Dock), its size laid out first. NSWindow.center()
    /// leaves it a third of the way down instead.
    static func centre(_ w: NSWindow, on screen: NSScreen? = nil) {
        w.contentView?.layoutSubtreeIfNeeded()
        guard let area = (screen ?? w.screen ?? NSScreen.main)?.visibleFrame else { return }
        w.setFrameOrigin(ScreenCentre.origin(w.frame.size, in: area))
    }

    /// Keeps `w`'s middle where it is when its size changes (a window sized
    /// by its content), until the person drags it somewhere else.
    static func keepCentred(_ w: NSWindow) -> [NSObjectProtocol] {
        var middle = NSPoint(x: w.frame.midX, y: w.frame.midY)
        var resizing = false
        let nc = NotificationCenter.default
        let moved = nc.addObserver(forName: NSWindow.didMoveNotification, object: w, queue: .main) { _ in
            MainActor.assumeIsolated {
                if !resizing { middle = NSPoint(x: w.frame.midX, y: w.frame.midY) }
            }
        }
        let resized = nc.addObserver(forName: NSWindow.didResizeNotification, object: w, queue: .main) { _ in
            MainActor.assumeIsolated {
                resizing = true
                w.setFrameOrigin(ScreenCentre.origin(w.frame.size, around: middle))
                resizing = false
            }
        }
        return [moved, resized]
    }

    /// The Settings window, once SwiftUI has made it.
    static var settings: NSWindow? {
        NSApp.windows.first {
            $0.identifier?.rawValue.hasPrefix(SettingsView.windowID) == true || $0.title == SettingsView.windowTitle
        }
    }

    /// Settings, in front and focused, from the popover or anywhere else.
    static func openSettings(_ openWindow: OpenWindowAction) {
        if settings?.isVisible != true {
            let screen = pointerScreen
            if let kept = settings {
                centre(kept, on: screen) // a closed window SwiftUI kept: moved while hidden
            } else {
                presetFrame(SettingsView.windowID, contentSize: NSSize(width: 880, height: 620), on: screen)
            }
        }
        // Opened while the popover's view still runs the action; the
        // popover closes next.
        openWindow(id: SettingsView.windowID)
        bringForward { settings }
    }

    /// Writes the frame AppKit restores a window from (its autosave name,
    /// "NSWindow Frame <name>" in the app's defaults), centred and at the
    /// size the person left it, so a window SwiftUI is about to make opens
    /// in the middle at once.
    static func presetFrame(_ name: String, contentSize: NSSize, on screen: NSScreen?) {
        guard let area = (screen ?? NSScreen.main)?.visibleFrame else { return }
        let tmp = NSWindow(contentRect: NSRect(origin: .zero, size: contentSize),
                           styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: true)
        _ = tmp.setFrameUsingName(name)
        let size = tmp.frame.size
        tmp.setFrame(NSRect(origin: ScreenCentre.origin(size, in: area), size: size), display: false)
        tmp.saveFrame(usingName: name)
    }

    /// The standard About panel, in the middle, in front of the popover's
    /// place. AppKit shows it in the same pass, so it is centred in that
    /// pass, before anything is drawn.
    static func about() {
        closePopover()
        NSApp.activate(ignoringOtherApps: true)
        let screen = pointerScreen
        let before = Set(NSApp.windows.map(ObjectIdentifier.init))
        if let kept = aboutPanel, !kept.isVisible { centre(kept, on: screen) }
        NSApp.orderFrontStandardAboutPanel(nil)
        if let panel = aboutPanel, !before.contains(ObjectIdentifier(panel)) { centre(panel, on: screen) }
        bringForward { aboutPanel }
    }

    static var aboutPanel: NSWindow? { NSApp.windows.first { $0.className.contains("About") } }
}
