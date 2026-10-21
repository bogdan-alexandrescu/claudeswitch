import Foundation

/// What the app's Rename did. Renaming needs the daemon stopped: it owns the
/// state file the rename rewrites, and `account rename` refuses while one
/// runs. So the app stops a running daemon service, renames, and starts it
/// again (owner decision, 2026-10-21). Start loads the same service file, so
/// the daemon comes back in the mode it had, live or dry run.
public struct PausedRename: Equatable {
    public var renamed: RenameResult
    /// A running daemon was stopped for the rename and started again.
    public var paused: Bool
    /// Starting it again failed: the account is renamed and the daemon is down.
    public var restartError: CallError?
}

enum RenameFlow {
    static func run(status: () -> Result<DaemonStatus, CallError>,
                    stop: () -> Result<DaemonStatus, CallError>,
                    rename: () -> Result<RenameResult, CallError>,
                    start: () -> Result<DaemonStatus, CallError>) -> Result<PausedRename, CallError> {
        // No service, none running, or no answer: rename as asked. The CLI
        // still refuses if a daemon runs after all.
        guard case .success(let s) = status(), s.installed, s.running else {
            return rename().map { PausedRename(renamed: $0, paused: false, restartError: nil) }
        }
        switch stop() {
        case .failure(let e):
            return .failure(e)
        case .success(let after) where after.running:
            // The service is stopped and a daemon still runs: one started
            // outside it. Put the service back and rename nothing.
            _ = start()
            return .failure(.refused("daemon_running",
                                     "A daemon that the service did not start is running, so nothing was renamed.",
                                     hint: "Stop it (a `claudeswitch watch` in a terminal?), then rename."))
        case .success:
            break
        }
        let renamed = rename()
        let started = start()
        switch (renamed, started) {
        case (.success(let r), .success):
            return .success(PausedRename(renamed: r, paused: true, restartError: nil))
        case (.success(let r), .failure(let e)):
            return .success(PausedRename(renamed: r, paused: true, restartError: e))
        case (.failure(let e), .success):
            return .failure(e)
        case (.failure(let e), .failure(let down)):
            return .failure(.refused(e.code ?? "failed",
                                     e.message + " The daemon was stopped for the rename and not started again: "
                                     + down.message,
                                     hint: "Start it from the Daemon pane."))
        }
    }
}

public extension CLI {
    /// `account rename`, with a running daemon stopped for it and started
    /// again after (RenameFlow).
    func accountRenamePausingDaemon(_ old: String, to new: String) -> Result<PausedRename, CallError> {
        RenameFlow.run(status: { daemon(.status) }, stop: { daemon(.stop) },
                       rename: { accountRename(old, to: new) }, start: { daemon(.start) })
    }
}
