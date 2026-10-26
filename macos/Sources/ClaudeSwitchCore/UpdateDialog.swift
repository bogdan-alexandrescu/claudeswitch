import Foundation

// The update window (0.8.9, owner request 2026-10-25): what Check for
// updates found, the offer with what is new, the install's progress bar,
// and how it ended. The words and the numbers live here, where they are
// tested; the Bar only draws them.

/// Where an install is, for its progress bar.
public enum UpdateStep: Equatable {
    case downloading(received: Int64, total: Int64)
    case verifying
    case installingCLI
    case installingApp
    case restartingDaemon
    case relaunching

    /// How far along, 0…1. The downloads take most of the bar: everything
    /// after them is quick.
    public var fraction: Double {
        switch self {
        case .downloading(let got, let total):
            return total > 0 ? 0.85 * min(1, Double(got) / Double(total)) : 0
        case .verifying: return 0.88
        case .installingCLI: return 0.91
        case .installingApp: return 0.94
        case .restartingDaemon: return 0.97
        case .relaunching: return 1
        }
    }

    public var label: String {
        switch self {
        case .downloading(let got, let total):
            return total > 0 ? "Downloading… \(Format.bytes(got)) of \(Format.bytes(total))" : "Downloading…"
        case .verifying: return "Checking the downloads against the release's checksums…"
        case .installingCLI: return "Installing claudeswitch…"
        case .installingApp: return "Installing ClaudeSwitch…"
        case .restartingDaemon: return "Restarting the daemon on the new claudeswitch…"
        case .relaunching: return "Reopening ClaudeSwitch…"
        }
    }

    /// Whether Cancel still leaves everything as it was.
    public var cancellable: Bool {
        switch self {
        case .downloading, .verifying: return true
        default: return false
        }
    }

    var kind: Int {
        switch self {
        case .downloading: return 0
        case .verifying: return 1
        case .installingCLI: return 2
        case .installingApp: return 3
        case .restartingDaemon: return 4
        case .relaunching: return 5
        }
    }
}

/// Which progress reports are worth a redraw: a new step, the end of a
/// download, or at least one percent more. A download reports every few
/// kilobytes, from URLSession's queue.
public final class UpdateProgressGate: @unchecked Sendable {
    private var last: UpdateStep?
    private let lock = NSLock()

    public init() {}

    public func pass(_ step: UpdateStep) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        var show = true
        if let l = last, l.kind == step.kind {
            show = step.fraction - l.fraction >= 0.01
            if case .downloading(let got, let total) = step, got == total, l != step { show = true }
        }
        if show { last = step }
        return show
    }
}

extension Release {
    /// What the release says is new, ready for an inline Markdown Text:
    /// headings in bold, list items as bullets. Without GitHub's own
    /// "Full Changelog" line or a first line that only repeats the tag; nil
    /// when nothing is left.
    public var whatsNew: String? {
        var lines: [String] = []
        for (i, raw) in notes.components(separatedBy: .newlines).enumerated() {
            var line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("**Full Changelog**") { continue }
            if i == 0, line == tag || SemVer(line).map({ $0 == version }) == true { continue }
            if line.hasPrefix("#") {
                line = line.drop(while: { $0 == "#" }).trimmingCharacters(in: .whitespaces)
                if !line.isEmpty { line = "**\(line)**" }
            } else if line.hasPrefix("- ") || line.hasPrefix("* ") {
                line = "• " + line.dropFirst(2)
            }
            lines.append(line)
        }
        while lines.first?.isEmpty == true { lines.removeFirst() }
        while lines.last?.isEmpty == true { lines.removeLast() }
        return lines.isEmpty ? nil : lines.joined(separator: "\n")
    }
}

/// The update window's state.
public enum UpdateDialog: Equatable {
    case checking
    case upToDate(current: String, latest: String)
    case checkFailed(String)
    case available(version: String, current: String, notes: String?, bytes: Int64, page: URL?)
    case installing(version: String, step: UpdateStep)
    case failed(version: String, message: String)
    case installed(version: String)

    /// What a check found, for the window.
    public static func checked(_ r: Result<Release, UpdateError>, current: String?, arch: UpdateArch) -> UpdateDialog {
        switch r {
        case .failure(let e):
            return .checkFailed(e.message)
        case .success(let release):
            let latest = release.version?.description ?? release.tag
            guard let v = UpdateOffer.available(current: current, latest: release) else {
                return .upToDate(current: current ?? latest, latest: latest)
            }
            let bytes = (try? release.plan(arch: arch).get()).map { Int64(($0.tarball.size ?? 0) + ($0.appZip.size ?? 0)) }
            return .available(version: v, current: current ?? "", notes: release.whatsNew, bytes: bytes ?? 0,
                              page: release.htmlURL)
        }
    }

    public var title: String {
        switch self {
        case .checking: return "Checking for updates…"
        case .upToDate: return "ClaudeSwitch is up to date"
        case .checkFailed: return "Could not check for updates"
        case .available(let v, _, _, _, _): return "ClaudeSwitch \(v) is available"
        case .installing(let v, _): return "Updating to \(v)"
        case .failed: return "The update did not install"
        case .installed(let v): return "ClaudeSwitch is now \(v)"
        }
    }

    public var message: String {
        switch self {
        case .checking:
            return "Asking GitHub for the latest release."
        case .upToDate(let current, let latest):
            if let c = SemVer(current), let l = SemVer(latest), c > l {
                return "This is \(current), newer than the latest release, \(latest)."
            }
            return "\(current) is the latest version."
        case .checkFailed(let m), .failed(_, let m):
            return m
        case .available(_, let current, _, _, _):
            return "You have \(current). Installing checks the app and the claudeswitch command against the "
                + "release's checksums, replaces both, restarts the daemon on the new claudeswitch and reopens "
                + "ClaudeSwitch."
        case .installing(_, let step):
            return step.label
        case .installed:
            return "The claudeswitch command was updated with it, and the daemon now runs the new one."
        }
    }

    /// The small print under an offer.
    public var detail: String? {
        if case .available(_, _, _, let bytes, _) = self, bytes > 0 { return "Downloads \(Format.bytes(bytes)) from GitHub" }
        return nil
    }
}
