import AppKit
import ClaudeSwitchCore
import SwiftUI

/// --render: OC1's folder menus and CC1-CC3's Claude Code pane, from the
/// set's `cli-profile-recent.json`, `cli-claude-show.json` and its
/// `cli-claude-show-<profile>.json` (a binary from before them: a set
/// brings a `usage` error object in their place).
extension Render {
    static func claudeFixtures(_ fx: inout PreviewData, dir: String, data: (String) -> Data?) {
        if let d = data("cli-profile-recent.json") {
            fx.recent = RecentFolders(data: d) ?? (AppError(data: d)?.code == "usage" ? RecentFolders() : nil)
        }
        if let d = data("cli-claude-show.json") {
            if let s = ClaudeSetup(data: d) {
                fx.claudeReports[""] = s
                fx.home = (s.dir as NSString).deletingLastPathComponent
            } else {
                fx.claudeUnsupported = AppError(data: d)?.code == "usage"
            }
        }
        let files = (try? FileManager.default.contentsOfDirectory(atPath: dir)) ?? []
        for f in files where f.hasPrefix("cli-claude-show-") && f.hasSuffix(".json") {
            if let s = data(f).flatMap(ClaudeSetup.init(data:)), let p = s.profile { fx.claudeReports[p] = s }
        }
    }

    /// The pane with Skills and with Plugins unfolded, showing one profile
    /// that is not ~/.claude, the editor on CLAUDE.md and on a settings.json
    /// refused as not valid JSON, and each card's folder menu.
    static func claudeCode(store: Store, login: LoginItem, snap: Snapshot, dark: Bool, out: String) {
        let sfx = dark ? "-dark" : ""
        let tall = CGSize(width: 880, height: 820)
        for kind in [SetupKind.skills, .plugins] {
            save(SettingsView(pane: .claudeCode, renderSidebar: true, claudeUnfolded: [kind]).environmentObject(store)
                    .environmentObject(login),
                 size: tall, dark: dark, to: out + "/settings-claude-code-\(kind.rawValue)\(sfx).png")
        }
        let profiles = store.claude.profiles
        if let p = profiles.first(where: { !$0.base && store.claude.reports[$0.name] != nil }) {
            store.claude.scope = p.name
            save(SettingsView(pane: .claudeCode, renderSidebar: true).environmentObject(store).environmentObject(login),
                 size: CGSize(width: 880, height: 640), dark: dark, to: out + "/settings-claude-code-profile\(sfx).png")
            store.claude.scope = nil
        }
        if let r = store.claude.report(nil) {
            let md = SetupEditRequest(item: .instructions, scope: nil,
                                      target: r.instructions?.target ?? r.dir + "/CLAUDE.md",
                                      readers: SetupText.readers(.instructions, scope: nil, profiles: profiles), linked: false)
            save(SetupEditorSheet(request: md, renderText: Self.claudeMD).environmentObject(store),
                 size: nil, dark: dark, to: out + "/sheet-edit-claude-md\(sfx).png")
            let js = SetupEditRequest(item: .settings, scope: nil, target: r.settings?.target ?? r.dir + "/settings.json",
                                      readers: SetupText.readers(.settings, scope: nil, profiles: profiles), linked: false)
            save(SetupEditorSheet(request: js, error: Self.settingsError, renderText: Self.settingsJSON)
                    .environmentObject(store),
                 size: nil, dark: dark, to: out + "/sheet-edit-settings-json-error\(sfx).png")
        }
        for c in snap.cards {
            save(FolderMenuFacsimile(profile: c.name).environmentObject(store), size: nil, dark: dark,
                 to: out + "/menu-folders-\(c.name)\(sfx).png")
        }
    }

    /// Invented text for the editor frames.
    static let claudeMD = """
        # How I work

        - Write the failing test first, then the fix.
        - Keep commits small; one idea each.
        - Never push to main; open a pull request.

        ## This machine

        - Projects live in ~/code and ~/clients.
        - Use the staging database for anything that writes.
        """

    /// A settings.json with a trailing comma, as `claude write` refuses it.
    static let settingsJSON = """
        {
          "model": "opus",
          "permissions": {
            "allow": ["Bash(git status)", "Bash(swift test)"],
            "deny": ["Read(./.env)"]
          },
          "statusLine": {"type": "command", "command": "claudeswitch statusline"},
        }
        """

    static let settingsError = AppError(
        code: "invalid_value",
        message: "settings.json is not valid JSON: invalid character '}' looking for beginning of object key string "
            + "(line 8, column 1)")
}
