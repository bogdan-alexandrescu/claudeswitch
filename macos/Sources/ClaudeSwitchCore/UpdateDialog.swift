import Foundation

// The update window (0.8.9, owner request 2026-10-25): what Check for
// updates found, the offer with what is new, the install's progress bar,
// and how it ended. The words and the numbers live here, where they are
// tested; the Bar only draws them.

/// Where an install is, for its progress bar.
public enum UpdateStep: Equatable, Sendable {
    case downloading(received: Int64, total: Int64)
    case verifying
    case installingCLI
    case installingApp
    case restartingDaemon
    case relaunching

    /// How full the bar is, 0…1. While downloading, the download's own
    /// share, so the bar agrees with the figure beside it (it was the whole
    /// install's, a third full beside "38%", second-pass QA 2026-10-28);
    /// then a fifth for each quick step after it.
    public var fraction: Double {
        switch self {
        case .downloading(let got, let total):
            return total > 0 ? min(1, max(0, Double(got) / Double(total))) : 0
        case .verifying: return 0.2
        case .installingCLI: return 0.4
        case .installingApp: return 0.6
        case .restartingDaemon: return 0.8
        case .relaunching: return 1
        }
    }

    public var label: String {
        switch self {
        case .downloading(let got, let total):
            return total > 0 ? "Downloading… \(Self.size(got)) of \(Self.size(total))" : "Downloading…"
        case .verifying: return "Checking the downloads against the release’s checksums…"
        case .installingCLI: return "Installing claudeswitch…"
        case .installingApp: return "Installing ClaudeSwitch…"
        case .restartingDaemon: return "Restarting the daemon on the new claudeswitch…"
        case .relaunching: return "Reopening ClaudeSwitch…"
        }
    }

    /// The figure beside the bar: the download's own share, as the bar is,
    /// so it agrees with "4.2 MB of 10.8 MB". 100% only once it is all
    /// there; none for the quick steps after the downloads.
    public var percent: String? {
        guard case .downloading(let got, let total) = self, total > 0 else { return nil }
        return "\(min(100, max(0, got) * 100 / total))%"
    }

    /// Whether Cancel still leaves everything as it was.
    public var cancellable: Bool {
        switch self {
        case .downloading, .verifying: return true
        default: return false
        }
    }

    /// The line under the bar once Cancel is greyed, which said why only in
    /// its tooltip.
    public var note: String? {
        switch self {
        case .downloading, .verifying: return nil
        case .installingCLI, .installingApp: return "Too late to cancel: the new files are going in."
        case .restartingDaemon, .relaunching: return "Too late to cancel: the new files are in."
        }
    }

