import AppKit
import ClaudeSwitchCore
import SwiftUI

// OC1 A (the owner's pick, 2026-10-28): Open Claude Code starts in the
// profile's last folder, named on the button; its folder line, or a
// right-click, opens the profile's recent folders, Choose Folder… and Home
// Folder.

/// The open card's Open Claude Code: "Open Claude Code" over "in
/// claudeswitch ⌄". A click opens Claude Code in that folder; the folder line
/// is a menu of the profile's folders, and a right-click shows it too.
struct OpenClaudeCodePill: View {
    @EnvironmentObject var store: Store
    let card: ProfileCard

    /// As before OC1: greyed with no binary, for a profile not signed in,
    /// and while one opens.
    var disabled: Bool { store.binaryPath == nil || !card.canOpen || store.isBusy("run:\(card.name)") }

    var body: some View {
        let pick = store.lastFolder(card.name)
        Button {
            store.openClaudeCode(card.name, in: pick?.path)
        } label: {
            VStack(spacing: 0) {
                Text("Open Claude Code").lineLimit(1)
                HStack(spacing: 3) {
                    Text(FolderText.buttonLine(pick, home: store.claude.home)).lineLimit(1).truncationMode(.middle)
                    Image(systemName: "chevron.down").font(.system(size: 8, weight: .bold))
                }
                .font(.caption)
            }
            .frame(maxWidth: .infinity, minHeight: 32)
        }
        .buttonStyle(PillButtonStyle(primary: false))
        // The folder line is a real menu, laid over the line: a click there
        // chooses, anywhere else on the pill opens. A button-style menu with
        // a plain button style is hit across its whole label; an AppKit
        // (borderless) one shrank to its 11 × 14 pt popup button, so a click
        // beside it would have opened Claude Code instead.
        .overlay(alignment: .bottom) {
            Menu {
                FolderMenuItems(profile: card.name)
            } label: {
                Color.clear.frame(maxWidth: .infinity).frame(height: FolderLine.height).contentShape(Rectangle())
            }
            .menuStyle(.button)
            .buttonStyle(.plain)
            .menuIndicator(.hidden)
            .padding(.horizontal, 12)
            .padding(.bottom, 4)
            .help("Choose the folder Claude Code starts in for \(card.name)")
            .accessibilityLabel("Choose the folder for \(card.name)")
        }
        .contextMenu { FolderMenuItems(profile: card.name) }
        .disabled(disabled)
        .help(card.openBlocked ?? FolderText.help(profile: card.name, pick: pick, home: store.claude.home))
    }
}

enum FolderLine {
    /// The folder line's band at the pill's foot: its caption line and the
    /// pill's bottom padding, less a little.
    static let height: CGFloat = 18
}

/// The folder menu's items: the profile's recent folders (the current one
/// checked), then Choose Folder… and Home Folder. Choosing one opens Claude
/// Code there and remembers it. FolderMenuFacsimile draws the same lines.
struct FolderMenuItems: View {
    @EnvironmentObject var store: Store
    let profile: String

    var body: some View {
        let entries = store.folderEntries(profile)
        Text(FolderText.header(profile))
        if entries.isEmpty { Text(FolderText.empty(profile)) }
        ForEach(entries) { e in
            // A Toggle puts the check in the menu's gutter, so every name
            // starts in the same place.
            Toggle(isOn: Binding(get: { e.checked }, set: { _ in store.openClaudeCode(profile, choosing: e.path) })) {
                Text(e.line)
            }
        }
        Divider()
        Button(FolderText.chooseItem) { store.chooseFolder(for: profile) }
        Button(FolderText.homeItem) { store.openClaudeCode(profile, choosing: store.claude.home) }
    }
}

/// The folder menu, drawn for review: a menu cannot be drawn off screen, so
/// this lays out FolderMenuItems' lines as the menu has them: the header and
/// the empty line as disabled items, the current folder checked in the
/// gutter. Render only.
struct FolderMenuFacsimile: View {
    @EnvironmentObject var store: Store
    let profile: String

    var body: some View {
        let entries = store.folderEntries(profile)
        VStack(alignment: .leading, spacing: 0) {
            line(FolderText.header(profile), dim: true)
            if entries.isEmpty { line(FolderText.empty(profile), dim: true) }
            ForEach(entries) { e in line(e.line, checked: e.checked) }
            Divider().padding(.horizontal, 10).padding(.vertical, 5)
            line(FolderText.chooseItem)
            line(FolderText.homeItem)
        }
        .padding(5)
        .frame(minWidth: 260, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.15)))
        .padding(18)
        .accessibilityHidden(true)
    }

    /// One item: one line that never wraps, as a menu item.
    func line(_ text: String, checked: Bool = false, dim: Bool = false) -> some View {
        HStack(spacing: 0) {
            Image(systemName: "checkmark").font(.system(size: 11, weight: .semibold))
                .opacity(checked ? 1 : 0).frame(width: 18)
            Text(text).lineLimit(1).fixedSize()
        }
        .font(.system(size: 13))
        .foregroundStyle(dim ? Color.secondary : Color.primary)
        .padding(.horizontal, 6).frame(height: 22, alignment: .leading)
    }
}
