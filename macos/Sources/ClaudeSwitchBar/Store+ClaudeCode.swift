import AppKit
@preconcurrency import ClaudeSwitchCore
import Foundation

/// OC1 and CC1-CC3 (the owner's picks, 2026-10-28): each profile's recent
/// folders and the one Open Claude Code starts in, and what Claude Code
/// reads, every profile's or one profile's. Each is read on demand and
/// degrades on an older binary.
struct ClaudeCodeState {
    /// UserDefaults: the folder chosen in the app per profile, and when.
    static let rememberedKey = "claudeCodeFolders"

    /// `profile recent --json`; nil until read. An older binary gives none.
    var recent: RecentFolders?
    var remembered: [String: RememberedFolder] = [:]
    /// `claude show --json` under "", `claude show --profile p --json`
    /// under p.
    var reports: [String: ClaudeSetup] = [:]
    var errors: [String: CallError] = [:]
    /// The binary predates `claude show`.
    var unsupported = false
    /// The Claude Code pane's Showing pick (nil: every profile), kept for
    /// this session only.
    var scope: String?
    /// The home the paths are abbreviated against (~).
    var home = NSHomeDirectory()
    /// The newest read asked for, per scope: an older answer is not applied
    /// over a newer one.
    var reads: [String: Int] = [:]

    static func key(_ scope: String?) -> String { scope ?? "" }
    func report(_ scope: String?) -> ClaudeSetup? { reports[Self.key(scope)] }
    func error(_ scope: String?) -> CallError? { errors[Self.key(scope)] }
    /// Every profile, from `claude show --json`.
    var profiles: [ProfileSetup] { reports[""]?.profiles ?? [] }
}

extension Store {
    // MARK: OC1 where Open Claude Code starts

    /// One `profile recent --json` read: when the popover opens and when
    /// Settings → Profiles shows. A binary without the command has no
    /// recent folders, silently; another failure keeps what was read.
    func loadRecentFolders() {
        guard !preview, !isBusy("load:recent") else { return }
        Task {
            switch await call("load:recent", { $0.profileRecent() }) {
            case .success(let r): claude.recent = r
            case .failure(let e) where SetupSupport.unsupported(e): claude.recent = RecentFolders()
            case .failure: break
            }
        }
    }

    /// The folder Open Claude Code starts in; nil: the home folder.
    func lastFolder(_ profile: String) -> FolderPick? {
        LastFolder.pick(remembered: claude.remembered[profile], recent: claude.recent?.folders(profile) ?? [],
                        exists: { [weak self] in self?.folderExists($0) ?? false })
    }

    func folderEntries(_ profile: String) -> [FolderMenuEntry] {
        FolderMenu.entries(recent: claude.recent?.folders(profile) ?? [], current: lastFolder(profile),
                           home: claude.home, now: now)
    }

    /// Whether a chosen folder is still there. Not asked in the folders
    /// macOS guards (Desktop, Documents, …): even that check can prompt,
    /// and the terminal says so if it is gone.
    private func folderExists(_ path: String) -> Bool {
        if preview || ProtectedFolders.contains(path, home: claude.home) { return true }
        var dir: ObjCBool = false
        return FileManager.default.fileExists(atPath: path, isDirectory: &dir) && dir.boolValue
    }

    /// The pill, the collapsed card's ▶ and Settings' button: the last folder.
    func openInLastFolder(_ profile: String) {
        openClaudeCode(profile, in: lastFolder(profile)?.path)
    }

    /// A folder chosen from the menu: opened there, and remembered.
    func openClaudeCode(_ profile: String, choosing folder: String) {
        remember(folder, for: profile)
        openClaudeCode(profile, in: folder)
    }

    func remember(_ path: String, for profile: String) {
        claude.remembered[profile] = RememberedFolder(path: path, at: Date())
        guard !preview else { return }
        UserDefaults.standard.set(RememberedFolder.plist(claude.remembered), forKey: ClaudeCodeState.rememberedKey)
    }

    /// Choose Folder…: a folder, starting at the current one.
    func chooseFolder(for profile: String) {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = true
        panel.prompt = "Open"
        panel.message = "The folder Claude Code starts in for \(profile)"
        panel.directoryURL = URL(fileURLWithPath: lastFolder(profile)?.path ?? claude.home, isDirectory: true)
        // An agent app's panel would open behind the frontmost app.
        NSApp.activate(ignoringOtherApps: true)
        guard panel.runModal() == .OK, let u = panel.url else { return }
        openClaudeCode(profile, choosing: u.path)
    }

    // MARK: CC1-CC3 what Claude Code reads

    /// `claude show [--profile p] --json`: when the Claude Code pane or
    /// Settings → Profiles shows, when Showing changes, and after a change.
    func loadClaudeSetup(_ scope: String? = nil) {
        guard !preview else { return }
        let k = ClaudeCodeState.key(scope)
        let n = (claude.reads[k] ?? 0) + 1
        claude.reads[k] = n
        Task {
            let r = await call("load:claude:" + k, { $0.claudeShow(profile: scope) })
            guard claude.reads[k] == n else { return }
            readSetup(r, scope: scope)
        }
    }

