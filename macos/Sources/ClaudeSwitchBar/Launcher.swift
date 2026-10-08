import AppKit
import ClaudeSwitchCore
import Foundation

/// Starts terminals and browsers. Blocking where it runs a program: call it
/// off the main thread.
enum Launcher {
    /// Carries out a TerminalLauncher plan: writes its script when it has one
    /// (in a directory only this user can read), then runs the program with
    /// its arguments, no shell in between.
    static func run(_ plan: LaunchPlan, scriptDir: String, scriptPath: String) -> Result<Bool, CallError> {
        if let script = plan.script {
            do {
                try FileManager.default.createDirectory(atPath: scriptDir, withIntermediateDirectories: true,
                                                        attributes: [.posixPermissions: 0o700])
                try script.write(toFile: scriptPath, atomically: true, encoding: .utf8)
                try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: scriptPath)
            } catch {
                return .failure(.cli(.failed("Could not write \(scriptPath): \(error.localizedDescription)")))
            }
        }
        let r = ProcessRunner().run(plan.executable, plan.arguments, timeout: 30)
        if r.timedOut { return .failure(.cli(.failed("The terminal did not start in time."))) }
        guard r.exitCode == 0 else {
            return .failure(.cli(.failed(CLI.tidy(r.stderrText, fallback: "exit status \(r.exitCode)"))))
        }
        return .success(true)
    }

    /// The apps that open https links, the default first.
    @MainActor
    static func browsers() -> [Browser] {
        guard let probe = URL(string: "https://example.com") else { return [] }
        let ws = NSWorkspace.shared
        let def = ws.urlForApplication(toOpen: probe)
        var urls = ws.urlsForApplications(toOpen: probe)
        if let d = def { urls.removeAll { $0 == d }; urls.insert(d, at: 0) }
        var seen = Set<String>()
        return urls.compactMap { u in
            let name = FileManager.default.displayName(atPath: u.path).replacingOccurrences(of: ".app", with: "")
            guard !seen.contains(name) else { return nil }
            seen.insert(name)
            return Browser(name: name, url: u)
        }
    }

    /// Opens an https URL in the chosen browser (the default when nil).
    @MainActor
    static func open(_ url: URL, in browser: Browser?) {
        guard url.scheme?.lowercased() == "https" else { return }
        if let b = browser {
            NSWorkspace.shared.open([url], withApplicationAt: b.url, configuration: NSWorkspace.OpenConfiguration())
        } else {
            NSWorkspace.shared.open(url)
        }
    }
}

struct Browser: Hashable, Identifiable {
    var id: String { name }
    var name: String
    var url: URL
}
