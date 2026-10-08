import Foundation

/// The terminal "Open Claude Code" runs `claudeswitch run <profile>` in.
public enum TerminalApp: String, CaseIterable, Identifiable {
    case terminal = "Terminal"
    case iterm = "iTerm"
    case ghostty = "Ghostty"
    case warp = "Warp"

    public var id: String { rawValue }
    public static let `default` = TerminalApp.terminal
}

/// How to start a terminal running one command.
public struct LaunchPlan: Equatable {
    /// Run with these arguments (no shell in between).
    public var executable: String
    public var arguments: [String]
    /// When set, written first to the script path (mode 0700) the plan was
    /// made with.
    public var script: String?
}

/// Builds the launch of a terminal running an argument vector, without ever
/// splicing that vector into AppleScript or an unquoted shell line:
///
/// - Terminal and iTerm: a fixed AppleScript that takes the command line as
///   its argument (`on run argv`), the command line itself shell-quoted
///   word by word, since both terminals hand it to a shell.
/// - Ghostty: `open -na Ghostty --args -e <argv…>`, the vector as is.
/// - Warp: no way to pass a command, so a one-line script file it opens.
public enum TerminalLauncher {
    /// `claudeswitch run <profile>`, or nil when the profile name could be
    /// read as a flag.
    public static func runArgv(binary: String, profile: String) -> [String]? {
        guard CLI.validID(profile) else { return nil }
        return [binary, "run", profile]
    }

    /// One word for a POSIX shell: single quotes, with each ' as '\''.
    public static func shellQuote(_ s: String) -> String {
        "'" + s.replacingOccurrences(of: "'", with: #"'\''"#) + "'"
    }

    public static func commandLine(_ argv: [String]) -> String {
        argv.map(shellQuote).joined(separator: " ")
    }

    public static func plan(_ app: TerminalApp, argv: [String], scriptPath: String) -> LaunchPlan {
        let line = commandLine(argv)
        switch app {
        case .terminal:
            return osascript([
                "on run argv",
                "tell application \"Terminal\"",
                "activate",
                "do script (item 1 of argv)",
                "end tell",
                "end run",
            ], line)
        case .iterm:
            return osascript([
                "on run argv",
                "tell application \"iTerm\"",
                "activate",
                "set w to (create window with default profile)",
                "tell current session of w to write text (item 1 of argv)",
                "end tell",
                "end run",
            ], line)
        case .ghostty:
            return LaunchPlan(executable: "/usr/bin/open", arguments: ["-na", "Ghostty", "--args", "-e"] + argv,
                              script: nil)
        case .warp:
            return LaunchPlan(executable: "/usr/bin/open", arguments: ["-a", "Warp", scriptPath],
                              script: "#!/bin/sh\nexec \(line)\n")
        }
    }

    static func osascript(_ lines: [String], _ arg: String) -> LaunchPlan {
        LaunchPlan(executable: "/usr/bin/osascript", arguments: lines.flatMap { ["-e", $0] } + [arg], script: nil)
    }
}