    /// A download's size: tenths of a megabyte under 100 MB ("4.2 MB of
    /// 10.8 MB"), where whole megabytes hid the progress.
    static func size(_ n: Int64) -> String {
        let mb: Int64 = 1 << 20
        guard n >= mb, n < 100 * mb else { return Format.bytes(n) }
        return String(format: "%.1f MB", Double(n) / Double(mb))
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

/// One line of What's new: a bullet's text without its "• ", so the window
/// can hang its wrapped lines under the text rather than the bullet.
public struct UpdateNoteLine: Equatable, Sendable {
    public var text: String
    public var bullet: Bool

    public init(text: String, bullet: Bool) {
        self.text = text
        self.bullet = bullet
    }
}

public enum UpdateNotes {
    /// What's new (Release.whatsNew), line by line.
    public static func lines(_ notes: String) -> [UpdateNoteLine] {
        notes.components(separatedBy: "\n").map { line in
            line.hasPrefix("• ") ? UpdateNoteLine(text: String(line.dropFirst(2)), bullet: true)
                : UpdateNoteLine(text: line, bullet: false)
        }
    }
}

/// The update window's state.
public enum UpdateDialog: Equatable, Sendable {
    case checking
    /// Check for updates while Advanced → Check for updates is off: no call
    /// is made, so the window says it is off rather than checking forever.
    case checkOff
    case upToDate(current: String, latest: String)
    /// The check read the release, but this copy has no version to compare
    /// it with (swift run): not "up to date".
    case versionUnknown(latest: String)
    case checkFailed(String)
    case available(version: String, current: String, notes: String?, bytes: Int64, page: URL?)
    case installing(version: String, step: UpdateStep)
    case failed(version: String, message: String)
    case installed(version: String)

    /// What Check for updates opens the window on: an install under way
    /// stays in view; with the check off, that it is off, since no call is
    /// made then; otherwise the check.
    public static func checkNow(enabled: Bool, showing: UpdateDialog?) -> UpdateDialog {
        if case .installing? = showing, let d = showing { return d }
        return enabled ? .checking : .checkOff
    }

    /// What a check found, for the window.
    public static func checked(_ r: Result<Release, UpdateError>, current: String?, arch: UpdateArch) -> UpdateDialog {
        switch r {
        case .failure(let e):
            return .checkFailed(e.message)
        case .success(let release):
            let latest = release.version?.description ?? release.tag
            guard current.flatMap(SemVer.init) != nil else { return .versionUnknown(latest: latest) }
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
        case .checkOff: return "Checking for updates is off"
        case .upToDate: return "ClaudeSwitch is up to date"
        case .versionUnknown: return "This copy’s version is unknown"
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
            return "Asking GitHub for the latest release…"
        case .checkOff:
            // Not the title again, nor "off makes no call at all".
            return "While it is off, the app never contacts GitHub. Turn On checks now, then once a day."
        case .upToDate(let current, let latest):
            if let c = SemVer(current), let l = SemVer(latest), c > l {
                return "This is \(current), newer than the latest release, \(latest)."
            }
            return "\(current) is the latest version."
        case .versionUnknown(let latest):
            return "\(latest) is the latest release. This copy has no version of its own (a build from swift run), "
                + "so there is nothing to compare it with."
        case .checkFailed(let m), .failed(_, let m):
            return m
        case .available(_, let current, _, _, _):
            return "You have \(current). Installing checks the app and the claudeswitch command against the "
                + "release’s checksums, replaces both, restarts the daemon on the new claudeswitch and reopens "
                + "ClaudeSwitch."
        case .installing(_, let step):
            return step.label
        case .installed:
            return "The claudeswitch command was updated with it, and the daemon now runs the new one."
        }
    }

    /// The link under the text: the offer's release notes, and the failed
    /// release's page, a way out when Try Again fails the same way (a
    /// checksum mismatch).
    public var page: URL? {
        switch self {
        case .available(_, _, _, _, let page): return page
        case .failed(let version, _): return UpdateSource.releasePage(version)
        default: return nil
        }
    }

    /// The link's name. After a failure it is the page the message sends
    /// you to, to download the release by hand: "Release Notes on GitHub"
    /// did not read as a way out (second-pass QA 2026-10-28).
    public var pageLabel: String? {
        switch self {
        case .available: return "Release Notes on GitHub"
        case .failed: return "Release page on GitHub"
        default: return nil
        }
    }

    /// The small print under an offer.
    public var detail: String? {
        guard case .available(_, _, _, let bytes, _) = self, bytes > 0 else { return nil }
        return "Downloads \(UpdateStep.size(bytes)) from GitHub"
    }
}

/// Reopening the app after an install. The app asks to quit, but a sheet
/// or dialog left open can refuse that: a Settings sheet kept 0.8.10
/// running after 0.8.11 was installed, and opening the bundle only brought
/// the old process forward (2026-10-26). So a helper waits for this process
/// to go, ends it when it has not after `grace` seconds (SIGKILL three
/// seconds later), then opens the new bundle; it gives up waiting after six
/// seconds more and opens it anyway.
public enum Relaunch {
    /// The helper's argument vector: /bin/sh with the script, then the
    /// bundle ($0), this process's pid ($1) and the opener ($2).
    public static func helper(app: String, pid: Int32, opener: String = "/usr/bin/open",
                              grace: Double = 5) -> [String] {
        let term = max(1, Int(grace * 10)), kill9 = term + 30, giveUp = term + 60
        let script = """
            i=0
            while kill -0 "$1" 2>/dev/null; do
              i=$((i+1))
              [ "$i" -eq \(term) ] && kill "$1" 2>/dev/null
              [ "$i" -eq \(kill9) ] && kill -9 "$1" 2>/dev/null
              [ "$i" -ge \(giveUp) ] && break
              sleep 0.1
            done
            exec "$2" "$0"
            """
        return ["/bin/sh", "-c", script, app, String(pid), opener]
    }
}

/// Where a window sits in the middle of a screen's visible area, in whole
/// points (owner request 2026-10-26). A window taller than the area keeps
/// its title bar at the top, one wider keeps its left edge on screen.
public enum ScreenCentre {
    public static func origin(_ size: CGSize, in area: CGRect) -> CGPoint {
        var p = origin(size, around: CGPoint(x: area.midX, y: area.midY))
        p.y = min(p.y, area.maxY - size.height)
        p.x = max(p.x, area.minX)
        return p
    }

    /// The origin that puts the window's middle at `middle`.
    public static func origin(_ size: CGSize, around middle: CGPoint) -> CGPoint {
        CGPoint(x: (middle.x - size.width / 2).rounded(), y: (middle.y - size.height / 2).rounded())
    }
}

