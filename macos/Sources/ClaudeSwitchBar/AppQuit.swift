import AppKit

/// Quitting with a sheet or dialog open. An open sheet refuses the quit and
/// nothing says so (seen in 0.8.11, when an update could not reopen the
/// app), so every way out closes them first, innermost first: a sheet can
/// carry a sheet, an alert on an editor.
@MainActor
enum AppQuit {
    static func closeSheets() {
        for _ in 0..<4 {
            let open = NSApp.windows.compactMap { w in w.attachedSheet.map { (w, $0) } }
            if open.isEmpty { break }
            for (w, sheet) in open where sheet.attachedSheet == nil { w.endSheet(sheet) }
        }
        if NSApp.modalWindow != nil { NSApp.abortModal() }
    }

    /// Quit ClaudeSwitch, from any of its Quit buttons.
    static func quit() {
        closeSheets()
        NSApp.terminate(nil)
    }
}
