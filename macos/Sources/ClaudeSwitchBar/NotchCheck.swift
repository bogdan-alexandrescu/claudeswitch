import AppKit
import ClaudeSwitchCore
import os

/// After launch: is the status item under the notch, where it is drawn but
/// cannot be seen? If so, log it, and once (per install) explain and offer
/// to move it near the right edge.
@MainActor
enum NotchCheck {
    static let log = Logger(subsystem: "xyz.claudeswitch.menubar", category: "placement")
    /// Set once the person has been told, so they are told once.
    static let warnedKey = "notchWarningShown"

    static func run(defaults: UserDefaults = .standard) {
        // The status item lives in a window of its own class.
        guard let item = NSApp.windows.first(where: { String(describing: type(of: $0)).contains("NSStatusBarWindow") }),
              let screen = item.screen ?? NSScreen.main else { return }
        guard StatusItemPlacement.hiddenByNotch(item: item.frame, safeAreaTop: screen.safeAreaInsets.top,
                                                left: screen.auxiliaryTopLeftArea,
                                                right: screen.auxiliaryTopRightArea) else { return }
        log.error("the menu-bar item is under the notch, so it cannot be seen: frame \(NSStringFromRect(item.frame), privacy: .public)")
        guard !defaults.bool(forKey: warnedKey) else { return }
        defaults.set(true, forKey: warnedKey)

        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "ClaudeSwitch is hidden behind the notch"
        alert.informativeText = "The menu bar is too full, so macOS put ClaudeSwitch's item under the camera notch, "
            + "where it cannot be seen. To make room, quit or hide other menu-bar apps, or hold ⌘ and drag "
            + "ClaudeSwitch's item to the right of the notch. Or move it now: it reopens near the right edge."
        alert.addButton(withTitle: "Move It")
        alert.addButton(withTitle: "Not Now")
        NSApp.activate(ignoringOtherApps: true)
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        // The person asked: their choice now replaces any stored position.
        defaults.set(StatusItemPlacement.defaultPosition, forKey: StatusItemPlacement.preferredPositionKey)
        relaunch()
    }

    /// AppKit reads the position when the item is created, so the app
    /// starts again. Only for an installed bundle; `swift run` just says so.
    static func relaunch() {
        let path = Bundle.main.bundlePath
        guard path.hasSuffix(".app") else {
            log.notice("position stored; it applies at the next launch")
            return
        }
        // Opened only once this process has exited, so two items never
        // stand side by side. The path is an argument, never script text.
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", "while kill -0 \"$1\" 2>/dev/null; do sleep 0.2; done; exec /usr/bin/open \"$2\"",
                       "sh", String(ProcessInfo.processInfo.processIdentifier), path]
        do {
            try p.run()
            NSApp.terminate(nil)
        } catch {
            log.error("could not relaunch: \(error.localizedDescription, privacy: .public)")
        }
    }
}