    func readSetup(_ r: Result<ClaudeSetup, CallError>, scope: String?) {
        let k = ClaudeCodeState.key(scope)
        switch r {
        case .success(let s):
            claude.reports[k] = s
            claude.errors[k] = nil
            claude.unsupported = false
        case .failure(let e) where SetupSupport.unsupported(e):
            claude.unsupported = true
            claude.errors[k] = nil
        case .failure(let e):
            claude.errors[k] = e
        }
    }

    /// Every profile's report, and the scope's when one is shown.
    func reloadSetup(_ scope: String?) {
        loadClaudeSetup(nil)
        if scope != nil { loadClaudeSetup(scope) }
    }

    /// Show in Claude Code (Settings → Profiles): the pane, showing `profile`.
    func showClaudeCode(scope profile: String?) {
        claude.scope = profile
        reloadSetup(profile)
        openSettings(.claudeCode)
    }

    /// A plugin's switch: `claude plugin enable|disable`, in the profile
    /// shown (with every profile, the one that is ~/.claude).
    func setPlugin(_ p: PluginEntry, on: Bool, scope: String?) {
        guard case .profile(let flag) = SetupTarget.of(scope: scope, profiles: claude.profiles) else { return }
        act("plugin:\(p.id)", title: SetupText.pluginFailed(p.name, on: on),
            { $0.claudePlugin(p.id, enable: on, profile: flag) },
            failed: { [weak self] _ in self?.reloadSetup(scope) }) { [weak self] r in
            self?.note = SetupText.pluginNote(r, name: p.name)
            self?.reloadSetup(scope)
        }
    }

    /// Make Own Copy, Use Shared… (once confirmed) and Share.
    func runShare(_ action: ShareAction, profile: String, item: SetupItemName) {
        let op: (CLI) -> Result<ShareResult, CallError> = action == .makeOwn
            ? { $0.profileOwn(profile, item) } : { $0.profileShare(profile, item) }
        act("share:\(profile):\(item.rawValue)", title: SetupText.shareFailed(action, profile: profile, item: item), op,
            failed: { [weak self] _ in self?.reloadSetup(profile) }) { [weak self] r in
            self?.note = SetupText.shareNote(r)
            self?.reloadSetup(profile)
        }
    }

    /// The editor's Save: `claude write`, the text on stdin, refused when
    /// the file changed since it was opened. The sheet shows a refusal.
    func writeSetupFile(_ item: SetupItemName, scope: String?, text: String,
                        sha: String) async -> Result<SetupWriteResult, CallError> {
        guard case .profile(let flag) = SetupTarget.of(scope: scope, profiles: claude.profiles) else {
            return .failure(.app(AppError(code: "failed", message: "No profile is ~/.claude, so nothing here can be saved.")))
        }
        let r = await call("write:\(item.rawValue)", { $0.claudeWrite(item, profile: flag, ifUnchanged: sha, text: text) })
        if case .success(let w) = r { note = SetupText.saved(w, home: claude.home) }
        reloadSetup(scope)
        return r
    }

    // MARK: files

    /// Open: the file in the app that opens it, else TextEdit. Reveal: the
    /// folder selected in Finder (its parent opened when it is not there).
    func perform(_ action: SetupFileAction) {
        let ws = NSWorkspace.shared
        switch action {
        case .open(let path):
            let url = URL(fileURLWithPath: path)
            if ws.urlForApplication(toOpen: url) != nil {
                ws.open(url)
            } else if let edit = ws.urlForApplication(withBundleIdentifier: "com.apple.TextEdit") {
                ws.open([url], withApplicationAt: edit, configuration: NSWorkspace.OpenConfiguration())
            }
        case .reveal(let path):
            if FileManager.default.fileExists(atPath: path) {
                ws.activateFileViewerSelecting([URL(fileURLWithPath: path)])
            } else {
                ws.open(URL(fileURLWithPath: (path as NSString).deletingLastPathComponent, isDirectory: true))
            }
        }
    }

    func showInFinder(_ dir: String) {
        NSWorkspace.shared.open(URL(fileURLWithPath: dir, isDirectory: true))
    }

    /// What the editor opens: the file's text and its hash, read here
    /// (read-only); saving goes through the CLI.
    func readEditorFile(_ path: String, item: SetupItemName) -> EditorRead {
        // None yet: an empty one, which Save creates.
        let data: Data?
        if FileManager.default.fileExists(atPath: path) {
            guard let d = FileManager.default.contents(atPath: path) else {
                return .failed("Could not read \(FolderText.abbreviate(path, home: claude.home)).")
            }
            data = d
        } else {
            data = nil
        }
        switch EditorBuffer.load(data) {
        case .success(let b): return .loaded(b)
        case .failure(let e): return .failed(EditorBuffer.loadMessage(e, item: item))
        }
    }
}

enum EditorRead {
    case loaded(EditorBuffer)
    case failed(String)
}
